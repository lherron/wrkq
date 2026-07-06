package rpccli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func TestCommentAddMessageFlagReadsStdinAndFile(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)

	run := func(args []string, stdin string) {
		t.Helper()
		cmd := NewRootCmdFor("wrkq")
		cmd.SetArgs(args)
		cmd.SetIn(strings.NewReader(stdin))
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("command failed: %v\noutput:\n%s", err, out.String())
		}
	}

	stdinBody := "stdin-backed message\n"
	run([]string{"--db", dbPath, "--principal-ref", "agent:test-user", "comment", "add", taskID, "-m", "-"}, stdinBody)
	assertLatestCommentBody(t, dbPath, stdinBody)

	bodyPath := t.TempDir() + "/comment.md"
	fileBody := "file-backed message\nsecond line\n"
	if err := os.WriteFile(bodyPath, []byte(fileBody), 0o644); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	run([]string{"--db", dbPath, "--principal-ref", "agent:test-user", "comment", "add", taskID, "-m", "@" + bodyPath}, "")
	assertLatestCommentBody(t, dbPath, fileBody)
}

func TestSetRejectsSecondStdinConsumer(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)

	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs([]string{
		"--db", dbPath,
		"--principal-ref", "agent:test-user",
		"set", taskID,
		"--description", "-",
		"--specification", "-",
	})
	cmd.SetIn(strings.NewReader("description from stdin"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected stdin claim error")
	}
	if !strings.Contains(err.Error(), "stdin already claimed by --description") {
		t.Fatalf("unexpected error: %v\noutput:\n%s", err, out.String())
	}
}

func TestSetMetaFileDashReadsStdin(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)

	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs([]string{
		"--db", dbPath,
		"--principal-ref", "agent:test-user",
		"set", taskID,
		"--meta-file", "-",
	})
	cmd.SetIn(strings.NewReader(`{"from":"stdin"}`))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("set --meta-file - failed: %v\noutput:\n%s", err, out.String())
	}

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var meta string
	if err := database.QueryRow(`SELECT meta FROM tasks WHERE uuid = ?`, seedTaskUUID).Scan(&meta); err != nil {
		t.Fatalf("query meta: %v", err)
	}
	if meta != `{"from":"stdin"}` {
		t.Fatalf("meta=%q", meta)
	}
}

func TestRmStdinSelectorsAreTrimmed(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)

	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs([]string{"--db", dbPath, "rm", "-", "--dry-run", "--json"})
	cmd.SetIn(strings.NewReader("  " + taskID + "  \n\n"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("rm stdin dry-run failed: %v\noutput:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), taskID) {
		t.Fatalf("dry-run output missing task id %s:\n%s", taskID, out.String())
	}
}

func assertLatestCommentBody(t *testing.T, dbPath, want string) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var stored string
	err = database.QueryRow(`
		SELECT body
		FROM comments
		WHERE task_uuid = ?
		ORDER BY id DESC
		LIMIT 1
	`, seedTaskUUID).Scan(&stored)
	if err != nil {
		t.Fatalf("query stored comment body: %v", err)
	}
	if stored != want {
		t.Fatalf("stored body mismatch:\nwant %q\ngot  %q", want, stored)
	}
}
