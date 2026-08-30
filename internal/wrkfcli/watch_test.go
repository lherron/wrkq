//go:build wrkq_local

package wrkfcli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workflow"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
	workrpcclient "github.com/lherron/wrkq/pkg/client"
	"github.com/spf13/cobra"
)

const watchCLITestTemplate = `{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "watch_cli_test",
  "version": "1",
  "kind": "agent_first_workflow",
  "initial": { "status": "active", "phase": "ready" },
  "roles": {
    "coordinator": { "description": "Test coordinator role" }
  },
  "states": [
    { "status": "active", "phase": "ready" },
    { "status": "closed", "outcome": "done" }
  ],
  "suspension": { "reasons": ["operator_required"] },
  "transitions": [
    {
      "id": "park",
      "from": { "status": "active", "phase": "ready" },
      "by": ["coordinator"],
      "outcomes": [
        { "id": "needs_operator", "when": { "always": true }, "suspend": { "reason": "operator_required" } }
      ]
    },
    {
      "id": "complete",
      "from": { "status": "active", "phase": "ready" },
      "by": ["coordinator"],
      "outcomes": [
        { "id": "done", "when": { "always": true }, "to": { "status": "closed", "outcome": "done" } }
      ]
    }
  ]
}`

func TestWatchCLIExitCodeMapping(t *testing.T) {
	a, taskUUID, database, _ := watchCLIFixture(t)

	for _, tc := range []struct {
		name  string
		flags watchFlags
		sel   string
		want  int
	}{
		{name: "bad until", flags: watchFlags{until: "bogus", pollInterval: "1ms"}, sel: taskUUID, want: 2},
		{name: "bad duration", flags: watchFlags{until: workflow.WatchUntilTerminal, timeout: "nope", pollInterval: "1ms"}, sel: taskUUID, want: 2},
		{name: "bad selector", flags: watchFlags{until: workflow.WatchUntilTerminal, pollInterval: "1ms"}, sel: "missing-task", want: 2},
		{name: "timeout", flags: watchFlags{until: workflow.WatchUntilWaiting, timeout: "1ms", pollInterval: "1ms"}, sel: taskUUID, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runWatch(a, testWatchCmd(), tc.sel, tc.flags)
			if code := ExitCodeForError(err); code != tc.want {
				t.Fatalf("exit code=%d, want %d (err=%v)", code, tc.want, err)
			}
		})
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- runWatch(a, testWatchCmd(), taskUUID, watchFlags{until: workflow.WatchUntilWaiting, timeout: "100ms", pollInterval: "10ms"})
	}()
	time.Sleep(20 * time.Millisecond)
	_ = database.Close()
	err := <-errCh
	if code := ExitCodeForError(err); code != 3 {
		t.Fatalf("post-resolution DB failure exit code=%d, want 3 (err=%v)", code, err)
	}
}

func TestWatchSuspendedBlocksUntilRealPark(t *testing.T) {
	a, taskUUID, _, svc := watchCLIFixture(t)
	var out bytes.Buffer
	cmd := testWatchCmd()
	cmd.SetOut(&out)
	done := make(chan error, 1)
	go func() {
		done <- runWatch(a, cmd, taskUUID, watchFlags{until: workflow.WatchUntilSuspended, timeout: "2s", pollInterval: "5ms"})
	}()
	time.Sleep(20 * time.Millisecond)
	if _, err := svc.Transition(taskUUID, "park", workflow.TransitionOptions{PrincipalRef: "agent:watch", Role: "coordinator"}); err != nil {
		t.Fatalf("Transition park: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("blocking watch: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("class=suspended")) {
		t.Fatalf("watch output = %q, want suspended class", out.String())
	}
}

func TestWatchFollowEmitsEventsAndSingleSummary(t *testing.T) {
	a, taskUUID, _, svc := watchCLIFixture(t)
	run, err := svc.StartRun(taskUUID, "coordinator", "agent:watch", workflow.StartRunOptions{})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if _, err := svc.FinishRun(run.ID, "completed", "done"); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	var out bytes.Buffer
	cmd := testWatchCmd()
	cmd.SetOut(&out)
	err = runWatch(a, cmd, run.ID, watchFlags{until: workflow.WatchUntilTerminal, follow: true, pollInterval: "1ms"})
	if err != nil {
		t.Fatalf("runWatch follow: %v", err)
	}

	lines := bytes.Split(bytes.TrimSpace(out.Bytes()), []byte("\n"))
	if len(lines) != 3 {
		t.Fatalf("follow emitted %d lines, want 3:\n%s", len(lines), out.String())
	}
	var event1, event2 struct {
		Type  string         `json:"type"`
		Event workflow.Event `json:"event"`
	}
	if err := json.Unmarshal(lines[0], &event1); err != nil {
		t.Fatalf("decode first event: %v", err)
	}
	if err := json.Unmarshal(lines[1], &event2); err != nil {
		t.Fatalf("decode second event: %v", err)
	}
	if event1.Type != "event" || event1.Event.Type != "workflow.run_started" {
		t.Fatalf("first line = %+v, want workflow.run_started event", event1)
	}
	if event2.Type != "event" || event2.Event.Type != "workflow.run_finished" {
		t.Fatalf("second line = %+v, want workflow.run_finished event", event2)
	}
	var summary watchSummary
	if err := json.Unmarshal(lines[2], &summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if summary.Type != "summary" || summary.Result != "met" || summary.Class != workflow.WatchClassSuccess || summary.ExitCode != 0 {
		t.Fatalf("summary = %+v, want one met/success/0 summary", summary)
	}
}

func watchCLIFixture(t *testing.T) (*app, string, *db.DB, *workflow.Service) {
	t.Helper()
	t.Setenv("WRKF_HOOK_CATALOG", explicitEmptyHookCatalog(t))
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "watch_cli.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	svc := workflow.NewService(database)

	tplPath := filepath.Join(dir, "watch_cli_test.json")
	if err := os.WriteFile(tplPath, []byte(watchCLITestTemplate), 0644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	if _, err := svc.InstallTemplate(tplPath, "agent:watch", nil); err != nil {
		t.Fatalf("InstallTemplate: %v", err)
	}

	actorUUID := "cccccccc-cccc-4ccc-cccc-000000000011"
	if _, err := database.Exec(`INSERT INTO actors (uuid, slug, role) VALUES (?, 'watch-cli-actor', 'system')`, actorUUID); err != nil {
		t.Fatalf("insert actor: %v", err)
	}
	containerUUID := "aaaaaaaa-aaaa-4aaa-aaaa-000000000011"
	if _, err := database.Exec(
		`INSERT INTO containers (uuid, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'watch-cli-project', 'Watch CLI Project', (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		containerUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert container: %v", err)
	}
	taskUUID := "bbbbbbbb-bbbb-4bbb-bbbb-000000000011"
	if _, err := database.Exec(
		`INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, kind, created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'watch-cli-task', 'Watch CLI Task', ?, 'open', 2, 'task', ?, ?)`,
		taskUUID, containerUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, "watch_cli_test@1", "agent:watch"); err != nil {
		t.Fatalf("AttachTask: %v", err)
	}
	api, opts, err := bootstrap.Server(database, &config.Config{DBPath: database.Path(), DefaultPrincipalRef: "agent:watch", AttachmentsMaxMB: 50})
	if err != nil {
		t.Fatalf("bootstrap.Server: %v", err)
	}
	transport, err := workrpcclient.NewInProcess(&bootstrap.Handle{API: api, Opts: opts}, workrpcclient.WrkfProfile)
	if err != nil {
		t.Fatalf("NewInProcess: %v", err)
	}
	t.Cleanup(func() { _ = transport.Close() })

	return &app{transport: transport}, taskUUID, database, svc
}

func testWatchCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	return cmd
}
