package events

import (
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
)

func TestAfterCommitOnlySignalsLoggedEvents(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	if _, err := database.Exec(`CREATE TABLE event_log (id INTEGER PRIMARY KEY, timestamp TEXT DEFAULT CURRENT_TIMESTAMP, principal_ref TEXT, scope_ref TEXT, resource_type TEXT, resource_uuid TEXT, event_type TEXT, etag INTEGER, payload TEXT)`); err != nil {
		t.Fatal(err)
	}
	writer := NewWriter(database.DB)
	_, changed := database.EventCommits.Snapshot()
	writer.RunAfterCommit(database)
	select {
	case <-changed:
		t.Fatal("read-only/no-op transaction signalled")
	default:
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.LogEvent(tx, &domain.Event{ResourceType: "task", EventType: "task.created"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
		t.Fatal("event signalled before commit")
	default:
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	writer.RunAfterCommit(database)
	select {
	case <-changed:
	default:
		t.Fatal("committed event did not signal")
	}
	_, changed = database.EventCommits.Snapshot()
	writer.RunAfterCommit(database)
	select {
	case <-changed:
		t.Fatal("post-commit queue signalled twice")
	default:
	}
}

// The event gate suppresses only the notification, never independent post-commit
// callbacks such as the promise.resolved webhook.
func TestAfterCommitCallbacksWithoutLoggedEvents(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "callbacks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	writer := NewWriter(database.DB)
	_, changed := database.EventCommits.Snapshot()
	var calls []int
	for _, ordinal := range []int{1, 2} {
		ordinal := ordinal
		writer.AfterCommit(func(got *db.DB) {
			if got != database {
				t.Fatal("callback received another database")
			}
			calls = append(calls, ordinal)
		})
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatal("callback ran before commit")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	writer.RunAfterCommit(database)
	if len(calls) != 2 || calls[0] != 1 || calls[1] != 2 {
		t.Fatalf("callbacks = %v, want [1 2] without logged events", calls)
	}
	writer.RunAfterCommit(database)
	if len(calls) != 2 {
		t.Fatalf("callbacks repeated: %v", calls)
	}
	select {
	case <-changed:
		t.Fatal("callbacks without events emitted a commit notification")
	default:
	}
}
