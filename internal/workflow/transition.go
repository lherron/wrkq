//go:build wrkq_local

package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/webhooks"
)

func (s *Service) Transition(taskSelector, transitionID string, opts TransitionOptions) (TransitionResult, error) {
	return s.TransitionForSelectors(taskSelector, "", transitionID, opts)
}

// activeActionRunIDsTx returns the ids of open (active) action runs on the
// instance. An open action run is the seat's lease; an operator-class
// transition (requiresNoActiveRun) refuses while one is live.
func activeActionRunIDsTx(tx *sql.Tx, instanceID string) ([]string, error) {
	rows, err := tx.Query(`SELECT id FROM workflow_runs WHERE instance_id = ? AND status = 'active' ORDER BY started_at`, instanceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Service) TransitionForSelectors(taskSelector, instanceID, transitionID string, opts TransitionOptions) (TransitionResult, error) {
	if opts.RunChecks {
		return nil, validationError(
			"runChecks",
			"transition.apply does not execute checks",
			"persisted check run ids in checkIds",
			nil,
			"call check.run first and pass every returned id in checkIds",
		)
	}
	if err := s.EnsureBuiltinTemplateForSelectors(taskSelector, instanceID, opts.PrincipalRef); err != nil {
		return nil, err
	}
	requestHash := transitionRequestHash(taskSelector, instanceID, transitionID, opts)
	var result TransitionResult
	var webhookCtx *webhooks.EventContext
	var webhookTaskUUID string
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		inst, err := resolveInstanceSelectors(tx, taskSelector, instanceID)
		if err != nil {
			return err
		}
		resultTaskSelector := taskSelector
		if strings.TrimSpace(resultTaskSelector) == "" {
			resultTaskSelector = inst.TaskRef
		}
		if opts.IdempotencyKey != "" {
			replayed, err := replayTransitionResult(tx, inst.ID, opts.IdempotencyKey, requestHash)
			if err != nil {
				return err
			}
			if replayed != nil {
				result = replayed
				return nil
			}
		}
		if opts.ExpectRevision != nil && *opts.ExpectRevision != inst.Revision {
			return staleRevisionError(inst.ID, *opts.ExpectRevision, inst.Revision)
		}
		tpl, _, err := s.ShowTemplate(inst.TemplateID + "@" + inst.TemplateVersion)
		if err != nil {
			return err
		}
		tr, err := findTransition(tpl, transitionID)
		if err != nil {
			return err
		}

		if tr.RequiresNoActiveRun {
			activeRuns, err := activeActionRunIDsTx(tx, inst.ID)
			if err != nil {
				return err
			}
			if len(activeRuns) > 0 {
				return activeRunGuardError(inst.ID, transitionID, activeRuns)
			}
		}
		task, err := loadTaskDoc(tx, inst.TaskUUID)
		if err != nil {
			return err
		}
		ev, err := listInstanceEvidence(tx, inst.ID)
		if err != nil {
			return err
		}
		obl, err := listObligations(tx, inst.ID, true)
		if err != nil {
			return err
		}
		checks := map[string]CheckRun{}
		for _, checkID := range tr.Checks {
			if latest, ok := latestCheckFor(s.db, inst.ID, tr.ID, checkID); ok {
				checks[checkID] = latest
			}
		}
		for _, id := range opts.CheckIDs {
			c, err := s.ShowCheckRun(id)
			if err != nil {
				return err
			}
			checks[c.CheckID] = *c
		}
		decision, err := s.EvaluateTransitionDecision(TransitionDecisionInput{
			Instance:           inst,
			Template:           tpl,
			Transition:         *tr,
			Task:               task,
			Evidence:           ev,
			Obligations:        obl,
			Checks:             checks,
			Role:               opts.Role,
			PrincipalRef:       opts.PrincipalRef,
			RoleQuery:          tx,
			DependencyQuery:    tx,
			CheckDatabase:      s.db,
			RequireRoleBinding: true,
		})
		if err != nil {
			return err
		}
		if !decision.Legal {
			return transitionDecisionError(inst.ID, transitionID, opts.Role, decision)
		}
		chosen := decision.Outcome
		if opts.DryRun {
			result = map[string]interface{}{"dryRun": true, "transition": transitionID, "outcome": chosen.ID, "state": *decision.ExpectedState}
			if chosen.Suspend != nil {
				result["suspend"] = chosen.Suspend
			}
			return nil
		}

		expectedRevision := inst.Revision
		if opts.ExpectRevision != nil {
			expectedRevision = *opts.ExpectRevision
		}
		updated, committed, eventMeta, err := commitTransitionOutcomeTx(tx, transitionCommit{
			inst: inst, task: task, outcome: chosen, transitionID: transitionID, resultTask: resultTaskSelector,
			principalRef: opts.PrincipalRef, role: opts.Role, runID: opts.RunID,
			idempotencyKey: opts.IdempotencyKey, requestHash: requestHash,
			now: s.now().Format(time.RFC3339), expectedRevision: expectedRevision,
		})
		if err != nil {
			return err
		}
		result = committed
		ctx := workflowTransitionWebhookContext(eventMeta, updated, opts.PrincipalRef, opts.Role, opts.RunID, transitionID, chosen.ID, inst.Revision, updated.Revision, opts.IdempotencyKey, inst.State(), updated.State())
		if chosen.Suspend != nil {
			ctx = workflowSuspensionWebhookContext(eventMeta, updated, opts.PrincipalRef, opts.Role, opts.RunID, inst.Revision, updated.Revision, opts.IdempotencyKey)
		}
		webhookCtx = &ctx
		webhookTaskUUID = updated.TaskUUID
		return nil
	})
	if err != nil {
		return nil, err
	}
	if webhookCtx != nil && webhookTaskUUID != "" {
		webhooks.DispatchTaskEvent(s.db, webhookTaskUUID, *webhookCtx)
	}
	if result != nil {
		result, err = s.deliverBuiltinTransitionEffects(result, transitionID)
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

// transitionCommit is one legal outcome to commit at inst's revision.
type transitionCommit struct {
	inst           *Instance
	task           *taskDoc
	outcome        *OutcomeCase
	transitionID   string
	resultTask     string // the "task" the result reports
	principalRef   string
	role           string
	runID          string
	idempotencyKey string
	requestHash    string
	now            string
	// expectedRevision is reported when the revision CAS loses.
	expectedRevision int64
}

// commitTransitionOutcomeTx applies a decided outcome: it moves the instance
// to the outcome's state (or suspends it) under a revision CAS, opens the
// outcome's obligations, enqueues its effects, records the mutation event
// with the replayable result, and mirrors the state onto the task. Legality
// is the caller's decision; this only writes.
func commitTransitionOutcomeTx(tx *sql.Tx, c transitionCommit) (Instance, map[string]interface{}, workflowEventMetadata, error) {
	inst, chosen := c.inst, c.outcome
	if inst.Suspension != nil {
		return Instance{}, nil, workflowEventMetadata{}, suspendedWriteError(inst)
	}
	eventID, err := nextSeqID(tx, "workflow_event_seq", "wfe")
	if err != nil {
		return Instance{}, nil, workflowEventMetadata{}, err
	}
	updated := *inst
	if chosen.To != nil {
		updated.Status = chosen.To.Status
		updated.Phase = chosen.To.Phase
		updated.Outcome = chosen.To.Outcome
	} else if err := applySuspendOutcomeTx(tx, &updated, chosen.Suspend, eventID, c.now); err != nil {
		return Instance{}, nil, workflowEventMetadata{}, err
	}
	updated.Revision = inst.Revision + 1
	updated.UpdatedAt = c.now
	if updated.Status == "closed" {
		updated.ClosedAt = c.now
	} else {
		updated.ClosedAt = ""
	}
	updated.TaskDocEtag = fmt.Sprint(c.task.ETag)
	updated.TaskDocHash = taskDocHash(c.task)
	res, err := tx.Exec(`
		UPDATE workflow_instances
		SET status = ?, phase = ?, outcome = ?, revision = ?, task_doc_etag = ?, task_doc_hash = ?,
		    updated_at = ?, closed_at = ?, suspension_id = ?, suspension_reason = ?, suspension_at = ?, suspension_cause_ref = ?
		WHERE id = ? AND revision = ?
	`, updated.Status, nullIfEmpty(updated.Phase), nullIfEmpty(updated.Outcome), updated.Revision, updated.TaskDocEtag, updated.TaskDocHash,
		updated.UpdatedAt, nullIfEmpty(updated.ClosedAt), suspensionID(updated.Suspension), suspensionReason(updated.Suspension), suspensionAt(updated.Suspension), suspensionCauseRef(updated.Suspension), updated.ID, inst.Revision)
	if err != nil {
		return Instance{}, nil, workflowEventMetadata{}, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return Instance{}, nil, workflowEventMetadata{}, err
	}
	if affected != 1 {
		actual, loadErr := instanceRevisionTx(tx, updated.ID)
		if loadErr != nil {
			return Instance{}, nil, workflowEventMetadata{}, loadErr
		}
		return Instance{}, nil, workflowEventMetadata{}, staleRevisionError(updated.ID, c.expectedRevision, actual.revision)
	}

	createdObligations, err := insertOutcomeObligationsTx(tx, updated.ID, chosen.Obligations, c.now)
	if err != nil {
		return Instance{}, nil, workflowEventMetadata{}, err
	}
	createdEffects, err := enqueueRenderedEffectsTx(tx, updated, chosen.Effects, chosen.ID, c.runID, c.now)
	if err != nil {
		return Instance{}, nil, workflowEventMetadata{}, err
	}
	result := transitionResultMap(c.resultTask, updated, eventID, createdEffects, createdObligations)
	result["idempotent"] = false
	result["transition"] = c.transitionID
	result["outcome"] = chosen.ID
	eventPayload := map[string]interface{}{"transition": c.transitionID, "outcome": chosen.ID, "from": inst.State(), "to": updated.State()}
	eventType := "workflow.transitioned"
	if chosen.Suspend != nil {
		eventType = "workflow.suspended"
		eventPayload["suspension"] = updated.Suspension
		eventPayload["beforeRevision"] = inst.Revision
		eventPayload["afterRevision"] = updated.Revision
	}
	resultJSON, _ := json.Marshal(result)
	eventMeta, err := insertWorkflowMutationEventWithID(tx, eventType, eventID, updated.ID, c.principalRef, c.role, c.runID, inst.Revision, updated.Revision, c.idempotencyKey, c.requestHash, string(resultJSON), c.task.ETag, updated.TaskDocHash, eventPayload)
	if err != nil {
		return Instance{}, nil, workflowEventMetadata{}, err
	}
	if chosen.Suspend == nil {
		if err := updateTaskWorkflowMeta(tx, updated.TaskUUID, updated, c.principalRef); err != nil {
			return Instance{}, nil, workflowEventMetadata{}, err
		}
	}
	return updated, result, eventMeta, nil
}

func instanceRevisionTx(tx *sql.Tx, instanceID string) (instanceRevision, error) {
	var out instanceRevision
	err := tx.QueryRow(`SELECT revision FROM workflow_instances WHERE id = ?`, instanceID).Scan(&out.revision)
	return out, err
}

func transitionRequestHash(taskSelector, instanceID, transitionID string, opts TransitionOptions) string {
	req := struct {
		Task           string   `json:"task"`
		InstanceID     string   `json:"instanceId,omitempty"`
		Transition     string   `json:"transition"`
		PrincipalRef   string   `json:"principal_ref,omitempty"`
		Role           string   `json:"role,omitempty"`
		ExpectRevision *int64   `json:"expectRevision,omitempty"`
		IdempotencyKey string   `json:"idempotencyKey,omitempty"`
		CheckIDs       []string `json:"checkIds,omitempty"`
		RunChecks      bool     `json:"runChecks,omitempty"`
		DryRun         bool     `json:"dryRun,omitempty"`
	}{
		Task: taskSelector, InstanceID: instanceID, Transition: transitionID, PrincipalRef: opts.PrincipalRef, Role: opts.Role,
		ExpectRevision: opts.ExpectRevision, IdempotencyKey: opts.IdempotencyKey,
		CheckIDs: append([]string(nil), opts.CheckIDs...), RunChecks: opts.RunChecks, DryRun: opts.DryRun,
	}
	b, _ := json.Marshal(req)
	return Hash(b)
}

func replayTransitionResult(tx *sql.Tx, instanceID, key, requestHash string) (map[string]interface{}, error) {
	var storedHash, resultJSON string
	err := tx.QueryRow(`
		SELECT COALESCE(request_hash,''), COALESCE(result_json,'')
		FROM workflow_events
		WHERE instance_id = ? AND idempotency_key = ?
	`, instanceID, key).Scan(&storedHash, &resultJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if storedHash != requestHash {
		return nil, idempotencyMismatchError(key)
	}
	if strings.TrimSpace(resultJSON) == "" {
		return nil, fmt.Errorf("idempotency result missing for key %q", key)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(resultJSON), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func transitionResultMap(taskSelector string, updated Instance, eventID string, effects []Effect, obligations []Obligation) map[string]interface{} {
	return map[string]interface{}{
		"task":        taskSelector,
		"instanceId":  updated.ID,
		"state":       updated.State(),
		"revision":    updated.Revision,
		"eventId":     eventID,
		"effects":     effects,
		"obligations": obligations,
		"instance":    updated,
	}
}

func insertWorkflowMutationEventWithID(tx *sql.Tx, eventType, id, instanceID, actor, role, runID string, observed, next int64, key, requestHash, resultJSON string, taskETag int64, taskHash string, payload interface{}) (workflowEventMetadata, error) {
	var seq int64
	_ = tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM workflow_events WHERE instance_id = ?`, instanceID).Scan(&seq)
	payloadJSON, _ := json.Marshal(payload)
	prevHash := previousEventHashTx(tx, instanceID)
	eventHash := chainedEventHash(prevHash, payloadJSON)
	_, err := tx.Exec(`
		INSERT INTO workflow_events (
			id, instance_id, seq, schema_version, type, actor, principal_ref, role, run_id,
			observed_revision, next_revision, task_doc_etag, task_doc_hash,
			idempotency_key, request_hash, result, result_json, payload_json, prev_event_hash, event_hash
		) VALUES (?, ?, ?, 'wrkf.workflow-event.v0', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'committed', ?, ?, ?, ?)
	`, id, instanceID, seq, eventType, emptyToNil(actor), emptyToNil(actor), emptyToNil(role), emptyToNil(runID), observed, next, fmt.Sprint(taskETag), taskHash, emptyToNil(key), nullIfEmpty(requestHash), nullIfEmpty(resultJSON), string(payloadJSON), nullIfEmpty(prevHash), eventHash)
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
		Type:          eventType,
		CreatedAt:     createdAt,
		Payload:       payload,
	}, nil
}
