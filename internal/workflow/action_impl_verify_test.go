//go:build wrkq_local

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
	"fmt"
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

// daedalus #9778 required tests 1, 2 and 4: the completion facts select the
// outcome. A valid disposition selects its fact-specific outcome (with its
// set_task_state effect); no facts, or facts without a disposition, follow the
// backward-compatible `otherwise` outcome.
func TestCompletionDispositionSelectsOutcomeAndTaskState(t *testing.T) {
	cases := []struct {
		name   string
		action string
		facts  string
		// wantOutcome is the transition outcome; wantStatus/wantPhase the instance after it.
		wantOutcome, wantStatus, wantPhase string
		// wantTaskState is checked when non-empty.
		wantTaskState string
		// wantDelivered names the set_task_state effect that must be delivered, if any.
		wantDelivered string
	}{
		// done carries no state effect; the task state is untouched (open after triage).
		{name: "implement-done", action: "implement", facts: `{"impl.disposition":"done"}`,
			wantOutcome: "done", wantStatus: "active", wantPhase: "implemented", wantTaskState: "open"},
		{name: "implement-blocked", action: "implement", facts: `{"impl.disposition":"blocked"}`,
			wantOutcome: "blocked", wantStatus: "active", wantPhase: "ready", wantTaskState: "blocked", wantDelivered: "blocked"},
		{name: "verify-verified", action: "verify", facts: `{"verify.disposition":"verified"}`,
			wantOutcome: "verified", wantStatus: "closed", wantPhase: "done", wantTaskState: "completed", wantDelivered: "completed"},
		{name: "verify-blocked", action: "verify", facts: `{"verify.disposition":"blocked"}`,
			wantOutcome: "blocked", wantStatus: "active", wantPhase: "ready", wantTaskState: "blocked", wantDelivered: "blocked"},
		{name: "implement-no-facts-otherwise", action: "implement", facts: "",
			wantOutcome: "implemented", wantStatus: "active", wantPhase: "implemented"},
		{name: "verify-no-facts-otherwise", action: "verify", facts: "",
			wantOutcome: "reviewing", wantStatus: "active", wantPhase: "reviewing"},
		// Facts present but the disposition key missing must not select done/blocked.
		{name: "implement-facts-without-disposition-otherwise", action: "implement", facts: `{"git.clean":true,"green.exit":0}`,
			wantOutcome: "implemented", wantStatus: "active", wantPhase: "implemented", wantTaskState: "open"},
		{name: "verify-facts-without-disposition-otherwise", action: "verify", facts: `{"install.exit":0,"smoke.exit":0}`,
			wantOutcome: "reviewing", wantStatus: "active", wantPhase: "reviewing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, taskUUID := actionFixture(t)
			if tc.action == "verify" {
				driveToImplemented(t, svc, taskUUID)
			} else {
				driveToReady(t, svc, taskUUID)
			}
			out := completeAction(t, svc, taskUUID, tc.action, tc.facts)
			if got := outcomeOf(t, out); got != tc.wantOutcome {
				t.Errorf("outcome = %q, want %q", got, tc.wantOutcome)
			}
			inst, _ := svc.LatestInstance(taskUUID)
			if inst.Status != tc.wantStatus || inst.Phase != tc.wantPhase {
				t.Errorf("workflow = %s/%s, want %s/%s", inst.Status, inst.Phase, tc.wantStatus, tc.wantPhase)
			}
			if tc.wantTaskState != "" {
				if got := readTaskState(t, svc, taskUUID); got != tc.wantTaskState {
					t.Errorf("task state = %q, want %q", got, tc.wantTaskState)
				}
			}
			if tc.wantDelivered != "" {
				assertSetTaskStateDelivered(t, svc, taskUUID, tc.wantDelivered)
			}
		})
	}
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
	detail, ok := AsErrorDetail(err)
	if !ok || detail.Code != wrkfCodeValidation || detail.Field != "facts.impl.disposition" {
		t.Fatalf("invalid disposition error = %v (detail %+v), want %s on field facts.impl.disposition", err, detail, wrkfCodeValidation)
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

// daedalus #9778 required test 6 (plus the triage blocked and verify-launch
// replay requirements): replaying the same action.complete is idempotent: same
// evidence id, same outcome, and the outcome's effect created exactly once.
// Every row uses a transition that stays available on replay (a blocked
// self-transition, or the replay key of a completed transition), so the replay
// goes through the idempotency-key path while carrying an effect.
func TestCompletionReplayReturnsSameEvidenceAndDoesNotDuplicateEffects(t *testing.T) {
	const verifyLaunchFacts = `{"impl.disposition":"done","automation.verifyLaunch":"acp-command-run/v1"}`
	cases := []struct {
		name    string
		prepare func(t *testing.T, svc *Service, taskUUID string)
		action  string
		facts   string
		// wantOutcome is checked on both completions when non-empty.
		wantOutcome string
		check       func(t *testing.T, svc *Service, taskUUID, runID string)
	}{
		{
			name: "triage-blocked-self-transition",
			prepare: func(t *testing.T, svc *Service, taskUUID string) {
				setTaskSpecAndState(t, svc, taskUUID, "", "open")
			},
			action: "triage",
			check: func(t *testing.T, svc *Service, taskUUID, _ string) {
				if got := readTaskState(t, svc, taskUUID); got != "blocked" {
					t.Errorf("task state after replay = %q, want blocked", got)
				}
			},
		},
		{
			name:        "implement-blocked-set-task-state",
			prepare:     driveToReady,
			action:      "implement",
			facts:       `{"impl.disposition":"blocked"}`,
			wantOutcome: "blocked",
			check: func(t *testing.T, svc *Service, taskUUID, _ string) {
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
			},
		},
		{
			name:    "implement-verify-launch-opt-in",
			prepare: driveToReady,
			action:  "implement",
			facts:   verifyLaunchFacts,
			check: func(t *testing.T, svc *Service, taskUUID, runID string) {
				effects := verifyLaunchEffects(t, svc, taskUUID)
				if len(effects) != 1 {
					t.Fatalf("verify-launch effects after replay = %d, want exactly 1: %+v", len(effects), effects)
				}
				wantKey := fmt.Sprintf("verify-launch:%s:%d:%s", taskUUID, effects[0].Revision, runID)
				if effects[0].SemanticKey != wantKey || effects[0].IdempotencyKey == "" {
					t.Errorf("effect idempotency binding = semanticKey %q idempotencyKey %q, want semanticKey %q and non-empty idempotencyKey", effects[0].SemanticKey, effects[0].IdempotencyKey, wantKey)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, taskUUID := actionFixture(t)
			tc.prepare(t, svc, taskUUID)

			run, err := svc.StartAction(StartActionParams{
				Task: taskUUID, Workflow: BuiltinSimpleTaskTemplateRef, Action: tc.action, PrincipalRef: "agent:t", IdempotencyKey: "replay-" + tc.name,
			})
			if err != nil {
				t.Fatalf("StartAction %s: %v", tc.action, err)
			}
			complete := func(label string) *ActionCompleteResult {
				out, err := svc.CompleteAction(CompleteActionParams{ActionRunID: run.RunID, Evidence: &ActionEvidenceInput{Summary: tc.action, Facts: tc.facts}})
				if err != nil {
					t.Fatalf("CompleteAction %s: %v", label, err)
				}
				return out
			}
			first := complete("first")
			second := complete("replay")

			if first.Evidence == nil || second.Evidence == nil || first.Evidence.ID != second.Evidence.ID {
				t.Errorf("replay produced different/absent evidence: %+v vs %+v", first.Evidence, second.Evidence)
			}
			if second.Run.Status != "completed" {
				t.Errorf("replay run status = %q, want completed", second.Run.Status)
			}
			if tc.wantOutcome != "" {
				if got1, got2 := outcomeOf(t, first), outcomeOf(t, second); got1 != tc.wantOutcome || got2 != tc.wantOutcome {
					t.Errorf("replay outcome mismatch: %q vs %q (want %s both)", got1, got2, tc.wantOutcome)
				}
			}
			tc.check(t, svc, taskUUID, run.RunID)
		})
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
