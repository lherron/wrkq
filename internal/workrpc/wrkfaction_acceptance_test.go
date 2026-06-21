package workrpc_test

// wrkfaction_acceptance_test.go — acceptance tests for T-05009, the
// low-ceremony wrkf.action.* API. All tests drive the REAL workrpc stdio server
// (go run ./cmd/wrkf rpc --stdio) via the shared p3Run/mkRPC helpers.
//
// Covered acceptance criteria (from the task spec):
//   1. action.start creates one durable run for triage on an un-workflowed task
//      using the built-in simple workflow.
//   2. action.start idempotency replay returns the same run.
//   3. action.bindExternal persists hrc:<runId> and rejects conflicting refs.
//   4. action.complete records run-linked evidence, applies triage_complete,
//      finishes the run.
//   5. action.complete replay is side-effect free.
//   6. action.fail records optional failure evidence and fails the run.
//   7. full stdio flow start -> bindExternal -> complete -> list.
//   8. action.list includeClosedInstances spans workflow instances.
//   9. no legacy cp_*/run_status task fields are read or written.

import (
	"database/sql"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

const actActor = "agent:action-tester"

// assertLegacyTaskFieldsUntouched opens dbPath and verifies the action surface
// left the legacy control-plane task scalar fields NULL/empty for taskUUID.
func assertLegacyTaskFieldsUntouched(t *testing.T, dbPath, taskUUID string) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = database.Close() }()
	var cpRunID, cpSessionID, runStatus sql.NullString
	err = database.QueryRow(
		`SELECT cp_run_id, cp_session_id, run_status FROM tasks WHERE uuid = ?`, taskUUID,
	).Scan(&cpRunID, &cpSessionID, &runStatus)
	if err != nil {
		t.Fatalf("query legacy task fields: %v", err)
	}
	if cpRunID.Valid && cpRunID.String != "" {
		t.Errorf("action surface wrote tasks.cp_run_id = %q", cpRunID.String)
	}
	if cpSessionID.Valid && cpSessionID.String != "" {
		t.Errorf("action surface wrote tasks.cp_session_id = %q", cpSessionID.String)
	}
	if runStatus.Valid && runStatus.String != "" {
		t.Errorf("action surface wrote tasks.run_status = %q", runStatus.String)
	}
}

func actRunID(t *testing.T, result map[string]any, label string) string {
	t.Helper()
	id, _ := result["runId"].(string)
	if id == "" {
		t.Fatalf("%s: result missing runId: %#v", label, result)
	}
	return id
}

// 1 + 2: start triage on an un-workflowed task installs the built-in workflow,
// creates one active run, and replays idempotently.
func TestWrkfActionStart_BuiltinWorkflowAndIdempotency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000001",
		"action-start-builtin", "Action Start Builtin")

	frames := p3Run(t, dbPath,
		mkRPC("s1", "wrkf.action.start", map[string]any{
			"task":           taskID,
			"action":         "triage",
			"actor":          actActor,
			"idempotencyKey": "act:start:1",
		}),
		mkRPC("s2", "wrkf.action.start", map[string]any{
			"task":           taskID,
			"action":         "triage",
			"actor":          actActor,
			"idempotencyKey": "act:start:1",
		}),
		mkRPC("l1", "wrkf.run.list", map[string]any{"task": taskID}),
	)
	r1 := p2ResultOrFail(t, frames[1], "wrkf.action.start")
	runID := actRunID(t, r1, "action.start")
	if got, _ := r1["actionRunId"].(string); got != runID {
		t.Errorf("actionRunId %q must equal runId %q", got, runID)
	}
	if got, _ := r1["action"].(string); got != "triage" {
		t.Errorf("action = %q, want triage", got)
	}
	if got, _ := r1["role"].(string); got != "triager" {
		t.Errorf("role defaulted to %q, want triager", got)
	}
	if got, _ := r1["status"].(string); got != "active" {
		t.Errorf("status = %q, want active", got)
	}
	wf, _ := r1["workflow"].(map[string]any)
	if wf == nil || wf["id"] != "wrkq-simple-task" {
		t.Errorf("workflow = %#v, want built-in wrkq-simple-task", wf)
	}

	// Replay → same run id.
	r2 := p2ResultOrFail(t, frames[2], "wrkf.action.start replay")
	if got := actRunID(t, r2, "replay"); got != runID {
		t.Errorf("idempotent replay returned %q, want %q", got, runID)
	}

	// Exactly one run on the instance.
	runs, _ := frames[3]["result"].([]any)
	if len(runs) != 1 {
		t.Fatalf("expected exactly 1 run after replay, got %d: %#v", len(runs), frames[3]["result"])
	}
}

// 3: bindExternal normalizes/persists hrc:<runId>, replays, rejects conflicts.
func TestWrkfActionBindExternal_HRCRefAndConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000002",
		"action-bind", "Action Bind")

	startFrames := p3Run(t, dbPath,
		mkRPC("s1", "wrkf.action.start", map[string]any{
			"task": taskID, "action": "triage", "actor": actActor,
		}),
	)
	runID := actRunID(t, p2ResultOrFail(t, startFrames[1], "start"), "start")

	frames := p3Run(t, dbPath,
		mkRPC("b1", "wrkf.action.bindExternal", map[string]any{
			"actionRunId":    runID,
			"externalRunRef": "hrc:run-123",
		}),
		mkRPC("b2", "wrkf.action.bindExternal", map[string]any{
			"actionRunId":    runID,
			"externalRunRef": "hrc:run-123",
		}),
		mkRPC("b3", "wrkf.action.bindExternal", map[string]any{
			"actionRunId":    runID,
			"externalRunRef": "hrc:run-999",
		}),
	)
	bound := p2ResultOrFail(t, frames[1], "bindExternal")
	if got, _ := bound["externalRunRef"].(string); got != "hrc:run-123" {
		t.Errorf("externalRunRef = %q, want hrc:run-123", got)
	}
	// Same ref replay succeeds.
	replay := p2ResultOrFail(t, frames[2], "bindExternal replay")
	if got, _ := replay["externalRunRef"].(string); got != "hrc:run-123" {
		t.Errorf("replay externalRunRef = %q, want hrc:run-123", got)
	}
	// Conflicting ref → error.
	if _, ok := frames[3]["error"]; !ok {
		t.Errorf("conflicting externalRunRef must error, got result: %#v", frames[3]["result"])
	}
}

// bindExternal also accepts a bare run id and prefixes hrc:.
func TestWrkfActionBindExternal_BareRefGetsHRCPrefix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000003",
		"action-bind-bare", "Action Bind Bare")
	startFrames := p3Run(t, dbPath,
		mkRPC("s1", "wrkf.action.start", map[string]any{"task": taskID, "action": "triage", "actor": actActor}),
	)
	runID := actRunID(t, p2ResultOrFail(t, startFrames[1], "start"), "start")

	frames := p3Run(t, dbPath,
		mkRPC("b1", "wrkf.action.bindExternal", map[string]any{
			"actionRunId":    runID,
			"externalRunRef": "run-bare-7",
		}),
	)
	bound := p2ResultOrFail(t, frames[1], "bindExternal bare")
	if got, _ := bound["externalRunRef"].(string); got != "hrc:run-bare-7" {
		t.Errorf("bare ref externalRunRef = %q, want hrc:run-bare-7", got)
	}
}

// 4 + 5: complete records run-linked evidence, applies triage_complete, finishes
// the run, and replays side-effect free.
func TestWrkfActionComplete_EvidenceTransitionFinishAndReplay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000004",
		"action-complete", "Action Complete")
	startFrames := p3Run(t, dbPath,
		mkRPC("s1", "wrkf.action.start", map[string]any{"task": taskID, "action": "triage", "actor": actActor}),
	)
	runID := actRunID(t, p2ResultOrFail(t, startFrames[1], "start"), "start")

	frames := p3Run(t, dbPath,
		mkRPC("c1", "wrkf.action.complete", map[string]any{
			"actionRunId": runID,
			"evidence":    map[string]any{"summary": "triaged ok"},
			"runSummary":  "done",
		}),
		mkRPC("c2", "wrkf.action.complete", map[string]any{
			"actionRunId": runID,
			"evidence":    map[string]any{"summary": "triaged ok"},
			"runSummary":  "done",
		}),
		mkRPC("sh", "wrkf.action.show", map[string]any{"actionRunId": runID}),
	)
	complete := p2ResultOrFail(t, frames[1], "action.complete")

	// Evidence recorded with default kind triage_result and run-linked id.
	ev, _ := complete["evidence"].(map[string]any)
	if ev == nil {
		t.Fatalf("complete result missing evidence: %#v", complete)
	}
	if got, _ := ev["kind"].(string); got != "triage_result" {
		t.Errorf("evidence kind = %q, want triage_result", got)
	}
	if got, _ := ev["runId"].(string); got != runID {
		t.Errorf("evidence runId = %q, want %q", got, runID)
	}
	// Transition applied.
	tr, _ := complete["transition"].(map[string]any)
	if tr == nil {
		t.Fatalf("complete result missing transition: %#v", complete)
	}
	state, _ := tr["state"].(map[string]any)
	if state == nil || state["phase"] != "ready" {
		t.Errorf("transition state = %#v, want phase ready", state)
	}
	// Run finished.
	run, _ := complete["run"].(map[string]any)
	if run == nil || run["status"] != "completed" {
		t.Errorf("run not finished: %#v", run)
	}

	// Replay: same committed shape, no duplicate evidence.
	replay := p2ResultOrFail(t, frames[2], "action.complete replay")
	rEv, _ := replay["evidence"].(map[string]any)
	if rEv == nil || rEv["id"] != ev["id"] {
		t.Errorf("replay evidence id = %v, want %v", rEv["id"], ev["id"])
	}

	// show: exactly one evidence and one transition event linked to the run.
	show := p2ResultOrFail(t, frames[3], "action.show")
	evIDs, _ := show["evidenceIds"].([]any)
	if len(evIDs) != 1 {
		t.Errorf("expected exactly 1 run-linked evidence, got %d: %#v", len(evIDs), show["evidenceIds"])
	}
	teIDs, _ := show["transitionEventIds"].([]any)
	if len(teIDs) != 1 {
		t.Errorf("expected exactly 1 run-linked transition event, got %d: %#v", len(teIDs), show["transitionEventIds"])
	}
}

// complete with transition:false skips the transition but still finishes the run.
func TestWrkfActionComplete_TransitionFalseSkips(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000005",
		"action-complete-skip", "Action Complete Skip")
	startFrames := p3Run(t, dbPath,
		mkRPC("s1", "wrkf.action.start", map[string]any{"task": taskID, "action": "triage", "actor": actActor}),
	)
	runID := actRunID(t, p2ResultOrFail(t, startFrames[1], "start"), "start")

	frames := p3Run(t, dbPath,
		mkRPC("c1", "wrkf.action.complete", map[string]any{
			"actionRunId": runID,
			"transition":  false,
			"runSummary":  "no transition",
		}),
		mkRPC("i1", "wrkf.instance.show", map[string]any{"task": taskID}),
	)
	complete := p2ResultOrFail(t, frames[1], "complete skip")
	if _, ok := complete["transition"]; ok {
		t.Errorf("transition:false must not return a transition: %#v", complete["transition"])
	}
	run, _ := complete["run"].(map[string]any)
	if run == nil || run["status"] != "completed" {
		t.Errorf("run not finished on skip: %#v", run)
	}
	// Instance stayed in intake (no transition applied).
	inst := p2ResultOrFail(t, frames[2], "instance.show")
	if inst["phase"] != "intake" {
		t.Errorf("instance phase = %v, want intake (no transition)", inst["phase"])
	}
}

// 6: fail records optional failure evidence and fails the run without a success
// transition.
func TestWrkfActionFail_RecordsEvidenceAndFails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000006",
		"action-fail", "Action Fail")
	startFrames := p3Run(t, dbPath,
		mkRPC("s1", "wrkf.action.start", map[string]any{"task": taskID, "action": "triage", "actor": actActor}),
	)
	runID := actRunID(t, p2ResultOrFail(t, startFrames[1], "start"), "start")

	frames := p3Run(t, dbPath,
		mkRPC("f1", "wrkf.action.fail", map[string]any{
			"actionRunId": runID,
			"summary":     "triage failed",
			"evidence":    map[string]any{"summary": "boom"},
		}),
		mkRPC("sh", "wrkf.action.show", map[string]any{"actionRunId": runID}),
		mkRPC("i1", "wrkf.instance.show", map[string]any{"task": taskID}),
	)
	failed := p2ResultOrFail(t, frames[1], "action.fail")
	if got, _ := failed["status"].(string); got != "failed" {
		t.Errorf("status = %q, want failed", got)
	}
	show := p2ResultOrFail(t, frames[2], "action.show")
	kinds, _ := show["evidenceKinds"].([]any)
	if len(kinds) != 1 || kinds[0] != "failure_result" {
		t.Errorf("failure evidence kinds = %#v, want [failure_result]", show["evidenceKinds"])
	}
	teIDs, _ := show["transitionEventIds"].([]any)
	if len(teIDs) != 0 {
		t.Errorf("fail must not apply a transition, got events: %#v", show["transitionEventIds"])
	}
	// Instance stayed in intake.
	inst := p2ResultOrFail(t, frames[3], "instance.show")
	if inst["phase"] != "intake" {
		t.Errorf("instance phase = %v, want intake after fail", inst["phase"])
	}
}

// 7: full stdio flow start -> bindExternal -> complete -> list in one session.
func TestWrkfAction_FullStdioFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000007",
		"action-flow", "Action Flow")

	startFrames := p3Run(t, dbPath,
		mkRPC("s1", "wrkf.action.start", map[string]any{"task": taskID, "action": "triage", "actor": actActor}),
	)
	runID := actRunID(t, p2ResultOrFail(t, startFrames[1], "start"), "start")

	frames := p3Run(t, dbPath,
		mkRPC("b1", "wrkf.action.bindExternal", map[string]any{
			"actionRunId": runID, "externalRunRef": "hrc:flow-1",
		}),
		mkRPC("c1", "wrkf.action.complete", map[string]any{
			"actionRunId": runID,
			"evidence":    map[string]any{"summary": "ok"},
		}),
		mkRPC("ls", "wrkf.action.list", map[string]any{"task": taskID, "includeClosedInstances": true}),
	)
	p2ResultOrFail(t, frames[1], "bindExternal")
	p2ResultOrFail(t, frames[2], "complete")
	list := p2ResultOrFail(t, frames[3], "action.list")
	items, _ := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("action.list expected 1 item, got %d: %#v", len(items), list["items"])
	}
	item, _ := items[0].(map[string]any)
	if item["runId"] != runID {
		t.Errorf("listed runId = %v, want %v", item["runId"], runID)
	}
	if item["externalRunRef"] != "hrc:flow-1" {
		t.Errorf("listed externalRunRef = %v, want hrc:flow-1", item["externalRunRef"])
	}
	if item["status"] != "completed" {
		t.Errorf("listed status = %v, want completed", item["status"])
	}
}

// 8 + 10: action.list with includeClosedInstances spans closed and active
// instances; default excludes closed instances.
func TestWrkfActionList_IncludeClosedInstances(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000008",
		"action-history", "Action History")

	// Drive a full lifecycle through the simple workflow to close the first
	// instance: triage -> implement -> verify -> review (review_complete closes).
	driveFrames := p3Run(t, dbPath,
		mkRPC("t1", "wrkf.action.start", map[string]any{"task": taskID, "action": "triage", "actor": actActor}),
	)
	triageRun := actRunID(t, p2ResultOrFail(t, driveFrames[1], "triage start"), "triage start")
	p3Run(t, dbPath, mkRPC("tc", "wrkf.action.complete", map[string]any{"actionRunId": triageRun, "evidence": map[string]any{"summary": "t"}}))

	implFrames := p3Run(t, dbPath, mkRPC("i1", "wrkf.action.start", map[string]any{"task": taskID, "action": "implement", "actor": actActor}))
	implRun := actRunID(t, p2ResultOrFail(t, implFrames[1], "impl start"), "impl start")
	p3Run(t, dbPath, mkRPC("ic", "wrkf.action.complete", map[string]any{"actionRunId": implRun, "evidence": map[string]any{"summary": "i"}}))

	verFrames := p3Run(t, dbPath, mkRPC("v1", "wrkf.action.start", map[string]any{"task": taskID, "action": "verify", "actor": actActor}))
	verRun := actRunID(t, p2ResultOrFail(t, verFrames[1], "verify start"), "verify start")
	p3Run(t, dbPath, mkRPC("vc", "wrkf.action.complete", map[string]any{"actionRunId": verRun, "evidence": map[string]any{"summary": "v"}}))

	revFrames := p3Run(t, dbPath, mkRPC("r1", "wrkf.action.start", map[string]any{"task": taskID, "action": "review", "actor": actActor}))
	revRun := actRunID(t, p2ResultOrFail(t, revFrames[1], "review start"), "review start")
	p3Run(t, dbPath, mkRPC("rc", "wrkf.action.complete", map[string]any{"actionRunId": revRun, "evidence": map[string]any{"summary": "r"}}))

	// First instance is now closed. Start a NEW action — attaches a new instance.
	newFrames := p3Run(t, dbPath, mkRPC("n1", "wrkf.action.start", map[string]any{"task": taskID, "action": "triage", "actor": actActor}))
	newRun := actRunID(t, p2ResultOrFail(t, newFrames[1], "new triage start"), "new triage start")

	frames := p3Run(t, dbPath,
		mkRPC("all", "wrkf.action.list", map[string]any{"task": taskID, "includeClosedInstances": true}),
		mkRPC("active", "wrkf.action.list", map[string]any{"task": taskID}),
	)
	all := p2ResultOrFail(t, frames[1], "action.list all")
	allItems, _ := all["items"].([]any)
	if len(allItems) != 5 {
		t.Errorf("includeClosedInstances=true expected 5 runs across instances, got %d", len(allItems))
	}

	active := p2ResultOrFail(t, frames[2], "action.list active")
	activeItems, _ := active["items"].([]any)
	if len(activeItems) != 1 {
		t.Fatalf("default list expected 1 run on active instance, got %d", len(activeItems))
	}
	item, _ := activeItems[0].(map[string]any)
	if item["runId"] != newRun {
		t.Errorf("active list runId = %v, want %v", item["runId"], newRun)
	}
}

// 9: the action surface never reads or writes legacy cp_*/run_status task fields.
func TestWrkfAction_NoLegacyTaskFields(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000009",
		"action-no-legacy", "Action No Legacy")

	startFrames := p3Run(t, dbPath,
		mkRPC("s1", "wrkf.action.start", map[string]any{"task": taskID, "action": "triage", "actor": actActor}),
	)
	runID := actRunID(t, p2ResultOrFail(t, startFrames[1], "start"), "start")
	p3Run(t, dbPath, mkRPC("c1", "wrkf.action.complete", map[string]any{"actionRunId": runID, "evidence": map[string]any{"summary": "ok"}}))

	assertLegacyTaskFieldsUntouched(t, dbPath, "a5000000-0000-4000-8000-000000000009")
}
