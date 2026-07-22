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

func TestCommentAddWritesCreatedEvent(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)

	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs([]string{
		"--db", dbPath,
		"--principal-ref", "agent:test-user",
		"comment", "add", taskID, "-m", "event-backed comment",
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("comment add failed: %v\noutput:\n%s", err, out.String())
	}

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	var eventType string
	err = database.QueryRow(`
		SELECT e.event_type
		FROM event_log e
		JOIN comments c ON c.uuid = e.resource_uuid
		WHERE c.task_uuid = ? AND c.body = ?
	`, seedTaskUUID, "event-backed comment").Scan(&eventType)
	if err != nil {
		t.Fatalf("query comment event: %v", err)
	}
	if eventType != "comment.created" {
		t.Fatalf("event_type = %q, want comment.created", eventType)
	}
}
