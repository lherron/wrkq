//go:build wrkq_local

package workflow

import (
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
	var eventType, payload string
	if err := svc.db.QueryRow(`SELECT type, payload_json FROM workflow_events WHERE id = ?`, eventID).Scan(&eventType, &payload); err != nil {
		t.Fatalf("load suspension event: %v", err)
	}
	if eventType != "workflow.suspended" {
		t.Fatalf("park event type = %q, want workflow.suspended", eventType)
	}
	if strings.Contains(payload, `"disposition"`) || !strings.Contains(payload, `"suspension"`) || !strings.Contains(payload, `"beforeRevision":0`) || !strings.Contains(payload, `"afterRevision":1`) {
		t.Fatalf("workflow.suspended payload = %s", payload)
	}
	var fakeTransitions int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM workflow_events WHERE id = ? AND type = 'workflow.transitioned'`, eventID).Scan(&fakeTransitions); err != nil {
		t.Fatalf("count fake transition events: %v", err)
	}
	if fakeTransitions != 0 {
		t.Fatalf("park emitted %d workflow.transitioned events", fakeTransitions)
	}
	queried, err := svc.QueryEvents(EventQueryParams{EventType: "workflow.suspended"})
	if err != nil {
		t.Fatalf("QueryEvents workflow.suspended: %v", err)
	}
	if len(queried.Items) != 1 || queried.Items[0].Suspension == nil || queried.Items[0].BeforeRevision != attached.Revision || queried.Items[0].AfterRevision != current.Revision {
		t.Fatalf("queried suspension event = %+v", queried.Items)
	}

	// Reads/inspection are unaffected by the park and surface the suspension.
	readback, err := svc.InspectTask(taskUUID)
	if err != nil {
		t.Fatalf("InspectTask while suspended: %v", err)
	}
	if !readback.Suspended() || readback.Suspension.ID != current.Suspension.ID {
		t.Fatalf("readback lost suspension: %+v", readback.Suspension)
	}
	if readback.Status != attached.Status || readback.Phase != attached.Phase ||
		readback.Outcome != attached.Outcome || readback.Revision != attached.Revision+1 {
		t.Fatalf("readback state drifted: %+v rev=%d", readback.State(), readback.Revision)
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
