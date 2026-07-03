package rpccli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func TestCommentAddDashReadsStdin(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)

	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs([]string{
		"--db", dbPath,
		"--principal-ref", "agent:test-user",
		"comment", "add", taskID, "-",
	})
	body := "stdin-backed comment\nsecond line\n"
	cmd.SetIn(strings.NewReader(body))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("comment add - failed: %v\noutput:\n%s", err, out.String())
	}

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
	if stored != body {
		t.Fatalf("stored body mismatch:\nwant %q\ngot  %q", body, stored)
	}
}

func TestCommentAddMessageFlag(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)

	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs([]string{
		"--db", dbPath,
		"--principal-ref", "agent:test-user",
		"comment", "add", taskID, "-m", "message-backed comment",
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("comment add -m failed: %v\noutput:\n%s", err, out.String())
	}

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
	if stored != "message-backed comment" {
		t.Fatalf("stored body mismatch: %q", stored)
	}
}
