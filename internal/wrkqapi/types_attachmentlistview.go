package wrkqapi

// AttachmentListViewParams mirrors the legacy `wrkq attach ls` surface for one task.
type AttachmentListViewParams struct {
	Task   string `json:"task"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// WrkqAttachmentListRow is one legacy `attach ls` row (alphabetical json-tag
// order so a struct marshal matches the legacy map's key ordering).
type WrkqAttachmentListRow struct {
	Checksum              *string `json:"checksum,omitempty"`
	CreatedAt             string  `json:"created_at"`
	CreatedByPrincipalRef *string `json:"created_by_principal_ref,omitempty"`
	Filename              string  `json:"filename"`
	ID                    string  `json:"id"`
	MimeType              *string `json:"mime_type,omitempty"`
	RelativePath          string  `json:"relative_path"`
	SizeBytes             int64   `json:"size_bytes"`
	UUID                  string  `json:"uuid"`
}

// WrkqAttachmentListView is the server-owned COMPATIBILITY list projection for
// `wrkq attach ls` (DB-only; does not touch attachment storage). Cursor pagination
// is owned server-side. Not the canonical WrkqAttachment resource.
type WrkqAttachmentListView struct {
	Items      []WrkqAttachmentListRow `json:"items"`
	NextCursor string                  `json:"next_cursor,omitempty"`
}
