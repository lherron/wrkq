package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/lherron/wrkq/internal/selectors"
)

// ContainerCatViewParams selects the container to project.
type ContainerCatViewParams struct {
	Container string `json:"container,omitempty"`
	Path      string `json:"path,omitempty"`
}

// WrkqContainerCatView is the server-owned COMPATIBILITY read model for
// `wrkq container cat` (T-05090 / list-view ruling). It reproduces the legacy
// snake_case object exactly — including description, friendly parent_id,
// sort_index, webhook_urls, and created_by/updated_by actor slugs — none of
// which are on the canonical WrkqContainer DTO. Not a domain resource.
type WrkqContainerCatView struct {
	ID          string   `json:"id"`
	UUID        string   `json:"uuid"`
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Kind        string   `json:"kind"`
	ParentID    *string  `json:"parent_id,omitempty"`
	ParentUUID  *string  `json:"parent_uuid,omitempty"`
	ParentPath  *string  `json:"parent_path,omitempty"`
	Path        string   `json:"path"`
	WebhookURLs []string `json:"webhook_urls,omitempty"`
	SortIndex   int      `json:"sort_index"`
	Etag        int64    `json:"etag"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	ArchivedAt  *string  `json:"archived_at,omitempty"`
	CreatedBy   string   `json:"created_by"`
	UpdatedBy   string   `json:"updated_by"`
}

// ContainerCatView assembles the legacy container-cat projection for one
// container. Selector→UUID resolution happens first (outside the snapshot); the
// single read transaction then covers the RESOLVED container UUID's projection
// only (scalars + actor slugs + parent id/path + webhook_urls are internally
// consistent for that UUID). The snapshot does not cover selector resolution.
// Mirrors internal/rpccli/container.go.
func (a *API) ContainerCatView(ctx context.Context, p ContainerCatViewParams) (*WrkqContainerCatView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	selector := p.Container
	if selector == "" {
		selector = p.Path
	}
	containerUUID, _, err := selectors.ResolveContainer(a.db, selector)
	if err != nil {
		return nil, NewNotFoundError(selector, "container")
	}

	tx, err := a.db.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	var containerPath string
	if err := tx.QueryRowContext(ctx, "SELECT path FROM v_container_paths WHERE uuid = ?", containerUUID).Scan(&containerPath); err != nil {
		return nil, NewInternalError(err)
	}

	var (
		id, slug, title, description, kind           string
		parentUUID, archivedAt, webhookRaw           *string
		sortIndex                                    int
		etag                                         int64
		createdAt, updatedAt                         string
		createdByPrincipalRef, updatedByPrincipalRef sql.NullString
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT id, slug, title, description, kind,
		       parent_uuid, webhook_urls, sort_index, etag,
		       created_at, updated_at, archived_at,
		       created_by_principal_ref, updated_by_principal_ref
		FROM containers WHERE uuid = ?`, containerUUID).Scan(
		&id, &slug, &title, &description, &kind,
		&parentUUID, &webhookRaw, &sortIndex, &etag,
		&createdAt, &updatedAt, &archivedAt,
		&createdByPrincipalRef, &updatedByPrincipalRef,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, NewNotFoundError(selector, "container")
		}
		return nil, NewInternalError(err)
	}

	var createdBySlug, updatedBySlug string
	if createdByPrincipalRef.Valid {
		createdBySlug = principalHandle(createdByPrincipalRef.String)
	}
	if updatedByPrincipalRef.Valid {
		updatedBySlug = principalHandle(updatedByPrincipalRef.String)
	}

	var parentID, parentPath *string
	if parentUUID != nil {
		var pID string
		if e := tx.QueryRowContext(ctx, "SELECT id FROM containers WHERE uuid = ?", *parentUUID).Scan(&pID); e == nil {
			parentID = &pID
		}
		if idx := strings.LastIndex(containerPath, "/"); idx >= 0 {
			pp := containerPath[:idx]
			parentPath = &pp
		}
	}

	var webhookURLs []string
	if webhookRaw != nil && *webhookRaw != "" {
		if json.Unmarshal([]byte(*webhookRaw), &webhookURLs) != nil {
			webhookURLs = nil
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, NewInternalError(err)
	}
	return &WrkqContainerCatView{
		ID: id, UUID: containerUUID, Slug: slug, Title: title, Description: description,
		Kind: kind, ParentID: parentID, ParentUUID: parentUUID, ParentPath: parentPath,
		Path: containerPath, WebhookURLs: webhookURLs, SortIndex: sortIndex, Etag: etag,
		CreatedAt: createdAt, UpdatedAt: updatedAt, ArchivedAt: archivedAt,
		CreatedBy: createdBySlug, UpdatedBy: updatedBySlug,
	}, nil
}
