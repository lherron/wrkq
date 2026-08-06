//go:build wrkq_local

package wrkqapi

import (
	"context"

	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/selectors"
)

var commentListSortFields = map[string]bool{"created_at": true, "updated_at": true, "id": true}

// CommentListView lists one or more tasks' comments with legacy cursor
// pagination. With multiple tasks it reproduces legacy accumulation EXACTLY: the
// same cursor predicate + limit+1 is applied to each task's query and the rows
// are appended in task order, then the combined set is truncated at limit and the
// next cursor is built from the last surviving row.
func (a *API) CommentListView(ctx context.Context, p CommentListViewParams) (*WrkqCommentListView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	tasks := p.Tasks
	if len(tasks) == 0 {
		tasks = []string{p.Task}
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

		return nil, NewValidationError(err.Error(), map[string]any{"field": "cursor"})
	}

	items := []WrkqCommentCatView{}
	for _, task := range tasks {
		taskUUID, _, rerr := selectors.ResolveTask(a.db, task)
		if rerr != nil {
			return nil, NewNotFoundError(task, "task")
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

		rows, qerr := a.db.QueryContext(ctx, query, args...)
		if qerr != nil {
			return nil, NewInternalError(qerr)
		}
		for rows.Next() {
			v, serr := scanCommentCatView(rows)
			if serr != nil {
				_ = rows.Close()
				return nil, NewInternalError(serr)
			}
			items = append(items, *v)
		}
		if rerr := rows.Err(); rerr != nil {
			_ = rows.Close()
			return nil, NewInternalError(rerr)
		}
		_ = rows.Close()
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
	default:
		return c.CreatedAt
	}
}
