//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attach"
	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/taskmember"
	"github.com/lherron/wrkq/internal/webhooks"
)

// Task lifecycle verbs past the edit: acknowledge a terminal task, delete or
// archive or purge it, and restore it with its subtasks.

// TaskAcknowledge records a terminal-state receipt (acknowledged_at). It mirrors
// internal/rpccli/ack.go: state must be completed|cancelled unless force is set.
// An already-acknowledged task is a no-op — the current DTO is returned with its
// stable acknowledgedAt and no new write / etag bump.
func (a *API) TaskAcknowledge(ctx context.Context, p TaskAcknowledgeParams) (*WrkqTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uuid, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}

	var state string
	var acknowledgedAt sql.NullString
	if scanErr := a.db.QueryRow("SELECT state, acknowledged_at FROM tasks WHERE uuid = ?", uuid).Scan(&state, &acknowledgedAt); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return nil, NewNotFoundError(p.Task, "task")
		}
		return nil, NewInternalError(scanErr)
	}

	if acknowledgedAt.Valid && strings.TrimSpace(acknowledgedAt.String) != "" {
		return a.loadTask(ctx, uuid)
	}

	if !p.Force && state != string(domain.StateCompleted) && state != string(domain.StateCancelled) {
		return nil, NewValidationError(
			"cannot acknowledge task: state is "+state+" (requires completed or cancelled)",
			map[string]any{"field": "state", "state": state},
		)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	attr, aerr := a.attributionFor(p.Actor)
	if aerr != nil {
		return nil, aerr
	}
	if _, uerr := a.store.Tasks.UpdateFieldsWithViaAttribution(attr, uuid, map[string]any{"acknowledged_at": now}, 0, "rpc"); uerr != nil {
		return nil, mapStoreError(uerr, p.Task)
	}
	return a.loadTask(ctx, uuid)
}

// TaskDelete disposes of a task per the caller-owned-confirmation invariant
// (architecture/records/invariants/wrkq.mutation.caller-owned-confirmation.yaml):
// the disposition is the EXPLICIT, caller-supplied mode — the server never
// prompts, inspects a TTY, reads stdin, or infers confirmation from transport.
//
//   - mode "" (legacy, PRESERVED): reversible delete — state='deleted' +
//     deleted_at=now, cascading to subtasks. Never sets archived_at, never
//     purges. Re-deleting an already-deleted task is a no-op.
//   - mode "archive": soft-archive — state='archived' + archived_at (legacy
//     `wrkq rm` default). Re-archiving is the store no-op.
//   - mode "purge": hard-delete the task + clean attachment files and the task
//     attachment dir (legacy `wrkq rm --purge`). Irreversible; never a default.
//   - any other mode: WRKQ_VALIDATION.
func (a *API) TaskDelete(ctx context.Context, p TaskDeleteParams) (*WrkqTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mode := strings.TrimSpace(p.Mode)
	switch mode {
	case "", "archive", "purge":
	default:
		return nil, NewValidationError("invalid mode: "+p.Mode+" (expected archive or purge)", map[string]any{"field": "mode"})
	}

	uuid, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}

	var state string
	if scanErr := a.db.QueryRow("SELECT state FROM tasks WHERE uuid = ?", uuid).Scan(&state); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return nil, NewNotFoundError(p.Task, "task")
		}
		return nil, NewInternalError(scanErr)
	}

	attr, aerr := a.attributionFor(p.Actor)
	if aerr != nil {
		return nil, aerr
	}

	switch mode {
	case "archive":
		if _, aerr := a.store.Tasks.ArchiveWithViaAttribution(attr, uuid, 0, "rpc"); aerr != nil {
			return nil, mapStoreError(aerr, p.Task)
		}
		return a.loadTask(ctx, uuid)
	case "purge":

		snapshot, serr := a.loadTask(ctx, uuid)
		if serr != nil {
			return nil, serr
		}

		attachments, gerr := a.store.Tasks.GetAttachments(uuid)
		if gerr != nil {
			return nil, NewInternalError(gerr)
		}
		if _, perr := a.store.Tasks.PurgeWithAttribution(attr, uuid, 0); perr != nil {
			return nil, mapStoreError(perr, p.Task)
		}

		if strings.TrimSpace(a.attachDir) != "" {
			for _, at := range attachments {
				if at.RelativePath == "" {
					continue
				}
				_ = attach.DeleteFile(a.attachDir, at.RelativePath)
			}
			_ = attach.DeleteTaskDir(a.attachDir, uuid)
		}
		return snapshot, nil
	default:

		if state == string(domain.StateDeleted) {
			return a.loadTask(ctx, uuid)
		}
		now := time.Now().UTC().Format(time.RFC3339)

		if _, uerr := a.store.Tasks.UpdateFieldsWithViaAttribution(attr, uuid, map[string]any{
			"state":      string(domain.StateDeleted),
			"deleted_at": now,
		}, 0, "rpc"); uerr != nil {
			return nil, mapStoreError(uerr, p.Task)
		}
		return a.loadTask(ctx, uuid)
	}
}

// TaskRestore reverses delete/archive: current state must be archived or deleted,
// the target defaults to open (archived/deleted targets rejected), archived_at /
// deleted_at / deleted_by are cleared, and subtasks are cascade-restored. Mirrors
// internal/rpccli/restore.go.
func (a *API) TaskRestore(ctx context.Context, p TaskRestoreParams) (*WrkqTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	targetState := string(domain.StateOpen)
	if strings.TrimSpace(p.State) != "" {
		parsed, perr := domain.ParseState(p.State)
		if perr != nil {
			return nil, NewValidationError(perr.Error(), map[string]any{"field": "state"})
		}
		if parsed == domain.StateArchived || parsed == domain.StateDeleted {
			return nil, NewValidationError("cannot restore to "+p.State+" state", map[string]any{"field": "state"})
		}
		targetState = string(parsed)
	}

	if p.Priority != 0 {
		if verr := domain.ValidatePriority(p.Priority); verr != nil {
			return nil, NewValidationError(verr.Error(), map[string]any{"field": "priority"})
		}
	}

	if strings.TrimSpace(p.Labels) != "" {
		var labels []string
		if jerr := json.Unmarshal([]byte(p.Labels), &labels); jerr != nil {
			return nil, NewValidationError("invalid labels JSON: "+jerr.Error(), map[string]any{"field": "labels"})
		}
	}

	// Resolve assignee (legacy attribution.NormalizeCompat).
	var assigneePrincipalRef *string
	if strings.TrimSpace(p.Assignee) != "" {
		principalRef, aerr := attribution.NormalizeCompat(p.Assignee)
		if aerr != nil {
			return nil, NewValidationError("failed to resolve assignee: "+aerr.Error(), map[string]any{"field": "assignee"})
		}
		assigneePrincipalRef = &principalRef
	}

	uuid, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}

	var currentState string
	var currentEtag int64
	if scanErr := a.db.QueryRow("SELECT state, etag FROM tasks WHERE uuid = ?", uuid).Scan(&currentState, &currentEtag); scanErr != nil {
		if scanErr == sql.ErrNoRows {
			return nil, NewNotFoundError(p.Task, "task")
		}
		return nil, NewInternalError(scanErr)
	}
	if currentState != string(domain.StateArchived) && currentState != string(domain.StateDeleted) {
		return nil, NewValidationError(
			"task is not deleted or archived (current state: "+currentState+")",
			map[string]any{"field": "state", "state": currentState},
		)
	}

	if p.IfMatch != 0 && p.IfMatch != currentEtag {
		return nil, NewConflictError(
			"etag mismatch: expected "+strconv.FormatInt(p.IfMatch, 10)+", got "+strconv.FormatInt(currentEtag, 10),
			map[string]any{"expectEtag": p.IfMatch, "currentEtag": currentEtag},
		)
	}

	// --to (move-on-restore): resolve the destination parent + final slug and
	// check for a slug conflict at the destination, exactly as legacy restore.go.
	var newProjectUUID *string
	var newSlug *string
	if strings.TrimSpace(p.ToPath) != "" {
		parentUUID, slug, _, derr := selectors.ResolveParentContainer(a.db, p.ToPath)
		if derr != nil {
			return nil, NewValidationError("failed to resolve destination: "+derr.Error(), map[string]any{"field": "toPath"})
		}
		newProjectUUID = parentUUID
		newSlug = &slug

		var existingUUID string
		cerr := a.db.QueryRow(
			"SELECT uuid FROM tasks WHERE project_uuid = ? AND slug = ? AND uuid != ? AND "+taskmember.Filter("", false),
			*parentUUID, slug, uuid,
		).Scan(&existingUUID)
		if cerr == nil {
			return nil, NewConflictError(
				"slug conflict: task with slug '"+slug+"' already exists at destination",
				map[string]any{"field": "slug", "slug": slug},
			)
		} else if cerr != sql.ErrNoRows {
			return nil, NewInternalError(cerr)
		}
	}

	attr, aerr := a.attributionFor(p.Actor)
	if aerr != nil {
		return nil, aerr
	}
	opts := restoreOptions{
		targetState:          targetState,
		newProjectUUID:       newProjectUUID,
		newSlug:              newSlug,
		newTitle:             p.Title,
		newDescription:       p.Description,
		newPriority:          p.Priority,
		newLabels:            p.Labels,
		assigneePrincipalRef: assigneePrincipalRef,
		comment:              p.Comment,
	}
	webhookCtx, rerr := a.restoreTaskTx(uuid, opts, attr)
	if rerr != nil {
		return nil, rerr
	}

	webhooks.DispatchTaskEvent(a.db, uuid, webhookCtx)

	if rerr := a.cascadeRestoreSubtasks(uuid, targetState, attr); rerr != nil {
		return nil, rerr
	}
	return a.loadTask(ctx, uuid)
}

// restoreTaskTx clears the archived/deleted markers and sets the target state for
// a single task within a transaction, applying any move / field updates / comment,
// and logging a task.restored event. Mirrors internal/cli restoreTaskWithOptions.
// The legacy UPDATE intentionally does NOT bump etag, so this method preserves it
// for byte-parity of the durable snapshot. It returns the webhooks.EventContext
// the caller dispatches after commit (matching legacy: the root task AND every
// cascade-restored subtask dispatch the restore webhook). Via is "rpc" — the
// RPC-mutation convention threaded through the store (e.g.
// UpdateFieldsWithViaAttribution(..., "rpc")) — distinct from the legacy "cli".
func (a *API) restoreTaskTx(taskUUID string, opts restoreOptions, attr attribution.Attribution) (webhooks.EventContext, error) {
	tx, err := a.db.Begin()
	if err != nil {
		return webhooks.EventContext{}, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	var currentState string
	if scanErr := tx.QueryRow("SELECT state FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentState); scanErr != nil {
		return webhooks.EventContext{}, NewInternalError(scanErr)
	}

	query := `UPDATE tasks SET state = ?, archived_at = NULL, deleted_at = NULL,
		deleted_by_principal_ref = NULL, deleted_by_scope_ref = NULL,
		updated_by_principal_ref = ?, updated_by_scope_ref = ?`
	args := []any{opts.targetState, attr.PrincipalRef, scopeBind(attr)}

	fields := map[string]any{
		"state":       opts.targetState,
		"archived_at": nil,
		"deleted_at":  nil,
	}

	if opts.newProjectUUID != nil {
		query += `, project_uuid = ?`
		args = append(args, *opts.newProjectUUID)
		fields["project_uuid"] = *opts.newProjectUUID
	}
	if opts.newSlug != nil {
		query += `, slug = ?`
		args = append(args, *opts.newSlug)
		fields["slug"] = *opts.newSlug
	}
	if opts.newTitle != "" {
		query += `, title = ?`
		args = append(args, opts.newTitle)
		fields["title"] = opts.newTitle
	}
	if opts.newDescription != "" {
		query += `, description = ?`
		args = append(args, opts.newDescription)
		fields["description"] = map[string]any{"length": len(opts.newDescription)}
	}
	if opts.newPriority != 0 {
		query += `, priority = ?`
		args = append(args, opts.newPriority)
		fields["priority"] = opts.newPriority
	}
	if opts.newLabels != "" {
		query += `, labels = ?`
		args = append(args, opts.newLabels)
		fields["labels"] = opts.newLabels
	}
	if opts.assigneePrincipalRef != nil {
		query += `, assignee_principal_ref = ?`
		args = append(args, *opts.assigneePrincipalRef)
		fields["assignee_principal_ref"] = *opts.assigneePrincipalRef
	}
	query += ` WHERE uuid = ?`
	args = append(args, taskUUID)

	if _, eerr := tx.Exec(query, args...); eerr != nil {
		return webhooks.EventContext{}, NewInternalError(eerr)
	}

	payloadMap := map[string]any{"action": "restored", "target_state": opts.targetState}
	if opts.newProjectUUID != nil {
		payloadMap["moved_to"] = *opts.newProjectUUID
	}
	if eerr := store.StampTaskCampaignContext(tx, taskUUID, payloadMap); eerr != nil {
		return webhooks.EventContext{}, NewInternalError(eerr)
	}
	payloadJSON, _ := json.Marshal(payloadMap)
	payload := string(payloadJSON)
	eventMeta, eerr := events.NewWriter(a.db.DB).LogEventReturning(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "task",
		ResourceUUID: &taskUUID,
		EventType:    "task.restored",
		Payload:      &payload,
	})
	if eerr != nil {
		return webhooks.EventContext{}, NewInternalError(eerr)
	}

	var commentResult *store.CommentCreateResult

	if opts.comment != "" {
		commentResult, eerr = a.store.Comments.CreateTxWithAttribution(
			tx,
			events.NewWriter(a.db.DB),
			attr,
			store.CommentCreateParams{TaskUUID: taskUUID, Body: opts.comment},
		)
		if eerr != nil {
			return webhooks.EventContext{}, NewInternalError(eerr)
		}
	}

	if cerr := tx.Commit(); cerr != nil {
		return webhooks.EventContext{}, NewInternalError(cerr)
	}
	if commentResult != nil {
		webhooks.DispatchCommentCreated(a.db, taskUUID, commentResult.EventMeta, attr.PrincipalRef, "rpc")
	}

	return webhooks.EventContext{
		Metadata:     eventMeta,
		Event:        "updated",
		PrincipalRef: attr.PrincipalRef,
		Via:          "rpc",
		Transition:   &webhooks.Transition{From: &currentState, To: &opts.targetState},
		Changed:      webhookSortedKeys(fields),
		Changes:      webhookMapChanges(fields, map[string]any{"state": currentState}),
	}, nil
}

// cascadeRestoreSubtasks restores archived/deleted resident subtasks of a
// parent, dispatching the restore webhook for EACH restored subtask. Cross-
// project parent edges are backlinks, not containment, so external children are
// not restored through the parent.
func (a *API) cascadeRestoreSubtasks(parentTaskUUID, targetState string, attr attribution.Attribution) error {
	rows, err := a.db.Query(
		`SELECT c.uuid
		   FROM tasks c
		   JOIN tasks p ON p.uuid = c.parent_task_uuid
		  WHERE c.parent_task_uuid = ?
		    AND c.project_uuid = p.project_uuid
		    AND c.state IN ('archived', 'deleted') AND `+taskmember.Filter("c", false),
		parentTaskUUID,
	)
	if err != nil {
		return NewInternalError(err)
	}
	var subtaskUUIDs []string
	for rows.Next() {
		var u string
		if serr := rows.Scan(&u); serr != nil {
			_ = rows.Close()
			return NewInternalError(serr)
		}
		subtaskUUIDs = append(subtaskUUIDs, u)
	}
	_ = rows.Close()
	if rerr := rows.Err(); rerr != nil {
		return NewInternalError(rerr)
	}

	for _, subtaskUUID := range subtaskUUIDs {
		webhookCtx, rerr := a.restoreTaskTx(subtaskUUID, restoreOptions{targetState: targetState}, attr)
		if rerr != nil {
			return rerr
		}
		webhooks.DispatchTaskEvent(a.db, subtaskUUID, webhookCtx)
		if rerr := a.cascadeRestoreSubtasks(subtaskUUID, targetState, attr); rerr != nil {
			return rerr
		}
	}
	return nil
}

// webhookSortedKeys returns the field keys in sorted order (mirrors legacy
// sortedMapKeys, driving the webhook Changed slice deterministically).
func webhookSortedKeys(fields map[string]any) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// webhookMapChanges builds the webhook Changes map (from oldValues, to fields),
// mirroring legacy mapChanges.
func webhookMapChanges(fields map[string]any, oldValues map[string]any) map[string]webhooks.Change {
	changes := make(map[string]webhooks.Change, len(fields))
	for _, key := range webhookSortedKeys(fields) {
		changes[key] = webhooks.Change{From: oldValues[key], To: fields[key]}
	}
	return changes
}
