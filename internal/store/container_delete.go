package store

import (
	"database/sql"
	"fmt"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/taskmember"
)

// ContainerDeleteRecursiveImpact summarizes a destructive container subtree
// purge. Container counts include the root container being deleted.
type ContainerDeleteRecursiveImpact struct {
	ContainerUUID string
	Containers    int64
	Tasks         int64
	Attachments   int64
	Bytes         int64
}

// ContainerDeleteRecursiveResult contains committed purge statistics and file
// cleanup inputs captured before rows were deleted.
type ContainerDeleteRecursiveResult struct {
	Deleted            bool
	ContainersDeleted  int64
	TasksDeleted       int64
	AttachmentsDeleted int64
	BytesFreed         int64
	Attachments        []AttachmentInfo
	TaskUUIDs          []string
}

// ContainerDeleteImpactMismatchError reports that a caller-supplied recursive
// delete preflight is stale relative to the impact recomputed in the delete
// transaction.
type ContainerDeleteImpactMismatchError struct {
	Expected ContainerDeleteRecursiveImpact
	Current  ContainerDeleteRecursiveImpact
}

func (e *ContainerDeleteImpactMismatchError) Error() string {
	return "container deleteRecursive expected impact does not match current impact"
}

// Delete hard-deletes an empty container.
func (cs *ContainerStore) Delete(actorUUID, containerUUID string, ifMatch int64) error {
	return cs.DeleteWithAttribution(cs.store.attributionFromActorUUID(actorUUID), containerUUID, ifMatch)
}

// DeleteWithAttribution hard-deletes an empty container using canonical attribution.
func (cs *ContainerStore) DeleteWithAttribution(attr attribution.Attribution, containerUUID string, ifMatch int64) error {
	if err := requireAttribution(attr); err != nil {
		return err
	}
	return cs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current state
		var currentETag int64
		var id, slug, kind string
		err := tx.QueryRow("SELECT etag, id, slug, kind FROM containers WHERE uuid = ?", containerUUID).Scan(&currentETag, &id, &slug, &kind)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("container not found: %s", containerUUID)
			}
			return fmt.Errorf("failed to get container: %w", err)
		}
		if kind == string(domain.ContainerKindRoot) {
			return fmt.Errorf("root container cannot be deleted")
		}

		// Check etag if ifMatch was provided
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}

		// Check for children (tasks or subcontainers)
		var childCount int
		err = tx.QueryRow(`
			SELECT (
				(SELECT COUNT(*) FROM tasks WHERE `+taskmember.Filter("", false)+` AND project_uuid = ?) +
				(SELECT COUNT(*) FROM containers WHERE parent_uuid = ?)
			)
		`, containerUUID, containerUUID).Scan(&childCount)
		if err != nil {
			return fmt.Errorf("failed to check children: %w", err)
		}
		if childCount > 0 {
			return fmt.Errorf("container is not empty: has %d children", childCount)
		}

		// Log event BEFORE deleting
		payload := map[string]interface{}{
			"slug":       slug,
			"deleted_by": attr.PrincipalRef,
		}
		if err := logContainerEvent(tx, ew, attr, containerUUID, "container.deleted", nil, payload); err != nil {
			return err
		}
		if err := retargetPromisesForPurgedContainer(tx, ew, attr, containerUUID, id, slug); err != nil {
			return err
		}

		// Hard delete
		_, err = tx.Exec("DELETE FROM containers WHERE uuid = ?", containerUUID)
		if err != nil {
			return fmt.Errorf("failed to delete container: %w", err)
		}

		return nil
	})
}

// DeleteRecursiveImpact computes the current impact for deleting the container
// subtree rooted at containerUUID without mutating the database.
func (cs *ContainerStore) DeleteRecursiveImpact(containerUUID string) (*ContainerDeleteRecursiveImpact, error) {
	tx, err := cs.store.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	impact, _, _, err := cs.recursiveDeleteImpactTx(tx, containerUUID)
	return impact, err
}

// DeleteRecursiveWithAttribution hard-deletes a container and all descendant
// tasks/containers. The root container etag is the only CAS target; expected
// impact is compared against a fresh impact computed inside the delete tx.
func (cs *ContainerStore) DeleteRecursiveWithAttribution(attr attribution.Attribution, containerUUID string, ifMatch int64, expected ContainerDeleteRecursiveImpact) (*ContainerDeleteRecursiveResult, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	var result *ContainerDeleteRecursiveResult
	err := cs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		var currentETag int64
		var kind string
		err := tx.QueryRow("SELECT etag, kind FROM containers WHERE uuid = ?", containerUUID).Scan(&currentETag, &kind)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("container not found: %s", containerUUID)
			}
			return fmt.Errorf("failed to get container: %w", err)
		}
		if kind == string(domain.ContainerKindRoot) {
			return fmt.Errorf("root container cannot be deleted")
		}
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}

		current, containers, tasks, err := cs.recursiveDeleteImpactTx(tx, containerUUID)
		if err != nil {
			return err
		}
		if !sameDeleteImpact(expected, *current) {
			return &ContainerDeleteImpactMismatchError{Expected: expected, Current: *current}
		}

		// Ownership survives logical deletion; physical container purge cannot
		// remove an owner while its named assignments still exist.
		for _, task := range tasks {
			var owns int
			if err := tx.QueryRow("SELECT COUNT(*) FROM tasks WHERE subtask_owner_uuid = ?", task.UUID).Scan(&owns); err != nil {
				return err
			}
			if owns > 0 {
				return fmt.Errorf("cannot purge container containing an owner with subtasks; archive instead")
			}
		}
		attachments, err := recursiveDeleteAttachmentsTx(tx, containerUUID)
		if err != nil {
			return err
		}
		taskUUIDs := make([]string, 0, len(tasks))
		for _, task := range tasks {
			taskUUIDs = append(taskUUIDs, task.UUID)
			payload := map[string]interface{}{
				"slug":      task.Slug,
				"purged_by": attr.PrincipalRef,
			}
			if task.AttachmentCount > 0 {
				payload["attachment_count"] = task.AttachmentCount
				payload["bytes_freed"] = task.Bytes
			}
			if _, err := logTaskEvent(tx, ew, attr, task.UUID, "task.purged", nil, payload); err != nil {
				return err
			}
		}
		if err := detachExternalSubtasksForParents(tx, attr, taskUUIDs); err != nil {
			return err
		}
		for _, task := range tasks {
			if err := retargetPromisesForPurgedTask(tx, ew, attr, task.UUID, task.ID, task.Slug); err != nil {
				return err
			}
			if _, err := tx.Exec("DELETE FROM tasks WHERE uuid = ?", task.UUID); err != nil {
				return fmt.Errorf("failed to delete task: %w", err)
			}
		}

		for _, container := range containers {
			if err := logContainerEvent(tx, ew, attr, container.UUID, "container.deleted", nil, map[string]interface{}{
				"slug":       container.Slug,
				"deleted_by": attr.PrincipalRef,
				"recursive":  true,
			}); err != nil {
				return err
			}
		}
		for _, container := range containers {
			if err := retargetPromisesForPurgedContainer(tx, ew, attr, container.UUID, container.ID, container.Slug); err != nil {
				return err
			}
			if _, err := tx.Exec("DELETE FROM containers WHERE uuid = ?", container.UUID); err != nil {
				return fmt.Errorf("failed to delete container: %w", err)
			}
		}

		result = &ContainerDeleteRecursiveResult{
			Deleted:            true,
			ContainersDeleted:  current.Containers,
			TasksDeleted:       current.Tasks,
			AttachmentsDeleted: current.Attachments,
			BytesFreed:         current.Bytes,
			Attachments:        attachments,
			TaskUUIDs:          taskUUIDs,
		}
		return nil
	})
	return result, err
}

type recursiveContainerRow struct {
	UUID  string
	ID    string
	Slug  string
	Depth int
}

type recursiveTaskRow struct {
	UUID            string
	ID              string
	Slug            string
	AttachmentCount int64
	Bytes           int64
}

func (cs *ContainerStore) recursiveDeleteImpactTx(tx *sql.Tx, containerUUID string) (*ContainerDeleteRecursiveImpact, []recursiveContainerRow, []recursiveTaskRow, error) {
	containers, err := recursiveDeleteContainersTx(tx, containerUUID)
	if err != nil {
		return nil, nil, nil, err
	}
	tasks, err := recursiveDeleteTasksTx(tx, containerUUID)
	if err != nil {
		return nil, nil, nil, err
	}
	var attachmentCount, totalBytes int64
	for _, task := range tasks {
		attachmentCount += task.AttachmentCount
		totalBytes += task.Bytes
	}
	return &ContainerDeleteRecursiveImpact{
		ContainerUUID: containerUUID,
		Containers:    int64(len(containers)),
		Tasks:         int64(len(tasks)),
		Attachments:   attachmentCount,
		Bytes:         totalBytes,
	}, containers, tasks, nil
}

func recursiveDeleteContainersTx(tx *sql.Tx, containerUUID string) ([]recursiveContainerRow, error) {
	rows, err := tx.Query(`
		WITH RECURSIVE subtree(uuid, id, slug, depth) AS (
			SELECT uuid, id, slug, 0 FROM containers WHERE uuid = ?
			UNION ALL
			SELECT c.uuid, c.id, c.slug, subtree.depth + 1
			  FROM containers c
			  JOIN subtree ON c.parent_uuid = subtree.uuid
		)
		SELECT uuid, id, slug, depth FROM subtree ORDER BY depth DESC, slug ASC
	`, containerUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query recursive containers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []recursiveContainerRow{}
	for rows.Next() {
		var row recursiveContainerRow
		if err := rows.Scan(&row.UUID, &row.ID, &row.Slug, &row.Depth); err != nil {
			return nil, fmt.Errorf("failed to scan recursive container: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate recursive containers: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("container not found: %s", containerUUID)
	}
	return out, nil
}

func recursiveDeleteTasksTx(tx *sql.Tx, containerUUID string) ([]recursiveTaskRow, error) {
	rows, err := tx.Query(`
		`+containerSubtreeCTE+`
		SELECT t.uuid, t.id, t.slug, COUNT(a.uuid), COALESCE(SUM(a.size_bytes), 0)
		  FROM tasks t
		  JOIN subtree ON t.project_uuid = subtree.uuid
		  LEFT JOIN attachments a ON a.task_uuid = t.uuid
	 WHERE `+taskmember.Filter("t", false)+`
 GROUP BY t.uuid, t.id, t.slug
		 ORDER BY t.id ASC
	`, containerUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query recursive tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []recursiveTaskRow{}
	for rows.Next() {
		var row recursiveTaskRow
		if err := rows.Scan(&row.UUID, &row.ID, &row.Slug, &row.AttachmentCount, &row.Bytes); err != nil {
			return nil, fmt.Errorf("failed to scan recursive task: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate recursive tasks: %w", err)
	}
	return out, nil
}

func recursiveDeleteAttachmentsTx(tx *sql.Tx, containerUUID string) ([]AttachmentInfo, error) {
	rows, err := tx.Query(`
		`+containerSubtreeCTE+`
		SELECT a.task_uuid, a.relative_path, a.size_bytes
		  FROM attachments a
		  JOIN tasks t ON t.uuid = a.task_uuid
		  JOIN subtree ON t.project_uuid = subtree.uuid
		 WHERE `+taskmember.Filter("t", false)+`
		 ORDER BY a.id ASC
	`, containerUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query recursive attachments: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []AttachmentInfo{}
	for rows.Next() {
		var a AttachmentInfo
		if err := rows.Scan(&a.TaskUUID, &a.RelativePath, &a.SizeBytes); err != nil {
			return nil, fmt.Errorf("failed to scan recursive attachment: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate recursive attachments: %w", err)
	}
	return out, nil
}

func sameDeleteImpact(a, b ContainerDeleteRecursiveImpact) bool {
	return a.Containers == b.Containers &&
		a.Tasks == b.Tasks &&
		a.Attachments == b.Attachments &&
		a.Bytes == b.Bytes
}
