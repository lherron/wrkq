package db

import (
	"path/filepath"
	"testing"
)

func TestEventCommitSignal(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "signal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("CREATE TABLE event_log (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	seq, first := database.EventCommits.Snapshot()
	_, second := database.EventCommits.Snapshot()
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO event_log DEFAULT VALUES"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first:
		t.Fatal("signal before commit")
	default:
	}
	if err := database.CommitEvents(tx); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []<-chan struct{}{first, second} {
		select {
		case <-ch:
		default:
			t.Fatal("commit did not broadcast to every waiter")
		}
	}
	next, pending := database.EventCommits.Snapshot()
	if next != seq+1 {
		t.Fatalf("sequence = %d, want %d", next, seq+1)
	}
	tx, err = database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO event_log DEFAULT VALUES"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := database.CommitEvents(tx); err == nil {
		t.Fatal("commit after rollback succeeded")
	}
	select {
	case <-pending:
		t.Fatal("rollback/failed commit signalled")
	default:
	}
	for i := 0; i < 1000; i++ {
		database.EventCommits.Notify()
	}
	next, pending = database.EventCommits.Snapshot()
	if next != seq+1001 {
		t.Fatalf("burst sequence = %d", next)
	}
	select {
	case <-pending:
		t.Fatal("burst left queued notifications")
	default:
	}
}
