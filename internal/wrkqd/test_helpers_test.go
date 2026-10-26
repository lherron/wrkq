package wrkqd

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/db"
)

func setupTestEnv(t *testing.T) (*db.DB, string) {
	t.Helper()
	catalogPath := filepath.Join(t.TempDir(), "empty-hook-catalog.json")
	if err := os.WriteFile(catalogPath, []byte(`{"schemaVersion":"wrkf.hook-catalog.v0","hooks":{}}`), 0o600); err != nil {
		t.Fatalf("write hook catalog: %v", err)
	}
	t.Setenv("WRKF_HOOK_CATALOG", catalogPath)
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("create test database: %v", err)
	}
	if err := database.Migrate(); err != nil {
		_ = database.Close()
		t.Fatalf("migrate test database: %v", err)
	}
	if _, err := database.Exec(`
		INSERT INTO actors (uuid, id, slug, display_name, role, created_at, updated_at)
		VALUES ('00000000-0000-0000-0000-000000000001', 'A-00001', 'test-user', 'Test User', 'human', datetime('now'), datetime('now'))
	`); err != nil {
		_ = database.Close()
		t.Fatalf("seed actor: %v", err)
	}
	if _, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, kind, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000002', 'P-00001', 'inbox', 'Inbox', (SELECT uuid FROM containers WHERE kind = 'root'), 'project', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`); err != nil {
		_ = database.Close()
		t.Fatalf("seed inbox: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database, dbPath
}

// serveDaemon runs ServeDaemon in the background until it is serving on
// opts.Addr and returns a stop func that interrupts it and requires a clean
// shutdown. Signal delivery is intercepted, so no real signal is involved.
func serveDaemon(t *testing.T, opts DaemonOptions) (stop func()) {
	t.Helper()
	sigCh := make(chan chan<- os.Signal, 1)
	oldNotify, oldStop := signalNotify, signalStop
	signalNotify = func(ch chan<- os.Signal) { sigCh <- ch }
	signalStop = func(chan<- os.Signal) {}
	t.Cleanup(func() { signalNotify, signalStop = oldNotify, oldStop })

	done := make(chan error, 1)
	go func() { done <- ServeDaemon(opts) }()

	var daemonSig chan<- os.Signal
	select {
	case daemonSig = <-sigCh:
	case err := <-done:
		t.Fatalf("ServeDaemon exited before serving: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("ServeDaemon did not start serving")
	}

	conn, err := net.DialTimeout("tcp", opts.Addr, time.Second)
	if err != nil {
		t.Fatalf("daemon not accepting connections on %s: %v", opts.Addr, err)
	}
	_ = conn.Close()

	return func() {
		t.Helper()
		daemonSig <- os.Interrupt
		if err := <-done; err != nil {
			t.Fatalf("ServeDaemon shutdown: %v", err)
		}
	}
}
