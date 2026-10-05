package store

import (
	"database/sql"
	"fmt"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/taskmember"
	"github.com/lherron/wrkq/internal/webhooks"
)

// ArchiveResult contains statistics about an archive operation.
type ArchiveResult struct {
	ETag int64
}

// Archive soft-deletes a task by setting state to 'archived' and archived_at timestamp.
func (ts *TaskStore) Archive(actorUUID, taskUUID string, ifMatch int64) (*ArchiveResult, error) {
	return ts.ArchiveWithViaAttribution(ts.store.attributionFromActorUUID(actorUUID), taskUUID, ifMatch, "cli")
}

// ArchiveWithAttribution archives a task with canonical principal attribution.
func (ts *TaskStore) ArchiveWithAttribution(attr attribution.Attribution, taskUUID string, ifMatch int64) (*ArchiveResult, error) {
	return ts.ArchiveWithViaAttribution(attr, taskUUID, ifMatch, "cli")
}

// ArchiveWithViaAttribution archives a task and records the ingress surface.
func (ts *TaskStore) ArchiveWithViaAttribution(attr attribution.Attribution, taskUUID string, ifMatch int64, via string) (*ArchiveResult, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	if via == "" {
		via = "cli"
	}
	var result *ArchiveResult
	var webhookCtx webhooks.EventContext

	err := ts.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current state
		var currentETag int64
		var slug string
		var currentState string
		err := tx.QueryRow("SELECT etag, slug, state FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentETag, &slug, &currentState)
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
				updated_by_principal_ref = ?,
				updated_by_scope_ref = ?,
				etag = etag + 1
			WHERE uuid = ?
		`, attr.PrincipalRef, scopeSQL(attr), taskUUID)
		if err != nil {
			return fmt.Errorf("failed to archive task: %w", err)
		}

		// Log event
		payload := map[string]interface{}{
			"slug":        slug,
			"soft_delete": true,
		}
		newETag := currentETag + 1
		meta, err := logTaskEvent(tx, ew, attr, taskUUID, "task.archived", &newETag, payload)
		if err != nil {
			return err
		}
		if !isCompletionState(currentState) {
			if err := maybeLogCampaignCloseNudgeForTask(tx, ew, attr, taskUUID); err != nil {
				return err
			}
		}
		webhookCtx = webhooks.EventContext{
			Metadata:     meta,
			Event:        "archived",
			PrincipalRef: attr.PrincipalRef,
			Via:          via,
			Transition:   &webhooks.Transition{From: stringPtr(currentState), To: stringPtr("archived")},
			Changed:      []string{"archived_at", "state"},
			Changes: map[string]webhooks.Change{
				"state":       {From: currentState, To: "archived"},
				"archived_at": {From: nil, To: "now"},
			},
		}

		result = &ArchiveResult{ETag: newETag}
		return nil
	})

	if err == nil && result != nil {
		webhooks.DispatchTaskEvent(ts.store.db, taskUUID, webhookCtx)
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
	return ts.PurgeWithAttribution(ts.store.attributionFromActorUUID(actorUUID), taskUUID, ifMatch)
}

// PurgeWithAttribution hard-deletes a task with canonical principal attribution.
func (ts *TaskStore) PurgeWithAttribution(attr attribution.Attribution, taskUUID string, ifMatch int64) (*PurgeResult, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	var result *PurgeResult
	var webhookInfo *webhooks.TaskInfo
	var webhookCtx webhooks.EventContext

	err := ts.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current state
		var currentETag int64
		var id, slug, currentState string
		err := tx.QueryRow("SELECT etag, id, slug, state FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentETag, &id, &slug, &currentState)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("task not found: %s", taskUUID)
			}
			return fmt.Errorf("failed to get task: %w", err)
		}

		var forbidden int
		if err := tx.QueryRow("SELECT count(*) FROM tasks WHERE (uuid = ? AND subtask_owner_uuid IS NOT NULL) OR subtask_owner_uuid = ?", taskUUID, taskUUID).Scan(&forbidden); err != nil {
			return err
		}
		if forbidden > 0 {
			return fmt.Errorf("cannot purge a subtask or an owner with subtasks; archive instead")
		}

		// Check etag if ifMatch was provided
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}

		// Guard causal lineage: refuse to hard-purge a task that surviving tasks
		// still attribute their defect/rework to. The caller must clear those
		// dependents' caused_by first (or purge them too). This mirrors the DB-level
		// ON DELETE RESTRICT but yields a stable, explanatory error.
		var causedByReferrers int
		if err := tx.QueryRow(
			"SELECT COUNT(*) FROM task_causes WHERE caused_by_task_uuid = ?", taskUUID,
		).Scan(&causedByReferrers); err != nil {
			return fmt.Errorf("failed to check caused_by referrers: %w", err)
		}
		if causedByReferrers > 0 {
			return fmt.Errorf("cannot purge task %s: it is referenced by the caused_by lineage of %d surviving task(s); clear those references first", slug, causedByReferrers)
		}

		// Guard the room (T-07641): rooms.task_uuid cascades on delete, so a
		// purge would silently destroy the task's conversation, every envelope
		// in it, its members and receipts — including reply_required obligations
		// presented to another seat. Talk outlives the runtime that carried it;
		// it must outlive the task row too. Archive instead (rm without --purge).
		var roomCount, envelopeCount int
		if err := tx.QueryRow(
			`SELECT COUNT(r.uuid), COUNT(e.uuid) FROM rooms r
			 LEFT JOIN envelopes e ON e.room_uuid = r.uuid
			 WHERE r.task_uuid = ?`, taskUUID,
		).Scan(&roomCount, &envelopeCount); err != nil {
			return fmt.Errorf("failed to check task room: %w", err)
		}
		if roomCount > 0 {
			return fmt.Errorf("cannot purge task %s: it has a room with %d envelope(s); a purge would destroy the conversation and every live obligation in it — archive it instead (rm without --purge)", slug, envelopeCount)
		}

		// Capture webhook payload info before deletion
		info, err := webhooks.LookupTaskInfoWith(tx, taskUUID)
		if err != nil {
			return fmt.Errorf("failed to load webhook info: %w", err)
		}
		webhookInfo = &info

		campaignUUID := ""
		if !isCompletionState(currentState) {
			campaignUUID, err = campaignUUIDForTaskTx(tx, taskUUID)
			if err != nil {
				return err
			}
		}

		// Count attachments for statistics
		var attachmentCount int
		var totalBytes int64
		rows, err := tx.Query("SELECT size_bytes FROM attachments WHERE task_uuid = ?", taskUUID)
		if err != nil {
			return fmt.Errorf("failed to query attachments: %w", err)
		}
		defer func() { _ = rows.Close() }()

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
			"purged_by": attr.PrincipalRef,
		}
		if attachmentCount > 0 {
			payload["attachment_count"] = attachmentCount
			payload["bytes_freed"] = totalBytes
		}
		meta, err := logTaskEvent(tx, ew, attr, taskUUID, "task.purged", nil, payload)
		if err != nil {
			return err
		}
		webhookCtx = webhooks.EventContext{
			Metadata:     meta,
			Event:        "purged",
			PrincipalRef: attr.PrincipalRef,
			Via:          "cli",
			Transition:   nil,
			Changed:      []string{},
			Changes:      map[string]webhooks.Change{},
		}

		if err := detachExternalSubtasks(tx, attr, taskUUID); err != nil {
			return fmt.Errorf("failed to detach external subtasks: %w", err)
		}
		if err := purgeResidentSubtasks(tx, ew, attr, taskUUID); err != nil {
			return fmt.Errorf("failed to purge resident subtasks: %w", err)
		}
		if err := retargetPromisesForPurgedTask(tx, ew, attr, taskUUID, id, slug); err != nil {
			return err
		}

		// Hard delete (CASCADE will delete attachments and comments)
		_, err = tx.Exec("DELETE FROM tasks WHERE uuid = ?", taskUUID)
		if err != nil {
			return fmt.Errorf("failed to delete task: %w", err)
		}
		if err := maybeLogCampaignCloseNudge(tx, ew, attr, campaignUUID); err != nil {
			return err
		}

		result = &PurgeResult{
			AttachmentsDeleted: attachmentCount,
			BytesFreed:         totalBytes,
		}
		return nil
	})

	if err == nil && webhookInfo != nil {
		webhooks.DispatchTaskInfoEvent(ts.store.db, *webhookInfo, webhookCtx)
	}

	return result, err
}

// AttachmentInfo contains info about an attachment for file cleanup.
type AttachmentInfo struct {
	TaskUUID     string
	RelativePath string
	SizeBytes    int64
}

// GetAttachments returns all attachments for a task.
func (ts *TaskStore) GetAttachments(taskUUID string) ([]AttachmentInfo, error) {
	rows, err := ts.store.db.Query("SELECT relative_path, size_bytes FROM attachments WHERE task_uuid = ?", taskUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query attachments: %w", err)
	}
	defer func() { _ = rows.Close() }()

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

// detachExternalSubtasks detaches child tasks that are linked by parent_task_uuid
// but live in a different resident container/project than the parent. Parent
// graph edges are not containment, so parent mutations must not delete or move
// external children.
func detachExternalSubtasks(tx *sql.Tx, attr attribution.Attribution, parentTaskUUID string) error {
	if _, err := tx.Exec(`
		UPDATE tasks
		SET parent_task_uuid = NULL,
		    kind = 'task',
		    etag = etag + 1,
		    updated_by_principal_ref = ?,
		    updated_by_scope_ref = ?
		WHERE parent_task_uuid = ?
		  AND state != 'deleted'
		  AND project_uuid != (SELECT project_uuid FROM tasks WHERE uuid = ?)
	`, attr.PrincipalRef, scopeSQL(attr), parentTaskUUID, parentTaskUUID); err != nil {
		return fmt.Errorf("failed to detach external subtasks: %w", err)
	}
	return nil
}

// detachExternalSubtasksForParents detaches external child backlinks for a set of
// parent tasks that are about to be hard-deleted.
func detachExternalSubtasksForParents(tx *sql.Tx, attr attribution.Attribution, parentTaskUUIDs []string) error {
	for _, parentTaskUUID := range parentTaskUUIDs {
		if err := detachExternalSubtasks(tx, attr, parentTaskUUID); err != nil {
			return err
		}
	}
	return nil
}

// cascadeDeleteResidentSubtasks deletes same-residency subtasks when a parent
// task is deleted. External children are detached by detachExternalSubtasks.
// This is called within a transaction when a task's state is set to 'deleted'.
func cascadeDeleteResidentSubtasks(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, parentTaskUUID string) error {
	// Find all subtasks (not already deleted)
	rows, err := tx.Query(`
		SELECT c.uuid
		FROM tasks c
		JOIN tasks p ON p.uuid = c.parent_task_uuid
		WHERE c.parent_task_uuid = ?
		  AND c.state != 'deleted'
		  AND c.project_uuid = p.project_uuid AND `+taskmember.Filter("c", false)+`
	`, parentTaskUUID)
	if err != nil {
		return fmt.Errorf("failed to query subtasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var subtaskUUIDs []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return fmt.Errorf("failed to scan subtask: %w", err)
		}
		subtaskUUIDs = append(subtaskUUIDs, uuid)
	}
	_ = rows.Close()

	// Delete each subtask
	for _, subtaskUUID := range subtaskUUIDs {
		_, err := tx.Exec(`
			UPDATE tasks
			SET state = 'deleted',
			    updated_by_principal_ref = ?,
			    updated_by_scope_ref = ?,
			    deleted_by_principal_ref = ?,
			    deleted_by_scope_ref = ?
			WHERE uuid = ?
		`, attr.PrincipalRef, scopeSQL(attr), attr.PrincipalRef, scopeSQL(attr), subtaskUUID)
		if err != nil {
			return fmt.Errorf("failed to delete subtask %s: %w", subtaskUUID, err)
		}

		// Log event
		if _, err := logTaskEvent(tx, ew, attr, subtaskUUID, "task.deleted", nil, map[string]any{"action": "cascade_deleted", "parent_deleted": true}); err != nil {
			return err
		}

		if err := detachExternalSubtasks(tx, attr, subtaskUUID); err != nil {
			return err
		}

		// Recursively delete nested resident subtasks
		if err := cascadeDeleteResidentSubtasks(tx, ew, attr, subtaskUUID); err != nil {
			return err
		}
	}

	return nil
}

// purgeResidentSubtasks hard-deletes same-residency subtasks before their parent
// task is purged. The schema no longer cascades via parent_task_uuid because
// cross-project parent edges are graph backlinks, not containment.
func purgeResidentSubtasks(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, parentTaskUUID string) error {
	rows, err := tx.Query(`
		SELECT c.uuid, c.id, c.slug
		FROM tasks c
		JOIN tasks p ON p.uuid = c.parent_task_uuid
		WHERE c.parent_task_uuid = ?
		  AND c.project_uuid = p.project_uuid AND `+taskmember.Filter("c", false)+`
		ORDER BY c.id
	`, parentTaskUUID)
	if err != nil {
		return fmt.Errorf("failed to query resident subtasks for purge: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type subtask struct {
		uuid string
		id   string
		slug string
	}
	var subtasks []subtask
	for rows.Next() {
		var st subtask
		if err := rows.Scan(&st.uuid, &st.id, &st.slug); err != nil {
			return fmt.Errorf("failed to scan resident subtask for purge: %w", err)
		}
		subtasks = append(subtasks, st)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to iterate resident subtasks for purge: %w", err)
	}

	for _, st := range subtasks {
		var namedCount int
		if err := tx.QueryRow("SELECT count(*) FROM tasks WHERE subtask_owner_uuid=?", st.uuid).Scan(&namedCount); err != nil {
			return err
		}
		if namedCount > 0 {
			return fmt.Errorf("cannot purge a task owning named subtasks; archive instead")
		}

		if err := detachExternalSubtasks(tx, attr, st.uuid); err != nil {
			return err
		}
		if err := purgeResidentSubtasks(tx, ew, attr, st.uuid); err != nil {
			return err
		}
		payloadMap := map[string]interface{}{
			"slug":                st.slug,
			"purged_by":           attr.PrincipalRef,
			"cascadeRootTaskUuid": parentTaskUUID,
		}
		if _, err := logTaskEvent(tx, ew, attr, st.uuid, "task.purged", nil, payloadMap); err != nil {
			return err
		}
		if err := retargetPromisesForPurgedTask(tx, ew, attr, st.uuid, st.id, st.slug); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM tasks WHERE uuid = ?", st.uuid); err != nil {
			return fmt.Errorf("failed to purge resident subtask %s: %w", st.uuid, err)
		}
	}
	return nil
}
