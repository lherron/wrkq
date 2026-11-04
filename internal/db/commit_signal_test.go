package db

import (
	"path/filepath"
	"testing"
	"time"
)

func TestEventCommitSignal(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "signal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
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

func TestChangeSignalCoalescesCommitWaitBursts(t *testing.T) {
	var signal ChangeSignal
	seq, changed := signal.CoalescedSnapshot()
	for i := 0; i < 100; i++ {
		signal.Notify()
	}
	current, same := signal.CoalescedSnapshot()
	if current != seq+100 || same != changed {
		t.Fatal("sequence must advance immediately without broadcasting the burst")
	}
	select {
	case <-changed:
		t.Fatal("burst broadcast before coalescing window")
	default:
	}
	select {
	case <-changed:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("coalesced broadcast too late")
	}
	_, next := signal.CoalescedSnapshot()
	select {
	case <-next:
		t.Fatal("extra broadcast without a new commit")
	case <-time.After(120 * time.Millisecond):
	}
}
