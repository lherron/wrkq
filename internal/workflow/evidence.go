//go:build wrkq_local

package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/rpcidem"
)

func (s *Service) AddEvidence(params AddEvidenceParams) (*Evidence, error) {
	if err := s.EnsureBuiltinTemplateForSelectors(params.TaskSelector, params.InstanceID, params.PrincipalRef); err != nil {
		return nil, err
	}
	var ev *Evidence
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		inst, err := resolveInstanceSelectors(tx, params.TaskSelector, params.InstanceID)
		if err != nil {
			return err
		}
		tpl, _, err := showTemplateTx(tx, inst.TemplateID+"@"+inst.TemplateVersion)
		if err != nil {
			return err
		}
		inserted, task, err := insertEvidenceTx(tx, inst, tpl, params, s.now().Format(time.RFC3339))
		if err != nil {
			return err
		}
		ev = inserted
		if task == nil { // idempotent replay: nothing new to reflect
			return nil
		}
		inst.TaskDocEtag = fmt.Sprint(task.ETag)
		inst.TaskDocHash = taskDocHash(task)
		inst.UpdatedAt = inserted.ProducedAt
		if _, err := tx.Exec(`UPDATE workflow_instances SET task_doc_etag = ?, task_doc_hash = ?, updated_at = ? WHERE id = ?`, inst.TaskDocEtag, inst.TaskDocHash, inst.UpdatedAt, inst.ID); err != nil {
			return err
		}
		return updateTaskWorkflowMeta(tx, inst.TaskUUID, *inst, params.PrincipalRef)
	})
	return ev, err
}

// insertEvidenceTx validates params against the template's kind spec and the
// workflow policy, replays an idempotent repeat, and otherwise records the
// evidence at now, runs the policy's side effects and stores the replay
// result. task is the task doc read at production, nil on a replay.
func insertEvidenceTx(tx *sql.Tx, inst *Instance, tpl *Template, params AddEvidenceParams, now string) (*Evidence, *taskDoc, error) {
	policy := ResolveWorkflowPolicy(tpl)
	var kindSpec *KindSpec
	if spec, ok := tpl.EvidenceKinds[params.Kind]; ok {
		kindSpec = &spec
	}
	if err := validateProducibleBy(params.Kind, kindSpec, params.Role); err != nil {
		return nil, nil, err
	}
	facts, err := parseAndValidateEvidenceFacts(params.Kind, params.Facts, kindSpec)
	if err != nil {
		return nil, nil, err
	}
	if err := policy.ValidateEvidence(params, facts); err != nil {
		return nil, nil, err
	}
	var dataArg interface{}
	var dataRaw json.RawMessage
	if strings.TrimSpace(params.Data) != "" {
		if !json.Valid([]byte(params.Data)) {
			return nil, nil, validationError("data", "data must be valid JSON"+jsonLocationSuffix(json.Unmarshal([]byte(params.Data), new(json.RawMessage))), "valid JSON", nil, "fix the JSON syntax in --data")
		}
		dataArg = params.Data
		dataRaw = json.RawMessage(params.Data)
	}
	var factsRaw json.RawMessage
	if facts != nil {
		factsRaw = facts.Raw
	}
	requestHash := ""
	if params.IdempotencyKey != "" {
		requestHash = evidenceAddRequestHash(params, factsRaw, dataRaw)
		replayed, err := replayEvidenceResult(tx, inst.ID, params.IdempotencyKey, requestHash)
		if err != nil {
			return nil, nil, err
		}
		if replayed != nil {
			return replayed, nil, nil
		}
	}
	task, err := loadTaskDoc(tx, inst.TaskUUID)
	if err != nil {
		return nil, nil, err
	}
	if kindSpec != nil && len(kindSpec.LinkageRefs) > 0 {
		existing, err := listInstanceEvidence(tx, inst.ID)
		if err != nil {
			return nil, nil, err
		}
		if err := validateLinkageRefs(existing, kindSpec, dataRaw); err != nil {
			return nil, nil, err
		}
	}
	id, err := nextSeqID(tx, "workflow_evidence_seq", "ev")
	if err != nil {
		return nil, nil, err
	}
	taskHashAtProduction := taskDocHash(task)
	source := map[string]interface{}{"type": "external_ref", "ref": params.Ref, "taskHashAtProduction": taskHashAtProduction}
	if len(dataRaw) > 0 {
		source["dataHash"] = Hash(dataRaw)
	}
	if strings.TrimSpace(params.ContentHash) != "" {
		source["contentHash"] = strings.TrimSpace(params.ContentHash)
	}
	if params.Build != nil {
		build := map[string]string{}
		if strings.TrimSpace(params.Build.ID) != "" {
			build["id"] = strings.TrimSpace(params.Build.ID)
		}
		if strings.TrimSpace(params.Build.Version) != "" {
			build["version"] = strings.TrimSpace(params.Build.Version)
		}
		if strings.TrimSpace(params.Build.Env) != "" {
			build["env"] = strings.TrimSpace(params.Build.Env)
		}
		if len(build) > 0 {
			source["build"] = build
		}
	}
	sourceJSON, _ := json.Marshal(source)
	var factsArg interface{}
	if facts != nil {
		factsArg = string(facts.Raw)
	}
	_, err = tx.Exec(`
		INSERT INTO workflow_evidence (id, instance_id, kind, ref, summary, facts_json, data_json, source_json, actor, principal_ref, role, run_id, task_etag_at_production, task_hash_at_production, produced_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, inst.ID, params.Kind, params.Ref, nullIfEmpty(params.Summary), factsArg, dataArg, string(sourceJSON), emptyToNil(params.PrincipalRef), emptyToNil(params.PrincipalRef), emptyToNil(params.Role), emptyToNil(params.RunID), fmt.Sprint(task.ETag), taskHashAtProduction, now)
	if err != nil {
		return nil, nil, err
	}
	ev := &Evidence{ID: id, InstanceID: inst.ID, Kind: params.Kind, Ref: params.Ref, Summary: params.Summary, Facts: factsRaw, Data: dataRaw, Source: sourceJSON, PrincipalRef: params.PrincipalRef, Role: params.Role, RunID: params.RunID, ContentHash: strings.TrimSpace(params.ContentHash), Build: normalizedEvidenceBuild(params.Build), TaskEtagAtProduction: fmt.Sprint(task.ETag), TaskHashAtProduction: taskHashAtProduction, ProducedAt: now}
	if err := policy.OnEvidenceAdded(tx, inst, ev); err != nil {
		return nil, nil, err
	}
	if params.IdempotencyKey != "" {
		if err := storeEvidenceResult(tx, inst.ID, params.IdempotencyKey, requestHash, ev); err != nil {
			return nil, nil, err
		}
	}
	return ev, task, nil
}

func evidenceAddRequestHash(params AddEvidenceParams, factsRaw, dataRaw json.RawMessage) string {
	req := struct {
		Kind         string          `json:"kind"`
		Ref          string          `json:"ref"`
		Summary      string          `json:"summary,omitempty"`
		Facts        json.RawMessage `json:"facts,omitempty"`
		Data         json.RawMessage `json:"data,omitempty"`
		PrincipalRef string          `json:"principal_ref,omitempty"`
		Role         string          `json:"role,omitempty"`
		RunID        string          `json:"runId,omitempty"`
		ContentHash  string          `json:"contentHash,omitempty"`
		Build        *EvidenceBuild  `json:"build,omitempty"`
	}{
		Kind: params.Kind, Ref: params.Ref, Summary: params.Summary,
		Facts: factsRaw, Data: dataRaw, PrincipalRef: params.PrincipalRef, Role: params.Role, RunID: params.RunID,
		ContentHash: strings.TrimSpace(params.ContentHash), Build: normalizedEvidenceBuild(params.Build),
	}
	return rpcidem.CanonicalRequestHash(req)
}

func normalizedEvidenceBuild(build *EvidenceBuild) *EvidenceBuild {
	if build == nil {
		return nil
	}
	out := &EvidenceBuild{
		ID:      strings.TrimSpace(build.ID),
		Version: strings.TrimSpace(build.Version),
		Env:     strings.TrimSpace(build.Env),
	}
	if out.ID == "" && out.Version == "" && out.Env == "" {
		return nil
	}
	return out
}

func replayEvidenceResult(tx *sql.Tx, instanceID, key, requestHash string) (*Evidence, error) {
	var storedHash, resultJSON string
	err := tx.QueryRow(`
		SELECT request_hash, result_json
		FROM workflow_evidence_idempotency
		WHERE instance_id = ? AND idempotency_key = ?
	`, instanceID, key).Scan(&storedHash, &resultJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if storedHash != requestHash {
		return nil, idempotencyMismatchError(key)
	}
	var ev Evidence
	if err := json.Unmarshal([]byte(resultJSON), &ev); err != nil {
		return nil, err
	}
	return &ev, nil
}

func storeEvidenceResult(tx *sql.Tx, instanceID, key, requestHash string, ev *Evidence) error {
	resultJSON, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`
		INSERT INTO workflow_evidence_idempotency (instance_id, idempotency_key, request_hash, result_json, evidence_id)
		VALUES (?, ?, ?, ?, ?)
	`, instanceID, key, requestHash, string(resultJSON), ev.ID)
	return err
}

// EvidenceSchema returns the declared contract for an evidence kind on the
// task's active workflow instance (F3).
func (s *Service) EvidenceSchema(taskSelector, kind string) (*EvidenceSchema, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	tpl, _, err := s.ShowTemplate(inst.TemplateID + "@" + inst.TemplateVersion)
	if err != nil {
		return nil, err
	}
	spec, ok := tpl.EvidenceKinds[kind]
	if !ok {
		declared := declaredEvidenceKinds(tpl)
		return nil, validationError("kind", fmt.Sprintf("evidence kind %s is not declared by template %s@%s", kind, tpl.ID, tpl.Version), "a declared evidence kind", declared, "use --kind with one of the declared kinds")
	}
	return &EvidenceSchema{
		Kind:         kind,
		Description:  spec.Description,
		Class:        spec.Class,
		Facts:        spec.Facts,
		ProducibleBy: spec.ProducibleBy,
		LinkageRefs:  spec.LinkageRefs,
	}, nil
}

func declaredEvidenceKinds(tpl *Template) []string {
	kinds := make([]string, 0, len(tpl.EvidenceKinds))
	for k := range tpl.EvidenceKinds {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}

func (s *Service) ListEvidence(taskSelector, instanceID string) ([]Evidence, error) {
	inst, err := s.ResolveInstance(taskSelector, instanceID)
	if err != nil {
		return nil, err
	}
	return listInstanceEvidence(s.db, inst.ID)
}

// evidenceColumns is the SELECT list scanEvidenceRows reads, in scan order.
const evidenceColumns = `id, instance_id, kind, ref, COALESCE(summary,''), COALESCE(facts_json,''), COALESCE(data_json,''), source_json,
	COALESCE(principal_ref, actor, ''), COALESCE(role,''), COALESCE(run_id,''), COALESCE(task_etag_at_production,''), COALESCE(task_hash_at_production,''), produced_at`

// queryEvidence runs a workflow_evidence query whose WHERE/ORDER tail is clause.
func queryEvidence(q rowsQueryer, clause string, args ...interface{}) ([]Evidence, error) {
	rows, err := q.Query(`SELECT `+evidenceColumns+` FROM workflow_evidence `+clause, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanEvidenceRows(rows)
}

func listInstanceEvidence(q rowsQueryer, instanceID string) ([]Evidence, error) {
	return queryEvidence(q, `WHERE instance_id = ? ORDER BY produced_at, id`, instanceID)
}

func (s *Service) ShowEvidence(id string) (*Evidence, error) {
	return evidenceByID(s.db, id)
}

func evidenceByID(q rowsQueryer, id string) (*Evidence, error) {
	out, err := queryEvidence(q, `WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("evidence not found: %s", id)
	}
	return &out[0], nil
}

func scanEvidenceRows(rows *sql.Rows) ([]Evidence, error) {
	out := []Evidence{}
	for rows.Next() {
		var e Evidence
		var facts, data, source string
		if err := rows.Scan(&e.ID, &e.InstanceID, &e.Kind, &e.Ref, &e.Summary, &facts, &data, &source, &e.PrincipalRef, &e.Role, &e.RunID, &e.TaskEtagAtProduction, &e.TaskHashAtProduction, &e.ProducedAt); err != nil {
			return nil, err
		}
		if facts != "" {
			e.Facts = json.RawMessage(facts)
		}
		if data != "" {
			e.Data = json.RawMessage(data)
		}
		if source != "" {
			e.Source = json.RawMessage(source)
			e.ContentHash, e.Build = evidenceProvenanceFromSource(source)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func evidenceProvenanceFromSource(source string) (string, *EvidenceBuild) {
	var doc struct {
		ContentHash string         `json:"contentHash"`
		Build       *EvidenceBuild `json:"build"`
	}
	if err := json.Unmarshal([]byte(source), &doc); err != nil {
		return "", nil
	}
	return strings.TrimSpace(doc.ContentHash), normalizedEvidenceBuild(doc.Build)
}

func (s *Service) SuggestEvidence(taskSelector, transitionID string) (map[string]interface{}, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	tpl, _, err := s.ShowTemplate(inst.TemplateID + "@" + inst.TemplateVersion)
	if err != nil {
		return nil, err
	}
	tr, err := findTransition(tpl, transitionID)
	if err != nil {
		return nil, err
	}
	ev, err := s.ListEvidence(taskSelector, "")
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, e := range ev {
		have[e.Kind] = true
	}
	required := []map[string]interface{}{}
	missing := []map[string]interface{}{}
	for _, req := range tr.Requires {
		if req.Evidence != nil {
			match := matchEvidenceRequirement(ev, *req.Evidence)
			item := map[string]interface{}{"kind": req.Evidence.Kind, "present": have[req.Evidence.Kind], "source": "transition.requires", "satisfied": match.OK}
			if len(req.Evidence.Facts) > 0 {
				item["requiredFacts"] = req.Evidence.Facts
			}
			if match.Latest != nil {
				item["latest"] = map[string]interface{}{"id": match.Latest.ID, "facts": match.Latest.Facts}
			}
			if match.Detail != "" {
				item["message"] = match.Detail
			}
			required = append(required, item)
			if !match.OK {
				missing = append(missing, item)
			}
		}
	}
	checks := []map[string]interface{}{}
	task, _ := loadTaskDoc(s.db, inst.TaskUUID)
	facts := taskFacts(task)
	for _, checkID := range tr.Checks {
		check := tpl.Checks[checkID]
		item := map[string]interface{}{"id": checkID, "type": check.Type}
		if check.HookID != "" {
			item["hookId"] = check.HookID
		}
		if check.EvidenceKind != "" {
			item["evidenceKind"] = check.EvidenceKind
		}
		requiredKinds := checkRequiredEvidenceKinds(checkID, check, facts)
		if len(requiredKinds) > 0 {
			item["requiredEvidence"] = requiredKinds
			missingKinds := []string{}
			for _, kind := range requiredKinds {
				if !have[kind] {
					missingKinds = append(missingKinds, kind)
				}
			}
			item["missingEvidence"] = missingKinds
		}
		checks = append(checks, item)
	}
	warnings := []string{}
	if len(checks) > 0 {
		warnings = append(warnings, "evidence presence does not prove hook/schema validity; run wrkf check run for transition-specific validation")
	}
	return map[string]interface{}{"transition": transitionID, "required": required, "missing": missing, "checks": checks, "warnings": warnings}, nil
}
