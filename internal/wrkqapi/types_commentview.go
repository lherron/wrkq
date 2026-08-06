package wrkqapi

// CommentCatViewParams selects one comment (friendly ID or UUID).
type CommentCatViewParams struct {
	Comment string `json:"comment"`
}

// WrkqCommentCatView is the server-owned COMPATIBILITY read model for
// `wrkq comment cat`. Legacy builds a map (alphabetical JSON keys) with several
// conditional fields beyond WrkqComment (actor display, scope refs, deletion
// provenance). Fields are declared in alphabetical json-tag order so a struct
// marshal matches the legacy map's key ordering byte-for-byte. Not a domain DTO.
type WrkqCommentCatView struct {
	Body                  string  `json:"body"`
	CreatedAt             string  `json:"created_at"`
	CreatedByPrincipalRef *string `json:"created_by_principal_ref,omitempty"`
	CreatedByScopeRef     *string `json:"created_by_scope_ref,omitempty"`
	DeletedAt             *string `json:"deleted_at,omitempty"`
	DeletedByPrincipalRef *string `json:"deleted_by_principal_ref,omitempty"`
	DeletedByScopeRef     *string `json:"deleted_by_scope_ref,omitempty"`
	Etag                  int64   `json:"etag"`
	ID                    string  `json:"id"`
	Meta                  *string `json:"meta,omitempty"`
	TaskID                string  `json:"task_id"`
	TaskUUID              string  `json:"task_uuid"`
	UpdatedAt             *string `json:"updated_at,omitempty"`
	UUID                  string  `json:"uuid"`

	// kind is an additive typed-comment carrier for CLI rendering. It remains
	// unexported so the frozen compatibility DTO fingerprint/schema is unchanged;
	// MarshalJSON emits it only for a typed row, preserving plain-row bytes.
	kind *string
}

type commentRowScanner interface {
	Scan(dest ...any) error
}
