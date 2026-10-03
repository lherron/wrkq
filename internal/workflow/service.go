//go:build wrkq_local

package workflow

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/webhooks"
)

func suspensionID(suspension *Suspension) interface{} {
	if suspension == nil {
		return nil
	}
	return suspension.ID
}

func suspensionReason(suspension *Suspension) interface{} {
	if suspension == nil {
		return nil
	}
	return suspension.Reason
}

func suspensionAt(suspension *Suspension) interface{} {
	if suspension == nil {
		return nil
	}
	return suspension.At
}

func suspensionCauseRef(suspension *Suspension) interface{} {
	if suspension == nil || suspension.CauseRef == "" {
		return nil
	}
	return suspension.CauseRef
}

func NewService(database *db.DB) *Service {
	return &Service{db: database, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) AttachTask(taskSelector, templateRef, actor string, opts ...AttachTaskOptions) (*Instance, error) {
	var options AttachTaskOptions
	if len(opts) > 0 {
		options = opts[0]
	}
	if len(opts) > 1 {
		return nil, validationError("options", "only one attach options value is supported", "single options object", nil, "pass one AttachTaskOptions value")
	}
	if options.Supersede {
		if strings.TrimSpace(options.PredecessorInstanceID) == "" {
			return nil, validationError("predecessorInstanceId", "predecessor instance id is required for supersede", "current live workflow instance id", nil, "inspect the task and pass the current instance id")
		}
		if options.PredecessorRevision == nil {
			return nil, validationError("predecessorRevision", "predecessor revision is required for supersede", "current live workflow revision", nil, "inspect the task and pass the current revision")
		}
	} else if strings.TrimSpace(options.PredecessorInstanceID) != "" || options.PredecessorRevision != nil {
		return nil, validationError("supersede", "predecessor guard requires supersede", "supersede=true", nil, "set supersede when passing predecessor CAS fields")
	}

	taskUUID, taskID, err := selectors.ResolveTask(s.db, taskSelector)
	if err != nil {
		return nil, err
	}
	id, version, err := parseTemplateRef(templateRef)
	if err != nil {
		return nil, err
	}

	if _, builtinErr := builtinTemplateData(templateRef); builtinErr == nil {
		if _, _, err := s.EnsureBuiltinTemplate(templateRef, actor); err != nil {
			return nil, err
		}
	}
	var inst *Instance
	var attachedEvent workflowEventMetadata
	var dispatchAttachedWebhook bool
	var initial State
	err = withImmediateTx(s.db, func(tx *sql.Tx) error {
		var subtaskOwner sql.NullString
		if err := tx.QueryRow("SELECT subtask_owner_uuid FROM tasks WHERE uuid=?", taskUUID).Scan(&subtaskOwner); err != nil {
			return err
		}
		if subtaskOwner.Valid {
			return validationError("task", "named subtasks cannot attach workflows", "ordinary task", nil, "attach the workflow to the owner")
		}

		var definition, templateHash string
		var discontinuedAt sql.NullString
		if err := tx.QueryRow(`
			SELECT definition_json, hash, discontinued_at
			FROM workflow_templates
			WHERE id = ? AND version = ?
		`, id, version).Scan(&definition, &templateHash, &discontinuedAt); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("template not found: %s", templateRef)
			}
			return err
		}
		if discontinuedAt.Valid && !options.AttachDiscontinued {
			return validationError(
				"workflow",
				fmt.Sprintf("template %s was discontinued at %s", templateRef, discontinuedAt.String),
				"non-discontinued template version",
				nil,
				"pass --attach-discontinued to deliberately attach this template version",
			)
		}
		tpl, _, err := ParseTemplate([]byte(definition))
		if err != nil {
			return err
		}
		initial = tpl.Initial
		task, err := loadTaskDoc(tx, taskUUID)
		if err != nil {
			return err
		}
		taskHash := taskDocHash(task)
		instanceID := fmt.Sprintf("wfi_%s_%d", strings.ToLower(strings.ReplaceAll(taskID, "-", "")), s.now().UnixNano())
		now := s.now().Format(time.RFC3339)
		var predecessor *InstanceLineageRef
		if options.Supersede {
			current, err := activeInstanceByTaskUUIDQuery(tx, taskUUID)
			if err != nil {
				return err
			}
			if current.ID != strings.TrimSpace(options.PredecessorInstanceID) {
				return validationError("predecessorInstanceId", "current live workflow instance does not match predecessor guard", current.ID, []string{current.ID}, "retry with the current live workflow instance")
			}
			if current.Revision != *options.PredecessorRevision {
				return staleRevisionError(current.ID, *options.PredecessorRevision, current.Revision)
			}
			closed := *current
			closed.Status = "closed"
			closed.Phase = "superseded"
			closed.Outcome = ""
			closed.Revision = current.Revision + 1
			closed.UpdatedAt = now
			closed.ClosedAt = now
			closed.TaskDocEtag = fmt.Sprint(task.ETag)
			closed.TaskDocHash = taskHash
			res, err := tx.Exec(`
				UPDATE workflow_instances
				SET status = 'closed', phase = 'superseded', outcome = NULL, revision = ?,
				    task_doc_etag = ?, task_doc_hash = ?, updated_at = ?, closed_at = ?
				WHERE task_uuid = ? AND id = ? AND status != 'closed' AND revision = ?
			`, closed.Revision, closed.TaskDocEtag, closed.TaskDocHash, closed.UpdatedAt, closed.ClosedAt, taskUUID, current.ID, current.Revision)
			if err != nil {
				return err
			}
			affected, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if affected != 1 {
				actual, loadErr := instanceRevisionTx(tx, current.ID)
				if loadErr != nil {
					return loadErr
				}
				return staleRevisionError(current.ID, current.Revision, actual.revision)
			}
			successorRef := instanceLineageRef(Instance{
				ID:              instanceID,
				TemplateID:      id,
				TemplateVersion: version,
				TemplateHash:    templateHash,
				Status:          tpl.Initial.Status,
				Phase:           tpl.Initial.Phase,
				Outcome:         tpl.Initial.Outcome,
				Revision:        0,
			})
			predecessor = instanceLineageRef(closed)
			payload := map[string]interface{}{
				"predecessor": instanceLineageRef(*current),
				"successor":   successorRef,
				"from":        current.State(),
				"to":          closed.State(),
			}
			if _, err := insertEventReturning(tx, current.ID, "workflow.superseded", actor, "", "", current.Revision, closed.Revision, "", task.ETag, taskHash, payload); err != nil {
				return err
			}
		}
		_, err = tx.Exec(`
			INSERT INTO workflow_instances (
				id, task_uuid, task_ref, project_id, template_id, template_version, template_hash,
				status, phase, outcome, revision, task_doc_etag, task_doc_hash,
				created_at, updated_at, closed_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?)
		`, instanceID, taskUUID, "wrkq:"+taskID, task.ProjectID, id, version, templateHash,
			tpl.Initial.Status, nullIfEmpty(tpl.Initial.Phase), nullIfEmpty(tpl.Initial.Outcome),
			fmt.Sprint(task.ETag), taskHash, now, now, nil)
		if err != nil {
			return err
		}
		inst = &Instance{ID: instanceID, TaskUUID: taskUUID, TaskRef: "wrkq:" + taskID, ProjectID: task.ProjectID, TemplateID: id, TemplateVersion: version, TemplateHash: templateHash, Status: tpl.Initial.Status, Phase: tpl.Initial.Phase, Outcome: tpl.Initial.Outcome, Revision: 0, TaskDocEtag: fmt.Sprint(task.ETag), TaskDocHash: taskHash, CreatedAt: now, UpdatedAt: now}
		attachedPayload := map[string]interface{}{"template": templateRef, "state": tpl.Initial}
		if predecessor != nil {
			inst.Supersedes = predecessor
			attachedPayload["supersededPredecessor"] = predecessor
		}
		attachedEvent, err = insertEventReturning(tx, instanceID, "workflow.attached", actor, "", "", 0, 0, "", task.ETag, taskHash, attachedPayload)
		if err != nil {
			return err
		}
		dispatchAttachedWebhook = true
		return updateTaskWorkflowMeta(tx, taskUUID, *inst, actor)
	})
	if err != nil {
		return nil, err
	}
	if dispatchAttachedWebhook && inst != nil {
		webhooks.DispatchTaskEvent(s.db, inst.TaskUUID, workflowAttachedWebhookContext(attachedEvent, *inst, templateRef, actor, initial))
	}
	return inst, nil
}

func loadTaskDoc(tx queryer, taskUUID string) (*taskDoc, error) {
	var t taskDoc
	var labels, meta sql.NullString
	err := tx.QueryRow(`
		SELECT uuid, id, project_uuid, slug, title, description, specification, state, priority, kind,
		       labels, meta, etag, updated_at
		FROM tasks WHERE uuid = ?
	`, taskUUID).Scan(&t.UUID, &t.ID, &t.ProjectID, &t.Slug, &t.Title, &t.Description, &t.Specification, &t.State, &t.Priority, &t.Kind, &labels, &meta, &t.ETag, &t.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("task not found")
		}
		return nil, err
	}
	if labels.Valid {
		t.Labels = labels.String
	}
	if meta.Valid {
		t.Meta = meta.String
	}
	return &t, nil
}

func taskRelationBlockers(q rowsQueryer, taskUUID string) ([]Blocker, error) {
	rows, err := q.Query(`
		SELECT b.id, b.state, COALESCE(b.title, '')
		FROM task_relations r
		JOIN tasks b ON b.uuid = r.from_task_uuid
		WHERE r.to_task_uuid = ?
		  AND r.kind = 'blocks'
		  AND b.state NOT IN ('completed', 'cancelled', 'archived', 'deleted')
		ORDER BY b.id
	`, taskUUID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var blockers []Blocker
	for rows.Next() {
		var id, state, title string
		if err := rows.Scan(&id, &state, &title); err != nil {
			return nil, err
		}
		msg := fmt.Sprintf("blocked by task %s in state %s", id, state)
		if title != "" {
			msg = fmt.Sprintf("blocked by task %s (%s) in state %s", id, title, state)
		}
		blockers = append(blockers, Blocker{Kind: "task_dependency", Ref: id, Message: msg})
	}
	return blockers, rows.Err()
}

func taskDocHash(t *taskDoc) string {
	meta := map[string]interface{}{}
	if strings.TrimSpace(t.Meta) != "" {
		_ = json.Unmarshal([]byte(t.Meta), &meta)
		delete(meta, "workflow")
	}
	doc := map[string]interface{}{
		"id": t.ID, "slug": t.Slug, "title": t.Title, "description": t.Description,
		"specification": t.Specification, "state": t.State, "priority": t.Priority,
		"kind": t.Kind, "labels": t.Labels, "meta": meta,
	}
	b, _ := json.Marshal(doc)
	return Hash(b)
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func withTx(db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// withImmediateTx starts a write transaction using SQLite BEGIN IMMEDIATE.
// go-sqlite3 emits BEGIN IMMEDIATE when the connection DSN has _txlock=immediate.
func withImmediateTx(database *db.DB, fn func(*sql.Tx) error) error {
	dsn := database.Path()
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	immediateDB, err := sql.Open("sqlite3", dsn+sep+"_txlock=immediate&_busy_timeout=5000")
	if err != nil {
		return err
	}
	defer func() { _ = immediateDB.Close() }()
	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := immediateDB.Exec(pragma); err != nil {
			return err
		}
	}
	tx, err := immediateDB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func insertEventReturning(tx *sql.Tx, instanceID, typ, actor, role, runID string, observed, next int64, key string, taskETag int64, taskHash string, payload interface{}) (workflowEventMetadata, error) {
	return insertEventWithResult(tx, instanceID, typ, actor, role, runID, observed, next, key, "", "", taskETag, taskHash, payload)
}

func insertEventWithResult(tx *sql.Tx, instanceID, typ, actor, role, runID string, observed, next int64, key, requestHash, resultJSON string, taskETag int64, taskHash string, payload interface{}) (workflowEventMetadata, error) {
	id, err := nextSeqID(tx, "workflow_event_seq", "wfe")
	if err != nil {
		return workflowEventMetadata{}, err
	}
	var seq int64
	_ = tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM workflow_events WHERE instance_id = ?`, instanceID).Scan(&seq)
	payloadJSON, _ := json.Marshal(payload)
	prevHash := previousEventHashTx(tx, instanceID)
	eventHash := chainedEventHash(prevHash, payloadJSON)
	_, err = tx.Exec(`
		INSERT INTO workflow_events (
			id, instance_id, seq, schema_version, type, actor, principal_ref, role, run_id,
			observed_revision, next_revision, task_doc_etag, task_doc_hash,
			idempotency_key, request_hash, result, result_json, payload_json, prev_event_hash, event_hash
		) VALUES (?, ?, ?, 'wrkf.workflow-event.v0', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'committed', ?, ?, ?, ?)
	`, id, instanceID, seq, typ, emptyToNil(actor), emptyToNil(actor), emptyToNil(role), emptyToNil(runID), observed, next, fmt.Sprint(taskETag), taskHash, emptyToNil(key), nullIfEmpty(requestHash), nullIfEmpty(resultJSON), string(payloadJSON), nullIfEmpty(prevHash), eventHash)
	if err != nil {
		return workflowEventMetadata{}, err
	}
	var createdAt string
	if err := tx.QueryRow(`SELECT created_at FROM workflow_events WHERE id = ?`, id).Scan(&createdAt); err != nil {
		return workflowEventMetadata{}, err
	}
	return workflowEventMetadata{
		ID:            id,
		Seq:           seq,
		SchemaVersion: "wrkf.workflow-event.v0",
		Type:          typ,
		CreatedAt:     createdAt,
		Payload:       payload,
	}, nil
}

func nextSeqID(tx *sql.Tx, table, prefix string) (string, error) {
	if _, err := tx.Exec(fmt.Sprintf("INSERT INTO %s (id) VALUES (NULL)", table)); err != nil {
		return "", err
	}
	var id int64
	if err := tx.QueryRow("SELECT last_insert_rowid()").Scan(&id); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%06d", prefix, id), nil
}

func updateTaskWorkflowMeta(tx *sql.Tx, taskUUID string, inst Instance, actor string) error {
	var metaText sql.NullString
	var taskETag int64
	if err := tx.QueryRow(`SELECT meta, etag FROM tasks WHERE uuid = ?`, taskUUID).Scan(&metaText, &taskETag); err != nil {
		return err
	}
	meta := map[string]interface{}{}
	if metaText.Valid && strings.TrimSpace(metaText.String) != "" {
		if err := json.Unmarshal([]byte(metaText.String), &meta); err != nil {
			return fmt.Errorf("task meta is not valid JSON: %w", err)
		}
	}
	before, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	wf := map[string]interface{}{
		"instanceId": inst.ID,
		"taskRef":    inst.TaskRef,
		"template": map[string]interface{}{
			"id": inst.TemplateID, "version": inst.TemplateVersion, "hash": inst.TemplateHash,
		},
		"state": map[string]interface{}{
			"status": inst.Status,
		},
		"revision": inst.Revision,
		"taskDoc": map[string]interface{}{
			"etag": inst.TaskDocEtag, "hash": inst.TaskDocHash,
		},
		"updatedAt": inst.UpdatedAt,
	}
	if inst.Phase != "" {
		wf["state"].(map[string]interface{})["phase"] = inst.Phase
	}
	if inst.Outcome != "" {
		wf["state"].(map[string]interface{})["outcome"] = inst.Outcome
	}
	meta["workflow"] = wf
	b, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if bytes.Equal(before, b) {
		return nil
	}

	if _, err := tx.Exec(`UPDATE tasks SET meta = ? WHERE uuid = ?`, string(b), taskUUID); err != nil {
		return err
	}
	payload := map[string]any{"meta": string(b)}
	if err := store.StampTaskCampaignContext(tx, taskUUID, payload); err != nil {
		return err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	payloadText := string(payloadJSON)
	return events.NewWriter(nil).LogEvent(tx, &domain.Event{
		PrincipalRef: actor,
		ResourceType: "task",
		ResourceUUID: &taskUUID,
		EventType:    "task.updated",
		ETag:         &taskETag,
		Payload:      &payloadText,
	})
}
