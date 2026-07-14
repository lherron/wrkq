package wrkqapi

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lherron/wrkq/internal/cursor"
)

// ProjectsListViewParams mirrors the legacy `wrkq projects` list query. Project
// root scoping is intentionally ignored by this command.
type ProjectsListViewParams struct {
	IncludeArchived bool   `json:"includeArchived,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	Cursor          string `json:"cursor,omitempty"`
}

// WrkqProjectEntry extends the projects compatibility row with the nullable
// checkout root registry field.
type WrkqProjectEntry struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title,omitempty"`
	Path  string `json:"path"`
	// Root is the stored host-portable checkout root. It is intentionally not
	// expanded here; consumers expand ~/... for their own host.
	Root *string `json:"root"`
}

// WrkqProjectsListView is the server-owned compatibility projection for
// `wrkq projects`.
type WrkqProjectsListView struct {
	Items      []WrkqProjectEntry `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

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
