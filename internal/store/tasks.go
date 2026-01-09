package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/webhooks"
)

// TaskStore handles task persistence operations.
type TaskStore struct {
	store *Store
}

// CreateParams contains parameters for creating a new task.
type CreateParams struct {
	UUID                 string // optional: force specific UUID instead of auto-generating
	Slug                 string
	Title                string
	Description          string
	ProjectUUID          string
	State                string
	Priority             int
	Kind                 string  // task, subtask, spike, bug, chore - defaults to "task"
	ParentTaskUUID       *string // for subtasks
	AssigneeActorUUID    *string // task assignment
	RequestedByProjectID *string
	AssignedProjectID    *string
	Resolution           *string
	Labels               string  // JSON array
	Meta                 *string // JSON object
	DueAt                string
	StartAt              string
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
					parent_task_uuid, assignee_actor_uuid, requested_by_project_id, assigned_project_id, resolution,
					labels, meta, due_at, start_at,
					created_by_actor_uuid, updated_by_actor_uuid
				)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`
			args = []interface{}{
				params.UUID,                 // uuid (forced)
				"",                          // id (auto-generated)
				params.Slug,                 // slug
				params.Title,                // title
				params.Description,          // description
				params.ProjectUUID,          // project_uuid
				params.State,                // state
				params.Priority,             // priority
				kind,                        // kind
				params.ParentTaskUUID,       // parent_task_uuid
				params.AssigneeActorUUID,    // assignee_actor_uuid
				params.RequestedByProjectID, // requested_by_project_id
				params.AssignedProjectID,    // assigned_project_id
				params.Resolution,           // resolution
				params.Labels,               // labels (can be empty string or JSON)
				params.Meta,                 // meta (JSON object, nullable)
				params.DueAt,                // due_at (can be empty string)
				params.StartAt,              // start_at (can be empty string)
				actorUUID,                   // created_by_actor_uuid
				actorUUID,                   // updated_by_actor_uuid
			}
		} else {
			// Auto-generate UUID
			query = `
				INSERT INTO tasks (
					id, slug, title, description, project_uuid, state, priority, kind,
					parent_task_uuid, assignee_actor_uuid, requested_by_project_id, assigned_project_id, resolution,
					labels, meta, due_at, start_at,
					created_by_actor_uuid, updated_by_actor_uuid
				)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`
			args = []interface{}{
				"",                          // id (auto-generated)
				params.Slug,                 // slug
				params.Title,                // title
				params.Description,          // description
				params.ProjectUUID,          // project_uuid
				params.State,                // state
				params.Priority,             // priority
				kind,                        // kind
				params.ParentTaskUUID,       // parent_task_uuid
				params.AssigneeActorUUID,    // assignee_actor_uuid
				params.RequestedByProjectID, // requested_by_project_id
				params.AssignedProjectID,    // assigned_project_id
				params.Resolution,           // resolution
				params.Labels,               // labels (can be empty string or JSON)
				params.Meta,                 // meta (JSON object, nullable)
				params.DueAt,                // due_at (can be empty string)
				params.StartAt,              // start_at (can be empty string)
				actorUUID,                   // created_by_actor_uuid
				actorUUID,                   // updated_by_actor_uuid
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
		if params.RequestedByProjectID != nil {
			payload["requested_by_project_id"] = *params.RequestedByProjectID
		}
		if params.AssignedProjectID != nil {
			payload["assigned_project_id"] = *params.AssignedProjectID
		}
		if params.Resolution != nil {
			payload["resolution"] = *params.Resolution
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

	if err == nil && result != nil {
		webhooks.DispatchTask(ts.store.db, result.UUID)
	}

	return result, err
}

// UpdateFields updates specified fields on a task and logs a task.updated event.
// Returns the new etag on success.
func (ts *TaskStore) UpdateFields(actorUUID, taskUUID string, fields map[string]interface{}, ifMatch int64) (int64, error) {
	var newETag int64
	var unblockedTaskUUIDs []string

	err := ts.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current etag and state
		var currentETag int64
		var currentState string
		err := tx.QueryRow("SELECT etag, state FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentETag, &currentState)
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

		// Check if we're transitioning to a completion state (for unblock webhook logic)
		newState, hasStateChange := fields["state"].(string)
		transitioningToCompletion := hasStateChange && !isCompletionState(currentState) && isCompletionState(newState)

		// If transitioning to completion, find tasks that might become unblocked
		var potentiallyUnblockedUUIDs []string
		if transitioningToCompletion {
			rows, err := tx.Query(`
				SELECT to_task_uuid
				FROM task_relations
				WHERE from_task_uuid = ?
				  AND kind = 'blocks'
			`, taskUUID)
			if err != nil {
				return fmt.Errorf("failed to query blocked tasks: %w", err)
			}
			for rows.Next() {
				var uuid string
				if err := rows.Scan(&uuid); err != nil {
					rows.Close()
					return fmt.Errorf("failed to scan blocked task: %w", err)
				}
				potentiallyUnblockedUUIDs = append(potentiallyUnblockedUUIDs, uuid)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return fmt.Errorf("error iterating blocked tasks: %w", err)
			}
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

		// After state update, check which tasks are now fully unblocked
		// (all their blockers are in completion states)
		if transitioningToCompletion {
			for _, blockedUUID := range potentiallyUnblockedUUIDs {
				// Count remaining incomplete blockers for this task
				var incompleteBlockerCount int
				err := tx.QueryRow(`
					SELECT COUNT(*)
					FROM task_relations r
					JOIN tasks t ON r.from_task_uuid = t.uuid
					WHERE r.to_task_uuid = ?
					  AND r.kind = 'blocks'
					  AND t.state NOT IN ('completed', 'archived', 'deleted', 'cancelled', 'idea')
				`, blockedUUID).Scan(&incompleteBlockerCount)
				if err != nil {
					return fmt.Errorf("failed to count blockers for task %s: %w", blockedUUID, err)
				}

				// If no more incomplete blockers, this task is now unblocked
				if incompleteBlockerCount == 0 {
					unblockedTaskUUIDs = append(unblockedTaskUUIDs, blockedUUID)
				}
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

	if err == nil {
		// Dispatch webhook for the updated task
		webhooks.DispatchTask(ts.store.db, taskUUID)

		// Dispatch webhooks for newly unblocked tasks
		for _, unblockedUUID := range unblockedTaskUUIDs {
			webhooks.DispatchTask(ts.store.db, unblockedUUID)
		}
	}

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

	if err == nil {
		webhooks.DispatchTask(ts.store.db, taskUUID)
	}

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

	if err == nil && result != nil {
		webhooks.DispatchTask(ts.store.db, taskUUID)
	}

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
	var webhookInfo *webhooks.TaskInfo

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

		// Capture webhook payload info before deletion
		var info webhooks.TaskInfo
		if err := tx.QueryRow(`
			SELECT t.id, t.project_uuid, c.id
			FROM tasks t
			JOIN containers c ON c.uuid = t.project_uuid
			WHERE t.uuid = ?
		`, taskUUID).Scan(&info.TaskID, &info.ProjectUUID, &info.ProjectID); err != nil {
			return fmt.Errorf("failed to load webhook info: %w", err)
		}
		webhookInfo = &info

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

	if err == nil && webhookInfo != nil {
		webhooks.DispatchTaskInfo(ts.store.db, *webhookInfo)
	}

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
	var startAt, dueAt, labels, meta, completedAt, archivedAt *string
	var requestedByProjectID, assignedProjectID, acknowledgedAt, resolution *string
	var cpProjectID, cpRunID, cpSessionID, sdkSessionID, runStatus *string
	var createdAt, updatedAt string

	err := ts.store.db.QueryRow(`
		SELECT uuid, id, slug, title, project_uuid, requested_by_project_id, assigned_project_id,
			   state, priority,
			   start_at, due_at, labels, meta, description, etag,
			   created_at, updated_at, completed_at, archived_at,
			   acknowledged_at, resolution,
			   cp_project_id, cp_run_id, cp_session_id, sdk_session_id, run_status,
			   created_by_actor_uuid, updated_by_actor_uuid
		FROM tasks WHERE uuid = ?
	`, uuid).Scan(
		&task.UUID, &task.ID, &task.Slug, &task.Title, &task.ProjectUUID,
		&requestedByProjectID, &assignedProjectID, &task.State, &task.Priority,
		&startAt, &dueAt, &labels, &meta, &task.Description, &task.ETag,
		&createdAt, &updatedAt, &completedAt, &archivedAt,
		&acknowledgedAt, &resolution,
		&cpProjectID, &cpRunID, &cpSessionID, &sdkSessionID, &runStatus,
		&task.CreatedByActorUUID, &task.UpdatedByActorUUID,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("task not found: %s", uuid)
		}
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	task.RequestedByProjectID = requestedByProjectID
	task.AssignedProjectID = assignedProjectID
	task.Resolution = resolution
	task.AcknowledgedAt = parseTimeNullable(acknowledgedAt)
	task.CPProjectID = cpProjectID
	task.CPRunID = cpRunID
	task.CPSessionID = cpSessionID
	task.SDKSessionID = sdkSessionID
	task.RunStatus = runStatus

	// Store the labels as-is since it's a JSON string
	task.Labels = labels
	task.Meta = meta

	return task, nil
}

func parseTimeNullable(value *string) *time.Time {
	if value == nil || *value == "" {
		return nil
	}
	layouts := []string{time.RFC3339, "2006-01-02 15:04:05"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, *value); err == nil {
			return &t
		}
	}
	return nil
}

// BlockingTask represents a lightweight view of a task that is blocking another task.
// Used by BlockedBy to return only essential info for dependency checking.
type BlockingTask struct {
	UUID  string `json:"uuid"`
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	State string `json:"state"`
}

// BlockedBy returns all incomplete tasks that are blocking the given task.
// A task is considered "blocking" if there is a 'blocks' relation where
// the blocking task is the source (from_task_uuid) and the given task is the target (to_task_uuid).
// A task is considered "incomplete" if its state is NOT in: completed, archived, deleted, cancelled.
// Tasks in 'idea' state are also excluded as they represent uncommitted work.
func (ts *TaskStore) BlockedBy(taskUUID string) ([]BlockingTask, error) {
	rows, err := ts.store.db.Query(`
		SELECT t.uuid, t.id, t.slug, t.title, t.state
		FROM task_relations r
		JOIN tasks t ON r.from_task_uuid = t.uuid
		WHERE r.to_task_uuid = ?
		  AND r.kind = 'blocks'
		  AND t.state NOT IN ('completed', 'archived', 'deleted', 'cancelled', 'idea')
		ORDER BY t.id
	`, taskUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query blocking tasks: %w", err)
	}
	defer rows.Close()

	var blockers []BlockingTask
	for rows.Next() {
		var b BlockingTask
		if err := rows.Scan(&b.UUID, &b.ID, &b.Slug, &b.Title, &b.State); err != nil {
			return nil, fmt.Errorf("failed to scan blocking task: %w", err)
		}
		blockers = append(blockers, b)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating blocking tasks: %w", err)
	}

	// Return empty slice instead of nil for consistency
	if blockers == nil {
		blockers = []BlockingTask{}
	}

	return blockers, nil
}

// GetTasksBlockedBy returns all task UUIDs that are blocked by the given task.
// In other words, it finds tasks where the given task is the blocker (from_task_uuid).
// This is the inverse of BlockedBy - BlockedBy returns "who is blocking me",
// GetTasksBlockedBy returns "who am I blocking".
func (ts *TaskStore) GetTasksBlockedBy(blockerTaskUUID string) ([]string, error) {
	rows, err := ts.store.db.Query(`
		SELECT to_task_uuid
		FROM task_relations
		WHERE from_task_uuid = ?
		  AND kind = 'blocks'
	`, blockerTaskUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query blocked tasks: %w", err)
	}
	defer rows.Close()

	var blockedTasks []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return nil, fmt.Errorf("failed to scan blocked task: %w", err)
		}
		blockedTasks = append(blockedTasks, uuid)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating blocked tasks: %w", err)
	}

	// Return empty slice instead of nil for consistency
	if blockedTasks == nil {
		blockedTasks = []string{}
	}

	return blockedTasks, nil
}

// isCompletionState returns true if the given state represents a "completed" blocker
// that should no longer block other tasks.
func isCompletionState(state string) bool {
	switch state {
	case "completed", "cancelled", "archived", "deleted":
		return true
	default:
		return false
	}
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
