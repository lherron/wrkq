package wrkqapi

import "github.com/lherron/wrkq/internal/webhooksub"

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
	ID          string                    `json:"id"`
	UUID        string                    `json:"uuid"`
	Slug        string                    `json:"slug"`
	Title       string                    `json:"title"`
	Description string                    `json:"description"`
	Kind        string                    `json:"kind"`
	ParentID    *string                   `json:"parent_id,omitempty"`
	ParentUUID  *string                   `json:"parent_uuid,omitempty"`
	ParentPath  *string                   `json:"parent_path,omitempty"`
	Path        string                    `json:"path"`
	WebhookURLs []webhooksub.Subscription `json:"webhook_urls,omitempty"`
	SortIndex   int                       `json:"sort_index"`
	Etag        int64                     `json:"etag"`
	CreatedAt   string                    `json:"created_at"`
	UpdatedAt   string                    `json:"updated_at"`
	ArchivedAt  *string                   `json:"archived_at,omitempty"`
	CreatedBy   string                    `json:"created_by"`
	UpdatedBy   string                    `json:"updated_by"`
	Promises    []WrkqPromise             `json:"promises"`
}
