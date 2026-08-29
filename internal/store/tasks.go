package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/webhooks"
)

// TaskStore handles task persistence operations.
type TaskStore struct {
	store *Store
}

type pendingWebhook struct {
	taskUUID string
	ctx      webhooks.EventContext
}

// CreateParams contains parameters for creating a new task.
type CreateParams struct {
	UUID                 string // optional: force specific UUID instead of auto-generating
	Slug                 string
	Title                string
	Description          string
	Specification        string
	ProjectUUID          string
	State                domain.State
	Priority             int
	Kind                 string  // task, subtask, spike, bug, chore - defaults to "task"
	ParentTaskUUID       *string // for subtasks
	AssigneeActorUUID    *string // task assignment
	AssigneePrincipalRef *string // canonical task assignment
	RequestedByProjectID *string
	AssignedProjectID    *string
	Resolution           *string
	WorkflowPreset       *string
	PresetVersion        *int
	Phase                *string
	RiskClass            *string
	Labels               string  // JSON array
	Meta                 *string // JSON object
	DueAt                string
	StartAt              string
	CausedBy             []CausedByRef // ordered, de-duplicated causal lineage edges
	CampaignUUID         *string       // optional campaign ENROLMENT at create time (cross-project membership)
	Via                  string        // origin.via for webhooks; defaults to "cli"
	CreatorScopeRef      string        // full praesidium scopeRef of the creating agent; stored as created_by_scope_ref
}

// CreateResult contains the result of task creation.
type CreateResult struct {
	UUID string
	ID   string
	ETag int64
}

func stringPtr(value string) *string {
	return &value
}

// nullableScopeRef returns the scopeRef as a value suitable for a SQL bind,
// mapping the empty string to NULL so unattributed creations don't store "".
func nullableScopeRef(scopeRef string) interface{} {
	if scopeRef == "" {
		return nil
	}
	return scopeRef
}

func sortedFieldNames(fields map[string]interface{}) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func summarizeWebhookValue(field string, value interface{}) interface{} {
	if value == nil {
		if field == "labels" {
			return []string{}
		}
		return nil
	}
	switch field {
	case "description", "specification":
		text, ok := value.(string)
		if !ok {
			return value
		}
		sum := sha256.Sum256([]byte(text))
		return map[string]interface{}{
			"length": len(text),
			"sha256": hex.EncodeToString(sum[:]),
		}
	case "labels":
		return summarizeWebhookLabels(value)
	}
	return value
}

func summarizeWebhookLabels(value interface{}) []string {
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case string:
		text := strings.TrimSpace(v)
		if text == "" || text == "null" {
			return []string{}
		}
		var labels []string
		if err := json.Unmarshal([]byte(text), &labels); err == nil && labels != nil {
			return labels
		}
	case []interface{}:
		labels := make([]string, 0, len(v))
		for _, item := range v {
			label, ok := item.(string)
			if !ok {
				return []string{}
			}
			labels = append(labels, label)
		}
		return labels
	}
	return []string{}
}

func normalizeStateValue(value interface{}) (domain.State, error) {
	switch v := value.(type) {
	case domain.State:
		return domain.ParseState(string(v))
	case string:
		return domain.ParseState(v)
	default:
		return "", fmt.Errorf("invalid state value type %T", value)
	}
}

func normalizeStateField(fields map[string]interface{}) (string, bool, error) {
	value, ok := fields["state"]
	if !ok {
		return "", false, nil
	}
	state, err := normalizeStateValue(value)
	if err != nil {
		return "", true, err
	}
	stateString := string(state)
	fields["state"] = stateString
	return stateString, true, nil
}

func loadTaskFieldValues(tx *sql.Tx, taskUUID string, fields []string) (map[string]interface{}, error) {
	values := make(map[string]interface{}, len(fields))
	for _, field := range fields {
		switch field {
		case "priority", "etag":
			var value int64
			if err := tx.QueryRow("SELECT "+field+" FROM tasks WHERE uuid = ?", taskUUID).Scan(&value); err != nil {
				return nil, err
			}
			if field == "priority" {
				values[field] = int(value)
			} else {
				values[field] = value
			}
		case "state", "slug", "title", "project_uuid", "kind", "resolution", "outcome", "meta", "labels", "due_at", "start_at", "archived_at", "deleted_at", "parent_task_uuid", "assignee_principal_ref", "requested_by_project_id", "assigned_project_id", "created_by_principal_ref", "updated_by_principal_ref", "deleted_by_principal_ref", "created_by_scope_ref", "updated_by_scope_ref", "deleted_by_scope_ref":
			var value sql.NullString
			if err := tx.QueryRow("SELECT "+field+" FROM tasks WHERE uuid = ?", taskUUID).Scan(&value); err != nil {
				return nil, err
			}
			if value.Valid {
				values[field] = value.String
			} else {
				values[field] = nil
			}
		case "description", "specification":
			var value string
			if err := tx.QueryRow("SELECT "+field+" FROM tasks WHERE uuid = ?", taskUUID).Scan(&value); err != nil {
				return nil, err
			}
			values[field] = summarizeWebhookValue(field, value)
		}
	}
	return values, nil
}

func buildWebhookChanges(oldValues map[string]interface{}, fields map[string]interface{}) map[string]webhooks.Change {
	changes := make(map[string]webhooks.Change, len(fields))
	for _, field := range sortedFieldNames(fields) {
		changes[field] = webhooks.Change{
			From: summarizeWebhookValue(field, oldValues[field]),
			To:   summarizeWebhookValue(field, fields[field]),
		}
	}
	return changes
}

func blockerInfosForTask(tx *sql.Tx, taskUUID string) ([]webhooks.BlockerInfo, error) {
	rows, err := tx.Query(`
		SELECT t.id, t.state
		FROM task_relations r
		JOIN tasks t ON r.from_task_uuid = t.uuid
		WHERE r.to_task_uuid = ?
		  AND r.kind = 'blocks'
		  AND t.state NOT IN ('completed', 'archived', 'deleted', 'cancelled', 'idea')
		ORDER BY t.id
	`, taskUUID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var blockers []webhooks.BlockerInfo
	for rows.Next() {
		var blocker webhooks.BlockerInfo
		if err := rows.Scan(&blocker.ID, &blocker.State); err != nil {
			return nil, err
		}
		blockers = append(blockers, blocker)
	}
	return blockers, rows.Err()
}

// Create creates a new task and logs a task.created event.
func (ts *TaskStore) Create(actorUUID string, params CreateParams) (*CreateResult, error) {
	return ts.CreateWithAttribution(ts.store.attributionFromActorUUID(actorUUID), params)
}

// CreateWithAttribution creates a task using canonical external principal
// attribution. Legacy actor UUIDs are optional display/cache metadata only.
func (ts *TaskStore) CreateWithAttribution(attr attribution.Attribution, params CreateParams) (*CreateResult, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	var result *CreateResult
	var webhookCtx webhooks.EventContext
	via := params.Via
	if via == "" {
		via = "cli"
	}

	// Default kind to "task" if not provided
	kind := params.Kind
	if kind == "" {
		kind = "task"
	}
	creatorScope := attr.ScopeRef
	if params.CreatorScopeRef != "" {
		creatorScope = params.CreatorScopeRef
	}

	err := ts.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Build query - include uuid column only if forcing a specific UUID
		var query string
		var args []interface{}

		if params.UUID != "" {
			query = `INSERT INTO tasks (uuid, id, slug, title, description, specification, project_uuid, state, priority, kind,
				parent_task_uuid, assignee_principal_ref, requested_by_project_id, assigned_project_id, resolution,
				workflow_preset, preset_version, phase, risk_class,
				labels, meta, due_at, start_at,
				created_by_principal_ref, updated_by_principal_ref, created_by_scope_ref, updated_by_scope_ref)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
			args = append(args, params.UUID)
		} else {
			query = `INSERT INTO tasks (id, slug, title, description, specification, project_uuid, state, priority, kind,
				parent_task_uuid, assignee_principal_ref, requested_by_project_id, assigned_project_id, resolution,
				workflow_preset, preset_version, phase, risk_class,
				labels, meta, due_at, start_at,
				created_by_principal_ref, updated_by_principal_ref, created_by_scope_ref, updated_by_scope_ref)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		}

		// Common args for both cases
		args = append(args,
			"", // id (auto-generated by trigger)
			params.Slug,
			params.Title,
			params.Description,
			params.Specification,
			params.ProjectUUID,
			string(params.State),
			params.Priority,
			kind,
			params.ParentTaskUUID,
			params.AssigneePrincipalRef,
			params.RequestedByProjectID,
			params.AssignedProjectID,
			params.Resolution,
			params.WorkflowPreset,
			params.PresetVersion,
			params.Phase,
			params.RiskClass,
			params.Labels,
			params.Meta,
			params.DueAt,
			params.StartAt,
			attr.PrincipalRef,
			attr.PrincipalRef,
			nullableScopeRef(creatorScope),
			scopeSQL(attr),
		)

		res, err := tx.Exec(query, args...)
		if err != nil {
			return fmt.Errorf("failed to create task: %w", err)
		}

		// Get the UUID and ID of the created task
		rowID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("failed to get last insert ID: %w", err)
		}

		var uuid, id string
		var etag int64
		err = tx.QueryRow("SELECT uuid, id, etag FROM tasks WHERE rowid = ?", rowID).Scan(&uuid, &id, &etag)
		if err != nil {
			return fmt.Errorf("failed to get task UUID: %w", err)
		}
		// Enrolment is staged before validation so create is a full admission
		// path: wrkq.campaign.canonical-portfolio-authority requires create to
		// be gated for RESIDENT and ENROLLED members alike, and the shared
		// validator rejecting inside this transaction rolls the insert back.
		if params.CampaignUUID != nil {
			if _, err := tx.Exec("UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", *params.CampaignUUID, uuid); err != nil {
				return fmt.Errorf("failed to enroll task in campaign: %w", err)
			}
		}
		if err := validateEffectiveMembershipTx(tx, campaignValidation{
			taskUUIDs:         []string{uuid},
			residentAdmission: true,
			enrollmentChange:  params.CampaignUUID != nil,
		}); err != nil {
			return err
		}

		// Insert causal lineage edges (caused_by) in the same transaction now that
		// the new task UUID is known.
		if len(params.CausedBy) > 0 {
			if err := insertCausedByRows(tx, causedByAttribution{
				principalRef: attr.PrincipalRef,
				scope:        scopeSQL(attr),
			}, uuid, params.CausedBy); err != nil {
				return err
			}
		}

		// Log event with structured payload
		payload := map[string]interface{}{
			"slug":     params.Slug,
			"title":    params.Title,
			"state":    string(params.State),
			"priority": params.Priority,
			"kind":     kind,
		}
		if params.ParentTaskUUID != nil {
			payload["parent_task_uuid"] = *params.ParentTaskUUID
		}
		if params.AssigneePrincipalRef != nil {
			payload["assignee_principal_ref"] = *params.AssigneePrincipalRef
		}
		if params.RequestedByProjectID != nil {
			payload["requested_by_project_id"] = *params.RequestedByProjectID
		}
		if params.AssignedProjectID != nil {
			payload["assigned_project_id"] = *params.AssignedProjectID
		}
		if params.Resolution != nil {
			payload["resolution"] = *params.Resolution
		}
		if params.WorkflowPreset != nil {
			payload["workflow_preset"] = *params.WorkflowPreset
		}
		if params.PresetVersion != nil {
			payload["preset_version"] = *params.PresetVersion
		}
		if params.Phase != nil {
			payload["phase"] = *params.Phase
		}
		if params.RiskClass != nil {
			payload["risk_class"] = *params.RiskClass
		}
		if params.Labels != "" {
			payload["labels"] = params.Labels
		}
		if params.DueAt != "" {
			payload["due_at"] = params.DueAt
		}
		if params.StartAt != "" {
			payload["start_at"] = params.StartAt
		}
		if params.Specification != "" {
			payload["specification"] = params.Specification
		}
		if len(params.CausedBy) > 0 {
			causedByIDs := make([]string, 0, len(params.CausedBy))
			for _, ref := range params.CausedBy {
				causedByIDs = append(causedByIDs, ref.FriendlyID)
			}
			payload["caused_by"] = causedByIDs
		}

		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal event payload: %w", err)
		}
		payloadStr := string(payloadJSON)

		meta, err := ew.LogEventReturning(tx, &domain.Event{
			PrincipalRef: attr.PrincipalRef,
			ScopeRef:     attr.ScopeRef,
			ResourceType: "task",
			ResourceUUID: &uuid,
			EventType:    "task.created",
			ETag:         &etag,
			Payload:      &payloadStr,
		})
		if err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}
		changed := sortedFieldNames(payload)
		changes := make(map[string]webhooks.Change, len(payload))
		for _, field := range changed {
			changes[field] = webhooks.Change{From: nil, To: summarizeWebhookValue(field, payload[field])}
		}
		webhookCtx = webhooks.EventContext{
			Metadata:     meta,
			Event:        "created",
			PrincipalRef: attr.PrincipalRef,
			Via:          via,
			Transition:   &webhooks.Transition{From: nil, To: stringPtr(string(params.State))},
			Changed:      changed,
			Changes:      changes,
		}

		result = &CreateResult{
			UUID: uuid,
			ID:   id,
			ETag: etag,
		}
		return nil
	})

	if err == nil && result != nil {
		webhooks.DispatchTaskEvent(ts.store.db, result.UUID, webhookCtx)
	}

	return result, err
}

// UpdateFields updates specified fields on a task and logs a task.updated event.
// Returns the new etag on success.
func (ts *TaskStore) UpdateFields(actorUUID, taskUUID string, fields map[string]interface{}, ifMatch int64) (int64, error) {
	return ts.UpdateFieldsWithVia(actorUUID, taskUUID, fields, ifMatch, "cli")
}

// UpdateFieldsWithVia updates fields and records the ingress surface in webhook origin.via.
func (ts *TaskStore) UpdateFieldsWithVia(actorUUID, taskUUID string, fields map[string]interface{}, ifMatch int64, via string) (int64, error) {
	return ts.UpdateFieldsWithViaAttribution(ts.store.attributionFromActorUUID(actorUUID), taskUUID, fields, ifMatch, via)
}

// UpdateFieldsWithAttribution updates fields with canonical principal attribution.
func (ts *TaskStore) UpdateFieldsWithAttribution(attr attribution.Attribution, taskUUID string, fields map[string]interface{}, ifMatch int64) (int64, error) {
	return ts.UpdateFieldsWithViaAttribution(attr, taskUUID, fields, ifMatch, "cli")
}

// UpdateFieldsWithViaAttribution updates fields and records the ingress surface.
func (ts *TaskStore) UpdateFieldsWithViaAttribution(attr attribution.Attribution, taskUUID string, fields map[string]interface{}, ifMatch int64, via string) (int64, error) {
	return ts.UpdateFieldsWithViaAttributionAndPrecondition(attr, taskUUID, fields, ifMatch, via, nil)
}

// UpdateFieldsWithViaAttributionAndPrecondition evaluates precondition inside
// the same BEGIN IMMEDIATE transaction as the task mutation. It is the fencing
// seam for authority checks whose truth must not change between validation and
// write (for example, generation-fenced task-claim completion).
func (ts *TaskStore) UpdateFieldsWithViaAttributionAndPrecondition(attr attribution.Attribution, taskUUID string, fields map[string]interface{}, ifMatch int64, via string, precondition func(*sql.Tx) error) (int64, error) {
	if err := requireAttribution(attr); err != nil {
		return 0, err
	}
	if via == "" {
		via = "cli"
	}
	// caused_by is a non-column task field: extract it before the scalar UPDATE is
	// built so it never becomes a `caused_by = ?` set clause. Its rows are replaced
	// in the same transaction below.
	var causedByUpdate *CausedByUpdate
	if raw, ok := fields[causedByFieldKey]; ok {
		cu, ok := raw.(CausedByUpdate)
		if !ok {
			return 0, fmt.Errorf("invalid caused_by value type %T", raw)
		}
		causedByUpdate = &cu
		delete(fields, causedByFieldKey)
	}
	if _, _, err := normalizeStateField(fields); err != nil {
		return 0, err
	}
	var newETag int64
	var unblockedTaskUUIDs []string
	var webhooksToDispatch []pendingWebhook

	err := ts.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current etag and state
		var currentETag int64
		var currentState string
		err := tx.QueryRow("SELECT etag, state FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentETag, &currentState)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("task not found: %s", taskUUID)
			}
			return fmt.Errorf("failed to get current etag: %w", err)
		}

		// Check etag if ifMatch was provided
		// Authority fences take precedence over optimistic metadata conflicts. If
		// a takeover changed both generation and etag after the caller's read, the
		// old holder must receive claim_superseded (with the new holder), not a
		// generic etag conflict that hides the authority change.
		if precondition != nil {
			if err := precondition(tx); err != nil {
				return err
			}
		}
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}

		// Validate re-parenting (no self-parent, no cycles, depth <= 1).
		if pv, ok := fields["parent_task_uuid"]; ok && pv != nil {
			parentUUID, _ := pv.(string)
			if err := validateParentAssignment(tx, taskUUID, parentUUID); err != nil {
				return err
			}
		}

		// Check if we're transitioning to a completion state (for unblock webhook logic)
		newState, hasStateChange := fields["state"].(string)
		transitioningToCompletion := hasStateChange && !isCompletionState(currentState) && isCompletionState(newState)
		fieldNames := sortedFieldNames(fields)
		oldValues, err := loadTaskFieldValues(tx, taskUUID, fieldNames)
		if err != nil {
			return fmt.Errorf("failed to load old task values: %w", err)
		}

		// If transitioning to completion, find tasks that might become unblocked
		var potentiallyUnblockedUUIDs []string
		blockersBefore := map[string][]webhooks.BlockerInfo{}
		if transitioningToCompletion {
			rows, err := tx.Query(`
				SELECT to_task_uuid
				FROM task_relations
				WHERE from_task_uuid = ?
				  AND kind = 'blocks'
			`, taskUUID)
			if err != nil {
				return fmt.Errorf("failed to query blocked tasks: %w", err)
			}
			for rows.Next() {
				var uuid string
				if err := rows.Scan(&uuid); err != nil {
					_ = rows.Close()
					return fmt.Errorf("failed to scan blocked task: %w", err)
				}
				potentiallyUnblockedUUIDs = append(potentiallyUnblockedUUIDs, uuid)
				blockers, err := blockerInfosForTask(tx, uuid)
				if err != nil {
					_ = rows.Close()
					return fmt.Errorf("failed to load blockers for task %s: %w", uuid, err)
				}
				blockersBefore[uuid] = blockers
			}
			_ = rows.Close()
			if err := rows.Err(); err != nil {
				return fmt.Errorf("error iterating blocked tasks: %w", err)
			}
		}

		// Build UPDATE query
		var setClauses []string
		var args []interface{}

		for key, value := range fields {
			setClauses = append(setClauses, fmt.Sprintf("%s = ?", key))
			args = append(args, value)
		}

		// Increment etag and update attribution
		setClauses = append(setClauses, "etag = etag + 1")
		setClauses = append(setClauses, "updated_by_principal_ref = ?")
		setClauses = append(setClauses, "updated_by_scope_ref = ?")
		args = append(args, attr.PrincipalRef, scopeSQL(attr))
		if newState, ok := fields["state"]; ok && newState == "deleted" {
			setClauses = append(setClauses, "deleted_by_principal_ref = ?")
			setClauses = append(setClauses, "deleted_by_scope_ref = ?")
			args = append(args, attr.PrincipalRef, scopeSQL(attr))
		}

		// Add WHERE clause
		args = append(args, taskUUID)

		query := fmt.Sprintf("UPDATE tasks SET %s WHERE uuid = ?", strings.Join(setClauses, ", "))
		_, err = tx.Exec(query, args...)
		if err != nil {
			return fmt.Errorf("failed to update task: %w", err)
		}
		_, enrollmentChange := fields["campaign_uuid"]
		_, projectChange := fields["project_uuid"]
		if enrollmentChange || projectChange {
			if err := validateEffectiveMembershipTx(tx, campaignValidation{
				taskUUIDs: []string{taskUUID}, enrollmentChange: enrollmentChange,
				residentAdmission: projectChange, move: projectChange,
			}); err != nil {
				return err
			}
		}

		// Replace the task's caused_by edge set (if a caused_by update was supplied).
		// Old values are captured first so the event/webhook change summary reports
		// old/new as friendly-ID arrays.
		var causedByOldIDs []string
		if causedByUpdate != nil {
			causedByOldIDs, err = CausedByIDs(tx, taskUUID)
			if err != nil {
				return err
			}
			if _, err := tx.Exec("DELETE FROM task_causes WHERE task_uuid = ?", taskUUID); err != nil {
				return fmt.Errorf("failed to clear caused_by: %w", err)
			}
			if err := insertCausedByRows(tx, causedByAttribution{
				principalRef: attr.PrincipalRef,
				scope:        scopeSQL(attr),
			}, taskUUID, causedByUpdate.Refs); err != nil {
				return err
			}
		}

		// Cascade delete resident subtasks and detach external child backlinks if
		// state is being set to 'deleted'.
		if newState, ok := fields["state"]; ok && newState == "deleted" {
			if err := detachExternalSubtasks(tx, attr, taskUUID); err != nil {
				return fmt.Errorf("failed to detach external subtasks: %w", err)
			}
			if err := cascadeDeleteResidentSubtasks(tx, ew, attr, taskUUID); err != nil {
				return fmt.Errorf("failed to cascade delete resident subtasks: %w", err)
			}
		}

		// After state update, check which tasks are now fully unblocked
		// (all their blockers are in completion states)
		if transitioningToCompletion {
			for _, blockedUUID := range potentiallyUnblockedUUIDs {
				// Count remaining incomplete blockers for this task
				var incompleteBlockerCount int
				err := tx.QueryRow(`
					SELECT COUNT(*)
					FROM task_relations r
					JOIN tasks t ON r.from_task_uuid = t.uuid
					WHERE r.to_task_uuid = ?
					  AND r.kind = 'blocks'
					  AND t.state NOT IN ('completed', 'archived', 'deleted', 'cancelled', 'idea')
				`, blockedUUID).Scan(&incompleteBlockerCount)
				if err != nil {
					return fmt.Errorf("failed to count blockers for task %s: %w", blockedUUID, err)
				}

				// If no more incomplete blockers, this task is now unblocked
				if incompleteBlockerCount == 0 {
					unblockedTaskUUIDs = append(unblockedTaskUUIDs, blockedUUID)
				}
			}
		}

		// Log event with structured payload. caused_by (a non-column field) is
		// merged back into the payload as a friendly-ID array so `wrkq log --patch`
		// shows it like any other field edit.
		payloadFields := fields
		eventChanged := fieldNames
		eventChanges := buildWebhookChanges(oldValues, fields)
		if causedByUpdate != nil {
			payloadFields = make(map[string]interface{}, len(fields)+1)
			for k, v := range fields {
				payloadFields[k] = v
			}
			newIDs := causedByUpdate.FriendlyIDs()
			payloadFields[causedByFieldKey] = newIDs
			eventChanged = append(append([]string{}, fieldNames...), causedByFieldKey)
			sort.Strings(eventChanged)
			eventChanges[causedByFieldKey] = webhooks.Change{From: causedByOldIDs, To: newIDs}
		}
		if hasStateChange && currentState != newState {
			if err := StampTaskCampaignContext(tx, taskUUID, payloadFields); err != nil {
				return err
			}
		}
		changesJSON, err := json.Marshal(payloadFields)
		if err != nil {
			return fmt.Errorf("failed to marshal changes: %w", err)
		}
		changesStr := string(changesJSON)
		newETag = currentETag + 1

		meta, err := ew.LogEventReturning(tx, &domain.Event{
			PrincipalRef: attr.PrincipalRef,
			ScopeRef:     attr.ScopeRef,
			ResourceType: "task",
			ResourceUUID: &taskUUID,
			EventType:    "task.updated",
			ETag:         &newETag,
			Payload:      &changesStr,
		})
		if err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}
		if outcome, ok := fields["outcome"]; ok {
			outcomePayload := map[string]any{
				"task_uuid": taskUUID,
				"outcome":   outcome,
			}
			if err := StampTaskCampaignContext(tx, taskUUID, outcomePayload); err != nil {
				return err
			}
			outcomeJSON, err := json.Marshal(outcomePayload)
			if err != nil {
				return fmt.Errorf("failed to marshal task.outcome_set payload: %w", err)
			}
			outcomePayloadStr := string(outcomeJSON)
			if _, err := ew.LogEventReturning(tx, &domain.Event{
				PrincipalRef: attr.PrincipalRef,
				ScopeRef:     attr.ScopeRef,
				ResourceType: "task",
				ResourceUUID: &taskUUID,
				EventType:    "task.outcome_set",
				ETag:         &newETag,
				Payload:      &outcomePayloadStr,
			}); err != nil {
				return fmt.Errorf("failed to log task.outcome_set event: %w", err)
			}
		}
		var transition *webhooks.Transition
		if hasStateChange && currentState != newState {
			transition = &webhooks.Transition{From: stringPtr(currentState), To: stringPtr(newState)}
		}
		webhooksToDispatch = append(webhooksToDispatch, pendingWebhook{
			taskUUID: taskUUID,
			ctx: webhooks.EventContext{
				Metadata:     meta,
				Event:        "updated",
				PrincipalRef: attr.PrincipalRef,
				Via:          via,
				Transition:   transition,
				Changed:      eventChanged,
				Changes:      eventChanges,
			},
		})

		for _, unblockedUUID := range unblockedTaskUUIDs {
			blockersAfter, err := blockerInfosForTask(tx, unblockedUUID)
			if err != nil {
				return fmt.Errorf("failed to load updated blockers for task %s: %w", unblockedUUID, err)
			}
			payload := map[string]interface{}{
				"cause_task_uuid": taskUUID,
				"cause_state":     newState,
			}
			payloadJSON, _ := json.Marshal(payload)
			payloadStr := string(payloadJSON)
			unblockedMeta, err := ew.LogEventReturning(tx, &domain.Event{
				PrincipalRef: attr.PrincipalRef,
				ScopeRef:     attr.ScopeRef,
				ResourceType: "task",
				ResourceUUID: &unblockedUUID,
				EventType:    "task.unblocked",
				Payload:      &payloadStr,
			})
			if err != nil {
				return fmt.Errorf("failed to log unblock event: %w", err)
			}
			webhooksToDispatch = append(webhooksToDispatch, pendingWebhook{
				taskUUID: unblockedUUID,
				ctx: webhooks.EventContext{
					Metadata:     unblockedMeta,
					Event:        "unblocked",
					PrincipalRef: attr.PrincipalRef,
					Via:          via,
					Transition:   nil,
					Changed:      []string{"blocked_by"},
					Changes: map[string]webhooks.Change{
						"blocked_by": {From: blockersBefore[unblockedUUID], To: blockersAfter},
					},
				},
			})
		}

		if hasStateChange && currentState != newState &&
			!isCompletionState(currentState) && isCompletionState(newState) {
			if err := maybeLogCampaignCloseNudgeForTask(tx, ew, attr, taskUUID); err != nil {
				return err
			}
		}

		return nil
	})

	if err == nil {
		for _, pending := range webhooksToDispatch {
			webhooks.DispatchTaskEvent(ts.store.db, pending.taskUUID, pending.ctx)
		}
	}

	return newETag, err
}

// validateParentAssignment enforces the invariants for re-parenting a task:
// the parent must exist, a task may not be its own parent, and the single-level
// subtask depth (max depth 1) must hold globally across containers/projects —
// neither the parent being a subtask nor the child already having subtasks.
func validateParentAssignment(tx *sql.Tx, childUUID, parentUUID string) error {
	if parentUUID == childUUID {
		return fmt.Errorf("a task cannot be its own parent")
	}

	var childExists int
	if err := tx.QueryRow("SELECT COUNT(*) FROM tasks WHERE uuid = ?", childUUID).Scan(&childExists); err != nil {
		return fmt.Errorf("failed to load task: %w", err)
	}
	if childExists == 0 {
		return fmt.Errorf("task not found: %s", childUUID)
	}

	var parentParent sql.NullString
	err := tx.QueryRow("SELECT parent_task_uuid FROM tasks WHERE uuid = ?", parentUUID).Scan(&parentParent)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("parent task not found: %s", parentUUID)
		}
		return fmt.Errorf("failed to load parent task: %w", err)
	}

	if parentParent.Valid && parentParent.String != "" {
		return fmt.Errorf("parent task is itself a subtask (max depth is 1)")
	}

	var childHasSubtasks int
	if err := tx.QueryRow(
		"SELECT COUNT(*) FROM tasks WHERE parent_task_uuid = ? AND state != 'deleted'",
		childUUID,
	).Scan(&childHasSubtasks); err != nil {
		return fmt.Errorf("failed to check for existing subtasks: %w", err)
	}
	if childHasSubtasks > 0 {
		return fmt.Errorf("cannot reparent a task that already has subtasks (max depth is 1)")
	}

	return nil
}

// Move moves a task to a different container and logs task.moved events.
// Returns the new etag on success.
func (ts *TaskStore) Move(actorUUID, taskUUID, newProjectUUID string, ifMatch int64) (int64, error) {
	return ts.MoveWithAttribution(ts.store.attributionFromActorUUID(actorUUID), taskUUID, newProjectUUID, ifMatch)
}

// MoveWithAttribution moves a task using canonical principal attribution.
func (ts *TaskStore) MoveWithAttribution(attr attribution.Attribution, taskUUID, newProjectUUID string, ifMatch int64) (int64, error) {
	return ts.MoveWithViaAttribution(attr, taskUUID, newProjectUUID, ifMatch, "cli")
}

// MoveWithViaAttribution moves a task subtree and records the ingress surface.
func (ts *TaskStore) MoveWithViaAttribution(attr attribution.Attribution, taskUUID, newProjectUUID string, ifMatch int64, via string) (int64, error) {
	if err := requireAttribution(attr); err != nil {
		return 0, err
	}
	if via == "" {
		via = "cli"
	}
	var newETag int64
	var webhooksToDispatch []pendingWebhook

	err := ts.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		type moveTask struct {
			uuid             string
			currentETag      int64
			oldProjectUUID   string
			parentTaskUUID   sql.NullString
			oldContainerPath string
		}

		var root moveTask
		err := tx.QueryRow(`
			SELECT t.uuid, t.etag, t.project_uuid, t.parent_task_uuid, COALESCE(cp.path, '')
			FROM tasks t
			LEFT JOIN v_container_paths cp ON cp.uuid = t.project_uuid
			WHERE t.uuid = ?
		`, taskUUID).Scan(&root.uuid, &root.currentETag, &root.oldProjectUUID, &root.parentTaskUUID, &root.oldContainerPath)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("task not found: %s", taskUUID)
			}
			return fmt.Errorf("failed to get task: %w", err)
		}

		if err := checkETag(root.currentETag, ifMatch); err != nil {
			return err
		}

		if root.parentTaskUUID.Valid && root.oldProjectUUID != newProjectUUID {
			var parentProjectUUID string
			err := tx.QueryRow("SELECT project_uuid FROM tasks WHERE uuid = ?", root.parentTaskUUID.String).Scan(&parentProjectUUID)
			if err != nil {
				return fmt.Errorf("failed to load parent task: %w", err)
			}
			if parentProjectUUID == root.oldProjectUUID {
				return fmt.Errorf("cannot move subtask across containers independently; move the root task instead")
			}
		}

		rows, err := tx.Query(`
			WITH RECURSIVE subtree(uuid, project_uuid) AS (
				SELECT uuid, project_uuid FROM tasks WHERE uuid = ?
				UNION ALL
				SELECT t.uuid, t.project_uuid
				  FROM tasks t
				  JOIN subtree s ON t.parent_task_uuid = s.uuid
				 WHERE t.project_uuid = s.project_uuid
			)
			SELECT t.uuid, t.etag, t.project_uuid, t.parent_task_uuid, COALESCE(cp.path, '')
			  FROM tasks t
			  JOIN subtree s ON s.uuid = t.uuid
			  LEFT JOIN v_container_paths cp ON cp.uuid = t.project_uuid
			 ORDER BY CASE WHEN t.uuid = ? THEN 0 ELSE 1 END, t.id
		`, taskUUID, taskUUID)
		if err != nil {
			return fmt.Errorf("failed to load task subtree: %w", err)
		}
		moveSet := []moveTask{}
		for rows.Next() {
			var mt moveTask
			if err := rows.Scan(&mt.uuid, &mt.currentETag, &mt.oldProjectUUID, &mt.parentTaskUUID, &mt.oldContainerPath); err != nil {
				_ = rows.Close()
				return fmt.Errorf("failed to scan task subtree: %w", err)
			}
			moveSet = append(moveSet, mt)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("failed to close task subtree rows: %w", err)
		}
		if len(moveSet) == 0 {
			return fmt.Errorf("task not found: %s", taskUUID)
		}

		allAlreadyInTarget := true
		for _, mt := range moveSet {
			if mt.oldProjectUUID != newProjectUUID {
				allAlreadyInTarget = false
				break
			}
		}
		if allAlreadyInTarget {
			moveUUIDs := make([]string, 0, len(moveSet))
			for _, mt := range moveSet {
				moveUUIDs = append(moveUUIDs, mt.uuid)
			}
			if err := validateEffectiveMembershipTx(tx, campaignValidation{taskUUIDs: moveUUIDs, move: true}); err != nil {
				return err
			}
			newETag = root.currentETag
			return nil
		}

		moveUUIDs := make([]string, 0, len(moveSet))
		for _, mt := range moveSet {
			moveUUIDs = append(moveUUIDs, mt.uuid)
			if _, err := tx.Exec("UPDATE tasks SET project_uuid = ? WHERE uuid = ?", newProjectUUID, mt.uuid); err != nil {
				return fmt.Errorf("failed to stage task move: %w", err)
			}
		}
		if err := validateEffectiveMembershipTx(tx, campaignValidation{
			taskUUIDs: moveUUIDs, residentAdmission: true, move: true,
		}); err != nil {
			return err
		}

		var newContainerPath string
		_ = tx.QueryRow("SELECT path FROM v_container_paths WHERE uuid = ?", newProjectUUID).Scan(&newContainerPath)

		for _, mt := range moveSet {
			if _, err := tx.Exec(`
					UPDATE tasks
					SET etag = etag + 1,
						updated_by_principal_ref = ?,
						updated_by_scope_ref = ?
					WHERE uuid = ?
				`, attr.PrincipalRef, scopeSQL(attr), mt.uuid); err != nil {
				return fmt.Errorf("failed to move task: %w", err)
			}

			taskETag := mt.currentETag + 1
			if mt.uuid == taskUUID {
				newETag = taskETag
			}
			payload := map[string]interface{}{
				"oldContainerUuid": mt.oldProjectUUID,
				"newContainerUuid": newProjectUUID,
				"oldContainerPath": mt.oldContainerPath,
				"newContainerPath": newContainerPath,
				"old_project_uuid": mt.oldProjectUUID,
				"new_project_uuid": newProjectUUID,
			}
			if mt.uuid != taskUUID {
				payload["cascadeRootTaskUuid"] = taskUUID
			}
			payloadJSON, _ := json.Marshal(payload)
			payloadStr := string(payloadJSON)

			resourceUUID := mt.uuid
			meta, err := ew.LogEventReturning(tx, &domain.Event{
				PrincipalRef: attr.PrincipalRef,
				ScopeRef:     attr.ScopeRef,
				ResourceType: "task",
				ResourceUUID: &resourceUUID,
				EventType:    "task.moved",
				ETag:         &taskETag,
				Payload:      &payloadStr,
			})
			if err != nil {
				return fmt.Errorf("failed to log event: %w", err)
			}
			webhooksToDispatch = append(webhooksToDispatch, pendingWebhook{
				taskUUID: mt.uuid,
				ctx: webhooks.EventContext{
					Metadata:     meta,
					Event:        "moved",
					PrincipalRef: attr.PrincipalRef,
					Via:          via,
					Transition:   nil,
					Changed:      []string{"container_path", "project_uuid"},
					Changes: map[string]webhooks.Change{
						"container_path": {From: mt.oldContainerPath, To: newContainerPath},
						"project_uuid":   {From: mt.oldProjectUUID, To: newProjectUUID},
					},
				},
			})
		}
		return nil
	})

	if err == nil {
		for _, pending := range webhooksToDispatch {
			webhooks.DispatchTaskEvent(ts.store.db, pending.taskUUID, pending.ctx)
		}
	}

	return newETag, err
}

// ArchiveResult contains statistics about an archive operation.
type ArchiveResult struct {
	ETag int64
}

// Archive soft-deletes a task by setting state to 'archived' and archived_at timestamp.
func (ts *TaskStore) Archive(actorUUID, taskUUID string, ifMatch int64) (*ArchiveResult, error) {
	return ts.ArchiveWithVia(actorUUID, taskUUID, ifMatch, "cli")
}

// ArchiveWithVia archives a task and records the ingress surface in webhook origin.via.
func (ts *TaskStore) ArchiveWithVia(actorUUID, taskUUID string, ifMatch int64, via string) (*ArchiveResult, error) {
	return ts.ArchiveWithViaAttribution(ts.store.attributionFromActorUUID(actorUUID), taskUUID, ifMatch, via)
}

// ArchiveWithAttribution archives a task with canonical principal attribution.
func (ts *TaskStore) ArchiveWithAttribution(attr attribution.Attribution, taskUUID string, ifMatch int64) (*ArchiveResult, error) {
	return ts.ArchiveWithViaAttribution(attr, taskUUID, ifMatch, "cli")
}

// ArchiveWithViaAttribution archives a task and records the ingress surface.
func (ts *TaskStore) ArchiveWithViaAttribution(attr attribution.Attribution, taskUUID string, ifMatch int64, via string) (*ArchiveResult, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	if via == "" {
		via = "cli"
	}
	var result *ArchiveResult
	var webhookCtx webhooks.EventContext

	err := ts.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current state
		var currentETag int64
		var slug string
		var currentState string
		err := tx.QueryRow("SELECT etag, slug, state FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentETag, &slug, &currentState)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("task not found: %s", taskUUID)
			}
			return fmt.Errorf("failed to get task: %w", err)
		}

		// Check etag if ifMatch was provided
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}

		// Soft delete
		_, err = tx.Exec(`
			UPDATE tasks
			SET state = 'archived',
				archived_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
				updated_by_principal_ref = ?,
				updated_by_scope_ref = ?,
				etag = etag + 1
			WHERE uuid = ?
		`, attr.PrincipalRef, scopeSQL(attr), taskUUID)
		if err != nil {
			return fmt.Errorf("failed to archive task: %w", err)
		}

		// Log event
		payload := map[string]interface{}{
			"slug":        slug,
			"soft_delete": true,
		}
		payloadStr, err := stampTaskCampaignJSON(tx, taskUUID, payload)
		if err != nil {
			return err
		}
		newETag := currentETag + 1

		meta, err := ew.LogEventReturning(tx, &domain.Event{
			PrincipalRef: attr.PrincipalRef,
			ScopeRef:     attr.ScopeRef,
			ResourceType: "task",
			ResourceUUID: &taskUUID,
			EventType:    "task.archived",
			ETag:         &newETag,
			Payload:      &payloadStr,
		})
		if err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}
		if !isCompletionState(currentState) {
			if err := maybeLogCampaignCloseNudgeForTask(tx, ew, attr, taskUUID); err != nil {
				return err
			}
		}
		webhookCtx = webhooks.EventContext{
			Metadata:     meta,
			Event:        "archived",
			PrincipalRef: attr.PrincipalRef,
			Via:          via,
			Transition:   &webhooks.Transition{From: stringPtr(currentState), To: stringPtr("archived")},
			Changed:      []string{"archived_at", "state"},
			Changes: map[string]webhooks.Change{
				"state":       {From: currentState, To: "archived"},
				"archived_at": {From: nil, To: "now"},
			},
		}

		result = &ArchiveResult{ETag: newETag}
		return nil
	})

	if err == nil && result != nil {
		webhooks.DispatchTaskEvent(ts.store.db, taskUUID, webhookCtx)
	}

	return result, err
}

// PurgeResult contains statistics about a purge operation.
type PurgeResult struct {
	AttachmentsDeleted int
	BytesFreed         int64
}

// Purge hard-deletes a task. The caller must handle attachment file cleanup.
// Returns the purge result including attachment statistics.
func (ts *TaskStore) Purge(actorUUID, taskUUID string, ifMatch int64) (*PurgeResult, error) {
	return ts.PurgeWithAttribution(ts.store.attributionFromActorUUID(actorUUID), taskUUID, ifMatch)
}

// PurgeWithAttribution hard-deletes a task with canonical principal attribution.
func (ts *TaskStore) PurgeWithAttribution(attr attribution.Attribution, taskUUID string, ifMatch int64) (*PurgeResult, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	var result *PurgeResult
	var webhookInfo *webhooks.TaskInfo
	var webhookCtx webhooks.EventContext

	err := ts.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current state
		var currentETag int64
		var id, slug, currentState string
		err := tx.QueryRow("SELECT etag, id, slug, state FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentETag, &id, &slug, &currentState)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("task not found: %s", taskUUID)
			}
			return fmt.Errorf("failed to get task: %w", err)
		}

		// Check etag if ifMatch was provided
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}

		// Guard causal lineage: refuse to hard-purge a task that surviving tasks
		// still attribute their defect/rework to. The caller must clear those
		// dependents' caused_by first (or purge them too). This mirrors the DB-level
		// ON DELETE RESTRICT but yields a stable, explanatory error.
		var causedByReferrers int
		if err := tx.QueryRow(
			"SELECT COUNT(*) FROM task_causes WHERE caused_by_task_uuid = ?", taskUUID,
		).Scan(&causedByReferrers); err != nil {
			return fmt.Errorf("failed to check caused_by referrers: %w", err)
		}
		if causedByReferrers > 0 {
			return fmt.Errorf("cannot purge task %s: it is referenced by the caused_by lineage of %d surviving task(s); clear those references first", slug, causedByReferrers)
		}

		// Guard the room (T-07641): rooms.task_uuid cascades on delete, so a
		// purge would silently destroy the task's conversation, every envelope
		// in it, its members and receipts — including reply_required obligations
		// presented to another seat. Talk outlives the runtime that carried it;
		// it must outlive the task row too. Archive instead (rm without --purge).
		var roomCount, envelopeCount int
		if err := tx.QueryRow(
			`SELECT COUNT(r.uuid), COUNT(e.uuid) FROM rooms r
			 LEFT JOIN envelopes e ON e.room_uuid = r.uuid
			 WHERE r.task_uuid = ?`, taskUUID,
		).Scan(&roomCount, &envelopeCount); err != nil {
			return fmt.Errorf("failed to check task room: %w", err)
		}
		if roomCount > 0 {
			return fmt.Errorf("cannot purge task %s: it has a room with %d envelope(s); a purge would destroy the conversation and every live obligation in it — archive it instead (rm without --purge)", slug, envelopeCount)
		}

		// Capture webhook payload info before deletion
		info, err := webhooks.LookupTaskInfoWith(tx, taskUUID)
		if err != nil {
			return fmt.Errorf("failed to load webhook info: %w", err)
		}
		webhookInfo = &info

		campaignUUID := ""
		if !isCompletionState(currentState) {
			campaignUUID, err = campaignUUIDForTaskTx(tx, taskUUID)
			if err != nil {
				return err
			}
		}

		// Count attachments for statistics
		var attachmentCount int
		var totalBytes int64
		rows, err := tx.Query("SELECT size_bytes FROM attachments WHERE task_uuid = ?", taskUUID)
		if err != nil {
			return fmt.Errorf("failed to query attachments: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var size int64
			if err := rows.Scan(&size); err != nil {
				return fmt.Errorf("failed to scan attachment: %w", err)
			}
			attachmentCount++
			totalBytes += size
		}

		// Log event BEFORE deleting (so we can still reference the task)
		payload := map[string]interface{}{
			"slug":      slug,
			"purged_by": attr.PrincipalRef,
		}
		if attachmentCount > 0 {
			payload["attachment_count"] = attachmentCount
			payload["bytes_freed"] = totalBytes
		}
		if err := StampTaskCampaignContext(tx, taskUUID, payload); err != nil {
			return err
		}
		payloadJSON, _ := json.Marshal(payload)
		payloadStr := string(payloadJSON)

		meta, err := ew.LogEventReturning(tx, &domain.Event{
			PrincipalRef: attr.PrincipalRef,
			ScopeRef:     attr.ScopeRef,
			ResourceType: "task",
			ResourceUUID: &taskUUID,
			EventType:    "task.purged",
			Payload:      &payloadStr,
		})
		if err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}
		webhookCtx = webhooks.EventContext{
			Metadata:     meta,
			Event:        "purged",
			PrincipalRef: attr.PrincipalRef,
			Via:          "cli",
			Transition:   nil,
			Changed:      []string{},
			Changes:      map[string]webhooks.Change{},
		}

		if err := detachExternalSubtasks(tx, attr, taskUUID); err != nil {
			return fmt.Errorf("failed to detach external subtasks: %w", err)
		}
		if err := purgeResidentSubtasks(tx, ew, attr, taskUUID); err != nil {
			return fmt.Errorf("failed to purge resident subtasks: %w", err)
		}
		if err := retargetPromisesForPurgedTask(tx, ew, attr, taskUUID, id, slug); err != nil {
			return err
		}

		// Hard delete (CASCADE will delete attachments and comments)
		_, err = tx.Exec("DELETE FROM tasks WHERE uuid = ?", taskUUID)
		if err != nil {
			return fmt.Errorf("failed to delete task: %w", err)
		}
		if err := maybeLogCampaignCloseNudge(tx, ew, attr, campaignUUID); err != nil {
			return err
		}

		result = &PurgeResult{
			AttachmentsDeleted: attachmentCount,
			BytesFreed:         totalBytes,
		}
		return nil
	})

	if err == nil && webhookInfo != nil {
		webhooks.DispatchTaskInfoEvent(ts.store.db, *webhookInfo, webhookCtx)
	}

	return result, err
}

// AttachmentInfo contains info about an attachment for file cleanup.
type AttachmentInfo struct {
	TaskUUID     string
	RelativePath string
	SizeBytes    int64
}

// GetAttachments returns all attachments for a task.
func (ts *TaskStore) GetAttachments(taskUUID string) ([]AttachmentInfo, error) {
	rows, err := ts.store.db.Query("SELECT relative_path, size_bytes FROM attachments WHERE task_uuid = ?", taskUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query attachments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var attachments []AttachmentInfo
	for rows.Next() {
		var a AttachmentInfo
		if err := rows.Scan(&a.RelativePath, &a.SizeBytes); err != nil {
			return nil, fmt.Errorf("failed to scan attachment: %w", err)
		}
		attachments = append(attachments, a)
	}
	return attachments, nil
}

// GetByUUID retrieves a task by UUID.
func (ts *TaskStore) GetByUUID(uuid string) (*domain.Task, error) {
	task := &domain.Task{}
	// Use string intermediates for nullable time fields since SQLite stores times as strings
	var startAt, dueAt, labels, meta, outcome, campaignUUID, completedAt, archivedAt *string
	var requestedByProjectID, assignedProjectID, acknowledgedAt, resolution, parentTaskUUID *string
	var sdkSessionID *string
	var workflowPreset, phase, riskClass *string
	var presetVersion sql.NullInt64
	var createdAt, updatedAt string
	var createdByPrincipal, updatedByPrincipal, createdByScope, updatedByScope sql.NullString

	err := ts.store.db.QueryRow(`
		SELECT uuid, id, slug, title, project_uuid, requested_by_project_id, assigned_project_id,
			   state, priority, kind, parent_task_uuid,
			   workflow_preset, preset_version, phase, risk_class,
			   start_at, due_at, labels, meta, description, specification, outcome, campaign_uuid, etag,
			   created_at, updated_at, completed_at, archived_at,
			   acknowledged_at, resolution,
			   sdk_session_id,
			   created_by_principal_ref, updated_by_principal_ref,
			   created_by_scope_ref, updated_by_scope_ref
		FROM tasks WHERE uuid = ?
	`, uuid).Scan(
		&task.UUID, &task.ID, &task.Slug, &task.Title, &task.ProjectUUID,
		&requestedByProjectID, &assignedProjectID, &task.State, &task.Priority, &task.Kind, &parentTaskUUID,
		&workflowPreset, &presetVersion, &phase, &riskClass,
		&startAt, &dueAt, &labels, &meta, &task.Description, &task.Specification, &outcome, &campaignUUID, &task.ETag,
		&createdAt, &updatedAt, &completedAt, &archivedAt,
		&acknowledgedAt, &resolution,
		&sdkSessionID,
		&createdByPrincipal, &updatedByPrincipal,
		&createdByScope, &updatedByScope,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("task not found: %s", uuid)
		}
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	task.RequestedByProjectID = requestedByProjectID
	task.AssignedProjectID = assignedProjectID
	task.ParentTaskUUID = parentTaskUUID
	task.Resolution = resolution
	task.AcknowledgedAt = parseTimeNullable(acknowledgedAt)
	task.SDKSessionID = sdkSessionID
	task.WorkflowPreset = workflowPreset
	task.Phase = phase
	task.RiskClass = riskClass
	task.Outcome = outcome
	task.CampaignUUID = campaignUUID
	if createdByPrincipal.Valid {
		task.CreatedByPrincipalRef = createdByPrincipal.String
	}
	if updatedByPrincipal.Valid {
		task.UpdatedByPrincipalRef = updatedByPrincipal.String
	}
	if createdByScope.Valid {
		task.CreatedByScopeRef = createdByScope.String
	}
	if updatedByScope.Valid {
		task.UpdatedByScopeRef = updatedByScope.String
	}
	if presetVersion.Valid {
		value := int(presetVersion.Int64)
		task.PresetVersion = &value
	}

	// Store the labels as-is since it's a JSON string
	task.Labels = labels
	task.Meta = meta

	return task, nil
}

func parseTimeNullable(value *string) *time.Time {
	if value == nil || *value == "" {
		return nil
	}
	layouts := []string{time.RFC3339, "2006-01-02 15:04:05"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, *value); err == nil {
			return &t
		}
	}
	return nil
}

// BlockingTask represents a lightweight view of a task that is blocking another task.
// Used by BlockedBy to return only essential info for dependency checking.
type BlockingTask struct {
	UUID  string `json:"uuid"`
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	State string `json:"state"`
}

// BlockedBy returns all incomplete tasks that are blocking the given task.
// A task is considered "blocking" if there is a 'blocks' relation where
// the blocking task is the source (from_task_uuid) and the given task is the target (to_task_uuid).
// A task is considered "incomplete" if its state is NOT in: completed, archived, deleted, cancelled.
// Tasks in 'idea' state are also excluded as they represent uncommitted work.
func (ts *TaskStore) BlockedBy(taskUUID string) ([]BlockingTask, error) {
	rows, err := ts.store.db.Query(`
		SELECT t.uuid, t.id, t.slug, t.title, t.state
		FROM task_relations r
		JOIN tasks t ON r.from_task_uuid = t.uuid
		WHERE r.to_task_uuid = ?
		  AND r.kind = 'blocks'
		  AND t.state NOT IN ('completed', 'archived', 'deleted', 'cancelled', 'idea')
		ORDER BY t.id
	`, taskUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query blocking tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var blockers []BlockingTask
	for rows.Next() {
		var b BlockingTask
		if err := rows.Scan(&b.UUID, &b.ID, &b.Slug, &b.Title, &b.State); err != nil {
			return nil, fmt.Errorf("failed to scan blocking task: %w", err)
		}
		blockers = append(blockers, b)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating blocking tasks: %w", err)
	}

	// Return empty slice instead of nil for consistency
	if blockers == nil {
		blockers = []BlockingTask{}
	}

	return blockers, nil
}

// GetTasksBlockedBy returns all task UUIDs that are blocked by the given task.
// In other words, it finds tasks where the given task is the blocker (from_task_uuid).
// This is the inverse of BlockedBy - BlockedBy returns "who is blocking me",
// GetTasksBlockedBy returns "who am I blocking".
func (ts *TaskStore) GetTasksBlockedBy(blockerTaskUUID string) ([]string, error) {
	rows, err := ts.store.db.Query(`
		SELECT to_task_uuid
		FROM task_relations
		WHERE from_task_uuid = ?
		  AND kind = 'blocks'
	`, blockerTaskUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query blocked tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var blockedTasks []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return nil, fmt.Errorf("failed to scan blocked task: %w", err)
		}
		blockedTasks = append(blockedTasks, uuid)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating blocked tasks: %w", err)
	}

	// Return empty slice instead of nil for consistency
	if blockedTasks == nil {
		blockedTasks = []string{}
	}

	return blockedTasks, nil
}

// isCompletionState returns true if the given state represents a "completed" blocker
// that should no longer block other tasks.
func isCompletionState(state string) bool {
	switch state {
	case "completed", "cancelled", "archived", "deleted":
		return true
	default:
		return false
	}
}

// detachExternalSubtasks detaches child tasks that are linked by parent_task_uuid
// but live in a different resident container/project than the parent. Parent
// graph edges are not containment, so parent mutations must not delete or move
// external children.
func detachExternalSubtasks(tx *sql.Tx, attr attribution.Attribution, parentTaskUUID string) error {
	if _, err := tx.Exec(`
		UPDATE tasks
		SET parent_task_uuid = NULL,
		    kind = 'task',
		    etag = etag + 1,
		    updated_by_principal_ref = ?,
		    updated_by_scope_ref = ?
		WHERE parent_task_uuid = ?
		  AND state != 'deleted'
		  AND project_uuid != (SELECT project_uuid FROM tasks WHERE uuid = ?)
	`, attr.PrincipalRef, scopeSQL(attr), parentTaskUUID, parentTaskUUID); err != nil {
		return fmt.Errorf("failed to detach external subtasks: %w", err)
	}
	return nil
}

// detachExternalSubtasksForParents detaches external child backlinks for a set of
// parent tasks that are about to be hard-deleted.
func detachExternalSubtasksForParents(tx *sql.Tx, attr attribution.Attribution, parentTaskUUIDs []string) error {
	for _, parentTaskUUID := range parentTaskUUIDs {
		if err := detachExternalSubtasks(tx, attr, parentTaskUUID); err != nil {
			return err
		}
	}
	return nil
}

// cascadeDeleteResidentSubtasks deletes same-residency subtasks when a parent
// task is deleted. External children are detached by detachExternalSubtasks.
// This is called within a transaction when a task's state is set to 'deleted'.
func cascadeDeleteResidentSubtasks(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, parentTaskUUID string) error {
	// Find all subtasks (not already deleted)
	rows, err := tx.Query(`
		SELECT c.uuid
		FROM tasks c
		JOIN tasks p ON p.uuid = c.parent_task_uuid
		WHERE c.parent_task_uuid = ?
		  AND c.state != 'deleted'
		  AND c.project_uuid = p.project_uuid
	`, parentTaskUUID)
	if err != nil {
		return fmt.Errorf("failed to query subtasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var subtaskUUIDs []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return fmt.Errorf("failed to scan subtask: %w", err)
		}
		subtaskUUIDs = append(subtaskUUIDs, uuid)
	}
	_ = rows.Close()

	// Delete each subtask
	for _, subtaskUUID := range subtaskUUIDs {
		_, err := tx.Exec(`
			UPDATE tasks
			SET state = 'deleted',
			    updated_by_principal_ref = ?,
			    updated_by_scope_ref = ?,
			    deleted_by_principal_ref = ?,
			    deleted_by_scope_ref = ?
			WHERE uuid = ?
		`, attr.PrincipalRef, scopeSQL(attr), attr.PrincipalRef, scopeSQL(attr), subtaskUUID)
		if err != nil {
			return fmt.Errorf("failed to delete subtask %s: %w", subtaskUUID, err)
		}

		// Log event
		payload, err := stampTaskCampaignJSON(tx, subtaskUUID, map[string]any{"action": "cascade_deleted", "parent_deleted": true})
		if err != nil {
			return err
		}
		if err := ew.LogEvent(tx, &domain.Event{
			PrincipalRef: attr.PrincipalRef,
			ScopeRef:     attr.ScopeRef,
			ResourceType: "task",
			ResourceUUID: &subtaskUUID,
			EventType:    "task.deleted",
			Payload:      &payload,
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		if err := detachExternalSubtasks(tx, attr, subtaskUUID); err != nil {
			return err
		}

		// Recursively delete nested resident subtasks
		if err := cascadeDeleteResidentSubtasks(tx, ew, attr, subtaskUUID); err != nil {
			return err
		}
	}

	return nil
}

// purgeResidentSubtasks hard-deletes same-residency subtasks before their parent
// task is purged. The schema no longer cascades via parent_task_uuid because
// cross-project parent edges are graph backlinks, not containment.
func purgeResidentSubtasks(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, parentTaskUUID string) error {
	rows, err := tx.Query(`
		SELECT c.uuid, c.id, c.slug
		FROM tasks c
		JOIN tasks p ON p.uuid = c.parent_task_uuid
		WHERE c.parent_task_uuid = ?
		  AND c.project_uuid = p.project_uuid
		ORDER BY c.id
	`, parentTaskUUID)
	if err != nil {
		return fmt.Errorf("failed to query resident subtasks for purge: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type subtask struct {
		uuid string
		id   string
		slug string
	}
	var subtasks []subtask
	for rows.Next() {
		var st subtask
		if err := rows.Scan(&st.uuid, &st.id, &st.slug); err != nil {
			return fmt.Errorf("failed to scan resident subtask for purge: %w", err)
		}
		subtasks = append(subtasks, st)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate resident subtasks for purge: %w", err)
	}

	for _, st := range subtasks {
		if err := detachExternalSubtasks(tx, attr, st.uuid); err != nil {
			return err
		}
		if err := purgeResidentSubtasks(tx, ew, attr, st.uuid); err != nil {
			return err
		}
		payloadMap := map[string]interface{}{
			"slug":                st.slug,
			"purged_by":           attr.PrincipalRef,
			"cascadeRootTaskUuid": parentTaskUUID,
		}
		payloadStr, err := stampTaskCampaignJSON(tx, st.uuid, payloadMap)
		if err != nil {
			return err
		}
		if err := ew.LogEvent(tx, &domain.Event{
			PrincipalRef: attr.PrincipalRef,
			ScopeRef:     attr.ScopeRef,
			ResourceType: "task",
			ResourceUUID: &st.uuid,
			EventType:    "task.purged",
			Payload:      &payloadStr,
		}); err != nil {
			return fmt.Errorf("failed to log resident subtask purge event: %w", err)
		}
		if err := retargetPromisesForPurgedTask(tx, ew, attr, st.uuid, st.id, st.slug); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM tasks WHERE uuid = ?", st.uuid); err != nil {
			return fmt.Errorf("failed to purge resident subtask %s: %w", st.uuid, err)
		}
	}
	return nil
}
