package store

import (
	"database/sql"
	"fmt"
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

// CreateParams contains parameters for creating a new task.
type CreateParams struct {
	UUID                  string // optional: force specific UUID instead of auto-generating
	Slug                  string
	Title                 string
	Description           string
	Specification         string
	ProjectUUID           string
	State                 domain.State
	Priority              int
	Kind                  string  // task, subtask, spike, bug, chore - defaults to "task"
	ParentTaskUUID        *string // for child tasks
	SubtaskOwnerUUID      *string // immutable named-subtask owner
	AssigneeActorUUID     *string // task assignment
	AssigneePrincipalRef  *string // canonical task assignment
	RequesterPrincipalRef *string // who asked for the work (not creator attribution)
	RequesterScopeRef     *string // requester's canonical ScopeRef; requires RequesterPrincipalRef
	RequestedByProjectID  *string
	AssignedProjectID     *string
	Resolution            *string
	WorkflowPreset        *string
	PresetVersion         *int
	Phase                 *string
	RiskClass             *string
	Labels                string  // JSON array
	Meta                  *string // JSON object
	DueAt                 string
	StartAt               string
	CausedBy              []CausedByRef // ordered, de-duplicated causal lineage edges
	CampaignUUID          *string       // optional campaign ENROLMENT at create time (cross-project membership)
	Via                   string        // origin.via for webhooks; defaults to "cli"
	CreatorScopeRef       string        // full praesidium scopeRef of the creating agent; stored as created_by_scope_ref
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
		// The server derives named-subtask identity and residency inside the write transaction.
		taskID := ""
		if params.SubtaskOwnerUUID != nil {
			var ownerID, ownerState string
			var ownerOwner sql.NullString
			if err := tx.QueryRow("SELECT id, project_uuid, state, subtask_owner_uuid FROM tasks WHERE uuid = ?", *params.SubtaskOwnerUUID).Scan(&ownerID, &params.ProjectUUID, &ownerState, &ownerOwner); err != nil {
				return fmt.Errorf("subtask owner not found: %w", err)
			}
			if ownerOwner.Valid {
				return fmt.Errorf("subtask owner must be an ordinary task")
			}
			if ownerState == "archived" || ownerState == "deleted" {
				return fmt.Errorf("cannot create subtask under archived or deleted owner")
			}
			if params.ParentTaskUUID != nil || params.CampaignUUID != nil {
				return fmt.Errorf("subtask cannot have parent or campaign")
			}
			taskID = ownerID + "." + params.Slug
		}
		// Build query - include uuid column only if forcing a specific UUID
		var query string
		var args []interface{}

		if params.UUID != "" {
			query = `INSERT INTO tasks (uuid, id, slug, title, description, specification, project_uuid, state, priority, kind,
				parent_task_uuid, subtask_owner_uuid, assignee_principal_ref, requester_principal_ref, requester_scope_ref, requested_by_project_id, assigned_project_id, resolution,
				workflow_preset, preset_version, phase, risk_class,
				labels, meta, due_at, start_at,
				created_by_principal_ref, updated_by_principal_ref, created_by_scope_ref, updated_by_scope_ref)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
			args = append(args, params.UUID)
		} else {
			query = `INSERT INTO tasks (id, slug, title, description, specification, project_uuid, state, priority, kind,
				parent_task_uuid, subtask_owner_uuid, assignee_principal_ref, requester_principal_ref, requester_scope_ref, requested_by_project_id, assigned_project_id, resolution,
				workflow_preset, preset_version, phase, risk_class,
				labels, meta, due_at, start_at,
				created_by_principal_ref, updated_by_principal_ref, created_by_scope_ref, updated_by_scope_ref)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
		}

		// Common args for both cases
		args = append(args,
			taskID, // ordinary IDs auto-generated by trigger
			params.Slug,
			params.Title,
			params.Description,
			params.Specification,
			params.ProjectUUID,
			string(params.State),
			params.Priority,
			kind,
			params.ParentTaskUUID,
			params.SubtaskOwnerUUID,
			params.AssigneePrincipalRef,
			params.RequesterPrincipalRef,
			params.RequesterScopeRef,
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
		if params.SubtaskOwnerUUID == nil {
			if err := validateEffectiveMembershipTx(tx, campaignValidation{
				taskUUIDs:         []string{uuid},
				residentAdmission: true,
				enrollmentChange:  params.CampaignUUID != nil,
			}); err != nil {
				return err
			}

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
		if params.SubtaskOwnerUUID != nil {
			payload["subtask_owner_uuid"] = *params.SubtaskOwnerUUID
		}
		if params.RequesterPrincipalRef != nil {
			payload["requester_principal_ref"] = *params.RequesterPrincipalRef
		}
		if params.RequesterScopeRef != nil {
			payload["requester_scope_ref"] = *params.RequesterScopeRef
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

		// The event carries the task's affiliation stamp so the project
		// timeline can place the creation by production-time container, the
		// same immutable stamp a state change carries. The webhook change set
		// below is built from the unstamped fields.
		eventPayload := make(map[string]interface{}, len(payload)+2)
		for k, v := range payload {
			eventPayload[k] = v
		}
		meta, err := logTaskEvent(tx, ew, attr, uuid, "task.created", &etag, eventPayload)
		if err != nil {
			return err
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
			   state, priority, kind, parent_task_uuid, subtask_owner_uuid,
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
		&requestedByProjectID, &assignedProjectID, &task.State, &task.Priority, &task.Kind, &parentTaskUUID, &task.SubtaskOwnerUUID,
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
