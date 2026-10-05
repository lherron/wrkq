package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/taskmember"
	"github.com/lherron/wrkq/internal/webhooks"
)

const containerArchiveCascadeMetaKey = "_wrkq_archive_cascade"

type containerArchiveCascadeMarker struct {
	Version        int    `json:"version"`
	ContainerUUID  string `json:"container_uuid"`
	ArchiveEventID int64  `json:"archive_event_id"`
	PriorState     string `json:"prior_state"`
	MetaWasNull    bool   `json:"meta_was_null,omitempty"`
}

type containerArchiveTask struct {
	UUID  string
	State string
	Meta  sql.NullString
	ETag  int64
}

// Archive soft-deletes a container by setting archived_at timestamp.
func (cs *ContainerStore) Archive(actorUUID, containerUUID string, ifMatch int64) (int64, error) {
	return cs.ArchiveWithAttribution(cs.store.attributionFromActorUUID(actorUUID), containerUUID, ifMatch)
}

// ArchiveWithAttribution archives a container using canonical principal attribution.
func (cs *ContainerStore) ArchiveWithAttribution(attr attribution.Attribution, containerUUID string, ifMatch int64) (int64, error) {
	if err := requireAttribution(attr); err != nil {
		return 0, err
	}
	var newETag int64
	var webhooksToDispatch []pendingWebhook

	err := cs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current state
		var currentETag int64
		var slug string
		err := tx.QueryRow("SELECT etag, slug FROM containers WHERE uuid = ?", containerUUID).Scan(&currentETag, &slug)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("container not found: %s", containerUUID)
			}
			return fmt.Errorf("failed to get container: %w", err)
		}

		// Check etag if ifMatch was provided
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}

		cascadeTasks, err := containerArchiveLiveTasks(tx, containerUUID)
		if err != nil {
			return err
		}

		// Soft delete. Re-archiving is intentional: it refreshes the archive event
		// and cascades any live stragglers created since the prior archive.
		_, err = tx.Exec(`
			UPDATE containers
			SET archived_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
				updated_by_principal_ref = ?,
				updated_by_scope_ref = ?,
				etag = etag + 1
			WHERE uuid = ?
		`, attr.PrincipalRef, scopeSQL(attr), containerUUID)
		if err != nil {
			return fmt.Errorf("failed to archive container: %w", err)
		}

		// Log the container event before task updates so its durable event id can
		// be stored in every causal marker written by this transaction.
		payload := map[string]interface{}{
			"slug":        slug,
			"soft_delete": true,
		}
		newETag = currentETag + 1
		archiveEvent, err := logContainerEventReturning(tx, ew, attr, containerUUID, "container.archived", &newETag, payload)
		if err != nil {
			return err
		}

		for _, task := range cascadeTasks {
			pending, err := cascadeCancelTask(tx, ew, attr, containerUUID, archiveEvent.ID, task)
			if err != nil {
				return err
			}
			webhooksToDispatch = append(webhooksToDispatch, pending)
		}
		for _, task := range cascadeTasks {
			if err := maybeLogCampaignCloseNudgeForTask(tx, ew, attr, task.UUID); err != nil {
				return err
			}
		}

		return nil
	})
	if err == nil {
		dispatchTaskWebhooks(cs.store.db, webhooksToDispatch)
	}

	return newETag, err
}

// RestoreWithAttribution clears a container's archived marker and reverses only
// task cancellations causally marked by ArchiveWithAttribution for this exact
// container UUID. The container and task mutations share one transaction.
func (cs *ContainerStore) RestoreWithAttribution(attr attribution.Attribution, containerUUID string) error {
	if err := requireAttribution(attr); err != nil {
		return err
	}
	var webhooksToDispatch []pendingWebhook
	err := cs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		var exists int
		if err := tx.QueryRow("SELECT COUNT(*) FROM containers WHERE uuid = ?", containerUUID).Scan(&exists); err != nil {
			return fmt.Errorf("failed to resolve container: %w", err)
		}
		if exists == 0 {
			return fmt.Errorf("container not found: %s", containerUUID)
		}

		tasks, err := containerArchiveMarkedTasks(tx, containerUUID)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			UPDATE containers
			SET archived_at = NULL,
			    updated_by_principal_ref = ?,
			    updated_by_scope_ref = ?
			WHERE uuid = ?
		`, attr.PrincipalRef, scopeSQL(attr), containerUUID); err != nil {
			return fmt.Errorf("failed to restore container: %w", err)
		}

		for _, task := range tasks {
			pending, _, err := restoreCascadeTask(tx, ew, attr, containerUUID, task)
			if err != nil {
				return err
			}
			webhooksToDispatch = append(webhooksToDispatch, pending)
		}
		err = logContainerEvent(tx, ew, attr, containerUUID, "container.restored", nil, map[string]interface{}{"action": "restored"})
		return err
	})
	if err == nil {
		dispatchTaskWebhooks(cs.store.db, webhooksToDispatch)
	}
	return err
}

// containerArchiveLiveTasks lists the live member tasks in the container
// subtree that an archive cancels.
func containerArchiveLiveTasks(tx *sql.Tx, containerUUID string) ([]containerArchiveTask, error) {
	return queryContainerArchiveTasks(tx, "archive", `AND t.state IN ('idea','draft','open','in_progress','blocked')
		  AND t.archived_at IS NULL AND t.deleted_at IS NULL`, containerUUID)
}

// containerArchiveMarkedTasks lists the subtree tasks carrying this
// container's archive-cascade marker, i.e. the ones a restore reverses.
func containerArchiveMarkedTasks(tx *sql.Tx, containerUUID string) ([]containerArchiveTask, error) {
	return queryContainerArchiveTasks(tx, "unarchive", `AND json_valid(t.meta)
		  AND json_extract(t.meta, '$._wrkq_archive_cascade.container_uuid') = ?`, containerUUID, containerUUID)
}

func queryContainerArchiveTasks(tx *sql.Tx, verb, filter string, args ...interface{}) ([]containerArchiveTask, error) {
	rows, err := tx.Query(`
		`+containerSubtreeCTE+`
		SELECT t.uuid, t.state, t.meta, t.etag
		FROM tasks t JOIN subtree s ON s.uuid = t.project_uuid
		WHERE `+taskmember.Filter("t", false)+` `+filter+`
		ORDER BY t.uuid`, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query container %s tasks: %w", verb, err)
	}
	defer func() { _ = rows.Close() }()
	var tasks []containerArchiveTask
	for rows.Next() {
		var task containerArchiveTask
		if err := rows.Scan(&task.UUID, &task.State, &task.Meta, &task.ETag); err != nil {
			return nil, fmt.Errorf("failed to scan container %s task: %w", verb, err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate container %s tasks: %w", verb, err)
	}
	return tasks, nil
}

func cascadeCancelTask(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, containerUUID string, archiveEventID int64, task containerArchiveTask) (pendingWebhook, error) {
	meta, err := parseContainerArchiveMeta(task)
	if err != nil {
		return pendingWebhook{}, err
	}
	meta[containerArchiveCascadeMetaKey] = containerArchiveCascadeMarker{
		Version: 1, ContainerUUID: containerUUID, ArchiveEventID: archiveEventID,
		PriorState: task.State, MetaWasNull: !task.Meta.Valid,
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return pendingWebhook{}, fmt.Errorf("failed to encode archive marker for task %s: %w", task.UUID, err)
	}
	if _, err := tx.Exec(`
		UPDATE tasks
		SET state = 'cancelled', meta = ?, etag = etag + 1,
		    updated_by_principal_ref = ?, updated_by_scope_ref = ?
		WHERE uuid = ?`, string(metaJSON), attr.PrincipalRef, scopeSQL(attr), task.UUID); err != nil {
		return pendingWebhook{}, fmt.Errorf("failed to cancel task %s during container archive: %w", task.UUID, err)
	}
	payload := map[string]any{"state": "cancelled", "state_from": task.State, "meta": string(metaJSON)}
	newETag := task.ETag + 1
	eventMeta, err := logTaskEvent(tx, ew, attr, task.UUID, "task.updated", &newETag, payload)
	if err != nil {
		return pendingWebhook{}, fmt.Errorf("failed to log archive cancellation for task %s: %w", task.UUID, err)
	}
	return pendingWebhook{taskUUID: task.UUID, ctx: webhooks.EventContext{
		Metadata: eventMeta, Event: "updated", PrincipalRef: attr.PrincipalRef, Via: "cli",
		Transition: &webhooks.Transition{From: stringPtr(task.State), To: stringPtr("cancelled")},
		Changed:    []string{"meta", "state"},
		Changes: map[string]webhooks.Change{
			"meta":  {From: archiveNullableStringValue(task.Meta), To: string(metaJSON)},
			"state": {From: task.State, To: "cancelled"},
		},
	}}, nil
}

func restoreCascadeTask(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, containerUUID string, task containerArchiveTask) (pendingWebhook, bool, error) {
	meta, err := parseContainerArchiveMeta(task)
	if err != nil {
		return pendingWebhook{}, false, err
	}
	rawMarker, ok := meta[containerArchiveCascadeMetaKey]
	if !ok {
		return pendingWebhook{}, false, fmt.Errorf("archive marker disappeared for task %s", task.UUID)
	}
	markerJSON, _ := json.Marshal(rawMarker)
	var marker containerArchiveCascadeMarker
	if err := json.Unmarshal(markerJSON, &marker); err != nil || marker.Version != 1 || marker.ContainerUUID != containerUUID || !isContainerArchiveLiveState(marker.PriorState) {
		return pendingWebhook{}, false, fmt.Errorf("invalid archive marker for task %s", task.UUID)
	}
	delete(meta, containerArchiveCascadeMetaKey)
	var restoredMeta any
	var restoredMetaForEvent any
	if len(meta) == 0 && marker.MetaWasNull {
		restoredMeta = nil
		restoredMetaForEvent = nil
	} else {
		metaJSON, err := json.Marshal(meta)
		if err != nil {
			return pendingWebhook{}, false, fmt.Errorf("failed to clear archive marker for task %s: %w", task.UUID, err)
		}
		restoredMeta = string(metaJSON)
		restoredMetaForEvent = string(metaJSON)
	}
	restoreState := task.State == "cancelled"
	state := task.State
	if restoreState {
		state = marker.PriorState
	}
	if _, err := tx.Exec(`
		UPDATE tasks
		SET state = ?, meta = ?, etag = etag + 1,
		    updated_by_principal_ref = ?, updated_by_scope_ref = ?
		WHERE uuid = ?`, state, restoredMeta, attr.PrincipalRef, scopeSQL(attr), task.UUID); err != nil {
		return pendingWebhook{}, false, fmt.Errorf("failed to restore task %s during container unarchive: %w", task.UUID, err)
	}
	payload := map[string]any{"meta": restoredMetaForEvent}
	changed := []string{"meta"}
	changes := map[string]webhooks.Change{"meta": {From: archiveNullableStringValue(task.Meta), To: restoredMetaForEvent}}
	var transition *webhooks.Transition
	if restoreState {
		payload["state"] = state
		payload["state_from"] = task.State
		changed = append(changed, "state")
		changes["state"] = webhooks.Change{From: task.State, To: state}
		transition = &webhooks.Transition{From: stringPtr(task.State), To: stringPtr(state)}
	}
	newETag := task.ETag + 1
	eventMeta, err := logTaskEvent(tx, ew, attr, task.UUID, "task.updated", &newETag, payload)
	if err != nil {
		return pendingWebhook{}, false, fmt.Errorf("failed to log cascade restore for task %s: %w", task.UUID, err)
	}
	return pendingWebhook{taskUUID: task.UUID, ctx: webhooks.EventContext{
		Metadata: eventMeta, Event: "updated", PrincipalRef: attr.PrincipalRef, Via: "cli",
		Transition: transition, Changed: changed, Changes: changes,
	}}, restoreState, nil
}

func parseContainerArchiveMeta(task containerArchiveTask) (map[string]any, error) {
	meta := map[string]any{}
	if !task.Meta.Valid || strings.TrimSpace(task.Meta.String) == "" {
		return meta, nil
	}
	if err := json.Unmarshal([]byte(task.Meta.String), &meta); err != nil || meta == nil {
		return nil, fmt.Errorf("task %s has invalid meta; container archive aborted", task.UUID)
	}
	return meta, nil
}

func isContainerArchiveLiveState(state string) bool {
	switch state {
	case "idea", "draft", "open", "in_progress", "blocked":
		return true
	default:
		return false
	}
}

func archiveNullableStringValue(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}
