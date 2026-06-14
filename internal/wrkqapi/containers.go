package wrkqapi

import (
	"context"
	"database/sql"
	"strings"

	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/selectors"
)

// ContainerShow resolves a container by path or project selector and returns its
// camelCase DTO (including the computed path). Not found → WRKQ_NOT_FOUND.
func (a *API) ContainerShow(ctx context.Context, p ContainerShowParams) (*WrkqContainer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	selector := strings.TrimSpace(p.Path)
	if selector == "" {
		selector = strings.TrimSpace(p.Project)
	}
	if selector == "" {
		return nil, NewValidationError("container path or project is required", map[string]any{"field": "path"})
	}
	containerUUID, _, err := selectors.ResolveContainer(a.db, selector)
	if err != nil {
		return nil, NewNotFoundError(selector, "container")
	}
	return a.loadContainer(containerUUID)
}

// ContainerList returns containers, optionally scoped to a project's children
// and including archived containers.
func (a *API) ContainerList(ctx context.Context, p ContainerListParams) (*WrkqContainerListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	where := []string{}
	args := []any{}

	if strings.TrimSpace(p.Project) != "" {
		projectUUID, _, rerr := selectors.ResolveContainer(a.db, p.Project)
		if rerr != nil {
			return nil, NewNotFoundError(p.Project, "container")
		}
		where = append(where, "c.parent_uuid = ?")
		args = append(args, projectUUID)
	}
	if !p.IncludeArchived {
		where = append(where, "c.archived_at IS NULL")
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
		SQLFields:  []string{"c.created_at"},
		Descending: []bool{false},
		IDField:    "c.id",
		Limit:      limit,
	})
	if err != nil {
		return nil, NewValidationError("invalid cursor", map[string]any{"field": "cursor"})
	}
	if page.WhereClause != "" {
		where = append(where, page.WhereClause)
		args = append(args, page.Params...)
	}

	query := `SELECT c.uuid, c.id, c.slug, c.title, c.kind, c.parent_uuid, c.etag,
		c.created_at, c.updated_at, c.archived_at, COALESCE(v.path, c.slug)
		FROM containers c
		LEFT JOIN v_container_paths v ON v.uuid = c.uuid`
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

	items := []WrkqContainer{}
	for rows.Next() {
		c, scanErr := scanContainerRow(rows)
		if scanErr != nil {
			return nil, NewInternalError(scanErr)
		}
		items = append(items, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(err)
	}

	result := &WrkqContainerListResult{Items: items}
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

// loadContainer reads a container by UUID into a WrkqContainer DTO.
func (a *API) loadContainer(containerUUID string) (*WrkqContainer, error) {
	row := a.db.QueryRow(`
		SELECT c.uuid, c.id, c.slug, c.title, c.kind, c.parent_uuid, c.etag,
		       c.created_at, c.updated_at, c.archived_at, COALESCE(v.path, c.slug)
		FROM containers c
		LEFT JOIN v_container_paths v ON v.uuid = c.uuid
		WHERE c.uuid = ?`, containerUUID)
	c, err := scanContainerRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, NewNotFoundError(containerUUID, "container")
		}
		return nil, NewInternalError(err)
	}
	return c, nil
}

// scanContainerRow scans a container row (column order matches the queries
// above) into a WrkqContainer DTO.
func scanContainerRow(s rowScanner) (*WrkqContainer, error) {
	var (
		containerUUID, id, slug, kind, path string
		title                               sql.NullString
		parentUUID, archivedAt              sql.NullString
		etag                                int64
		createdAt, updatedAt                string
	)
	if err := s.Scan(
		&containerUUID, &id, &slug, &title, &kind, &parentUUID, &etag,
		&createdAt, &updatedAt, &archivedAt, &path,
	); err != nil {
		return nil, err
	}
	return &WrkqContainer{
		UUID:         containerUUID,
		ID:           id,
		Slug:         slug,
		Title:        title.String,
		Kind:         kind,
		ParentUUID:   parentUUID.String,
		Path:         path,
		ETag:         etag,
		CreatedAt:    toRFC3339(createdAt),
		UpdatedAt:    toRFC3339(updatedAt),
		ArchivedAt:   toRFC3339(archivedAt.String),
		createdAtRaw: createdAt,
	}, nil
}
