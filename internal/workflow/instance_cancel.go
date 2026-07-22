package workflow

import (
	"database/sql"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/webhooks"
)

type CancelInstanceParams struct {
	Task           string
	InstanceID     string
	ExpectRevision *int64
	Explanation    string
	PrincipalRef   string
	Role           string
}

func (s *Service) CancelInstance(params CancelInstanceParams) (map[string]interface{}, error) {
	if strings.TrimSpace(params.Task) == "" && strings.TrimSpace(params.InstanceID) == "" {
		return nil, validationError("selector", "task or instanceId is required", "task or instanceId", nil, "supply task or instanceId")
	}
	var result map[string]interface{}
	var webhookCtx *webhooks.EventContext
	var webhookTaskUUID string
	err := withImmediateTx(s.db, func(tx *sql.Tx) error {
		inst, err := resolveInstanceSelectors(tx, params.Task, params.InstanceID)
		if err != nil {
			return err
		}
		if inst.Suspension != nil {
			return suspendedWriteError(inst)
		}
		if inst.Status != "active" {
			return validationError("state", "instance is not active", "active unsuspended instance", []string{"active"}, "inspect the instance and cancel only active work")
		}
		if params.ExpectRevision != nil && *params.ExpectRevision != inst.Revision {
			return staleRevisionError(inst.ID, *params.ExpectRevision, inst.Revision)
		}
		tpl, _, err := showTemplateTx(tx, inst.TemplateID+"@"+inst.TemplateVersion)
		if err != nil {
			return err
		}
		eventID, err := nextSeqID(tx, "workflow_event_seq", "wfe")
		if err != nil {
			return err
		}
		now := s.now().Format(time.RFC3339)
		terminalized, err := terminalizeActiveRunsTx(tx, inst, DispositionCancel, eventID, now)
		if err != nil {
			return err
		}
		cancelled := *inst
		cancelled.Status = "closed"
		cancelled.Phase = ""
		cancelled.Outcome = "cancelled"
		cancelled.Revision = inst.Revision + 1
		cancelled.UpdatedAt = now
		cancelled.ClosedAt = now
		res, err := tx.Exec(`
			UPDATE workflow_instances
			SET status = 'closed', phase = NULL, outcome = 'cancelled', revision = ?, updated_at = ?, closed_at = ?
			WHERE id = ? AND revision = ? AND suspension_id IS NULL AND status = 'active'
		`, cancelled.Revision, now, now, cancelled.ID, inst.Revision)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			actual, loadErr := instanceRevisionTx(tx, inst.ID)
			if loadErr != nil {
				return loadErr
			}
			return staleRevisionError(inst.ID, inst.Revision, actual.revision)
		}
		effects, err := createDispositionEffectsTx(tx, tpl, cancelled, DispositionCancel, now)
		if err != nil {
			return err
		}
		payload := map[string]interface{}{
			"explanation": params.Explanation, "beforeRevision": inst.Revision, "afterRevision": cancelled.Revision,
			"from": inst.State(), "to": cancelled.State(), "disposition": DispositionCancel,
			"terminalizedRunCount": len(terminalized), "terminalizedRuns": terminalized,
		}
		meta, err := insertWorkflowMutationEventWithID(tx, "workflow.instance_cancelled", eventID, cancelled.ID, params.PrincipalRef, params.Role, "", inst.Revision, cancelled.Revision, "", "", "", taskDocEtagInt(inst), inst.TaskDocHash, payload)
		if err != nil {
			return err
		}
		if err := updateTaskWorkflowMeta(tx, cancelled.TaskUUID, cancelled, params.PrincipalRef); err != nil {
			return err
		}
		resultTask := strings.TrimSpace(params.Task)
		if resultTask == "" {
			resultTask = cancelled.TaskRef
		}
		result = map[string]interface{}{
			"task": resultTask, "instanceId": cancelled.ID, "state": cancelled.State(), "revision": cancelled.Revision,
			"eventId": eventID, "effects": effects, "terminalizedRuns": terminalized, "instance": cancelled,
		}
		ctx := workflowInstanceCancelledWebhookContext(meta, cancelled, params.PrincipalRef, params.Role, inst.Revision, cancelled.Revision)
		webhookCtx = &ctx
		webhookTaskUUID = cancelled.TaskUUID
		return nil
	})
	if err != nil {
		return nil, err
	}
	if webhookCtx != nil && webhookTaskUUID != "" {
		webhooks.DispatchTaskEvent(s.db, webhookTaskUUID, *webhookCtx)
	}
	result, err = s.deliverBuiltinTransitionEffects(result, DispositionCancel)
	if err != nil {
		return result, err
	}
	return result, nil
}
