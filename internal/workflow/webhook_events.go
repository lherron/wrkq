package workflow

import (
	"encoding/json"

	"github.com/lherron/wrkq/internal/webhooks"
)

func workflowAttachedWebhookContext(meta workflowEventMetadata, inst Instance, templateRef, actor string, state State) webhooks.EventContext {
	return webhooks.EventContext{
		Event:      webhooks.EventWorkflowAttached,
		EventID:    meta.ID,
		EventSeq:   meta.Seq,
		OccurredAt: meta.CreatedAt,
		Via:        "wrkf",
		Changed:    []string{},
		Changes:    map[string]webhooks.Change{},
		Subject: &webhooks.Subject{
			WorkflowInstanceID: inst.ID,
		},
		Workflow: &webhooks.WorkflowPayload{
			SchemaVersion: meta.SchemaVersion,
			Type:          meta.Type,
			EventID:       meta.ID,
			EventSeq:      meta.Seq,
			InstanceID:    inst.ID,
			Template:      templateRef,
			State:         state,
			PrincipalRef:  actor,
			NextRevision:  int64Ptr(inst.Revision),
			TaskDocETag:   inst.TaskDocEtag,
			TaskDocHash:   inst.TaskDocHash,
			Payload:       meta.Payload,
		},
	}
}

func workflowTransitionWebhookContext(meta workflowEventMetadata, updated Instance, actor, role, runID, transitionID, outcomeID string, observedRevision, nextRevision int64, idempotencyKey string, from, to State) webhooks.EventContext {
	return webhooks.EventContext{
		Event:      webhooks.EventWorkflowTransitioned,
		EventID:    meta.ID,
		EventSeq:   meta.Seq,
		OccurredAt: meta.CreatedAt,
		Via:        "wrkf",
		Transition: &webhooks.Transition{
			From: stateSummaryPtr(from),
			To:   stateSummaryPtr(to),
		},
		Changed: []string{"workflow"},
		Changes: map[string]webhooks.Change{
			"workflow": {From: from, To: to},
		},
		Subject: &webhooks.Subject{
			WorkflowInstanceID: updated.ID,
		},
		Workflow: &webhooks.WorkflowPayload{
			SchemaVersion:    meta.SchemaVersion,
			Type:             meta.Type,
			EventID:          meta.ID,
			EventSeq:         meta.Seq,
			InstanceID:       updated.ID,
			Transition:       transitionID,
			Outcome:          outcomeID,
			From:             from,
			To:               to,
			PrincipalRef:     actor,
			Role:             role,
			RunID:            nonEmptyStringPtr(runID),
			ObservedRevision: int64Ptr(observedRevision),
			NextRevision:     int64Ptr(nextRevision),
			TaskDocETag:      updated.TaskDocEtag,
			TaskDocHash:      updated.TaskDocHash,
			IdempotencyKey:   nonEmptyStringPtr(idempotencyKey),
			Payload:          meta.Payload,
		},
	}
}

func workflowSuspensionWebhookContext(meta workflowEventMetadata, updated Instance, actor, role, runID string, beforeRevision, afterRevision int64, idempotencyKey string) webhooks.EventContext {
	return webhooks.EventContext{
		Event: webhooks.EventWorkflowSuspended, EventID: meta.ID, EventSeq: meta.Seq, OccurredAt: meta.CreatedAt, Via: "wrkf",
		Changed: []string{"workflow.suspension"},
		Changes: map[string]webhooks.Change{"workflow.suspension": {From: nil, To: updated.Suspension}},
		Subject: &webhooks.Subject{WorkflowInstanceID: updated.ID},
		Workflow: &webhooks.WorkflowPayload{
			SchemaVersion: meta.SchemaVersion, Type: meta.Type, EventID: meta.ID, EventSeq: meta.Seq,
			InstanceID: updated.ID, PrincipalRef: actor, Role: role, RunID: nonEmptyStringPtr(runID),
			Suspension: updated.Suspension, BeforeRevision: int64Ptr(beforeRevision), AfterRevision: int64Ptr(afterRevision),
			IdempotencyKey: nonEmptyStringPtr(idempotencyKey), Payload: meta.Payload,
		},
	}
}

func workflowSuspensionResolvedWebhookContext(meta workflowEventMetadata, updated Instance, suspension Suspension, disposition, actor, role string, beforeRevision, afterRevision int64) webhooks.EventContext {
	return webhooks.EventContext{
		Event: webhooks.EventWorkflowSuspensionResolved, EventID: meta.ID, EventSeq: meta.Seq, OccurredAt: meta.CreatedAt, Via: "wrkf",
		Changed: []string{"workflow.suspension"},
		Changes: map[string]webhooks.Change{"workflow.suspension": {From: suspension, To: nil}},
		Subject: &webhooks.Subject{WorkflowInstanceID: updated.ID},
		Workflow: &webhooks.WorkflowPayload{
			SchemaVersion: meta.SchemaVersion, Type: meta.Type, EventID: meta.ID, EventSeq: meta.Seq,
			InstanceID: updated.ID, PrincipalRef: actor, Role: role, Suspension: updated.Suspension, Disposition: disposition,
			BeforeRevision: int64Ptr(beforeRevision), AfterRevision: int64Ptr(afterRevision), Payload: meta.Payload,
		},
	}
}

func stateSummaryPtr(state State) *string {
	summary := state.Status
	if state.Phase != "" {
		summary += ":" + state.Phase
	}
	if state.Outcome != "" {
		summary += ":" + state.Outcome
	}
	return &summary
}

func int64Ptr(value int64) *int64 {
	return &value
}

func nonEmptyStringPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func (s *Service) workflowEventMetadataByID(id string) (workflowEventMetadata, error) {
	var meta workflowEventMetadata
	var payload string
	err := s.db.QueryRow(`
		SELECT id, seq, schema_version, type, payload_json, created_at
		FROM workflow_events WHERE id = ?
	`, id).Scan(&meta.ID, &meta.Seq, &meta.SchemaVersion, &meta.Type, &payload, &meta.CreatedAt)
	if err != nil {
		return workflowEventMetadata{}, err
	}
	meta.Payload = json.RawMessage(payload)
	return meta, nil
}
