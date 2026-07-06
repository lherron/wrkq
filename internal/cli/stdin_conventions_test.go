package cli

import (
	"bytes"
	"database/sql"
	"os"
	"strings"
	"testing"
)

func TestLegacyCommentAddMessageFlagReadsStdinAndFile(t *testing.T) {
	database, dbPath := setupTestEnv(t)
	seedStdinConventionTask(t, database)
	setStdinConventionEnv(t, dbPath)

	run := func(args []string, stdin string) {
		t.Helper()
		resetLegacyStdinConventionGlobals()
		cmd := rootCmd
		cmd.SetArgs(args)
		cmd.SetIn(strings.NewReader(stdin))
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("command failed: %v\noutput:\n%s", err, out.String())
		}
		cmd.SetIn(os.Stdin)
	}

	stdinBody := "legacy stdin-backed message"
	run([]string{"comment", "add", "T-00001", "-m", "-"}, stdinBody)
	assertLegacyLatestCommentBody(t, database, stdinBody)

	bodyPath := t.TempDir() + "/comment.md"
	fileBody := "legacy file-backed message"
	if err := os.WriteFile(bodyPath, []byte(fileBody), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	run([]string{"comment", "add", "T-00001", "-m", "@" + bodyPath}, "")
	assertLegacyLatestCommentBody(t, database, fileBody)
}

func TestLegacySetRejectsSecondStdinConsumer(t *testing.T) {
	database, dbPath := setupTestEnv(t)
	seedStdinConventionTask(t, database)
	setStdinConventionEnv(t, dbPath)
	resetLegacyStdinConventionGlobals()

	cmd := rootCmd
	cmd.SetArgs([]string{"set", "T-00001", "--description", "-", "--specification", "-"})
	cmd.SetIn(strings.NewReader("description from stdin"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	cmd.SetIn(os.Stdin)

	if err == nil {
		t.Fatalf("expected stdin claim error")
	}
	if !strings.Contains(err.Error(), "stdin already claimed by --description") {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out.String())
	}
}

func TestLegacySetMetaFileDashReadsStdin(t *testing.T) {
	database, dbPath := setupTestEnv(t)
	seedStdinConventionTask(t, database)
	setStdinConventionEnv(t, dbPath)
	resetLegacyStdinConventionGlobals()

	cmd := rootCmd
	cmd.SetArgs([]string{"set", "T-00001", "--meta-file", "-"})
	cmd.SetIn(strings.NewReader(`{"from":"stdin"}`))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("set --meta-file - failed: %v\noutput:\n%s", err, out.String())
	}
	cmd.SetIn(os.Stdin)

	var meta string
	if err := database.QueryRow(`SELECT meta FROM tasks WHERE uuid = 'task-uuid-1'`).Scan(&meta); err != nil {
		t.Fatalf("query meta: %v", err)
	}
	if meta != `{"from":"stdin"}` {
		t.Fatalf("meta=%q", meta)
	}
}

func TestLegacyRmStdinSelectorsAreTrimmed(t *testing.T) {
	database, dbPath := setupTestEnv(t)
	seedStdinConventionTask(t, database)
	setStdinConventionEnv(t, dbPath)
	resetLegacyStdinConventionGlobals()

	cmd := rootCmd
	cmd.SetArgs([]string{"rm", "-", "--dry-run", "--json"})
	cmd.SetIn(strings.NewReader("  T-00001  \n\n"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm stdin dry-run failed: %v\noutput:\n%s", err, out.String())
	}
	cmd.SetIn(os.Stdin)
	if !strings.Contains(out.String(), "T-00001") {
		t.Fatalf("dry-run output missing task id:\n%s", out.String())
	}
}

func resetLegacyStdinConventionGlobals() {
	commentAddMessage = ""
	commentAddFile = ""
	commentAddMeta = ""
	setDescription = ""
	setSpecification = ""
	setMeta = ""
	setMetaFile = ""
	setState = ""
	setPriority = 0
	setTitle = ""
	setSlug = ""
	setLabels = ""
	setDueAt = ""
	setStartAt = ""
	setKind = ""
	setParentTask = ""
	setParentID = ""
	setAssignee = ""
	setRequestedBy = ""
	setAssignedProject = ""
	setResolution = ""
	setCausedBy = causedByUnset
}

func seedStdinConventionTask(t *testing.T, database interface {
	Exec(string, ...interface{}) (sql.Result, error)
}) {
	t.Helper()
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('task-uuid-1', 'T-00001', 'stdin-task', 'Stdin Task', '00000000-0000-0000-0000-000000000002', 'open', 2, 'Task body', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
}

func setStdinConventionEnv(t *testing.T, dbPath string) {
	t.Helper()
	t.Setenv("WRKQ_DB_PATH", dbPath)
	t.Setenv("WRKQ_PRINCIPAL_REF", "agent:test-user")
}

func assertLegacyLatestCommentBody(t *testing.T, database interface {
	QueryRow(string, ...interface{}) *sql.Row
}, want string) {
	t.Helper()
	var stored string
	err := database.QueryRow(`
		SELECT body
		FROM comments
		WHERE task_uuid = 'task-uuid-1'
		ORDER BY id DESC
		LIMIT 1
	`).Scan(&stored)
	if err != nil {
		t.Fatalf("query stored comment body: %v", err)
	}
	if stored != want {
		t.Fatalf("stored body mismatch:\nwant %q\ngot  %q", want, stored)
	}
}
