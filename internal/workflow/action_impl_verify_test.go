package workflow

// action_impl_verify_test.go — service-level tests for the fact-branched
// implement_complete / verify_complete outcomes added to the built-in
// wrkq-simple-task@1 workflow (T-05068). These cover daedalus's refined
// required-test set (#9778): no-facts backward-compat via `otherwise`,
// disposition-selected done/blocked outcomes with set_task_state effects,
// invalid-disposition rejection before the transition, facts-without-disposition
// routing to `otherwise`, and replay idempotency. The agent-loop evidence-builder
// test (disposition-always) is owned by the loop side, not here.

import (
	"encoding/json"
	"strings"
	"testing"
)

// driveToReady completes triage (the fixture seeds a spec) so the instance lands
// in active/ready, the from-state for implement_complete.
func driveToReady(t *testing.T, svc *Service, taskUUID string) {
	t.Helper()
	run, err := svc.StartAction(StartActionParams{
		Task: taskUUID, Workflow: BuiltinSimpleTaskTemplateRef, Action: "triage", PrincipalRef: "agent:t",
	})
	if err != nil {
		t.Fatalf("StartAction triage: %v", err)
	}
	if _, err := svc.CompleteAction(CompleteActionParams{ActionRunID: run.RunID, Evidence: &ActionEvidenceInput{Summary: "shaped"}}); err != nil {
		t.Fatalf("CompleteAction triage: %v", err)
	}
	inst, _ := svc.LatestInstance(taskUUID)
	if inst.Status != "active" || inst.Phase != "ready" {
		t.Fatalf("after triage: workflow = %s/%s, want active/ready", inst.Status, inst.Phase)
	}
}

// driveToImplemented advances to active/implemented via a no-facts implement
// completion (the `otherwise` path), the from-state for verify_complete.
func driveToImplemented(t *testing.T, svc *Service, taskUUID string) {
	t.Helper()
	driveToReady(t, svc, taskUUID)
	run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: "implement", PrincipalRef: "agent:t"})
	if err != nil {
		t.Fatalf("StartAction implement: %v", err)
	}
	if _, err := svc.CompleteAction(CompleteActionParams{ActionRunID: run.RunID, Evidence: &ActionEvidenceInput{Summary: "impl"}}); err != nil {
		t.Fatalf("CompleteAction implement: %v", err)
	}
	inst, _ := svc.LatestInstance(taskUUID)
	if inst.Status != "active" || inst.Phase != "implemented" {
		t.Fatalf("after implement: workflow = %s/%s, want active/implemented", inst.Status, inst.Phase)
	}
}

func completeAction(t *testing.T, svc *Service, taskUUID, action, facts string) *ActionCompleteResult {
	t.Helper()
	run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: action, PrincipalRef: "agent:t"})
	if err != nil {
		t.Fatalf("StartAction %s: %v", action, err)
	}
	out, err := svc.CompleteAction(CompleteActionParams{
		ActionRunID: run.RunID,
		Evidence:    &ActionEvidenceInput{Summary: action, Facts: facts},
	})
	if err != nil {
		t.Fatalf("CompleteAction %s (facts=%q): %v", action, facts, err)
	}
	return out
}

func outcomeOf(t *testing.T, out *ActionCompleteResult) string {
	t.Helper()
	if out.Transition == nil {
		t.Fatalf("expected a transition, got none")
	}
	id, _ := out.Transition["outcome"].(string)
	return id
}

// assertSetTaskStateDelivered asserts at least one delivered set_task_state
// effect on the task whose semanticKey ends in the given state.
func assertSetTaskStateDelivered(t *testing.T, svc *Service, taskUUID, state string) {
	t.Helper()
	effects, err := svc.ListEffects(taskUUID, true)
	if err != nil {
		t.Fatalf("ListEffects: %v", err)
	}
	for _, e := range effects {
		if e.Kind == "set_task_state" && strings.HasSuffix(e.SemanticKey, ":"+state) {
			if e.Status != "delivered" {
				t.Errorf("set_task_state:%s effect status = %q, want delivered", state, e.Status)
			}
			return
		}
	}
	t.Errorf("no set_task_state effect with semanticKey ending :%s found in %d effects", state, len(effects))
}

func countSetTaskStateEffects(t *testing.T, svc *Service, taskUUID, state string) int {
	t.Helper()
	effects, err := svc.ListEffects(taskUUID, true)
	if err != nil {
		t.Fatalf("ListEffects: %v", err)
	}
	n := 0
	for _, e := range effects {
		if e.Kind == "set_task_state" && strings.HasSuffix(e.SemanticKey, ":"+state) {
			n++
		}
	}
	return n
}

// daedalus #9778 required test 1: no-facts implement/verify completion succeeds
// and follows the backward-compatible `otherwise` outcome.
func TestImplVerify_NoFactsFollowsOtherwise(t *testing.T) {
	t.Run("implement", func(t *testing.T) {
		svc, taskUUID := actionFixture(t)
		driveToReady(t, svc, taskUUID)
		out := completeAction(t, svc, taskUUID, "implement", "")
		if got := outcomeOf(t, out); got != "implemented" {
			t.Errorf("outcome = %q, want implemented (otherwise)", got)
		}
		inst, _ := svc.LatestInstance(taskUUID)
		if inst.Status != "active" || inst.Phase != "implemented" {
			t.Errorf("workflow = %s/%s, want active/implemented", inst.Status, inst.Phase)
		}
	})
	t.Run("verify", func(t *testing.T) {
		svc, taskUUID := actionFixture(t)
		driveToImplemented(t, svc, taskUUID)
		out := completeAction(t, svc, taskUUID, "verify", "")
		if got := outcomeOf(t, out); got != "reviewing" {
			t.Errorf("outcome = %q, want reviewing (otherwise)", got)
		}
		inst, _ := svc.LatestInstance(taskUUID)
		if inst.Status != "active" || inst.Phase != "reviewing" {
			t.Errorf("workflow = %s/%s, want active/reviewing", inst.Status, inst.Phase)
		}
	})
}

// daedalus #9778 required test 2: valid dispositions select the fact-specific
// outcomes with their set_task_state effects.
func TestImplVerify_DispositionSelectsOutcome(t *testing.T) {
	t.Run("implement-done", func(t *testing.T) {
		svc, taskUUID := actionFixture(t)
		driveToReady(t, svc, taskUUID)
		out := completeAction(t, svc, taskUUID, "implement", `{"impl.disposition":"done"}`)
		if got := outcomeOf(t, out); got != "done" {
			t.Errorf("outcome = %q, want done", got)
		}
		inst, _ := svc.LatestInstance(taskUUID)
		if inst.Status != "active" || inst.Phase != "implemented" {
			t.Errorf("workflow = %s/%s, want active/implemented", inst.Status, inst.Phase)
		}
		// done carries no state effect; the task state is untouched (open after triage).
		if got := readTaskState(t, svc, taskUUID); got != "open" {
			t.Errorf("task state = %q, want open (done has no state effect)", got)
		}
	})
	t.Run("implement-blocked", func(t *testing.T) {
		svc, taskUUID := actionFixture(t)
		driveToReady(t, svc, taskUUID)
		out := completeAction(t, svc, taskUUID, "implement", `{"impl.disposition":"blocked"}`)
		if got := outcomeOf(t, out); got != "blocked" {
			t.Errorf("outcome = %q, want blocked", got)
		}
		inst, _ := svc.LatestInstance(taskUUID)
		if inst.Status != "active" || inst.Phase != "ready" {
			t.Errorf("workflow = %s/%s, want active/ready", inst.Status, inst.Phase)
		}
		if got := readTaskState(t, svc, taskUUID); got != "blocked" {
			t.Errorf("task state = %q, want blocked", got)
		}
		assertSetTaskStateDelivered(t, svc, taskUUID, "blocked")
	})
	t.Run("verify-verified", func(t *testing.T) {
		svc, taskUUID := actionFixture(t)
		driveToImplemented(t, svc, taskUUID)
		out := completeAction(t, svc, taskUUID, "verify", `{"verify.disposition":"verified"}`)
		if got := outcomeOf(t, out); got != "verified" {
			t.Errorf("outcome = %q, want verified", got)
		}
		inst, _ := svc.LatestInstance(taskUUID)
		if inst.Status != "closed" || inst.Phase != "done" {
			t.Errorf("workflow = %s/%s, want closed/done", inst.Status, inst.Phase)
		}
		if got := readTaskState(t, svc, taskUUID); got != "completed" {
			t.Errorf("task state = %q, want completed", got)
		}
		assertSetTaskStateDelivered(t, svc, taskUUID, "completed")
	})
	t.Run("verify-blocked", func(t *testing.T) {
		svc, taskUUID := actionFixture(t)
		driveToImplemented(t, svc, taskUUID)
		out := completeAction(t, svc, taskUUID, "verify", `{"verify.disposition":"blocked"}`)
		if got := outcomeOf(t, out); got != "blocked" {
			t.Errorf("outcome = %q, want blocked", got)
		}
		inst, _ := svc.LatestInstance(taskUUID)
		if inst.Status != "active" || inst.Phase != "ready" {
			t.Errorf("workflow = %s/%s, want active/ready", inst.Status, inst.Phase)
		}
		if got := readTaskState(t, svc, taskUUID); got != "blocked" {
			t.Errorf("task state = %q, want blocked", got)
		}
		assertSetTaskStateDelivered(t, svc, taskUUID, "blocked")
	})
}

// daedalus #9778 required test 3: an invalid disposition is rejected by evidence
// fact validation BEFORE the transition — the action errors, no evidence is
// written, and the instance does not advance.
func TestImplVerify_InvalidDispositionRejectedBeforeTransition(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	driveToReady(t, svc, taskUUID)

	run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: "implement", PrincipalRef: "agent:t"})
	if err != nil {
		t.Fatalf("StartAction implement: %v", err)
	}
	_, err = svc.CompleteAction(CompleteActionParams{
		ActionRunID: run.RunID,
		Evidence:    &ActionEvidenceInput{Summary: "impl", Facts: `{"impl.disposition":"bogus"}`},
	})
	if err == nil {
		t.Fatalf("expected error for invalid disposition, got nil")
	}
	if !strings.Contains(err.Error(), "impl.disposition") {
		t.Errorf("error %q does not mention impl.disposition", err.Error())
	}
	// No evidence was written and the instance did not advance.
	ev, err := svc.ListEvidence(taskUUID, "")
	if err != nil {
		t.Fatalf("ListEvidence: %v", err)
	}
	for _, e := range ev {
		if e.Kind == "implement_result" {
			t.Errorf("invalid-disposition evidence must not be written, found %+v", e)
		}
	}
	inst, _ := svc.LatestInstance(taskUUID)
	if inst.Status != "active" || inst.Phase != "ready" {
		t.Errorf("workflow = %s/%s, want active/ready (no transition on rejected evidence)", inst.Status, inst.Phase)
	}
}

// daedalus #9778 required test 4: facts supplied but MISSING the disposition do
// NOT select done/blocked — they route to `otherwise`. Made explicit so the
// behavior is intentional, not accidental.
func TestImplVerify_FactsWithoutDispositionRouteToOtherwise(t *testing.T) {
	t.Run("implement", func(t *testing.T) {
		svc, taskUUID := actionFixture(t)
		driveToReady(t, svc, taskUUID)
		out := completeAction(t, svc, taskUUID, "implement", `{"git.clean":true,"green.exit":0}`)
		if got := outcomeOf(t, out); got != "implemented" {
			t.Errorf("outcome = %q, want implemented (otherwise)", got)
		}
		inst, _ := svc.LatestInstance(taskUUID)
		if inst.Phase != "implemented" {
			t.Errorf("workflow phase = %q, want implemented", inst.Phase)
		}
		if got := readTaskState(t, svc, taskUUID); got != "open" {
			t.Errorf("task state = %q, want open (no blocked effect)", got)
		}
	})
	t.Run("verify", func(t *testing.T) {
		svc, taskUUID := actionFixture(t)
		driveToImplemented(t, svc, taskUUID)
		out := completeAction(t, svc, taskUUID, "verify", `{"install.exit":0,"smoke.exit":0}`)
		if got := outcomeOf(t, out); got != "reviewing" {
			t.Errorf("outcome = %q, want reviewing (otherwise)", got)
		}
		inst, _ := svc.LatestInstance(taskUUID)
		if inst.Phase != "reviewing" {
			t.Errorf("workflow phase = %q, want reviewing", inst.Phase)
		}
	})
}

// daedalus #9778 required test 6: replaying the same action.complete is
// idempotent — same evidence id, same transition outcome, and the engine-owned
// set_task_state effect created exactly once (no duplicate). Uses the implement
// `blocked` self-transition (active/ready → active/ready), which keeps the same
// transition available on replay and so exercises the idempotency-key replay
// path while carrying a set_task_state effect.
func TestImplVerify_ReplayIdempotent(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	driveToReady(t, svc, taskUUID)

	run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: "implement", PrincipalRef: "agent:t", IdempotencyKey: "ik"})
	if err != nil {
		t.Fatalf("StartAction implement: %v", err)
	}
	facts := `{"impl.disposition":"blocked"}`
	first, err := svc.CompleteAction(CompleteActionParams{ActionRunID: run.RunID, Evidence: &ActionEvidenceInput{Summary: "i", Facts: facts}})
	if err != nil {
		t.Fatalf("CompleteAction first: %v", err)
	}
	second, err := svc.CompleteAction(CompleteActionParams{ActionRunID: run.RunID, Evidence: &ActionEvidenceInput{Summary: "i", Facts: facts}})
	if err != nil {
		t.Fatalf("CompleteAction replay: %v", err)
	}

	if first.Evidence == nil || second.Evidence == nil || first.Evidence.ID != second.Evidence.ID {
		t.Errorf("replay produced different/absent evidence: %+v vs %+v", first.Evidence, second.Evidence)
	}
	if outcomeOf(t, first) != "blocked" || outcomeOf(t, second) != "blocked" {
		t.Errorf("replay outcome mismatch: %q vs %q (want blocked both)", outcomeOf(t, first), outcomeOf(t, second))
	}
	// Exactly one implement_result evidence and one set_task_state:blocked effect.
	ev, _ := svc.ListEvidence(taskUUID, "")
	implCount := 0
	for _, e := range ev {
		if e.Kind == "implement_result" {
			implCount++
		}
	}
	if implCount != 1 {
		t.Errorf("implement_result evidence count = %d, want 1", implCount)
	}
	if n := countSetTaskStateEffects(t, svc, taskUUID, "blocked"); n != 1 {
		t.Errorf("set_task_state:blocked effect count = %d, want 1 (no duplicate)", n)
	}
	if got := readTaskState(t, svc, taskUUID); got != "blocked" {
		t.Errorf("task state after replay = %q, want blocked", got)
	}
}

// Invariant guard (daedalus #9778/#9770): outcome selection follows the LATEST
// same-kind evidence, not stale same-kind evidence. After an implement `blocked`
// completion (which leaves a blocked implement_result on record and rewinds to
// active/ready), a fresh implement completion with `done` facts writes a newer
// implement_result and must select `done` — proving the current run's evidence
// controls over the stale blocked evidence.
func TestImplVerify_LatestEvidenceControlsOverStale(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	driveToReady(t, svc, taskUUID)

	// First implement: blocked (stale evidence after this).
	out := completeAction(t, svc, taskUUID, "implement", `{"impl.disposition":"blocked"}`)
	if got := outcomeOf(t, out); got != "blocked" {
		t.Fatalf("precondition outcome = %q, want blocked", got)
	}
	inst, _ := svc.LatestInstance(taskUUID)
	if inst.Phase != "ready" {
		t.Fatalf("precondition phase = %q, want ready (rewound)", inst.Phase)
	}

	// Second implement on the same instance: done. Newer implement_result must win.
	out2 := completeAction(t, svc, taskUUID, "implement", `{"impl.disposition":"done"}`)
	if got := outcomeOf(t, out2); got != "done" {
		t.Errorf("outcome = %q, want done (latest evidence must control over stale blocked)", got)
	}
	inst2, _ := svc.LatestInstance(taskUUID)
	if inst2.Status != "active" || inst2.Phase != "implemented" {
		t.Errorf("workflow = %s/%s, want active/implemented", inst2.Status, inst2.Phase)
	}
}

// Template-contract test (daedalus #9768 #1 / acceptance criteria): the built-in
// declares the fact-branched outcomes with effects, `otherwise` last, and the
// disposition enum facts schemas on implement_result / verify_result without
// marking them required.
func TestBuiltinTemplate_FactBranchedContract(t *testing.T) {
	svc, _ := actionFixture(t)
	if _, _, err := svc.EnsureBuiltinTemplate(BuiltinSimpleTaskTemplateRef, "agent:t"); err != nil {
		t.Fatalf("EnsureBuiltinTemplate: %v", err)
	}
	tpl, _, err := svc.ShowTemplate(BuiltinSimpleTaskTemplateRef)
	if err != nil {
		t.Fatalf("ShowTemplate: %v", err)
	}

	byID := map[string]TransitionSpec{}
	for _, tr := range tpl.Transitions {
		byID[tr.ID] = tr
	}

	assertOutcomeOrder := func(trID string, want []string) {
		tr, ok := byID[trID]
		if !ok {
			t.Fatalf("transition %s not found", trID)
		}
		if len(tr.Outcomes) != len(want) {
			t.Fatalf("%s outcomes = %d, want %d", trID, len(tr.Outcomes), len(want))
		}
		for i, id := range want {
			if tr.Outcomes[i].ID != id {
				t.Errorf("%s outcome[%d] = %q, want %q", trID, i, tr.Outcomes[i].ID, id)
			}
		}
		last := tr.Outcomes[len(tr.Outcomes)-1]
		if last.When.Otherwise == nil || !*last.When.Otherwise {
			t.Errorf("%s last outcome %q is not `otherwise`", trID, last.ID)
		}
	}
	assertOutcomeOrder("implement_complete", []string{"done_verify_launch", "done", "blocked", "implemented"})
	assertOutcomeOrder("verify_complete", []string{"verified", "blocked", "reviewing"})

	// Effects: implement blocked + verify verified/blocked carry set_task_state.
	findEffectState := func(trID, outcomeID string) (string, bool) {
		for _, o := range byID[trID].Outcomes {
			if o.ID != outcomeID {
				continue
			}
			for _, e := range o.Effects {
				if e.Kind == "set_task_state" {
					s, _ := e.Data["state"].(string)
					return s, true
				}
			}
		}
		return "", false
	}
	for _, c := range []struct {
		tr, outcome, state string
	}{
		{"implement_complete", "blocked", "blocked"},
		{"verify_complete", "verified", "completed"},
		{"verify_complete", "blocked", "blocked"},
	} {
		got, ok := findEffectState(c.tr, c.outcome)
		if !ok {
			t.Errorf("%s/%s missing set_task_state effect", c.tr, c.outcome)
		} else if got != c.state {
			t.Errorf("%s/%s set_task_state state = %q, want %q", c.tr, c.outcome, got, c.state)
		}
	}
	// done must carry NO effect.
	if _, ok := findEffectState("implement_complete", "done"); ok {
		t.Errorf("implement_complete/done must not carry a set_task_state effect")
	}

	// Facts schemas: disposition enums declared, NOT required (verdict A).
	assertEnumNotRequired := func(kind, prop string, wantEnum []string) {
		spec, ok := tpl.EvidenceKinds[kind]
		if !ok || spec.Facts == nil {
			t.Fatalf("evidence kind %s has no facts schema", kind)
		}
		fp, ok := spec.Facts.Properties[prop]
		if !ok {
			t.Fatalf("%s facts missing property %s", kind, prop)
		}
		got := []string{}
		for _, raw := range fp.Enum {
			var s string
			_ = json.Unmarshal(raw, &s)
			got = append(got, s)
		}
		if strings.Join(got, ",") != strings.Join(wantEnum, ",") {
			t.Errorf("%s.%s enum = %v, want %v", kind, prop, got, wantEnum)
		}
		for _, r := range spec.Facts.Required {
			if r == prop {
				t.Errorf("%s.%s must NOT be required (verdict A: backward-compat no-facts path)", kind, prop)
			}
		}
	}
	assertEnumNotRequired("implement_result", "impl.disposition", []string{"done", "blocked"})
	assertEnumNotRequired("verify_result", "verify.disposition", []string{"verified", "blocked"})
}
