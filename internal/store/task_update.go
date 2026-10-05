package store

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/webhooks"
)

func normalizeStateValue(value interface{}) (domain.State, error) {
	switch v := value.(type) {
	case domain.State:
		return domain.ParseState(string(v))
	case string:
		return domain.ParseState(v)
	default:
		return "", fmt.Errorf("invalid state value type %T", value)
	}
}

func normalizeStateField(fields map[string]interface{}) (string, bool, error) {
	value, ok := fields["state"]
	if !ok {
		return "", false, nil
	}
	state, err := normalizeStateValue(value)
	if err != nil {
		return "", true, err
	}
	stateString := string(state)
	fields["state"] = stateString
	return stateString, true, nil
}

// UpdateFields updates specified fields on a task and logs a task.updated event.
// Returns the new etag on success.
func (ts *TaskStore) UpdateFields(actorUUID, taskUUID string, fields map[string]interface{}, ifMatch int64) (int64, error) {
	return ts.UpdateFieldsWithViaAttribution(ts.store.attributionFromActorUUID(actorUUID), taskUUID, fields, ifMatch, "cli")
}

// UpdateFieldsWithAttribution updates fields with canonical principal attribution.
func (ts *TaskStore) UpdateFieldsWithAttribution(attr attribution.Attribution, taskUUID string, fields map[string]interface{}, ifMatch int64) (int64, error) {
	return ts.UpdateFieldsWithViaAttribution(attr, taskUUID, fields, ifMatch, "cli")
}

// UpdateFieldsWithViaAttribution updates fields and records the ingress surface.
func (ts *TaskStore) UpdateFieldsWithViaAttribution(attr attribution.Attribution, taskUUID string, fields map[string]interface{}, ifMatch int64, via string) (int64, error) {
	return ts.UpdateFieldsWithViaAttributionAndPrecondition(attr, taskUUID, fields, ifMatch, via, nil)
}

// UpdateFieldsWithViaAttributionAndPrecondition evaluates precondition inside
// the same BEGIN IMMEDIATE transaction as the task mutation. It is the fencing
// seam for authority checks whose truth must not change between validation and
// write (for example, generation-fenced task-claim completion).
func (ts *TaskStore) UpdateFieldsWithViaAttributionAndPrecondition(attr attribution.Attribution, taskUUID string, fields map[string]interface{}, ifMatch int64, via string, precondition func(*sql.Tx) error) (int64, error) {
	if err := requireAttribution(attr); err != nil {
		return 0, err
	}
	if via == "" {
		via = "cli"
	}
	// caused_by is a non-column task field: extract it before the scalar UPDATE is
	// built so it never becomes a `caused_by = ?` set clause. Its rows are replaced
	// in the same transaction below.
	var causedByUpdate *CausedByUpdate
	if raw, ok := fields[causedByFieldKey]; ok {
		cu, ok := raw.(CausedByUpdate)
		if !ok {
			return 0, fmt.Errorf("invalid caused_by value type %T", raw)
		}
		causedByUpdate = &cu
		delete(fields, causedByFieldKey)
	}
	if _, _, err := normalizeStateField(fields); err != nil {
		return 0, err
	}
	var newETag int64
	var webhooksToDispatch []pendingWebhook

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

		if _, changesResidency := fields["project_uuid"]; changesResidency {
			var owner sql.NullString
			if err := tx.QueryRow("SELECT subtask_owner_uuid FROM tasks WHERE uuid=?", taskUUID).Scan(&owner); err != nil {
				return err
			}
			if owner.Valid {
				return fmt.Errorf("named subtasks cannot move independently; move the owner")
			}
		}

		// Check etag if ifMatch was provided
		// Authority fences take precedence over optimistic metadata conflicts. If
		// a takeover changed both generation and etag after the caller's read, the
		// old holder must receive claim_superseded (with the new holder), not a
		// generic etag conflict that hides the authority change.
		if precondition != nil {
			if err := precondition(tx); err != nil {
				return err
			}
		}
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}

		// Validate re-parenting (no self-parent, no cycles, depth <= 1).
		if pv, ok := fields["parent_task_uuid"]; ok && pv != nil {
			parentUUID, _ := pv.(string)
			if err := validateParentAssignment(tx, taskUUID, parentUUID); err != nil {
				return err
			}
		}

		newState, hasStateChange := fields["state"].(string)
		stateChanged := hasStateChange && currentState != newState
		transitioningToCompletion := hasStateChange && !isCompletionState(currentState) && isCompletionState(newState)
		fieldNames := sortedFieldNames(fields)
		oldValues, err := loadTaskFieldValues(tx, taskUUID, fieldNames)
		if err != nil {
			return fmt.Errorf("failed to load old task values: %w", err)
		}

		// Completing a blocker may unblock its dependents: snapshot their
		// blockers before the write so the unblock webhook can show the delta.
		var dependents []blockedDependent
		if transitioningToCompletion {
			if dependents, err = blockedDependentsTx(tx, taskUUID); err != nil {
				return err
			}
		}

		if err := updateTaskColumnsTx(tx, attr, taskUUID, fields); err != nil {
			return err
		}
		_, enrollmentChange := fields["campaign_uuid"]
		_, projectChange := fields["project_uuid"]
		if enrollmentChange || projectChange {
			if err := validateEffectiveMembershipTx(tx, campaignValidation{
				taskUUIDs: []string{taskUUID}, enrollmentChange: enrollmentChange,
				residentAdmission: projectChange, move: projectChange,
			}); err != nil {
				return err
			}
		}

		// Replace the task's caused_by edge set (if a caused_by update was supplied).
		// Old values are captured first so the event/webhook change summary reports
		// old/new as friendly-ID arrays.
		var causedByOldIDs []string
		if causedByUpdate != nil {
			if causedByOldIDs, err = replaceCausedByTx(tx, attr, taskUUID, causedByUpdate.Refs); err != nil {
				return err
			}
		}

		// Cascade delete resident subtasks and detach external child backlinks if
		// state is being set to 'deleted'.
		if newState, ok := fields["state"]; ok && newState == "deleted" {
			if err := detachExternalSubtasks(tx, attr, taskUUID); err != nil {
				return fmt.Errorf("failed to detach external subtasks: %w", err)
			}
			if err := cascadeDeleteResidentSubtasks(tx, ew, attr, taskUUID); err != nil {
				return fmt.Errorf("failed to cascade delete resident subtasks: %w", err)
			}
		}

		// Tasks whose every blocker is now in a completion state.
		unblocked, err := newlyUnblockedTx(tx, dependents)
		if err != nil {
			return err
		}

		// Log event with structured payload. caused_by (a non-column field) is
		// merged back into the payload as a friendly-ID array so `wrkq log --patch`
		// shows it like any other field edit.
		payloadFields := make(map[string]interface{}, len(fields)+4)
		for k, v := range fields {
			payloadFields[k] = v
		}
		eventChanged := fieldNames
		eventChanges := buildWebhookChanges(oldValues, fields)
		if causedByUpdate != nil {
			newIDs := causedByUpdate.FriendlyIDs()
			payloadFields[causedByFieldKey] = newIDs
			eventChanged = append(append([]string{}, fieldNames...), causedByFieldKey)
			sort.Strings(eventChanged)
			eventChanges[causedByFieldKey] = webhooks.Change{From: causedByOldIDs, To: newIDs}
		}
		if stateChanged {
			payloadFields["state_from"] = currentState
		}
		newETag = currentETag + 1
		// Every update carries the affiliation stamp, not only a state change:
		// the project timeline places field edits (task.edited) by it too.
		meta, err := logTaskEvent(tx, ew, attr, taskUUID, "task.updated", &newETag, payloadFields)
		if err != nil {
			return err
		}
		if outcome, ok := fields["outcome"]; ok {
			outcomePayload := map[string]any{"task_uuid": taskUUID, "outcome": outcome}
			if _, err := logTaskEvent(tx, ew, attr, taskUUID, "task.outcome_set", &newETag, outcomePayload); err != nil {
				return err
			}
		}
		var transition *webhooks.Transition
		if stateChanged {
			transition = &webhooks.Transition{From: stringPtr(currentState), To: stringPtr(newState)}
		}
		webhooksToDispatch = append(webhooksToDispatch, pendingWebhook{
			taskUUID: taskUUID,
			ctx: webhooks.EventContext{
				Metadata:     meta,
				Event:        "updated",
				PrincipalRef: attr.PrincipalRef,
				Via:          via,
				Transition:   transition,
				Changed:      eventChanged,
				Changes:      eventChanges,
			},
		})

		unblockHooks, err := logUnblockedTx(tx, ew, attr, via, taskUUID, newState, unblocked)
		if err != nil {
			return err
		}
		webhooksToDispatch = append(webhooksToDispatch, unblockHooks...)

		if transitioningToCompletion {
			if err := maybeLogCampaignCloseNudgeForTask(tx, ew, attr, taskUUID); err != nil {
				return err
			}
		}
		return nil
	})

	if err == nil {
		dispatchTaskWebhooks(ts.store.db, webhooksToDispatch)
	}
	return newETag, err
}

// updateTaskColumnsTx writes the scalar column changes, bumps the etag and
// stamps the updater (and the deleter, for a move to deleted).
func updateTaskColumnsTx(tx *sql.Tx, attr attribution.Attribution, taskUUID string, fields map[string]interface{}) error {
	var setClauses []string
	var args []interface{}
	for key, value := range fields {
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", key))
		args = append(args, value)
	}
	setClauses = append(setClauses, "etag = etag + 1", "updated_by_principal_ref = ?", "updated_by_scope_ref = ?")
	args = append(args, attr.PrincipalRef, scopeSQL(attr))
	if newState, ok := fields["state"]; ok && newState == "deleted" {
		setClauses = append(setClauses, "deleted_by_principal_ref = ?", "deleted_by_scope_ref = ?")
		args = append(args, attr.PrincipalRef, scopeSQL(attr))
	}
	args = append(args, taskUUID)
	query := fmt.Sprintf("UPDATE tasks SET %s WHERE uuid = ?", strings.Join(setClauses, ", "))
	if _, err := tx.Exec(query, args...); err != nil {
		return fmt.Errorf("failed to update task: %w", err)
	}
	return nil
}

// replaceCausedByTx swaps the task's caused_by edge set for refs and returns
// the friendly IDs it replaced.
func replaceCausedByTx(tx *sql.Tx, attr attribution.Attribution, taskUUID string, refs []CausedByRef) ([]string, error) {
	oldIDs, err := CausedByIDs(tx, taskUUID)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec("DELETE FROM task_causes WHERE task_uuid = ?", taskUUID); err != nil {
		return nil, fmt.Errorf("failed to clear caused_by: %w", err)
	}
	if err := insertCausedByRows(tx, causedByAttribution{
		principalRef: attr.PrincipalRef,
		scope:        scopeSQL(attr),
	}, taskUUID, refs); err != nil {
		return nil, err
	}
	return oldIDs, nil
}

// validateParentAssignment enforces the invariants for re-parenting a task:
// the parent must exist, a task may not be its own parent, and the single-level
// subtask depth (max depth 1) must hold globally across containers/projects —
// neither the parent being a subtask nor the child already having subtasks.
func validateParentAssignment(tx *sql.Tx, childUUID, parentUUID string) error {
	if parentUUID == childUUID {
		return fmt.Errorf("a task cannot be its own parent")
	}

	var childExists int
	if err := tx.QueryRow("SELECT COUNT(*) FROM tasks WHERE uuid = ?", childUUID).Scan(&childExists); err != nil {
		return fmt.Errorf("failed to load task: %w", err)
	}
	if childExists == 0 {
		return fmt.Errorf("task not found: %s", childUUID)
	}

	var parentParent sql.NullString
	err := tx.QueryRow("SELECT parent_task_uuid FROM tasks WHERE uuid = ?", parentUUID).Scan(&parentParent)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("parent task not found: %s", parentUUID)
		}
		return fmt.Errorf("failed to load parent task: %w", err)
	}

	if parentParent.Valid && parentParent.String != "" {
		return fmt.Errorf("parent task is itself a subtask (max depth is 1)")
	}

	var childHasSubtasks int
	if err := tx.QueryRow(
		"SELECT COUNT(*) FROM tasks WHERE parent_task_uuid = ? AND state != 'deleted'",
		childUUID,
	).Scan(&childHasSubtasks); err != nil {
		return fmt.Errorf("failed to check for existing subtasks: %w", err)
	}
	if childHasSubtasks > 0 {
		return fmt.Errorf("cannot reparent a task that already has subtasks (max depth is 1)")
	}

	return nil
}
