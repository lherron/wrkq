package workflow

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/id"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/rpcidem"
	"github.com/lherron/wrkq/internal/selectors"
)

type Service struct {
	db  *db.DB
	now nowFunc
}

func NewService(database *db.DB) *Service {
	return &Service{db: database, now: func() time.Time { return time.Now().UTC() }}
}

func LoadTemplateFile(path string) (*Template, []byte, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, "", err
	}
	tpl, canonical, err := ParseTemplate(data)
	if err != nil {
		return nil, nil, "", err
	}
	return tpl, canonical, Hash(canonical), nil
}

func ParseTemplate(data []byte) (*Template, []byte, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, err
	}
	var tpl Template
	if err := json.Unmarshal(data, &tpl); err != nil {
		return nil, nil, err
	}
	tpl.Raw = raw
	canonical, err := json.Marshal(tplForHash(tpl))
	if err != nil {
		return nil, nil, err
	}
	return &tpl, canonical, nil
}

func tplForHash(t Template) map[string]interface{} {
	b, _ := json.Marshal(t)
	var out map[string]interface{}
	_ = json.Unmarshal(b, &out)
	return out
}

func Hash(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func previousEventHashTx(tx *sql.Tx, instanceID string) string {
	var prev sql.NullString
	_ = tx.QueryRow(`SELECT event_hash FROM workflow_events WHERE instance_id = ? ORDER BY seq DESC LIMIT 1`, instanceID).Scan(&prev)
	if prev.Valid {
		return prev.String
	}
	return ""
}

func chainedEventHash(prev string, payloadJSON []byte) string {
	chainPayload, _ := json.Marshal(map[string]interface{}{
		"prevEventHash": prev,
		"payloadHash":   Hash(payloadJSON),
	})
	return Hash(chainPayload)
}

func canonicalHookCatalog(catalog *HookCatalog) ([]byte, string, error) {
	if catalog == nil {
		return nil, "", nil
	}
	canonical, err := json.Marshal(catalog)
	if err != nil {
		return nil, "", err
	}
	return canonical, Hash(canonical), nil
}

func (s *Service) ValidateTemplateFile(path string, catalog *HookCatalog) ValidateResult {
	tpl, canonical, hash, err := LoadTemplateFile(path)
	if err != nil {
		return ValidateResult{Valid: false, Errors: []string{err.Error()}}
	}
	result := ValidateResult{Valid: true, ID: tpl.ID, Version: tpl.Version, Hash: hash}
	errs := ValidateTemplate(tpl, canonical, catalog)
	if len(errs) > 0 {
		result.Valid = false
		result.Errors = errs
	}
	return result
}

func ValidateTemplate(tpl *Template, canonical []byte, catalog *HookCatalog) []string {
	var errs []string
	if tpl.SchemaVersion != "wrkf.workflow-template.v0" {
		errs = append(errs, "schemaVersion must be wrkf.workflow-template.v0")
	}
	if tpl.ID == "" {
		errs = append(errs, "id is required")
	}
	if tpl.Version == "" {
		errs = append(errs, "version is required")
	}
	if tpl.Kind != "agent_first_workflow" {
		errs = append(errs, "kind must be agent_first_workflow")
	}
	if len(tpl.Roles) == 0 {
		errs = append(errs, "roles must not be empty")
	}
	if len(tpl.States) == 0 {
		errs = append(errs, "states must not be empty")
	}
	if len(tpl.Transitions) == 0 {
		errs = append(errs, "transitions must not be empty")
	}
	if containsInlineExecutable(canonical) {
		errs = append(errs, "template must not contain inline executable command keys")
	}
	errs = append(errs, validateFactsContracts(tpl)...)

	stateSet := map[string]bool{}
	for _, st := range tpl.States {
		if !validStatus(st.Status) {
			errs = append(errs, fmt.Sprintf("invalid state status %q", st.Status))
		}
		key := stateKey(st)
		if stateSet[key] {
			errs = append(errs, fmt.Sprintf("duplicate state %s", key))
		}
		stateSet[key] = true
	}
	if !stateSet[stateKey(tpl.Initial)] {
		errs = append(errs, "initial state must be listed in states")
	}
	transitions := map[string]bool{}
	for _, tr := range tpl.Transitions {
		if tr.ID == "" {
			errs = append(errs, "transition id is required")
			continue
		}
		if transitions[tr.ID] {
			errs = append(errs, fmt.Sprintf("duplicate transition %s", tr.ID))
		}
		transitions[tr.ID] = true
		if !stateSet[stateKey(tr.From)] {
			errs = append(errs, fmt.Sprintf("transition %s from state is not declared", tr.ID))
		}
		if len(tr.By) == 0 {
			errs = append(errs, fmt.Sprintf("transition %s must declare by roles", tr.ID))
		}
		for _, role := range tr.By {
			if _, ok := tpl.Roles[role]; !ok && role != "supervisor" && role != "system" {
				errs = append(errs, fmt.Sprintf("transition %s references unknown role %s", tr.ID, role))
			}
		}
		if tr.Responsibility != nil && tr.Responsibility.Role != "" {
			role := tr.Responsibility.Role
			if _, ok := tpl.Roles[role]; !ok && role != "supervisor" && role != "system" {
				errs = append(errs, fmt.Sprintf("transition %s responsibility references unknown role %s", tr.ID, role))
			}
		}
		for _, req := range tr.Requires {
			if req.Evidence != nil && tpl.EvidenceKinds != nil {
				if _, ok := tpl.EvidenceKinds[req.Evidence.Kind]; !ok {
					errs = append(errs, fmt.Sprintf("transition %s requires unknown evidence kind %s", tr.ID, req.Evidence.Kind))
				}
			}
			if req.Obligation != nil && tpl.ObligationKinds != nil && req.Obligation.Kind != "" {
				if _, ok := tpl.ObligationKinds[req.Obligation.Kind]; !ok {
					errs = append(errs, fmt.Sprintf("transition %s requires unknown obligation kind %s", tr.ID, req.Obligation.Kind))
				}
			}
		}
		if tr.SeparationOfDuty != nil && tpl.EvidenceKinds != nil {
			for _, kind := range tr.SeparationOfDuty.DistinctActorFromEvidence {
				if _, ok := tpl.EvidenceKinds[kind]; !ok {
					errs = append(errs, fmt.Sprintf("transition %s SoD references unknown evidence kind %s", tr.ID, kind))
				}
			}
			for _, pair := range tr.SeparationOfDuty.EvidenceActorPairsDistinct {
				if _, ok := tpl.EvidenceKinds[pair.LeftKind]; !ok {
					errs = append(errs, fmt.Sprintf("transition %s SoD references unknown evidence kind %s", tr.ID, pair.LeftKind))
				}
				if _, ok := tpl.EvidenceKinds[pair.RightKind]; !ok {
					errs = append(errs, fmt.Sprintf("transition %s SoD references unknown evidence kind %s", tr.ID, pair.RightKind))
				}
			}
		}
		for _, checkID := range tr.Checks {
			check, ok := tpl.Checks[checkID]
			if !ok {
				errs = append(errs, fmt.Sprintf("transition %s references unknown check %s", tr.ID, checkID))
				continue
			}
			if check.Type == "hook" {
				if check.HookID == "" {
					errs = append(errs, fmt.Sprintf("check %s missing hookId", checkID))
				} else if catalog != nil {
					if _, ok := catalog.Hooks[check.HookID]; !ok {
						errs = append(errs, fmt.Sprintf("check %s references missing hook %s", checkID, check.HookID))
					}
				}
			}
		}
		if len(tr.Outcomes) == 0 {
			errs = append(errs, fmt.Sprintf("transition %s must declare outcomes", tr.ID))
		}
		for i, out := range tr.Outcomes {
			if out.ID == "" {
				errs = append(errs, fmt.Sprintf("transition %s outcome id is required", tr.ID))
			}
			if !stateSet[stateKey(out.To)] {
				errs = append(errs, fmt.Sprintf("transition %s outcome %s target is not declared", tr.ID, out.ID))
			}
			if out.When.Otherwise != nil && *out.When.Otherwise && i != len(tr.Outcomes)-1 {
				errs = append(errs, fmt.Sprintf("transition %s otherwise outcome must be final", tr.ID))
			}
			for _, obl := range out.Obligations {
				if tpl.ObligationKinds != nil {
					if _, ok := tpl.ObligationKinds[obl.Kind]; !ok {
						errs = append(errs, fmt.Sprintf("transition %s outcome %s creates unknown obligation kind %s", tr.ID, out.ID, obl.Kind))
					}
				}
			}
		}
	}
	return errs
}

func containsInlineExecutable(canonical []byte) bool {
	var walk func(interface{}) bool
	var v interface{}
	if err := json.Unmarshal(canonical, &v); err != nil {
		return true
	}
	forbidden := map[string]bool{"argv": true, "cmd": true, "command": true, "shell": true, "cwd": true, "env": true}
	walk = func(x interface{}) bool {
		switch t := x.(type) {
		case map[string]interface{}:
			for k, v := range t {
				if forbidden[k] {
					return true
				}
				if walk(v) {
					return true
				}
			}
		case []interface{}:
			for _, v := range t {
				if walk(v) {
					return true
				}
			}
		}
		return false
	}
	return walk(v)
}

func validStatus(status string) bool {
	switch status {
	case "open", "active", "waiting", "closed":
		return true
	default:
		return false
	}
}

func stateKey(st State) string {
	return st.Status + "/" + st.Phase + "/" + st.Outcome
}

func stateMatches(inst Instance, st State) bool {
	if st.Status != "" && inst.Status != st.Status {
		return false
	}
	if st.Phase != "" && inst.Phase != st.Phase {
		return false
	}
	if st.Outcome != "" && inst.Outcome != st.Outcome {
		return false
	}
	return true
}

func (s *Service) InstallTemplate(path, actor string, catalog *HookCatalog) (map[string]interface{}, error) {
	tpl, canonical, hash, err := LoadTemplateFile(path)
	if err != nil {
		return nil, err
	}
	return s.installTemplateCanonical(tpl, canonical, hash, actor, catalog, false)
}

// installTemplateCanonical installs an already-parsed template. It is shared by
// the file-based InstallTemplate and the embedded built-in installer so both
// honor the same validation and idempotent-by-hash semantics.
//
// When supersede is true (embedded built-in installer only), a same-version
// hash change overwrites the stored definition/hash in place instead of
// erroring, so a rebuilt binary can evolve a built-in workflow. There is NO
// pinned-hash guard: old instances carrying the prior template hash may then
// evaluate under the NEW definition. That divergence is an explicitly accepted
// operational risk for embedded built-ins only (Lance, T-triage-spec-gate);
// correctness is asserted only for behavior evaluated after install, not for
// historical template-hash immutability. File-based InstallTemplate keeps the
// immutable same-id/version-hash-mismatch rejection (supersede=false).
func (s *Service) installTemplateCanonical(tpl *Template, canonical []byte, hash, actor string, catalog *HookCatalog, supersede bool) (map[string]interface{}, error) {
	if errs := ValidateTemplate(tpl, canonical, catalog); len(errs) > 0 {
		return nil, fmt.Errorf("invalid template: %s", strings.Join(errs, "; "))
	}
	catalogCanonical, catalogHash, err := canonicalHookCatalog(catalog)
	if err != nil {
		return nil, err
	}
	var existingHash, existingCatalogHash string
	err = s.db.QueryRow(`SELECT hash, COALESCE(hook_catalog_hash, '') FROM workflow_templates WHERE id = ? AND version = ?`, tpl.ID, tpl.Version).Scan(&existingHash, &existingCatalogHash)
	if err == nil {
		if existingHash != hash {
			if !supersede {
				return nil, fmt.Errorf("template %s@%s already installed with different hash", tpl.ID, tpl.Version)
			}
			if _, err := s.db.Exec(`UPDATE workflow_templates SET hash = ?, definition_json = ?, hook_catalog_json = ?, hook_catalog_hash = ? WHERE id = ? AND version = ?`,
				hash, string(canonical), nullIfEmpty(string(catalogCanonical)), nullIfEmpty(catalogHash), tpl.ID, tpl.Version); err != nil {
				return nil, err
			}
			return map[string]interface{}{"id": tpl.ID, "version": tpl.Version, "hash": hash, "installed": true, "superseded": true}, nil
		}
		if catalogHash != "" && existingCatalogHash != "" && existingCatalogHash != catalogHash {
			return nil, fmt.Errorf("template %s@%s already installed with different hook catalog hash", tpl.ID, tpl.Version)
		}
		if catalogHash != "" && existingCatalogHash == "" {
			if _, err := s.db.Exec(`UPDATE workflow_templates SET hook_catalog_json = ?, hook_catalog_hash = ? WHERE id = ? AND version = ?`, string(catalogCanonical), catalogHash, tpl.ID, tpl.Version); err != nil {
				return nil, err
			}
		}
		return map[string]interface{}{"id": tpl.ID, "version": tpl.Version, "hash": hash, "installed": false}, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}
	_, err = s.db.Exec(`
		INSERT INTO workflow_templates (id, version, hash, definition_json, installed_by, hook_catalog_json, hook_catalog_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, tpl.ID, tpl.Version, hash, string(canonical), emptyToNil(actor), nullIfEmpty(string(catalogCanonical)), nullIfEmpty(catalogHash))
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"id": tpl.ID, "version": tpl.Version, "hash": hash, "installed": true}, nil
}

func (s *Service) ListTemplates() ([]map[string]interface{}, error) {
	rows, err := s.db.Query(`SELECT id, version, hash, installed_at, installed_by FROM workflow_templates ORDER BY id, version`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []map[string]interface{}
	for rows.Next() {
		var id, version, hash, installedAt string
		var installedBy sql.NullString
		if err := rows.Scan(&id, &version, &hash, &installedAt, &installedBy); err != nil {
			return nil, err
		}
		row := map[string]interface{}{"id": id, "version": version, "hash": hash, "installedAt": installedAt}
		if installedBy.Valid {
			row["installedBy"] = installedBy.String
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Service) ShowTemplate(ref string) (*Template, string, error) {
	id, version, err := parseTemplateRef(ref)
	if err != nil {
		return nil, "", err
	}
	var definition, hash string
	if err := s.db.QueryRow(`SELECT definition_json, hash FROM workflow_templates WHERE id = ? AND version = ?`, id, version).Scan(&definition, &hash); err != nil {
		if err == sql.ErrNoRows {
			return nil, "", fmt.Errorf("template not found: %s", ref)
		}
		return nil, "", err
	}
	tpl, _, err := ParseTemplate([]byte(definition))
	return tpl, hash, err
}

func (s *Service) storedHookCatalog(templateID, version string) (*HookCatalog, error) {
	var raw sql.NullString
	if err := s.db.QueryRow(`SELECT hook_catalog_json FROM workflow_templates WHERE id = ? AND version = ?`, templateID, version).Scan(&raw); err != nil {
		return nil, err
	}
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil, nil
	}
	var catalog HookCatalog
	if err := json.Unmarshal([]byte(raw.String), &catalog); err != nil {
		return nil, err
	}
	return &catalog, nil
}

func parseTemplateRef(ref string) (string, string, error) {
	parts := strings.Split(ref, "@")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("template ref must be id@version")
	}
	return parts[0], parts[1], nil
}

func emptyToNil(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func (s *Service) AttachTask(taskSelector, templateRef, actor string) (*Instance, error) {
	taskUUID, taskID, err := selectors.ResolveTask(s.db, taskSelector)
	if err != nil {
		return nil, err
	}
	id, version, err := parseTemplateRef(templateRef)
	if err != nil {
		return nil, err
	}
	var definition, templateHash string
	if err := s.db.QueryRow(`SELECT definition_json, hash FROM workflow_templates WHERE id = ? AND version = ?`, id, version).Scan(&definition, &templateHash); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("template not found: %s", templateRef)
		}
		return nil, err
	}
	tpl, _, err := ParseTemplate([]byte(definition))
	if err != nil {
		return nil, err
	}

	var inst *Instance
	err = withTx(s.db.DB, func(tx *sql.Tx) error {
		task, err := loadTaskDoc(tx, taskUUID)
		if err != nil {
			return err
		}
		taskHash := taskDocHash(task)
		instanceID := fmt.Sprintf("wfi_%s_%d", strings.ToLower(strings.ReplaceAll(taskID, "-", "")), s.now().UnixNano())
		ctxHash := contextHash(templateHash, tpl.Initial, 0, taskHash, nil, nil, nil)
		now := s.now().Format(time.RFC3339)
		_, err = tx.Exec(`
			INSERT INTO workflow_instances (
				id, task_uuid, task_ref, project_id, template_id, template_version, template_hash,
				status, phase, outcome, revision, context_hash, task_doc_etag, task_doc_hash,
				created_at, updated_at, closed_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?)
		`, instanceID, taskUUID, "wrkq:"+taskID, task.ProjectID, id, version, templateHash,
			tpl.Initial.Status, nullIfEmpty(tpl.Initial.Phase), nullIfEmpty(tpl.Initial.Outcome),
			ctxHash, fmt.Sprint(task.ETag), taskHash, now, now, nil)
		if err != nil {
			return err
		}
		inst = &Instance{ID: instanceID, TaskUUID: taskUUID, TaskRef: "wrkq:" + taskID, ProjectID: task.ProjectID, TemplateID: id, TemplateVersion: version, TemplateHash: templateHash, Status: tpl.Initial.Status, Phase: tpl.Initial.Phase, Outcome: tpl.Initial.Outcome, Revision: 0, ContextHash: ctxHash, TaskDocEtag: fmt.Sprint(task.ETag), TaskDocHash: taskHash, CreatedAt: now, UpdatedAt: now}
		if err := insertEvent(tx, instanceID, "workflow.attached", actor, "", "", 0, 0, "", task.ETag, taskHash, ctxHash, map[string]interface{}{"template": templateRef, "state": tpl.Initial}); err != nil {
			return err
		}
		return updateTaskWorkflowMeta(tx, taskUUID, *inst, actor)
	})
	if err != nil {
		return nil, err
	}
	return inst, nil
}

type taskDoc struct {
	UUID          string
	ID            string
	ProjectID     string
	Slug          string
	Title         string
	Description   string
	Specification string
	State         string
	Priority      int
	Kind          string
	Labels        string
	Meta          string
	ETag          int64
	UpdatedAt     string
}

func loadTaskDoc(tx queryer, taskUUID string) (*taskDoc, error) {
	var t taskDoc
	var labels, meta sql.NullString
	err := tx.QueryRow(`
		SELECT uuid, id, project_uuid, slug, title, description, specification, state, priority, kind,
		       labels, meta, etag, updated_at
		FROM tasks WHERE uuid = ?
	`, taskUUID).Scan(&t.UUID, &t.ID, &t.ProjectID, &t.Slug, &t.Title, &t.Description, &t.Specification, &t.State, &t.Priority, &t.Kind, &labels, &meta, &t.ETag, &t.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("task not found")
		}
		return nil, err
	}
	if labels.Valid {
		t.Labels = labels.String
	}
	if meta.Valid {
		t.Meta = meta.String
	}
	return &t, nil
}

type queryer interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}

type rowsQueryer interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
}

func taskRelationBlockers(q rowsQueryer, taskUUID string) ([]Blocker, error) {
	rows, err := q.Query(`
		SELECT b.id, b.state, COALESCE(b.title, '')
		FROM task_relations r
		JOIN tasks b ON b.uuid = r.from_task_uuid
		WHERE r.to_task_uuid = ?
		  AND r.kind = 'blocks'
		  AND b.state NOT IN ('completed', 'cancelled', 'archived', 'deleted')
		ORDER BY b.id
	`, taskUUID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var blockers []Blocker
	for rows.Next() {
		var id, state, title string
		if err := rows.Scan(&id, &state, &title); err != nil {
			return nil, err
		}
		msg := fmt.Sprintf("blocked by task %s in state %s", id, state)
		if title != "" {
			msg = fmt.Sprintf("blocked by task %s (%s) in state %s", id, title, state)
		}
		blockers = append(blockers, Blocker{Kind: "task_dependency", Ref: id, Message: msg})
	}
	return blockers, rows.Err()
}

func taskDocHash(t *taskDoc) string {
	meta := map[string]interface{}{}
	if strings.TrimSpace(t.Meta) != "" {
		_ = json.Unmarshal([]byte(t.Meta), &meta)
		delete(meta, "workflow")
	}
	doc := map[string]interface{}{
		"id": t.ID, "slug": t.Slug, "title": t.Title, "description": t.Description,
		"specification": t.Specification, "state": t.State, "priority": t.Priority,
		"kind": t.Kind, "labels": t.Labels, "meta": meta,
	}
	b, _ := json.Marshal(doc)
	return Hash(b)
}

func contextHash(templateHash string, state State, revision int64, taskHash string, ev []Evidence, obl []Obligation, eff []Effect) string {
	doc := map[string]interface{}{"templateHash": templateHash, "state": state, "revision": revision, "taskDocHash": taskHash}
	if ev != nil {
		refs := make([]string, 0, len(ev))
		for _, e := range ev {
			refs = append(refs, e.ID+":"+e.Kind+":"+e.Ref+":"+Hash(e.Facts))
		}
		sort.Strings(refs)
		doc["evidence"] = refs
	}
	if obl != nil {
		refs := make([]string, 0, len(obl))
		for _, o := range obl {
			if o.Status == "open" {
				refs = append(refs, o.ID+":"+o.Kind+":"+fmt.Sprint(o.Blocking))
			}
		}
		sort.Strings(refs)
		doc["openObligations"] = refs
	}
	if eff != nil {
		refs := make([]string, 0, len(eff))
		for _, e := range eff {
			if e.Status == "pending" || e.Status == "leased" {
				refs = append(refs, e.ID+":"+e.Kind+":"+e.Status)
			}
		}
		sort.Strings(refs)
		doc["pendingEffects"] = refs
	}
	b, _ := json.Marshal(doc)
	return Hash(b)
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func withTx(db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// withImmediateTx starts a write transaction using SQLite BEGIN IMMEDIATE.
// go-sqlite3 emits BEGIN IMMEDIATE when the connection DSN has _txlock=immediate.
func withImmediateTx(database *db.DB, fn func(*sql.Tx) error) error {
	dsn := database.Path()
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	immediateDB, err := sql.Open("sqlite3", dsn+sep+"_txlock=immediate&_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer func() { _ = immediateDB.Close() }()
	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := immediateDB.Exec(pragma); err != nil {
			return err
		}
	}
	tx, err := immediateDB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func insertEvent(tx *sql.Tx, instanceID, typ, actor, role, runID string, observed, next int64, key string, taskETag int64, taskHash, ctxHash string, payload interface{}) error {
	_, err := insertEventWithResult(tx, instanceID, typ, actor, role, runID, observed, next, key, "", "", taskETag, taskHash, ctxHash, payload)
	return err
}

func insertEventWithResult(tx *sql.Tx, instanceID, typ, actor, role, runID string, observed, next int64, key, requestHash, resultJSON string, taskETag int64, taskHash, ctxHash string, payload interface{}) (string, error) {
	id, err := nextSeqID(tx, "workflow_event_seq", "wfe")
	if err != nil {
		return "", err
	}
	var seq int64
	_ = tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM workflow_events WHERE instance_id = ?`, instanceID).Scan(&seq)
	payloadJSON, _ := json.Marshal(payload)
	prevHash := previousEventHashTx(tx, instanceID)
	eventHash := chainedEventHash(prevHash, payloadJSON)
	_, err = tx.Exec(`
		INSERT INTO workflow_events (
			id, instance_id, seq, schema_version, type, actor, role, run_id,
			observed_revision, next_revision, task_doc_etag, task_doc_hash, context_hash,
			idempotency_key, request_hash, result, result_json, payload_json, prev_event_hash, event_hash
		) VALUES (?, ?, ?, 'wrkf.workflow-event.v0', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'committed', ?, ?, ?, ?)
	`, id, instanceID, seq, typ, emptyToNil(actor), emptyToNil(role), emptyToNil(runID), observed, next, fmt.Sprint(taskETag), taskHash, ctxHash, emptyToNil(key), nullIfEmpty(requestHash), nullIfEmpty(resultJSON), string(payloadJSON), nullIfEmpty(prevHash), eventHash)
	return id, err
}

func nextSeqID(tx *sql.Tx, table, prefix string) (string, error) {
	if _, err := tx.Exec(fmt.Sprintf("INSERT INTO %s (id) VALUES (NULL)", table)); err != nil {
		return "", err
	}
	var id int64
	if err := tx.QueryRow("SELECT last_insert_rowid()").Scan(&id); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%06d", prefix, id), nil
}

func updateTaskWorkflowMeta(tx *sql.Tx, taskUUID string, inst Instance, actor string) error {
	var metaText sql.NullString
	if err := tx.QueryRow(`SELECT meta FROM tasks WHERE uuid = ?`, taskUUID).Scan(&metaText); err != nil {
		return err
	}
	meta := map[string]interface{}{}
	if metaText.Valid && strings.TrimSpace(metaText.String) != "" {
		if err := json.Unmarshal([]byte(metaText.String), &meta); err != nil {
			return fmt.Errorf("task meta is not valid JSON: %w", err)
		}
	}
	wf := map[string]interface{}{
		"instanceId": inst.ID,
		"taskRef":    inst.TaskRef,
		"template": map[string]interface{}{
			"id": inst.TemplateID, "version": inst.TemplateVersion, "hash": inst.TemplateHash,
		},
		"state": map[string]interface{}{
			"status": inst.Status,
		},
		"revision":    inst.Revision,
		"contextHash": inst.ContextHash,
		"taskDoc": map[string]interface{}{
			"etag": inst.TaskDocEtag, "hash": inst.TaskDocHash,
		},
		"updatedAt": inst.UpdatedAt,
	}
	if inst.Phase != "" {
		wf["state"].(map[string]interface{})["phase"] = inst.Phase
	}
	if inst.Outcome != "" {
		wf["state"].(map[string]interface{})["outcome"] = inst.Outcome
	}
	meta["workflow"] = wf
	b, _ := json.Marshal(meta)
	_, err := tx.Exec(`UPDATE tasks SET meta = ?, updated_by_actor_uuid = COALESCE((SELECT uuid FROM actors WHERE slug = ? OR id = ? LIMIT 1), updated_by_actor_uuid) WHERE uuid = ?`, string(b), actor, actor, taskUUID)
	return err
}

func (s *Service) ActiveInstance(taskSelector string) (*Instance, error) {
	taskUUID, _, err := selectors.ResolveTask(s.db, taskSelector)
	if err != nil {
		return nil, err
	}
	return s.activeInstanceByTaskUUID(taskUUID)
}

func (s *Service) activeInstanceByTaskUUID(taskUUID string) (*Instance, error) {
	row := s.db.QueryRow(`
		SELECT id, task_uuid, task_ref, COALESCE(project_id,''), template_id, template_version, template_hash,
		       status, COALESCE(phase,''), COALESCE(outcome,''), revision, context_hash,
		       task_doc_etag, task_doc_hash, created_at, updated_at, COALESCE(closed_at,'')
		FROM workflow_instances
		WHERE task_uuid = ? AND status != 'closed'
		ORDER BY created_at DESC LIMIT 1
	`, taskUUID)
	return scanInstance(row)
}

func (s *Service) LatestInstance(taskSelector string) (*Instance, error) {
	taskUUID, _, err := selectors.ResolveTask(s.db, taskSelector)
	if err != nil {
		return nil, err
	}
	row := s.db.QueryRow(`
		SELECT id, task_uuid, task_ref, COALESCE(project_id,''), template_id, template_version, template_hash,
		       status, COALESCE(phase,''), COALESCE(outcome,''), revision, context_hash,
		       task_doc_etag, task_doc_hash, created_at, updated_at, COALESCE(closed_at,'')
		FROM workflow_instances
		WHERE task_uuid = ?
		ORDER BY created_at DESC LIMIT 1
	`, taskUUID)
	return scanInstance(row)
}

func scanInstance(row *sql.Row) (*Instance, error) {
	var i Instance
	if err := row.Scan(&i.ID, &i.TaskUUID, &i.TaskRef, &i.ProjectID, &i.TemplateID, &i.TemplateVersion, &i.TemplateHash, &i.Status, &i.Phase, &i.Outcome, &i.Revision, &i.ContextHash, &i.TaskDocEtag, &i.TaskDocHash, &i.CreatedAt, &i.UpdatedAt, &i.ClosedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("workflow instance not found")
		}
		return nil, err
	}
	return &i, nil
}

func (s *Service) InspectTask(taskSelector string) (*Instance, error) {
	return s.LatestInstance(taskSelector)
}

func (s *Service) Timeline(taskSelector string) ([]Event, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT id, instance_id, seq, schema_version, type, COALESCE(actor,''), COALESCE(role,''), COALESCE(run_id,''),
		       COALESCE(observed_revision,0), next_revision, COALESCE(task_doc_etag,''), COALESCE(task_doc_hash,''), COALESCE(context_hash,''),
		       COALESCE(idempotency_key,''), COALESCE(result,''), COALESCE(rejection_code,''), payload_json, created_at
		FROM workflow_events WHERE instance_id = ? ORDER BY seq
	`, inst.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		var e Event
		var payload string
		if err := rows.Scan(&e.ID, &e.InstanceID, &e.Seq, &e.SchemaVersion, &e.Type, &e.Actor, &e.Role, &e.RunID, &e.ObservedRevision, &e.NextRevision, &e.TaskDocEtag, &e.TaskDocHash, &e.ContextHash, &e.IdempotencyKey, &e.Result, &e.RejectionCode, &payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Service) QueryEvents(params EventQueryParams) (EventQueryResult, error) {
	eventType := strings.TrimSpace(params.EventType)
	if eventType == "" {
		eventType = "workflow.transitioned"
	}
	if eventType != "workflow.transitioned" {
		return EventQueryResult{}, validationError("eventType", "only workflow.transitioned is queryable", "workflow.transitioned", []string{"workflow.transitioned"}, "set eventType to workflow.transitioned")
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	page, err := cursor.Apply(params.Cursor, cursor.ApplyOptions{
		SortFields: []string{"created_at"},
		SQLFields:  []string{"e.created_at"},
		Descending: []bool{false},
		IDField:    "e.id",
		Limit:      limit,
	})
	if err != nil {
		return EventQueryResult{}, validationError("cursor", "invalid cursor", "cursor returned by previous event query page", nil, "retry without cursor or with a cursor from this query")
	}

	where := []string{"e.type = ?"}
	args := []interface{}{eventType}
	if project := strings.TrimSpace(params.Project); project != "" {
		where = append(where, "(wi.project_id = ? OR p.uuid = ? OR p.id = ? OR p.slug = ?)")
		args = append(args, project, project, project, project)
	}
	if fromPhase := strings.TrimSpace(params.FromPhase); fromPhase != "" {
		where = append(where, "COALESCE(json_extract(e.payload_json, '$.from.phase'), '') = ?")
		args = append(args, fromPhase)
	}
	if toPhase := strings.TrimSpace(params.ToPhase); toPhase != "" {
		where = append(where, "COALESCE(json_extract(e.payload_json, '$.to.phase'), '') = ?")
		args = append(args, toPhase)
	}
	if classes := compactStrings(append(params.RiskClasses, params.RiskClass)); len(classes) > 0 {
		ph := placeholders(len(classes))
		where = append(where, "COALESCE(t.risk_class, '') IN ("+ph+")")
		for _, class := range classes {
			args = append(args, class)
		}
	}
	if classes := compactStrings(append(params.ExcludeRiskClasses, params.ExcludeRiskClass)); len(classes) > 0 {
		ph := placeholders(len(classes))
		where = append(where, "COALESCE(t.risk_class, '') NOT IN ("+ph+")")
		for _, class := range classes {
			args = append(args, class)
		}
	}
	boundRole := strings.TrimSpace(params.BoundRole)
	if boundRole != "" {
		where = append(where, "EXISTS (SELECT 1 FROM workflow_role_bindings rb WHERE rb.instance_id = e.instance_id AND rb.role = ?)")
		args = append(args, boundRole)
	}
	if page.WhereClause != "" {
		where = append(where, page.WhereClause)
		args = append(args, page.Params...)
	}

	query := `
		SELECT e.id, e.instance_id, e.seq, e.type, COALESCE(e.actor,''), COALESCE(e.role,''),
		       e.payload_json, e.created_at, wi.task_ref,
		       t.uuid, t.id, t.slug, t.project_uuid, COALESCE(t.risk_class,''),
		       COALESCE(p.id,''), COALESCE(p.slug,'')
		FROM workflow_events e
		JOIN workflow_instances wi ON wi.id = e.instance_id
		JOIN tasks t ON t.uuid = wi.task_uuid
		LEFT JOIN containers p ON p.uuid = t.project_uuid
		WHERE ` + strings.Join(where, " AND ") + `
		` + page.OrderByClause
	if page.LimitClause != "" {
		query += "\n\t\t" + page.LimitClause
		args = append(args, *page.LimitParam)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return EventQueryResult{}, err
	}
	defer func() { _ = rows.Close() }()

	items := []TransitionEvent{}
	for rows.Next() {
		var item TransitionEvent
		var payload string
		if err := rows.Scan(
			&item.ID, &item.InstanceID, &item.Seq, &item.EventType, &item.Actor, &item.ActorRole,
			&payload, &item.TransitionedAt, &item.Task.Ref,
			&item.Task.UUID, &item.Task.ID, &item.Task.Slug, &item.Task.ProjectUUID, &item.Task.RiskClass,
			&item.Task.ProjectID, &item.Task.ProjectSlug,
		); err != nil {
			return EventQueryResult{}, err
		}
		item.Payload = json.RawMessage(payload)
		applyTransitionPayload(&item)
		if boundRole != "" {
			bindings, err := listRoleBindingsForInstanceRole(s.db, item.InstanceID, boundRole)
			if err != nil {
				return EventQueryResult{}, err
			}
			if bindings == nil {
				bindings = []RoleBinding{}
			}
			item.MatchingRoleBindings = bindings
		}
		if params.IncludeRoleBindings {
			bindings, err := listRoleBindingsForInstance(s.db, item.InstanceID)
			if err != nil {
				return EventQueryResult{}, err
			}
			if bindings == nil {
				bindings = []RoleBinding{}
			}
			item.RoleBindings = bindings
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return EventQueryResult{}, err
	}

	result := EventQueryResult{Items: items}
	if len(items) > limit {
		result.Items = items[:limit]
		result.HasMore = true
		anchor := result.Items[limit-1]
		next, cerr := cursor.BuildNextCursor([]string{"created_at"}, []any{anchor.TransitionedAt}, anchor.ID)
		if cerr == nil {
			result.NextCursor = next
		}
	}
	return result, nil
}

func applyTransitionPayload(item *TransitionEvent) {
	if item == nil || len(item.Payload) == 0 {
		return
	}
	var payload struct {
		Transition string `json:"transition"`
		Outcome    string `json:"outcome"`
		From       State  `json:"from"`
		To         State  `json:"to"`
	}
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		return
	}
	item.Transition = payload.Transition
	item.Outcome = payload.Outcome
	item.From = payload.From
	item.To = payload.To
	item.FromPhase = payload.From.Phase
	item.ToPhase = payload.To.Phase
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	parts := make([]string, n)
	for i := range parts {
		parts[i] = "?"
	}
	return strings.Join(parts, ",")
}

func (s *Service) Refresh(taskSelector, actor string) (*Instance, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	err = withTx(s.db.DB, func(tx *sql.Tx) error {
		task, err := loadTaskDoc(tx, inst.TaskUUID)
		if err != nil {
			return err
		}
		ev, _ := listEvidenceTx(tx, inst.ID)
		obl, _ := listObligationsTx(tx, inst.ID, false)
		eff, _ := listEffectsTx(tx, inst.ID, false)
		inst.TaskDocEtag = fmt.Sprint(task.ETag)
		inst.TaskDocHash = taskDocHash(task)
		inst.ContextHash = contextHash(inst.TemplateHash, inst.State(), inst.Revision, inst.TaskDocHash, ev, obl, eff)
		inst.UpdatedAt = s.now().Format(time.RFC3339)
		_, err = tx.Exec(`UPDATE workflow_instances SET task_doc_etag = ?, task_doc_hash = ?, context_hash = ?, updated_at = ? WHERE id = ?`,
			inst.TaskDocEtag, inst.TaskDocHash, inst.ContextHash, inst.UpdatedAt, inst.ID)
		if err != nil {
			return err
		}
		return updateTaskWorkflowMeta(tx, inst.TaskUUID, *inst, actor)
	})
	return inst, err
}

func (s *Service) SyncMeta(taskSelector, actor string) (int, error) {
	if taskSelector == "" {
		rows, err := s.db.Query(`SELECT id FROM workflow_instances ORDER BY updated_at DESC`)
		if err != nil {
			return 0, err
		}
		defer func() { _ = rows.Close() }()
		count := 0
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return count, err
			}
			inst, err := s.instanceByID(id)
			if err != nil {
				return count, err
			}
			if err := withTx(s.db.DB, func(tx *sql.Tx) error { return updateTaskWorkflowMeta(tx, inst.TaskUUID, *inst, actor) }); err != nil {
				return count, err
			}
			count++
		}
		return count, rows.Err()
	}
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return 0, err
	}
	if err := withTx(s.db.DB, func(tx *sql.Tx) error { return updateTaskWorkflowMeta(tx, inst.TaskUUID, *inst, actor) }); err != nil {
		return 0, err
	}
	return 1, nil
}

func (s *Service) instanceByID(id string) (*Instance, error) {
	row := s.db.QueryRow(`
		SELECT id, task_uuid, task_ref, COALESCE(project_id,''), template_id, template_version, template_hash,
		       status, COALESCE(phase,''), COALESCE(outcome,''), revision, context_hash,
		       task_doc_etag, task_doc_hash, created_at, updated_at, COALESCE(closed_at,'')
		FROM workflow_instances WHERE id = ?
	`, id)
	return scanInstance(row)
}

func (s *Service) ResolveInstance(taskSelector, instanceID string) (*Instance, error) {
	return resolveInstanceSelectors(s.db, taskSelector, instanceID)
}

func resolveInstanceSelectors(q queryer, taskSelector, instanceID string) (*Instance, error) {
	taskSelector = strings.TrimSpace(taskSelector)
	instanceID = strings.TrimSpace(instanceID)
	if taskSelector == "" && instanceID == "" {
		return nil, validationError("selector", "task or instanceId is required", "task or instanceId", nil, "supply task or instanceId")
	}

	var taskInst *Instance
	if taskSelector != "" {
		taskUUID, err := resolveTaskUUIDQuery(q, taskSelector)
		if err != nil {
			return nil, err
		}
		inst, err := latestInstanceByTaskUUIDQuery(q, taskUUID)
		if err != nil {
			return nil, err
		}
		taskInst = inst
	}
	if instanceID == "" {
		return taskInst, nil
	}

	instanceInst, err := instanceByIDQuery(q, instanceID)
	if err != nil {
		return nil, err
	}
	if taskInst != nil && taskInst.ID != instanceInst.ID {
		return nil, validationError("instanceId", "task and instanceId resolve to different workflow instances", "matching task and instanceId", nil, "retry with selectors for the same workflow instance")
	}
	return instanceInst, nil
}

func (s *Service) ListRoleBindings(taskSelector, instanceID string) ([]RoleBinding, error) {
	inst, err := s.ResolveInstance(taskSelector, instanceID)
	if err != nil {
		return nil, err
	}
	return listRoleBindingsForInstance(s.db, inst.ID)
}

type RoleBindOptions struct {
	TaskSelector string
	InstanceID   string
	Role         string
	Actor        string
	DeliveryRef  string
	Lane         string
	BindingMode  string
}

func (s *Service) BindRole(opts RoleBindOptions) (*RoleBinding, error) {
	if err := validateRoleBindingInput(opts.Role, opts.Actor, opts.BindingMode); err != nil {
		return nil, err
	}
	mode := strings.TrimSpace(opts.BindingMode)
	if mode == "" {
		mode = "required"
	}
	var out *RoleBinding
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		inst, err := resolveInstanceSelectors(tx, opts.TaskSelector, opts.InstanceID)
		if err != nil {
			return err
		}
		now := s.now().Format(time.RFC3339)
		_, err = tx.Exec(`
			INSERT INTO workflow_role_bindings (instance_id, role, actor, delivery_ref, lane, binding_mode, bound_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(instance_id, role, actor) DO UPDATE SET
				delivery_ref = excluded.delivery_ref,
				lane = excluded.lane,
				binding_mode = excluded.binding_mode,
				bound_at = excluded.bound_at
		`, inst.ID, strings.TrimSpace(opts.Role), strings.TrimSpace(opts.Actor), nullIfEmpty(opts.DeliveryRef), nullIfEmpty(opts.Lane), mode, now)
		if err != nil {
			return err
		}
		binding, err := getRoleBindingTx(tx, inst.ID, strings.TrimSpace(opts.Role), strings.TrimSpace(opts.Actor))
		if err != nil {
			return err
		}
		out = binding
		return nil
	})
	return out, err
}

func (s *Service) UnbindRole(taskSelector, instanceID, role, actor string) ([]RoleBinding, error) {
	role = strings.TrimSpace(role)
	actor = strings.TrimSpace(actor)
	if role == "" {
		return nil, validationError("role", "role is required", "non-empty role", nil, "supply role")
	}
	var out []RoleBinding
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		inst, err := resolveInstanceSelectors(tx, taskSelector, instanceID)
		if err != nil {
			return err
		}
		if actor == "" {
			_, err = tx.Exec(`DELETE FROM workflow_role_bindings WHERE instance_id = ? AND role = ?`, inst.ID, role)
		} else {
			_, err = tx.Exec(`DELETE FROM workflow_role_bindings WHERE instance_id = ? AND role = ? AND actor = ?`, inst.ID, role, actor)
		}
		if err != nil {
			return err
		}
		out, err = listRoleBindingsForInstance(tx, inst.ID)
		return err
	})
	return out, err
}

func (s *Service) SetRoleBindings(taskSelector, instanceID string, roleMap map[string]string) ([]RoleBinding, error) {
	if roleMap == nil {
		roleMap = map[string]string{}
	}
	for role, actor := range roleMap {
		if err := validateRoleBindingInput(role, actor, "required"); err != nil {
			return nil, err
		}
	}
	var out []RoleBinding
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		inst, err := resolveInstanceSelectors(tx, taskSelector, instanceID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM workflow_role_bindings WHERE instance_id = ?`, inst.ID); err != nil {
			return err
		}
		now := s.now().Format(time.RFC3339)
		for role, actor := range roleMap {
			_, err := tx.Exec(`
				INSERT INTO workflow_role_bindings (instance_id, role, actor, binding_mode, bound_at)
				VALUES (?, ?, ?, 'required', ?)
			`, inst.ID, strings.TrimSpace(role), strings.TrimSpace(actor), now)
			if err != nil {
				return err
			}
		}
		out, err = listRoleBindingsForInstance(tx, inst.ID)
		return err
	})
	return out, err
}

func validateRoleBindingInput(role, actor, mode string) error {
	if strings.TrimSpace(role) == "" {
		return validationError("role", "role is required", "non-empty role", nil, "supply role")
	}
	if strings.TrimSpace(actor) == "" {
		return validationError("actor", "actor is required", "non-empty actor", nil, "supply actor")
	}
	switch strings.TrimSpace(mode) {
	case "", "required", "optional", "auto":
		return nil
	default:
		return validationError("bindingMode", "bindingMode must be required, optional, or auto", "required|optional|auto", []string{"required", "optional", "auto"}, "use a supported bindingMode")
	}
}

func listRoleBindingsForInstance(q rowsQueryer, instanceID string) ([]RoleBinding, error) {
	rows, err := q.Query(`
		SELECT instance_id, role, actor, COALESCE(delivery_ref,''), COALESCE(lane,''), binding_mode, bound_at
		FROM workflow_role_bindings
		WHERE instance_id = ?
		ORDER BY role, actor
	`, instanceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RoleBinding
	for rows.Next() {
		var binding RoleBinding
		if err := rows.Scan(&binding.InstanceID, &binding.Role, &binding.Actor, &binding.DeliveryRef, &binding.Lane, &binding.BindingMode, &binding.BoundAt); err != nil {
			return nil, err
		}
		out = append(out, binding)
	}
	return out, rows.Err()
}

func listRoleBindingsForInstanceRole(q rowsQueryer, instanceID, role string) ([]RoleBinding, error) {
	rows, err := q.Query(`
		SELECT instance_id, role, actor, COALESCE(delivery_ref,''), COALESCE(lane,''), binding_mode, bound_at
		FROM workflow_role_bindings
		WHERE instance_id = ? AND role = ?
		ORDER BY role, actor
	`, instanceID, role)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []RoleBinding
	for rows.Next() {
		var binding RoleBinding
		if err := rows.Scan(&binding.InstanceID, &binding.Role, &binding.Actor, &binding.DeliveryRef, &binding.Lane, &binding.BindingMode, &binding.BoundAt); err != nil {
			return nil, err
		}
		out = append(out, binding)
	}
	return out, rows.Err()
}

func getRoleBindingTx(tx *sql.Tx, instanceID, role, actor string) (*RoleBinding, error) {
	row := tx.QueryRow(`
		SELECT instance_id, role, actor, COALESCE(delivery_ref,''), COALESCE(lane,''), binding_mode, bound_at
		FROM workflow_role_bindings
		WHERE instance_id = ? AND role = ? AND actor = ?
	`, instanceID, role, actor)
	var binding RoleBinding
	if err := row.Scan(&binding.InstanceID, &binding.Role, &binding.Actor, &binding.DeliveryRef, &binding.Lane, &binding.BindingMode, &binding.BoundAt); err != nil {
		return nil, err
	}
	return &binding, nil
}

func resolveTaskUUIDQuery(q queryer, selector string) (string, error) {
	parsed := selectors.Parse(selector)
	if parsed.Type != selectors.TypeTask && parsed.Type != selectors.TypeAuto {
		return "", fmt.Errorf("expected task selector (t:), got %s selector", parsed.Type)
	}
	token := parsed.Token
	if expanded, ok := id.ExpandTaskID(token); ok {
		token = expanded
	}
	if strings.HasPrefix(token, "T-") {
		var uuid string
		err := q.QueryRow("SELECT uuid FROM tasks WHERE id = ?", token).Scan(&uuid)
		if err == nil {
			return uuid, nil
		}
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("task not found: %s", token)
		}
		return "", fmt.Errorf("database error: %w", err)
	}
	if len(token) == 36 && strings.Count(token, "-") == 4 {
		var uuid string
		err := q.QueryRow("SELECT uuid FROM tasks WHERE uuid = ?", token).Scan(&uuid)
		if err == nil {
			return uuid, nil
		}
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("task not found: %s", token)
		}
		return "", fmt.Errorf("database error: %w", err)
	}
	return resolveTaskUUIDByPathQuery(q, token)
}

func resolveTaskUUIDByPathQuery(q queryer, path string) (string, error) {
	segments := paths.SplitPath(path)
	if len(segments) == 0 {
		return "", fmt.Errorf("invalid path: empty")
	}
	var parentUUID *string
	if len(segments) > 1 {
		uuid, err := walkContainerPathQuery(q, paths.JoinPath(segments[:len(segments)-1]...))
		if err != nil {
			return "", err
		}
		parentUUID = &uuid
	}
	normalizedSlug, err := paths.NormalizeSlug(segments[len(segments)-1])
	if err != nil {
		return "", fmt.Errorf("invalid task slug %q: %w", segments[len(segments)-1], err)
	}
	var taskUUID string
	if parentUUID == nil {
		err = q.QueryRow(`
			SELECT uuid FROM tasks WHERE slug = ? AND project_uuid IN (
				SELECT uuid FROM containers WHERE kind = 'project'
			) LIMIT 1
		`, normalizedSlug).Scan(&taskUUID)
	} else {
		err = q.QueryRow(`SELECT uuid FROM tasks WHERE slug = ? AND project_uuid = ?`, normalizedSlug, *parentUUID).Scan(&taskUUID)
	}
	if err == nil {
		return taskUUID, nil
	}
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("task not found: %s", path)
	}
	return "", fmt.Errorf("database error: %w", err)
}

func walkContainerPathQuery(q queryer, path string) (string, error) {
	segments := paths.SplitPath(path)
	if len(segments) == 0 {
		return "", nil
	}
	var currentUUID *string
	for i, segment := range segments {
		slug, err := paths.NormalizeSlug(segment)
		if err != nil {
			return "", fmt.Errorf("invalid slug %q: %w", segment, err)
		}
		query := `SELECT uuid FROM containers WHERE slug = ? AND `
		args := []interface{}{slug}
		if currentUUID == nil {
			query += `parent_uuid = (SELECT uuid FROM containers WHERE kind = 'root')`
		} else {
			query += `parent_uuid = ?`
			args = append(args, *currentUUID)
		}
		var uuid string
		err = q.QueryRow(query, args...).Scan(&uuid)
		if err == nil {
			currentUUID = &uuid
			continue
		}
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("container not found: %s", paths.JoinPath(segments[:i+1]...))
		}
		return "", fmt.Errorf("database error: %w", err)
	}
	return *currentUUID, nil
}

func latestInstanceByTaskUUIDQuery(q queryer, taskUUID string) (*Instance, error) {
	row := q.QueryRow(`
		SELECT id, task_uuid, task_ref, COALESCE(project_id,''), template_id, template_version, template_hash,
		       status, COALESCE(phase,''), COALESCE(outcome,''), revision, context_hash,
		       task_doc_etag, task_doc_hash, created_at, updated_at, COALESCE(closed_at,'')
		FROM workflow_instances
		WHERE task_uuid = ?
		ORDER BY created_at DESC LIMIT 1
	`, taskUUID)
	return scanInstance(row)
}

func instanceByIDQuery(q queryer, instanceID string) (*Instance, error) {
	row := q.QueryRow(`
		SELECT id, task_uuid, task_ref, COALESCE(project_id,''), template_id, template_version, template_hash,
		       status, COALESCE(phase,''), COALESCE(outcome,''), revision, context_hash,
		       task_doc_etag, task_doc_hash, created_at, updated_at, COALESCE(closed_at,'')
		FROM workflow_instances WHERE id = ?
	`, instanceID)
	return scanInstance(row)
}

func showTemplateTx(q queryer, ref string) (*Template, string, error) {
	id, version, err := parseTemplateRef(ref)
	if err != nil {
		return nil, "", err
	}
	var definition, hash string
	if err := q.QueryRow(`SELECT definition_json, hash FROM workflow_templates WHERE id = ? AND version = ?`, id, version).Scan(&definition, &hash); err != nil {
		if err == sql.ErrNoRows {
			return nil, "", fmt.Errorf("template not found: %s", ref)
		}
		return nil, "", err
	}
	tpl, _, err := ParseTemplate([]byte(definition))
	return tpl, hash, err
}

func (s *Service) AddEvidence(params AddEvidenceParams) (*Evidence, error) {
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
		var kindSpec *KindSpec
		if spec, ok := tpl.EvidenceKinds[params.Kind]; ok {
			kindSpec = &spec
		}
		// E1 — supplied-role conformance: reject when the kind declares producers
		// and the supplied role is not among them. Not an authenticated boundary.
		if err := validateProducibleBy(params.Kind, kindSpec, params.Role); err != nil {
			return err
		}
		facts, err := parseAndValidateEvidenceFacts(params.Kind, params.Facts, kindSpec)
		if err != nil {
			return err
		}
		var dataArg interface{}
		var dataRaw json.RawMessage
		if strings.TrimSpace(params.Data) != "" {
			if !json.Valid([]byte(params.Data)) {
				return validationError("data", "data must be valid JSON"+jsonLocationSuffix(json.Unmarshal([]byte(params.Data), new(json.RawMessage))), "valid JSON", nil, "fix the JSON syntax in --data")
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
				return err
			}
			if replayed != nil {
				ev = replayed
				return nil
			}
		}
		task, err := loadTaskDoc(tx, inst.TaskUUID)
		if err != nil {
			return err
		}
		// E3 — data-linkage correctness: declared refs must resolve to a live
		// evidence id on this instance before the new row is written.
		if kindSpec != nil && len(kindSpec.LinkageRefs) > 0 {
			existing, err := listEvidenceTx(tx, inst.ID)
			if err != nil {
				return err
			}
			if err := validateLinkageRefs(existing, kindSpec, dataRaw); err != nil {
				return err
			}
		}
		id, err := nextSeqID(tx, "workflow_evidence_seq", "ev")
		if err != nil {
			return err
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
		now := s.now().Format(time.RFC3339)
		_, err = tx.Exec(`
			INSERT INTO workflow_evidence (id, instance_id, kind, ref, summary, facts_json, data_json, source_json, actor, role, run_id, task_etag_at_production, task_hash_at_production, produced_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, id, inst.ID, params.Kind, params.Ref, nullIfEmpty(params.Summary), factsArg, dataArg, string(sourceJSON), emptyToNil(params.Actor), emptyToNil(params.Role), emptyToNil(params.RunID), fmt.Sprint(task.ETag), taskHashAtProduction, now)
		if err != nil {
			return err
		}
		if params.Kind == "delegated_task_manifest" && dataArg != nil {
			if err := createDelegatedTaskClosureObligationsTx(tx, inst.ID, id, string(dataRaw)); err != nil {
				return err
			}
		}
		if params.Kind == "coordinator_runbook" && dataArg != nil {
			if err := createCoordinatorSmokeExecutionObligationTx(tx, inst.ID, id); err != nil {
				return err
			}
		}
		if params.Kind == "completion_claim" {
			if err := createObserverCompletionReviewObligationTx(tx, inst.ID, id); err != nil {
				return err
			}
		}
		if params.Kind == "observer_completion_review" {
			_, _ = tx.Exec(`
				UPDATE workflow_effects
				SET status = 'delivered', delivered_at = COALESCE(delivered_at, ?), updated_at = ?
				WHERE instance_id = ? AND kind = 'request_observer_review' AND status IN ('pending','failed','leased')
			`, now, now, inst.ID)
		}
		evidence, err := listEvidenceTx(tx, inst.ID)
		if err != nil {
			return err
		}
		obligations, err := listObligationsTx(tx, inst.ID, false)
		if err != nil {
			return err
		}
		effects, err := listEffectsTx(tx, inst.ID, false)
		if err != nil {
			return err
		}
		inst.ContextHash = contextHash(inst.TemplateHash, inst.State(), inst.Revision, taskDocHash(task), evidence, obligations, effects)
		inst.TaskDocEtag = fmt.Sprint(task.ETag)
		inst.TaskDocHash = taskDocHash(task)
		inst.UpdatedAt = now
		if _, err := tx.Exec(`UPDATE workflow_instances SET context_hash = ?, task_doc_etag = ?, task_doc_hash = ?, updated_at = ? WHERE id = ?`, inst.ContextHash, inst.TaskDocEtag, inst.TaskDocHash, inst.UpdatedAt, inst.ID); err != nil {
			return err
		}
		if err := updateTaskWorkflowMeta(tx, inst.TaskUUID, *inst, params.Actor); err != nil {
			return err
		}
		ev = &Evidence{ID: id, InstanceID: inst.ID, Kind: params.Kind, Ref: params.Ref, Summary: params.Summary, Facts: factsRaw, Data: dataRaw, Source: sourceJSON, Actor: params.Actor, Role: params.Role, RunID: params.RunID, ContentHash: strings.TrimSpace(params.ContentHash), Build: normalizedEvidenceBuild(params.Build), TaskEtagAtProduction: fmt.Sprint(task.ETag), TaskHashAtProduction: taskHashAtProduction, ProducedAt: now}
		if params.IdempotencyKey != "" {
			if err := storeEvidenceResult(tx, inst.ID, params.IdempotencyKey, requestHash, ev); err != nil {
				return err
			}
		}
		return nil
	})
	return ev, err
}

func evidenceAddRequestHash(params AddEvidenceParams, factsRaw, dataRaw json.RawMessage) string {
	req := struct {
		Kind        string          `json:"kind"`
		Ref         string          `json:"ref"`
		Summary     string          `json:"summary,omitempty"`
		Facts       json.RawMessage `json:"facts,omitempty"`
		Data        json.RawMessage `json:"data,omitempty"`
		Actor       string          `json:"actor,omitempty"`
		Role        string          `json:"role,omitempty"`
		RunID       string          `json:"runId,omitempty"`
		ContentHash string          `json:"contentHash,omitempty"`
		Build       *EvidenceBuild  `json:"build,omitempty"`
	}{
		Kind: params.Kind, Ref: params.Ref, Summary: params.Summary,
		Facts: factsRaw, Data: dataRaw, Actor: params.Actor, Role: params.Role, RunID: params.RunID,
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

func (s *Service) ListEvidence(taskSelector string) ([]Evidence, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT id, instance_id, kind, ref, COALESCE(summary,''), COALESCE(facts_json,''), COALESCE(data_json,''), source_json,
		       COALESCE(actor,''), COALESCE(role,''), COALESCE(run_id,''), COALESCE(task_etag_at_production,''), COALESCE(task_hash_at_production,''), produced_at
		FROM workflow_evidence WHERE instance_id = ? ORDER BY produced_at, id
	`, inst.ID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanEvidenceRows(rows)
}

func listEvidenceTx(tx *sql.Tx, instanceID string) ([]Evidence, error) {
	rows, err := tx.Query(`
		SELECT id, instance_id, kind, ref, COALESCE(summary,''), COALESCE(facts_json,''), COALESCE(data_json,''), source_json,
		       COALESCE(actor,''), COALESCE(role,''), COALESCE(run_id,''), COALESCE(task_etag_at_production,''), COALESCE(task_hash_at_production,''), produced_at
		FROM workflow_evidence WHERE instance_id = ? ORDER BY produced_at, id
	`, instanceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanEvidenceRows(rows)
}

func (s *Service) ShowEvidence(id string) (*Evidence, error) {
	rows, err := s.db.Query(`
		SELECT id, instance_id, kind, ref, COALESCE(summary,''), COALESCE(facts_json,''), COALESCE(data_json,''), source_json,
		       COALESCE(actor,''), COALESCE(role,''), COALESCE(run_id,''), COALESCE(task_etag_at_production,''), COALESCE(task_hash_at_production,''), produced_at
		FROM workflow_evidence WHERE id = ?
	`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out, err := scanEvidenceRows(rows)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("evidence not found: %s", id)
	}
	return &out[0], nil
}

func scanEvidenceRows(rows *sql.Rows) ([]Evidence, error) {
	var out []Evidence
	for rows.Next() {
		var e Evidence
		var facts, data, source string
		if err := rows.Scan(&e.ID, &e.InstanceID, &e.Kind, &e.Ref, &e.Summary, &facts, &data, &source, &e.Actor, &e.Role, &e.RunID, &e.TaskEtagAtProduction, &e.TaskHashAtProduction, &e.ProducedAt); err != nil {
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
	ev, err := s.ListEvidence(taskSelector)
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

func findTransition(tpl *Template, id string) (*TransitionSpec, error) {
	for i := range tpl.Transitions {
		if tpl.Transitions[i].ID == id {
			return &tpl.Transitions[i], nil
		}
	}
	return nil, fmt.Errorf("transition not found: %s", id)
}

func roleAllowed(role string, by []string) bool {
	role = strings.TrimSpace(role)
	if role == "" {
		return false
	}
	for _, b := range by {
		if b == role {
			return true
		}
	}
	return false
}

func roleBindingAllowed(q queryer, inst *Instance, tpl *Template, role, actor string) bool {
	role = strings.TrimSpace(role)
	actor = strings.TrimSpace(actor)
	if role == "" || actor == "" {
		return false
	}
	if role == "system" || role == "supervisor" {
		return true
	}
	if tpl != nil {
		if spec, ok := tpl.Roles[role]; ok && len(spec.Actors) > 0 {
			for _, allowed := range spec.Actors {
				if allowed == "*" || allowed == actor {
					return true
				}
			}
			return false
		}
	}
	if inst == nil {
		return false
	}
	var count int
	if err := q.QueryRow(`SELECT COUNT(*) FROM workflow_role_bindings WHERE instance_id = ? AND role = ?`, inst.ID, role).Scan(&count); err != nil {
		return false
	}
	if count == 0 {
		// Legacy/simple mode: require an authenticated actor and declared role, but do not
		// require pre-binding until a binding exists for this role.
		return true
	}
	var matched int
	if err := q.QueryRow(`SELECT COUNT(*) FROM workflow_role_bindings WHERE instance_id = ? AND role = ? AND actor = ?`, inst.ID, role, actor).Scan(&matched); err != nil {
		return false
	}
	return matched > 0
}

func (s *Service) transitionOwners(inst *Instance, tpl *Template, tr TransitionSpec, requestedRole string) ([]ActionOwner, []Blocker) {
	roles, blockers := transitionCandidateRoles(tr, requestedRole)
	if len(blockers) > 0 {
		return nil, blockers
	}
	var owners []ActionOwner
	var bindingBlockers []Blocker
	seen := map[string]bool{}
	for _, role := range roles {
		roleOwners, err := s.ownersForRole(inst, tpl, role)
		if err != nil {
			bindingBlockers = append(bindingBlockers, Blocker{Kind: "role_binding", Ref: role, Message: err.Error()})
			continue
		}
		if len(roleOwners) == 0 {
			bindingBlockers = append(bindingBlockers, Blocker{Kind: "role_binding", Ref: role, Message: fmt.Sprintf("role %s has no eligible bound actors", role)})
			continue
		}
		for _, owner := range roleOwners {
			key := owner.Role + "\x00" + owner.Actor + "\x00" + owner.DeliveryRef + "\x00" + owner.Lane
			if seen[key] {
				continue
			}
			seen[key] = true
			owners = append(owners, owner)
		}
	}
	if len(owners) == 0 && len(bindingBlockers) > 0 {
		return nil, bindingBlockers
	}
	return owners, nil
}

func transitionCandidateRoles(tr TransitionSpec, requestedRole string) ([]string, []Blocker) {
	requestedRole = strings.TrimSpace(requestedRole)
	if requestedRole != "" {
		if !roleAllowed(requestedRole, tr.By) {
			return nil, []Blocker{{Kind: "role", Ref: requestedRole, Message: "role is not allowed for transition"}}
		}
		return []string{requestedRole}, nil
	}
	roles := make([]string, 0, len(tr.By))
	seen := map[string]bool{}
	add := func(role string) {
		role = strings.TrimSpace(role)
		if role == "" || seen[role] || !roleAllowed(role, tr.By) {
			return
		}
		seen[role] = true
		roles = append(roles, role)
	}
	if tr.Responsibility != nil {
		add(tr.Responsibility.Role)
	}
	for _, role := range tr.By {
		add(role)
	}
	return roles, nil
}

func (s *Service) ownersForRole(inst *Instance, tpl *Template, role string) ([]ActionOwner, error) {
	role = strings.TrimSpace(role)
	if role == "" {
		return nil, fmt.Errorf("role is required")
	}
	if role == "system" || role == "supervisor" {
		return []ActionOwner{{Role: role}}, nil
	}
	if tpl != nil {
		if spec, ok := tpl.Roles[role]; ok && len(spec.Actors) > 0 {
			owners := make([]ActionOwner, 0, len(spec.Actors))
			for _, actor := range spec.Actors {
				actor = strings.TrimSpace(actor)
				if actor == "" {
					continue
				}
				owner := ActionOwner{Role: role}
				if actor != "*" {
					owner.Actor = actor
				}
				owners = append(owners, owner)
			}
			return owners, nil
		}
	}
	if inst == nil {
		return nil, fmt.Errorf("workflow instance is required")
	}
	rows, err := s.db.Query(`
		SELECT actor, COALESCE(delivery_ref,''), COALESCE(lane,'')
		FROM workflow_role_bindings
		WHERE instance_id = ? AND role = ?
		ORDER BY bound_at DESC, actor
	`, inst.ID, role)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var owners []ActionOwner
	for rows.Next() {
		var owner ActionOwner
		owner.Role = role
		if err := rows.Scan(&owner.Actor, &owner.DeliveryRef, &owner.Lane); err != nil {
			return nil, err
		}
		owners = append(owners, owner)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(owners) == 0 {
		// Legacy/simple mode matches roleBindingAllowed: no binding rows for this
		// role means a declared role plus authenticated actor is sufficient.
		return []ActionOwner{{Role: role}}, nil
	}
	return owners, nil
}

func transitionActionID(transitionID string, owner ActionOwner, ownerCount int) string {
	id := "transition_" + transitionID
	if ownerCount <= 1 {
		return id
	}
	suffix := owner.Role
	if owner.Actor != "" {
		suffix += "_" + owner.Actor
	}
	return id + "_" + sanitizeActionID(suffix)
}

func sanitizeActionID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func transitionCommand(taskRef, transitionID string, owner ActionOwner, revision int64) string {
	cmd := fmt.Sprintf("wrkf transition %s %s --role %s", strings.TrimPrefix(taskRef, "wrkq:"), transitionID, owner.Role)
	if owner.Actor != "" {
		cmd += fmt.Sprintf(" --actor %s", owner.Actor)
	}
	cmd += fmt.Sprintf(" --expect-revision %d", revision)
	return cmd
}

type evalContext struct {
	Evidence    []Evidence
	Obligations []Obligation
	Checks      map[string]CheckRun
	Facts       map[string]interface{}
	Task        *taskDoc
	State       State
}

func evalPredicate(p Predicate, ctx evalContext) bool {
	if p.Always != nil {
		return *p.Always
	}
	if p.Otherwise != nil {
		return *p.Otherwise
	}
	if len(p.All) > 0 {
		for _, child := range p.All {
			if !evalPredicate(child, ctx) {
				return false
			}
		}
		return true
	}
	if len(p.Any) > 0 {
		for _, child := range p.Any {
			if evalPredicate(child, ctx) {
				return true
			}
		}
		return false
	}
	if p.Not != nil {
		return !evalPredicate(*p.Not, ctx)
	}
	if p.EvidenceExists != nil {
		return matchEvidenceRequirement(ctx.Evidence, EvidenceRequirementSpec{Kind: p.EvidenceExists.Kind, Facts: p.EvidenceExists.Facts}).OK
	}
	if p.ObligationStatus != nil {
		for _, o := range ctx.Obligations {
			if p.ObligationStatus.ID != "" && o.ID != p.ObligationStatus.ID {
				continue
			}
			if p.ObligationStatus.Kind != "" && o.Kind != p.ObligationStatus.Kind {
				continue
			}
			if o.Status == p.ObligationStatus.Is {
				return true
			}
		}
		return false
	}
	if p.CheckVerdict != nil {
		c, ok := ctx.Checks[p.CheckVerdict.Check]
		return ok && c.Verdict == p.CheckVerdict.Is
	}
	if p.CheckOutcome != nil {
		c, ok := ctx.Checks[p.CheckOutcome.Check]
		return ok && c.Outcome == p.CheckOutcome.Is
	}
	if p.FactEquals != nil {
		return reflect.DeepEqual(resolveFact(ctx, p.FactEquals.Path), p.FactEquals.Value)
	}
	return false
}

func resolveFact(ctx evalContext, path string) interface{} {
	if ctx.Task != nil {
		switch path {
		case "task.state":
			return ctx.Task.State
		case "task.id":
			return ctx.Task.ID
		case "task.has_specification":
			// Derived fact: true iff the task carries a non-empty specification.
			// Lets transition outcomes branch on the triage deliverable without a
			// coordinator-run check (checks do not auto-run in the action flow).
			return strings.TrimSpace(ctx.Task.Specification) != ""
		}
	}
	switch path {
	case "workflow.status":
		return ctx.State.Status
	case "workflow.phase":
		return ctx.State.Phase
	case "workflow.outcome":
		return ctx.State.Outcome
	}
	if ctx.Facts != nil {
		return ctx.Facts[path]
	}
	return nil
}

func (s *Service) Next(taskSelector, role string) (*NextActionResponse, error) {
	inst, err := s.LatestInstance(taskSelector)
	if err != nil {
		return nil, err
	}
	tpl, _, err := s.ShowTemplate(inst.TemplateID + "@" + inst.TemplateVersion)
	if err != nil {
		return nil, err
	}
	ev, _ := s.ListEvidence(taskSelector)
	obl, _ := s.ListObligations(taskSelector, true)
	eff, _ := s.ListEffects(taskSelector, false)
	openObl := filterOpenObligations(obl)
	pendingEff := filterPendingEffects(eff)
	resp := &NextActionResponse{Actions: []NextAction{}, BlockedTransitions: []BlockedTransition{}, OpenObligations: openObl, PendingEffects: pendingEff}
	resp.Instance.ID = inst.ID
	resp.Instance.TaskRef = inst.TaskRef
	resp.Instance.Template.ID = inst.TemplateID
	resp.Instance.Template.Version = inst.TemplateVersion
	resp.Instance.Template.Hash = inst.TemplateHash
	resp.Instance.State = inst.State()
	resp.Instance.Revision = inst.Revision
	resp.Instance.ContextHash = inst.ContextHash
	resp.Instance.TaskDoc.Etag = inst.TaskDocEtag
	resp.Instance.TaskDoc.Hash = inst.TaskDocHash
	task, _ := loadTaskDoc(s.db, inst.TaskUUID)
	if task != nil {
		resp.Instance.Stale = taskDocHash(task) != inst.TaskDocHash
	}
	if inst.Status == "closed" {
		resp.Actions = []NextAction{}
		return resp, nil
	}
	for _, o := range openObl {
		switch o.Kind {
		case "await_subordinate_closure":
			resp.Actions = append(resp.Actions, NextAction{
				ID:    "await_" + o.ID,
				Kind:  "await_subordinate_closure",
				Mode:  "deterministic",
				Owner: ActionOwner{Role: "coordinator"},
				Rank:  85,
				Why:   o.Reason,
				Guardrails: Guardrails{
					Hard:     []string{"do not record closure_evidence for a subordinate until its wrkq task reaches a terminal state"},
					Warnings: []string{},
				},
			})
		case "await_coordinator_smoke_execution":
			resp.Actions = append(resp.Actions, NextAction{
				ID:    "execute_" + o.ID,
				Kind:  "execute_coordinator_smoke",
				Mode:  "deterministic",
				Owner: ActionOwner{Role: "coordinator"},
				Rank:  86,
				Why:   o.Reason,
				Guardrails: Guardrails{
					Hard:     []string{"execute the locked runbook with fresh artifacts before recording coordinator_smoke_execution"},
					Warnings: []string{},
				},
			})
		case "await_observer_completion_review":
			resp.Actions = append(resp.Actions, NextAction{
				ID:    "request_" + o.ID,
				Kind:  "request_observer_review",
				Mode:  "deterministic",
				Owner: ActionOwner{Role: "observer"},
				Rank:  92,
				Why:   o.Reason,
				Guardrails: Guardrails{
					Hard: []string{
						"observer must be outside the coordinator loop",
						"observer must judge against the original task body, not only coordinator-authored criteria",
						"do not accept coordinator claims that requested functionality can be bypassed without an explicit human or supervisor override",
					},
					Warnings: []string{},
				},
			})
		case "address_observer_rejection":
			resp.Actions = append(resp.Actions, NextAction{
				ID:    "address_" + o.ID,
				Kind:  "address_observer_rejection",
				Mode:  "deterministic",
				Owner: ActionOwner{Role: "coordinator"},
				Rank:  91,
				Why:   o.Reason,
				Guardrails: Guardrails{
					Hard:     []string{"submit a revised completion_claim referencing the rejected claim and observer review"},
					Warnings: []string{},
				},
			})
		}
	}
	facts := taskFacts(task)
	for _, e := range pendingEff {
		if e.Status != "pending" && e.Status != "failed" {
			continue
		}
		if e.Kind != "request_observer_review" {
			continue
		}
		role := effectRole(&e)
		if role == "" {
			continue
		}
		binding, bindErr := s.latestRunForRole(inst.ID, role)
		if bindErr != nil {
			resp.Actions = append(resp.Actions, NextAction{
				ID:         "bind_" + role,
				Kind:       "bind_role",
				Mode:       "deterministic",
				Owner:      ActionOwner{Role: "coordinator"},
				Rank:       95,
				Why:        fmt.Sprintf("effect %s requires a bound %s delivery handle", e.ID, role),
				Command:    fmt.Sprintf("wrkf run bind %s %s <agent@project:%s~%s>", strings.TrimPrefix(inst.TaskRef, "wrkq:"), role, strings.TrimPrefix(inst.TaskRef, "wrkq:"), role),
				Guardrails: Guardrails{Hard: []string{"bind role to a project/task-scoped handle before delivering the effect"}, Warnings: []string{}},
			})
			continue
		}
		resp.Actions = append(resp.Actions, NextAction{
			ID:         "deliver_" + e.ID,
			Kind:       "deliver_effect",
			Mode:       "deterministic",
			Owner:      ActionOwner{Role: "coordinator", Actor: binding.Actor, DeliveryRef: binding.DeliveryRef, Lane: binding.Lane},
			Rank:       94,
			Why:        fmt.Sprintf("deliver pending %s effect to %s", e.Kind, binding.DeliveryRef),
			Command:    fmt.Sprintf("wrkf effect deliver %s", e.ID),
			Guardrails: Guardrails{Hard: []string{"deliver through wrkf effect handler; do not hand-compose an out-of-band dispatch"}, Warnings: []string{}},
		})
	}
	for _, tr := range tpl.Transitions {
		if !stateMatches(*inst, tr.From) {
			continue
		}
		owners, ownerBlockers := s.transitionOwners(inst, tpl, tr, role)
		ownerRole := ""
		if len(owners) > 0 {
			ownerRole = owners[0].Role
		} else if role != "" {
			ownerRole = role
		} else if tr.Responsibility != nil && tr.Responsibility.Role != "" {
			ownerRole = tr.Responsibility.Role
		} else if len(tr.By) > 0 {
			ownerRole = tr.By[0]
		}
		if len(ownerBlockers) > 0 {
			resp.BlockedTransitions = append(resp.BlockedTransitions, BlockedTransition{ID: tr.ID, Role: ownerRole, BlocksOn: ownerBlockers})
			continue
		}
		blockers := transitionBlockers(tr, ev, obl, taskDocHashOrEmpty(task))
		if len(blockers) > 0 {
			resp.BlockedTransitions = append(resp.BlockedTransitions, BlockedTransition{ID: tr.ID, Role: ownerRole, BlocksOn: blockers})
			for _, b := range blockers {
				if b.Kind == "evidence" {
					resp.Actions = append(resp.Actions, NextAction{ID: "collect_" + b.Ref, Kind: "collect_evidence", Mode: "deterministic", Owner: ActionOwner{Role: ownerRole}, Rank: 80, Why: b.Message, Unblocks: []string{tr.ID}, Guardrails: Guardrails{Hard: []string{"reference source truth as evidence"}, Warnings: []string{}}})
				}
				if b.Kind == "obligation" {
					resp.Actions = append(resp.Actions, NextAction{ID: "satisfy_" + b.Ref, Kind: "satisfy_obligation", Mode: "deterministic", Owner: ActionOwner{Role: ownerRole}, Rank: 50, Why: b.Message, Unblocks: []string{tr.ID}, Guardrails: Guardrails{Hard: []string{"satisfy or waive the blocking obligation"}, Warnings: []string{}}})
				}
			}
			continue
		}
		checks := map[string]CheckRun{}
		for _, checkID := range tr.Checks {
			if latest, ok := latestCheckFor(s.db, inst.ID, tr.ID, checkID); ok {
				checks[checkID] = latest
			}
		}
		checkBlockers := checkCommitBlockers(tpl, tr, ev, facts, inst, task, ev, obl, checks, "", ownerRole, s.db)
		if len(checkBlockers) > 0 {
			resp.BlockedTransitions = append(resp.BlockedTransitions, BlockedTransition{ID: tr.ID, Role: ownerRole, BlocksOn: checkBlockers})
			addedEvidenceActions := map[string]bool{}
			for _, b := range checkBlockers {
				if b.Kind == "check_input" && strings.HasPrefix(b.Ref, "evidence:") {
					kind := strings.TrimPrefix(b.Ref, "evidence:")
					if !addedEvidenceActions[kind] {
						resp.Actions = append(resp.Actions, NextAction{ID: "collect_" + kind, Kind: "collect_evidence", Mode: "deterministic", Owner: ActionOwner{Role: ownerRole}, Rank: 80, Why: b.Message, Unblocks: []string{tr.ID}, Guardrails: Guardrails{Hard: []string{"reference source truth as evidence before running the check"}, Warnings: []string{}}})
						addedEvidenceActions[kind] = true
					}
					continue
				}
				if b.Kind == "check" || b.Kind == "stale_check" {
					checkID := strings.TrimPrefix(b.Ref, "check:")
					resp.Actions = append(resp.Actions, NextAction{ID: "run_check_" + checkID, Kind: "run_check", Mode: "deterministic", Owner: ActionOwner{Role: ownerRole}, Rank: 90, Why: b.Message, Unblocks: []string{tr.ID}, Command: fmt.Sprintf("wrkf check run %s %s", strings.TrimPrefix(inst.TaskRef, "wrkq:"), tr.ID), Guardrails: Guardrails{Hard: []string{"do not commit stale check results"}, Warnings: []string{}}})
				}
			}
			continue
		}
		if sodBlockers := separationOfDutyBlockers(tr, ev, ""); len(sodBlockers) > 0 {
			resp.BlockedTransitions = append(resp.BlockedTransitions, BlockedTransition{ID: tr.ID, Role: ownerRole, BlocksOn: sodBlockers})
			continue
		}
		ctx := evalContext{Evidence: ev, Obligations: obl, Checks: checks, Facts: facts, Task: task, State: inst.State()}
		guardBlocked := false
		for _, guard := range tr.Guards {
			if !evalPredicate(guard, ctx) {
				resp.BlockedTransitions = append(resp.BlockedTransitions, BlockedTransition{ID: tr.ID, Role: ownerRole, BlocksOn: []Blocker{{Kind: "guard", Message: "transition guard failed"}}})
				guardBlocked = true
				break
			}
		}
		if guardBlocked {
			continue
		}
		var chosen *OutcomeCase
		for i := range tr.Outcomes {
			out := &tr.Outcomes[i]
			if evalPredicate(out.When, ctx) {
				chosen = out
				break
			}
		}
		if chosen == nil {
			resp.BlockedTransitions = append(resp.BlockedTransitions, BlockedTransition{ID: tr.ID, Role: ownerRole, BlocksOn: []Blocker{{Kind: "outcome", Message: "no transition outcome matched"}}})
			continue
		}
		if chosen.To.Status == "closed" {
			depBlockers, depErr := taskRelationBlockers(s.db, inst.TaskUUID)
			if depErr != nil {
				resp.BlockedTransitions = append(resp.BlockedTransitions, BlockedTransition{ID: tr.ID, Role: ownerRole, BlocksOn: []Blocker{{Kind: "task_dependency", Message: depErr.Error()}}})
				continue
			}
			if len(depBlockers) > 0 {
				resp.BlockedTransitions = append(resp.BlockedTransitions, BlockedTransition{ID: tr.ID, Role: ownerRole, BlocksOn: depBlockers})
				continue
			}
		}
		if postBlockers := postconditionBlockers(inst, tr, chosen, ev, obl, checks, task); len(postBlockers) > 0 {
			resp.BlockedTransitions = append(resp.BlockedTransitions, BlockedTransition{ID: tr.ID, Role: ownerRole, BlocksOn: postBlockers})
			continue
		}
		expected := chosen.To
		for _, owner := range owners {
			if sodBlockers := separationOfDutyBlockers(tr, ev, owner.Actor); len(sodBlockers) > 0 {
				resp.BlockedTransitions = append(resp.BlockedTransitions, BlockedTransition{ID: tr.ID, Role: owner.Role, BlocksOn: sodBlockers})
				continue
			}
			resp.Actions = append(resp.Actions, NextAction{ID: transitionActionID(tr.ID, owner, len(owners)), Kind: "transition", Mode: "deterministic", Owner: owner, Rank: 100, Why: "transition is legal and prerequisites are satisfied", Command: transitionCommand(inst.TaskRef, tr.ID, owner, inst.Revision), ExpectedState: &expected, Guardrails: Guardrails{Hard: []string{"provide expected revision"}, Warnings: []string{}}})
		}
	}
	sort.SliceStable(resp.Actions, func(i, j int) bool { return resp.Actions[i].Rank > resp.Actions[j].Rank })
	return resp, nil
}

func checkCommitBlockers(tpl *Template, tr TransitionSpec, ev []Evidence, facts map[string]interface{}, inst *Instance, task *taskDoc, currentEv []Evidence, currentObl []Obligation, checks map[string]CheckRun, actor, role string, database *db.DB) []Blocker {
	var blockers []Blocker
	haveEvidence := map[string]bool{}
	for _, e := range ev {
		haveEvidence[e.Kind] = true
	}
	for _, checkID := range tr.Checks {
		check, ok := tpl.Checks[checkID]
		if !ok {
			blockers = append(blockers, Blocker{Kind: "check", Ref: "check:" + checkID, Message: "transition references missing check"})
			continue
		}
		for _, kind := range checkRequiredEvidenceKinds(checkID, check, facts) {
			if !haveEvidence[kind] {
				blockers = append(blockers, Blocker{Kind: "check_input", Ref: "evidence:" + kind, Message: fmt.Sprintf("%s requires %s evidence before check %s can pass", tr.ID, kind, checkID)})
			}
		}
		cr, ok := checks[checkID]
		if !ok && database != nil {
			cr, ok = latestCheckFor(database, inst.ID, tr.ID, checkID)
		}
		if !ok {
			blockers = append(blockers, Blocker{Kind: "check", Ref: "check:" + checkID, Message: fmt.Sprintf("%s requires latest %s check to pass", tr.ID, checkID)})
			continue
		}
		currentHash := currentCheckInputHash(inst, &tr, cr.Actor, cr.Role, task, currentEv, currentObl)
		if cr.InputHash != "" && currentHash != "" && cr.InputHash != currentHash {
			blockers = append(blockers, Blocker{Kind: "stale_check", Ref: "check:" + checkID, Message: fmt.Sprintf("%s check %s was produced from stale inputs", tr.ID, checkID)})
			continue
		}
		if cr.Verdict != "pass" {
			blockers = append(blockers, Blocker{Kind: "check", Ref: "check:" + checkID, Message: fmt.Sprintf("%s requires latest %s check to pass", tr.ID, checkID)})
		}
	}
	return blockers
}

func checkRequiredEvidenceKinds(checkID string, check CheckSpec, facts map[string]interface{}) []string {
	set := map[string]bool{}
	add := func(kind string) {
		if strings.TrimSpace(kind) != "" {
			set[kind] = true
		}
	}
	add(check.EvidenceKind)
	for _, kind := range check.EvidenceKinds {
		add(kind)
	}
	switch check.HookID {
	case "plan_ready":
		add("source_spec")
		add("decomposition_plan")
	case "architect_verdict":
		add("architect_verdict")
	case "delegated_tasks_recorded":
		add("delegated_task_manifest")
	case "branch_ready":
		if factPathBool(facts, "branch.required") {
			add("branch_evidence")
		}
	case "stacked_terminal":
		add("stacked_terminal")
	case "red_verified":
		add("red_evidence")
		add("closure_evidence")
		add("artifact_verification")
	case "impl_verified":
		add("impl_evidence")
		add("closure_evidence")
		add("artifact_verification")
	case "live_smoke_verified":
		add("live_smoke_evidence")
	case "coordinator_smoke_verified":
		add("coordinator_runbook")
		add("coordinator_smoke_execution")
		add("impl_evidence")
	case "observer_review_verdict":
		add("completion_claim")
		add("observer_completion_review")
	case "cleanup_verified":
		add("cleanup_evidence")
	case "report_ready":
		add("report_evidence")
	}
	switch checkID {
	case "plan_ready":
		add("source_spec")
		add("decomposition_plan")
	case "architect_verdict":
		add("architect_verdict")
	case "delegated_tasks_recorded":
		add("delegated_task_manifest")
	case "branch_ready":
		if factPathBool(facts, "branch.required") {
			add("branch_evidence")
		}
	case "stacked_terminal":
		add("stacked_terminal")
	case "red_verified":
		add("red_evidence")
		add("closure_evidence")
		add("artifact_verification")
	case "impl_verified":
		add("impl_evidence")
		add("closure_evidence")
		add("artifact_verification")
	case "live_smoke_verified":
		add("live_smoke_evidence")
	case "coordinator_smoke_verified":
		add("coordinator_runbook")
		add("coordinator_smoke_execution")
		add("impl_evidence")
	case "observer_review_verdict":
		add("completion_claim")
		add("observer_completion_review")
	case "cleanup_verified":
		add("cleanup_evidence")
	case "report_ready":
		add("report_evidence")
	}
	out := make([]string, 0, len(set))
	for kind := range set {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

func factPathBool(facts map[string]interface{}, path string) bool {
	var current interface{} = facts
	for _, part := range strings.Split(path, ".") {
		m, ok := current.(map[string]interface{})
		if !ok {
			return false
		}
		current, ok = m[part]
		if !ok {
			return false
		}
	}
	b, _ := current.(bool)
	return b
}

func separationOfDutyBlockers(tr TransitionSpec, ev []Evidence, actor string) []Blocker {
	if tr.SeparationOfDuty == nil {
		return nil
	}
	var blockers []Blocker
	for _, kind := range tr.SeparationOfDuty.DistinctActorFromEvidence {
		latest, ok := latestEvidenceByKind(ev, kind)
		if !ok || strings.TrimSpace(actor) == "" || strings.TrimSpace(latest.Actor) == "" {
			continue
		}
		if latest.Actor == actor {
			blockers = append(blockers, Blocker{Kind: "separation_of_duty", Ref: latest.ID, Message: fmt.Sprintf("transition actor must differ from %s evidence producer", kind)})
		}
	}
	for _, pair := range tr.SeparationOfDuty.EvidenceActorPairsDistinct {
		left, leftOK := latestEvidenceByKind(ev, pair.LeftKind)
		right, rightOK := latestEvidenceByKind(ev, pair.RightKind)
		if !leftOK || !rightOK || strings.TrimSpace(left.Actor) == "" || strings.TrimSpace(right.Actor) == "" {
			continue
		}
		if left.Actor == right.Actor {
			blockers = append(blockers, Blocker{Kind: "separation_of_duty", Ref: left.ID + ":" + right.ID, Message: fmt.Sprintf("%s and %s evidence must be produced by different actors", pair.LeftKind, pair.RightKind)})
		}
	}
	return blockers
}

func postconditionBlockers(inst *Instance, tr TransitionSpec, chosen *OutcomeCase, ev []Evidence, obl []Obligation, checks map[string]CheckRun, task *taskDoc) []Blocker {
	if chosen == nil || len(tr.Postconditions) == 0 {
		return nil
	}
	ctx := evalContext{Evidence: ev, Obligations: obl, Checks: checks, Facts: taskFacts(task), Task: task, State: chosen.To}
	var blockers []Blocker
	for i, post := range tr.Postconditions {
		if !evalPredicate(post, ctx) {
			blockers = append(blockers, Blocker{Kind: "postcondition", Ref: fmt.Sprintf("%s:%d", tr.ID, i), Message: "transition postcondition failed"})
		}
	}
	return blockers
}

func taskDocHashOrEmpty(task *taskDoc) string {
	if task == nil {
		return ""
	}
	return taskDocHash(task)
}

func transitionBlockers(tr TransitionSpec, ev []Evidence, obl []Obligation, currentTaskHash string) []Blocker {
	var blockers []Blocker
	for _, o := range obl {
		if o.Blocking && o.Status == "open" {
			blockers = append(blockers, Blocker{Kind: "obligation", Ref: o.ID, Message: "blocking obligation is open"})
		}
	}
	for _, req := range tr.Requires {
		if req.Evidence != nil {
			match := matchEvidenceRequirement(ev, *req.Evidence)
			if !match.OK {
				blockers = append(blockers, Blocker{Kind: "evidence", Ref: req.Evidence.Kind, Message: match.Detail})
				continue
			}
			if match.Latest != nil && evidenceStaleForTask(*match.Latest, currentTaskHash) {
				blockers = append(blockers, Blocker{Kind: "stale_evidence", Ref: match.Latest.ID, Message: fmt.Sprintf("required evidence %s is stale for current task document", match.Latest.ID)})
			}
		}
		if req.Obligation != nil {
			found := false
			want := req.Obligation.Status
			if want == "" {
				want = "satisfied"
			}
			for _, o := range obl {
				if req.Obligation.ID != "" && o.ID != req.Obligation.ID {
					continue
				}
				if req.Obligation.Kind != "" && o.Kind != req.Obligation.Kind {
					continue
				}
				if o.Status == want {
					found = true
				}
			}
			if !found {
				ref := req.Obligation.ID
				if ref == "" {
					ref = req.Obligation.Kind
				}
				blockers = append(blockers, Blocker{Kind: "obligation", Ref: ref, Message: "required obligation status is missing"})
			}
		}
	}
	return blockers
}

func evidenceStaleForTask(e Evidence, currentTaskHash string) bool {
	if strings.TrimSpace(currentTaskHash) == "" || strings.TrimSpace(e.TaskHashAtProduction) == "" {
		return false
	}
	return e.TaskHashAtProduction != currentTaskHash
}

func latestCheckFor(database *db.DB, instanceID, transitionID, checkID string) (CheckRun, bool) {
	var c CheckRun
	var exit sql.NullInt64
	var hook, outcome, code, summary, facts, actor, role, runID, completed sql.NullString
	err := database.QueryRow(`
		SELECT id, instance_id, transition_id, check_id, COALESCE(hook_id,''), input_hash, exit_code, verdict,
		       outcome, code, summary, facts_json, COALESCE(actor,''), COALESCE(role,''), COALESCE(run_id,''), started_at, completed_at
		FROM workflow_check_runs
		WHERE instance_id = ? AND transition_id = ? AND check_id = ?
		ORDER BY started_at DESC, id DESC LIMIT 1
	`, instanceID, transitionID, checkID).Scan(&c.ID, &c.InstanceID, &c.TransitionID, &c.CheckID, &hook, &c.InputHash, &exit, &c.Verdict, &outcome, &code, &summary, &facts, &actor, &role, &runID, &c.StartedAt, &completed)
	if err != nil {
		return c, false
	}
	c.HookID = hook.String
	if exit.Valid {
		v := int(exit.Int64)
		c.ExitCode = &v
	}
	c.Outcome = outcome.String
	c.Code = code.String
	c.Summary = summary.String
	if facts.Valid {
		c.Facts = json.RawMessage(facts.String)
	}
	c.Actor = actor.String
	c.Role = role.String
	c.RunID = runID.String
	c.CompletedAt = completed.String
	return c, true
}

func filterOpenObligations(in []Obligation) []Obligation {
	out := []Obligation{}
	for _, o := range in {
		if o.Status == "open" {
			out = append(out, o)
		}
	}
	return out
}

func filterPendingEffects(in []Effect) []Effect {
	out := []Effect{}
	for _, e := range in {
		if e.Status == "pending" || e.Status == "leased" {
			out = append(out, e)
		}
	}
	return out
}

func (s *Service) RunChecks(taskSelector, transitionID, actor, role string, catalog *HookCatalog, templateDir string) ([]CheckRun, error) {
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
	var out []CheckRun
	for _, checkID := range tr.Checks {
		check, ok := tpl.Checks[checkID]
		if !ok {
			return nil, fmt.Errorf("check not found: %s", checkID)
		}
		cr, err := s.executeCheck(inst, tr, checkID, check, actor, role, catalog, templateDir, true)
		if err != nil {
			return nil, err
		}
		out = append(out, *cr)
	}
	return out, nil
}

func (s *Service) RunSingleHook(taskSelector, transitionID, hookID, actor, role string, catalog *HookCatalog, templateDir string) (*CheckRun, error) {
	if catalog == nil {
		return nil, fmt.Errorf("hook catalog is required")
	}
	if _, ok := catalog.Hooks[hookID]; !ok {
		return nil, fmt.Errorf("hook not found: %s", hookID)
	}
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
	check := CheckSpec{Type: "hook", HookID: hookID, ExitMap: map[string]ExitMap{"0": {Verdict: "pass", Outcome: "passed"}, "*": {Verdict: "error", Outcome: "failed"}}}
	return s.executeCheck(inst, tr, hookID, check, actor, role, catalog, templateDir, false)
}

func buildCheckInput(inst *Instance, tr *TransitionSpec, actor, role string, task *taskDoc, ev []Evidence, obl []Obligation) ([]byte, map[string]interface{}) {
	facts := taskFacts(task)
	taskHash := ""
	taskEtag := ""
	if task != nil {
		taskHash = taskDocHash(task)
		taskEtag = fmt.Sprint(task.ETag)
	}
	input := map[string]interface{}{
		"task":        map[string]interface{}{"ref": inst.TaskRef, "uuid": inst.TaskUUID, "etag": taskEtag, "hash": taskHash},
		"workflow":    map[string]interface{}{"instanceId": inst.ID, "state": inst.State(), "revision": inst.Revision, "contextHash": inst.ContextHash},
		"transition":  map[string]interface{}{"id": tr.ID},
		"actor":       map[string]interface{}{"id": actor},
		"role":        role,
		"facts":       facts,
		"evidence":    ev,
		"obligations": obl,
	}
	inputJSON, _ := json.Marshal(input)
	return inputJSON, facts
}

func currentCheckInputHash(inst *Instance, tr *TransitionSpec, actor, role string, task *taskDoc, ev []Evidence, obl []Obligation) string {
	inputJSON, _ := buildCheckInput(inst, tr, actor, role, task, ev, obl)
	return Hash(inputJSON)
}

func (s *Service) executeCheck(inst *Instance, tr *TransitionSpec, checkID string, check CheckSpec, actor, role string, catalog *HookCatalog, templateDir string, persist bool) (*CheckRun, error) {
	task, _ := loadTaskDoc(s.db, inst.TaskUUID)
	ev, _ := listEvidenceForInstance(s.db, inst.ID)
	obl, _ := listObligationsForInstance(s.db, inst.ID, true)
	inputJSON, facts := buildCheckInput(inst, tr, actor, role, task, ev, obl)
	cr := &CheckRun{InstanceID: inst.ID, TransitionID: tr.ID, CheckID: checkID, HookID: check.HookID, InputHash: Hash(inputJSON), Verdict: "inconclusive", Actor: actor, Role: role, StartedAt: s.now().Format(time.RFC3339)}
	switch check.Type {
	case "predicate":
		if check.Predicate == nil {
			cr.Verdict = "error"
			cr.Summary = "predicate check missing predicate"
		} else if evalPredicate(*check.Predicate, evalContext{Evidence: ev, Obligations: obl, Checks: map[string]CheckRun{}, Facts: facts, Task: task, State: inst.State()}) {
			cr.Verdict = "pass"
			cr.Outcome = "passed"
		} else {
			cr.Verdict = "fail"
			cr.Outcome = "failed"
		}
	case "builtin":
		switch check.Name {
		case "always_pass":
			cr.Verdict = "pass"
			cr.Outcome = "passed"
		case "always_fail":
			cr.Verdict = "fail"
			cr.Outcome = "failed"
		case "task_completed":
			if task != nil && task.State == "completed" {
				cr.Verdict = "pass"
				cr.Outcome = "completed"
			} else {
				cr.Verdict = "fail"
				cr.Outcome = "not_completed"
			}
		default:
			cr.Verdict = "error"
			cr.Outcome = "unknown_builtin"
			cr.Summary = "unknown builtin check"
		}
	case "hook":
		if catalog == nil {
			stored, err := s.storedHookCatalog(inst.TemplateID, inst.TemplateVersion)
			if err != nil {
				return nil, err
			}
			catalog = stored
		}
		if catalog == nil {
			return nil, fmt.Errorf("hook catalog is required for check %s", checkID)
		}
		hook, ok := catalog.Hooks[check.HookID]
		if !ok {
			return nil, fmt.Errorf("hook not found: %s", check.HookID)
		}
		exit, stdout, stderr, err := runHook(hook, templateDir, inputJSON)
		cr.ExitCode = &exit
		if err != nil && exit == -1 {
			cr.Verdict = "error"
			cr.Outcome = "hook_error"
			cr.Summary = err.Error()
		} else if hook.Stdout == "json" && len(bytes.TrimSpace(stdout)) > 0 {
			var doc struct {
				Verdict string                 `json:"verdict"`
				Code    string                 `json:"code"`
				Summary string                 `json:"summary"`
				Facts   map[string]interface{} `json:"facts"`
			}
			if err := json.Unmarshal(stdout, &doc); err == nil && doc.Verdict != "" {
				cr.Verdict = doc.Verdict
				cr.Code = doc.Code
				cr.Summary = doc.Summary
				if doc.Facts != nil {
					cr.Facts, _ = json.Marshal(doc.Facts)
				}
			}
		} else {
			m := check.ExitMap[fmt.Sprint(exit)]
			if m.Verdict == "" {
				m = check.ExitMap["*"]
			}
			if m.Verdict == "" {
				if exit == 0 {
					m.Verdict = "pass"
				} else {
					m.Verdict = "error"
				}
			}
			cr.Verdict = m.Verdict
			cr.Outcome = m.Outcome
			if len(stderr) > 0 {
				cr.Summary = strings.TrimSpace(string(stderr))
			}
		}
	case "role":
		cr.Verdict = "inconclusive"
		cr.Outcome = "role_required"
		cr.Summary = check.Instruction
	default:
		cr.Verdict = "error"
		cr.Outcome = "unknown_check_type"
	}
	cr.CompletedAt = s.now().Format(time.RFC3339)
	if persist {
		if err := withTx(s.db.DB, func(tx *sql.Tx) error {
			id, err := nextSeqID(tx, "workflow_check_run_seq", "chk")
			if err != nil {
				return err
			}
			cr.ID = id
			var facts interface{}
			if len(cr.Facts) > 0 {
				facts = string(cr.Facts)
			}
			_, err = tx.Exec(`
				INSERT INTO workflow_check_runs (
					id, instance_id, transition_id, check_id, hook_id, input_hash, exit_code, verdict,
					outcome, code, summary, facts_json, actor, role, started_at, completed_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, cr.ID, cr.InstanceID, cr.TransitionID, cr.CheckID, nullIfEmpty(cr.HookID), cr.InputHash, cr.ExitCode, cr.Verdict, nullIfEmpty(cr.Outcome), nullIfEmpty(cr.Code), nullIfEmpty(cr.Summary), facts, emptyToNil(cr.Actor), emptyToNil(cr.Role), cr.StartedAt, cr.CompletedAt)
			return err
		}); err != nil {
			return nil, err
		}
	}
	return cr, nil
}

func taskFacts(task *taskDoc) map[string]interface{} {
	if task == nil || strings.TrimSpace(task.Meta) == "" {
		return map[string]interface{}{}
	}
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(task.Meta), &meta); err != nil {
		return map[string]interface{}{}
	}
	facts, ok := meta["workflowFacts"].(map[string]interface{})
	if !ok {
		return map[string]interface{}{}
	}
	return facts
}

func runHook(hook HookSpec, templateDir string, input []byte) (int, []byte, []byte, error) {
	if len(hook.Argv) == 0 {
		return -1, nil, nil, fmt.Errorf("hook argv is empty")
	}
	timeout := time.Duration(hook.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, hook.Argv[0], hook.Argv[1:]...)
	if hook.CWD == "template_dir" && templateDir != "" {
		cmd.Dir = templateDir
	} else if hook.CWD != "" && hook.CWD != "template_dir" {
		cmd.Dir = hook.CWD
	}
	if hook.Stdin == "json" {
		cmd.Stdin = bytes.NewReader(input)
	}
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return -1, stdout.Bytes(), stderr.Bytes(), err
		}
	}
	return exitCode, limitBytes(stdout.Bytes(), hook.MaxStdoutBytes), limitBytes(stderr.Bytes(), hook.MaxStderrBytes), err
}

func limitBytes(b []byte, max int) []byte {
	if max <= 0 || len(b) <= max {
		return b
	}
	return b[:max]
}

func LoadHookCatalog(path string) (*HookCatalog, error) {
	resolved, err := ResolveHookCatalogPath(path)
	if err != nil {
		return nil, err
	}
	if resolved == "" {
		return nil, nil
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, err
	}
	var cat HookCatalog
	if err := json.Unmarshal(data, &cat); err != nil {
		return nil, err
	}
	if cat.Hooks == nil {
		cat.Hooks = map[string]HookSpec{}
	}
	if cat.EffectHandlers == nil {
		cat.EffectHandlers = map[string]HookSpec{}
	}
	return &cat, nil
}

func ResolveHookCatalogPath(path string) (string, error) {
	if strings.TrimSpace(path) != "" {
		return path, nil
	}
	if envPath := strings.TrimSpace(os.Getenv("WRKF_HOOK_CATALOG")); envPath != "" {
		return envPath, nil
	}

	candidates := []string{".wrkf/hooks.json", "wrkf-hooks.json"}
	for _, pattern := range []string{
		".wrkq/wrkf-*/hook-catalog.wrapped.json",
		".wrkq/wrkf-*/hook-catalog.json",
	} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return "", err
		}
		sort.Strings(matches)
		candidates = append(candidates, matches...)
	}

	cwd, err := os.Getwd()
	if err == nil {
		for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
			for _, pattern := range []string{
				filepath.Join(dir, ".wrkf", "hooks.json"),
				filepath.Join(dir, "wrkf-hooks.json"),
				filepath.Join(dir, ".wrkq", "wrkf-*", "hook-catalog.wrapped.json"),
				filepath.Join(dir, ".wrkq", "wrkf-*", "hook-catalog.json"),
			} {
				matches, err := filepath.Glob(pattern)
				if err != nil {
					return "", err
				}
				sort.Strings(matches)
				candidates = append(candidates, matches...)
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}

	if home, err := os.UserHomeDir(); err == nil {
		for _, pattern := range []string{
			filepath.Join(home, "praesidium", "*", ".wrkq", "wrkf-*", "hook-catalog.wrapped.json"),
			filepath.Join(home, "praesidium", "*", ".wrkq", "wrkf-*", "hook-catalog.json"),
		} {
			matches, err := filepath.Glob(pattern)
			if err != nil {
				return "", err
			}
			sort.Strings(matches)
			candidates = append(candidates, matches...)
		}
	}

	seen := map[string]bool{}
	for _, candidate := range candidates {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", nil
}

func HookCatalogDir(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Dir(path)
}
