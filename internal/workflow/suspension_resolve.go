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

// Suspension resolution dispositions.
const (
	DispositionResume = "resume"
	DispositionClose  = "close"
	DispositionCancel = "cancel"
)

// dispositionTargetState maps a disposition to the instance state it lands in.
// resume returns (nil, true): the instance stays exactly where it parked, so
// there is no target state to apply — clearing the suspension is the whole
// effect. close and cancel terminalize the instance.
func dispositionTargetState(disposition string) (*State, bool) {
	switch disposition {
	case DispositionResume:
		return nil, true
	case DispositionClose:
		return &State{Status: "closed", Outcome: "done"}, true
	case DispositionCancel:
		return &State{Status: "closed", Outcome: "cancelled"}, true
	default:
		return nil, false
	}
}

// ResolveSuspension resolves the active suspension named by params.SuspensionID
// with the given disposition, atomically. It is the one write that a suspended
// instance accepts.
func (s *Service) ResolveSuspension(params ResolveSuspensionParams) (map[string]interface{}, error) {
	suspensionID := strings.TrimSpace(params.SuspensionID)
	if suspensionID == "" {
		return nil, validationError("suspensionId", "a suspension id is required", "active suspension id", nil, "supply the suspension id to resolve")
	}
	disposition := strings.TrimSpace(params.Disposition)
	target, ok := dispositionTargetState(disposition)
	if !ok {
		return nil, validationError("disposition", fmt.Sprintf("unknown disposition %q", disposition),
			"resume, close, or cancel", []string{DispositionResume, DispositionClose, DispositionCancel},
			"resolve with resume, close, or cancel")
	}

	var result map[string]interface{}
	var webhookCtx *webhooks.EventContext
	var webhookTaskUUID string
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {

		inst, err := instanceBySuspensionIDTx(tx, suspensionID)
		if err != nil {
			return err
		}
		if params.ExpectRevision != nil && *params.ExpectRevision != inst.Revision {
			return staleRevisionError(inst.ID, *params.ExpectRevision, inst.Revision)
		}
		tpl, _, err := s.ShowTemplate(inst.TemplateID + "@" + inst.TemplateVersion)
		if err != nil {
			return err
		}

		eventID, err := nextSeqID(tx, "workflow_event_seq", "wfe")
		if err != nil {
			return err
		}
		now := s.now().Format(time.RFC3339)
		priorSuspension := *inst.Suspension
		resolved := *inst
		terminalizedRuns := []TerminalizedRunSummary{}
		if target != nil {
			terminalizedRuns, err = terminalizeActiveRunsTx(tx, inst, disposition, eventID, now)
			if err != nil {
				return err
			}
		}
		if target != nil {
			resolved.Status = target.Status
			resolved.Phase = target.Phase
			resolved.Outcome = target.Outcome
		}
		resolved.Suspension = nil
		resolved.Revision = inst.Revision + 1
		resolved.UpdatedAt = now
		if resolved.Status == "closed" {
			resolved.ClosedAt = now
		}

		res, err := tx.Exec(`
			UPDATE workflow_instances
			SET status = ?, phase = ?, outcome = ?, revision = ?, updated_at = ?, closed_at = ?,
			    suspension_id = NULL, suspension_reason = NULL, suspension_at = NULL, suspension_cause_ref = NULL
			WHERE id = ? AND revision = ? AND suspension_id = ?
		`, resolved.Status, nullIfEmpty(resolved.Phase), nullIfEmpty(resolved.Outcome), resolved.Revision,
			resolved.UpdatedAt, nullIfEmpty(resolved.ClosedAt), resolved.ID, inst.Revision, priorSuspension.ID)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			actual, loadErr := instanceRevisionTx(tx, resolved.ID)
			if loadErr != nil {
				return loadErr
			}
			expected := inst.Revision
			if params.ExpectRevision != nil {
				expected = *params.ExpectRevision
			}
			return staleRevisionError(resolved.ID, expected, actual.revision)
		}

		createdEffects, err := createDispositionEffectsTx(tx, tpl, resolved, disposition, now)
		if err != nil {
			return err
		}

		eventPayload := map[string]interface{}{
			"suspension":           priorSuspension,
			"disposition":          disposition,
			"explanation":          params.Explanation,
			"beforeRevision":       inst.Revision,
			"afterRevision":        resolved.Revision,
			"from":                 inst.State(),
			"to":                   resolved.State(),
			"terminalizedRunCount": len(terminalizedRuns),
			"terminalizedRuns":     terminalizedRuns,
		}
		eventMeta, err := insertSuspensionResolvedEventTx(tx, eventID, resolved.ID, params.PrincipalRef, params.Role, inst.Revision, resolved.Revision, eventPayload)
		if err != nil {
			return err
		}
		ctx := workflowSuspensionResolvedWebhookContext(eventMeta, resolved, priorSuspension, disposition, params.PrincipalRef, params.Role, inst.Revision, resolved.Revision)
		webhookCtx = &ctx
		webhookTaskUUID = resolved.TaskUUID

		result = map[string]interface{}{
			"task":             resolved.TaskRef,
			"instanceId":       resolved.ID,
			"suspensionId":     priorSuspension.ID,
			"disposition":      disposition,
			"state":            resolved.State(),
			"revision":         resolved.Revision,
			"eventId":          eventID,
			"effects":          createdEffects,
			"instance":         resolved,
			"terminalizedRuns": terminalizedRuns,
		}

		if resolved.Status == "closed" {
			return updateTaskWorkflowMeta(tx, resolved.TaskUUID, resolved, params.PrincipalRef)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if webhookCtx != nil && webhookTaskUUID != "" {
		webhooks.DispatchTaskEvent(s.db, webhookTaskUUID, *webhookCtx)
	}
	result, err = s.deliverBuiltinTransitionEffects(result, disposition)
	if err != nil {
		return result, err
	}
	return result, nil
}

// instanceBySuspensionIDTx loads the running instance whose active suspension
// carries the given id. A miss means the id does not name any active
// suspension — the gate rejection.
func instanceBySuspensionIDTx(tx *sql.Tx, suspensionID string) (*Instance, error) {
	row := tx.QueryRow(`
		SELECT `+instanceSelectColumns+`
		FROM workflow_instances WHERE suspension_id = ?
	`, suspensionID)
	inst, err := scanInstanceRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, noActiveSuspensionError(suspensionID)
		}
		return nil, err
	}
	return inst, nil
}

// insertSuspensionResolvedEventTx records the workflow.suspension_resolved event
// carrying the resolved suspension, the disposition, and the revision delta. The
// event chains into the instance ledger like every other workflow event.
func insertSuspensionResolvedEventTx(tx *sql.Tx, id, instanceID, principalRef, role string, observed, next int64, payload interface{}) (workflowEventMetadata, error) {
	var seq int64
	_ = tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM workflow_events WHERE instance_id = ?`, instanceID).Scan(&seq)
	payloadJSON, _ := json.Marshal(payload)
	prevHash := previousEventHashTx(tx, instanceID)
	eventHash := chainedEventHash(prevHash, payloadJSON)
	_, err := tx.Exec(`
		INSERT INTO workflow_events (
			id, instance_id, seq, schema_version, type, actor, principal_ref, role,
			observed_revision, next_revision, payload_json, result, prev_event_hash, event_hash
		) VALUES (?, ?, ?, 'wrkf.workflow-event.v0', 'workflow.suspension_resolved', ?, ?, ?, ?, ?, ?, 'committed', ?, ?)
	`, id, instanceID, seq, emptyToNil(principalRef), emptyToNil(principalRef), emptyToNil(role),
		observed, next, string(payloadJSON), nullIfEmpty(prevHash), eventHash)
	if err != nil {
		return workflowEventMetadata{}, err
	}
	var createdAt string
	if err := tx.QueryRow(`SELECT created_at FROM workflow_events WHERE id = ?`, id).Scan(&createdAt); err != nil {
		return workflowEventMetadata{}, err
	}
	return workflowEventMetadata{ID: id, Seq: seq, SchemaVersion: "wrkf.workflow-event.v0", Type: "workflow.suspension_resolved", CreatedAt: createdAt, Payload: payload}, nil
}
