//go:build wrkq_local

package wrkqapi

import (
	"context"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/domain"
)

// subtaskOwnerStateClause is shared by the canonical and compatibility read surfaces.
func subtaskOwnerStateClause(state string, subtasks bool) (string, error) {
	if !subtasks {
		return "", NewValidationError("ownerState requires subtasks", nil)
	}
	if state == "terminal" {
		return "t.subtask_owner_uuid IN (SELECT uuid FROM tasks WHERE state IN ('completed','cancelled','archived','deleted'))", nil
	}
	if !domain.IsValidState(state) {
		return "", NewValidationError("invalid ownerState", nil)
	}
	return "t.subtask_owner_uuid IN (SELECT uuid FROM tasks WHERE state='" + state + "')", nil
}

func (a *API) lsOwnerSubtasks(ctx context.Context, owner string, filter treeFilter, pag *cursor.ApplyResult) ([]WrkqLsEntry, error) {
	query := `SELECT t.id,t.slug,t.title,t.created_at,t.updated_at,t.state,t.kind,COALESCE(cp.path||'/'||o.slug||'/'||t.slug,t.slug) FROM tasks t JOIN tasks o ON o.uuid=t.subtask_owner_uuid LEFT JOIN v_container_paths cp ON cp.uuid=t.project_uuid WHERE t.subtask_owner_uuid=?`
	args := []any{owner}
	if clause, vals := filter.sqlPredicate("t"); clause != "" {
		query += clause
		args = append(args, vals...)
	}
	// Expose unambiguous column names to the shared cursor clauses.
	query = "SELECT * FROM (" + query + ") WHERE 1=1"
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
	result := []WrkqLsEntry{}
	for rows.Next() {
		var item WrkqLsEntry
		item.Type = "task"
		if err := rows.Scan(&item.ID, &item.Slug, &item.Title, &item.CreatedAt, &item.UpdatedAt, &item.State, &item.Kind, &item.Path); err != nil {
			return nil, NewInternalError(err)
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
