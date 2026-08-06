//go:build wrkq_local

package workflow

// operator_run_guard_test.go — T-06235. Covers the transactional
// no-active-run guard for operator-class transitions (requiresNoActiveRun),
// evidence-schema minLength preservation/enforcement, and the multi-source
// `from` (fromAny) matcher with its closed-state guard.

import (
	"encoding/json"
	"strings"
	"testing"
)

// installOperatorGuardTemplate installs a v2-derived template whose
// operator_resolved transition is marked requiresNoActiveRun and made legal
// from the active/ready and active/implemented phases via fromAny, so the ONLY
// thing that can refuse it from an active phase is the run guard.
func installOperatorGuardTemplate(t *testing.T, svc *Service, taskUUID string) {
	t.Helper()
	doc := builtinV2Doc(t)
	doc["id"] = "operator-guard-v2"
	transitions := doc["transitions"].([]any)
	for _, raw := range transitions {
		tr := raw.(map[string]any)
		if tr["id"] == "operator_resolved" {
			tr["requiresNoActiveRun"] = true
			tr["fromAny"] = []any{
				map[string]any{"status": "active", "phase": "ready"},
				map[string]any{"status": "active", "phase": "implemented"},
				map[string]any{"status": "waiting", "phase": "operator_required"},
			}
		}
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal operator-guard template: %v", err)
	}
	tpl, canonical, err := ParseTemplate(data)
	if err != nil {
		t.Fatalf("ParseTemplate(operator-guard): %v", err)
	}
	if _, err := svc.installTemplateCanonical(tpl, canonical, Hash(canonical), "agent:t", nil, false); err != nil {
		t.Fatalf("install operator-guard template: %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, "operator-guard-v2@2", "agent:t"); err != nil {
		t.Fatalf("AttachTask(operator-guard): %v", err)
	}
}

func taskStateForTest(t *testing.T, svc *Service, taskUUID string) string {
	t.Helper()
	var state string
	if err := svc.db.QueryRow(`SELECT state FROM tasks WHERE uuid = ?`, taskUUID).Scan(&state); err != nil {
		t.Fatalf("read task state: %v", err)
	}
	return state
}

// TestOperatorTransitionRefusedWithActiveRun is the spec negative test: an
// operator-class transition (requiresNoActiveRun) is refused while the instance
// holds an open action run, and the refusal leaves the instance revision and
// task state unchanged. Expiry alone does not release the guard; late settlement
// by the current seat does.
func TestOperatorTransitionRefusedWithActiveRun(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	installOperatorGuardTemplate(t, svc, taskUUID)

	// Advance to active/ready and open an implement run (claimed, not settled).
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	implement := claimActionForTest(t, svc, taskUUID, "implement")

	// Supervisor has an operator resolution recorded and wants to cancel.
	addOperatorResolution(t, svc, taskUUID, "cancelled", "operator cancelling wedged task")

	inst, err := resolveInstanceSelectors(svc.db, taskUUID, "")
	if err != nil {
		t.Fatalf("resolve instance: %v", err)
	}
	if inst.Status != "active" || inst.Phase != "ready" {
		t.Fatalf("pre-guard state = %s/%s, want active/ready", inst.Status, inst.Phase)
	}
	revBefore := inst.Revision
	stateBefore := taskStateForTest(t, svc, taskUUID)

	// Guard must refuse while the implement run is open.
	_, err = svc.Transition(taskUUID, "operator_resolved", TransitionOptions{PrincipalRef: "agent:supervisor", Role: "supervisor"})
	if err == nil {
		t.Fatalf("operator_resolved with active run: expected refusal, got success")
	}
	detail, ok := AsErrorDetail(err)
	if !ok || detail.Code != wrkfCodeActiveRunGuard {
		t.Fatalf("operator_resolved refusal code = %q (%v), want %s", detail.Code, err, wrkfCodeActiveRunGuard)
	}
	if strings.Contains(detail.Fix, "reap") || !strings.Contains(detail.Fix, "action settle") || !strings.Contains(detail.Fix, "--prior-run") {
		t.Fatalf("operator_resolved remedy = %q, want settlement/succession guidance without reaper vocabulary", detail.Fix)
	}

	// Refusal is transactional: revision and task state unchanged.
	after, err := resolveInstanceSelectors(svc.db, taskUUID, "")
	if err != nil {
		t.Fatalf("resolve instance after refusal: %v", err)
	}
	if after.Revision != revBefore {
		t.Fatalf("instance revision moved on refusal: before=%d after=%d", revBefore, after.Revision)
	}
	if after.Status != "active" || after.Phase != "ready" {
		t.Fatalf("instance state moved on refusal: %s/%s, want active/ready", after.Status, after.Phase)
	}
	if got := taskStateForTest(t, svc, taskUUID); got != stateBefore {
		t.Fatalf("task state moved on refusal: before=%q after=%q", stateBefore, got)
	}

	// Dry-run must refuse for the same reason (honest preflight).
	_, err = svc.Transition(taskUUID, "operator_resolved", TransitionOptions{PrincipalRef: "agent:supervisor", Role: "supervisor", DryRun: true})
	if detail, _ := AsErrorDetail(err); detail.Code != wrkfCodeActiveRunGuard {
		t.Fatalf("dry-run refusal code = %q (%v), want %s", detail.Code, err, wrkfCodeActiveRunGuard)
	}

	// Expiry only makes the claim contestable. It does not terminalize the run or
	// release the operator guard.
	expireActionRunForTest(t, svc, implement.Binding.Run.ID)
	_, err = svc.Transition(taskUUID, "operator_resolved", TransitionOptions{PrincipalRef: "agent:supervisor", Role: "supervisor"})
	if detail, _ := AsErrorDetail(err); detail.Code != wrkfCodeActiveRunGuard {
		t.Fatalf("operator_resolved after lease expiry code = %q (%v), want %s", detail.Code, err, wrkfCodeActiveRunGuard)
	}

	// Positive control: the current seat may settle after expiry. Once settlement
	// terminalizes the run, the guard releases and the operator transition commits.
	settled := settleClaimForTest(t, svc, implement, `{"result":"done","commit.sha":"abc123","change.id":"change-v1:abc123","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`, "implemented after lease expiry")
	if settled.Run.Status != "completed" {
		t.Fatalf("late settlement status = %q, want completed", settled.Run.Status)
	}
	out, err := svc.Transition(taskUUID, "operator_resolved", TransitionOptions{PrincipalRef: "agent:supervisor", Role: "supervisor"})
	if err != nil {
		t.Fatalf("operator_resolved after settlement: %v", err)
	}
	if out["outcome"] != "cancelled" {
		t.Fatalf("operator_resolved outcome = %v, want cancelled", out["outcome"])
	}
	resolved, err := resolveInstanceSelectors(svc.db, taskUUID, "")
	if err != nil {
		t.Fatalf("resolve instance after resolve: %v", err)
	}
	if resolved.Status != "closed" || resolved.Phase != "cancelled" {
		t.Fatalf("post-resolve state = %s/%s, want closed/cancelled", resolved.Status, resolved.Phase)
	}
	if resolved.Revision <= revBefore {
		t.Fatalf("revision did not advance on successful resolve: before=%d after=%d", revBefore, resolved.Revision)
	}
}

// TestEvidenceMinLengthPreservedAndEnforced covers the validate-accepts/
// install-strips drift: a string fact minLength survives install (registry/
// show retains it) and is enforced when evidence is added.
func TestEvidenceMinLengthPreservedAndEnforced(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	doc := builtinV2Doc(t)
	doc["id"] = "minlength-v2"
	reason := doc["evidenceKinds"].(map[string]any)["operator_resolution"].(map[string]any)["facts"].(map[string]any)["properties"].(map[string]any)["reason"].(map[string]any)
	reason["minLength"] = float64(3)
	// Require reason so an empty/short value is actually reachable at add time.
	facts := doc["evidenceKinds"].(map[string]any)["operator_resolution"].(map[string]any)["facts"].(map[string]any)
	facts["required"] = []any{"resolution", "reason"}

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal minlength template: %v", err)
	}
	tpl, canonical, err := ParseTemplate(data)
	if err != nil {
		t.Fatalf("ParseTemplate(minlength): %v", err)
	}
	if _, err := svc.installTemplateCanonical(tpl, canonical, Hash(canonical), "agent:t", nil, false); err != nil {
		t.Fatalf("install minlength template: %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, "minlength-v2@2", "agent:t"); err != nil {
		t.Fatalf("AttachTask(minlength): %v", err)
	}

	// Preservation: the installed/shown definition must retain minLength, not
	// strip it (canonicalization must round-trip the constraint).
	shown, _, err := svc.ShowTemplate("minlength-v2@2")
	if err != nil {
		t.Fatalf("ShowTemplate(minlength): %v", err)
	}
	prop := shown.EvidenceKinds["operator_resolution"].Facts.Properties["reason"]
	if prop.MinLength != 3 {
		t.Fatalf("shown reason minLength = %d, want 3 (install stripped the constraint)", prop.MinLength)
	}

	// Enforcement: a too-short reason is rejected.
	_, err = svc.AddEvidence(AddEvidenceParams{
		TaskSelector: taskUUID,
		Kind:         "operator_resolution",
		Ref:          "operator:test",
		Summary:      "short",
		Facts:        `{"resolution":"cancelled","reason":"no"}`,
		PrincipalRef: "agent:supervisor",
		Role:         "supervisor",
	})
	if err == nil || !strings.Contains(err.Error(), "at least 3 characters") {
		t.Fatalf("short reason error = %v, want minLength rejection", err)
	}

	// A compliant reason is accepted.
	if _, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: taskUUID,
		Kind:         "operator_resolution",
		Ref:          "operator:test",
		Summary:      "ok",
		Facts:        `{"resolution":"cancelled","reason":"needs a human"}`,
		PrincipalRef: "agent:supervisor",
		Role:         "supervisor",
	}); err != nil {
		t.Fatalf("compliant reason rejected: %v", err)
	}
}

// TestTransitionFromAnyClosedStateGuard covers the multi-source `from` matcher
// and the closed-state guard: fromAny matches any listed source state, and a
// blank/wildcard from never implicitly matches a CLOSED instance.
func TestTransitionFromAnyClosedStateGuard(t *testing.T) {
	multi := TransitionSpec{
		ID: "operator_resolved",
		FromAny: []State{
			{Status: "active", Phase: "ready"},
			{Status: "active", Phase: "implemented"},
		},
	}
	cases := []struct {
		status, phase string
		want          bool
	}{
		{"active", "ready", true},
		{"active", "implemented", true},
		{"active", "intake", false},
		{"closed", "cancelled", false},
	}
	for _, tc := range cases {
		inst := Instance{Status: tc.status, Phase: tc.phase}
		if got := transitionFromMatches(inst, multi); got != tc.want {
			t.Fatalf("fromAny match %s/%s = %v, want %v", tc.status, tc.phase, got, tc.want)
		}
	}

	// Blank/wildcard From matches open states but never a closed instance.
	wildcard := TransitionSpec{ID: "reopen", From: State{}}
	if !transitionFromMatches(Instance{Status: "active", Phase: "ready"}, wildcard) {
		t.Fatalf("wildcard from should match an active instance")
	}
	if transitionFromMatches(Instance{Status: "closed", Phase: "done"}, wildcard) {
		t.Fatalf("wildcard from must NOT match a closed instance (reopen guard)")
	}
	// An explicit closed from still matches a closed instance.
	explicitClosed := TransitionSpec{ID: "archive", From: State{Status: "closed"}}
	if !transitionFromMatches(Instance{Status: "closed", Phase: "done"}, explicitClosed) {
		t.Fatalf("explicit closed from should match a closed instance")
	}
}

// TestExecutableActionFromAnyClosedGuard covers the candidate-legality surface:
// an executable action bound to a fromAny (blank-From) transition must NOT be
// offered from a closed instance — the same closed-state guard enforced
// everywhere else must hold here too.
func TestExecutableActionFromAnyClosedGuard(t *testing.T) {
	tpl := &Template{
		Transitions: []TransitionSpec{{
			ID:      "op",
			FromAny: []State{{Status: "active", Phase: "ready"}},
			By:      []string{"supervisor"},
		}},
	}
	spec := ExecutableActionSpec{Transition: "op", Role: "supervisor"} // spec.From nil → transition source applies

	offered := Instance{ID: "wfi_x", TaskRef: "wrkq:T-1", Status: "active", Phase: "ready"}
	_, block, err := candidateForExecutableAction(nil, &offered, nil, tpl, nil, "op", spec, 0)
	if err != nil {
		t.Fatalf("candidate (active/ready): %v", err)
	}
	if block != "" {
		t.Fatalf("action should be offered from active/ready, blocked: %s", block)
	}

	closed := Instance{ID: "wfi_x", TaskRef: "wrkq:T-1", Status: "closed", Phase: "done"}
	_, block, err = candidateForExecutableAction(nil, &closed, nil, tpl, nil, "op", spec, 0)
	if err != nil {
		t.Fatalf("candidate (closed): %v", err)
	}
	if block == "" {
		t.Fatalf("fromAny action must NOT be offered from a closed instance (reopen guard)")
	}
}