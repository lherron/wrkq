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
	"fmt"
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
			"action":         "triage",
			"principal_ref":  actActor,
			"idempotencyKey": "act:start:1",
		}),
		mkRPC("s2", "wrkf.action.start", map[string]any{
			"task":           taskID,
			"action":         "triage",
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

func TestWrkfActionNextV2CandidatesAndSourceBinding(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000033",
		"action-next-v2", "Action Next V2")

	triageFrames := p3Run(t, dbPath,
		mkRPC("s1", "wrkf.action.start", map[string]any{
			"task": taskID, "workflow": "wrkq-simple-task@2", "action": "triage", "principal_ref": actActor,
		}),
	)
	triageRun := actRunID(t, p2ResultOrFail(t, triageFrames[1], "start triage"), "start triage")
	frames := p3Run(t, dbPath,
		mkRPC("c1", "wrkf.action.complete", map[string]any{
			"actionRunId": triageRun,
			"evidence":    map[string]any{"summary": "triaged", "facts": map[string]any{"result": "ready"}},
		}),
		mkRPC("next-impl", "wrkf.action.next", map[string]any{"task": taskID}),
	)
	p2ResultOrFail(t, frames[1], "complete triage")
	nextImpl := p2ResultOrFail(t, frames[2], "action.next implement")
	implCandidates, _ := nextImpl["candidates"].([]any)
	if len(implCandidates) != 1 {
		t.Fatalf("implement candidates = %#v, want one", nextImpl["candidates"])
	}
	implCandidate, _ := implCandidates[0].(map[string]any)
	if implCandidate["action"] != "implement" || implCandidate["requiredEvidenceKind"] != "implement_result" {
		t.Fatalf("implement candidate = %#v", implCandidate)
	}

	implFrames := p3Run(t, dbPath,
		mkRPC("s2", "wrkf.action.start", map[string]any{"task": taskID, "action": "implement", "principal_ref": actActor}),
	)
	implRun := actRunID(t, p2ResultOrFail(t, implFrames[1], "start implement"), "start implement")
	verifyFrames := p3Run(t, dbPath,
		mkRPC("c2", "wrkf.action.complete", map[string]any{
			"actionRunId": implRun,
			"evidence": map[string]any{
				"summary": "implemented",
				"facts": map[string]any{
					"result":        "done",
					"commit.sha":    "abc123",
					"change.id":     "change-v1:abc123",
					"git.clean":     true,
					"base.sha":      "base000",
					"postcondition": "git_committed_clean",
					"repair.turns":  0,
				},
			},
		}),
		mkRPC("next-verify", "wrkf.action.next", map[string]any{"task": taskID}),
	)
	p2ResultOrFail(t, verifyFrames[1], "complete implement")
	nextVerify := p2ResultOrFail(t, verifyFrames[2], "action.next verify")
	verifyCandidates, _ := nextVerify["candidates"].([]any)
	if len(verifyCandidates) != 1 {
		t.Fatalf("verify candidates = %#v, want one", nextVerify["candidates"])
	}
	verifyCandidate, _ := verifyCandidates[0].(map[string]any)
	if verifyCandidate["action"] != "verify" {
		t.Fatalf("verify candidate = %#v", verifyCandidate)
	}
	source, _ := verifyCandidate["source"].(map[string]any)
	// Post-§2.3: the source binding surfaces the lane-computed change identity
	// (bindFields.sourceIdentity = change.id), not the dropped commitSha authority.
	if source == nil || source["sourceRunId"] != implRun || source["sourceIdentity"] != "change-v1:abc123" {
		t.Fatalf("verify source = %#v, want run %s identity change-v1:abc123", source, implRun)
	}
	// semanticActionKey identifies the action occurrence by instance revision and
	// no longer embeds the source run/commit (see semanticActionKey in action_next.go).
	instanceID, _ := verifyCandidate["instanceId"].(string)
	rev, _ := verifyCandidate["expectedStateRevision"].(float64)
	wantKey := fmt.Sprintf("verify:%s:r%d", instanceID, int64(rev))
	key, _ := verifyCandidate["semanticActionKey"].(string)
	if key != wantKey {
		t.Fatalf("semanticActionKey = %q, want action occurrence %q", key, wantKey)
	}
}

func TestWrkfActionClaimV2FencedRunAndSuccession(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000034",
		"action-claim-v2", "Action Claim V2")

	triageFrames := p3Run(t, dbPath,
		mkRPC("s1", "wrkf.action.start", map[string]any{
			"task": taskID, "workflow": "wrkq-simple-task@2", "action": "triage", "principal_ref": actActor,
		}),
	)
	triageRun := actRunID(t, p2ResultOrFail(t, triageFrames[1], "start triage"), "start triage")
	readyFrames := p3Run(t, dbPath,
		mkRPC("c1", "wrkf.action.complete", map[string]any{
			"actionRunId": triageRun,
			"evidence":    map[string]any{"summary": "triaged", "facts": map[string]any{"result": "ready"}},
		}),
		mkRPC("claim-1", "wrkf.action.claim", map[string]any{
			"task": taskID, "runnerId": "runner-a", "agentRef": "agent:cody", "scopeRef": "cody@wrkq:T-05386", "leaseMs": float64(300000), "priorRun": nil,
		}),
	)
	p2ResultOrFail(t, readyFrames[1], "complete triage")
	claim1 := p2ResultOrFail(t, readyFrames[2], "claim implement")

	binding1 := actClaimBinding(t, claim1, "claim implement")
	run1, _ := binding1["run"].(map[string]any)
	refusedFrames := p3Run(t, dbPath, mkRPC("claim-refused", "wrkf.action.claim", map[string]any{
		"task": taskID, "runnerId": "runner-b", "agentRef": "agent:larry", "leaseMs": float64(300000),
	}))
	errObj, _ := refusedFrames[1]["error"].(map[string]any)
	errData, _ := errObj["data"].(map[string]any)
	predecessor, _ := errData["predecessor"].(map[string]any)
	if errData["code"] != "WRKF_LEASE_CONFLICT" || predecessor["runId"] != run1["id"] || predecessor["owner"] != "runner-a" {
		t.Fatalf("claim refusal payload = %#v, want full named predecessor", errObj)
	}
	for _, field := range []string{"claimedAt", "heartbeatAt", "expiresAt", "settleStatus", "sideEffectClasses", "evidenceWritten"} {
		if _, ok := predecessor[field]; !ok {
			t.Fatalf("claim refusal predecessor missing %s: %#v", field, predecessor)
		}
	}
	successorFrames := p3Run(t, dbPath, mkRPC("claim-2", "wrkf.action.claim", map[string]any{
		"task": taskID, "runnerId": "runner-a", "agentRef": "agent:cody", "scopeRef": "cody@wrkq:T-05386", "leaseMs": float64(300000), "priorRun": run1["id"],
	}))
	claim2 := p2ResultOrFail(t, successorFrames[1], "claim implement successor")
	binding2 := actClaimBinding(t, claim2, "claim implement successor")
	run2, _ := binding2["run"].(map[string]any)
	if run1["action"] != "implement" || run1["role"] != "implementer" {
		t.Fatalf("claimed run = %#v, want implement/implementer", run1)
	}
	if run1["id"] == "" || run2["id"] == run1["id"] || run2["predecessorRunId"] != run1["id"] {
		t.Fatalf("claim succession mismatch: predecessor=%#v successor=%#v", run1, run2)
	}
	if run1["semanticActionKey"] == "" {
		t.Fatalf("claimed run missing semanticActionKey: %#v", run1)
	}
	auth1, _ := binding1["authority"].(map[string]any)
	auth2, _ := binding2["authority"].(map[string]any)
	if auth1["ownerToken"] == "" || auth1["runnerId"] != "runner-a" {
		t.Fatalf("authority = %#v, want runner-a token", auth1)
	}
	if auth2["ownerGeneration"] != float64(1) {
		t.Fatalf("successor ownerGeneration = %#v, want 1", auth2["ownerGeneration"])
	}
	if auth2["ownerToken"] == auth1["ownerToken"] {
		t.Fatalf("successor should have a distinct owner token")
	}

	showFrames := p3Run(t, dbPath,
		mkRPC("show", "wrkf.action.show", map[string]any{"actionRunId": run1["id"]}),
	)
	show := p2ResultOrFail(t, showFrames[1], "action.show claimed run")
	if _, ok := show["leaseToken"]; ok {
		t.Fatalf("action.show exposed leaseToken: %#v", show)
	}
}

func TestWrkfActionClaimSuspendedRefusalPayload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000099",
		"action-claim-suspended", "Action Claim Suspended")
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

	first := actRPCClaim(t, dbPath, taskID, "test")
	actRPCSettle(t, dbPath, first, map[string]any{"result": "operator_required"}, "parked")
	showFrames := p3Run(t, dbPath,
		mkRPC("show", "wrkf.instance.show", map[string]any{"task": taskID}),
	)
	instance := p2ResultOrFail(t, showFrames[1], "show suspended instance")
	suspension, _ := instance["suspension"].(map[string]any)
	if suspension == nil || suspension["id"] == "" || suspension["reason"] != "operator_required" || suspension["at"] == "" || suspension["causeRef"] == "" {
		t.Fatalf("active suspension = %#v, want complete record", suspension)
	}

	wrong := "run-not-the-predecessor"
	refusedFrames := p3Run(t, dbPath,
		mkRPC("claim-refused", "wrkf.action.claim", map[string]any{
			"task": taskID, "prefer": map[string]any{"action": "test"},
			"runnerId": "runner-successor", "agentRef": "agent:successor",
			"leaseMs": float64(300000), "priorRun": wrong,
		}),
	)
	errObj, _ := refusedFrames[1]["error"].(map[string]any)
	errData, _ := errObj["data"].(map[string]any)
	gotSuspension, _ := errData["suspension"].(map[string]any)
	if errData["code"] != "WRKF_SUSPENDED" {
		t.Fatalf("claim refusal = %#v, want WRKF_SUSPENDED", errObj)
	}
	for _, field := range []string{"id", "reason", "at", "causeRef"} {
		if gotSuspension[field] != suspension[field] {
			t.Fatalf("claim refusal suspension[%s] = %#v, want %#v", field, gotSuspension[field], suspension[field])
		}
	}
	if _, ok := errData["predecessor"]; ok {
		t.Fatalf("suspended claim refusal leaked predecessor dossier: %#v", errData)
	}

	resumeFrames := p3Run(t, dbPath,
		mkRPC("resume", "wrkf.suspension.resolve", map[string]any{
			"suspensionId": suspension["id"], "disposition": "resume", "principal_ref": actActor,
		}),
		mkRPC("claim-after-resume", "wrkf.action.claim", map[string]any{
			"task": taskID, "prefer": map[string]any{"action": "test"},
			"runnerId": "runner-successor", "agentRef": "agent:successor",
			"leaseMs": float64(300000), "priorRun": nil,
		}),
	)
	p2ResultOrFail(t, resumeFrames[1], "resume suspended instance")
	claim := p2ResultOrFail(t, resumeFrames[2], "claim after resume")
	if binding := actClaimBinding(t, claim, "claim after resume"); binding["run"] == nil {
		t.Fatalf("claim after resume missing run: %#v", claim)
	}
}

func TestWrkfActionSettleV2ClaimedFlowAndSourceCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000035",
		"action-settle-v2", "Action Settle V2")
	tplPath := "internal/workflow/builtins/wrkq-simple-task-v2.workflow.json"
	attachFrames := p3Run(t, dbPath,
		mkRPC("i1", "wrkf.workflow.install", map[string]any{"body": templateBody(t, tplPath)}),
		mkRPC("a1", "wrkq.workflow.attach", map[string]any{
			"task":     taskID,
			"workflow": "wrkq-simple-task@2",
		}),
	)
	p2ResultOrFail(t, attachFrames[1], "install v2")
	p2ResultOrFail(t, attachFrames[2], "attach v2")

	triage := actRPCClaim(t, dbPath, taskID, "triage")
	actRPCSettle(t, dbPath, triage, map[string]any{"result": "ready"}, "triaged")
	impl := actRPCClaim(t, dbPath, taskID, "implement")
	actRPCSettle(t, dbPath, impl, map[string]any{
		"result":        "done",
		"commit.sha":    "abc123",
		"change.id":     "change-v1:abc123",
		"git.clean":     true,
		"base.sha":      "base000",
		"postcondition": "git_committed_clean",
		"repair.turns":  float64(0),
	}, "implemented")
	p3Run(t, dbPath,
		mkRPC("unrelated", "wrkf.evidence.add", map[string]any{
			"task": taskID, "kind": "implement_result", "ref": "manual:latest",
			"summary": "unrelated latest", "principal_ref": actActor, "role": "implementer",
			"facts": map[string]any{"result": "done", "commit.sha": "wrong-latest"},
		}),
	)

	verify := actRPCClaim(t, dbPath, taskID, "verify")
	run, _ := verify["run"].(map[string]any)
	source, _ := run["source"].(map[string]any)
	// Post-§2.3: the claimed verify source is bound by change identity, not commitSha.
	if source == nil || source["sourceIdentity"] != "change-v1:abc123" {
		t.Fatalf("verify claim source = %#v, want identity change-v1:abc123", source)
	}
	srcEvID, _ := source["sourceEvidenceId"].(string)
	// The wrong-source verify settle supplies every template-declared fact but echoes
	// a mismatched source commit; the settle contract's echo check must still reject it.
	wrong := p3Run(t, dbPath,
		mkRPC("bad-verify", "wrkf.action.settle", map[string]any{
			"runId":           run["id"],
			"ownerToken":      verify["ownerToken"],
			"ownerGeneration": verify["ownerGeneration"],
			"result":          "completed",
			"evidence": map[string]any{
				"summary": "verified wrong latest",
				"facts": map[string]any{
					"result":              "verified",
					"context.id":          "context-v1:abc123",
					"source.evidence_id":  srcEvID,
					"source.commit.sha":   "wrong-latest",
					"verified.commit.sha": "wrong-latest",
					"verified.change.id":  "change-v1:abc123",
					"git.clean":           true,
				},
			},
		}),
	)
	if _, ok := wrong[1]["error"]; !ok {
		t.Fatalf("wrong-source verify settle must error, got %#v", wrong[1])
	}
	final := actRPCSettle(t, dbPath, verify, map[string]any{
		"result":              "verified",
		"context.id":          "context-v1:abc123",
		"source.evidence_id":  srcEvID,
		"source.commit.sha":   "abc123",
		"verified.commit.sha": "abc123",
		"verified.change.id":  "change-v1:abc123",
		"git.clean":           true,
	}, "verified")
	tr, _ := final["transition"].(map[string]any)
	state, _ := tr["state"].(map[string]any)
	if state["status"] != "closed" || state["phase"] != "done" {
		t.Fatalf("final transition state = %#v, want closed/done", state)
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
			"task": taskID, "action": "triage", "principal_ref": actActor,
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

func actClaimBinding(t *testing.T, result map[string]any, label string) map[string]any {
	t.Helper()
	binding, _ := result["binding"].(map[string]any)
	if binding == nil {
		t.Fatalf("%s: result missing binding: %#v", label, result)
	}
	return binding
}

func actRPCClaim(t *testing.T, dbPath, taskID, action string) map[string]any {
	t.Helper()
	frames := p3Run(t, dbPath,
		mkRPC("claim-"+action, "wrkf.action.claim", map[string]any{
			"task": taskID, "prefer": map[string]any{"action": action},
			"runnerId": "runner-" + action, "agentRef": "agent:" + action, "leaseMs": float64(300000), "priorRun": nil,
		}),
	)
	if errObj, ok := frames[1]["error"].(map[string]any); ok {
		data, _ := errObj["data"].(map[string]any)
		predecessor, _ := data["predecessor"].(map[string]any)
		if priorRun, _ := predecessor["runId"].(string); priorRun != "" {
			frames = p3Run(t, dbPath, mkRPC("claim-"+action+"-successor", "wrkf.action.claim", map[string]any{
				"task": taskID, "prefer": map[string]any{"action": action},
				"runnerId": "runner-" + action, "agentRef": "agent:" + action, "leaseMs": float64(300000), "priorRun": priorRun,
			}))
		}
	}
	binding := actClaimBinding(t, p2ResultOrFail(t, frames[1], "claim "+action), "claim "+action)
	run, _ := binding["run"].(map[string]any)
	auth, _ := binding["authority"].(map[string]any)
	if run == nil || auth == nil {
		t.Fatalf("claim %s binding = %#v", action, binding)
	}
	return map[string]any{
		"run":             run,
		"ownerToken":      auth["ownerToken"],
		"ownerGeneration": auth["ownerGeneration"],
	}
}

func actRPCSettle(t *testing.T, dbPath string, claim map[string]any, facts map[string]any, summary string) map[string]any {
	t.Helper()
	run, _ := claim["run"].(map[string]any)
	frames := p3Run(t, dbPath,
		mkRPC("settle-"+summary, "wrkf.action.settle", map[string]any{
			"runId":           run["id"],
			"ownerToken":      claim["ownerToken"],
			"ownerGeneration": claim["ownerGeneration"],
			"result":          "completed",
			"evidence": map[string]any{
				"summary": summary,
				"facts":   facts,
			},
		}),
	)
	return p2ResultOrFail(t, frames[1], "settle "+summary)
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
		mkRPC("s1", "wrkf.action.start", map[string]any{"task": taskID, "action": "triage", "principal_ref": actActor}),
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
	startFrames := p3Run(t, dbPath,
		mkRPC("s1", "wrkf.action.start", map[string]any{"task": taskID, "action": "triage", "principal_ref": actActor}),
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
		mkRPC("s1", "wrkf.action.start", map[string]any{"task": taskID, "action": "triage", "principal_ref": actActor}),
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
		mkRPC("s1", "wrkf.action.start", map[string]any{"task": taskID, "action": "triage", "principal_ref": actActor}),
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
		mkRPC("s1", "wrkf.action.start", map[string]any{"task": taskID, "action": "triage", "principal_ref": actActor}),
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
	actSeedSpecification(t, dbPath, "a5000000-0000-4000-8000-000000000008", "spec: triaged deliverable")

	// Drive a full lifecycle through the simple workflow to close the first
	// instance: triage -> implement -> verify -> review (review_complete closes).
	driveFrames := p3Run(t, dbPath,
		mkRPC("t1", "wrkf.action.start", map[string]any{"task": taskID, "action": "triage", "principal_ref": actActor}),
	)
	triageRun := actRunID(t, p2ResultOrFail(t, driveFrames[1], "triage start"), "triage start")
	p3Run(t, dbPath, mkRPC("tc", "wrkf.action.complete", map[string]any{"actionRunId": triageRun, "evidence": map[string]any{"summary": "t"}}))

	implFrames := p3Run(t, dbPath, mkRPC("i1", "wrkf.action.start", map[string]any{"task": taskID, "action": "implement", "principal_ref": actActor}))
	implRun := actRunID(t, p2ResultOrFail(t, implFrames[1], "impl start"), "impl start")
	p3Run(t, dbPath, mkRPC("ic", "wrkf.action.complete", map[string]any{"actionRunId": implRun, "evidence": map[string]any{"summary": "i"}}))

	verFrames := p3Run(t, dbPath, mkRPC("v1", "wrkf.action.start", map[string]any{"task": taskID, "action": "verify", "principal_ref": actActor}))
	verRun := actRunID(t, p2ResultOrFail(t, verFrames[1], "verify start"), "verify start")
	p3Run(t, dbPath, mkRPC("vc", "wrkf.action.complete", map[string]any{"actionRunId": verRun, "evidence": map[string]any{"summary": "v"}}))

	revFrames := p3Run(t, dbPath, mkRPC("r1", "wrkf.action.start", map[string]any{"task": taskID, "action": "review", "principal_ref": actActor}))
	revRun := actRunID(t, p2ResultOrFail(t, revFrames[1], "review start"), "review start")
	p3Run(t, dbPath, mkRPC("rc", "wrkf.action.complete", map[string]any{"actionRunId": revRun, "evidence": map[string]any{"summary": "r"}}))

	// First instance is now closed. Start a NEW action — attaches a new instance.
	newFrames := p3Run(t, dbPath, mkRPC("n1", "wrkf.action.start", map[string]any{"task": taskID, "action": "triage", "principal_ref": actActor}))
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
		mkRPC("s1", "wrkf.action.start", map[string]any{"task": taskID, "action": "triage", "principal_ref": actActor}),
	)
	runID := actRunID(t, p2ResultOrFail(t, startFrames[1], "start"), "start")
	p3Run(t, dbPath, mkRPC("c1", "wrkf.action.complete", map[string]any{"actionRunId": runID, "evidence": map[string]any{"summary": "ok"}}))

	assertLegacyTaskFieldsUntouched(t, dbPath, "a5000000-0000-4000-8000-000000000009")
}
