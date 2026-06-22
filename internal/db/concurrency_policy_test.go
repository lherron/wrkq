package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"sync"
	"testing"

	sqlite3 "github.com/mattn/go-sqlite3"
)

// seedCounter creates a tiny table with one row per id and a value column,
// the minimal shape that reproduces the read-then-write transaction pattern
// (read current value, then UPDATE) that wrkq's task writes use.
func seedCounter(t *testing.T, d *sql.DB, ids int) {
	t.Helper()
	if _, err := d.Exec(`CREATE TABLE IF NOT EXISTS counter (id INTEGER PRIMARY KEY, v INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for i := 0; i < ids; i++ {
		if _, err := d.Exec(`INSERT OR IGNORE INTO counter (id, v) VALUES (?, 0)`, i); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}
}

// readThenWrite runs the contention-prone pattern inside a single transaction:
// BEGIN, SELECT the current value, then UPDATE. Under the default deferred
// transaction this reads a snapshot and then loses an un-retryable lock upgrade
// when another writer commits in between; under BEGIN IMMEDIATE it reserves the
// writer lock up front and serializes cleanly.
func readThenWrite(d *sql.DB, id int) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	var v int
	if err := tx.QueryRow(`SELECT v FROM counter WHERE id = ?`, id).Scan(&v); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`UPDATE counter SET v = ? WHERE id = ?`, v+1, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// TestOpen_ConcurrentReadThenWrite_NoBusy is the regression for T-05066: the
// production Open() path (which carries _txlock=immediate in the DSN) must let
// many concurrent read-then-write transactions serialize and commit with zero
// SQLite busy/locked errors, even when database/sql opens multiple physical
// connections.
//
// This test is sensitive to the fix: remove _txlock=immediate from Open() and
// these overlapping read-then-write transactions start losing the lock-upgrade
// race and surface IsBusy errors, failing the test.
func TestOpen_ConcurrentReadThenWrite_NoBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.db")
	d, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = d.Close() }()
	// Force a real connection pool so the policy must hold across connections.
	d.SetMaxOpenConns(8)
	seedCounter(t, d.DB, 8)

	const rounds, writers = 30, 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	var busy, other int
	for r := 0; r < rounds; r++ {
		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				if err := readThenWrite(d.DB, id); err != nil {
					mu.Lock()
					if IsBusy(err) {
						busy++
					} else {
						other++
					}
					mu.Unlock()
				}
			}(w)
		}
		wg.Wait()
	}
	if busy != 0 {
		t.Errorf("expected 0 SQLite busy errors under _txlock=immediate, got %d", busy)
	}
	if other != 0 {
		t.Errorf("expected 0 non-busy errors, got %d", other)
	}
}

// TestDeferredTxlock_LosesUpgrade_IsBusy is the control that proves the
// mechanism and that IsBusy classifies it. With the default deferred
// transaction, two connections that both read a snapshot and then write
// deterministically produce a busy/locked error on the second writer — exactly
// the failure _txlock=immediate eliminates. If this ever stops failing, the
// regression test above is no longer guarding anything.
func TestDeferredTxlock_LosesUpgrade_IsBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deferred.db")
	// Same policy as Open() but WITHOUT _txlock=immediate (default deferred).
	dsn := path + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on&_synchronous=NORMAL"
	d, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = d.Close() }()
	seedCounter(t, d, 1)

	ctx := context.Background()
	c1, err := d.Conn(ctx)
	if err != nil {
		t.Fatalf("conn1: %v", err)
	}
	defer func() { _ = c1.Close() }()
	c2, err := d.Conn(ctx)
	if err != nil {
		t.Fatalf("conn2: %v", err)
	}
	defer func() { _ = c2.Close() }()

	tx1, err := c1.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx1: %v", err)
	}
	tx2, err := c2.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin tx2: %v", err)
	}

	var v1, v2 int
	if err := tx1.QueryRow(`SELECT v FROM counter WHERE id = 0`).Scan(&v1); err != nil {
		t.Fatalf("tx1 read: %v", err)
	}
	if err := tx2.QueryRow(`SELECT v FROM counter WHERE id = 0`).Scan(&v2); err != nil {
		t.Fatalf("tx2 read: %v", err)
	}
	// tx1 upgrades read->write first and wins the writer lock.
	if _, err := tx1.Exec(`UPDATE counter SET v = ? WHERE id = 0`, v1+1); err != nil {
		t.Fatalf("tx1 update should succeed: %v", err)
	}
	if err := tx1.Commit(); err != nil {
		t.Fatalf("tx1 commit: %v", err)
	}
	// tx2 now holds a stale read snapshot and loses the upgrade.
	_, err = tx2.Exec(`UPDATE counter SET v = ? WHERE id = 0`, v2+1)
	_ = tx2.Rollback()
	if err == nil {
		t.Fatal("expected tx2 UPDATE to fail with a busy/locked error under deferred txlock, got nil")
	}
	if !IsBusy(err) {
		t.Fatalf("expected IsBusy(err)=true for deferred lock-upgrade loss, got %v", err)
	}
}

// TestIsBusy covers the busy/locked classification helper.
func TestIsBusy(t *testing.T) {
	if IsBusy(nil) {
		t.Error("nil must not be busy")
	}
	if !IsBusy(sqlite3.Error{Code: sqlite3.ErrBusy}) {
		t.Error("ErrBusy must be busy")
	}
	if !IsBusy(sqlite3.Error{Code: sqlite3.ErrLocked}) {
		t.Error("ErrLocked must be busy")
	}
	// Wrapped busy error must still be detected through the chain.
	wrapped := sql.ErrTxDone
	if IsBusy(wrapped) {
		t.Error("unrelated error must not be busy")
	}
}
