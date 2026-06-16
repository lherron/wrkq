package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

const gapClosureTemplate = `{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "gap_closure_test",
  "version": "1",
  "kind": "agent_first_workflow",
  "initial": { "status": "active", "phase": "review" },
  "roles": {
    "reviewer": { "description": "Review authority" }
  },
  "states": [
    { "status": "active", "phase": "review" },
    { "status": "closed", "outcome": "rejected" },
    { "status": "closed", "outcome": "approved" }
  ],
  "evidenceKinds": {
    "verdict": {
      "description": "Reviewer verdict",
      "facts": {
        "required": ["route"],
        "properties": {
          "route": { "type": "string", "enum": ["reject", "approve"] }
        }
      }
    }
  },
  "transitions": [
    {
      "id": "decide",
      "from": { "status": "active", "phase": "review" },
      "by": ["reviewer"],
      "requires": [ { "evidence": { "kind": "verdict" } } ],
      "postconditions": [ { "factEquals": { "path": "workflow.status", "value": "closed" } } ],
      "outcomes": [
        {
          "id": "rejected",
          "when": { "evidenceExists": { "kind": "verdict", "facts": { "route": "reject" } } },
          "to": { "status": "closed", "outcome": "rejected" }
        },
        {
          "id": "approved",
          "when": { "evidenceExists": { "kind": "verdict", "facts": { "route": "approve" } } },
          "to": { "status": "closed", "outcome": "approved" },
          "effects": [
            {
              "kind": "set_task_state",
              "role": "system",
              "semanticKey": "task-state:{taskUuid}:{revision}:completed",
              "data": { "state": "completed" }
            }
          ]
        }
      ]
    }
  ]
}`

const gapExternalEffectTemplate = `{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "gap_external_effect_test",
  "version": "1",
  "kind": "agent_first_workflow",
  "initial": { "status": "active", "phase": "review" },
  "roles": {
    "reviewer": { "description": "Review authority" }
  },
  "states": [
    { "status": "active", "phase": "review" },
    { "status": "closed", "outcome": "approved" }
  ],
  "transitions": [
    {
      "id": "decide",
      "from": { "status": "active", "phase": "review" },
      "by": ["reviewer"],
      "outcomes": [
        {
          "id": "approved",
          "when": { "always": true },
          "to": { "status": "closed", "outcome": "approved" },
          "effects": [
            {
              "kind": "request_observer_review",
              "role": "observer",
              "semanticKey": "observer-review:{taskUuid}:{revision}",
              "data": { "reason": "external review required" }
            }
          ]
        }
      ]
    }
  ]
}`

const gapInvalidBuiltinEffectTemplate = `{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "gap_invalid_builtin_effect_test",
  "version": "1",
  "kind": "agent_first_workflow",
  "initial": { "status": "active", "phase": "review" },
  "roles": {
    "reviewer": { "description": "Review authority" }
  },
  "states": [
    { "status": "active", "phase": "review" },
    { "status": "closed", "outcome": "approved" }
  ],
  "transitions": [
    {
      "id": "decide",
      "from": { "status": "active", "phase": "review" },
      "by": ["reviewer"],
      "outcomes": [
        {
          "id": "approved",
          "when": { "always": true },
          "to": { "status": "closed", "outcome": "approved" },
          "effects": [
            {
              "kind": "set_task_state",
              "role": "system",
              "semanticKey": "task-state:{taskUuid}:{revision}:invalid",
              "data": { "state": "not-a-task-state" }
            }
          ]
        }
      ]
    }
  ]
}`

func setupGapClosureFixture(t *testing.T) (*Service, string, *db.DB) {
	return setupGapClosureFixtureWithTemplate(t, "gap_closure_test", gapClosureTemplate)
}

func setupGapClosureFixtureWithTemplate(t *testing.T, templateID, templateBody string) (*Service, string, *db.DB) {
	t.Helper()
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "gap_closure.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	svc := NewService(database)
	tplPath := filepath.Join(tmpDir, "gap_closure_template.json")
	if err := os.WriteFile(tplPath, []byte(templateBody), 0644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	if _, err := svc.InstallTemplate(tplPath, "gap-installer", nil); err != nil {
		t.Fatalf("InstallTemplate: %v", err)
	}

	actorUUID := "dddddddd-dddd-4ddd-8ddd-000000000001"
	if _, err := database.Exec(`INSERT INTO actors (uuid, slug, role) VALUES (?, 'gap-actor', 'system')`, actorUUID); err != nil {
		t.Fatalf("insert actor: %v", err)
	}
	containerUUID := "eeeeeeee-eeee-4eee-8eee-000000000001"
	if _, err := database.Exec(
		`INSERT INTO containers (uuid, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'gap-project', 'Gap Project', (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		containerUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert container: %v", err)
	}
	taskUUID := "ffffffff-ffff-4fff-8fff-000000000001"
	if _, err := database.Exec(
		`INSERT INTO tasks (uuid, slug, title, description, project_uuid, state, priority, kind,
		                    created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'gap-task', 'Gap Test Task', 'initial body', ?, 'open', 2, 'task', ?, ?)`,
		taskUUID, containerUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, templateID+"@1", "gap-installer"); err != nil {
		t.Fatalf("AttachTask: %v", err)
	}
	return svc, taskUUID, database
}

func TestGapClosureNextStaleEvidenceAndEffectReceipt(t *testing.T) {
	svc, taskUUID, database := setupGapClosureFixture(t)

	if _, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: taskUUID,
		Kind:         "verdict",
		Ref:          "urn:test:verdict:old",
		Facts:        `{"route":"approve"}`,
		Actor:        "reviewer-a",
		Role:         "reviewer",
	}); err != nil {
		t.Fatalf("AddEvidence old verdict: %v", err)
	}

	next, err := svc.Next(taskUUID, "reviewer")
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	var expectedOutcome string
	for _, action := range next.Actions {
		if action.ID == "transition_decide" && action.ExpectedState != nil {
			expectedOutcome = action.ExpectedState.Outcome
		}
	}
	if expectedOutcome != "approved" {
		t.Fatalf("Next chose outcome %q, want approved (the second outcome)", expectedOutcome)
	}

	if _, err := database.Exec(`UPDATE tasks SET description = 'changed body', etag = etag + 1 WHERE uuid = ?`, taskUUID); err != nil {
		t.Fatalf("mutate task: %v", err)
	}
	expectRev := int64(0)
	_, err = svc.Transition(taskUUID, "decide", TransitionOptions{Actor: "reviewer-a", Role: "reviewer", ExpectRevision: &expectRev, IdempotencyKey: "stale-attempt"})
	if got := wrkfCode(err); got != "WRKF_TRANSITION_BLOCKED" {
		t.Fatalf("stale evidence transition code = %q, want WRKF_TRANSITION_BLOCKED (err=%v)", got, err)
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Fatalf("blocked transition did not report stale evidence: %v", err)
	}

	fresh, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: taskUUID,
		Kind:         "verdict",
		Ref:          "urn:test:verdict:fresh",
		Facts:        `{"route":"approve"}`,
		Actor:        "reviewer-a",
		Role:         "reviewer",
	})
	if err != nil {
		t.Fatalf("AddEvidence fresh verdict: %v", err)
	}
	if fresh.TaskHashAtProduction == "" {
		t.Fatal("fresh evidence did not record task hash provenance")
	}

	result, err := svc.Transition(taskUUID, "decide", TransitionOptions{Actor: "reviewer-a", Role: "reviewer", ExpectRevision: &expectRev, IdempotencyKey: "fresh-commit"})
	if err != nil {
		t.Fatalf("Transition fresh: %v", err)
	}
	var effects []Effect
	rawEffects, _ := json.Marshal(result["effects"])
	if err := json.Unmarshal(rawEffects, &effects); err != nil {
		t.Fatalf("decode effects: %v", err)
	}
	if len(effects) != 1 {
		t.Fatalf("created effects = %d, want 1", len(effects))
	}
	if effects[0].Sequence != 1 || effects[0].SemanticKey == "" {
		t.Fatalf("effect sequencing/idempotency not populated: %+v", effects[0])
	}
	if effects[0].Status != "delivered" || len(effects[0].Receipt) == 0 {
		t.Fatalf("transition did not auto-deliver native effect with receipt: %+v", effects[0])
	}
	stored, err := svc.ShowEffect(effects[0].ID)
	if err != nil {
		t.Fatalf("ShowEffect: %v", err)
	}
	if stored.Status != "delivered" || len(stored.Receipt) == 0 {
		t.Fatalf("stored effect was not delivered with receipt: %+v", stored)
	}
	var state string
	if err := database.QueryRow(`SELECT state FROM tasks WHERE uuid = ?`, taskUUID).Scan(&state); err != nil {
		t.Fatalf("read task state: %v", err)
	}
	if state != "completed" {
		t.Fatalf("task state = %q, want completed", state)
	}
}

func TestTransitionExternalEffectRemainsPending(t *testing.T) {
	svc, taskUUID, _ := setupGapClosureFixtureWithTemplate(t, "gap_external_effect_test", gapExternalEffectTemplate)

	rev0 := int64(0)
	result, err := svc.Transition(taskUUID, "decide", TransitionOptions{Actor: "reviewer-a", Role: "reviewer", ExpectRevision: &rev0})
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}
	var effects []Effect
	rawEffects, _ := json.Marshal(result["effects"])
	if err := json.Unmarshal(rawEffects, &effects); err != nil {
		t.Fatalf("decode effects: %v", err)
	}
	if len(effects) != 1 {
		t.Fatalf("created effects = %d, want 1", len(effects))
	}
	if effects[0].Kind != "request_observer_review" || effects[0].Status != "pending" {
		t.Fatalf("external effect should remain pending: %+v", effects[0])
	}
}

func TestTransitionIdempotentReplayDrainsPendingBuiltinEffect(t *testing.T) {
	svc, taskUUID, database := setupGapClosureFixture(t)

	if _, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: taskUUID,
		Kind:         "verdict",
		Ref:          "urn:test:verdict:approve",
		Facts:        `{"route":"approve"}`,
		Actor:        "reviewer-a",
		Role:         "reviewer",
	}); err != nil {
		t.Fatalf("AddEvidence verdict: %v", err)
	}

	rev0 := int64(0)
	const idemKey = "gap-replay-drain"
	result, err := svc.Transition(taskUUID, "decide", TransitionOptions{Actor: "reviewer-a", Role: "reviewer", ExpectRevision: &rev0, IdempotencyKey: idemKey})
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}
	var effects []Effect
	rawEffects, _ := json.Marshal(result["effects"])
	if err := json.Unmarshal(rawEffects, &effects); err != nil {
		t.Fatalf("decode effects: %v", err)
	}
	if len(effects) != 1 || effects[0].Status != "delivered" {
		t.Fatalf("initial transition did not deliver builtin effect: %+v", effects)
	}

	if _, err := database.Exec(`UPDATE tasks SET state = 'open', etag = etag + 1 WHERE uuid = ?`, taskUUID); err != nil {
		t.Fatalf("reset task state: %v", err)
	}
	if _, err := database.Exec(`
		UPDATE workflow_effects
		SET status = 'pending', attempts = 0, leased_by = NULL, leased_until = NULL, lease_token = NULL,
		    delivered_at = NULL, last_error = NULL, receipt_json = NULL
		WHERE id = ?
	`, effects[0].ID); err != nil {
		t.Fatalf("reset effect state: %v", err)
	}

	replayed, err := svc.Transition(taskUUID, "decide", TransitionOptions{Actor: "reviewer-a", Role: "reviewer", ExpectRevision: &rev0, IdempotencyKey: idemKey})
	if err != nil {
		t.Fatalf("replay transition: %v", err)
	}
	rawEffects, _ = json.Marshal(replayed["effects"])
	if err := json.Unmarshal(rawEffects, &effects); err != nil {
		t.Fatalf("decode replay effects: %v", err)
	}
	if len(effects) != 1 || effects[0].Status != "delivered" || len(effects[0].Receipt) == 0 {
		t.Fatalf("replay did not drain pending builtin effect: %+v", effects)
	}
	var state string
	if err := database.QueryRow(`SELECT state FROM tasks WHERE uuid = ?`, taskUUID).Scan(&state); err != nil {
		t.Fatalf("read task state: %v", err)
	}
	if state != "completed" {
		t.Fatalf("task state after replay = %q, want completed", state)
	}
}

func TestTransitionBuiltinEffectFailureIsVisible(t *testing.T) {
	svc, taskUUID, _ := setupGapClosureFixtureWithTemplate(t, "gap_invalid_builtin_effect_test", gapInvalidBuiltinEffectTemplate)

	rev0 := int64(0)
	result, err := svc.Transition(taskUUID, "decide", TransitionOptions{Actor: "reviewer-a", Role: "reviewer", ExpectRevision: &rev0})
	if got := wrkfCode(err); got != wrkfCodeEffectDeliveryFailed {
		t.Fatalf("transition error code = %q, want %s (err=%v)", got, wrkfCodeEffectDeliveryFailed, err)
	}
	if result == nil {
		t.Fatal("partial failure did not return the committed transition result")
	}
	var effects []Effect
	rawEffects, _ := json.Marshal(result["effects"])
	if err := json.Unmarshal(rawEffects, &effects); err != nil {
		t.Fatalf("decode effects: %v", err)
	}
	if len(effects) != 1 {
		t.Fatalf("created effects = %d, want 1", len(effects))
	}
	if effects[0].Status != "failed" || effects[0].Attempts != 1 || effects[0].LastError == "" {
		t.Fatalf("failed builtin effect was not visible with attempts/error: %+v", effects[0])
	}
}
