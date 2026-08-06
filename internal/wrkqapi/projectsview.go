//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lherron/wrkq/internal/cursor"
)

func (a *API) ProjectsListView(ctx context.Context, p ProjectsListViewParams) (*WrkqProjectsListView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pag, err := cursor.Apply(p.Cursor, cursor.ApplyOptions{
		SortFields: []string{"slug"},
		Descending: []bool{false},
		IDField:    "id",
		Limit:      p.Limit,
	})
	if err != nil {
		return nil, NewValidationError(err.Error(), map[string]any{"field": "cursor"})
	}

	query := `
		SELECT uuid, id, slug, title, root
		FROM containers
		WHERE parent_uuid = (SELECT uuid FROM containers WHERE kind = 'root')
	`
	args := []any{}
	if !p.IncludeArchived {
		query += ` AND archived_at IS NULL`
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

	rows, err := a.db.Query(query, args...)
	if err != nil {
		return nil, NewInternalError(fmt.Errorf("failed to query projects: %w", err))
	}
	defer func() { _ = rows.Close() }()

	projects := []WrkqProjectEntry{}
	for rows.Next() {
		var uuid, id, slug string
		var title, root sql.NullString
		if err := rows.Scan(&uuid, &id, &slug, &title, &root); err != nil {
			return nil, NewInternalError(fmt.Errorf("failed to scan row: %w", err))
		}
		titleStr := slug
		if title.Valid && title.String != "" {
			titleStr = title.String
		}
		projects = append(projects, WrkqProjectEntry{
			Type:  "project",
			ID:    id,
			Slug:  slug,
			Title: titleStr,
			Path:  slug,
			Root:  nullStringPtr(root),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(err)
	}

	view := &WrkqProjectsListView{Items: projects}
	if p.Limit > 0 && len(projects) > p.Limit {
		view.Items = projects[:p.Limit]
		last := view.Items[len(view.Items)-1]
		view.NextCursor, _ = cursor.BuildNextCursor([]string{"slug"}, []any{last.Slug}, last.ID)
	}
	return view, nil
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	v := value.String
	return &v
}
