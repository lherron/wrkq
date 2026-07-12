package workflow

// suspension_resolve.go — T-06262. The single atomic resolution command for a
// suspended instance, and the sole exception to the suspended-write gate.
//
// Contract (WRKF_SIMPLIFICATION.md §4): resolveSuspension(suspensionId,
// disposition) runs in one transaction —
//   1. Load the instance whose ACTIVE suspension carries the presented id. The
//      matching suspension id is the ONLY gate: no role checks, no evidence
//      validation, no per-reason policy.
//   2. Apply the disposition's template-declared effects.
//   3. Clear the suspension, bump the revision, emit workflow.suspension_resolved.
//
// Dispositions: resume (back in the parked phase, state untouched), close
// (closed/done), cancel (closed/cancelled). The operator explanation is free
// text, recorded on the event, never validated. Ordinary revision CAS applies.

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

// ResolveSuspensionParams is the input to the atomic resolution command. Only
// SuspensionID and Disposition are required; ExpectRevision is the ordinary CAS
// precondition and Explanation is recorded free text.
type ResolveSuspensionParams struct {
	SuspensionID   string
	Disposition    string
	Explanation    string
	ExpectRevision *int64
	PrincipalRef   string
	Role           string
}

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
		// Gate: the instance is located BY its active suspension id. If no
		// running instance carries this suspension, the id does not match the
		// active suspension — that is the entire gate.
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

		// CAS + gate in one WHERE: the revision must be unchanged AND the active
		// suspension must still be the one we resolved. A concurrent resolve
		// bumps the revision and clears the suspension, so this fails cleanly.
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

		var effects []EffectSpec
		if tpl.Suspension != nil {
			effects = tpl.Suspension.Effects[disposition]
		}
		createdEffects := make([]Effect, 0, len(effects))
		for _, ef := range effects {
			id, err := nextSeqID(tx, "workflow_effect_seq", "eff")
			if err != nil {
				return err
			}
			seq, err := nextEffectSequenceTx(tx, resolved.ID)
			if err != nil {
				return err
			}
			renderedEffect, semanticKey, err := renderEffectSpec(ef, effectRenderContext{
				instance: resolved, outcomeID: disposition, sequence: seq,
			})
			if err != nil {
				return err
			}
			payload, _ := json.Marshal(renderedEffect)
			key := fmt.Sprintf("%s:%s", resolved.ID, semanticKey)
			if _, err := tx.Exec(`
				INSERT INTO workflow_effects (id, instance_id, revision, sequence, kind, payload_json, status, idempotency_key, semantic_key, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?)
			`, id, resolved.ID, resolved.Revision, seq, renderedEffect.Kind, string(payload), key, semanticKey, now, now); err != nil {
				return err
			}
			createdEffects = append(createdEffects, Effect{
				ID: id, InstanceID: resolved.ID, Revision: resolved.Revision, Sequence: seq, Kind: renderedEffect.Kind,
				Payload: json.RawMessage(payload), Status: "pending", IdempotencyKey: key, SemanticKey: semanticKey,
				CreatedAt: now, UpdatedAt: now,
			})
		}

		eventPayload := map[string]interface{}{
			"suspension":     priorSuspension,
			"disposition":    disposition,
			"explanation":    params.Explanation,
			"beforeRevision": inst.Revision,
			"afterRevision":  resolved.Revision,
			"from":           inst.State(),
			"to":             resolved.State(),
		}
		eventMeta, err := insertSuspensionResolvedEventTx(tx, eventID, resolved.ID, params.PrincipalRef, params.Role, inst.Revision, resolved.Revision, eventPayload)
		if err != nil {
			return err
		}
		ctx := workflowSuspensionResolvedWebhookContext(eventMeta, resolved, priorSuspension, disposition, params.PrincipalRef, params.Role, inst.Revision, resolved.Revision)
		webhookCtx = &ctx
		webhookTaskUUID = resolved.TaskUUID

		result = map[string]interface{}{
			"task":         resolved.TaskRef,
			"instanceId":   resolved.ID,
			"suspensionId": priorSuspension.ID,
			"disposition":  disposition,
			"state":        resolved.State(),
			"revision":     resolved.Revision,
			"eventId":      eventID,
			"effects":      createdEffects,
			"instance":     resolved,
		}

		// resume never touches the task document — the parked task returns
		// exactly to its phase (§5). close/cancel terminalize the instance, so
		// they mirror the closed state into task workflow meta like any normal
		// terminal transition.
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
