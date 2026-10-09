//go:build wrkq_local

package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
)

func (s *Service) ActiveInstance(taskSelector string) (*Instance, error) {
	taskUUID, _, err := selectors.ResolveTask(s.db, taskSelector)
	if err != nil {
		return nil, err
	}
	return s.activeInstanceByTaskUUID(taskUUID)
}

func (s *Service) activeInstanceByTaskUUID(taskUUID string) (*Instance, error) {
	inst, err := activeInstanceByTaskUUIDQuery(s.db, taskUUID)
	if err != nil {
		return nil, err
	}
	return inst, s.populateInstanceLineage(inst)
}

// applySuspendOutcomeTx authors the single active suspension from a validated
// template outcome. It deliberately has no public service or CLI counterpart:
// entering suspension is possible only by taking a normal workflow transition.
// The caller persists the populated instance in the same transition commit.
func applySuspendOutcomeTx(tx *sql.Tx, inst *Instance, spec *SuspendSpec, causeRef, at string) error {
	if spec == nil {
		return validationError("suspend", "suspend outcome is required", "template-declared suspend outcome", nil, "fix the workflow template")
	}
	reason := strings.TrimSpace(spec.Reason)
	if reason == "" {
		return validationError("reason", "a suspension reason code is required", "template-declared reason code", nil, "fix the workflow template")
	}
	if inst.Status == "closed" {
		return validationError("status", fmt.Sprintf("instance %s is closed and cannot be suspended", inst.ID), "running instance", nil, "fix the workflow template")
	}
	if inst.Suspension != nil {
		return alreadySuspendedError(inst)
	}
	suspensionID, err := nextSeqID(tx, "workflow_suspension_seq", "sus")
	if err != nil {
		return err
	}
	inst.Suspension = &Suspension{ID: suspensionID, Reason: reason, At: at, CauseRef: strings.TrimSpace(causeRef)}
	return nil
}

func (s *Service) LatestInstance(taskSelector string) (*Instance, error) {
	taskUUID, _, err := selectors.ResolveTask(s.db, taskSelector)
	if err != nil {
		return nil, err
	}
	inst, err := latestInstanceByTaskUUIDQuery(s.db, taskUUID)
	if err != nil {
		return nil, err
	}
	return inst, s.populateInstanceLineage(inst)
}

// Instances returns every workflow generation attached to a task. The order is
// deliberately identical to the singleton inspect selection: live instances
// first, then newest creation time and id. A known task with no history returns
// a non-nil empty slice.
func (s *Service) Instances(taskSelector string) ([]*Instance, error) {
	taskUUID, _, err := selectors.ResolveTask(s.db, taskSelector)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT `+instanceSelectColumns+`
		FROM workflow_instances
		WHERE task_uuid = ?
		ORDER BY `+instanceInspectOrder+`
	`, taskUUID)
	if err != nil {
		return nil, err
	}
	instances := make([]*Instance, 0)
	for rows.Next() {
		inst, scanErr := scanInstanceRow(rows)
		if scanErr != nil {
			_ = rows.Close()
			return nil, scanErr
		}
		instances = append(instances, inst)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for _, inst := range instances {
		if err := s.populateInstanceLineage(inst); err != nil {
			return nil, err
		}
	}
	return instances, nil
}

// instanceSelectColumns is the single source of truth for the instance column
// projection. Every instance read (row and rows) selects exactly these columns
// in this order and scans them through scanInstanceRow, so the suspension
// fields can never drift between read sites.
const instanceSelectColumns = `id, task_uuid, task_ref, COALESCE(project_id,''), template_id, template_version, template_hash,
	       status, COALESCE(phase,''), COALESCE(outcome,''), revision,
	       task_doc_etag, task_doc_hash, created_at, updated_at, COALESCE(closed_at,''),
	       COALESCE(suspension_id,''), COALESCE(suspension_reason,''), COALESCE(suspension_at,''), COALESCE(suspension_cause_ref,'')`

const instanceInspectOrder = `CASE WHEN status != 'closed' THEN 0 ELSE 1 END, created_at DESC, id DESC`

// scanInstanceRow scans one instance row (projected via instanceSelectColumns)
// and reconstitutes its active suspension record when present.
func scanInstanceRow(sc instanceScanner) (*Instance, error) {
	var i Instance
	var susID, susReason, susAt, susCause string
	if err := sc.Scan(&i.ID, &i.TaskUUID, &i.TaskRef, &i.ProjectID, &i.TemplateID, &i.TemplateVersion, &i.TemplateHash, &i.Status, &i.Phase, &i.Outcome, &i.Revision, &i.TaskDocEtag, &i.TaskDocHash, &i.CreatedAt, &i.UpdatedAt, &i.ClosedAt, &susID, &susReason, &susAt, &susCause); err != nil {
		return nil, err
	}
	if susID != "" {
		i.Suspension = &Suspension{ID: susID, Reason: susReason, At: susAt, CauseRef: susCause}
	}
	return &i, nil
}

func scanInstance(row *sql.Row) (*Instance, error) {
	inst, err := scanInstanceRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("workflow instance not found")
		}
		return nil, err
	}
	return inst, nil
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
		SELECT id, instance_id, seq, schema_version, type, COALESCE(principal_ref, actor, ''), COALESCE(role,''), COALESCE(run_id,''),
		       COALESCE(observed_revision,0), next_revision, COALESCE(task_doc_etag,''), COALESCE(task_doc_hash,''),
		       COALESCE(idempotency_key,''), COALESCE(result,''), payload_json, created_at
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
		if err := rows.Scan(&e.ID, &e.InstanceID, &e.Seq, &e.SchemaVersion, &e.Type, &e.PrincipalRef, &e.Role, &e.RunID, &e.ObservedRevision, &e.NextRevision, &e.TaskDocEtag, &e.TaskDocHash, &e.IdempotencyKey, &e.Result, &payload, &e.CreatedAt); err != nil {
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
	allowedEventTypes := []string{"workflow.instance_cancelled", "workflow.suspended", "workflow.suspension_resolved", "workflow.transitioned"}
	if eventType != "workflow.transitioned" && eventType != "workflow.suspended" && eventType != "workflow.suspension_resolved" && eventType != "workflow.instance_cancelled" {
		return EventQueryResult{}, validationError("eventType", "event type is not queryable", "workflow.instance_cancelled|workflow.suspended|workflow.suspension_resolved|workflow.transitioned", allowedEventTypes, "set eventType to a queryable workflow event type")
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
		SELECT e.id, e.instance_id, e.seq, e.type, COALESCE(e.principal_ref, e.actor, ''), COALESCE(e.role,''),
		       COALESCE(e.observed_revision, 0), e.next_revision,
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

	items := []QueriedEvent{}
	for rows.Next() {
		var item QueriedEvent
		var payload string
		if err := rows.Scan(
			&item.ID, &item.InstanceID, &item.Seq, &item.EventType, &item.PrincipalRef, &item.Role,
			&item.BeforeRevision, &item.AfterRevision, &payload, &item.OccurredAt, &item.Task.Ref,
			&item.Task.UUID, &item.Task.ID, &item.Task.Slug, &item.Task.ProjectUUID, &item.Task.RiskClass,
			&item.Task.ProjectID, &item.Task.ProjectSlug,
		); err != nil {
			return EventQueryResult{}, err
		}
		item.Payload = json.RawMessage(payload)
		applyQueriedEventPayload(&item)
		item.MatchingRoleBindings = []RoleBinding{}
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
		next, cerr := cursor.BuildNextCursor([]string{"created_at"}, []any{anchor.OccurredAt}, anchor.ID)
		if cerr == nil {
			result.NextCursor = next
		}
	}
	return result, nil
}

func applyQueriedEventPayload(item *QueriedEvent) {
	if item == nil || len(item.Payload) == 0 {
		return
	}
	var payload struct {
		Transition     string      `json:"transition"`
		Outcome        string      `json:"outcome"`
		From           State       `json:"from"`
		To             State       `json:"to"`
		Suspension     *Suspension `json:"suspension"`
		Disposition    string      `json:"disposition"`
		BeforeRevision int64       `json:"beforeRevision"`
		AfterRevision  int64       `json:"afterRevision"`
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
	item.Suspension = payload.Suspension
	item.Disposition = payload.Disposition
	if payload.BeforeRevision != 0 || item.BeforeRevision == 0 {
		item.BeforeRevision = payload.BeforeRevision
	}
	if payload.AfterRevision != 0 {
		item.AfterRevision = payload.AfterRevision
	}
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
		inst.TaskDocEtag = fmt.Sprint(task.ETag)
		inst.TaskDocHash = taskDocHash(task)
		inst.UpdatedAt = s.now().Format(time.RFC3339)
		_, err = tx.Exec(`UPDATE workflow_instances SET task_doc_etag = ?, task_doc_hash = ?, updated_at = ? WHERE id = ?`,
			inst.TaskDocEtag, inst.TaskDocHash, inst.UpdatedAt, inst.ID)
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
	inst, err := instanceByIDQuery(s.db, id)
	if err != nil {
		return nil, err
	}
	return inst, s.populateInstanceLineage(inst)
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

func resolveTaskUUIDQuery(q queryer, selector string) (string, error) {
	parsed := selectors.Parse(selector)
	if parsed.Type != selectors.TypeTask && parsed.Type != selectors.TypeAuto {
		return "", fmt.Errorf("expected task selector (t:), got %s selector", parsed.Type)
	}
	token := parsed.Token
	if expanded, ok, err := selectors.ExpandBareTaskID(q, token); err != nil {
		return "", err
	} else if ok {
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
		SELECT `+instanceSelectColumns+`
		FROM workflow_instances
		WHERE task_uuid = ?
		ORDER BY `+instanceInspectOrder+` LIMIT 1
	`, taskUUID)
	return scanInstance(row)
}

func activeInstanceByTaskUUIDQuery(q queryer, taskUUID string) (*Instance, error) {
	row := q.QueryRow(`
		SELECT `+instanceSelectColumns+`
		FROM workflow_instances
		WHERE task_uuid = ? AND status != 'closed'
		ORDER BY created_at DESC, id DESC LIMIT 1
	`, taskUUID)
	return scanInstance(row)
}

func instanceByIDQuery(q queryer, instanceID string) (*Instance, error) {
	row := q.QueryRow(`
		SELECT `+instanceSelectColumns+`
		FROM workflow_instances WHERE id = ?
	`, instanceID)
	return scanInstance(row)
}

func instanceLineageRef(inst Instance) *InstanceLineageRef {
	return &InstanceLineageRef{
		InstanceID:      inst.ID,
		TemplateID:      inst.TemplateID,
		TemplateVersion: inst.TemplateVersion,
		TemplateHash:    inst.TemplateHash,
		Revision:        inst.Revision,
		Status:          inst.Status,
		Phase:           inst.Phase,
		Outcome:         inst.Outcome,
	}
}

func (s *Service) populateInstanceLineage(inst *Instance) error {
	if inst == nil {
		return nil
	}
	if pred, err := s.lineageRefFromEvent(inst.ID, "workflow.attached", "supersededPredecessor"); err != nil {
		return err
	} else if pred != nil {
		inst.Supersedes = pred
	}
	if succ, err := s.lineageRefFromEvent(inst.ID, "workflow.superseded", "successor"); err != nil {
		return err
	} else if succ != nil {
		inst.SupersededBy = succ
	}
	return nil
}

func (s *Service) lineageRefFromEvent(instanceID, eventType, field string) (*InstanceLineageRef, error) {
	var raw string
	err := s.db.QueryRow(`
		SELECT payload_json
		FROM workflow_events
		WHERE instance_id = ? AND type = ?
		ORDER BY seq DESC LIMIT 1
	`, instanceID, eventType).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, err
	}
	refRaw, ok := payload[field]
	if !ok || len(refRaw) == 0 || string(refRaw) == "null" {
		return nil, nil
	}
	var ref InstanceLineageRef
	if err := json.Unmarshal(refRaw, &ref); err != nil {
		return nil, err
	}
	return &ref, nil
}
