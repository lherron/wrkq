package workrpc_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

type instanceCancelRPCFixture struct {
	dbPath     string
	taskID     string
	instanceID string
	revision   float64
	claim      map[string]any
}

func setupInstanceCancelRPCFixture(t *testing.T, taskUUID, slug string) instanceCancelRPCFixture {
	t.Helper()
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath, taskUUID, slug, "Instance Cancel Acceptance")
	setup := p3Run(t, dbPath,
		mkRPC("install", "wrkf.workflow.install", map[string]any{
			"body": templateBody(t, "internal/workflow/builtins/wrkq-simple-task-v5.workflow.json"),
		}),
		mkRPC("attach", "wrkq.workflow.attach", map[string]any{
			"task": taskID, "workflow": "wrkq-simple-task@5",
		}),
	)
	p2ResultOrFail(t, setup[1], "install v5")
	p2ResultOrFail(t, setup[2], "attach v5")
	claim := actRPCClaim(t, dbPath, taskID, "test")
	show := p3Run(t, dbPath, mkRPC("show", "wrkf.instance.show", map[string]any{"task": taskID}))
	instance := p2ResultOrFail(t, show[1], "show cancellable instance")
	instanceID, _ := instance["id"].(string)
	revision, _ := instance["revision"].(float64)
	if instanceID == "" {
		t.Fatalf("instance.show missing id: %#v", instance)
	}
	return instanceCancelRPCFixture{dbPath: dbPath, taskID: taskID, instanceID: instanceID, revision: revision, claim: claim}
}

func instanceCancelDurableSnapshot(t *testing.T, f instanceCancelRPCFixture) string {
	t.Helper()
	database, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatalf("open cancel snapshot db: %v", err)
	}
	defer func() { _ = database.Close() }()
	var instance, runs, task string
	if err := database.QueryRow(`SELECT json_object('status',status,'phase',phase,'outcome',outcome,'revision',revision,'suspensionId',suspension_id,'closedAt',closed_at) FROM workflow_instances WHERE id = ?`, f.instanceID).Scan(&instance); err != nil {
		t.Fatalf("snapshot cancel instance: %v", err)
	}
	if err := database.QueryRow(`SELECT json_group_array(json_object('id',id,'status',status,'token',lease_token,'completed',completed_at,'terminal',terminal_result)) FROM (SELECT * FROM workflow_runs WHERE instance_id = ? ORDER BY id)`, f.instanceID).Scan(&runs); err != nil {
		t.Fatalf("snapshot cancel runs: %v", err)
	}
	if err := database.QueryRow(`SELECT json_object('state',state,'etag',etag,'meta',meta) FROM tasks WHERE id = ?`, f.taskID).Scan(&task); err != nil {
		t.Fatalf("snapshot cancel task: %v", err)
	}
	var events, effects int
	if err := database.QueryRow(`SELECT COUNT(*) FROM workflow_events WHERE instance_id = ?`, f.instanceID).Scan(&events); err != nil {
		t.Fatalf("snapshot cancel events: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM workflow_effects WHERE instance_id = ?`, f.instanceID).Scan(&effects); err != nil {
		t.Fatalf("snapshot cancel effects: %v", err)
	}
	return strings.Join([]string{instance, runs, task, fmt.Sprintf("events=%d", events), fmt.Sprintf("effects=%d", effects)}, "\n")
}

func TestWrkfInstanceCancelUnsuspendedRevokesRunAndRejectsLateSettle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	f := setupInstanceCancelRPCFixture(t, "a5000000-0000-4000-8000-000000000101", "instance-cancel-active")
	run, _ := f.claim["run"].(map[string]any)
	runID, _ := run["id"].(string)
	ownerToken, _ := f.claim["ownerToken"].(string)

	frames := p3Run(t, f.dbPath, mkRPC("cancel", "wrkf.instance.cancel", map[string]any{
		"task": f.taskID, "instanceId": f.instanceID, "expectRevision": f.revision,
		"explanation": "operator cancelled never-cranked work", "principal_ref": "human:operator", "role": "coordinator",
	}))
	result := p2ResultOrFail(t, frames[1], "wrkf.instance.cancel")
	state, _ := result["state"].(map[string]any)
	if result["task"] != f.taskID || result["instanceId"] != f.instanceID || state["status"] != "closed" || (state["phase"] != "cancelled" && state["outcome"] != "cancelled") {
		t.Fatalf("instance.cancel result = %#v, want task/instance closed/cancelled", result)
	}
	eventID, _ := result["eventId"].(string)
	terminalized, _ := result["terminalizedRuns"].([]any)
	if eventID == "" || len(terminalized) != 1 {
		t.Fatalf("instance.cancel event/run summaries = event %q runs %#v", eventID, terminalized)
	}
	encoded := fmt.Sprintf("%#v", result)
	if !strings.Contains(encoded, runID) || (ownerToken != "" && strings.Contains(encoded, ownerToken)) {
		t.Fatalf("instance.cancel result missing run id or leaked bearer: %s", encoded)
	}

	database, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatalf("open cancelled db: %v", err)
	}
	var status, token, completedAt, cause string
	if err := database.QueryRow(`SELECT status, COALESCE(lease_token,''), COALESCE(completed_at,''), COALESCE(terminal_result,'') FROM workflow_runs WHERE id = ?`, runID).Scan(&status, &token, &completedAt, &cause); err != nil {
		_ = database.Close()
		t.Fatalf("read cancelled run: %v", err)
	}
	var eventType string
	if err := database.QueryRow(`SELECT type FROM workflow_events WHERE id = ?`, eventID).Scan(&eventType); err != nil {
		_ = database.Close()
		t.Fatalf("read instance cancel event: %v", err)
	}
	_ = database.Close()
	if status == "active" || token != "" || completedAt == "" || !strings.Contains(cause, "cancel") || !strings.Contains(cause, eventID) || eventType != "workflow.instance_cancelled" {
		t.Fatalf("cancelled run/event = status %q token %q completed %q cause %q event %q", status, token, completedAt, cause, eventType)
	}

	late := p3Run(t, f.dbPath, mkRPC("late-settle", "wrkf.action.settle", map[string]any{
		"runId": runID, "ownerToken": f.claim["ownerToken"], "ownerGeneration": f.claim["ownerGeneration"],
		"result": "completed", "evidence": map[string]any{"summary": "late", "facts": map[string]any{"result": "pass"}},
	}))
	if _, ok := late[1]["error"]; !ok {
		t.Fatalf("late settle after instance cancel succeeded: %#v", late[1])
	}

	beforeReplay := instanceCancelDurableSnapshot(t, f)
	replay := p3Run(t, f.dbPath, mkRPC("cancel-closed", "wrkf.instance.cancel", map[string]any{
		"task": f.taskID, "instanceId": f.instanceID,
	}))
	if p2ErrCode(replay[1]) == "" {
		t.Fatalf("closed instance cancel did not return a typed state refusal: %#v", replay[1])
	}
	if afterReplay := instanceCancelDurableSnapshot(t, f); afterReplay != beforeReplay {
		t.Fatalf("closed instance cancel mutated state:\nbefore %s\nafter  %s", beforeReplay, afterReplay)
	}
}

func TestWrkfInstanceCancelStaleRevisionIsAtomicNoOp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	f := setupInstanceCancelRPCFixture(t, "a5000000-0000-4000-8000-000000000102", "instance-cancel-stale")
	before := instanceCancelDurableSnapshot(t, f)
	frames := p3Run(t, f.dbPath, mkRPC("cancel-stale", "wrkf.instance.cancel", map[string]any{
		"task": f.taskID, "instanceId": f.instanceID, "expectRevision": f.revision + 99,
	}))
	if code := p2ErrCode(frames[1]); code != "WRKF_STALE_REVISION" {
		t.Fatalf("stale instance cancel code = %q frame=%#v", code, frames[1])
	}
	if after := instanceCancelDurableSnapshot(t, f); after != before {
		t.Fatalf("stale instance cancel mutated state:\nbefore %s\nafter  %s", before, after)
	}
}

func TestWrkfInstanceCancelSuspendedRequiresMatchingResolution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	f := setupInstanceCancelRPCFixture(t, "a5000000-0000-4000-8000-000000000103", "instance-cancel-suspended")
	actRPCSettle(t, f.dbPath, f.claim, map[string]any{"result": "operator_required"}, "parked for operator")
	show := p3Run(t, f.dbPath, mkRPC("show-suspended", "wrkf.instance.show", map[string]any{"task": f.taskID}))
	instance := p2ResultOrFail(t, show[1], "show suspended instance")
	suspension, _ := instance["suspension"].(map[string]any)
	suspensionID, _ := suspension["id"].(string)
	if suspensionID == "" {
		t.Fatalf("parked instance missing suspension id: %#v", instance)
	}
	f.revision, _ = instance["revision"].(float64)
	before := instanceCancelDurableSnapshot(t, f)
	frames := p3Run(t, f.dbPath, mkRPC("cancel-suspended", "wrkf.instance.cancel", map[string]any{
		"task": f.taskID, "instanceId": f.instanceID, "expectRevision": f.revision,
	}))
	if code := p2ErrCode(frames[1]); code != "WRKF_SUSPENDED" {
		t.Fatalf("suspended instance cancel code = %q frame=%#v", code, frames[1])
	}
	fix, _ := p2ErrDataField(frames[1], "fix").(string)
	gotSuspension, _ := p2ErrDataField(frames[1], "suspension").(map[string]any)
	if gotSuspension["id"] != suspensionID || !strings.Contains(fix, "wrkf suspension resolve") || !strings.Contains(fix, suspensionID) || !strings.Contains(fix, "--disposition cancel") {
		t.Fatalf("suspended instance cancel recovery = suspension %#v fix %q", gotSuspension, fix)
	}
	if after := instanceCancelDurableSnapshot(t, f); after != before {
		t.Fatalf("suspended instance cancel mutated state:\nbefore %s\nafter  %s", before, after)
	}
}
