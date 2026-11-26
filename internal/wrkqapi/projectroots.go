//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/selectors"
)

// ProjectSetRoot stores a caller-normalized checkout root on a top-level
// project container. The server deliberately does not expand or rewrite the
// path: ~/... is host-portable data, and expansion belongs to consumers.
func (a *API) ProjectSetRoot(ctx context.Context, p ProjectSetRootParams) (*WrkqProjectEntry, error) {
	return a.setProjectField(ctx, p.Project, "root", p.Root, p.Actor, p.ExpectETag)
}

func (a *API) ProjectSetShort(ctx context.Context, p ProjectSetShortParams) (*WrkqProjectEntry, error) {
	if p.Short == nil {
		return nil, NewValidationError("short is required; pass an empty string to clear it", map[string]any{"field": "short"})
	}
	return a.setProjectField(ctx, p.Project, "short", *p.Short, p.Actor, p.ExpectETag)
}

func (a *API) setProjectField(ctx context.Context, selector, field, value, actor string, expectETag int64) (*WrkqProjectEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	project := strings.TrimSpace(selector)
	if project == "" {
		return nil, NewValidationError("project is required", map[string]any{"field": "project"})
	}
	parsed := selectors.Parse(project)
	if parsed.Type == selectors.TypeTask || (strings.HasPrefix(parsed.Token, "T-") || strings.HasPrefix(parsed.Token, "W-")) {
		return nil, NewValidationError("--"+field+" can only be set on a top-level project; task IDs are not projects", map[string]any{"field": "project"})
	}

	projectUUID, _, err := selectors.ResolveContainer(a.db, project)
	if err != nil {
		return nil, NewNotFoundError(project, "project")
	}
	var kind string
	var isTopLevel bool
	if err := a.db.QueryRow(`
		SELECT c.kind, c.parent_uuid = (SELECT uuid FROM containers WHERE kind = 'root')
		FROM containers c WHERE c.uuid = ?`, projectUUID).Scan(&kind, &isTopLevel); err != nil {
		if err == sql.ErrNoRows {
			return nil, NewNotFoundError(project, "project")
		}
		return nil, NewInternalError(err)
	}
	if kind != string(domain.ContainerKindProject) || !isTopLevel {
		return nil, NewValidationError("--"+field+" can only be set on a top-level project container", map[string]any{"field": "project"})
	}

	var stored any
	if value != "" {
		stored = value
	}
	attr, err := a.attributionFor(ctx, actor)
	if err != nil {
		return nil, err
	}
	if _, err := a.store.Containers.UpdateFieldsWithAttribution(attr, projectUUID, map[string]any{field: stored}, expectETag); err != nil {
		return nil, mapContainerStoreError(err, project)
	}
	return a.loadProjectEntry(projectUUID)
}

func (a *API) loadProjectEntry(projectUUID string) (*WrkqProjectEntry, error) {
	var id, slug string
	var title, root, short sql.NullString
	if err := a.db.QueryRow(`
		SELECT id, slug, title, root, short
		FROM containers
		WHERE uuid = ?
		  AND kind = 'project'
		  AND parent_uuid = (SELECT uuid FROM containers WHERE kind = 'root')`, projectUUID).
		Scan(&id, &slug, &title, &root, &short); err != nil {
		if err == sql.ErrNoRows {
			return nil, NewNotFoundError(projectUUID, "project")
		}
		return nil, NewInternalError(fmt.Errorf("failed to load project: %w", err))
	}
	titleValue := slug
	if title.Valid && title.String != "" {
		titleValue = title.String
	}
	return &WrkqProjectEntry{
		Type: "project", ID: id, Slug: slug, Title: titleValue, Path: slug,
		Root: nullStringPtr(root), Short: nullStringPtr(short),
	}, nil
}
