package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const hookIsolationTemplate = `{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "hook_isolation",
  "version": "1",
  "kind": "agent_first_workflow",
  "initial": { "status": "active", "phase": "ready" },
  "roles": { "coordinator": { "description": "Test coordinator" } },
  "states": [
    { "status": "active", "phase": "ready" },
    { "status": "closed", "outcome": "done" }
  ],
  "checks": {
    "slow_check": {
      "type": "hook",
      "hookId": "slow_hook",
      "exitMap": {
        "0": { "verdict": "pass", "outcome": "passed" },
        "*": { "verdict": "error", "outcome": "failed" }
      }
    }
  },
  "transitions": [{
    "id": "complete",
    "from": { "status": "active", "phase": "ready" },
    "by": ["coordinator"],
    "checks": ["slow_check"],
    "outcomes": [{
      "id": "done",
      "when": { "checkVerdict": { "check": "slow_check", "is": "pass" } },
      "to": { "status": "closed", "outcome": "done" }
    }]
  }]
}`

func hookIsolationFixture(t *testing.T, argv []string, timeoutMs int) (*Service, string, *HookCatalog) {
	t.Helper()
	svc, taskUUID := actionFixture(t)
	catalog := &HookCatalog{
		SchemaVersion: "wrkf.hook-catalog.v0",
		Hooks: map[string]HookSpec{
			"slow_hook": {Kind: "exec", Argv: argv, TimeoutMs: timeoutMs},
		},
	}
	templatePath := filepath.Join(t.TempDir(), "hook-isolation.json")
	if err := os.WriteFile(templatePath, []byte(hookIsolationTemplate), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.InstallTemplate(templatePath, "agent:installer", catalog); err != nil {
		t.Fatalf("install template: %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, "hook_isolation@1", "agent:installer"); err != nil {
		t.Fatalf("attach template: %v", err)
	}
	return svc, taskUUID, catalog
}

func waitForTestFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func TestRunChecksHookDoesNotHoldWriterTransaction(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	release := filepath.Join(dir, "release")
	script := `touch "$1"; while [ ! -f "$2" ]; do sleep 0.01; done`
	svc, taskUUID, catalog := hookIsolationFixture(t, []string{"sh", "-c", script, "hook", started, release}, 5_000)
	t.Cleanup(func() { _ = os.WriteFile(release, []byte("release"), 0o600) })

	type checkResult struct {
		runs []CheckRun
		err  error
	}
	checkDone := make(chan checkResult, 1)
	go func() {
		runs, err := svc.RunChecksWithOptions(taskUUID, "complete", "agent:runner", "coordinator", catalog, "", HookExecutionOptions{})
		checkDone <- checkResult{runs: runs, err: err}
	}()
	waitForTestFile(t, started)

	writeDone := make(chan error, 1)
	go func() {
		_, err := svc.db.Exec(`UPDATE tasks SET title = ? WHERE uuid = ?`, "writer progressed", taskUUID)
		writeDone <- err
	}()
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("independent writer: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("independent writer blocked while hook was executing")
	}

	if err := os.WriteFile(release, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-checkDone:
		if result.err != nil {
			t.Fatalf("run checks: %v", result.err)
		}
		if len(result.runs) != 1 || result.runs[0].ID == "" {
			t.Fatalf("persisted runs = %#v", result.runs)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hook did not finish after release")
	}
}

func TestRunChecksRefusesOversizedRemoteTimeoutBeforeExecution(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	svc, taskUUID, catalog := hookIsolationFixture(t, []string{"sh", "-c", `touch "$1"`, "hook", marker}, 5_000)

	_, err := svc.RunChecksWithOptions(taskUUID, "complete", "agent:runner", "coordinator", catalog, "", HookExecutionOptions{TimeoutCeiling: 100 * time.Millisecond})
	if err == nil {
		t.Fatal("expected oversized timeout refusal")
	}
	detail, ok := AsErrorDetail(err)
	if !ok || detail.Code != wrkfCodeValidation || detail.Field != "timeoutMs" {
		t.Fatalf("error detail = %#v (typed=%v), err=%v", detail, ok, err)
	}
	if !strings.Contains(err.Error(), "exceeds remote execution ceiling") {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("oversized hook executed unexpectedly: %v", statErr)
	}
	runs, listErr := svc.ListCheckRuns(taskUUID, "complete")
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(runs) != 0 {
		t.Fatalf("oversized refusal persisted runs: %#v", runs)
	}
}

func TestRunChecksCancellationStopsBeforePersistence(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	svc, taskUUID, catalog := hookIsolationFixture(t, []string{"sh", "-c", `touch "$1"; exec sleep 5`, "hook", marker}, 5_000)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := svc.RunChecksWithOptions(taskUUID, "complete", "agent:runner", "coordinator", catalog, "", HookExecutionOptions{Context: ctx})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("run checks error = %v, want deadline exceeded", err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("hook never started: %v", statErr)
	}
	runs, listErr := svc.ListCheckRuns(taskUUID, "complete")
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(runs) != 0 {
		t.Fatalf("cancelled request persisted runs: %s", fmt.Sprint(runs))
	}
}

func TestTransitionRevalidatesPersistedCheckInputHash(t *testing.T) {
	svc, taskUUID, catalog := hookIsolationFixture(t, []string{"true"}, 5_000)
	runs, err := svc.RunChecks(taskUUID, "complete", "agent:runner", "coordinator", catalog, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID == "" {
		t.Fatalf("runs = %#v", runs)
	}
	if _, err := svc.db.Exec(`UPDATE tasks SET title = ? WHERE uuid = ?`, "changed after check", taskUUID); err != nil {
		t.Fatal(err)
	}

	_, err = svc.Transition(taskUUID, "complete", TransitionOptions{
		PrincipalRef: "agent:runner", Role: "coordinator", CheckIDs: []string{runs[0].ID},
	})
	if wrkfCode(err) != wrkfCodeTransitionBlocked {
		t.Fatalf("transition error = %v (code %q), want %s", err, wrkfCode(err), wrkfCodeTransitionBlocked)
	}
	inst, inspectErr := svc.LatestInstance(taskUUID)
	if inspectErr != nil {
		t.Fatal(inspectErr)
	}
	if inst.Revision != 0 || inst.Status != "active" {
		t.Fatalf("stale check mutated instance: %#v", inst)
	}
}

func TestTransitionRunChecksRefusedBeforeMutation(t *testing.T) {
	svc, taskUUID, _ := setupCASFixture(t)
	_, err := svc.Transition(taskUUID, "complete", TransitionOptions{
		PrincipalRef: "agent:runner", Role: "coordinator", RunChecks: true,
	})
	if err == nil {
		t.Fatal("expected runChecks refusal")
	}
	detail, ok := AsErrorDetail(err)
	if !ok || detail.Code != wrkfCodeValidation || detail.Field != "runChecks" {
		t.Fatalf("error detail = %#v (typed=%v), err=%v", detail, ok, err)
	}
	inst, inspectErr := svc.LatestInstance(taskUUID)
	if inspectErr != nil {
		t.Fatal(inspectErr)
	}
	if inst.Revision != 0 || inst.Status != "active" {
		t.Fatalf("instance mutated on refusal: %#v", inst)
	}
}
