package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
)

const nsTaskCreate = "wrkq.task.create"

// TaskCreate creates a task and returns the WrkqTask DTO. It enforces mandatory
// idempotency when an idempotencyKey is supplied (§8.2).
func (a *API) TaskCreate(ctx context.Context, p TaskCreateParams) (*WrkqTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Title) == "" {
		return nil, NewValidationError("title is required", map[string]any{
			"field":    "title",
			"expected": "non-empty string",
		})
	}

	var requestHash string
	if p.IdempotencyKey != "" {
		hp := p
		hp.IdempotencyKey = ""
		requestHash = canonicalRequestHash(hp)
		if raw, ok, err := a.idempotentReplay(nsTaskCreate, p.IdempotencyKey, requestHash); err != nil {
			return nil, err
		} else if ok {
			var dto WrkqTask
			if err := json.Unmarshal(raw, &dto); err != nil {
				return nil, NewInternalError(err)
			}
			return &dto, nil
		}
	}

	projectUUID, slug, err := a.resolveCreateTarget(p)
	if err != nil {
		return nil, err
	}

	state := domain.StateOpen
	if strings.TrimSpace(p.State) != "" {
		parsed, perr := domain.ParseState(p.State)
		if perr != nil {
			return nil, NewValidationError(perr.Error(), map[string]any{"field": "state"})
		}
		state = parsed
	}
	if p.Kind != "" {
		if kerr := domain.ValidateTaskKind(p.Kind); kerr != nil {
			return nil, NewValidationError(kerr.Error(), map[string]any{"field": "kind"})
		}
	}
	var riskClass *string
	if strings.TrimSpace(p.RiskClass) != "" {
		trimmed := strings.TrimSpace(p.RiskClass)
		if rerr := domain.ValidateTaskRiskClass(trimmed); rerr != nil {
			return nil, NewValidationError(rerr.Error(), map[string]any{"field": "riskClass"})
		}
		riskClass = &trimmed
	}
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	if verr := domain.ValidatePriority(priority); verr != nil {
		return nil, NewValidationError(verr.Error(), map[string]any{"field": "priority"})
	}

	var parentTaskUUID *string
	if strings.TrimSpace(p.ParentTask) != "" {
		uuid, _, rerr := selectors.ResolveTask(a.db, p.ParentTask)
		if rerr != nil {
			return nil, NewNotFoundError(p.ParentTask, "task")
		}
		parentTaskUUID = &uuid
	}
	var assigneePrincipalRef *string
	if strings.TrimSpace(p.AssigneePrincipalRef) != "" {
		trimmed := strings.TrimSpace(p.AssigneePrincipalRef)
		assigneePrincipalRef = &trimmed
	}

	attr := a.attributionFor(p.Actor)
	result, err := a.store.Tasks.CreateWithAttribution(attr, store.CreateParams{
		Slug:                 slug,
		Title:                p.Title,
		Description:          p.Description,
		Specification:        p.Specification,
		ProjectUUID:          projectUUID,
		State:                state,
		Priority:             priority,
		Kind:                 p.Kind,
		ParentTaskUUID:       parentTaskUUID,
		AssigneePrincipalRef: assigneePrincipalRef,
		Labels:               labelsString(p.Labels),
		Meta:                 metaString(p.Meta),
		RiskClass:            riskClass,
		Via:                  "rpc",
	})
	if err != nil {
		return nil, mapStoreError(err, "")
	}

	dto, err := a.loadTask(result.UUID)
	if err != nil {
		return nil, err
	}
	if p.IdempotencyKey != "" {
		if serr := a.idempotentStore(nsTaskCreate, p.IdempotencyKey, requestHash, dto); serr != nil {
			return nil, serr
		}
	}
	return dto, nil
}

// resolveCreateTarget resolves the destination project UUID and task slug for a
// create request from its path/project selectors (or defaults).
func (a *API) resolveCreateTarget(p TaskCreateParams) (projectUUID, slug string, err error) {
	switch {
	case strings.TrimSpace(p.Path) != "":
		parentUUID, finalSlug, _, rerr := selectors.ResolveParentContainer(a.db, p.Path)
		if rerr != nil {
			return "", "", NewNotFoundError(p.Path, "container")
		}
		slug = finalSlug
		if parentUUID != nil {
			projectUUID = *parentUUID
		} else {
			projectUUID, err = a.defaultProjectUUID()
			if err != nil {
				return "", "", err
			}
		}
	case strings.TrimSpace(p.Project) != "":
		uuid, _, rerr := selectors.ResolveContainer(a.db, p.Project)
		if rerr != nil {
			return "", "", NewNotFoundError(p.Project, "container")
		}
		projectUUID = uuid
		slug = slugFromTitle(p.Title)
	default:
		projectUUID, err = a.defaultProjectUUID()
		if err != nil {
			return "", "", err
		}
		slug = slugFromTitle(p.Title)
	}
	return projectUUID, slug, nil
}

// defaultProjectUUID returns the first project container. Tasks cannot live
// directly under the root container, so when no project exists yet (e.g. a
// freshly migrated database) a default "inbox" project is auto-created.
func (a *API) defaultProjectUUID() (string, error) {
	var uuid string
	err := a.db.QueryRow("SELECT uuid FROM containers WHERE kind = 'project' ORDER BY id LIMIT 1").Scan(&uuid)
	if err == nil {
		return uuid, nil
	}
	if err != sql.ErrNoRows {
		return "", NewInternalError(err)
	}
	res, cerr := a.store.Containers.CreateWithAttribution(a.attributionFor(""), store.ContainerCreateParams{
		Slug:  "inbox",
		Title: "Inbox",
		Kind:  "project",
	})
	if cerr != nil {
		return "", mapStoreError(cerr, "")
	}
	return res.UUID, nil
}

func slugFromTitle(title string) string {
	if slug, err := paths.NormalizeSlug(title); err == nil && slug != "" {
		return slug
	}
	return "task"
}

// TaskShow returns the WrkqTask DTO for a task selector.
func (a *API) TaskShow(ctx context.Context, p TaskShowParams) (*WrkqTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uuid, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}
	return a.loadTask(uuid)
}

// TaskList returns a paginated list of WrkqTask DTOs with optional filters.
func (a *API) TaskList(ctx context.Context, p TaskListParams) (*WrkqTaskListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	sortField, sqlField, serr := normalizeTaskListSort(p.Sort)
	if serr != nil {
		return nil, serr
	}
	descending, derr := normalizeTaskListDirection(p.Direction)
	if derr != nil {
		return nil, derr
	}

	where := []string{}
	args := []any{}

	if strings.TrimSpace(p.Path) != "" {
		containerUUID, _, rerr := selectors.ResolveContainer(a.db, p.Path)
		if rerr != nil {
			return nil, NewNotFoundError(p.Path, "container")
		}
		if p.Recursive {
			// Subtree filter: match the target container path and every path
			// nested beneath it. cp.path is the task's container path from
			// v_container_paths (root slug excluded).
			var containerPath string
			perr := a.db.QueryRow("SELECT path FROM v_container_paths WHERE uuid = ?", containerUUID).Scan(&containerPath)
			switch {
			case perr == sql.ErrNoRows:
				// Container has no visible path (e.g. root) — direct filter only.
				where = append(where, "t.project_uuid = ?")
				args = append(args, containerUUID)
			case perr != nil:
				return nil, NewInternalError(perr)
			default:
				where = append(where, "(cp.path = ? OR cp.path LIKE ? || '/%')")
				args = append(args, containerPath, containerPath)
			}
		} else {
			where = append(where, "t.project_uuid = ?")
			args = append(args, containerUUID)
		}
	}
	if len(p.State) > 0 {
		ph, vals := inClause(p.State)
		where = append(where, "t.state IN ("+ph+")")
		args = append(args, vals...)
	} else if !p.IncludeDeleted {
		where = append(where, "t.state != 'deleted'")
	}
	if len(p.Kind) > 0 {
		ph, vals := inClause(p.Kind)
		where = append(where, "t.kind IN ("+ph+")")
		args = append(args, vals...)
	}
	if strings.TrimSpace(p.Assignee) != "" {
		where = append(where, "t.assignee_principal_ref = ?")
		args = append(args, p.Assignee)
	}

	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	page, err := cursor.Apply(p.Cursor, cursor.ApplyOptions{
		SortFields: []string{sortField},
		SQLFields:  []string{sqlField},
		Descending: []bool{descending},
		IDField:    "t.id",
		Limit:      limit,
	})
	if err != nil {
		return nil, NewValidationError("invalid cursor", map[string]any{"field": "cursor"})
	}

	query := "SELECT t.uuid, t.id, t.slug, t.title, t.project_uuid, t.state, t.priority, t.kind, t.description, t.specification, " +
		"t.labels, t.meta, t.etag, t.start_at, t.due_at, t.created_at, t.updated_at, t.completed_at, t.archived_at, t.deleted_at, t.acknowledged_at, " +
		"t.assignee_principal_ref, t.created_by_principal_ref, t.updated_by_principal_ref, COALESCE(t.risk_class,''), " +
		"COALESCE(cp.path || '/' || t.slug, t.slug) FROM tasks t LEFT JOIN v_container_paths cp ON cp.uuid = t.project_uuid"
	if page.WhereClause != "" {
		where = append(where, page.WhereClause)
		args = append(args, page.Params...)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " " + page.OrderByClause
	if page.LimitClause != "" {
		query += " " + page.LimitClause
		args = append(args, *page.LimitParam)
	}

	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()

	items := []WrkqTask{}
	for rows.Next() {
		task, _, scanErr := scanTaskRow(rows)
		if scanErr != nil {
			return nil, NewInternalError(scanErr)
		}
		items = append(items, *task)
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(err)
	}

	result := &WrkqTaskListResult{Items: items}
	if len(items) > limit {
		result.Items = items[:limit]
		anchor := result.Items[limit-1]
		// Encode the active (sort, direction) tuple into the cursor so it can
		// only be reused under the same ordering (cursor identity).
		next := &cursor.Cursor{
			SortFields: []string{sortField},
			LastValues: []any{taskCursorAnchor(anchor, sortField)},
			LastID:     anchor.ID,
			Descending: []bool{descending},
		}
		if encoded, cerr := next.Encode(); cerr == nil {
			result.NextCursor = encoded
		}
	}
	return result, nil
}

// taskListSortWhitelist maps each accepted sort field to its SQL expression.
// Mirrors the CLI find whitelist (created_at, updated_at, id, path) plus priority.
var taskListSortWhitelist = map[string]string{
	"created_at": "t.created_at",
	"updated_at": "t.updated_at",
	"priority":   "t.priority",
	"id":         "t.id",
	"path":       "cp.path || '/' || t.slug",
}

// normalizeTaskListSort validates the sort field against the whitelist and
// returns its logical name and SQL expression. An empty value defaults to
// created_at; any non-whitelisted value is rejected with WRKQ_VALIDATION.
func normalizeTaskListSort(field string) (logical, sqlExpr string, err error) {
	field = strings.TrimSpace(field)
	if field == "" {
		return "created_at", taskListSortWhitelist["created_at"], nil
	}
	sqlExpr, ok := taskListSortWhitelist[field]
	if !ok {
		return "", "", NewValidationError(
			"invalid sort field: "+field+" (choose created_at, updated_at, priority, id, or path)",
			map[string]any{"field": "sort"},
		)
	}
	return field, sqlExpr, nil
}

// normalizeTaskListDirection maps a direction string to a descending flag. An
// empty value preserves the default (ascending); only "asc"/"desc" are accepted,
// case-sensitively. Any other non-empty value is rejected with WRKQ_VALIDATION.
func normalizeTaskListDirection(direction string) (descending bool, err error) {
	switch strings.TrimSpace(direction) {
	case "", "asc":
		return false, nil
	case "desc":
		return true, nil
	default:
		return false, NewValidationError(
			"invalid direction: "+direction+" (choose asc or desc)",
			map[string]any{"field": "direction"},
		)
	}
}

// taskCursorAnchor returns the raw sort-column value for the given sort field,
// used as the cursor anchor for the next page.
func taskCursorAnchor(t WrkqTask, sortField string) any {
	switch sortField {
	case "priority":
		return t.Priority
	case "id":
		return t.ID
	case "updated_at":
		return t.updatedAtRaw
	case "path":
		return t.Path
	default: // created_at
		return t.createdAtRaw
	}
}

// TaskUpdate applies a patch with an atomic expectEtag CAS (§8.1).
func (a *API) TaskUpdate(ctx context.Context, p TaskUpdateParams) (*WrkqTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uuid, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}

	// Read current etag atomically against the CAS precondition. A supplied
	// expectEtag of 0 is a real (stale) precondition, not "skip".
	var currentEtag int64
	if scanErr := a.db.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", uuid).Scan(&currentEtag); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return nil, NewNotFoundError(p.Task, "task")
		}
		return nil, NewInternalError(scanErr)
	}
	if p.ExpectEtag != nil && *p.ExpectEtag != currentEtag {
		return nil, NewConflictError("task etag precondition failed", map[string]any{
			"expectEtag":  *p.ExpectEtag,
			"currentEtag": currentEtag,
		})
	}

	fields, ferr := patchFields(p.Patch)
	if ferr != nil {
		return nil, ferr
	}
	if len(fields) == 0 {
		// Nothing to change; return the current DTO.
		return a.loadTask(uuid)
	}

	attr := a.attributionFor(p.Actor)
	_, err = a.store.Tasks.UpdateFieldsWithViaAttribution(attr, uuid, fields, currentEtag, "rpc")
	if err != nil {
		var mismatch *domain.ETagMismatchError
		if errors.As(err, &mismatch) {
			return nil, NewConflictError("task update conflict", map[string]any{
				"currentEtag": mismatch.Actual,
			})
		}
		return nil, mapStoreError(err, p.Task)
	}
	return a.loadTask(uuid)
}

// TaskAcknowledge records a terminal-state receipt (acknowledged_at). It mirrors
// internal/cli/ack.go: state must be completed|cancelled unless force is set.
// An already-acknowledged task is a no-op — the current DTO is returned with its
// stable acknowledgedAt and no new write / etag bump.
func (a *API) TaskAcknowledge(ctx context.Context, p TaskAcknowledgeParams) (*WrkqTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uuid, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}

	var state string
	var acknowledgedAt sql.NullString
	if scanErr := a.db.QueryRow("SELECT state, acknowledged_at FROM tasks WHERE uuid = ?", uuid).Scan(&state, &acknowledgedAt); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return nil, NewNotFoundError(p.Task, "task")
		}
		return nil, NewInternalError(scanErr)
	}

	// Already acknowledged → no-op (mirror ack.go:106-115).
	if acknowledgedAt.Valid && strings.TrimSpace(acknowledgedAt.String) != "" {
		return a.loadTask(uuid)
	}

	if !p.Force && state != string(domain.StateCompleted) && state != string(domain.StateCancelled) {
		return nil, NewValidationError(
			"cannot acknowledge task: state is "+state+" (requires completed or cancelled)",
			map[string]any{"field": "state", "state": state},
		)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	attr := a.attributionFor(p.Actor)
	if _, uerr := a.store.Tasks.UpdateFieldsWithViaAttribution(attr, uuid, map[string]any{"acknowledged_at": now}, 0, "rpc"); uerr != nil {
		return nil, mapStoreError(uerr, p.Task)
	}
	return a.loadTask(uuid)
}

// TaskDelete performs a reversible delete: state='deleted' + deleted_at=now,
// cascading to subtasks. It never sets archived_at and never purges. Re-deleting
// an already-deleted task is a no-op return of the current task.
func (a *API) TaskDelete(ctx context.Context, p TaskDeleteParams) (*WrkqTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uuid, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}

	var state string
	if scanErr := a.db.QueryRow("SELECT state FROM tasks WHERE uuid = ?", uuid).Scan(&state); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return nil, NewNotFoundError(p.Task, "task")
		}
		return nil, NewInternalError(scanErr)
	}

	// Re-delete is a no-op (no etag bump).
	if state == string(domain.StateDeleted) {
		return a.loadTask(uuid)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	attr := a.attributionFor(p.Actor)
	// Setting state=deleted triggers cascade-delete of subtasks in the store.
	if _, uerr := a.store.Tasks.UpdateFieldsWithViaAttribution(attr, uuid, map[string]any{
		"state":      string(domain.StateDeleted),
		"deleted_at": now,
	}, 0, "rpc"); uerr != nil {
		return nil, mapStoreError(uerr, p.Task)
	}
	return a.loadTask(uuid)
}

// TaskRestore reverses delete/archive: current state must be archived or deleted,
// the target defaults to open (archived/deleted targets rejected), archived_at /
// deleted_at / deleted_by are cleared, and subtasks are cascade-restored. Mirrors
// internal/cli/restore.go.
func (a *API) TaskRestore(ctx context.Context, p TaskRestoreParams) (*WrkqTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uuid, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}

	// Validate target state.
	targetState := string(domain.StateOpen)
	if strings.TrimSpace(p.State) != "" {
		parsed, perr := domain.ParseState(p.State)
		if perr != nil {
			return nil, NewValidationError(perr.Error(), map[string]any{"field": "state"})
		}
		if parsed == domain.StateArchived || parsed == domain.StateDeleted {
			return nil, NewValidationError("cannot restore to "+p.State+" state", map[string]any{"field": "state"})
		}
		targetState = string(parsed)
	}

	var currentState string
	if scanErr := a.db.QueryRow("SELECT state FROM tasks WHERE uuid = ?", uuid).Scan(&currentState); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return nil, NewNotFoundError(p.Task, "task")
		}
		return nil, NewInternalError(scanErr)
	}
	if currentState != string(domain.StateArchived) && currentState != string(domain.StateDeleted) {
		return nil, NewValidationError(
			"task is not deleted or archived (current state: "+currentState+")",
			map[string]any{"field": "state", "state": currentState},
		)
	}

	attr := a.attributionFor(p.Actor)
	if rerr := a.restoreTaskTx(uuid, targetState, attr); rerr != nil {
		return nil, rerr
	}
	if rerr := a.cascadeRestoreSubtasks(uuid, targetState, attr); rerr != nil {
		return nil, rerr
	}
	return a.loadTask(uuid)
}

// restoreTaskTx clears the archived/deleted markers and sets the target state for
// a single task within a transaction, logging a task.restored event.
func (a *API) restoreTaskTx(taskUUID, targetState string, attr attribution.Attribution) error {
	tx, err := a.db.Begin()
	if err != nil {
		return NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	var currentState string
	if scanErr := tx.QueryRow("SELECT state FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentState); scanErr != nil {
		return NewInternalError(scanErr)
	}

	if _, eerr := tx.Exec(`
		UPDATE tasks
		SET state = ?, archived_at = NULL, deleted_at = NULL,
		    deleted_by_principal_ref = NULL, deleted_by_scope_ref = NULL,
		    etag = etag + 1,
		    updated_by_actor_uuid = ?, updated_by_principal_ref = ?, updated_by_scope_ref = ?
		WHERE uuid = ?
	`, targetState, legacyActorBind(attr), attr.PrincipalRef, scopeBind(attr), taskUUID); eerr != nil {
		return NewInternalError(eerr)
	}

	payload := `{"action":"restored","target_state":"` + targetState + `"}`
	if eerr := events.NewWriter(a.db.DB).LogEvent(tx, &domain.Event{
		ActorUUID:    attr.LegacyActorUUID,
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "task",
		ResourceUUID: &taskUUID,
		EventType:    "task.restored",
		Payload:      &payload,
	}); eerr != nil {
		return NewInternalError(eerr)
	}

	if cerr := tx.Commit(); cerr != nil {
		return NewInternalError(cerr)
	}
	return nil
}

// cascadeRestoreSubtasks restores all archived/deleted subtasks of a parent.
func (a *API) cascadeRestoreSubtasks(parentTaskUUID, targetState string, attr attribution.Attribution) error {
	rows, err := a.db.Query(
		"SELECT uuid FROM tasks WHERE parent_task_uuid = ? AND state IN ('archived', 'deleted')",
		parentTaskUUID,
	)
	if err != nil {
		return NewInternalError(err)
	}
	var subtaskUUIDs []string
	for rows.Next() {
		var u string
		if serr := rows.Scan(&u); serr != nil {
			_ = rows.Close()
			return NewInternalError(serr)
		}
		subtaskUUIDs = append(subtaskUUIDs, u)
	}
	_ = rows.Close()
	if rerr := rows.Err(); rerr != nil {
		return NewInternalError(rerr)
	}

	for _, subtaskUUID := range subtaskUUIDs {
		if rerr := a.restoreTaskTx(subtaskUUID, targetState, attr); rerr != nil {
			return rerr
		}
		if rerr := a.cascadeRestoreSubtasks(subtaskUUID, targetState, attr); rerr != nil {
			return rerr
		}
	}
	return nil
}

// patchFields converts a TaskPatch into the store field map (DB column names).
func patchFields(patch TaskPatch) (map[string]any, error) {
	fields := map[string]any{}
	if patch.Title != nil {
		fields["title"] = *patch.Title
	}
	if patch.Description != nil {
		fields["description"] = *patch.Description
	}
	if patch.Specification != nil {
		fields["specification"] = *patch.Specification
	}
	if patch.State != nil {
		if _, err := domain.ParseState(*patch.State); err != nil {
			return nil, NewValidationError(err.Error(), map[string]any{"field": "state"})
		}
		fields["state"] = *patch.State
	}
	if patch.Priority != nil {
		if err := domain.ValidatePriority(*patch.Priority); err != nil {
			return nil, NewValidationError(err.Error(), map[string]any{"field": "priority"})
		}
		fields["priority"] = *patch.Priority
	}
	if patch.Kind != nil {
		if err := domain.ValidateTaskKind(*patch.Kind); err != nil {
			return nil, NewValidationError(err.Error(), map[string]any{"field": "kind"})
		}
		fields["kind"] = *patch.Kind
	}
	if patch.RiskClass != nil {
		riskClass := strings.TrimSpace(*patch.RiskClass)
		if riskClass == "" {
			fields["risk_class"] = nil
		} else {
			if err := domain.ValidateTaskRiskClass(riskClass); err != nil {
				return nil, NewValidationError(err.Error(), map[string]any{"field": "riskClass"})
			}
			fields["risk_class"] = riskClass
		}
	}
	if patch.Labels != nil {
		fields["labels"] = labelsString(*patch.Labels)
	}
	if patch.Meta != nil {
		fields["meta"] = metaString(*patch.Meta)
	}
	if patch.AssigneePrincipalRef != nil {
		if *patch.AssigneePrincipalRef == "" {
			fields["assignee_principal_ref"] = nil
		} else {
			fields["assignee_principal_ref"] = *patch.AssigneePrincipalRef
		}
	}
	if patch.DueAt != nil {
		fields["due_at"] = *patch.DueAt
	}
	if patch.StartAt != nil {
		fields["start_at"] = *patch.StartAt
	}
	return fields, nil
}

// resolveTaskUUID resolves a task selector to its UUID, returning WRKQ_NOT_FOUND
// when the task does not exist.
func (a *API) resolveTaskUUID(selector string) (string, error) {
	if strings.TrimSpace(selector) == "" {
		return "", NewValidationError("task selector is required", map[string]any{"field": "task"})
	}
	uuid, _, err := selectors.ResolveTask(a.db, selector)
	if err != nil {
		return "", NewNotFoundError(selector, "task")
	}
	return uuid, nil
}

// loadTask reads a task by UUID into a WrkqTask DTO.
func (a *API) loadTask(uuid string) (*WrkqTask, error) {
	row := a.db.QueryRow(
		"SELECT t.uuid, t.id, t.slug, t.title, t.project_uuid, t.state, t.priority, t.kind, t.description, t.specification, "+
			"t.labels, t.meta, t.etag, t.start_at, t.due_at, t.created_at, t.updated_at, t.completed_at, t.archived_at, t.deleted_at, t.acknowledged_at, "+
			"t.assignee_principal_ref, t.created_by_principal_ref, t.updated_by_principal_ref, COALESCE(t.risk_class,''), "+
			"COALESCE(cp.path || '/' || t.slug, t.slug) FROM tasks t LEFT JOIN v_container_paths cp ON cp.uuid = t.project_uuid WHERE t.uuid = ?",
		uuid,
	)
	task, _, err := scanTaskRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, NewNotFoundError(uuid, "task")
		}
		return nil, NewInternalError(err)
	}
	return task, nil
}

// rowScanner abstracts *sql.Row and *sql.Rows for scanTaskRow.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanTaskRow scans a task row (column order matches the queries above) into a
// WrkqTask DTO, returning the raw created_at for cursor anchoring.
func scanTaskRow(s rowScanner) (*WrkqTask, string, error) {
	var (
		uuid, id, slug, title, projectUUID, state, kind, description, specification string
		labels, meta                                                                sql.NullString
		priority                                                                    int
		etag                                                                        int64
		createdAt, updatedAt                                                        string
		startAt, dueAt, completedAt, archivedAt, deletedAt, acknowledgedAt          sql.NullString
		assignee, createdByPrincipal, updatedByPrincipal                            sql.NullString
		riskClass, path                                                             string
	)
	if err := s.Scan(
		&uuid, &id, &slug, &title, &projectUUID, &state, &priority, &kind, &description, &specification,
		&labels, &meta, &etag, &startAt, &dueAt, &createdAt, &updatedAt, &completedAt, &archivedAt, &deletedAt, &acknowledgedAt,
		&assignee, &createdByPrincipal, &updatedByPrincipal, &riskClass, &path,
	); err != nil {
		return nil, "", err
	}
	task := &WrkqTask{
		UUID:                  uuid,
		ID:                    id,
		Slug:                  slug,
		Title:                 title,
		ProjectUUID:           projectUUID,
		Path:                  path,
		State:                 state,
		Priority:              priority,
		Kind:                  kind,
		RiskClass:             riskClass,
		Description:           description,
		Specification:         specification,
		Labels:                parseLabels(labels.String),
		Meta:                  parseMeta(meta.String),
		ETag:                  etag,
		StartAt:               toRFC3339(startAt.String),
		DueAt:                 toRFC3339(dueAt.String),
		CreatedAt:             toRFC3339(createdAt),
		UpdatedAt:             toRFC3339(updatedAt),
		CompletedAt:           toRFC3339(completedAt.String),
		ArchivedAt:            toRFC3339(archivedAt.String),
		DeletedAt:             toRFC3339(deletedAt.String),
		AcknowledgedAt:        toRFC3339(acknowledgedAt.String),
		AssigneePrincipalRef:  assignee.String,
		CreatedByPrincipalRef: createdByPrincipal.String,
		UpdatedByPrincipalRef: updatedByPrincipal.String,
		createdAtRaw:          createdAt,
		updatedAtRaw:          updatedAt,
	}
	return task, createdAt, nil
}

// inClause builds a "?, ?, ..." placeholder list and the matching args.
func inClause(values []string) (string, []any) {
	placeholders := make([]string, len(values))
	args := make([]any, len(values))
	for i, v := range values {
		placeholders[i] = "?"
		args[i] = v
	}
	return strings.Join(placeholders, ", "), args
}

// mapStoreError converts a raw store/domain error into a typed WRKQ error.
func mapStoreError(err error, selector string) error {
	if err == nil {
		return nil
	}
	var mismatch *domain.ETagMismatchError
	if errors.As(err, &mismatch) {
		return NewConflictError("task update conflict", map[string]any{"currentEtag": mismatch.Actual})
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "not found"):
		return NewNotFoundError(selector, "task")
	case strings.Contains(lower, "unique") || strings.Contains(lower, "constraint"):
		return NewConflictError(msg, nil)
	case strings.Contains(lower, "invalid") || strings.Contains(lower, "required") || strings.Contains(lower, "must "):
		return NewValidationError(msg, nil)
	default:
		return NewInternalError(err)
	}
}
