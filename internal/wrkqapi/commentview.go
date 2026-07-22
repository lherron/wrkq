package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/lherron/wrkq/internal/id"
)

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

func (v WrkqCommentCatView) MarshalJSON() ([]byte, error) {
	type wire WrkqCommentCatView
	base, err := json.Marshal(wire(v))
	if err != nil || v.kind == nil {
		return base, err
	}
	var fields map[string]any
	if err := json.Unmarshal(base, &fields); err != nil {
		return nil, err
	}
	fields["kind"] = *v.kind
	return json.Marshal(fields)
}

// CommentCatView projects one comment into the legacy comment-cat shape under a
// single read (mirrors internal/rpccli/comment.go).
func (a *API) CommentCatView(ctx context.Context, p CommentCatViewParams) (*WrkqCommentCatView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ref := p.Comment
	isUUID := id.IsUUID(ref)
	isFriendly := id.IsFriendlyID(ref)
	if !isUUID && !isFriendly {
		return nil, NewValidationError("invalid comment reference: "+ref+" (expected friendly ID like C-00001 or UUID)", map[string]any{"field": "comment"})
	}
	where := "c.id = ?"
	if isUUID {
		where = "c.uuid = ?"
	}

	row := a.db.QueryRowContext(ctx, commentCatViewSelect+" WHERE "+where, ref)
	v, err := scanCommentCatView(row)
	if err == sql.ErrNoRows {
		return nil, NewNotFoundError(ref, "comment")
	}
	if err != nil {
		return nil, NewInternalError(err)
	}
	return v, nil
}

// commentCatViewSelect is the shared SELECT for the comment compat projection,
// used by both comment catView (single) and comment listView (paginated) so
// their rows are byte-identical.
const commentCatViewSelect = `
		SELECT c.uuid, c.id, c.task_uuid, c.kind, c.body, c.meta, c.etag,
	       c.created_at, c.updated_at, c.deleted_at,
	       c.created_by_principal_ref, c.created_by_scope_ref,
	       c.deleted_by_principal_ref, c.deleted_by_scope_ref,
	       t.id
	FROM comments c
	LEFT JOIN tasks t ON c.task_uuid = t.uuid`

type commentRowScanner interface {
	Scan(dest ...any) error
}

// scanCommentCatView scans one commentCatViewSelect row into the compat shape.
func scanCommentCatView(s commentRowScanner) (*WrkqCommentCatView, error) {
	var (
		commentUUID, commentID, taskUUID, body, createdAt, taskID string
		etag                                                      int64
		kind, meta, updatedAt, deletedAt                          sql.NullString
		createdByPrincipalRef, createdByScopeRef                  sql.NullString
		deletedByPrincipalRef, deletedByScopeRef                  sql.NullString
	)
	if err := s.Scan(
		&commentUUID, &commentID, &taskUUID, &kind, &body, &meta, &etag,
		&createdAt, &updatedAt, &deletedAt,
		&createdByPrincipalRef, &createdByScopeRef,
		&deletedByPrincipalRef, &deletedByScopeRef,
		&taskID,
	); err != nil {
		return nil, err
	}
	v := &WrkqCommentCatView{
		UUID: commentUUID, ID: commentID, TaskUUID: taskUUID, TaskID: taskID,
		Body: body, Etag: etag, CreatedAt: createdAt,
		CreatedByPrincipalRef: nsPtr(createdByPrincipalRef), CreatedByScopeRef: nsPtr(createdByScopeRef),
		UpdatedAt: nsPtr(updatedAt), DeletedAt: nsPtr(deletedAt),
		DeletedByPrincipalRef: nsPtr(deletedByPrincipalRef),
		DeletedByScopeRef:     nsPtr(deletedByScopeRef),
	}
	if meta.Valid && meta.String != "" {
		m := meta.String
		v.Meta = &m
	}
	v.kind = nsPtr(kind)
	return v, nil
}

func nsPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}
