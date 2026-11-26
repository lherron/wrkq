package wrkqd

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
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
	stopDaemon := startDaemonForTest(t, opts)
	conn, err := net.DialTimeout("tcp", opts.Addr, time.Second)
	if err != nil {
		t.Fatalf("daemon not accepting connections on %s: %v", opts.Addr, err)
	}
	_ = conn.Close()
	return func() {
		t.Helper()
		if file := stopDaemon(os.Interrupt); file != nil {
			_ = file.Close()
			t.Fatal("interrupt returned a handoff listener")
		}
	}
}

func startDaemonForTest(t *testing.T, opts DaemonOptions) func(os.Signal) *os.File {
	t.Helper()
	stdout := os.Stdout
	oldNotify, oldStop := signalNotify, signalStop
	ready := make(chan chan<- os.Signal, 1)
	signalNotify = func(ch chan<- os.Signal) { ready <- ch }
	signalStop = func(chan<- os.Signal) {}
	type result struct {
		file *os.File
		err  error
	}
	done := make(chan result, 1)
	go func() {
		file, err := ServeDaemon(opts)
		done <- result{file, err}
	}()
	var signals chan<- os.Signal
	select {
	case signals = <-ready:
	case result := <-done:
		t.Fatalf("ServeDaemon exited before serving: %v", result.err)
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not start")
	}
	stopped := false
	stop := func(sig os.Signal) *os.File {
		t.Helper()
		signals <- sig
		result := <-done
		stopped = true
		os.Stdout, signalNotify, signalStop = stdout, oldNotify, oldStop
		if result.err != nil {
			t.Fatal(result.err)
		}
		return result.file
	}
	t.Cleanup(func() {
		if !stopped {
			if file := stop(syscall.SIGTERM); file != nil {
				_ = file.Close()
			}
		}
	})
	return stop
}
