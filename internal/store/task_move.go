package store

import (
	"database/sql"
	"fmt"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/taskmember"
	"github.com/lherron/wrkq/internal/webhooks"
)

// Move moves a task to a different container and logs task.moved events.
// Returns the new etag on success.
func (ts *TaskStore) Move(actorUUID, taskUUID, newProjectUUID string, ifMatch int64) (int64, error) {
	return ts.MoveWithAttribution(ts.store.attributionFromActorUUID(actorUUID), taskUUID, newProjectUUID, ifMatch)
}

// MoveWithAttribution moves a task using canonical principal attribution.
func (ts *TaskStore) MoveWithAttribution(attr attribution.Attribution, taskUUID, newProjectUUID string, ifMatch int64) (int64, error) {
	return ts.MoveWithViaAttribution(attr, taskUUID, newProjectUUID, ifMatch, "cli")
}

// MoveWithViaAttribution moves a task subtree and records the ingress surface.
func (ts *TaskStore) MoveWithViaAttribution(attr attribution.Attribution, taskUUID, newProjectUUID string, ifMatch int64, via string) (int64, error) {
	if err := requireAttribution(attr); err != nil {
		return 0, err
	}
	if via == "" {
		via = "cli"
	}
	var newETag int64
	var webhooksToDispatch []pendingWebhook

	err := ts.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		var owner sql.NullString
		if err := tx.QueryRow("SELECT subtask_owner_uuid FROM tasks WHERE uuid=?", taskUUID).Scan(&owner); err != nil {
			return err
		}
		if owner.Valid {
			return fmt.Errorf("named subtasks cannot move independently; move the owner")
		}
		type moveTask struct {
			uuid             string
			currentETag      int64
			oldProjectUUID   string
			parentTaskUUID   sql.NullString
			oldContainerPath string
		}

		var root moveTask
		err := tx.QueryRow(`
			SELECT t.uuid, t.etag, t.project_uuid, t.parent_task_uuid, COALESCE(cp.path, '')
			FROM tasks t
			LEFT JOIN v_container_paths cp ON cp.uuid = t.project_uuid
			WHERE t.uuid = ?
		`, taskUUID).Scan(&root.uuid, &root.currentETag, &root.oldProjectUUID, &root.parentTaskUUID, &root.oldContainerPath)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("task not found: %s", taskUUID)
			}
			return fmt.Errorf("failed to get task: %w", err)
		}

		if err := checkETag(root.currentETag, ifMatch); err != nil {
			return err
		}

		if root.parentTaskUUID.Valid && root.oldProjectUUID != newProjectUUID {
			var parentProjectUUID string
			err := tx.QueryRow("SELECT project_uuid FROM tasks WHERE uuid = ?", root.parentTaskUUID.String).Scan(&parentProjectUUID)
			if err != nil {
				return fmt.Errorf("failed to load parent task: %w", err)
			}
			if parentProjectUUID == root.oldProjectUUID {
				return fmt.Errorf("cannot move subtask across containers independently; move the root task instead")
			}
		}

		rows, err := tx.Query(`
			WITH RECURSIVE subtree(uuid, project_uuid) AS (
				SELECT uuid, project_uuid FROM tasks WHERE uuid = ?
				UNION ALL
				SELECT t.uuid, t.project_uuid
				  FROM tasks t
				  JOIN subtree s ON t.parent_task_uuid = s.uuid
				 WHERE t.project_uuid = s.project_uuid AND `+taskmember.Filter("t", false)+`
			)
			SELECT t.uuid, t.etag, t.project_uuid, t.parent_task_uuid, COALESCE(cp.path, '')
			  FROM tasks t
			  JOIN subtree s ON s.uuid = t.uuid
			  LEFT JOIN v_container_paths cp ON cp.uuid = t.project_uuid
			 ORDER BY CASE WHEN t.uuid = ? THEN 0 ELSE 1 END, t.id
		`, taskUUID, taskUUID)
		if err != nil {
			return fmt.Errorf("failed to load task subtree: %w", err)
		}
		moveSet := []moveTask{}
		for rows.Next() {
			var mt moveTask
			if err := rows.Scan(&mt.uuid, &mt.currentETag, &mt.oldProjectUUID, &mt.parentTaskUUID, &mt.oldContainerPath); err != nil {
				_ = rows.Close()
				return fmt.Errorf("failed to scan task subtree: %w", err)
			}
			moveSet = append(moveSet, mt)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("failed to close task subtree rows: %w", err)
		}
		if len(moveSet) == 0 {
			return fmt.Errorf("task not found: %s", taskUUID)
		}

		allAlreadyInTarget := true
		for _, mt := range moveSet {
			if mt.oldProjectUUID != newProjectUUID {
				allAlreadyInTarget = false
				break
			}
		}
		if allAlreadyInTarget {
			moveUUIDs := make([]string, 0, len(moveSet))
			for _, mt := range moveSet {
				moveUUIDs = append(moveUUIDs, mt.uuid)
			}
			if err := validateEffectiveMembershipTx(tx, campaignValidation{taskUUIDs: moveUUIDs, move: true}); err != nil {
				return err
			}
			newETag = root.currentETag
			return nil
		}

		moveUUIDs := make([]string, 0, len(moveSet))
		for _, mt := range moveSet {
			moveUUIDs = append(moveUUIDs, mt.uuid)
			if _, err := tx.Exec("UPDATE tasks SET project_uuid = ? WHERE uuid = ?", newProjectUUID, mt.uuid); err != nil {
				return fmt.Errorf("failed to stage task move: %w", err)
			}
		}
		if err := validateEffectiveMembershipTx(tx, campaignValidation{
			taskUUIDs: moveUUIDs, residentAdmission: true, move: true,
		}); err != nil {
			return err
		}

		var newContainerPath string
		_ = tx.QueryRow("SELECT path FROM v_container_paths WHERE uuid = ?", newProjectUUID).Scan(&newContainerPath)

		for _, mt := range moveSet {
			if _, err := tx.Exec(`
					UPDATE tasks
					SET etag = etag + 1,
						updated_by_principal_ref = ?,
						updated_by_scope_ref = ?
					WHERE uuid = ?
				`, attr.PrincipalRef, scopeSQL(attr), mt.uuid); err != nil {
				return fmt.Errorf("failed to move task: %w", err)
			}

			taskETag := mt.currentETag + 1
			if mt.uuid == taskUUID {
				newETag = taskETag
			}
			payload := map[string]interface{}{
				"oldContainerUuid": mt.oldProjectUUID,
				"newContainerUuid": newProjectUUID,
				"oldContainerPath": mt.oldContainerPath,
				"newContainerPath": newContainerPath,
				"old_project_uuid": mt.oldProjectUUID,
				"new_project_uuid": newProjectUUID,
			}
			if mt.uuid != taskUUID {
				payload["cascadeRootTaskUuid"] = taskUUID
			}
			meta, err := logTaskEvent(tx, ew, attr, mt.uuid, "task.moved", &taskETag, payload)
			if err != nil {
				return err
			}
			webhooksToDispatch = append(webhooksToDispatch, pendingWebhook{
				taskUUID: mt.uuid,
				ctx: webhooks.EventContext{
					Metadata:     meta,
					Event:        "moved",
					PrincipalRef: attr.PrincipalRef,
					Via:          via,
					Transition:   nil,
					Changed:      []string{"container_path", "project_uuid"},
					Changes: map[string]webhooks.Change{
						"container_path": {From: mt.oldContainerPath, To: newContainerPath},
						"project_uuid":   {From: mt.oldProjectUUID, To: newProjectUUID},
					},
				},
			})
		}
		return nil
	})

	if err == nil {
		dispatchTaskWebhooks(ts.store.db, webhooksToDispatch)
	}

	return newETag, err
}
