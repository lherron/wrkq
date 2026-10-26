package wrkqd

import (
	"path/filepath"
	"testing"
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

	stop := serveDaemon(t, DaemonOptions{Addr: freeAddr(t), DBPath: dbPath, Token: "test-token"})
	stop()
}
