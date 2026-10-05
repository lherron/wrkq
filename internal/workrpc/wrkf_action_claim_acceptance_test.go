//go:build wrkq_local

package workrpc_test

// wrkf_action_claim_acceptance_test.go — the fenced claim/settle protocol of
// wrkf.action.* on wrkq-simple-task@2+: wrkf.action.next candidates and their
// source binding, wrkf.action.claim leases with named-predecessor refusal and
// succession, the WRKF_SUSPENDED refusal on a suspended instance, and
// wrkf.action.settle's source-echo check (T-05386).

import (
	"fmt"
	"testing"
)

// attachBuiltinSimpleTask installs the built-in wrkq-simple-task@version
// template and attaches it to taskID.
func attachBuiltinSimpleTask(t *testing.T, dbPath, taskID string, version int) {
	t.Helper()
	path := fmt.Sprintf("internal/workflow/builtins/wrkq-simple-task-v%d.workflow.json", version)
	frames := p3Run(t, dbPath,
		mkRPC("install", "wrkf.workflow.install", map[string]any{"body": templateBody(t, path)}),
		mkRPC("attach", "wrkq.workflow.attach", map[string]any{"task": taskID, "workflow": fmt.Sprintf("wrkq-simple-task@%d", version)}),
	)
	p2ResultOrFail(t, frames[1], fmt.Sprintf("install v%d", version))
	p2ResultOrFail(t, frames[2], fmt.Sprintf("attach v%d", version))
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

func TestWrkfActionNextV2CandidatesAndSourceBinding(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000033",
		"action-next-v2", "Action Next V2")

	triageRun := actStart(t, dbPath, "start triage", map[string]any{"task": taskID, "workflow": "wrkq-simple-task@2", "action": "triage"})
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

	implRun := actStart(t, dbPath, "start implement", map[string]any{"task": taskID, "action": "implement"})
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

	triageRun := actStart(t, dbPath, "start triage", map[string]any{"task": taskID, "workflow": "wrkq-simple-task@2", "action": "triage"})
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
	if settled, ok := predecessor["settled"].(bool); !ok || settled {
		t.Fatalf("active claim refusal predecessor settled = %#v, want false", predecessor["settled"])
	}
	for _, field := range []string{"claimedAt", "heartbeatAt", "expiresAt", "settleStatus", "settled", "sideEffectClasses", "evidenceWritten"} {
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
	attachBuiltinSimpleTask(t, dbPath, taskID, 5)

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
	attachBuiltinSimpleTask(t, dbPath, taskID, 2)

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
