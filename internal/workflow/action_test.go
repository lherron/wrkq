package workflow

// action_test.go — service-level tests for the low-ceremony wrkf.action.* API
// (T-05009). These exercise the composition directly against workflow.Service,
// complementing the stdio RPC acceptance tests in internal/workrpc.

import (
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// actionFixture returns a Service and a bare task UUID with NO workflow attached,
// so StartAction installs and attaches the built-in wrkq-simple-task workflow.
func actionFixture(t *testing.T) (*Service, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "action_test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	svc := NewService(database)

	actorUUID := "dddddddd-dddd-4ddd-dddd-000000000001"
	if _, err := database.Exec(`INSERT INTO actors (uuid, slug, role) VALUES (?, 'act-actor', 'system')`, actorUUID); err != nil {
		t.Fatalf("insert actor: %v", err)
	}
	containerUUID := "eeeeeeee-eeee-4eee-eeee-000000000001"
	if _, err := database.Exec(
		`INSERT INTO containers (uuid, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'act-project', 'Action Project', (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		containerUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert container: %v", err)
	}
	taskUUID := "ffffffff-ffff-4fff-ffff-000000000001"
	if _, err := database.Exec(
		`INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, kind, created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'act-task', 'Action Task', ?, 'open', 2, 'task', ?, ?)`,
		taskUUID, containerUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	return svc, taskUUID
}

func TestStartAction_BuiltinAttachAndIdempotency(t *testing.T) {
	svc, taskUUID := actionFixture(t)

	run, err := svc.StartAction(StartActionParams{
		Task: taskUUID, Action: "triage", Actor: "agent:t", IdempotencyKey: "k1",
	})
	if err != nil {
		t.Fatalf("StartAction: %v", err)
	}
	if run.Role != "triager" {
		t.Errorf("role = %q, want triager", run.Role)
	}
	if run.Lane != "triage" {
		t.Errorf("lane = %q, want triage", run.Lane)
	}
	if run.Workflow.ID != "wrkq-simple-task" || run.Workflow.Version != "1" {
		t.Errorf("workflow = %+v, want wrkq-simple-task@1", run.Workflow)
	}
	if run.Status != "active" {
		t.Errorf("status = %q, want active", run.Status)
	}

	// Replay → same run.
	again, err := svc.StartAction(StartActionParams{
		Task: taskUUID, Action: "triage", Actor: "agent:t", IdempotencyKey: "k1",
	})
	if err != nil {
		t.Fatalf("StartAction replay: %v", err)
	}
	if again.RunID != run.RunID {
		t.Errorf("replay run %q != original %q", again.RunID, run.RunID)
	}

	// Exactly one run on the instance.
	runs, err := svc.ListRuns(taskUUID)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Errorf("expected 1 run, got %d", len(runs))
	}

	// Mismatch: same key, different action → idempotency mismatch.
	if _, err := svc.StartAction(StartActionParams{
		Task: taskUUID, Action: "implement", Actor: "agent:t", IdempotencyKey: "k1",
	}); err == nil {
		t.Errorf("expected idempotency mismatch for same key + different action")
	}
}

func TestCompleteAction_EvidenceTransitionFinishReplay(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: "triage", Actor: "agent:t"})
	if err != nil {
		t.Fatalf("StartAction: %v", err)
	}

	out, err := svc.CompleteAction(CompleteActionParams{
		ActionRunID: run.RunID,
		Evidence:    &ActionEvidenceInput{Summary: "ok"},
		RunSummary:  "done",
	})
	if err != nil {
		t.Fatalf("CompleteAction: %v", err)
	}
	if out.Evidence == nil || out.Evidence.Kind != "triage_result" {
		t.Errorf("evidence = %+v, want kind triage_result", out.Evidence)
	}
	if out.Evidence.RunID != run.RunID {
		t.Errorf("evidence runId = %q, want %q", out.Evidence.RunID, run.RunID)
	}
	if out.Transition == nil {
		t.Fatalf("expected a transition")
	}
	if out.Run.Status != "completed" {
		t.Errorf("run status = %q, want completed", out.Run.Status)
	}

	// Instance advanced to active/ready.
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if inst.Phase != "ready" {
		t.Errorf("instance phase = %q, want ready", inst.Phase)
	}

	// Replay: same evidence id, no duplicate, run still completed.
	replay, err := svc.CompleteAction(CompleteActionParams{
		ActionRunID: run.RunID,
		Evidence:    &ActionEvidenceInput{Summary: "ok"},
		RunSummary:  "done",
	})
	if err != nil {
		t.Fatalf("CompleteAction replay: %v", err)
	}
	if replay.Evidence == nil || replay.Evidence.ID != out.Evidence.ID {
		t.Errorf("replay evidence id = %v, want %v", replay.Evidence, out.Evidence.ID)
	}
	evList, err := svc.ListEvidence(taskUUID)
	if err != nil {
		t.Fatalf("ListEvidence: %v", err)
	}
	if len(evList) != 1 {
		t.Errorf("expected 1 evidence after replay, got %d", len(evList))
	}
}

// TestCompleteAction_PartialReplayRecovers simulates a mid-sequence failure
// where evidence and the transition are already committed but the run was not
// finished, then retries CompleteAction and asserts recovery without duplicate
// evidence/transition and with the run finished (daedalus required test #2).
func TestCompleteAction_PartialReplayRecovers(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: "triage", Actor: "agent:t"})
	if err != nil {
		t.Fatalf("StartAction: %v", err)
	}
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}

	// Pre-commit exactly what CompleteAction would, using the same deterministic
	// idempotency keys, but DO NOT finish the run (the partial-failure window).
	if _, err := svc.AddEvidence(AddEvidenceParams{
		InstanceID:     run.InstanceID,
		Kind:           "triage_result",
		Ref:            "wrkf-action:" + run.RunID,
		Summary:        "ok",
		Actor:          run.Actor,
		Role:           run.Role,
		RunID:          run.RunID,
		IdempotencyKey: "wrkf-action:" + run.RunID + ":evidence:triage_result",
	}); err != nil {
		t.Fatalf("pre-commit evidence: %v", err)
	}
	if _, err := svc.TransitionForSelectors("", inst.ID, "triage_complete", TransitionOptions{
		Actor:          run.Actor,
		Role:           run.Role,
		IdempotencyKey: "wrkf-action:" + run.RunID + ":transition:triage_complete",
		RunID:          run.RunID,
	}); err != nil {
		t.Fatalf("pre-commit transition: %v", err)
	}
	// Run is still active at this point (finish never happened).
	mid, err := svc.ShowRun(run.RunID)
	if err != nil {
		t.Fatalf("ShowRun: %v", err)
	}
	if mid.Status != "active" {
		t.Fatalf("precondition: run should still be active, got %q", mid.Status)
	}

	// Retry → must finish the run with no duplicate evidence/transition.
	out, err := svc.CompleteAction(CompleteActionParams{
		ActionRunID: run.RunID,
		Evidence:    &ActionEvidenceInput{Summary: "ok"},
		RunSummary:  "done",
	})
	if err != nil {
		t.Fatalf("CompleteAction retry: %v", err)
	}
	if out.Run.Status != "completed" {
		t.Errorf("run status = %q, want completed after retry", out.Run.Status)
	}

	evList, err := svc.ListEvidence(taskUUID)
	if err != nil {
		t.Fatalf("ListEvidence: %v", err)
	}
	if len(evList) != 1 {
		t.Errorf("expected exactly 1 evidence after partial replay, got %d", len(evList))
	}
	show, err := svc.ShowAction(run.RunID)
	if err != nil {
		t.Fatalf("ShowAction: %v", err)
	}
	if len(show.TransitionEventIDs) != 1 {
		t.Errorf("expected exactly 1 transition event after partial replay, got %d", len(show.TransitionEventIDs))
	}
	final, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if final.Phase != "ready" {
		t.Errorf("instance phase = %q, want ready (single transition)", final.Phase)
	}
}

// TestCompleteAction_DefaultEvidenceKindMatchesTemplate asserts the generated
// default evidence kind (<action>_result) matches the built-in workflow's
// declared vocabulary for implement and verify (daedalus required test #3).
func TestCompleteAction_DefaultEvidenceKindMatchesTemplate(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	if _, _, err := svc.EnsureBuiltinTemplate(BuiltinSimpleTaskTemplateRef, "agent:t"); err != nil {
		t.Fatalf("EnsureBuiltinTemplate: %v", err)
	}
	tpl, _, err := svc.ShowTemplate(BuiltinSimpleTaskTemplateRef)
	if err != nil {
		t.Fatalf("load template: %v", err)
	}

	type step struct {
		action   string
		wantKind string
	}
	for _, s := range []step{
		{"triage", "triage_result"},
		{"implement", "implement_result"},
		{"verify", "verify_result"},
		{"review", "review_result"},
	} {
		run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: s.action, Actor: "agent:t"})
		if err != nil {
			t.Fatalf("StartAction %s: %v", s.action, err)
		}
		out, err := svc.CompleteAction(CompleteActionParams{
			ActionRunID: run.RunID,
			Evidence:    &ActionEvidenceInput{Summary: s.action},
		})
		if err != nil {
			t.Fatalf("CompleteAction %s: %v", s.action, err)
		}
		if out.Evidence == nil || out.Evidence.Kind != s.wantKind {
			t.Errorf("%s default evidence kind = %v, want %q", s.action, out.Evidence, s.wantKind)
		}
		if _, ok := tpl.EvidenceKinds[s.wantKind]; !ok {
			t.Errorf("built-in template does not declare evidence kind %q produced by action %q", s.wantKind, s.action)
		}
	}
}

func TestFailAction_EvidenceAndTerminalNoTransition(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: "triage", Actor: "agent:t"})
	if err != nil {
		t.Fatalf("StartAction: %v", err)
	}

	failed, err := svc.FailAction(FailActionParams{
		ActionRunID: run.RunID,
		Summary:     "nope",
		Evidence:    &ActionEvidenceInput{Summary: "boom"},
	})
	if err != nil {
		t.Fatalf("FailAction: %v", err)
	}
	if failed.Status != "failed" {
		t.Errorf("status = %q, want failed", failed.Status)
	}

	show, err := svc.ShowAction(run.RunID)
	if err != nil {
		t.Fatalf("ShowAction: %v", err)
	}
	if len(show.EvidenceKinds) != 1 || show.EvidenceKinds[0] != "failure_result" {
		t.Errorf("evidence kinds = %v, want [failure_result]", show.EvidenceKinds)
	}
	if len(show.TransitionEventIDs) != 0 {
		t.Errorf("fail must not apply a transition, got %v", show.TransitionEventIDs)
	}

	// Instance stayed in intake.
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if inst.Phase != "intake" {
		t.Errorf("instance phase = %q, want intake", inst.Phase)
	}
}
