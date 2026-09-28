package wrkqd

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestServeDaemonWithDBFlagStaysUpUnderConflictingDBEnv pins the fleet
// daemon's shape (T-08302): wrkqd started with --db must serve even when the
// environment carries a WRKQ_DB_PATH that loses to WRKQ_DB. The explicit --db
// moots that conflict; only a caller relying on the env pair is refused.
func TestServeDaemonWithDBFlagStaysUpUnderConflictingDBEnv(t *testing.T) {
	database, _ := setupTestEnv(t)
	dbPath := database.Path()
	_ = database.Close()

	t.Setenv("WRKQ_DB", "rpc://127.0.0.1:1")
	t.Setenv("WRKQ_DB_PATH", filepath.Join(t.TempDir(), "stale.db"))
	t.Setenv("WRKQ_DB_PATH_FILE", "")

	sigCh := make(chan chan<- os.Signal, 1)
	oldNotify, oldStop := signalNotify, signalStop
	signalNotify = func(ch chan<- os.Signal) { sigCh <- ch }
	signalStop = func(chan<- os.Signal) {}
	t.Cleanup(func() { signalNotify, signalStop = oldNotify, oldStop })

	addr := freeAddr(t)
	done := make(chan error, 1)
	go func() {
		done <- ServeDaemon(DaemonOptions{Addr: addr, DBPath: dbPath, Token: "test-token"})
	}()

	var daemonSig chan<- os.Signal
	select {
	case daemonSig = <-sigCh:
	case err := <-done:
		t.Fatalf("ServeDaemon exited before serving: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("ServeDaemon did not start serving")
	}

	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("daemon not accepting connections on %s: %v", addr, err)
	}
	_ = conn.Close()

	daemonSig <- os.Interrupt
	if err := <-done; err != nil {
		t.Fatalf("ServeDaemon shutdown: %v", err)
	}
}
