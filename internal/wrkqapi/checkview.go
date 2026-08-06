//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"

	"github.com/lherron/wrkq/internal/selectors"
)

// TaskBlockedView resolves the selector and enumerates the incomplete tasks that
// block it via the same store query legacy `check blocked` uses
// (store.Tasks.BlockedBy), returning the compat projection. A selector that does
// not resolve surfaces the raw resolve error so the CLI can re-wrap it exactly as
// legacy ("failed to resolve task: <err>").
func (a *API) TaskBlockedView(ctx context.Context, p TaskBlockedViewParams) (*WrkqTaskBlockedView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	taskUUID, taskID, err := selectors.ResolveTask(a.db, p.Task)
	if err != nil {

		return nil, NewValidationError(err.Error(), map[string]any{"field": "task"})
	}
	blockers, err := a.store.Tasks.BlockedBy(taskUUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	view := &WrkqTaskBlockedView{
		TaskID:    taskID,
		TaskUUID:  taskUUID,
		IsBlocked: len(blockers) > 0,
		Blockers:  make([]WrkqTaskBlockedEntry, len(blockers)),
	}
	for i, b := range blockers {
		view.Blockers[i] = WrkqTaskBlockedEntry{
			ID:    b.ID,
			UUID:  b.UUID,
			Slug:  b.Slug,
			Title: b.Title,
			State: b.State,
		}
	}
	return view, nil
}

// InboxView reproduces legacy runCheckInbox's two queries (open inbox tasks, then
// ack-pending requested-by tasks) over the already-scoped inbox path / project id.
func (a *API) InboxView(ctx context.Context, p InboxViewParams) (*WrkqInboxView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	results := []WrkqInboxEntry{}

	pathLike := p.InboxPath + "/%"
	openQuery := `
		SELECT t.uuid, t.id, t.slug, t.title, t.state, t.priority, t.kind, t.due_at, t.etag,
		       t.requested_by_project_id, t.assigned_project_id, t.acknowledged_at, t.resolution,
		       cp.path || '/' || t.slug AS path
		FROM tasks t
		JOIN v_container_paths cp ON cp.uuid = t.project_uuid
		WHERE t.state = 'open'
		  AND (cp.path = ? OR cp.path LIKE ?)
		ORDER BY t.priority ASC, t.created_at ASC`
	rows, err := a.db.QueryContext(ctx, openQuery, p.InboxPath, pathLike)
	if err != nil {
		return nil, NewInternalError(err)
	}
	open, err := scanInboxRows(rows)
	if err != nil {
		return nil, err
	}
	results = append(results, open...)

	if p.ProjectID != "" {
		ackQuery := `
			SELECT t.uuid, t.id, t.slug, t.title, t.state, t.priority, t.kind, t.due_at, t.etag,
			       t.requested_by_project_id, t.assigned_project_id, t.acknowledged_at, t.resolution,
			       cp.path || '/' || t.slug AS path
			FROM tasks t
			JOIN v_container_paths cp ON cp.uuid = t.project_uuid
			WHERE t.requested_by_project_id = ?
			  AND t.state IN ('completed', 'cancelled')
			  AND t.acknowledged_at IS NULL
			ORDER BY t.updated_at DESC`
		ackRows, err := a.db.QueryContext(ctx, ackQuery, p.ProjectID)
		if err != nil {
			return nil, NewInternalError(err)
		}
		ack, err := scanInboxRows(ackRows)
		if err != nil {
			return nil, err
		}
		results = append(results, ack...)
	}

	return &WrkqInboxView{Items: results}, nil
}

// scanInboxRows decodes the shared inbox row projection (closes rows on return).
func scanInboxRows(rows *sql.Rows) ([]WrkqInboxEntry, error) {
	defer func() { _ = rows.Close() }()
	var out []WrkqInboxEntry
	for rows.Next() {
		var r WrkqInboxEntry
		var state, kind, dueAt sql.NullString
		var requestedBy, assignedProject, acknowledgedAt, resolution sql.NullString
		var priority sql.NullInt64
		if err := rows.Scan(&r.UUID, &r.ID, &r.Slug, &r.Title, &state, &priority, &kind, &dueAt, &r.ETag,
			&requestedBy, &assignedProject, &acknowledgedAt, &resolution, &r.Path); err != nil {
			return nil, NewInternalError(err)
		}
		r.Type = "task"
		if state.Valid {
			r.State = &state.String
		}
		if priority.Valid {
			pv := int(priority.Int64)
			r.Priority = &pv
		}
		if kind.Valid {
			r.Kind = &kind.String
		}
		if dueAt.Valid {
			r.DueAt = &dueAt.String
		}
		if requestedBy.Valid {
			r.RequestedByProjectID = &requestedBy.String
		}
		if assignedProject.Valid {
			r.AssignedProjectID = &assignedProject.String
		}
		if acknowledgedAt.Valid {
			r.AcknowledgedAt = &acknowledgedAt.String
		}
		if resolution.Valid {
			r.Resolution = &resolution.String
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(err)
	}
	return out, nil
}
