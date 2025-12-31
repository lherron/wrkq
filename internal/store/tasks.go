package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
)

// TaskStore handles task persistence operations.
type TaskStore struct {
	store *Store
}

// CreateParams contains parameters for creating a new task.
type CreateParams struct {
	UUID              string  // optional: force specific UUID instead of auto-generating
	Slug              string
	Title             string
	Description       string
	ProjectUUID       string
	State             string
	Priority          int
	Kind              string  // task, subtask, spike, bug, chore - defaults to "task"
	ParentTaskUUID    *string // for subtasks
	AssigneeActorUUID *string // task assignment
	Labels            string  // JSON array
	DueAt             string
	StartAt           string
}

// CreateResult contains the result of task creation.
type CreateResult struct {
	UUID string
	ID   string
	ETag int64
}

// Create creates a new task and logs a task.created event.
func (ts *TaskStore) Create(actorUUID string, params CreateParams) (*CreateResult, error) {
	var result *CreateResult

	// Default kind to "task" if not provided
	kind := params.Kind
	if kind == "" {
		kind = "task"
	}

	err := ts.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		var query string
		var args []interface{}

		if params.UUID != "" {
			// Force specific UUID
			query = `
				INSERT INTO tasks (
					uuid, id, slug, title, description, project_uuid, state, priority, kind,
					parent_task_uuid, assignee_actor_uuid, labels, due_at, start_at,
					created_by_actor_uuid, updated_by_actor_uuid
				)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`
			args = []interface{}{
				params.UUID,              // uuid (forced)
				"",                       // id (auto-generated)
				params.Slug,              // slug
				params.Title,             // title
				params.Description,       // description
				params.ProjectUUID,       // project_uuid
				params.State,             // state
				params.Priority,          // priority
				kind,                     // kind
				params.ParentTaskUUID,    // parent_task_uuid
				params.AssigneeActorUUID, // assignee_actor_uuid
				params.Labels,            // labels (can be empty string or JSON)
				params.DueAt,             // due_at (can be empty string)
				params.StartAt,           // start_at (can be empty string)
				actorUUID,                // created_by_actor_uuid
				actorUUID,                // updated_by_actor_uuid
			}
		} else {
			// Auto-generate UUID
			query = `
				INSERT INTO tasks (
					id, slug, title, description, project_uuid, state, priority, kind,
					parent_task_uuid, assignee_actor_uuid, labels, due_at, start_at,
					created_by_actor_uuid, updated_by_actor_uuid
				)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`
			args = []interface{}{
				"",                       // id (auto-generated)
				params.Slug,              // slug
				params.Title,             // title
				params.Description,       // description
				params.ProjectUUID,       // project_uuid
				params.State,             // state
				params.Priority,          // priority
				kind,                     // kind
				params.ParentTaskUUID,    // parent_task_uuid
				params.AssigneeActorUUID, // assignee_actor_uuid
				params.Labels,            // labels (can be empty string or JSON)
				params.DueAt,             // due_at (can be empty string)
				params.StartAt,           // start_at (can be empty string)
				actorUUID,                // created_by_actor_uuid
				actorUUID,                // updated_by_actor_uuid
			}
		}

		res, err := tx.Exec(query, args...)
		if err != nil {
			return fmt.Errorf("failed to create task: %w", err)
		}

		// Get the UUID and ID of the created task
		rowID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("failed to get last insert ID: %w", err)
		}

		var uuid, id string
		var etag int64
		err = tx.QueryRow("SELECT uuid, id, etag FROM tasks WHERE rowid = ?", rowID).Scan(&uuid, &id, &etag)
		if err != nil {
			return fmt.Errorf("failed to get task UUID: %w", err)
		}

		// Log event with structured payload
		payload := map[string]interface{}{
			"slug":     params.Slug,
			"title":    params.Title,
			"state":    params.State,
			"priority": params.Priority,
			"kind":     kind,
		}
		if params.ParentTaskUUID != nil {
			payload["parent_task_uuid"] = *params.ParentTaskUUID
		}
		if params.AssigneeActorUUID != nil {
			payload["assignee_actor_uuid"] = *params.AssigneeActorUUID
		}
		if params.Labels != "" {
			payload["labels"] = params.Labels
		}
		if params.DueAt != "" {
			payload["due_at"] = params.DueAt
		}
		if params.StartAt != "" {
			payload["start_at"] = params.StartAt
		}

		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal event payload: %w", err)
		}
		payloadStr := string(payloadJSON)

		if err := ew.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "task",
			ResourceUUID: &uuid,
			EventType:    "task.created",
			ETag:         &etag,
			Payload:      &payloadStr,
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		result = &CreateResult{
			UUID: uuid,
			ID:   id,
			ETag: etag,
		}
		return nil
	})

	return result, err
}

// UpdateFields updates specified fields on a task and logs a task.updated event.
// Returns the new etag on success.
func (ts *TaskStore) UpdateFields(actorUUID, taskUUID string, fields map[string]interface{}, ifMatch int64) (int64, error) {
	var newETag int64

	err := ts.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current etag
		var currentETag int64
		err := tx.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentETag)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("task not found: %s", taskUUID)
			}
			return fmt.Errorf("failed to get current etag: %w", err)
		}

		// Check etag if ifMatch was provided
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}

		// Build UPDATE query
		var setClauses []string
		var args []interface{}

		for key, value := range fields {
			setClauses = append(setClauses, fmt.Sprintf("%s = ?", key))
			args = append(args, value)
		}

		// Increment etag and update actor
		setClauses = append(setClauses, "etag = etag + 1")
		setClauses = append(setClauses, "updated_by_actor_uuid = ?")
		args = append(args, actorUUID)

		// Add WHERE clause
		args = append(args, taskUUID)

		query := fmt.Sprintf("UPDATE tasks SET %s WHERE uuid = ?", strings.Join(setClauses, ", "))
		_, err = tx.Exec(query, args...)
		if err != nil {
			return fmt.Errorf("failed to update task: %w", err)
		}

		// Cascade delete subtasks if state is being set to 'deleted'
		if newState, ok := fields["state"]; ok && newState == "deleted" {
			if err := cascadeDeleteSubtasks(tx, ew, actorUUID, taskUUID); err != nil {
				return fmt.Errorf("failed to cascade delete subtasks: %w", err)
			}
		}

		// Log event with structured payload
		changesJSON, err := json.Marshal(fields)
		if err != nil {
			return fmt.Errorf("failed to marshal changes: %w", err)
		}
		changesStr := string(changesJSON)
		newETag = currentETag + 1

		if err := ew.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "task",
			ResourceUUID: &taskUUID,
			EventType:    "task.updated",
			ETag:         &newETag,
			Payload:      &changesStr,
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		return nil
	})

	return newETag, err
}

// Move moves a task to a different container and logs a task.updated event.
// Returns the new etag on success.
func (ts *TaskStore) Move(actorUUID, taskUUID, newProjectUUID string, ifMatch int64) (int64, error) {
	var newETag int64

	err := ts.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current state
		var currentETag int64
		var oldProjectUUID string
		err := tx.QueryRow("SELECT etag, project_uuid FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentETag, &oldProjectUUID)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("task not found: %s", taskUUID)
			}
			return fmt.Errorf("failed to get task: %w", err)
		}

		// Check etag if ifMatch was provided
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}

		// Update the task
		_, err = tx.Exec(`
			UPDATE tasks
			SET project_uuid = ?,
				etag = etag + 1,
				updated_by_actor_uuid = ?
			WHERE uuid = ?
		`, newProjectUUID, actorUUID, taskUUID)
		if err != nil {
			return fmt.Errorf("failed to move task: %w", err)
		}

		// Log event with structured payload
		payload := map[string]interface{}{
			"old_project_uuid": oldProjectUUID,
			"new_project_uuid": newProjectUUID,
		}
		payloadJSON, _ := json.Marshal(payload)
		payloadStr := string(payloadJSON)
		newETag = currentETag + 1

		if err := ew.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "task",
			ResourceUUID: &taskUUID,
			EventType:    "task.moved",
			ETag:         &newETag,
			Payload:      &payloadStr,
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		return nil
	})

	return newETag, err
}

// ArchiveResult contains statistics about an archive operation.
type ArchiveResult struct {
	ETag int64
}

// Archive soft-deletes a task by setting state to 'archived' and archived_at timestamp.
func (ts *TaskStore) Archive(actorUUID, taskUUID string, ifMatch int64) (*ArchiveResult, error) {
	var result *ArchiveResult

	err := ts.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current state
		var currentETag int64
		var slug string
		err := tx.QueryRow("SELECT etag, slug FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentETag, &slug)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("task not found: %s", taskUUID)
			}
			return fmt.Errorf("failed to get task: %w", err)
		}

		// Check etag if ifMatch was provided
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}

		// Soft delete
		_, err = tx.Exec(`
			UPDATE tasks
			SET state = 'archived',
				archived_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
				updated_by_actor_uuid = ?,
				etag = etag + 1
			WHERE uuid = ?
		`, actorUUID, taskUUID)
		if err != nil {
			return fmt.Errorf("failed to archive task: %w", err)
		}

		// Log event
		payload := map[string]interface{}{
			"slug":        slug,
			"soft_delete": true,
		}
		payloadJSON, _ := json.Marshal(payload)
		payloadStr := string(payloadJSON)
		newETag := currentETag + 1

		if err := ew.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "task",
			ResourceUUID: &taskUUID,
			EventType:    "task.archived",
			ETag:         &newETag,
			Payload:      &payloadStr,
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		result = &ArchiveResult{ETag: newETag}
		return nil
	})

	return result, err
}

// PurgeResult contains statistics about a purge operation.
type PurgeResult struct {
	AttachmentsDeleted int
	BytesFreed         int64
}

// Purge hard-deletes a task. The caller must handle attachment file cleanup.
// Returns the purge result including attachment statistics.
func (ts *TaskStore) Purge(actorUUID, taskUUID string, ifMatch int64) (*PurgeResult, error) {
	var result *PurgeResult

	err := ts.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current state
		var currentETag int64
		var slug string
		err := tx.QueryRow("SELECT etag, slug FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentETag, &slug)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("task not found: %s", taskUUID)
			}
			return fmt.Errorf("failed to get task: %w", err)
		}

		// Check etag if ifMatch was provided
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}

		// Count attachments for statistics
		var attachmentCount int
		var totalBytes int64
		rows, err := tx.Query("SELECT size_bytes FROM attachments WHERE task_uuid = ?", taskUUID)
		if err != nil {
			return fmt.Errorf("failed to query attachments: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var size int64
			if err := rows.Scan(&size); err != nil {
				return fmt.Errorf("failed to scan attachment: %w", err)
			}
			attachmentCount++
			totalBytes += size
		}

		// Log event BEFORE deleting (so we can still reference the task)
		payload := map[string]interface{}{
			"slug":      slug,
			"purged_by": actorUUID,
		}
		if attachmentCount > 0 {
			payload["attachment_count"] = attachmentCount
			payload["bytes_freed"] = totalBytes
		}
		payloadJSON, _ := json.Marshal(payload)
		payloadStr := string(payloadJSON)

		if err := ew.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "task",
			ResourceUUID: &taskUUID,
			EventType:    "task.purged",
			Payload:      &payloadStr,
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		// Hard delete (CASCADE will delete attachments and comments)
		_, err = tx.Exec("DELETE FROM tasks WHERE uuid = ?", taskUUID)
		if err != nil {
			return fmt.Errorf("failed to delete task: %w", err)
		}

		result = &PurgeResult{
			AttachmentsDeleted: attachmentCount,
			BytesFreed:         totalBytes,
		}
		return nil
	})

	return result, err
}

// AttachmentInfo contains info about an attachment for file cleanup.
type AttachmentInfo struct {
	RelativePath string
	SizeBytes    int64
}

// GetAttachments returns all attachments for a task.
func (ts *TaskStore) GetAttachments(taskUUID string) ([]AttachmentInfo, error) {
	rows, err := ts.store.db.Query("SELECT relative_path, size_bytes FROM attachments WHERE task_uuid = ?", taskUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query attachments: %w", err)
	}
	defer rows.Close()

	var attachments []AttachmentInfo
	for rows.Next() {
		var a AttachmentInfo
		if err := rows.Scan(&a.RelativePath, &a.SizeBytes); err != nil {
			return nil, fmt.Errorf("failed to scan attachment: %w", err)
		}
		attachments = append(attachments, a)
	}
	return attachments, nil
}

// GetByUUID retrieves a task by UUID.
func (ts *TaskStore) GetByUUID(uuid string) (*domain.Task, error) {
	task := &domain.Task{}
	// Use string intermediates for nullable time fields since SQLite stores times as strings
	var startAt, dueAt, labels, completedAt, archivedAt *string
	var createdAt, updatedAt string

	err := ts.store.db.QueryRow(`
		SELECT uuid, id, slug, title, project_uuid, state, priority,
			   start_at, due_at, labels, description, etag,
			   created_at, updated_at, completed_at, archived_at,
			   created_by_actor_uuid, updated_by_actor_uuid
		FROM tasks WHERE uuid = ?
	`, uuid).Scan(
		&task.UUID, &task.ID, &task.Slug, &task.Title, &task.ProjectUUID,
		&task.State, &task.Priority, &startAt, &dueAt,
		&labels, &task.Description, &task.ETag,
		&createdAt, &updatedAt, &completedAt, &archivedAt,
		&task.CreatedByActorUUID, &task.UpdatedByActorUUID,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("task not found: %s", uuid)
		}
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	// Store the labels as-is since it's a JSON string
	task.Labels = labels

	return task, nil
}

// cascadeDeleteSubtasks deletes all subtasks when a parent task is deleted.
// This is called within a transaction when a task's state is set to 'deleted'.
func cascadeDeleteSubtasks(tx *sql.Tx, ew *events.Writer, actorUUID, parentTaskUUID string) error {
	// Find all subtasks (not already deleted)
	rows, err := tx.Query(`
		SELECT uuid FROM tasks
		WHERE parent_task_uuid = ? AND state != 'deleted'
	`, parentTaskUUID)
	if err != nil {
		return fmt.Errorf("failed to query subtasks: %w", err)
	}
	defer rows.Close()

	var subtaskUUIDs []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return fmt.Errorf("failed to scan subtask: %w", err)
		}
		subtaskUUIDs = append(subtaskUUIDs, uuid)
	}
	rows.Close()

	// Delete each subtask
	for _, subtaskUUID := range subtaskUUIDs {
		_, err := tx.Exec(`
			UPDATE tasks
			SET state = 'deleted',
			    updated_by_actor_uuid = ?
			WHERE uuid = ?
		`, actorUUID, subtaskUUID)
		if err != nil {
			return fmt.Errorf("failed to delete subtask %s: %w", subtaskUUID, err)
		}

		// Log event
		payload := `{"action":"cascade_deleted","parent_deleted":true}`
		if err := ew.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "task",
			ResourceUUID: &subtaskUUID,
			EventType:    "task.deleted",
			Payload:      &payload,
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		// Recursively delete nested subtasks
		if err := cascadeDeleteSubtasks(tx, ew, actorUUID, subtaskUUID); err != nil {
			return err
		}
	}

	return nil
}
