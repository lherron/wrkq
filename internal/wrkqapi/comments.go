package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/webhooks"
)

const nsCommentAdd = "wrkq.comment.add"

// CommentAdd appends a comment to exactly one task or container and returns the
// WrkqComment DTO.
// Idempotency is honored when an idempotencyKey is supplied (§8.2).
func (a *API) CommentAdd(ctx context.Context, p CommentAddParams) (*WrkqComment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Body) == "" {
		return nil, NewValidationError("comment body is required", map[string]any{
			"field":    "body",
			"expected": "non-empty string",
		})
	}
	if err := domain.ValidateCommentKind(p.Kind); err != nil {
		return nil, NewValidationError(err.Error(), map[string]any{"field": "kind"})
	}
	parent, err := a.resolveCommentParent(p.Task, p.Container)
	if err != nil {
		return nil, err
	}

	var requestHash string
	if p.IdempotencyKey != "" {
		hp := p
		hp.IdempotencyKey = ""
		requestHash = canonicalRequestHash(hp)
		if raw, ok, rerr := a.idempotentReplay(nsCommentAdd, p.IdempotencyKey, requestHash); rerr != nil {
			return nil, rerr
		} else if ok {
			var dto WrkqComment
			if uerr := json.Unmarshal(raw, &dto); uerr != nil {
				return nil, NewInternalError(uerr)
			}
			return &dto, nil
		}
	}

	attr, aerr := a.attributionFor(p.Actor)
	if aerr != nil {
		return nil, aerr
	}

	var metaStr *string
	if p.Meta != nil {
		metaStr = metaString(p.Meta)
	}
	result, err := a.store.Comments.CreateWithAttribution(attr, store.CommentCreateParams{
		TaskUUID:      parent.taskUUID,
		ContainerUUID: parent.containerUUID,
		Kind:          p.Kind,
		Body:          p.Body,
		Meta:          metaStr,
	})
	if err != nil {
		return nil, mapStoreError(err, parent.selector)
	}
	if parent.taskUUID != "" {
		webhooks.DispatchCommentCreated(a.db, parent.taskUUID, result.EventMeta, attr.PrincipalRef, "rpc")
	}

	dto, err := a.loadComment(result.UUID, parent.taskID, parent.containerID)
	if err != nil {
		return nil, err
	}
	if p.IdempotencyKey != "" {
		if serr := a.idempotentStore(nsCommentAdd, p.IdempotencyKey, requestHash, dto); serr != nil {
			return nil, serr
		}
	}
	return dto, nil
}

// CommentList returns a paginated list of WrkqComment DTOs for one task or
// container.
func (a *API) CommentList(ctx context.Context, p CommentListParams) (*WrkqCommentListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parent, err := a.resolveCommentParent(p.Task, p.Container)
	if err != nil {
		return nil, err
	}

	parentColumn, parentUUID := "task_uuid", parent.taskUUID
	if parent.containerUUID != "" {
		parentColumn, parentUUID = "container_uuid", parent.containerUUID
	}
	where := []string{parentColumn + " = ?"}
	args := []any{parentUUID}
	if !p.IncludeDeleted {
		where = append(where, "deleted_at IS NULL")
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
	if page.WhereClause != "" {
		where = append(where, page.WhereClause)
		args = append(args, page.Params...)
	}

	query := "SELECT uuid, id, task_uuid, container_uuid, kind, body, meta, etag, created_at, updated_at, deleted_at, created_by_principal_ref FROM comments"
	query += " WHERE " + strings.Join(where, " AND ")
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

	items := []WrkqComment{}
	for rows.Next() {
		c, _, scanErr := scanCommentRow(rows, parent.taskID, parent.containerID)
		if scanErr != nil {
			return nil, NewInternalError(scanErr)
		}
		items = append(items, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(err)
	}

	result := &WrkqCommentListResult{Items: items}
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

func (a *API) taskFriendlyID(taskUUID string) (string, error) {
	var taskID string
	err := a.db.QueryRow("SELECT id FROM tasks WHERE uuid = ?", taskUUID).Scan(&taskID)
	if err == sql.ErrNoRows {
		return "", NewNotFoundError(taskUUID, "task")
	}
	if err != nil {
		return "", NewInternalError(err)
	}
	return taskID, nil
}

type resolvedCommentParent struct {
	selector      string
	taskUUID      string
	taskID        string
	containerUUID string
	containerID   string
}

func (a *API) resolveCommentParent(taskSelector, containerSelector string) (resolvedCommentParent, error) {
	if (strings.TrimSpace(taskSelector) == "") == (strings.TrimSpace(containerSelector) == "") {
		return resolvedCommentParent{}, NewValidationError(
			"exactly one of task or container is required",
			map[string]any{"fields": []string{"task", "container"}},
		)
	}
	if taskSelector != "" {
		taskUUID, err := a.resolveTaskUUID(taskSelector)
		if err != nil {
			return resolvedCommentParent{}, err
		}
		taskID, err := a.taskFriendlyID(taskUUID)
		if err != nil {
			return resolvedCommentParent{}, err
		}
		return resolvedCommentParent{selector: taskSelector, taskUUID: taskUUID, taskID: taskID}, nil
	}

	containerUUID, _, err := selectors.ResolveContainer(a.db, containerSelector)
	if err != nil {
		return resolvedCommentParent{}, NewNotFoundError(containerSelector, "container")
	}
	var containerID string
	if err := a.db.QueryRow("SELECT id FROM containers WHERE uuid = ?", containerUUID).Scan(&containerID); err != nil {
		return resolvedCommentParent{}, NewInternalError(err)
	}
	return resolvedCommentParent{
		selector: containerSelector, containerUUID: containerUUID, containerID: containerID,
	}, nil
}

// CommentShow returns the WrkqComment DTO for a comment id or uuid.
func (a *API) CommentShow(ctx context.Context, p CommentShowParams) (*WrkqComment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ID) == "" {
		return nil, NewValidationError("comment id is required", map[string]any{"field": "id"})
	}
	commentUUID, taskUUID, err := a.resolveComment(p.ID)
	if err != nil {
		return nil, err
	}
	taskID, err := a.taskFriendlyID(taskUUID)
	if err != nil {
		return nil, err
	}
	return a.loadComment(commentUUID, taskID, "")
}

// CommentDelete disposes of a comment per the caller-owned-confirmation
// invariant: the disposition is the EXPLICIT, caller-supplied mode (absent/"soft"
// → reversible soft-delete; "purge" → hard-delete). The server never prompts,
// inspects a TTY, or reads stdin. Mirrors internal/rpccli/comment_rm.go semantics
// (soft-delete sets deleted_at + logs comment.deleted; purge DELETEs the row +
// logs comment.purged). IfMatch (>0) is a machine-checkable etag precondition
// (mismatch → WRKQ_CONFLICT). For soft-delete it returns the updated DTO; for
// purge (row gone) it returns the pre-purge DTO snapshot.
func (a *API) CommentDelete(ctx context.Context, p CommentDeleteParams) (*WrkqComment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ID) == "" {
		return nil, NewValidationError("comment id is required", map[string]any{"field": "id"})
	}
	mode := strings.TrimSpace(p.Mode)
	switch mode {
	case "", "soft", "purge":
	default:
		return nil, NewValidationError("invalid comment delete mode: "+mode, map[string]any{
			"field":    "mode",
			"expected": "soft | purge",
		})
	}
	commentUUID, taskUUID, err := a.resolveComment(p.ID)
	if err != nil {
		return nil, err
	}
	taskID, err := a.taskFriendlyID(taskUUID)
	if err != nil {
		return nil, err
	}

	if p.IfMatch > 0 {
		var etag int64
		if serr := a.db.QueryRow("SELECT etag FROM comments WHERE uuid = ?", commentUUID).Scan(&etag); serr != nil {
			return nil, NewInternalError(serr)
		}
		if etag != p.IfMatch {
			return nil, NewConflictError("comment etag precondition failed", map[string]any{
				"currentEtag":  etag,
				"expectedEtag": p.IfMatch,
			})
		}
	}

	// Snapshot the DTO before a purge removes the row.
	preDTO, err := a.loadComment(commentUUID, taskID, "")
	if err != nil {
		return nil, err
	}

	attr, aerr := a.attributionFor(p.Actor)
	if aerr != nil {
		return nil, aerr
	}

	tx, err := a.db.Begin()
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	if mode == "purge" {
		if _, eerr := tx.Exec("DELETE FROM comments WHERE uuid = ?", commentUUID); eerr != nil {
			return nil, NewInternalError(eerr)
		}
		payload := `{"task_id":"` + taskUUID + `","comment_id":"` + preDTO.ID + `","hard_delete":true}`
		if eerr := events.NewWriter(a.db.DB).LogEvent(tx, &domain.Event{
			PrincipalRef: attr.PrincipalRef,
			ScopeRef:     attr.ScopeRef,
			ResourceType: "comment",
			ResourceUUID: &commentUUID,
			EventType:    "comment.purged",
			Payload:      &payload,
		}); eerr != nil {
			return nil, NewInternalError(eerr)
		}
		if cerr := tx.Commit(); cerr != nil {
			return nil, NewInternalError(cerr)
		}
		return preDTO, nil
	}

	if _, eerr := tx.Exec(`
		UPDATE comments
		SET deleted_at = datetime('now'),
		    deleted_by_principal_ref = ?,
		    deleted_by_scope_ref = ?,
		    etag = etag + 1
		WHERE uuid = ?
	`, attr.PrincipalRef, scopeBind(attr), commentUUID); eerr != nil {
		return nil, NewInternalError(eerr)
	}

	// Read back the bumped etag for the event (legacy re-fetches after the UPDATE).
	var newEtag int64
	if eerr := tx.QueryRow("SELECT etag FROM comments WHERE uuid = ?", commentUUID).Scan(&newEtag); eerr != nil {
		return nil, NewInternalError(eerr)
	}

	// Event-log payload MUST byte-match legacy internal/rpccli/comment_rm.go:
	// task_id = the task UUID (not the friendly id), comment_id = the FRIENDLY
	// comment id (not the uuid), deleted_by_principal_ref present, soft_delete:true.
	// The event ETag is the NEW (post-bump) comment etag.
	payload := `{"task_id":"` + taskUUID + `","comment_id":"` + preDTO.ID +
		`","deleted_by_principal_ref":"` + attr.PrincipalRef + `","soft_delete":true}`
	if eerr := events.NewWriter(a.db.DB).LogEvent(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "comment",
		ResourceUUID: &commentUUID,
		EventType:    "comment.deleted",
		ETag:         &newEtag,
		Payload:      &payload,
	}); eerr != nil {
		return nil, NewInternalError(eerr)
	}
	if cerr := tx.Commit(); cerr != nil {
		return nil, NewInternalError(cerr)
	}

	return a.loadComment(commentUUID, taskID, "")
}

// resolveComment resolves a comment id or uuid to (commentUUID, taskUUID).
func (a *API) resolveComment(ref string) (commentUUID, taskUUID string, err error) {
	scanErr := a.db.QueryRow(
		"SELECT uuid, task_uuid FROM comments WHERE id = ? OR uuid = ? LIMIT 1",
		ref, ref,
	).Scan(&commentUUID, &taskUUID)
	if scanErr == sql.ErrNoRows {
		return "", "", NewNotFoundError(ref, "comment")
	}
	if scanErr != nil {
		return "", "", NewInternalError(scanErr)
	}
	return commentUUID, taskUUID, nil
}

func (a *API) loadComment(commentUUID, taskID, containerID string) (*WrkqComment, error) {
	row := a.db.QueryRow(
		"SELECT uuid, id, task_uuid, container_uuid, kind, body, meta, etag, created_at, updated_at, deleted_at, created_by_principal_ref FROM comments WHERE uuid = ?",
		commentUUID,
	)
	c, _, err := scanCommentRow(row, taskID, containerID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, NewNotFoundError(commentUUID, "comment")
		}
		return nil, NewInternalError(err)
	}
	return c, nil
}

// scanCommentRow scans a comment row (column order matches the queries above).
func scanCommentRow(s rowScanner, taskID, containerID string) (*WrkqComment, string, error) {
	var (
		commentUUID, commentID, body        string
		taskUUID, containerUUID, kind, meta sql.NullString
		etag                                int64
		createdAt                           string
		updatedAt, deletedAt, createdByPrin sql.NullString
	)
	if err := s.Scan(
		&commentUUID, &commentID, &taskUUID, &containerUUID, &kind, &body, &meta, &etag,
		&createdAt, &updatedAt, &deletedAt, &createdByPrin,
	); err != nil {
		return nil, "", err
	}
	c := &WrkqComment{
		UUID:                  commentUUID,
		ID:                    commentID,
		Task:                  taskID,
		Container:             containerID,
		Kind:                  kind.String,
		Body:                  body,
		Meta:                  parseMeta(meta.String),
		ETag:                  etag,
		CreatedAt:             toRFC3339(createdAt),
		UpdatedAt:             toRFC3339(updatedAt.String),
		DeletedAt:             toRFC3339(deletedAt.String),
		CreatedByPrincipalRef: createdByPrin.String,
		createdAtRaw:          createdAt,
	}
	return c, createdAt, nil
}
