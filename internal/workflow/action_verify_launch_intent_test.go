package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// T-05310 RED tests for the producer side of ACP verify-launch. These pin the
// observable wrkf contract: only fact-opted implement completions create a
// durable pending launch intent, and that effect is bound to the source action
// run through rendered transition context.

func completeImplementWithRun(t *testing.T, svc *Service, taskUUID, facts, idempotencyKey string) (*ActionRun, *ActionCompleteResult, error) {
	t.Helper()
	params := StartActionParams{Task: taskUUID, Action: "implement", PrincipalRef: "agent:t"}
	if idempotencyKey != "" {
		params.IdempotencyKey = idempotencyKey
	}
	run, err := svc.StartAction(params)
	if err != nil {
		t.Fatalf("StartAction implement: %v", err)
	}
	out, err := svc.CompleteAction(CompleteActionParams{
		ActionRunID: run.RunID,
		Evidence:    &ActionEvidenceInput{Summary: "impl", Facts: facts},
	})
	return run, out, err
}

func verifyLaunchEffects(t *testing.T, svc *Service, taskUUID string) []Effect {
	t.Helper()
	effects, err := svc.ListEffects(taskUUID, true)
	if err != nil {
		t.Fatalf("ListEffects: %v", err)
	}
	out := make([]Effect, 0, len(effects))
	for _, effect := range effects {
		if strings.HasPrefix(effect.SemanticKey, "verify-launch:") {
			out = append(out, effect)
		}
	}
	return out
}

func assertNoVerifyLaunchEffect(t *testing.T, svc *Service, taskUUID string) {
	t.Helper()
	if effects := verifyLaunchEffects(t, svc, taskUUID); len(effects) != 0 {
		t.Fatalf("verify-launch effects = %d, want 0: %+v", len(effects), effects)
	}
}

func TestImplementCompleteDoneWithoutAutomationFactDoesNotLaunchVerify(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	driveToReady(t, svc, taskUUID)

	out := completeAction(t, svc, taskUUID, "implement", `{"impl.disposition":"done"}`)
	if got := outcomeOf(t, out); got != "done" {
		t.Errorf("outcome = %q, want generic done", got)
	}
	inst, _ := svc.LatestInstance(taskUUID)
	if inst.Status != "active" || inst.Phase != "implemented" {
		t.Errorf("workflow = %s/%s, want active/implemented", inst.Status, inst.Phase)
	}
	assertNoVerifyLaunchEffect(t, svc, taskUUID)
}

func TestImplementCompleteDoneWithAutomationFactCreatesPendingVerifyLaunch(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	driveToReady(t, svc, taskUUID)

	run, out, err := completeImplementWithRun(t, svc, taskUUID, `{"impl.disposition":"done","automation.verifyLaunch":"acp-command-run/v1"}`, "impl-opt-in")
	if err != nil {
		t.Fatalf("CompleteAction implement: %v", err)
	}
	if got := outcomeOf(t, out); got == "done" || got == "implemented" || got == "" {
		t.Errorf("outcome = %q, want more-specific verify-launch outcome before generic done", got)
	}
	inst, _ := svc.LatestInstance(taskUUID)
	if inst.Status != "active" || inst.Phase != "implemented" {
		t.Errorf("workflow = %s/%s, want active/implemented", inst.Status, inst.Phase)
	}

	effects := verifyLaunchEffects(t, svc, taskUUID)
	if len(effects) != 1 {
		t.Fatalf("verify-launch effect count = %d, want exactly 1", len(effects))
	}
	effect := effects[0]
	if effect.Status != "pending" {
		t.Errorf("verify-launch effect status = %q, want pending", effect.Status)
	}
	wantKey := fmt.Sprintf("verify-launch:%s:%d:%s", taskUUID, effect.Revision, run.RunID)
	if effect.SemanticKey != wantKey {
		t.Errorf("semanticKey = %q, want %q", effect.SemanticKey, wantKey)
	}
	var payload map[string]any
	if err := json.Unmarshal(effect.Payload, &payload); err != nil {
		t.Fatalf("decode effect payload: %v", err)
	}
	data, _ := payload["data"].(map[string]any)
	if data["sourceImplementActionRunId"] != run.RunID {
		t.Errorf("payload sourceImplementActionRunId = %#v, want %q", data["sourceImplementActionRunId"], run.RunID)
	}
	if data["action"] != "verify" {
		t.Errorf("payload action = %#v, want verify", data["action"])
	}
	if data["role"] != "tester" {
		t.Errorf("payload role = %#v, want tester", data["role"])
	}
}

func TestImplementCompleteDoesNotLaunchVerifyForNonOptInEvidence(t *testing.T) {
	cases := []struct {
		name      string
		facts     string
		wantPhase string
		allowErr  bool
	}{
		{name: "blocked", facts: `{"impl.disposition":"blocked","automation.verifyLaunch":"acp-command-run/v1"}`, wantPhase: "ready"},
		{name: "fail", facts: `{"impl.disposition":"fail","automation.verifyLaunch":"acp-command-run/v1"}`, wantPhase: "ready", allowErr: true},
		{name: "missing-disposition", facts: `{"automation.verifyLaunch":"acp-command-run/v1"}`, wantPhase: "implemented"},
		{name: "wrong-version", facts: `{"impl.disposition":"done","automation.verifyLaunch":"acp-command-run/v2"}`, wantPhase: "implemented"},
		{name: "wrong-fact-name", facts: `{"impl.disposition":"done","automation.verify_launch":"acp-command-run/v1"}`, wantPhase: "implemented"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, taskUUID := actionFixture(t)
			driveToReady(t, svc, taskUUID)
			_, _, err := completeImplementWithRun(t, svc, taskUUID, tc.facts, "impl-"+tc.name)
			if err != nil && !tc.allowErr {
				t.Fatalf("CompleteAction implement: %v", err)
			}
			inst, _ := svc.LatestInstance(taskUUID)
			if inst.Status != "active" || inst.Phase != tc.wantPhase {
				t.Errorf("workflow = %s/%s, want active/%s", inst.Status, inst.Phase, tc.wantPhase)
			}
			assertNoVerifyLaunchEffect(t, svc, taskUUID)
		})
	}
}

const verifyLaunchRenderTemplate = `{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "verify_launch_render_test",
  "version": "1",
  "kind": "agent_first_workflow",
  "initial": { "status": "active", "phase": "implementing" },
  "roles": {
    "implementer": { "description": "Implementation agent" }
  },
  "states": [
    { "status": "active", "phase": "implementing" },
    { "status": "active", "phase": "implemented" }
  ],
  "transitions": [
    {
      "id": "implement_complete",
      "from": { "status": "active", "phase": "implementing" },
      "by": ["implementer"],
      "outcomes": [
        {
          "id": "verify_launch",
          "when": { "always": true },
          "to": { "status": "active", "phase": "implemented" },
          "effects": [
            {
              "kind": "verify_launch",
              "role": "tester",
              "semanticKey": "verify-launch:{taskUuid}:{revision}:{runId}",
              "data": {
                "taskUuid": "{taskUuid}",
                "revision": "{revision}",
                "sourceImplementActionRunId": "{runId}",
                "action": "verify",
                "role": "tester"
              }
            }
          ]
        }
      ]
    }
  ]
}`

func TestTransitionEffectMaterializesRevisionAndRunID(t *testing.T) {
	svc, taskUUID, _ := setupGapClosureFixtureWithTemplate(t, "verify_launch_render_test", verifyLaunchRenderTemplate)

	const runID = "run_verify_launch_source"
	rev0 := int64(0)
	result, err := svc.Transition(taskUUID, "implement_complete", TransitionOptions{
		PrincipalRef:   "agent:t",
		Role:           "implementer",
		ExpectRevision: &rev0,
		IdempotencyKey: "verify-launch-render",
		RunID:          runID,
	})
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}
	rawEffects, _ := json.Marshal(result["effects"])
	var effects []Effect
	if err := json.Unmarshal(rawEffects, &effects); err != nil {
		t.Fatalf("decode effects: %v", err)
	}
	if len(effects) != 1 {
		t.Fatalf("effects = %d, want 1", len(effects))
	}
	effect := effects[0]
	wantKey := fmt.Sprintf("verify-launch:%s:1:%s", taskUUID, runID)
	if effect.SemanticKey != wantKey {
		t.Errorf("semanticKey = %q, want %q", effect.SemanticKey, wantKey)
	}
	var payload map[string]any
	if err := json.Unmarshal(effect.Payload, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	data, _ := payload["data"].(map[string]any)
	if data["sourceImplementActionRunId"] != runID {
		t.Errorf("payload sourceImplementActionRunId = %#v, want %q", data["sourceImplementActionRunId"], runID)
	}
	if data["taskUuid"] != taskUUID {
		t.Errorf("payload taskUuid = %#v, want %q", data["taskUuid"], taskUUID)
	}
	if data["revision"] != "1" {
		t.Errorf("payload revision = %#v, want rendered revision \"1\"", data["revision"])
	}
}

const verifyLaunchUnresolvedTokenTemplate = `{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "verify_launch_unresolved_token_test",
  "version": "1",
  "kind": "agent_first_workflow",
  "initial": { "status": "active", "phase": "implementing" },
  "roles": {
    "implementer": { "description": "Implementation agent" }
  },
  "states": [
    { "status": "active", "phase": "implementing" },
    { "status": "active", "phase": "implemented" }
  ],
  "transitions": [
    {
      "id": "implement_complete",
      "from": { "status": "active", "phase": "implementing" },
      "by": ["implementer"],
      "outcomes": [
        {
          "id": "verify_launch",
          "when": { "always": true },
          "to": { "status": "active", "phase": "implemented" },
          "effects": [
            {
              "kind": "verify_launch",
              "role": "tester",
              "semanticKey": "verify-launch:{taskUuid}:{revision}:{doesNotExist}",
              "data": { "sourceImplementActionRunId": "{runId}" }
            }
          ]
        }
      ]
    }
  ]
}`

func TestTransitionEffectUnresolvedTokenFailsAtomically(t *testing.T) {
	svc, taskUUID, _ := setupGapClosureFixtureWithTemplate(t, "verify_launch_unresolved_token_test", verifyLaunchUnresolvedTokenTemplate)

	rev0 := int64(0)
	_, err := svc.Transition(taskUUID, "implement_complete", TransitionOptions{
		PrincipalRef:   "agent:t",
		Role:           "implementer",
		ExpectRevision: &rev0,
		IdempotencyKey: "verify-launch-unresolved",
		RunID:          "run_bad_token",
	})
	if err == nil {
		t.Fatal("Transition succeeded with unresolved effect token; want atomic failure")
	}
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if inst.Revision != 0 || inst.Status != "active" || inst.Phase != "implementing" {
		t.Errorf("instance after failed transition = rev %d %s/%s, want rev 0 active/implementing", inst.Revision, inst.Status, inst.Phase)
	}
	effects, err := svc.ListEffects(taskUUID, true)
	if err != nil {
		t.Fatalf("ListEffects: %v", err)
	}
	if len(effects) != 0 {
		t.Fatalf("effects committed after failed token render = %d, want 0: %+v", len(effects), effects)
	}
}

func TestImplementCompleteVerifyLaunchReplayIsIdempotent(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	driveToReady(t, svc, taskUUID)

	run, first, err := completeImplementWithRun(t, svc, taskUUID, `{"impl.disposition":"done","automation.verifyLaunch":"acp-command-run/v1"}`, "impl-replay")
	if err != nil {
		t.Fatalf("CompleteAction first: %v", err)
	}
	second, err := svc.CompleteAction(CompleteActionParams{
		ActionRunID: run.RunID,
		Evidence:    &ActionEvidenceInput{Summary: "impl", Facts: `{"impl.disposition":"done","automation.verifyLaunch":"acp-command-run/v1"}`},
	})
	if err != nil {
		t.Fatalf("CompleteAction replay: %v", err)
	}
	if first.Evidence == nil || second.Evidence == nil || first.Evidence.ID != second.Evidence.ID {
		t.Fatalf("replay evidence mismatch: first=%+v second=%+v", first.Evidence, second.Evidence)
	}
	effects := verifyLaunchEffects(t, svc, taskUUID)
	if len(effects) != 1 {
		t.Fatalf("verify-launch effects after replay = %d, want exactly 1: %+v", len(effects), effects)
	}
	wantKey := fmt.Sprintf("verify-launch:%s:%d:%s", taskUUID, effects[0].Revision, run.RunID)
	if effects[0].SemanticKey != wantKey || effects[0].IdempotencyKey == "" {
		t.Errorf("effect idempotency binding = semanticKey %q idempotencyKey %q, want semanticKey %q and non-empty idempotencyKey", effects[0].SemanticKey, effects[0].IdempotencyKey, wantKey)
	}
}
