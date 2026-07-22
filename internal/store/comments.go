package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/id"
)

// CommentStore handles comment persistence operations.
type CommentStore struct {
	store *Store
}

// CommentCreateParams contains parameters for creating a comment.
type CommentCreateParams struct {
	TaskUUID string
	Body     string
	Meta     *string
}

// CommentCreateResult contains the durable comment and event identities.
type CommentCreateResult struct {
	UUID      string
	ID        string
	ETag      int64
	EventMeta events.EventMetadata
}

// CreateWithAttribution creates a comment and its canonical event atomically.
func (cs *CommentStore) CreateWithAttribution(attr attribution.Attribution, params CommentCreateParams) (*CommentCreateResult, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}

	var result *CommentCreateResult
	err := cs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		var err error
		result, err = cs.CreateTxWithAttribution(tx, ew, attr, params)
		return err
	})
	return result, err
}

// CreateTxWithAttribution creates a comment and comment.created event in the
// caller's transaction. The caller owns commit and any post-commit webhook.
func (cs *CommentStore) CreateTxWithAttribution(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, params CommentCreateParams) (*CommentCreateResult, error) {
	if tx == nil || ew == nil {
		return nil, fmt.Errorf("comment transaction and event writer are required")
	}
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}

	var nextSeq int
	if err := tx.QueryRow(
		"SELECT COALESCE(MAX(CAST(SUBSTR(id, 3) AS INTEGER)), 0) + 1 FROM comments",
	).Scan(&nextSeq); err != nil {
		return nil, fmt.Errorf("failed to allocate comment id: %w", err)
	}
	if _, err := tx.Exec("UPDATE comment_sequences SET value = ? WHERE name = 'next_comment'", nextSeq); err != nil {
		return nil, fmt.Errorf("failed to update comment sequence: %w", err)
	}

	commentUUID := uuid.New().String()
	commentID := id.FormatComment(nextSeq)
	const etag int64 = 1
	if _, err := tx.Exec(`
		INSERT INTO comments (
			uuid, id, task_uuid, created_by_principal_ref,
			created_by_scope_ref, body, meta, etag
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, commentUUID, commentID, params.TaskUUID, attr.PrincipalRef,
		scopeSQL(attr), params.Body, params.Meta, etag); err != nil {
		return nil, fmt.Errorf("failed to create comment: %w", err)
	}

	payload, err := commentCreatedEventPayload(params.TaskUUID, commentID)
	if err != nil {
		return nil, err
	}
	eventMeta, err := ew.LogEventReturning(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "comment",
		ResourceUUID: &commentUUID,
		EventType:    "comment.created",
		ETag:         int64Ptr(etag),
		Payload:      &payload,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to log comment.created event: %w", err)
	}

	return &CommentCreateResult{
		UUID:      commentUUID,
		ID:        commentID,
		ETag:      etag,
		EventMeta: eventMeta,
	}, nil
}

// commentCreatedEventPayload is the single construction point for the
// canonical event payload. Later payload stamps belong here.
func commentCreatedEventPayload(taskUUID, commentID string) (string, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"task_id":    taskUUID,
		"comment_id": commentID,
	})
	if err != nil {
		return "", fmt.Errorf("failed to marshal comment.created payload: %w", err)
	}
	return string(payload), nil
}

func int64Ptr(value int64) *int64 {
	return &value
}
