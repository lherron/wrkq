package wrkqd

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/webhooks"
)

type commentsListRequest struct {
	Task           string `json:"task"`
	IncludeDeleted bool   `json:"include_deleted,omitempty"`
}

func (s *daemonServer) handleCommentsList(w http.ResponseWriter, r *http.Request) {
	var req commentsListRequest
	if !s.decodePost(w, r, &req) {
		return
	}
	if req.Task == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("task required"))
		return
	}

	taskUUID, ok := s.resolveTask(w, req.Task)
	if !ok {
		return
	}

	query := `
		SELECT c.uuid, c.id, c.task_uuid, c.body, c.meta, c.etag,
		       c.created_at, c.updated_at, c.deleted_at,
		       c.created_by_principal_ref, c.created_by_scope_ref,
		       c.created_by_host_session_id, c.created_by_generation,
		       c.deleted_by_principal_ref, c.deleted_by_scope_ref,
		       t.id as task_id
		FROM comments c
		LEFT JOIN tasks t ON c.task_uuid = t.uuid
		WHERE c.task_uuid = ?
	`
	args := []interface{}{taskUUID}
	if !req.IncludeDeleted {
		query += " AND c.deleted_at IS NULL"
	}
	query += " ORDER BY c.created_at ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	defer func() { _ = rows.Close() }()

	var comments []map[string]interface{}
	for rows.Next() {
		var uuid, id, taskUUID, body, createdAt string
		var taskIDStr string
		var meta, updatedAt, deletedAt sql.NullString
		var createdByPrincipalRef, createdByScopeRef, hostSessionID sql.NullString
		var generation sql.NullInt64
		var deletedByPrincipalRef, deletedByScopeRef sql.NullString
		var etag int64

		if err := rows.Scan(&uuid, &id, &taskUUID, &body, &meta, &etag,
			&createdAt, &updatedAt, &deletedAt,
			&createdByPrincipalRef, &createdByScopeRef, &hostSessionID, &generation,
			&deletedByPrincipalRef, &deletedByScopeRef,
			&taskIDStr); err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}

		comment := map[string]interface{}{
			"uuid":       uuid,
			"id":         id,
			"task_uuid":  taskUUID,
			"task_id":    taskIDStr,
			"body":       body,
			"etag":       etag,
			"created_at": createdAt,
		}
		if createdByPrincipalRef.Valid {
			comment["created_by_principal_ref"] = createdByPrincipalRef.String
		}
		if createdByScopeRef.Valid {
			comment["created_by_scope_ref"] = createdByScopeRef.String
		}
		if hostSessionID.Valid {
			comment["created_by_host_session_id"] = hostSessionID.String
		}
		if generation.Valid {
			comment["created_by_generation"] = generation.Int64
		}

		if meta.Valid && meta.String != "" {
			comment["meta"] = meta.String
		}
		if updatedAt.Valid {
			comment["updated_at"] = updatedAt.String
		}
		if deletedAt.Valid {
			comment["deleted_at"] = deletedAt.String
		}
		if deletedByPrincipalRef.Valid {
			comment["deleted_by_principal_ref"] = deletedByPrincipalRef.String
		}
		if deletedByScopeRef.Valid {
			comment["deleted_by_scope_ref"] = deletedByScopeRef.String
		}

		comments = append(comments, comment)
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"comments": comments,
	})
}

type commentsCreateRequest struct {
	Task    string                 `json:"task"`
	Body    string                 `json:"body"`
	Meta    map[string]interface{} `json:"meta,omitempty"`
	IfMatch int64                  `json:"ifMatch,omitempty"`
}

func (s *daemonServer) handleCommentsCreate(w http.ResponseWriter, r *http.Request) {
	var req commentsCreateRequest
	if !s.decodePost(w, r, &req) {
		return
	}

	if req.Task == "" || strings.TrimSpace(req.Body) == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("task and body required"))
		return
	}

	attr, err := s.resolveAttribution(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	taskUUID, ok := s.resolveTask(w, req.Task)
	if !ok {
		return
	}

	metaStr := ""
	if req.Meta != nil {
		if data, err := json.Marshal(req.Meta); err == nil {
			metaStr = string(data)
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if req.IfMatch > 0 {
		var currentEtag int64
		if err := tx.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentEtag); err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		if currentEtag != req.IfMatch {
			s.writeError(w, http.StatusConflict, fmt.Errorf("etag mismatch: task has etag %d, expected %d", currentEtag, req.IfMatch))
			return
		}
	}

	var nextSeq int
	if err := tx.QueryRow("SELECT COALESCE(MAX(CAST(SUBSTR(id, 3) AS INTEGER)), 0) + 1 FROM comments").Scan(&nextSeq); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if _, err := tx.Exec("UPDATE comment_sequences SET value = ? WHERE name = 'next_comment'", nextSeq); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	commentUUID := uuid.New().String()
	commentID := fmt.Sprintf("C-%05d", nextSeq)

	var metaPtr *string
	if metaStr != "" {
		metaPtr = &metaStr
	}

	if _, err := tx.Exec(`
		INSERT INTO comments (
			uuid, id, task_uuid, created_by_principal_ref, created_by_scope_ref,
			body, meta, etag
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1)
	`, commentUUID, commentID, taskUUID, attr.PrincipalRef, scopeBind(attr), strings.TrimSpace(req.Body), metaPtr); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	var comment domain.Comment
	var createdAtStr string
	var createdByPrincipalRef, createdByScopeRef sql.NullString
	if err := tx.QueryRow(`
		SELECT uuid, id, task_uuid, created_by_principal_ref, created_by_scope_ref,
		       body, meta, etag, created_at
		FROM comments WHERE uuid = ?
	`, commentUUID).Scan(
		&comment.UUID, &comment.ID, &comment.TaskUUID,
		&createdByPrincipalRef, &createdByScopeRef,
		&comment.Body, &comment.Meta, &comment.ETag, &createdAtStr,
	); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	if createdByPrincipalRef.Valid {
		comment.CreatedByPrincipalRef = createdByPrincipalRef.String
	}
	if createdByScopeRef.Valid {
		comment.CreatedByScopeRef = createdByScopeRef.String
	}
	if parsedCreatedAt, err := parseDaemonTimestamp(createdAtStr); err == nil {
		comment.CreatedAt = parsedCreatedAt
	}

	payloadJSON, _ := json.Marshal(map[string]interface{}{
		"task_id":       comment.TaskUUID,
		"comment_id":    comment.ID,
		"principal_ref": attr.PrincipalRef,
	})
	payload := string(payloadJSON)
	eventMeta, err := events.NewWriter(s.db.DB).LogEventReturning(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "comment",
		ResourceUUID: &comment.UUID,
		EventType:    "comment.created",
		ETag:         &comment.ETag,
		Payload:      &payload,
	})
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := tx.Commit(); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	webhooks.DispatchTaskEvent(s.db, taskUUID, webhooks.EventContext{
		Metadata:     eventMeta,
		Event:        "comment_added",
		PrincipalRef: attr.PrincipalRef,
		Via:          "api",
		Transition:   nil,
		Changed:      []string{"comments"},
		Changes: map[string]webhooks.Change{
			"comments": {From: nil, To: commentID},
		},
	})

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"comment": comment,
	})
}

func parseDaemonTimestamp(value string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, format := range formats {
		if parsed, err := time.Parse(format, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp format: %s", value)
}
