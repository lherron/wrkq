package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const suspendOutcomeTemplate = `{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "suspend-outcome-test",
  "version": "1",
  "kind": "agent_first_workflow",
  "initial": { "status": "active", "phase": "ready" },
  "roles": { "coordinator": {} },
  "states": [
    { "status": "active", "phase": "ready" },
    { "status": "closed", "outcome": "done" },
    { "status": "closed", "outcome": "cancelled" }
  ],
  "suspension": {
    "reasons": ["operator_required"],
    "effects": {
      "resume": [{ "kind": "resume_notice", "role": "coordinator" }],
      "close": [{ "kind": "set_task_state", "data": { "state": "completed" } }],
      "cancel": [{ "kind": "set_task_state", "data": { "state": "cancelled" } }]
    }
  },
  "transitions": [
    {
      "id": "park",
      "from": { "status": "active", "phase": "ready" },
      "by": ["coordinator"],
      "outcomes": [{
        "id": "needs_operator",
        "when": { "always": true },
        "suspend": { "reason": "operator_required" }
      }]
    },
    {
      "id": "complete",
      "from": { "status": "active", "phase": "ready" },
      "by": ["coordinator"],
      "outcomes": [{
        "id": "done",
        "when": { "always": true },
        "to": { "status": "closed", "outcome": "done" }
      }]
    }
  ]
}`

func TestSuspendOutcomeParsesTaggedUnionAndDispositionEffects(t *testing.T) {
	tpl, canonical, err := ParseTemplate([]byte(suspendOutcomeTemplate))
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	if errs := ValidateTemplate(tpl, canonical, nil); len(errs) > 0 {
		t.Fatalf("ValidateTemplate: %v", errs)
	}
	outcome := tpl.Transitions[0].Outcomes[0]
	if outcome.To != nil || outcome.Suspend == nil || outcome.Suspend.Reason != "operator_required" {
		t.Fatalf("outcome union = to:%+v suspend:%+v", outcome.To, outcome.Suspend)
	}
	if tpl.Suspension == nil || len(tpl.Suspension.Effects["resume"]) != 1 || len(tpl.Suspension.Effects["close"]) != 1 || len(tpl.Suspension.Effects["cancel"]) != 1 {
		t.Fatalf("suspension disposition effects did not parse: %+v", tpl.Suspension)
	}
}

func TestSuspendOutcomeRejectsBothToAndSuspend(t *testing.T) {
	bad := strings.Replace(suspendOutcomeTemplate,
		`"suspend": { "reason": "operator_required" }`,
		`"to": { "status": "closed", "outcome": "done" }, "suspend": { "reason": "operator_required" }`, 1)
	requireTemplateValidationError(t, bad, "must declare exactly one of to or suspend")
}

func TestSuspendOutcomeRejectsNeitherToNorSuspend(t *testing.T) {
	bad := strings.Replace(suspendOutcomeTemplate, `"suspend": { "reason": "operator_required" }`, `"description": "missing target"`, 1)
	requireTemplateValidationError(t, bad, "transition park outcome needs_operator must declare exactly one of to or suspend")
}

func TestSuspendOutcomeRejectsUndeclaredReason(t *testing.T) {
	bad := strings.Replace(suspendOutcomeTemplate, `"reason": "operator_required"`, `"reason": "unlisted_reason"`, 1)
	requireTemplateValidationError(t, bad, `outcome needs_operator suspend reason "unlisted_reason" is not declared`)
}

func TestSuspendOutcomeRejectsClosedSource(t *testing.T) {
	bad := strings.Replace(suspendOutcomeTemplate,
		`"from": { "status": "active", "phase": "ready" }`,
		`"from": { "status": "closed", "outcome": "done" }`, 1)
	requireTemplateValidationError(t, bad, "outcome needs_operator cannot suspend from a closed state")
}

func TestSuspensionPolicyRejectsDeadDeclaration(t *testing.T) {
	bad := strings.Replace(suspendOutcomeTemplate, `"suspend": { "reason": "operator_required" }`, `"to": { "status": "closed", "outcome": "done" }`, 1)
	requireTemplateValidationError(t, bad, "suspension is declared but no outcome uses suspend")
}

func TestSuspensionPolicyRejectsUnknownDisposition(t *testing.T) {
	bad := strings.Replace(suspendOutcomeTemplate,
		`"resume": [{ "kind": "resume_notice", "role": "coordinator" }]`,
		`"retry": [{ "kind": "resume_notice", "role": "coordinator" }]`, 1)
	requireTemplateValidationError(t, bad, `unknown disposition "retry"`)
}

func TestTransitionSuspendOutcomeRecordsConditionAndPreservesState(t *testing.T) {
	svc, taskUUID, attached := setupSuspendOutcomeFixture(t)
	var taskETagBefore int64
	if err := svc.db.QueryRow(`SELECT etag FROM tasks WHERE uuid = ?`, taskUUID).Scan(&taskETagBefore); err != nil {
		t.Fatalf("task etag before: %v", err)
	}

	result, err := svc.Transition(taskUUID, "park", TransitionOptions{PrincipalRef: "agent:test", Role: "coordinator"})
	if err != nil {
		t.Fatalf("Transition park: %v", err)
	}
	current, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after park: %v", err)
	}
	if current.Status != attached.Status || current.Phase != attached.Phase || current.Outcome != attached.Outcome {
		t.Fatalf("suspend outcome changed state: before=%+v after=%+v", attached.State(), current.State())
	}
	if current.Revision != attached.Revision+1 {
		t.Fatalf("revision = %d, want %d", current.Revision, attached.Revision+1)
	}
	var taskETagAfter int64
	if err := svc.db.QueryRow(`SELECT etag FROM tasks WHERE uuid = ?`, taskUUID).Scan(&taskETagAfter); err != nil {
		t.Fatalf("task etag after: %v", err)
	}
	if taskETagAfter != taskETagBefore {
		t.Fatalf("suspend outcome touched task document etag: before=%d after=%d", taskETagBefore, taskETagAfter)
	}
	eventID, _ := result["eventId"].(string)
	if current.Suspension == nil || current.Suspension.Reason != "operator_required" || current.Suspension.ID == "" || current.Suspension.At == "" || current.Suspension.CauseRef != eventID || eventID == "" {
		t.Fatalf("suspension = %+v eventId=%q", current.Suspension, eventID)
	}
}

func TestActionSettlementSuspendOutcomeRecordsConditionAndPreservesState(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	doc := builtinV2Doc(t)
	doc["id"] = "action-suspend-outcome-test"
	doc["version"] = "1"
	doc["suspension"] = map[string]any{"reasons": []any{"operator_required"}}
	transitions := doc["transitions"].([]any)
	for _, raw := range transitions {
		transition := raw.(map[string]any)
		if transition["id"] != "triage_complete" {
			continue
		}
		ready := transition["outcomes"].([]any)[0].(map[string]any)
		delete(ready, "to")
		ready["suspend"] = map[string]any{"reason": "operator_required"}
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal template: %v", err)
	}
	tpl, canonical, err := ParseTemplate(data)
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	if _, err := svc.installTemplateCanonical(tpl, canonical, Hash(canonical), "test-installer", nil, false); err != nil {
		t.Fatalf("install template: %v", err)
	}
	attached, err := svc.AttachTask(taskUUID, "action-suspend-outcome-test@1", "test-installer")
	if err != nil {
		t.Fatalf("AttachTask: %v", err)
	}
	claim := claimActionForTest(t, svc, taskUUID, "triage")
	var taskETagBefore int64
	if err := svc.db.QueryRow(`SELECT etag FROM tasks WHERE uuid = ?`, taskUUID).Scan(&taskETagBefore); err != nil {
		t.Fatalf("task etag before settle: %v", err)
	}
	if _, err := svc.SettleAction(SettleActionParams{
		ActionRunID:     claim.Binding.Run.ID,
		OwnerToken:      claim.Binding.Authority.OwnerToken,
		OwnerGeneration: claim.Binding.Authority.OwnerGeneration,
		Result:          "completed",
		Evidence:        &ActionEvidenceInput{Summary: "triaged", Facts: `{"result":"ready"}`},
	}); err != nil {
		t.Fatalf("SettleAction: %v", err)
	}
	current, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if current.State() != attached.State() {
		t.Fatalf("settlement suspend outcome changed state: before=%+v after=%+v", attached.State(), current.State())
	}
	if current.Revision != attached.Revision+1 || current.Suspension == nil || current.Suspension.Reason != "operator_required" || current.Suspension.CauseRef == "" {
		t.Fatalf("settlement suspension/revision = %+v rev=%d", current.Suspension, current.Revision)
	}
	var taskETagAfter int64
	if err := svc.db.QueryRow(`SELECT etag FROM tasks WHERE uuid = ?`, taskUUID).Scan(&taskETagAfter); err != nil {
		t.Fatalf("task etag after settle: %v", err)
	}
	if taskETagAfter != taskETagBefore {
		t.Fatalf("settlement suspend outcome touched task document etag: before=%d after=%d", taskETagBefore, taskETagAfter)
	}
}

func setupSuspendOutcomeFixture(t *testing.T) (*Service, string, *Instance) {
	t.Helper()
	svc, taskUUID, _ := setupCASFixture(t)
	templatePath := filepath.Join(t.TempDir(), "suspend-outcome.json")
	if err := os.WriteFile(templatePath, []byte(suspendOutcomeTemplate), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	if _, err := svc.InstallTemplate(templatePath, "test-installer", nil); err != nil {
		t.Fatalf("InstallTemplate: %v", err)
	}
	predecessor, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance predecessor: %v", err)
	}
	revision := predecessor.Revision
	attached, err := svc.AttachTask(taskUUID, "suspend-outcome-test@1", "test-installer", AttachTaskOptions{
		Supersede: true, PredecessorInstanceID: predecessor.ID, PredecessorRevision: &revision,
	})
	if err != nil {
		t.Fatalf("AttachTask: %v", err)
	}
	return svc, taskUUID, attached
}

func requireTemplateValidationError(t *testing.T, document, want string) {
	t.Helper()
	tpl, canonical, err := ParseTemplate([]byte(document))
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	joined := strings.Join(ValidateTemplate(tpl, canonical, nil), "; ")
	if !strings.Contains(joined, want) {
		t.Fatalf("validation errors = %q, want substring %q", joined, want)
	}
}
