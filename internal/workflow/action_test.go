package workflow

// action_test.go — service-level tests for the low-ceremony wrkf.action.* API
// (T-05009). These exercise the composition directly against workflow.Service,
// complementing the stdio RPC acceptance tests in internal/workrpc.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	// Seed with a specification so triage_complete resolves the ready outcome by
	// default. The blocked-on-missing-spec path is covered by a dedicated test
	// that clears the specification.
	if _, err := database.Exec(
		`INSERT INTO tasks (uuid, slug, title, specification, project_uuid, state, priority, kind, created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'act-task', 'Action Task', 'Shaped spec.', ?, 'open', 2, 'task', ?, ?)`,
		taskUUID, containerUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	return svc, taskUUID
}

// T-05067: action leases are durable recovery metadata, so a migrated workflow
// DB must expose the fields the coordinator/reaper can rely on.
func TestActionLeaseRecoveryMigrationAddsRunColumns(t *testing.T) {
	svc, _ := actionFixture(t)

	rows, err := svc.db.Query(`PRAGMA table_info(workflow_runs)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info(workflow_runs): %v", err)
	}
	defer func() { _ = rows.Close() }()

	got := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan workflow_runs column: %v", err)
		}
		got[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read workflow_runs columns: %v", err)
	}

	for _, want := range []string{
		"lease_owner", "lease_token", "lease_expires_at", "heartbeat_at",
		"semantic_action_key", "attempt", "agent_ref", "scope_ref",
		"handler_contract", "handler_id", "handler_version", "workspace_ref",
		"source_run_id", "source_evidence_id", "source_commit_sha", "owner_generation",
	} {
		if !got[want] {
			t.Errorf("workflow_runs missing %s column for action lease recovery", want)
		}
	}
}

func TestStartAction_BuiltinAttachAndIdempotency(t *testing.T) {
	svc, taskUUID := actionFixture(t)

	run, err := svc.StartAction(StartActionParams{
		Task: taskUUID, Action: "triage", PrincipalRef: "agent:t", IdempotencyKey: "k1",
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
		Task: taskUUID, Action: "triage", PrincipalRef: "agent:t", IdempotencyKey: "k1",
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
		Task: taskUUID, Action: "implement", PrincipalRef: "agent:t", IdempotencyKey: "k1",
	}); err == nil {
		t.Errorf("expected idempotency mismatch for same key + different action")
	}
}

func TestCompleteAction_EvidenceTransitionFinishReplay(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: "triage", PrincipalRef: "agent:t"})
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
	run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: "triage", PrincipalRef: "agent:t"})
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
		PrincipalRef:   run.PrincipalRef,
		Role:           run.Role,
		RunID:          run.RunID,
		IdempotencyKey: "wrkf-action:" + run.RunID + ":evidence:triage_result",
	}); err != nil {
		t.Fatalf("pre-commit evidence: %v", err)
	}
	if _, err := svc.TransitionForSelectors("", inst.ID, "triage_complete", TransitionOptions{
		PrincipalRef:   run.PrincipalRef,
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
		run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: s.action, PrincipalRef: "agent:t"})
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
	run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: "triage", PrincipalRef: "agent:t"})
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

// T-05067: a failed/reaped action is terminal. Retrying success completion after
// terminalization must not record success evidence or apply a success transition.
func TestCompleteAction_TerminalFailedRunRejectsSuccessSideEffects(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: "triage", PrincipalRef: "agent:t"})
	if err != nil {
		t.Fatalf("StartAction: %v", err)
	}
	if _, err := svc.FailAction(FailActionParams{
		ActionRunID: run.RunID,
		Summary:     "action lease expired: agent-loop",
		Evidence:    &ActionEvidenceInput{Summary: "lease expired"},
	}); err != nil {
		t.Fatalf("FailAction: %v", err)
	}

	if _, err := svc.CompleteAction(CompleteActionParams{
		ActionRunID: run.RunID,
		Evidence:    &ActionEvidenceInput{Summary: "should not be recorded"},
		RunSummary:  "should not complete",
	}); err == nil {
		t.Fatalf("CompleteAction on terminal failed run succeeded; want lease/terminal conflict")
	}

	show, err := svc.ShowAction(run.RunID)
	if err != nil {
		t.Fatalf("ShowAction: %v", err)
	}
	if show.Status != "failed" {
		t.Errorf("terminal run status = %q, want failed", show.Status)
	}
	if len(show.EvidenceKinds) != 1 || show.EvidenceKinds[0] != "failure_result" {
		t.Errorf("terminal failed run evidence kinds = %v, want only [failure_result]", show.EvidenceKinds)
	}
	if len(show.TransitionEventIDs) != 0 {
		t.Errorf("terminal failed run recorded success transition ids %v, want none", show.TransitionEventIDs)
	}
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if inst.Phase != "intake" {
		t.Errorf("instance phase after terminal completion attempt = %q, want intake", inst.Phase)
	}
}

// --- Triage spec-gate (blocked-on-no-spec) tests ---

func setTaskSpecAndState(t *testing.T, svc *Service, taskUUID, spec, state string) {
	t.Helper()
	if _, err := svc.db.Exec(`UPDATE tasks SET specification = ?, state = ? WHERE uuid = ?`, spec, state, taskUUID); err != nil {
		t.Fatalf("update task spec/state: %v", err)
	}
}

func readTaskState(t *testing.T, svc *Service, taskUUID string) string {
	t.Helper()
	var s string
	if err := svc.db.QueryRow(`SELECT state FROM tasks WHERE uuid = ?`, taskUUID).Scan(&s); err != nil {
		t.Fatalf("read task state: %v", err)
	}
	return s
}

// daedalus required test 1: a successful triage normalizes task.state in_progress -> open.
func TestCompleteAction_TriageReadyNormalizesInProgressToOpen(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	setTaskSpecAndState(t, svc, taskUUID, "A shaped specification.", "in_progress")

	run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: "triage", PrincipalRef: "agent:t"})
	if err != nil {
		t.Fatalf("StartAction: %v", err)
	}
	out, err := svc.CompleteAction(CompleteActionParams{ActionRunID: run.RunID, Evidence: &ActionEvidenceInput{Summary: "shaped"}, RunSummary: "done"})
	if err != nil {
		t.Fatalf("CompleteAction: %v", err)
	}
	if out.Run.Status != "completed" {
		t.Errorf("run status = %q, want completed", out.Run.Status)
	}
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if inst.Status != "active" || inst.Phase != "ready" {
		t.Errorf("workflow = %s/%s, want active/ready", inst.Status, inst.Phase)
	}
	if got := readTaskState(t, svc, taskUUID); got != "open" {
		t.Errorf("task state = %q, want open (in_progress must not survive triage)", got)
	}
}

// daedalus required test 2: empty spec leaves workflow open/intake and blocks the task.
func TestCompleteAction_TriageNoSpecBlocksTask(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	setTaskSpecAndState(t, svc, taskUUID, "", "open")

	run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: "triage", PrincipalRef: "agent:t"})
	if err != nil {
		t.Fatalf("StartAction: %v", err)
	}
	out, err := svc.CompleteAction(CompleteActionParams{ActionRunID: run.RunID, Evidence: &ActionEvidenceInput{Summary: "no actionable detail"}, RunSummary: "blocked"})
	if err != nil {
		t.Fatalf("CompleteAction: %v", err)
	}
	if out.Run.Status != "completed" {
		t.Errorf("run status = %q, want completed (blocked is a task state, the action still completes)", out.Run.Status)
	}
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if inst.Status != "open" || inst.Phase != "intake" {
		t.Errorf("workflow = %s/%s, want open/intake (held for re-triage)", inst.Status, inst.Phase)
	}
	if got := readTaskState(t, svc, taskUUID); got != "blocked" {
		t.Errorf("task state = %q, want blocked", got)
	}
}

// daedalus required test 3: whitespace-only spec follows the blocked path.
func TestCompleteAction_TriageWhitespaceSpecBlocksTask(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	setTaskSpecAndState(t, svc, taskUUID, "   \n\t  ", "open")

	run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: "triage", PrincipalRef: "agent:t"})
	if err != nil {
		t.Fatalf("StartAction: %v", err)
	}
	if _, err := svc.CompleteAction(CompleteActionParams{ActionRunID: run.RunID, Evidence: &ActionEvidenceInput{Summary: "whitespace only"}}); err != nil {
		t.Fatalf("CompleteAction: %v", err)
	}
	inst, _ := svc.LatestInstance(taskUUID)
	if inst.Phase != "intake" {
		t.Errorf("workflow phase = %q, want intake", inst.Phase)
	}
	if got := readTaskState(t, svc, taskUUID); got != "blocked" {
		t.Errorf("task state = %q, want blocked (whitespace-only spec is not a spec)", got)
	}
}

// daedalus required test 4: blocked self-transition replay is idempotent.
func TestCompleteAction_TriageBlockedReplayIdempotent(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	setTaskSpecAndState(t, svc, taskUUID, "", "open")

	run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: "triage", PrincipalRef: "agent:t"})
	if err != nil {
		t.Fatalf("StartAction: %v", err)
	}
	first, err := svc.CompleteAction(CompleteActionParams{ActionRunID: run.RunID, Evidence: &ActionEvidenceInput{Summary: "blocked"}})
	if err != nil {
		t.Fatalf("CompleteAction first: %v", err)
	}
	second, err := svc.CompleteAction(CompleteActionParams{ActionRunID: run.RunID, Evidence: &ActionEvidenceInput{Summary: "blocked"}})
	if err != nil {
		t.Fatalf("CompleteAction replay: %v", err)
	}
	if first.Evidence == nil || second.Evidence == nil || first.Evidence.ID != second.Evidence.ID {
		t.Errorf("replay produced different/absent evidence: %+v vs %+v", first.Evidence, second.Evidence)
	}
	if second.Run.Status != "completed" {
		t.Errorf("replay run status = %q, want completed", second.Run.Status)
	}
	if got := readTaskState(t, svc, taskUUID); got != "blocked" {
		t.Errorf("task state after replay = %q, want blocked", got)
	}
}

// daedalus required test 5: a blocked task can be re-triaged to ready with state open.
func TestCompleteAction_TriageReTriageFromBlocked(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	setTaskSpecAndState(t, svc, taskUUID, "", "open")

	run1, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: "triage", PrincipalRef: "agent:t", IdempotencyKey: "t1"})
	if err != nil {
		t.Fatalf("StartAction 1: %v", err)
	}
	if _, err := svc.CompleteAction(CompleteActionParams{ActionRunID: run1.RunID, Evidence: &ActionEvidenceInput{Summary: "blocked"}}); err != nil {
		t.Fatalf("CompleteAction 1: %v", err)
	}
	if got := readTaskState(t, svc, taskUUID); got != "blocked" {
		t.Fatalf("precondition: task state = %q, want blocked", got)
	}

	// Re-triage: a spec is now produced.
	setTaskSpecAndState(t, svc, taskUUID, "Now properly shaped.", "blocked")
	run2, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: "triage", PrincipalRef: "agent:t", IdempotencyKey: "t2"})
	if err != nil {
		t.Fatalf("StartAction 2: %v", err)
	}
	if _, err := svc.CompleteAction(CompleteActionParams{ActionRunID: run2.RunID, Evidence: &ActionEvidenceInput{Summary: "shaped"}}); err != nil {
		t.Fatalf("CompleteAction 2: %v", err)
	}
	inst, _ := svc.LatestInstance(taskUUID)
	if inst.Status != "active" || inst.Phase != "ready" {
		t.Errorf("workflow = %s/%s, want active/ready after re-triage", inst.Status, inst.Phase)
	}
	if got := readTaskState(t, svc, taskUUID); got != "open" {
		t.Errorf("task state after re-triage = %q, want open (unblocked)", got)
	}
}

// daedalus required test 6: file-based InstallTemplate stays immutable on a same
// id/version hash mismatch (the built-in supersede exception does not leak here).
func TestInstallTemplate_RejectsSameVersionDifferentHash(t *testing.T) {
	svc, _ := actionFixture(t)
	dir := t.TempDir()

	p1 := filepath.Join(dir, "t1.json")
	if err := os.WriteFile(p1, builtinSimpleTaskJSON, 0o644); err != nil {
		t.Fatalf("write t1: %v", err)
	}
	if _, err := svc.InstallTemplate(p1, "installer", nil); err != nil {
		t.Fatalf("first install: %v", err)
	}

	var def map[string]interface{}
	if err := json.Unmarshal(builtinSimpleTaskJSON, &def); err != nil {
		t.Fatalf("unmarshal builtin: %v", err)
	}
	def["description"] = "different content yields a different hash"
	mod, err := json.Marshal(def)
	if err != nil {
		t.Fatalf("marshal modified: %v", err)
	}
	p2 := filepath.Join(dir, "t2.json")
	if err := os.WriteFile(p2, mod, 0o644); err != nil {
		t.Fatalf("write t2: %v", err)
	}
	if _, err := svc.InstallTemplate(p2, "installer", nil); err == nil || !strings.Contains(err.Error(), "different hash") {
		t.Fatalf("file-based install must reject same id/version with different hash, got: %v", err)
	}
}
