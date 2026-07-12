package workrpc_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWrkfWorkspaceLeaseRPCContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "same-root")
	if err := os.Symlink(root, link); err != nil {
		t.Fatalf("symlink workspace root: %v", err)
	}
	other := t.TempDir()

	frames := p3Run(t, dbPath,
		mkRPC("claim-a", "wrkf.workspace.claim", map[string]any{
			"workspaceRoot": root, "ownerId": "runner-a", "leaseMs": float64(300000),
		}),
		mkRPC("claim-conflict", "wrkf.workspace.claim", map[string]any{
			"workspaceRoot": link, "ownerId": "runner-b", "leaseMs": float64(300000),
		}),
		mkRPC("claim-other", "wrkf.workspace.claim", map[string]any{
			"workspaceRoot": other, "ownerId": "runner-b", "leaseMs": float64(300000),
		}),
	)
	first := p2ResultOrFail(t, frames[1], "workspace.claim first")
	token, _ := first["leaseToken"].(string)
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks root: %v", err)
	}
	if token == "" || first["canonicalRoot"] != canonicalRoot || first["ownerGeneration"] != float64(1) {
		t.Fatalf("workspace.claim first = %#v", first)
	}
	if code := p2ErrCode(frames[2]); code != "WRKF_LEASE_CONFLICT" {
		t.Fatalf("same-root conflict code = %q, want WRKF_LEASE_CONFLICT", code)
	}
	second := p2ResultOrFail(t, frames[3], "workspace.claim different root")
	if second["canonicalRoot"] == first["canonicalRoot"] {
		t.Fatalf("different root claim collapsed: first=%#v second=%#v", first, second)
	}

	guardFrames := p3Run(t, dbPath,
		mkRPC("hb-bad", "wrkf.workspace.heartbeat", map[string]any{
			"workspaceRoot": root, "leaseToken": "wrong", "ownerGeneration": first["ownerGeneration"], "leaseMs": float64(300000),
		}),
		mkRPC("hb", "wrkf.workspace.heartbeat", map[string]any{
			"workspaceRoot": root, "leaseToken": token, "ownerGeneration": first["ownerGeneration"], "leaseMs": float64(300000),
		}),
		mkRPC("release-bad", "wrkf.workspace.release", map[string]any{
			"workspaceRoot": root, "leaseToken": "wrong", "ownerGeneration": first["ownerGeneration"],
		}),
		mkRPC("show", "wrkf.workspace.show", map[string]any{"workspaceRoot": root}),
	)
	if code := p2ErrCode(guardFrames[1]); code != "WRKF_LEASE_CONFLICT" {
		t.Fatalf("wrong heartbeat code = %q, want WRKF_LEASE_CONFLICT", code)
	}
	heartbeat := p2ResultOrFail(t, guardFrames[2], "workspace.heartbeat")
	if heartbeat["leaseToken"] != token {
		t.Fatalf("heartbeat token = %#v, want original token", heartbeat["leaseToken"])
	}
	if code := p2ErrCode(guardFrames[3]); code != "WRKF_LEASE_CONFLICT" {
		t.Fatalf("wrong release code = %q, want WRKF_LEASE_CONFLICT", code)
	}
	shown := p2ResultOrFail(t, guardFrames[4], "workspace.show")
	if _, ok := shown["leaseToken"]; ok {
		t.Fatalf("workspace.show leaked leaseToken: %#v", shown)
	}
}

func TestWrkfActionSettleWorkspaceFenceRPC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000037",
		"action-workspace-fence", "Action Workspace Fence")
	tplPath := "internal/workflow/builtins/wrkq-simple-task-v2.workflow.json"
	attachFrames := p3Run(t, dbPath,
		mkRPC("i1", "wrkf.workflow.install", map[string]any{"path": tplPath}),
		mkRPC("a1", "wrkq.workflow.attach", map[string]any{
			"task":     taskID,
			"workflow": "wrkq-simple-task@2",
		}),
	)
	p2ResultOrFail(t, attachFrames[1], "install v2")
	p2ResultOrFail(t, attachFrames[2], "attach v2")
	triage := actRPCClaim(t, dbPath, taskID, "triage")
	actRPCSettle(t, dbPath, triage, map[string]any{"result": "ready"}, "triaged")

	root := t.TempDir()
	claimFrames := p3Run(t, dbPath,
		mkRPC("claim-impl", "wrkf.action.claim", map[string]any{
			"task": taskID, "prefer": map[string]any{"action": "implement"},
			"runnerId": "runner-impl", "agentRef": "agent:impl",
			"leaseMs": float64(300000), "workspaceRoot": root,
		}),
	)
	binding := actClaimBinding(t, p2ResultOrFail(t, claimFrames[1], "workspace action claim"), "workspace action claim")
	run, _ := binding["run"].(map[string]any)
	auth, _ := binding["authority"].(map[string]any)
	workspace, _ := binding["workspace"].(map[string]any)
	if run["workspaceRef"] == "" || workspace == nil || workspace["leaseToken"] == "" {
		t.Fatalf("workspace action binding = %#v", binding)
	}

	badFrames := p3Run(t, dbPath,
		mkRPC("settle-bad", "wrkf.action.settle", map[string]any{
			"runId": run["id"], "ownerToken": auth["ownerToken"], "ownerGeneration": auth["ownerGeneration"],
			"workspaceToken": "wrong", "workspaceGeneration": workspace["ownerGeneration"],
			"result": "completed",
			"evidence": map[string]any{"summary": "bad", "facts": map[string]any{
				"result": "done", "commit.sha": "abc123", "change.id": "change-v1:abc123", "git.clean": true,
				"base.sha": "base000", "postcondition": "git_committed_clean", "repair.turns": 0,
			}},
		}),
	)
	if code := p2ErrCode(badFrames[1]); code != "WRKF_LEASE_CONFLICT" {
		t.Fatalf("wrong workspace settle code = %q, want WRKF_LEASE_CONFLICT", code)
	}

	goodFrames := p3Run(t, dbPath,
		mkRPC("settle-good", "wrkf.action.settle", map[string]any{
			"runId": run["id"], "ownerToken": auth["ownerToken"], "ownerGeneration": auth["ownerGeneration"],
			"workspaceToken": workspace["leaseToken"], "workspaceGeneration": workspace["ownerGeneration"],
			"result": "completed",
			"evidence": map[string]any{"summary": "implemented", "facts": map[string]any{
				"result": "done", "commit.sha": "abc123", "change.id": "change-v1:abc123", "git.clean": true,
				"base.sha": "base000", "postcondition": "git_committed_clean", "repair.turns": 0,
			}},
		}),
		mkRPC("release", "wrkf.workspace.release", map[string]any{
			"workspaceRoot": root, "leaseToken": workspace["leaseToken"], "ownerGeneration": workspace["ownerGeneration"],
		}),
	)
	settled := p2ResultOrFail(t, goodFrames[1], "workspace action settle")
	settledRun, _ := settled["run"].(map[string]any)
	if settledRun["status"] != "completed" {
		t.Fatalf("settled run = %#v, want completed", settledRun)
	}
	released := p2ResultOrFail(t, goodFrames[2], "workspace.release")
	if _, ok := released["leaseToken"]; ok {
		t.Fatalf("workspace.release should return no live token after release: %#v", released)
	}
}

func TestWrkfActionWorkspaceExpiryDoesNotTerminalizeRPC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath,
		"a5000000-0000-4000-8000-000000000038",
		"action-workspace-expiry", "Action Workspace Expiry")
	tplPath := "internal/workflow/builtins/wrkq-simple-task-v2.workflow.json"
	p3Run(t, dbPath,
		mkRPC("i1", "wrkf.workflow.install", map[string]any{"path": tplPath}),
		mkRPC("a1", "wrkq.workflow.attach", map[string]any{
			"task":     taskID,
			"workflow": "wrkq-simple-task@2",
		}),
	)
	triage := actRPCClaim(t, dbPath, taskID, "triage")
	actRPCSettle(t, dbPath, triage, map[string]any{"result": "ready"}, "triaged")
	root := t.TempDir()
	claimFrames := p3Run(t, dbPath,
		mkRPC("claim-impl", "wrkf.action.claim", map[string]any{
			"task": taskID, "prefer": map[string]any{"action": "implement"},
			"runnerId": "runner-impl", "agentRef": "agent:impl",
			"leaseMs": float64(1), "workspaceRoot": root,
		}),
	)
	binding := actClaimBinding(t, p2ResultOrFail(t, claimFrames[1], "workspace action claim"), "workspace action claim")
	run, _ := binding["run"].(map[string]any)
	time.Sleep(5 * time.Millisecond)
	showFrames := p3Run(t, dbPath,
		mkRPC("show", "wrkf.action.show", map[string]any{"actionRunId": run["id"]}),
	)
	shown := p2ResultOrFail(t, showFrames[1], "action.show expired workspace-bound implement")
	if shown["runId"] != run["id"] || shown["status"] != "active" {
		t.Fatalf("expired workspace-bound run = %#v, want run %s active", shown, run["id"])
	}
}
