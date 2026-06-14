package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/domain"
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

	attr := a.attributionFor(p.Actor)
	result, err := a.store.Tasks.CreateWithAttribution(attr, store.CreateParams{
		Slug:           slug,
		Title:          p.Title,
		Description:    p.Description,
		Specification:  p.Specification,
		ProjectUUID:    projectUUID,
		State:          state,
		Priority:       priority,
		Kind:           p.Kind,
		ParentTaskUUID: parentTaskUUID,
		Labels:         labelsString(p.Labels),
		Meta:           metaString(p.Meta),
		Via:            "rpc",
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

	where := []string{}
	args := []any{}

	if strings.TrimSpace(p.Path) != "" {
		containerUUID, _, rerr := selectors.ResolveContainer(a.db, p.Path)
		if rerr != nil {
			return nil, NewNotFoundError(p.Path, "container")
		}
		where = append(where, "project_uuid = ?")
		args = append(args, containerUUID)
	}
	if len(p.State) > 0 {
		ph, vals := inClause(p.State)
		where = append(where, "state IN ("+ph+")")
		args = append(args, vals...)
	} else if !p.IncludeDeleted {
		where = append(where, "state != 'deleted'")
	}
	if len(p.Kind) > 0 {
		ph, vals := inClause(p.Kind)
		where = append(where, "kind IN ("+ph+")")
		args = append(args, vals...)
	}
	if strings.TrimSpace(p.Assignee) != "" {
		where = append(where, "assignee_principal_ref = ?")
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
		SortFields: []string{"created_at"},
		SQLFields:  []string{"created_at"},
		Descending: []bool{false},
		IDField:    "id",
		Limit:      limit,
	})
	if err != nil {
		return nil, NewValidationError("invalid cursor", map[string]any{"field": "cursor"})
	}

	query := "SELECT uuid, id, slug, title, project_uuid, state, priority, kind, description, specification, " +
		"labels, meta, etag, created_at, updated_at, completed_at, archived_at, deleted_at, " +
		"assignee_principal_ref, created_by_principal_ref, updated_by_principal_ref FROM tasks"
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
		next, cerr := cursor.BuildNextCursor([]string{"created_at"}, []any{anchor.createdAtRaw}, anchor.ID)
		if cerr == nil {
			result.NextCursor = next
		}
	}
	return result, nil
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
		"SELECT uuid, id, slug, title, project_uuid, state, priority, kind, description, specification, "+
			"labels, meta, etag, created_at, updated_at, completed_at, archived_at, deleted_at, "+
			"assignee_principal_ref, created_by_principal_ref, updated_by_principal_ref FROM tasks WHERE uuid = ?",
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
		completedAt, archivedAt, deletedAt                                          sql.NullString
		assignee, createdByPrincipal, updatedByPrincipal                            sql.NullString
	)
	if err := s.Scan(
		&uuid, &id, &slug, &title, &projectUUID, &state, &priority, &kind, &description, &specification,
		&labels, &meta, &etag, &createdAt, &updatedAt, &completedAt, &archivedAt, &deletedAt,
		&assignee, &createdByPrincipal, &updatedByPrincipal,
	); err != nil {
		return nil, "", err
	}
	task := &WrkqTask{
		UUID:                  uuid,
		ID:                    id,
		Slug:                  slug,
		Title:                 title,
		ProjectUUID:           projectUUID,
		State:                 state,
		Priority:              priority,
		Kind:                  kind,
		Description:           description,
		Specification:         specification,
		Labels:                parseLabels(labels.String),
		Meta:                  parseMeta(meta.String),
		ETag:                  etag,
		CreatedAt:             toRFC3339(createdAt),
		UpdatedAt:             toRFC3339(updatedAt),
		CompletedAt:           toRFC3339(completedAt.String),
		ArchivedAt:            toRFC3339(archivedAt.String),
		DeletedAt:             toRFC3339(deletedAt.String),
		AssigneePrincipalRef:  assignee.String,
		CreatedByPrincipalRef: createdByPrincipal.String,
		UpdatedByPrincipalRef: updatedByPrincipal.String,
		createdAtRaw:          createdAt,
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
