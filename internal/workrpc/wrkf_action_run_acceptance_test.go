//go:build wrkq_local

package workrpc_test

// wrkf_action_run_acceptance_test.go — the low-ceremony wrkf.action.* run
// lifecycle (T-05009), driven through the REAL wrkf stdio server:
//   1. action.start creates one durable run on an un-workflowed task using the
//      built-in simple workflow, and replays idempotently; explicit workflow
//      refs stay authoritative (a discontinued one is refused).
//   2. action.bindExternal persists hrc:<runId> and rejects conflicting refs.
//   3. leased runs: heartbeat/renew/complete are token-guarded and tokens never
//      leak through show/list.
//   4. action.complete records run-linked evidence, applies the transition
//      (unless transition:false), finishes the run, and replays side-effect free.
//   5. action.fail records failure evidence and fails the run.
//   6. the full start -> bindExternal -> complete -> list flow in one session.
//   7. action.list includeClosedInstances spans workflow instances.
//   8. no legacy cp_*/run_status task fields are read or written.

import (
	"database/sql"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

const actActor = "agent:action-tester"

// actStart starts an action with params (principal_ref defaults to actActor)
// and returns its run id.
func actStart(t *testing.T, dbPath, label string, params map[string]any) string {
	t.Helper()
	if _, ok := params["principal_ref"]; !ok {
		params["principal_ref"] = actActor
	}
	frames := p3Run(t, dbPath, mkRPC("start", "wrkf.action.start", params))
	return actRunID(t, p2ResultOrFail(t, frames[1], label), label)
}

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

// actSeedSpecification sets a non-empty specification on the seeded task so the
// triage_complete transition resolves task.has_specification=true and takes the
// `ready` outcome. Without a spec, triage_complete blocks the task (the
// blocked_no_spec doctrine in wrkq-simple-task), which derails the happy-path
// lifecycle these tests exercise. The real triage deliverable is the
// specification; the action surface does not author it, so the test seeds it.
func actSeedSpecification(t *testing.T, dbPath, taskUUID, spec string) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("actSeedSpecification: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	if _, err := database.Exec(
		`UPDATE tasks SET specification = ? WHERE uuid = ?`, spec, taskUUID,
	); err != nil {
		t.Fatalf("actSeedSpecification: UPDATE: %v", err)
	}
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
			"action":         "implement",
			"principal_ref":  actActor,
			"idempotencyKey": "act:start:1",
		}),
		mkRPC("s2", "wrkf.action.start", map[string]any{
			"task":           taskID,
			"action":         "implement",
			"principal_ref":  actActor,
			"idempotencyKey": "act:start:1",
		}),
		mkRPC("l1", "wrkf.run.list", map[string]any{"task": taskID}),
	)
	r1 := p2ResultOrFail(t, frames[1], "wrkf.action.start")
	runID := actRunID(t, r1, "action.start")
	if got, _ := r1["actionRunId"].(string); got != runID {
		t.Errorf("actionRunId %q must equal runId %q", got, runID)
	}
	if got, _ := r1["action"].(string); got != "implement" {
		t.Errorf("action = %q, want implement", got)
	}
	if got, _ := r1["role"].(string); got != "implementer" {
		t.Errorf("role defaulted to %q, want implementer", got)
	}
	if got, _ := r1["status"].(string); got != "active" {
		t.Errorf("status = %q, want active", got)
	}
	wf, _ := r1["workflow"].(map[string]any)
	if wf == nil || wf["id"] != "wrkq-simple-task" || wf["version"] != "5" {
		t.Errorf("workflow = %#v, want built-in wrkq-simple-task@5", wf)
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

func TestWrkfActionStart_DefaultV5WithV1DiscontinuedAndExplicitRefsRemainAuthoritative(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	v1Task := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000101",
		"action-explicit-v1-seed", "Action Explicit V1 Seed")
	v5InstallTask := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000102",
		"action-explicit-v5-seed", "Action Explicit V5 Seed")
	defaultTask := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000103",
		"action-default-v5", "Action Default V5")
	discontinuedTask := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000104",
		"action-explicit-discontinued", "Action Explicit Discontinued")
	explicitV2Task := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000105",
		"action-explicit-v2", "Action Explicit V2")

	frames := p3Run(t, dbPath,
		mkRPC("seed-v1", "wrkf.action.start", map[string]any{
			"task": v1Task, "workflow": "wrkq-simple-task@1", "action": "triage", "principal_ref": actActor,
		}),
		mkRPC("discontinue-v1", "wrkf.workflow.discontinue", map[string]any{
			"ref": "wrkq-simple-task@1", "principal_ref": "agent:curator",
		}),
		mkRPC("install-v5", "wrkq.workflow.attach", map[string]any{
			"task": v5InstallTask, "workflow": "wrkq-simple-task@5", "actor": actActor,
		}),
		mkRPC("default", "wrkf.action.start", map[string]any{
			"task": defaultTask, "action": "implement", "principal_ref": actActor,
		}),
		mkRPC("explicit-discontinued", "wrkf.action.start", map[string]any{
			"task": discontinuedTask, "workflow": "wrkq-simple-task@1", "action": "triage", "principal_ref": actActor,
		}),
		mkRPC("explicit-v2", "wrkf.action.start", map[string]any{
			"task": explicitV2Task, "workflow": "wrkq-simple-task@2", "action": "triage", "principal_ref": actActor,
		}),
	)
	p2ResultOrFail(t, frames[1], "seed explicit v1")
	p2ResultOrFail(t, frames[2], "discontinue v1")
	p2ResultOrFail(t, frames[3], "ensure-install explicit v5")

	defaultRun := p2ResultOrFail(t, frames[4], "bare default")
	defaultWorkflow, _ := defaultRun["workflow"].(map[string]any)
	if defaultWorkflow == nil || defaultWorkflow["id"] != "wrkq-simple-task" || defaultWorkflow["version"] != "5" {
		t.Fatalf("bare workflow = %#v, want wrkq-simple-task@5", defaultWorkflow)
	}
	if got := p2ErrCode(frames[5]); got != "WRKF_VALIDATION" {
		t.Fatalf("explicit discontinued v1 code = %q, want WRKF_VALIDATION; frame=%#v", got, frames[5])
	}
	if got := p2ErrDataField(frames[5], "field"); got != "workflow" {
		t.Fatalf("explicit discontinued v1 field = %#v, want workflow", got)
	}

	explicitV2Run := p2ResultOrFail(t, frames[6], "explicit v2")
	explicitV2Workflow, _ := explicitV2Run["workflow"].(map[string]any)
	if explicitV2Workflow == nil || explicitV2Workflow["version"] != "2" {
		t.Fatalf("explicit workflow = %#v, want wrkq-simple-task@2", explicitV2Workflow)
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

	runID := actStart(t, dbPath, "start", map[string]any{"task": taskID, "action": "triage"})

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
	runID := actStart(t, dbPath, "start", map[string]any{"task": taskID, "action": "triage"})

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

func TestWrkfActionLeaseHeartbeatAndTokenGuards(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000030",
		"action-lease", "Action Lease")

	startFrames := p3Run(t, dbPath,
		mkRPC("s1", "wrkf.action.start", map[string]any{
			"task":           taskID,
			"action":         "triage",
			"principal_ref":  actActor,
			"idempotencyKey": "act:lease:start:1",
			"leaseOwner":     "agent-loop:test",
			"leaseMs":        60000,
		}),
	)
	started := p2ResultOrFail(t, startFrames[1], "leased action.start")
	runID := actRunID(t, started, "leased start")
	token, _ := started["leaseToken"].(string)
	if token == "" {
		t.Fatalf("leased action.start did not return leaseToken: %#v", started)
	}
	if got, _ := started["leaseOwner"].(string); got != "agent-loop:test" {
		t.Fatalf("leaseOwner = %q, want agent-loop:test", got)
	}
	readFrames := p3Run(t, dbPath,
		mkRPC("show", "wrkf.action.show", map[string]any{"actionRunId": runID}),
		mkRPC("list", "wrkf.action.list", map[string]any{"task": taskID}),
	)
	shown := p2ResultOrFail(t, readFrames[1], "action.show")
	if _, ok := shown["leaseToken"]; ok {
		t.Fatalf("action.show leaked leaseToken: %#v", shown)
	}
	listed := p2ResultOrFail(t, readFrames[2], "action.list")
	items, _ := listed["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("action.list items = %#v, want one item", listed["items"])
	}
	if item, _ := items[0].(map[string]any); item != nil {
		if _, ok := item["leaseToken"]; ok {
			t.Fatalf("action.list leaked leaseToken: %#v", item)
		}
	}

	guardFrames := p3Run(t, dbPath,
		mkRPC("hbBad", "wrkf.action.heartbeat", map[string]any{"actionRunId": runID, "leaseToken": "wrong-token", "leaseMs": 60000}),
		mkRPC("hb", "wrkf.action.heartbeat", map[string]any{"actionRunId": runID, "leaseToken": token, "leaseMs": 60000}),
		mkRPC("renew", "wrkf.action.renewLease", map[string]any{"actionRunId": runID, "leaseToken": token, "leaseMs": 60000}),
		mkRPC("completeBad", "wrkf.action.complete", map[string]any{
			"actionRunId": runID,
			"transition":  false,
			"evidence":    map[string]any{"summary": "missing token must fail"},
		}),
		mkRPC("complete", "wrkf.action.complete", map[string]any{
			"actionRunId": runID,
			"leaseToken":  token,
			"transition":  false,
			"evidence":    map[string]any{"summary": "leased complete"},
		}),
	)
	if code := p2ErrCode(guardFrames[1]); code != "WRKF_LEASE_CONFLICT" {
		t.Fatalf("wrong-token heartbeat code = %q, want WRKF_LEASE_CONFLICT", code)
	}
	heartbeat := p2ResultOrFail(t, guardFrames[2], "action.heartbeat")
	if got, _ := heartbeat["leaseToken"].(string); got != token {
		t.Fatalf("heartbeat leaseToken = %q, want original token", got)
	}
	p2ResultOrFail(t, guardFrames[3], "action.renewLease")
	if code := p2ErrCode(guardFrames[4]); code != "WRKF_LEASE_CONFLICT" {
		t.Fatalf("complete without token code = %q, want WRKF_LEASE_CONFLICT", code)
	}
	completed := p2ResultOrFail(t, guardFrames[5], "leased action.complete")
	completedRun, _ := completed["run"].(map[string]any)
	if got, _ := completedRun["status"].(string); got != "completed" {
		t.Fatalf("leased complete status = %q, want completed", got)
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
	actSeedSpecification(t, dbPath, "a5000000-0000-4000-8000-000000000004", "spec: triaged deliverable")
	runID := actStart(t, dbPath, "start", map[string]any{"task": taskID, "workflow": "wrkq-simple-task@1", "action": "triage"})

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
	runID := actStart(t, dbPath, "start", map[string]any{"task": taskID, "workflow": "wrkq-simple-task@1", "action": "triage"})

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
	runID := actStart(t, dbPath, "start", map[string]any{"task": taskID, "workflow": "wrkq-simple-task@1", "action": "triage"})

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

	runID := actStart(t, dbPath, "start", map[string]any{"task": taskID, "workflow": "wrkq-simple-task@1", "action": "triage"})

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
	actSeedSpecification(t, dbPath, "a5000000-0000-4000-8000-000000000008", "spec: triaged deliverable")

	// Drive a full lifecycle through the simple workflow to close the first
	// instance: triage -> implement -> verify -> review (review_complete closes).
	for i, action := range []string{"triage", "implement", "verify", "review"} {
		params := map[string]any{"task": taskID, "action": action}
		if i == 0 {
			params["workflow"] = "wrkq-simple-task@1"
		}
		runID := actStart(t, dbPath, action+" start", params)
		p3Run(t, dbPath, mkRPC("c-"+action, "wrkf.action.complete", map[string]any{"actionRunId": runID, "evidence": map[string]any{"summary": action[:1]}}))
	}

	// First instance is now closed. Start a NEW action — attaches a new instance.
	newRun := actStart(t, dbPath, "new triage start", map[string]any{"task": taskID, "action": "triage"})

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

	runID := actStart(t, dbPath, "start", map[string]any{"task": taskID, "action": "triage"})
	p3Run(t, dbPath, mkRPC("c1", "wrkf.action.complete", map[string]any{"actionRunId": runID, "evidence": map[string]any{"summary": "ok"}}))

	assertLegacyTaskFieldsUntouched(t, dbPath, "a5000000-0000-4000-8000-000000000009")
}
