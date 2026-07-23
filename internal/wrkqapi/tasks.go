package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attach"
	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/causedby"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/nodeauth"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/webhooks"
)

const nsTaskCreate = "wrkq.task.create"

const taskPresenceTrimCharsSQL = "char(9)||char(10)||char(11)||char(12)||char(13)||' '"

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
	if strings.TrimSpace(p.Resolution) != "" {
		if rerr := domain.ValidateResolution(p.Resolution); rerr != nil {
			return nil, NewValidationError(rerr.Error(), map[string]any{"field": "resolution"})
		}
	}
	if strings.TrimSpace(p.ForceUUID) != "" {
		if uerr := domain.ValidateUUID(p.ForceUUID); uerr != nil {
			return nil, NewValidationError(uerr.Error(), map[string]any{"field": "forceUuid"})
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
	var requestedByProjectID *string
	if strings.TrimSpace(p.RequestedByProjectID) != "" {
		trimmed := strings.TrimSpace(p.RequestedByProjectID)
		requestedByProjectID = &trimmed
	}
	var assignedProjectID *string
	if strings.TrimSpace(p.AssignedProjectID) != "" {
		trimmed := strings.TrimSpace(p.AssignedProjectID)
		assignedProjectID = &trimmed
	}
	var resolution *string
	if strings.TrimSpace(p.Resolution) != "" {
		trimmed := strings.TrimSpace(p.Resolution)
		resolution = &trimmed
	}
	meta, merr := taskMetaString(p.Meta, p.MetaRaw)
	if merr != nil {
		return nil, merr
	}

	var causedByRefs []store.CausedByRef
	if len(p.CausedBy) > 0 {
		refs, cerr := causedby.ResolveTokens(a.db, p.CausedBy, "")
		if cerr != nil {
			return nil, NewValidationError(cerr.Error(), map[string]any{"field": "causedBy"})
		}
		causedByRefs = refs
	}

	attr, aerr := a.attributionFor(p.PrincipalRef)
	if aerr != nil {
		return nil, aerr
	}
	result, err := a.store.Tasks.CreateWithAttribution(attr, store.CreateParams{
		UUID:                 p.ForceUUID,
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
		RequestedByProjectID: requestedByProjectID,
		AssignedProjectID:    assignedProjectID,
		Resolution:           resolution,
		Labels:               labelsString(p.Labels),
		Meta:                 meta,
		DueAt:                p.DueAt,
		StartAt:              p.StartAt,
		RiskClass:            riskClass,
		CausedBy:             causedByRefs,
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
	attr, aerr := a.attributionFor("")
	if aerr != nil {
		return "", aerr
	}
	res, cerr := a.store.Containers.CreateWithAttribution(attr, store.ContainerCreateParams{
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
	if strings.TrimSpace(p.ClaimedBy) != "" {
		where = append(where, "t.claimed_by_principal_ref = ?")
		args = append(args, p.ClaimedBy)
	}
	if strings.TrimSpace(p.ClaimedNode) != "" {
		where = append(where, "t.claimed_node = ?")
		args = append(args, p.ClaimedNode)
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

	bodyColumns := "t.description, t.specification"
	if p.Summary {
		bodyColumns = "'' AS description, '' AS specification"
	}
	query := "SELECT t.uuid, t.id, t.slug, t.title, t.project_uuid, t.campaign_uuid, t.state, t.priority, t.kind, " + bodyColumns + ", t.outcome, " +
		"t.labels, t.meta, t.etag, t.start_at, t.due_at, t.created_at, t.updated_at, t.completed_at, t.archived_at, t.deleted_at, t.acknowledged_at, " +
		"t.assignee_principal_ref, t.claimed_by_principal_ref, t.claimed_scope_ref, t.claimed_node, t.claimed_at, t.claim_generation, " +
		"t.created_by_principal_ref, t.updated_by_principal_ref, COALESCE(t.risk_class,''), " +
		"COALESCE(cp.path || '/' || t.slug, t.slug), " +
		"CAST(LENGTH(TRIM(COALESCE(t.description,''), " + taskPresenceTrimCharsSQL + ")) > 0 AS INTEGER) AS has_description, " +
		"CAST(LENGTH(TRIM(COALESCE(t.specification,''), " + taskPresenceTrimCharsSQL + ")) > 0 AS INTEGER) AS has_specification " +
		"FROM tasks t LEFT JOIN v_container_paths cp ON cp.uuid = t.project_uuid"
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
	// Hydrate caused_by lineage for each task (after the rows cursor is closed).
	for i := range items {
		causedBy, cerr := store.CausedByIDs(a.db, items[i].UUID)
		if cerr != nil {
			return nil, NewInternalError(cerr)
		}
		if len(causedBy) > 0 {
			items[i].CausedBy = causedBy
		}
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

func taskMetaString(meta map[string]any, metaRaw string) (*string, error) {
	metaRaw = strings.TrimSpace(metaRaw)
	if metaRaw == "" {
		return metaString(meta), nil
	}
	if metaRaw == "null" {
		return nil, nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(metaRaw), &parsed); err != nil {
		return nil, NewValidationError("invalid meta JSON: "+err.Error(), map[string]any{"field": "metaRaw"})
	}
	if parsed == nil {
		return nil, nil
	}
	return &metaRaw, nil
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

	fields, ferr := a.patchFields(p.Patch)
	if ferr != nil {
		return nil, ferr
	}
	// Reject self-causation: a task cannot be in its own caused_by set.
	if cbv, ok := fields["caused_by"]; ok {
		if cu, ok := cbv.(store.CausedByUpdate); ok {
			for _, r := range cu.Refs {
				if r.TaskUUID == uuid {
					return nil, NewValidationError("a task cannot be caused by itself", map[string]any{"field": "causedBy"})
				}
			}
		}
	}
	if len(fields) == 0 {
		// Nothing to change; return the current DTO.
		return a.loadTask(uuid)
	}

	attr, aerr := a.attributionFor(p.Actor)
	if aerr != nil {
		return nil, aerr
	}
	var claimPrecondition func(*sql.Tx) error
	if state, ok := fields["state"].(string); ok && state == string(domain.StateCompleted) {
		claimPrecondition = func(tx *sql.Tx) error {
			row, qerr := loadTaskClaimRow(tx, uuid)
			if qerr != nil {
				return qerr
			}
			if !row.claimedBy.Valid {
				return nil
			}
			nodeID, nodeOK := nodeauth.FromContext(ctx)
			if !nodeOK {
				return NewNodeIdentityError()
			}
			if !claimMatches(row, attr.PrincipalRef, p.ClaimScope, nodeID, p.ClaimGeneration, p.ClaimToken) {
				return NewClaimSupersededError(claimHolderData(row))
			}
			return nil
		}
	}
	_, err = a.store.Tasks.UpdateFieldsWithViaAttributionAndPrecondition(attr, uuid, fields, currentEtag, "rpc", claimPrecondition)
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

// TaskMove moves a root task subtree to an existing target container.
func (a *API) TaskMove(ctx context.Context, p TaskMoveParams) (*WrkqTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uuid, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.TargetPath) == "" {
		return nil, NewValidationError("targetPath is required", map[string]any{"field": "targetPath"})
	}
	var currentEtag int64
	if scanErr := a.db.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", uuid).Scan(&currentEtag); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return nil, NewNotFoundError(p.Task, "task")
		}
		return nil, NewInternalError(scanErr)
	}
	if p.ExpectEtag != nil && *p.ExpectEtag != currentEtag {
		return nil, NewConflictError("task move conflict", map[string]any{
			"expectEtag":  *p.ExpectEtag,
			"currentEtag": currentEtag,
		})
	}

	ifMatch := int64(0)
	if p.ExpectEtag != nil {
		ifMatch = currentEtag
	}
	attr, aerr := a.attributionFor(p.Actor)
	if aerr != nil {
		return nil, aerr
	}
	if targetUUID, _, rerr := selectors.ResolveContainer(a.db, p.TargetPath); rerr == nil {
		if _, merr := a.store.Tasks.MoveWithViaAttribution(attr, uuid, targetUUID, ifMatch, "rpc"); merr != nil {
			return nil, mapStoreError(merr, p.Task)
		}
		return a.loadTask(uuid)
	}

	targetProjectUUID, targetSlug, rerr := a.resolveTaskMoveTarget(uuid, p.TargetPath)
	if rerr != nil {
		return nil, rerr
	}
	var existingTaskUUID string
	existingErr := a.db.QueryRow(
		`SELECT uuid FROM tasks WHERE slug = ? AND project_uuid = ?`,
		targetSlug, targetProjectUUID,
	).Scan(&existingTaskUUID)
	if existingErr != nil && existingErr != sql.ErrNoRows {
		return nil, NewInternalError(existingErr)
	}
	if existingErr == nil && existingTaskUUID != uuid {
		if !p.OverwriteTask {
			return nil, NewConflictError(
				"destination task already exists: "+p.TargetPath+" (use --overwrite-task to replace)",
				map[string]any{"targetPath": p.TargetPath},
			)
		}
		if _, perr := a.store.Tasks.PurgeWithAttribution(attr, existingTaskUUID, 0); perr != nil {
			return nil, mapStoreError(perr, p.TargetPath)
		}
	}

	fields := map[string]any{
		"slug":         targetSlug,
		"project_uuid": targetProjectUUID,
	}
	if _, uerr := a.store.Tasks.UpdateFieldsWithViaAttribution(attr, uuid, fields, ifMatch, "rpc"); uerr != nil {
		return nil, mapStoreError(uerr, p.Task)
	}
	return a.loadTask(uuid)
}

func (a *API) resolveTaskMoveTarget(taskUUID, targetPath string) (string, string, error) {
	parentUUID, normalizedSlug, _, err := selectors.ResolveParentContainer(a.db, targetPath)
	if err != nil {
		dstSegments := paths.SplitPath(targetPath)
		if len(dstSegments) != 1 {
			return "", "", NewNotFoundError(targetPath, "container")
		}
		var currentProjectUUID string
		if qerr := a.db.QueryRow("SELECT project_uuid FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentProjectUUID); qerr != nil {
			if qerr == sql.ErrNoRows {
				return "", "", NewNotFoundError(taskUUID, "task")
			}
			return "", "", NewInternalError(qerr)
		}
		normalized, nerr := paths.NormalizeSlug(dstSegments[0])
		if nerr != nil {
			return "", "", NewValidationError("invalid destination slug "+strconv.Quote(dstSegments[0])+": "+nerr.Error(), map[string]any{"field": "targetPath"})
		}
		return currentProjectUUID, normalized, nil
	}
	if parentUUID != nil {
		return *parentUUID, normalizedSlug, nil
	}
	var currentProjectUUID string
	if qerr := a.db.QueryRow("SELECT project_uuid FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentProjectUUID); qerr != nil {
		if qerr == sql.ErrNoRows {
			return "", "", NewNotFoundError(taskUUID, "task")
		}
		return "", "", NewInternalError(qerr)
	}
	return currentProjectUUID, normalizedSlug, nil
}

// TaskAcknowledge records a terminal-state receipt (acknowledged_at). It mirrors
// internal/rpccli/ack.go: state must be completed|cancelled unless force is set.
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
	attr, aerr := a.attributionFor(p.Actor)
	if aerr != nil {
		return nil, aerr
	}
	if _, uerr := a.store.Tasks.UpdateFieldsWithViaAttribution(attr, uuid, map[string]any{"acknowledged_at": now}, 0, "rpc"); uerr != nil {
		return nil, mapStoreError(uerr, p.Task)
	}
	return a.loadTask(uuid)
}

// TaskDelete disposes of a task per the caller-owned-confirmation invariant
// (architecture/records/invariants/wrkq.mutation.caller-owned-confirmation.yaml):
// the disposition is the EXPLICIT, caller-supplied mode — the server never
// prompts, inspects a TTY, reads stdin, or infers confirmation from transport.
//
//   - mode "" (legacy, PRESERVED): reversible delete — state='deleted' +
//     deleted_at=now, cascading to subtasks. Never sets archived_at, never
//     purges. Re-deleting an already-deleted task is a no-op.
//   - mode "archive": soft-archive — state='archived' + archived_at (legacy
//     `wrkq rm` default). Re-archiving is the store no-op.
//   - mode "purge": hard-delete the task + clean attachment files and the task
//     attachment dir (legacy `wrkq rm --purge`). Irreversible; never a default.
//   - any other mode: WRKQ_VALIDATION.
func (a *API) TaskDelete(ctx context.Context, p TaskDeleteParams) (*WrkqTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mode := strings.TrimSpace(p.Mode)
	switch mode {
	case "", "archive", "purge":
	default:
		return nil, NewValidationError("invalid mode: "+p.Mode+" (expected archive or purge)", map[string]any{"field": "mode"})
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

	attr, aerr := a.attributionFor(p.Actor)
	if aerr != nil {
		return nil, aerr
	}

	switch mode {
	case "archive":
		if _, aerr := a.store.Tasks.ArchiveWithViaAttribution(attr, uuid, 0, "rpc"); aerr != nil {
			return nil, mapStoreError(aerr, p.Task)
		}
		return a.loadTask(uuid)
	case "purge":
		// Snapshot the task DTO BEFORE the row is gone so the method still returns a
		// valid task; the mirror derives its purge stats (count + bytes) from a
		// pre-purge attachment.list, not from this return value.
		snapshot, serr := a.loadTask(uuid)
		if serr != nil {
			return nil, serr
		}
		// Capture attachment paths BEFORE purge for post-commit file cleanup.
		attachments, gerr := a.store.Tasks.GetAttachments(uuid)
		if gerr != nil {
			return nil, NewInternalError(gerr)
		}
		if _, perr := a.store.Tasks.PurgeWithAttribution(attr, uuid, 0); perr != nil {
			return nil, mapStoreError(perr, p.Task)
		}
		// Delete attachment files + the task dir AFTER the DB purge commits. File
		// cleanup is best-effort (the durable DB delete already committed); skipped
		// entirely when no attach dir is configured.
		if strings.TrimSpace(a.attachDir) != "" {
			for _, at := range attachments {
				if at.RelativePath == "" {
					continue
				}
				_ = attach.DeleteFile(a.attachDir, at.RelativePath)
			}
			_ = attach.DeleteTaskDir(a.attachDir, uuid)
		}
		return snapshot, nil
	default:
		// mode == "" — legacy reversible delete, PRESERVED.
		if state == string(domain.StateDeleted) {
			return a.loadTask(uuid)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		// Setting state=deleted triggers cascade-delete of subtasks in the store.
		if _, uerr := a.store.Tasks.UpdateFieldsWithViaAttribution(attr, uuid, map[string]any{
			"state":      string(domain.StateDeleted),
			"deleted_at": now,
		}, 0, "rpc"); uerr != nil {
			return nil, mapStoreError(uerr, p.Task)
		}
		return a.loadTask(uuid)
	}
}

// TaskRestore reverses delete/archive: current state must be archived or deleted,
// the target defaults to open (archived/deleted targets rejected), archived_at /
// deleted_at / deleted_by are cleared, and subtasks are cascade-restored. Mirrors
// internal/rpccli/restore.go.
func (a *API) TaskRestore(ctx context.Context, p TaskRestoreParams) (*WrkqTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Legacy precedence: state/priority/labels/assignee are validated BEFORE the
	// task ref is resolved, so a bad flag errors even on an unresolvable ref.
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

	// Validate priority (legacy: only when non-zero).
	if p.Priority != 0 {
		if verr := domain.ValidatePriority(p.Priority); verr != nil {
			return nil, NewValidationError(verr.Error(), map[string]any{"field": "priority"})
		}
	}

	// Validate labels JSON (legacy: only when non-empty).
	if strings.TrimSpace(p.Labels) != "" {
		var labels []string
		if jerr := json.Unmarshal([]byte(p.Labels), &labels); jerr != nil {
			return nil, NewValidationError("invalid labels JSON: "+jerr.Error(), map[string]any{"field": "labels"})
		}
	}

	// Resolve assignee (legacy attribution.NormalizeCompat).
	var assigneePrincipalRef *string
	if strings.TrimSpace(p.Assignee) != "" {
		principalRef, aerr := attribution.NormalizeCompat(p.Assignee)
		if aerr != nil {
			return nil, NewValidationError("failed to resolve assignee: "+aerr.Error(), map[string]any{"field": "assignee"})
		}
		assigneePrincipalRef = &principalRef
	}

	uuid, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}

	var currentState string
	var currentEtag int64
	if scanErr := a.db.QueryRow("SELECT state, etag FROM tasks WHERE uuid = ?", uuid).Scan(&currentState, &currentEtag); scanErr != nil {
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

	// --if-match precondition (legacy: checked AFTER the state check, BEFORE --to).
	// Mirrors legacy "etag mismatch: expected %d, got %d"; surfaced as WRKQ_CONFLICT.
	if p.IfMatch != 0 && p.IfMatch != currentEtag {
		return nil, NewConflictError(
			"etag mismatch: expected "+strconv.FormatInt(p.IfMatch, 10)+", got "+strconv.FormatInt(currentEtag, 10),
			map[string]any{"expectEtag": p.IfMatch, "currentEtag": currentEtag},
		)
	}

	// --to (move-on-restore): resolve the destination parent + final slug and
	// check for a slug conflict at the destination, exactly as legacy restore.go.
	var newProjectUUID *string
	var newSlug *string
	if strings.TrimSpace(p.ToPath) != "" {
		parentUUID, slug, _, derr := selectors.ResolveParentContainer(a.db, p.ToPath)
		if derr != nil {
			return nil, NewValidationError("failed to resolve destination: "+derr.Error(), map[string]any{"field": "toPath"})
		}
		newProjectUUID = parentUUID
		newSlug = &slug

		var existingUUID string
		cerr := a.db.QueryRow(
			"SELECT uuid FROM tasks WHERE project_uuid = ? AND slug = ? AND uuid != ?",
			*parentUUID, slug, uuid,
		).Scan(&existingUUID)
		if cerr == nil {
			return nil, NewConflictError(
				"slug conflict: task with slug '"+slug+"' already exists at destination",
				map[string]any{"field": "slug", "slug": slug},
			)
		} else if cerr != sql.ErrNoRows {
			return nil, NewInternalError(cerr)
		}
	}

	attr, aerr := a.attributionFor(p.Actor)
	if aerr != nil {
		return nil, aerr
	}
	opts := restoreOptions{
		targetState:          targetState,
		newProjectUUID:       newProjectUUID,
		newSlug:              newSlug,
		newTitle:             p.Title,
		newDescription:       p.Description,
		newPriority:          p.Priority,
		newLabels:            p.Labels,
		assigneePrincipalRef: assigneePrincipalRef,
		comment:              p.Comment,
	}
	webhookCtx, rerr := a.restoreTaskTx(uuid, opts, attr)
	if rerr != nil {
		return nil, rerr
	}
	// Dispatch the restore webhook for the ROOT task (legacy runRestore parity).
	webhooks.DispatchTaskEvent(a.db, uuid, webhookCtx)
	// Subtasks cascade-restore to the target state only (no field updates / move /
	// comment propagate to children — legacy cascadeRestoreSubtasks passes a bare
	// options struct); each restored subtask dispatches its own webhook.
	if rerr := a.cascadeRestoreSubtasks(uuid, targetState, attr); rerr != nil {
		return nil, rerr
	}
	return a.loadTask(uuid)
}

// restoreOptions carries the per-task restore mutation (target state + the
// optional move / field updates / comment). Mirrors internal/cli restoreTaskOptions.
type restoreOptions struct {
	targetState          string
	newProjectUUID       *string
	newSlug              *string
	newTitle             string
	newDescription       string
	newPriority          int
	newLabels            string
	assigneePrincipalRef *string
	comment              string
}

// restoreTaskTx clears the archived/deleted markers and sets the target state for
// a single task within a transaction, applying any move / field updates / comment,
// and logging a task.restored event. Mirrors internal/cli restoreTaskWithOptions.
// The legacy UPDATE intentionally does NOT bump etag, so this method preserves it
// for byte-parity of the durable snapshot. It returns the webhooks.EventContext
// the caller dispatches after commit (matching legacy: the root task AND every
// cascade-restored subtask dispatch the restore webhook). Via is "rpc" — the
// RPC-mutation convention threaded through the store (e.g.
// UpdateFieldsWithViaAttribution(..., "rpc")) — distinct from the legacy "cli".
func (a *API) restoreTaskTx(taskUUID string, opts restoreOptions, attr attribution.Attribution) (webhooks.EventContext, error) {
	tx, err := a.db.Begin()
	if err != nil {
		return webhooks.EventContext{}, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	var currentState string
	if scanErr := tx.QueryRow("SELECT state FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentState); scanErr != nil {
		return webhooks.EventContext{}, NewInternalError(scanErr)
	}

	query := `UPDATE tasks SET state = ?, archived_at = NULL, deleted_at = NULL,
		deleted_by_principal_ref = NULL, deleted_by_scope_ref = NULL,
		updated_by_principal_ref = ?, updated_by_scope_ref = ?`
	args := []any{opts.targetState, attr.PrincipalRef, scopeBind(attr)}

	// fields mirrors legacy restoreTaskWithOptions' webhook `fields` map exactly —
	// it drives Changed (sorted keys) + Changes (from→to). Note description is
	// reported as {"length": N}, not the raw body (legacy parity).
	fields := map[string]any{
		"state":       opts.targetState,
		"archived_at": nil,
		"deleted_at":  nil,
	}

	if opts.newProjectUUID != nil {
		query += `, project_uuid = ?`
		args = append(args, *opts.newProjectUUID)
		fields["project_uuid"] = *opts.newProjectUUID
	}
	if opts.newSlug != nil {
		query += `, slug = ?`
		args = append(args, *opts.newSlug)
		fields["slug"] = *opts.newSlug
	}
	if opts.newTitle != "" {
		query += `, title = ?`
		args = append(args, opts.newTitle)
		fields["title"] = opts.newTitle
	}
	if opts.newDescription != "" {
		query += `, description = ?`
		args = append(args, opts.newDescription)
		fields["description"] = map[string]any{"length": len(opts.newDescription)}
	}
	if opts.newPriority != 0 {
		query += `, priority = ?`
		args = append(args, opts.newPriority)
		fields["priority"] = opts.newPriority
	}
	if opts.newLabels != "" {
		query += `, labels = ?`
		args = append(args, opts.newLabels)
		fields["labels"] = opts.newLabels
	}
	if opts.assigneePrincipalRef != nil {
		query += `, assignee_principal_ref = ?`
		args = append(args, *opts.assigneePrincipalRef)
		fields["assignee_principal_ref"] = *opts.assigneePrincipalRef
	}
	query += ` WHERE uuid = ?`
	args = append(args, taskUUID)

	if _, eerr := tx.Exec(query, args...); eerr != nil {
		return webhooks.EventContext{}, NewInternalError(eerr)
	}

	// Event payload mirrors legacy: action + target_state, plus moved_to on a move.
	payloadMap := map[string]any{"action": "restored", "target_state": opts.targetState}
	if opts.newProjectUUID != nil {
		payloadMap["moved_to"] = *opts.newProjectUUID
	}
	if eerr := store.StampTaskCampaignContext(tx, taskUUID, payloadMap); eerr != nil {
		return webhooks.EventContext{}, NewInternalError(eerr)
	}
	payloadJSON, _ := json.Marshal(payloadMap)
	payload := string(payloadJSON)
	eventMeta, eerr := events.NewWriter(a.db.DB).LogEventReturning(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "task",
		ResourceUUID: &taskUUID,
		EventType:    "task.restored",
		Payload:      &payload,
	})
	if eerr != nil {
		return webhooks.EventContext{}, NewInternalError(eerr)
	}

	var commentResult *store.CommentCreateResult
	// --comment: append through the canonical store path so its comment.created
	// event is part of this same restore transaction.
	if opts.comment != "" {
		commentResult, eerr = a.store.Comments.CreateTxWithAttribution(
			tx,
			events.NewWriter(a.db.DB),
			attr,
			store.CommentCreateParams{TaskUUID: taskUUID, Body: opts.comment},
		)
		if eerr != nil {
			return webhooks.EventContext{}, NewInternalError(eerr)
		}
	}

	if cerr := tx.Commit(); cerr != nil {
		return webhooks.EventContext{}, NewInternalError(cerr)
	}
	if commentResult != nil {
		webhooks.DispatchCommentCreated(a.db, taskUUID, commentResult.EventMeta, attr.PrincipalRef, "rpc")
	}

	// EventContext mirrors legacy restoreTaskWithOptions: an `updated` event with
	// the archived/deleted→target transition, the per-field Changed/Changes derived
	// from the same `fields` map, and Via="rpc" (this path's mutation convention).
	return webhooks.EventContext{
		Metadata:     eventMeta,
		Event:        "updated",
		PrincipalRef: attr.PrincipalRef,
		Via:          "rpc",
		Transition:   &webhooks.Transition{From: &currentState, To: &opts.targetState},
		Changed:      webhookSortedKeys(fields),
		Changes:      webhookMapChanges(fields, map[string]any{"state": currentState}),
	}, nil
}

// cascadeRestoreSubtasks restores archived/deleted resident subtasks of a
// parent, dispatching the restore webhook for EACH restored subtask. Cross-
// project parent edges are backlinks, not containment, so external children are
// not restored through the parent.
func (a *API) cascadeRestoreSubtasks(parentTaskUUID, targetState string, attr attribution.Attribution) error {
	rows, err := a.db.Query(
		`SELECT c.uuid
		   FROM tasks c
		   JOIN tasks p ON p.uuid = c.parent_task_uuid
		  WHERE c.parent_task_uuid = ?
		    AND c.project_uuid = p.project_uuid
		    AND c.state IN ('archived', 'deleted')`,
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
		webhookCtx, rerr := a.restoreTaskTx(subtaskUUID, restoreOptions{targetState: targetState}, attr)
		if rerr != nil {
			return rerr
		}
		webhooks.DispatchTaskEvent(a.db, subtaskUUID, webhookCtx)
		if rerr := a.cascadeRestoreSubtasks(subtaskUUID, targetState, attr); rerr != nil {
			return rerr
		}
	}
	return nil
}

// webhookSortedKeys returns the field keys in sorted order (mirrors legacy
// sortedMapKeys, driving the webhook Changed slice deterministically).
func webhookSortedKeys(fields map[string]any) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// webhookMapChanges builds the webhook Changes map (from oldValues, to fields),
// mirroring legacy mapChanges.
func webhookMapChanges(fields map[string]any, oldValues map[string]any) map[string]webhooks.Change {
	changes := make(map[string]webhooks.Change, len(fields))
	for _, key := range webhookSortedKeys(fields) {
		changes[key] = webhooks.Change{From: oldValues[key], To: fields[key]}
	}
	return changes
}

// patchFields converts a TaskPatch into the store field map (DB column names).
func (a *API) patchFields(patch TaskPatch) (map[string]any, error) {
	fields := map[string]any{}
	if patch.Slug != nil {
		normalized, err := paths.NormalizeSlug(*patch.Slug)
		if err != nil {
			return nil, NewValidationError("invalid slug: "+err.Error(), map[string]any{"field": "slug"})
		}
		slug, err := paths.NewSlug(normalized)
		if err != nil {
			return nil, NewValidationError("invalid slug: "+err.Error(), map[string]any{"field": "slug"})
		}
		fields["slug"] = string(slug)
	}
	if patch.Title != nil {
		fields["title"] = *patch.Title
	}
	if patch.Description != nil {
		fields["description"] = *patch.Description
	}
	if patch.Specification != nil {
		fields["specification"] = *patch.Specification
	}
	if patch.Outcome != nil {
		if strings.TrimSpace(*patch.Outcome) == "" {
			fields["outcome"] = nil
		} else {
			fields["outcome"] = *patch.Outcome
		}
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
	if patch.ParentTask != nil {
		parentRef := strings.TrimSpace(*patch.ParentTask)
		if parentRef == "" {
			fields["parent_task_uuid"] = nil
			if patch.Kind == nil {
				fields["kind"] = "task"
			}
		} else {
			parentUUID, _, err := selectors.ResolveTask(a.db, parentRef)
			if err != nil {
				return nil, NewNotFoundError(parentRef, "task")
			}
			fields["parent_task_uuid"] = parentUUID
			if patch.Kind == nil {
				fields["kind"] = "subtask"
			}
		}
	}
	if patch.Labels != nil {
		fields["labels"] = labelsString(*patch.Labels)
	}
	if patch.MetaRaw != nil {
		metaRaw := strings.TrimSpace(*patch.MetaRaw)
		if metaRaw == "" || metaRaw == "null" {
			fields["meta"] = nil
		} else {
			var meta map[string]any
			if err := json.Unmarshal([]byte(metaRaw), &meta); err != nil {
				return nil, NewValidationError("invalid meta JSON: "+err.Error(), map[string]any{"field": "metaRaw"})
			}
			if meta == nil {
				fields["meta"] = nil
			} else {
				fields["meta"] = metaRaw
			}
		}
	} else if patch.Meta != nil {
		fields["meta"] = metaString(*patch.Meta)
	}
	if patch.AssigneePrincipalRef != nil {
		assignee := strings.TrimSpace(*patch.AssigneePrincipalRef)
		if assignee == "" {
			fields["assignee_principal_ref"] = nil
		} else {
			principalRef, err := attribution.NormalizeCompat(assignee)
			if err != nil {
				return nil, NewValidationError(err.Error(), map[string]any{"field": "assigneePrincipalRef"})
			}
			fields["assignee_principal_ref"] = principalRef
		}
	}
	if patch.RequestedByProjectID != nil {
		fields["requested_by_project_id"] = *patch.RequestedByProjectID
	}
	if patch.AssignedProjectID != nil {
		fields["assigned_project_id"] = *patch.AssignedProjectID
	}
	if patch.Resolution != nil {
		if err := domain.ValidateResolution(*patch.Resolution); err != nil {
			return nil, NewValidationError(err.Error(), map[string]any{"field": "resolution"})
		}
		fields["resolution"] = *patch.Resolution
	}
	if patch.DueAt != nil {
		fields["due_at"] = *patch.DueAt
	}
	if patch.StartAt != nil {
		fields["start_at"] = *patch.StartAt
	}
	if patch.Campaign != nil {
		campaign := strings.TrimSpace(*patch.Campaign)
		if campaign == "" {
			fields["campaign_uuid"] = nil
		} else {
			uuid, _, err := selectors.ResolveContainer(a.db, campaign)
			if err != nil {
				return nil, NewNotFoundError(campaign, "campaign")
			}
			fields["campaign_uuid"] = uuid
		}
	}
	if patch.CausedBy != nil {
		refs, cerr := causedby.ResolveTokens(a.db, *patch.CausedBy, "")
		if cerr != nil {
			return nil, NewValidationError(cerr.Error(), map[string]any{"field": "causedBy"})
		}
		fields["caused_by"] = store.CausedByUpdate{Refs: refs}
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
		"SELECT t.uuid, t.id, t.slug, t.title, t.project_uuid, t.campaign_uuid, t.state, t.priority, t.kind, t.description, t.specification, t.outcome, "+
			"t.labels, t.meta, t.etag, t.start_at, t.due_at, t.created_at, t.updated_at, t.completed_at, t.archived_at, t.deleted_at, t.acknowledged_at, "+
			"t.assignee_principal_ref, t.claimed_by_principal_ref, t.claimed_scope_ref, t.claimed_node, t.claimed_at, t.claim_generation, "+
			"t.created_by_principal_ref, t.updated_by_principal_ref, COALESCE(t.risk_class,''), "+
			"COALESCE(cp.path || '/' || t.slug, t.slug), "+
			"CAST(LENGTH(TRIM(COALESCE(t.description,''), "+taskPresenceTrimCharsSQL+")) > 0 AS INTEGER) AS has_description, "+
			"CAST(LENGTH(TRIM(COALESCE(t.specification,''), "+taskPresenceTrimCharsSQL+")) > 0 AS INTEGER) AS has_specification "+
			"FROM tasks t LEFT JOIN v_container_paths cp ON cp.uuid = t.project_uuid WHERE t.uuid = ?",
		uuid,
	)
	task, _, err := scanTaskRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, NewNotFoundError(uuid, "task")
		}
		return nil, NewInternalError(err)
	}
	causedBy, cerr := store.CausedByIDs(a.db, task.UUID)
	if cerr != nil {
		return nil, NewInternalError(cerr)
	}
	if len(causedBy) > 0 {
		task.CausedBy = causedBy
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
		campaignUUID                                                                sql.NullString
		outcome                                                                     sql.NullString
		labels, meta                                                                sql.NullString
		priority                                                                    int
		etag                                                                        int64
		createdAt, updatedAt                                                        string
		startAt, dueAt, completedAt, archivedAt, deletedAt, acknowledgedAt          sql.NullString
		assignee, claimedBy, claimedScope, claimedNode, claimedAt                   sql.NullString
		createdByPrincipal, updatedByPrincipal                                      sql.NullString
		claimGeneration                                                             int64
		riskClass, path                                                             string
		hasDescription, hasSpecification                                            int
	)
	if err := s.Scan(
		&uuid, &id, &slug, &title, &projectUUID, &campaignUUID, &state, &priority, &kind, &description, &specification, &outcome,
		&labels, &meta, &etag, &startAt, &dueAt, &createdAt, &updatedAt, &completedAt, &archivedAt, &deletedAt, &acknowledgedAt,
		&assignee, &claimedBy, &claimedScope, &claimedNode, &claimedAt, &claimGeneration,
		&createdByPrincipal, &updatedByPrincipal, &riskClass, &path, &hasDescription, &hasSpecification,
	); err != nil {
		return nil, "", err
	}
	task := &WrkqTask{
		UUID:                  uuid,
		ID:                    id,
		Slug:                  slug,
		Title:                 title,
		ProjectUUID:           projectUUID,
		CampaignUUID:          campaignUUID.String,
		Path:                  path,
		State:                 state,
		Priority:              priority,
		Kind:                  kind,
		RiskClass:             riskClass,
		Description:           description,
		Specification:         specification,
		Outcome:               nullStringPtr(outcome),
		HasDescription:        hasDescription != 0,
		HasSpecification:      hasSpecification != 0,
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
		ClaimedBy:             claimedBy.String,
		ClaimedScope:          claimedScope.String,
		ClaimedNode:           claimedNode.String,
		ClaimedAt:             toRFC3339(claimedAt.String),
		ClaimGeneration:       claimGeneration,
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
	var domainErr Error
	if errors.As(err, &domainErr) {
		return domainErr
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
	case strings.Contains(lower, "invalid") || strings.Contains(lower, "required") || strings.Contains(lower, "must ") || strings.Contains(lower, "cannot "):
		return NewValidationError(msg, nil)
	default:
		return NewInternalError(err)
	}
}
