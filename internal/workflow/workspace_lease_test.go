package workflow

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceLeaseConcurrentClaimsCanonicalRootConflict(t *testing.T) {
	svc, _ := actionFixture(t)
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "same-root")
	if err := os.Symlink(root, link); err != nil {
		t.Fatalf("symlink workspace root: %v", err)
	}

	first, err := svc.ClaimWorkspace(ClaimWorkspaceParams{WorkspaceRoot: root, OwnerID: "runner-a", LeaseMs: 300000})
	if err != nil {
		t.Fatalf("ClaimWorkspace first: %v", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks root: %v", err)
	}
	if first.CanonicalRoot != canonicalRoot || first.LeaseToken == "" || first.OwnerGeneration != 1 {
		t.Fatalf("first lease = %+v", first)
	}

	_, err = svc.ClaimWorkspace(ClaimWorkspaceParams{WorkspaceRoot: link, OwnerID: "runner-b", LeaseMs: 300000})
	if err == nil || !strings.Contains(err.Error(), "lease conflict") {
		t.Fatalf("foreign claim through symlink error = %v, want lease conflict", err)
	}
}

func TestWorkspaceLeaseDifferentRootsDoNotConflict(t *testing.T) {
	svc, _ := actionFixture(t)
	a := t.TempDir()
	b := t.TempDir()

	first, err := svc.ClaimWorkspace(ClaimWorkspaceParams{WorkspaceRoot: a, OwnerID: "runner-a", LeaseMs: 300000})
	if err != nil {
		t.Fatalf("ClaimWorkspace first: %v", err)
	}
	second, err := svc.ClaimWorkspace(ClaimWorkspaceParams{WorkspaceRoot: b, OwnerID: "runner-b", LeaseMs: 300000})
	if err != nil {
		t.Fatalf("ClaimWorkspace second: %v", err)
	}
	if first.CanonicalRoot == second.CanonicalRoot {
		t.Fatalf("different workspace roots collapsed: first=%+v second=%+v", first, second)
	}
}

func TestWorkspaceLeaseHeartbeatReleaseWrongAndExpiredTokenFail(t *testing.T) {
	svc, _ := actionFixture(t)
	root := t.TempDir()
	lease, err := svc.ClaimWorkspace(ClaimWorkspaceParams{WorkspaceRoot: root, OwnerID: "runner-a", LeaseMs: 300000})
	if err != nil {
		t.Fatalf("ClaimWorkspace: %v", err)
	}

	_, err = svc.HeartbeatWorkspace(HeartbeatWorkspaceParams{WorkspaceRoot: root, LeaseToken: "wrong", OwnerGeneration: lease.OwnerGeneration, LeaseMs: 300000})
	if err == nil || !strings.Contains(err.Error(), "lease conflict") {
		t.Fatalf("wrong heartbeat error = %v, want lease conflict", err)
	}
	_, err = svc.ReleaseWorkspace(ReleaseWorkspaceParams{WorkspaceRoot: root, LeaseToken: "wrong", OwnerGeneration: lease.OwnerGeneration})
	if err == nil || !strings.Contains(err.Error(), "lease conflict") {
		t.Fatalf("wrong release error = %v, want lease conflict", err)
	}

	expireWorkspaceLeaseForTest(t, svc, root)
	_, err = svc.HeartbeatWorkspace(HeartbeatWorkspaceParams{WorkspaceRoot: root, LeaseToken: lease.LeaseToken, OwnerGeneration: lease.OwnerGeneration, LeaseMs: 300000})
	if err == nil || !strings.Contains(err.Error(), "lease conflict") {
		t.Fatalf("expired heartbeat error = %v, want lease conflict", err)
	}
	_, err = svc.ReleaseWorkspace(ReleaseWorkspaceParams{WorkspaceRoot: root, LeaseToken: lease.LeaseToken, OwnerGeneration: lease.OwnerGeneration})
	if err == nil || !strings.Contains(err.Error(), "lease conflict") {
		t.Fatalf("expired release error = %v, want lease conflict", err)
	}
}

func TestWorkspaceBoundActionSettleRequiresCurrentWorkspaceToken(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	root := t.TempDir()
	impl, err := svc.ClaimAction(ClaimActionParams{
		Task:          taskUUID,
		RunnerID:      "runner-impl",
		AgentRef:      "agent:impl",
		Prefer:        ActionClaimPrefer{Action: "implement"},
		LeaseMs:       300000,
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("ClaimAction implement with workspace: %v", err)
	}
	if impl.Binding.Workspace == nil || impl.Binding.Run.WorkspaceRef == "" {
		t.Fatalf("workspace-bound claim missing workspace authority: %+v", impl.Binding)
	}

	_, err = svc.SettleAction(SettleActionParams{
		ActionRunID:         impl.Binding.Run.ID,
		OwnerToken:          impl.Binding.Authority.OwnerToken,
		OwnerGeneration:     impl.Binding.Authority.OwnerGeneration,
		WorkspaceToken:      "wrong",
		WorkspaceGeneration: impl.Binding.Workspace.OwnerGeneration,
		Result:              "completed",
		Evidence:            &ActionEvidenceInput{Summary: "implemented", Facts: `{"result":"done","commit.sha":"abc123","change.id":"change-v1:abc123","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`},
	})
	if err == nil || !strings.Contains(err.Error(), "lease conflict") {
		t.Fatalf("wrong workspace settle error = %v, want lease conflict", err)
	}

	expireWorkspaceLeaseForTest(t, svc, root)
	_, err = svc.ClaimWorkspace(ClaimWorkspaceParams{WorkspaceRoot: root, OwnerID: "runner-b", LeaseMs: 300000})
	if err != nil {
		t.Fatalf("steal expired workspace lease: %v", err)
	}
	_, err = svc.SettleAction(SettleActionParams{
		ActionRunID:         impl.Binding.Run.ID,
		OwnerToken:          impl.Binding.Authority.OwnerToken,
		OwnerGeneration:     impl.Binding.Authority.OwnerGeneration,
		WorkspaceToken:      impl.Binding.Workspace.LeaseToken,
		WorkspaceGeneration: impl.Binding.Workspace.OwnerGeneration,
		Result:              "completed",
		Evidence:            &ActionEvidenceInput{Summary: "implemented", Facts: `{"result":"done","commit.sha":"abc123","change.id":"change-v1:abc123","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`},
	})
	if err == nil || !strings.Contains(err.Error(), "lease conflict") {
		t.Fatalf("stale workspace settle error = %v, want lease conflict", err)
	}
}

func TestWorkspaceRootCannotBeAddedToActiveUnboundActionReplay(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	impl := claimActionForTest(t, svc, taskUUID, "implement")

	_, err := svc.ClaimAction(ClaimActionParams{
		Task:          taskUUID,
		RunnerID:      impl.Binding.Authority.RunnerID,
		AgentRef:      "agent:implement",
		Prefer:        ActionClaimPrefer{Action: "implement"},
		LeaseMs:       300000,
		WorkspaceRoot: t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "lease conflict") {
		t.Fatalf("workspace retrofit replay error = %v, want lease conflict", err)
	}
}

func TestWorkspaceBoundActionReapOperatorRequiredEvidence(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	root := t.TempDir()
	impl, err := svc.ClaimAction(ClaimActionParams{
		Task:          taskUUID,
		RunnerID:      "runner-impl",
		AgentRef:      "agent:impl",
		Prefer:        ActionClaimPrefer{Action: "implement"},
		LeaseMs:       300000,
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("ClaimAction implement with workspace: %v", err)
	}
	expireActionRunForTest(t, svc, impl.Binding.Run.ID)
	expireWorkspaceLeaseForTest(t, svc, root)
	reaped, err := svc.ReapActions(ReapActionsParams{Task: taskUUID, Action: "implement", ExpiredBefore: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatalf("ReapActions: %v", err)
	}
	if len(reaped.Items) != 1 || reaped.Items[0].Status != "operator_required" {
		t.Fatalf("workspace-bound reap = %+v, want operator_required", reaped.Items)
	}
	ev := settledFailureEvidenceForRun(t, svc, impl.Binding.Run.ID)
	if ev == nil || ev.Kind != "failure_result" || !strings.Contains(ev.Summary, "action lease expired") {
		t.Fatalf("reap evidence = %+v, want failure_result action lease evidence", ev)
	}
	lease, err := svc.ShowWorkspace(ShowWorkspaceParams{WorkspaceRoot: root})
	if err != nil {
		t.Fatalf("ShowWorkspace: %v", err)
	}
	if lease.LeaseOwner != "" || lease.LeaseExpiresAt != "" || lease.ReleasedAt == "" {
		t.Fatalf("reap did not release matching workspace lease: %+v", lease)
	}
	var facts map[string]interface{}
	if err := json.Unmarshal(ev.Data, &facts); err != nil {
		t.Fatalf("unmarshal reap evidence facts: %v", err)
	}
	if got := facts["workspaceLeaseRelease"]; got != "released" {
		t.Fatalf("workspaceLeaseRelease = %#v, want released", got)
	}
}

func TestWorkspaceBoundActionReapPreservesNewerWorkspaceOwner(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	root := t.TempDir()
	impl, err := svc.ClaimAction(ClaimActionParams{
		Task:          taskUUID,
		RunnerID:      "runner-dead",
		AgentRef:      "agent:impl",
		Prefer:        ActionClaimPrefer{Action: "implement"},
		LeaseMs:       300000,
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("ClaimAction implement with workspace: %v", err)
	}
	expireActionRunForTest(t, svc, impl.Binding.Run.ID)
	expireWorkspaceLeaseForTest(t, svc, root)
	newer, err := svc.ClaimWorkspace(ClaimWorkspaceParams{WorkspaceRoot: root, OwnerID: "runner-new", LeaseMs: 300000})
	if err != nil {
		t.Fatalf("ClaimWorkspace newer owner: %v", err)
	}

	reaped, err := svc.ReapActions(ReapActionsParams{Task: taskUUID, Action: "implement", ExpiredBefore: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatalf("ReapActions: %v", err)
	}
	if len(reaped.Items) != 1 || reaped.Items[0].Status != "operator_required" {
		t.Fatalf("workspace-bound reap = %+v, want operator_required", reaped.Items)
	}
	lease, err := svc.ShowWorkspace(ShowWorkspaceParams{WorkspaceRoot: root})
	if err != nil {
		t.Fatalf("ShowWorkspace: %v", err)
	}
	if lease.LeaseOwner != newer.LeaseOwner || lease.OwnerGeneration != newer.OwnerGeneration || lease.LeaseExpiresAt == "" {
		t.Fatalf("reap clobbered newer workspace lease: got=%+v newer=%+v", lease, newer)
	}
	ev := settledFailureEvidenceForRun(t, svc, impl.Binding.Run.ID)
	var facts map[string]interface{}
	if err := json.Unmarshal(ev.Data, &facts); err != nil {
		t.Fatalf("unmarshal reap evidence facts: %v", err)
	}
	if got := facts["workspaceLeaseRelease"]; got != "owner_mismatch" {
		t.Fatalf("workspaceLeaseRelease = %#v, want owner_mismatch", got)
	}
}

func TestWorkspaceBoundActionReapPreservesLiveSameOwnerReclaim(t *testing.T) {
	svc, taskA := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskA)
	triage := claimActionForTest(t, svc, taskA, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	root := t.TempDir()
	oldRun, err := svc.ClaimAction(ClaimActionParams{
		Task:          taskA,
		RunnerID:      "runner-r",
		AgentRef:      "agent:impl-a",
		Prefer:        ActionClaimPrefer{Action: "implement"},
		LeaseMs:       300000,
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("ClaimAction old run: %v", err)
	}
	expireActionRunForTest(t, svc, oldRun.Binding.Run.ID)
	expireWorkspaceLeaseForTest(t, svc, root)

	const taskB = "ffffffff-ffff-4fff-ffff-000000000002"
	if _, err := svc.db.Exec(`
		INSERT INTO tasks (uuid, slug, title, specification, project_uuid, state, priority, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, 'act-task-b', 'Action Task B', 'Shaped spec.', 'eeeeeeee-eeee-4eee-eeee-000000000001', 'open', 2, 'task', 'dddddddd-dddd-4ddd-dddd-000000000001', 'dddddddd-dddd-4ddd-dddd-000000000001')
	`, taskB); err != nil {
		t.Fatalf("insert task B: %v", err)
	}
	attachSimpleTaskV2(t, svc, taskB)
	triageB := claimActionForTest(t, svc, taskB, "triage")
	settleClaimForTest(t, svc, triageB, `{"result":"ready"}`, "triaged")
	liveRun, err := svc.ClaimAction(ClaimActionParams{
		Task:          taskB,
		RunnerID:      "runner-r",
		AgentRef:      "agent:impl-b",
		Prefer:        ActionClaimPrefer{Action: "implement"},
		LeaseMs:       300000,
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("ClaimAction live same-owner re-claim: %v", err)
	}
	if liveRun.Binding.Workspace == nil || liveRun.Binding.Workspace.OwnerGeneration <= oldRun.Binding.Workspace.OwnerGeneration {
		t.Fatalf("live same-owner re-claim did not advance workspace generation: old=%+v live=%+v", oldRun.Binding.Workspace, liveRun.Binding.Workspace)
	}

	reaped, err := svc.ReapActions(ReapActionsParams{Task: taskA, Action: "implement", ExpiredBefore: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatalf("ReapActions: %v", err)
	}
	if len(reaped.Items) != 1 || reaped.Items[0].Status != "operator_required" {
		t.Fatalf("old workspace-bound reap = %+v, want operator_required", reaped.Items)
	}
	lease, err := svc.ShowWorkspace(ShowWorkspaceParams{WorkspaceRoot: root})
	if err != nil {
		t.Fatalf("ShowWorkspace: %v", err)
	}
	if lease.LeaseOwner != "runner-r" || lease.OwnerGeneration != liveRun.Binding.Workspace.OwnerGeneration || lease.LeaseExpiresAt == "" {
		t.Fatalf("reap clobbered live same-owner workspace lease: got=%+v live=%+v", lease, liveRun.Binding.Workspace)
	}
	ev := settledFailureEvidenceForRun(t, svc, oldRun.Binding.Run.ID)
	var facts map[string]interface{}
	if err := json.Unmarshal(ev.Data, &facts); err != nil {
		t.Fatalf("unmarshal reap evidence facts: %v", err)
	}
	if got := facts["workspaceLeaseRelease"]; got != "still_active" {
		t.Fatalf("workspaceLeaseRelease = %#v, want still_active", got)
	}
}

func expireWorkspaceLeaseForTest(t *testing.T, svc *Service, root string) {
	t.Helper()
	canonical, err := canonicalWorkspaceRoot(root)
	if err != nil {
		t.Fatalf("canonicalWorkspaceRoot: %v", err)
	}
	if _, err := svc.db.Exec(`UPDATE workflow_workspace_leases SET lease_expires_at = '2000-01-01T00:00:00Z' WHERE canonical_root = ?`, canonical); err != nil {
		t.Fatalf("expire workspace lease: %v", err)
	}
}

func settledFailureEvidenceForRun(t *testing.T, svc *Service, runID string) *Evidence {
	t.Helper()
	ev, err := settledEvidenceForRunTxForTest(svc, runID, "failure_result")
	if err != nil {
		t.Fatalf("settledEvidenceForRunTxForTest: %v", err)
	}
	return ev
}

func settledEvidenceForRunTxForTest(svc *Service, runID, kind string) (*Evidence, error) {
	var out *Evidence
	err := withImmediateTx(svc.db, func(tx *sql.Tx) error {
		ev, err := settledEvidenceForRunTx(tx, runID, kind)
		if err != nil {
			return err
		}
		out = ev
		return nil
	})
	return out, err
}
