package wrkqapi

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/lherron/wrkq/internal/attach"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
)

// ContainerCreate creates a project/directory container and returns the stable
// container DTO with server-computed path and etag.
func (a *API) ContainerCreate(ctx context.Context, p ContainerCreateParams) (*WrkqContainer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	parentUUID, slug, err := a.resolveContainerCreateTarget(p)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(slug) == "" {
		return nil, NewValidationError("container slug is required", map[string]any{"field": "slug"})
	}
	normalizedSlug, serr := paths.NormalizeSlug(slug)
	if serr != nil {
		return nil, NewValidationError(serr.Error(), map[string]any{"field": "slug"})
	}

	result, err := a.store.Containers.CreateWithAttribution(a.attributionFor(p.Actor), store.ContainerCreateParams{
		Slug:       normalizedSlug,
		Title:      strings.TrimSpace(p.Title),
		ParentUUID: parentUUID,
		Kind:       strings.TrimSpace(p.Kind),
	})
	if err != nil {
		return nil, mapContainerStoreError(err, "")
	}
	return a.loadContainer(result.UUID)
}

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

// ContainerDelete hard-deletes an empty container.
func (a *API) ContainerDelete(ctx context.Context, p ContainerDeleteParams) (*WrkqContainerDeleteResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	selector := containerMutationSelector(p.Container, p.Path, p.Project)
	if selector == "" {
		return nil, NewValidationError("container selector is required", map[string]any{"field": "container"})
	}
	containerUUID, _, err := selectors.ResolveContainer(a.db, selector)
	if err != nil {
		return nil, NewNotFoundError(selector, "container")
	}
	if err := a.rejectRootContainer(containerUUID); err != nil {
		return nil, err
	}
	if err := a.store.Containers.DeleteWithAttribution(a.attributionFor(p.Actor), containerUUID, p.ExpectETag); err != nil {
		return nil, mapContainerStoreError(err, selector)
	}
	return &WrkqContainerDeleteResult{Deleted: true}, nil
}

// ContainerDeleteRecursive preflights or commits a destructive container subtree
// purge. Commit compares expected impact against a fresh impact inside the store
// transaction and cleans attachment files after the DB commit.
func (a *API) ContainerDeleteRecursive(ctx context.Context, p ContainerDeleteRecursiveParams) (*WrkqContainerDeleteRecursiveResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	selector := containerMutationSelector(p.Container, p.Path, p.Project)
	if selector == "" {
		return nil, NewValidationError("container selector is required", map[string]any{"field": "container"})
	}
	containerUUID, _, err := selectors.ResolveContainer(a.db, selector)
	if err != nil {
		return nil, NewNotFoundError(selector, "container")
	}
	if err := a.rejectRootContainer(containerUUID); err != nil {
		return nil, err
	}

	dto, err := a.loadContainer(containerUUID)
	if err != nil {
		return nil, err
	}
	if p.DryRun {
		impact, ierr := a.store.Containers.DeleteRecursiveImpact(containerUUID)
		if ierr != nil {
			return nil, mapContainerStoreError(ierr, selector)
		}
		return deleteRecursiveImpactResult(dto, impact), nil
	}
	if p.Expected == nil {
		return nil, NewValidationError("expected impact is required", map[string]any{"field": "expected"})
	}
	expected := store.ContainerDeleteRecursiveImpact{
		ContainerUUID: containerUUID,
		Containers:    p.Expected.Containers,
		Tasks:         p.Expected.Tasks,
		Attachments:   p.Expected.Attachments,
		Bytes:         p.Expected.Bytes,
	}
	result, derr := a.store.Containers.DeleteRecursiveWithAttribution(a.attributionFor(p.Actor), containerUUID, p.ExpectETag, expected)
	if derr != nil {
		return nil, mapContainerStoreError(derr, selector)
	}
	cleanupErrors := a.cleanupRecursiveAttachmentFiles(result)
	return &WrkqContainerDeleteRecursiveResult{
		Deleted:            boolPtr(result.Deleted),
		ContainersDeleted:  int64Ptr(result.ContainersDeleted),
		TasksDeleted:       int64Ptr(result.TasksDeleted),
		AttachmentsDeleted: int64Ptr(result.AttachmentsDeleted),
		BytesFreed:         int64Ptr(result.BytesFreed),
		FileCleanupErrors:  cleanupErrors,
	}, nil
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

func (a *API) resolveContainerCreateTarget(p ContainerCreateParams) (*string, string, error) {
	slug := strings.TrimSpace(p.Slug)
	parentSelector := strings.TrimSpace(p.ParentPath)
	if parentSelector == "" {
		parentSelector = strings.TrimSpace(p.Project)
	}
	if slug != "" {
		if parentSelector == "" {
			parentSelector = strings.TrimSpace(p.Path)
		}
		if parentSelector == "" {
			return nil, slug, nil
		}
		parentUUID, _, err := selectors.ResolveContainer(a.db, parentSelector)
		if err != nil {
			return nil, "", NewNotFoundError(parentSelector, "container")
		}
		return &parentUUID, slug, nil
	}
	if strings.TrimSpace(p.Path) != "" {
		parentUUID, finalSlug, _, err := selectors.ResolveParentContainer(a.db, p.Path)
		if err != nil {
			return nil, "", NewNotFoundError(p.Path, "container")
		}
		return parentUUID, finalSlug, nil
	}
	if strings.TrimSpace(p.Title) != "" {
		slug = slugFromTitle(p.Title)
		if parentSelector == "" {
			return nil, slug, nil
		}
		parentUUID, _, err := selectors.ResolveContainer(a.db, parentSelector)
		if err != nil {
			return nil, "", NewNotFoundError(parentSelector, "container")
		}
		return &parentUUID, slug, nil
	}
	return nil, "", NewValidationError("container slug or path is required", map[string]any{"field": "slug"})
}

func containerMutationSelector(container, path, project string) string {
	for _, value := range []string{container, path, project} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (a *API) rejectRootContainer(containerUUID string) error {
	var kind string
	if err := a.db.QueryRow("SELECT kind FROM containers WHERE uuid = ?", containerUUID).Scan(&kind); err != nil {
		if err == sql.ErrNoRows {
			return NewNotFoundError(containerUUID, "container")
		}
		return NewInternalError(err)
	}
	if kind == string(domain.ContainerKindRoot) {
		return NewValidationError("root container cannot be deleted", map[string]any{"field": "container"})
	}
	return nil
}

func deleteRecursiveImpactResult(container *WrkqContainer, impact *store.ContainerDeleteRecursiveImpact) *WrkqContainerDeleteRecursiveResult {
	return &WrkqContainerDeleteRecursiveResult{
		Container:   container,
		Containers:  int64Ptr(impact.Containers),
		Tasks:       int64Ptr(impact.Tasks),
		Attachments: int64Ptr(impact.Attachments),
		Bytes:       int64Ptr(impact.Bytes),
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}

func boolPtr(value bool) *bool {
	return &value
}

func (a *API) cleanupRecursiveAttachmentFiles(result *store.ContainerDeleteRecursiveResult) []string {
	if strings.TrimSpace(a.attachDir) == "" || result == nil {
		return nil
	}
	errs := []string{}
	for _, attachment := range result.Attachments {
		if attachment.RelativePath == "" {
			continue
		}
		if err := attach.DeleteFile(a.attachDir, attachment.RelativePath); err != nil {
			errs = append(errs, attachment.RelativePath+": "+err.Error())
		}
	}
	for _, taskUUID := range result.TaskUUIDs {
		if err := attach.DeleteTaskDir(a.attachDir, taskUUID); err != nil {
			errs = append(errs, "tasks/"+taskUUID+": "+err.Error())
		}
	}
	return errs
}

func mapContainerStoreError(err error, selector string) error {
	if err == nil {
		return nil
	}
	var mismatch *domain.ETagMismatchError
	if errors.As(err, &mismatch) {
		return NewConflictError("container etag precondition failed", map[string]any{"currentEtag": mismatch.Actual})
	}
	var impactMismatch *store.ContainerDeleteImpactMismatchError
	if errors.As(err, &impactMismatch) {
		return NewConflictError("container deleteRecursive impact changed", map[string]any{
			"current": map[string]any{
				"containers":  impactMismatch.Current.Containers,
				"tasks":       impactMismatch.Current.Tasks,
				"attachments": impactMismatch.Current.Attachments,
				"bytes":       impactMismatch.Current.Bytes,
			},
		})
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "not found"):
		return NewNotFoundError(selector, "container")
	case strings.Contains(lower, "unique") || strings.Contains(lower, "constraint"):
		return NewConflictError(msg, nil)
	case strings.Contains(lower, "not empty") ||
		strings.Contains(lower, "invalid") ||
		strings.Contains(lower, "required") ||
		strings.Contains(lower, "must ") ||
		strings.Contains(lower, "cannot be deleted") ||
		strings.Contains(lower, "only project containers"):
		return NewValidationError(msg, nil)
	default:
		return NewInternalError(err)
	}
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
