package db

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// A read transaction must not wait on the writer lock (T-09997). go-sqlite3
// runs BeginTx(ReadOnly) as the main pool's BEGIN IMMEDIATE, so with a writer
// holding the lock such a "read" sat out busy_timeout; BeginRead must not.
func TestBeginReadDoesNotTakeTheWriterLock(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "read.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = d.Close() }()
	seedCounter(t, d.DB, 1)

	writer, err := d.Begin() // BEGIN IMMEDIATE: holds the writer lock
	if err != nil {
		t.Fatalf("begin writer: %v", err)
	}
	defer func() { _ = writer.Rollback() }()
	if _, err := writer.Exec(`UPDATE counter SET v = 7 WHERE id = 0`); err != nil {
		t.Fatalf("write: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	tx, err := d.BeginRead(ctx)
	if err != nil {
		t.Fatalf("begin read under a held writer lock: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	var v int
	if err := tx.QueryRowContext(ctx, `SELECT v FROM counter WHERE id = 0`).Scan(&v); err != nil {
		t.Fatalf("read under a held writer lock: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("read waited %s on the writer lock", elapsed)
	}
	if v != 0 {
		t.Fatalf("read saw uncommitted value %d, want the committed snapshot 0", v)
	}
}

func TestBeginReadRefusesWrites(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "read.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = d.Close() }()
	seedCounter(t, d.DB, 1)
	tx, err := d.BeginRead(context.Background())
	if err != nil {
		t.Fatalf("begin read: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE counter SET v = 1 WHERE id = 0`); err == nil {
		t.Fatal("write inside a read transaction succeeded; the read pool must be query_only")
	}
}
