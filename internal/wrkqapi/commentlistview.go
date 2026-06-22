package wrkqapi

import (
	"context"

	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/selectors"
)

// CommentListViewParams mirrors the legacy `wrkq comment ls` surface for one
// task. The server owns cursor.Apply, limit+1, sort/direction validation, and
// next-cursor construction.
type CommentListViewParams struct {
	Task           string `json:"task"`
	Limit          int    `json:"limit,omitempty"`
	Cursor         string `json:"cursor,omitempty"`
	IncludeDeleted bool   `json:"includeDeleted,omitempty"`
	Sort           string `json:"sort,omitempty"` // default created_at
	Desc           bool   `json:"desc,omitempty"`
}

// WrkqCommentListView is the server-owned COMPATIBILITY list projection for
// `wrkq comment ls`. Items reuse the comment compat row shape; next_cursor is
// the legacy cursor token (empty when there is no further page). Not canonical.
type WrkqCommentListView struct {
	Items      []WrkqCommentCatView `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

var commentListSortFields = map[string]bool{"created_at": true, "updated_at": true, "id": true}

// CommentListView lists one task's comments with legacy cursor pagination.
func (a *API) CommentListView(ctx context.Context, p CommentListViewParams) (*WrkqCommentListView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	taskUUID, _, err := selectors.ResolveTask(a.db, p.Task)
	if err != nil {
		return nil, NewNotFoundError(p.Task, "task")
	}
	sortField := p.Sort
	if sortField == "" {
		sortField = "created_at"
	}
	if !commentListSortFields[sortField] {
		return nil, NewValidationError("unsupported sort field: "+sortField, map[string]any{"field": "sort"})
	}

	pag, err := cursor.Apply(p.Cursor, cursor.ApplyOptions{
		SortFields: []string{sortField},
		SQLFields:  []string{"c." + sortField},
		Descending: []bool{p.Desc},
		IDField:    "c.id",
		Limit:      p.Limit,
	})
	if err != nil {
		// cursor.Apply already prefixes "invalid cursor:"; don't double-wrap.
		return nil, NewValidationError(err.Error(), map[string]any{"field": "cursor"})
	}

	query := commentCatViewSelect + " WHERE c.task_uuid = ?"
	args := []any{taskUUID}
	if !p.IncludeDeleted {
		query += " AND c.deleted_at IS NULL"
	}
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

	items := []WrkqCommentCatView{}
	for rows.Next() {
		v, serr := scanCommentCatView(rows)
		if serr != nil {
			return nil, NewInternalError(serr)
		}
		items = append(items, *v)
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(err)
	}

	nextCursor := ""
	if p.Limit > 0 && len(items) > p.Limit {
		items = items[:p.Limit]
		last := items[len(items)-1]
		sortVal := commentSortValue(last, sortField)
		nextCursor, _ = cursor.BuildNextCursor([]string{sortField}, []any{sortVal}, last.ID)
	}
	return &WrkqCommentListView{Items: items, NextCursor: nextCursor}, nil
}

func commentSortValue(c WrkqCommentCatView, sortField string) any {
	switch sortField {
	case "updated_at":
		if c.UpdatedAt != nil {
			return *c.UpdatedAt
		}
		return ""
	case "id":
		return c.ID
	default: // created_at
		return c.CreatedAt
	}
}
