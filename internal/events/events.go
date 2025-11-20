package events

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/domain"
)

// Writer handles writing events to the event log
type Writer struct {
	db *sql.DB
}

// NewWriter creates a new event writer
func NewWriter(db *sql.DB) *Writer {
	return &Writer{db: db}
}

// LogEvent writes an event to the event log
func (w *Writer) LogEvent(tx *sql.Tx, event *domain.Event) error {
	query := `
		INSERT INTO event_log (actor_uuid, resource_type, resource_uuid, event_type, etag, payload)
		VALUES (?, ?, ?, ?, ?, ?)
	`

	executor := w.getExecutor(tx)
	_, err := executor.Exec(query, event.ActorUUID, event.ResourceType, event.ResourceUUID, event.EventType, event.ETag, event.Payload)
	if err != nil {
		return fmt.Errorf("failed to write event: %w", err)
	}

	return nil
}

// LogTaskCreated logs a task creation event
func (w *Writer) LogTaskCreated(tx *sql.Tx, actorUUID string, task *domain.Task) error {
	payload, err := json.Marshal(map[string]interface{}{
		"slug":  task.Slug,
		"title": task.Title,
		"state": task.State,
	})
	if err != nil {
		return err
	}

	payloadStr := string(payload)
	event := &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "task",
		ResourceUUID: &task.UUID,
		EventType:    "task.created",
		ETag:         &task.ETag,
		Payload:      &payloadStr,
	}

	return w.LogEvent(tx, event)
}

// LogTaskUpdated logs a task update event
func (w *Writer) LogTaskUpdated(tx *sql.Tx, actorUUID string, task *domain.Task, changes map[string]interface{}) error {
	payload, err := json.Marshal(changes)
	if err != nil {
		return err
	}

	payloadStr := string(payload)
	event := &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "task",
		ResourceUUID: &task.UUID,
		EventType:    "task.updated",
		ETag:         &task.ETag,
		Payload:      &payloadStr,
	}

	return w.LogEvent(tx, event)
}

// LogTaskDeleted logs a task deletion event
func (w *Writer) LogTaskDeleted(tx *sql.Tx, actorUUID string, taskUUID string) error {
	event := &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "task",
		ResourceUUID: &taskUUID,
		EventType:    "task.deleted",
	}

	return w.LogEvent(tx, event)
}

// LogContainerCreated logs a container creation event
func (w *Writer) LogContainerCreated(tx *sql.Tx, actorUUID string, container *domain.Container) error {
	payload, err := json.Marshal(map[string]interface{}{
		"slug":  container.Slug,
		"title": container.Title,
	})
	if err != nil {
		return err
	}

	payloadStr := string(payload)
	event := &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "container",
		ResourceUUID: &container.UUID,
		EventType:    "container.created",
		ETag:         &container.ETag,
		Payload:      &payloadStr,
	}

	return w.LogEvent(tx, event)
}

// LogContainerUpdated logs a container update event
func (w *Writer) LogContainerUpdated(tx *sql.Tx, actorUUID string, container *domain.Container, changes map[string]interface{}) error {
	payload, err := json.Marshal(changes)
	if err != nil {
		return err
	}

	payloadStr := string(payload)
	event := &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "container",
		ResourceUUID: &container.UUID,
		EventType:    "container.updated",
		ETag:         &container.ETag,
		Payload:      &payloadStr,
	}

	return w.LogEvent(tx, event)
}

// LogContainerDeleted logs a container deletion event
func (w *Writer) LogContainerDeleted(tx *sql.Tx, actorUUID string, containerUUID string) error {
	event := &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "container",
		ResourceUUID: &containerUUID,
		EventType:    "container.deleted",
	}

	return w.LogEvent(tx, event)
}

// LogCommentCreated logs a comment creation event
func (w *Writer) LogCommentCreated(tx *sql.Tx, actorUUID string, comment *domain.Comment) error {
	payload, err := json.Marshal(map[string]interface{}{
		"task_id":    comment.TaskUUID,
		"comment_id": comment.ID,
		"actor_id":   comment.ActorUUID,
	})
	if err != nil {
		return err
	}

	payloadStr := string(payload)
	event := &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "comment",
		ResourceUUID: &comment.UUID,
		EventType:    "comment.created",
		ETag:         &comment.ETag,
		Payload:      &payloadStr,
	}

	return w.LogEvent(tx, event)
}

// LogCommentDeleted logs a comment soft-delete event
func (w *Writer) LogCommentDeleted(tx *sql.Tx, actorUUID string, comment *domain.Comment) error {
	payload, err := json.Marshal(map[string]interface{}{
		"task_id":              comment.TaskUUID,
		"comment_id":           comment.ID,
		"deleted_by_actor_id":  actorUUID,
		"soft_delete":          true,
	})
	if err != nil {
		return err
	}

	payloadStr := string(payload)
	event := &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "comment",
		ResourceUUID: &comment.UUID,
		EventType:    "comment.deleted",
		ETag:         &comment.ETag,
		Payload:      &payloadStr,
	}

	return w.LogEvent(tx, event)
}

// LogCommentPurged logs a comment hard-delete event
func (w *Writer) LogCommentPurged(tx *sql.Tx, actorUUID string, commentUUID string, commentID string, taskUUID string) error {
	payload, err := json.Marshal(map[string]interface{}{
		"task_id":    taskUUID,
		"comment_id": commentID,
		"hard_delete": true,
	})
	if err != nil {
		return err
	}

	payloadStr := string(payload)
	event := &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "comment",
		ResourceUUID: &commentUUID,
		EventType:    "comment.purged",
		Payload:      &payloadStr,
	}

	return w.LogEvent(tx, event)
}

// getExecutor returns the appropriate executor (tx or db)
func (w *Writer) getExecutor(tx *sql.Tx) interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
} {
	if tx != nil {
		return tx
	}
	return w.db
}
