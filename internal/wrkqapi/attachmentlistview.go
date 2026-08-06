//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"

	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/selectors"
)

// AttachmentListView lists one task's attachment rows with legacy cursor pagination.
func (a *API) AttachmentListView(ctx context.Context, p AttachmentListViewParams) (*WrkqAttachmentListView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	taskUUID, _, err := selectors.ResolveTask(a.db, p.Task)
	if err != nil {
		return nil, NewNotFoundError(p.Task, "task")
	}
	pag, err := cursor.Apply(p.Cursor, cursor.ApplyOptions{
		SortFields: []string{"created_at"},
		SQLFields:  []string{"a.created_at"},
		Descending: []bool{false},
		IDField:    "a.id",
		Limit:      p.Limit,
	})
	if err != nil {
		return nil, NewValidationError("invalid cursor: "+err.Error(), map[string]any{"field": "cursor"})
	}

	query := `
		SELECT a.uuid, a.id, a.filename, a.relative_path, a.mime_type, a.size_bytes,
		       a.checksum, a.created_at, a.created_by_principal_ref
		FROM attachments a
		WHERE a.task_uuid = ?`
	args := []any{taskUUID}
	if pag.WhereClause != "" {
		query += " AND " + pag.WhereClause
		args = append(args, pag.Params...)
	}
	query += " " + pag.OrderByClause
	if pag.LimitClause != "" {
		query += " " + pag.LimitClause
		args = append(args, *pag.LimitParam)
	}

	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()

	items := []WrkqAttachmentListRow{}
	for rows.Next() {
		var row WrkqAttachmentListRow
		var mimeType, checksum, createdBy sql.NullString
		if err := rows.Scan(&row.UUID, &row.ID, &row.Filename, &row.RelativePath,
			&mimeType, &row.SizeBytes, &checksum, &row.CreatedAt, &createdBy); err != nil {
			return nil, NewInternalError(err)
		}
		row.MimeType = nsPtr(mimeType)
		row.Checksum = nsPtr(checksum)
		row.CreatedByPrincipalRef = nsPtr(createdBy)
		items = append(items, row)
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(err)
	}

	nextCursor := ""
	if p.Limit > 0 && len(items) > p.Limit {
		items = items[:p.Limit]
		last := items[len(items)-1]
		nextCursor, _ = cursor.BuildNextCursor([]string{"created_at"}, []any{last.CreatedAt}, last.ID)
	}
	return &WrkqAttachmentListView{Items: items, NextCursor: nextCursor}, nil
}
