//go:build wrkq_local

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

func startV1Action(svc *Service, params StartActionParams) (*ActionRun, error) {
	if strings.TrimSpace(params.Workflow) == "" {
		params.Workflow = BuiltinSimpleTaskTemplateRef
	}
	return svc.StartAction(params)
}

// TestCompleteAction_PartialReplayRecovers simulates a mid-sequence failure
// where evidence and the transition are already committed but the run was not
// finished, then retries CompleteAction and asserts recovery without duplicate
// evidence/transition and with the run finished (daedalus required test #2).
func TestCompleteAction_PartialReplayRecovers(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	run, err := startV1Action(svc, StartActionParams{Task: taskUUID, Action: "triage", PrincipalRef: "agent:t"})
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

	evList, err := svc.ListEvidence(taskUUID, "")
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

// T-05067: a failed/reaped action is terminal. Retrying success completion after
// terminalization must not record success evidence or apply a success transition.
func TestCompleteAfterFailIsRefusedWithoutSuccessEvidence(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	run, err := startV1Action(svc, StartActionParams{Task: taskUUID, Action: "triage", PrincipalRef: "agent:t"})
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

	_, err = svc.CompleteAction(CompleteActionParams{
		ActionRunID: run.RunID,
		Evidence:    &ActionEvidenceInput{Summary: "should not be recorded"},
		RunSummary:  "should not complete",
	})
	requireWrkfCode(t, err, "WRKF_LEASE_CONFLICT")

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

	run, err := startV1Action(svc, StartActionParams{Task: taskUUID, Action: "triage", PrincipalRef: "agent:t"})
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

// daedalus required tests 2+3: an empty or whitespace-only spec leaves the
// workflow open/intake (held for re-triage) and blocks the task, while the
// triage action itself still completes.
func TestTriageWithoutSpecBlocksTaskAndHoldsIntake(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec string
	}{
		{name: "empty", spec: ""},
		{name: "whitespace-only", spec: "   \n\t  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, taskUUID := actionFixture(t)
			setTaskSpecAndState(t, svc, taskUUID, tc.spec, "open")

			run, err := startV1Action(svc, StartActionParams{Task: taskUUID, Action: "triage", PrincipalRef: "agent:t"})
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
		})
	}
}

// daedalus required test 5: a blocked task can be re-triaged to ready with state open.
func TestCompleteAction_TriageReTriageFromBlocked(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	setTaskSpecAndState(t, svc, taskUUID, "", "open")

	run1, err := startV1Action(svc, StartActionParams{Task: taskUUID, Action: "triage", PrincipalRef: "agent:t", IdempotencyKey: "t1"})
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
	run2, err := startV1Action(svc, StartActionParams{Task: taskUUID, Action: "triage", PrincipalRef: "agent:t", IdempotencyKey: "t2"})
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
