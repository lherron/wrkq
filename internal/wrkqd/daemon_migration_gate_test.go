package wrkqd

import (
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// dbWithPendingMigration returns a fully migrated database whose newest
// migration has been un-recorded, so the current binary sees one pending
// migration — a DB that is behind the code serving it.
func dbWithPendingMigration(t *testing.T) string {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "pending.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		_ = database.Close()
		t.Fatalf("Migrate: %v", err)
	}
	applied, pending, err := database.MigrationStatus()
	if err != nil {
		_ = database.Close()
		t.Fatalf("MigrationStatus: %v", err)
	}
	if len(pending) != 0 || len(applied) == 0 {
		_ = database.Close()
		t.Fatalf("fixture precondition: applied=%d pending=%d", len(applied), len(pending))
	}
	newest := applied[len(applied)-1]
	if _, err := database.Exec("DELETE FROM schema_migrations WHERE version = ?", newest); err != nil {
		_ = database.Close()
		t.Fatalf("un-record migration: %v", err)
	}
	if err := database.RequiresMigrationError(); err == nil {
		_ = database.Close()
		t.Fatal("fixture precondition: expected a pending migration after un-recording")
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close fixture db: %v", err)
	}
	return dbPath
}

// freeAddr reserves a loopback port and releases it, so the test can prove
// nothing ever listens there.
func freeAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}
	return addr
}

// TestServeDaemonRefusesPendingMigrationBeforeExposingRPC pins the canonical
// host's half of wrkq.rpc.remote-transport-locator: migrations are owned by
// the server, so a wrkqd whose own embedded migrations are pending must refuse
// exposure outright. Remote clients rely on this readiness gate rather than
// trying to infer DB compatibility from migration names, which they cannot do
// — the canonical DB legitimately carries retired migration names that no
// current binary embeds.
func TestServeDaemonRefusesPendingMigrationBeforeExposingRPC(t *testing.T) {
	dbPath := dbWithPendingMigration(t)
	addr := freeAddr(t)

	err := ServeDaemon(DaemonOptions{Addr: addr, DBPath: dbPath, Token: "test-token"})
	if err == nil {
		t.Fatal("ServeDaemon exposed a daemon backed by a DB with a pending migration")
	}
	if !strings.Contains(err.Error(), "requires migration") {
		t.Fatalf("error = %q, want a migration-readiness refusal", err)
	}

	// The refusal must happen before the listener exists: no client can have
	// reached business dispatch on this endpoint.
	conn, dialErr := net.DialTimeout("tcp", addr, 250*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		t.Fatalf("daemon listener is accepting connections on %s despite the migration refusal", addr)
	}
}

// TestBootstrapOpenRefusesPendingMigration covers the other production RPC
// entrypoint: an opened workrpc handle must not be constructed over a DB that
// is behind its binary.
func TestBootstrapOpenRefusesPendingMigration(t *testing.T) {
	dbPath := dbWithPendingMigration(t)

	handle, err := bootstrap.Open(dbPath)
	if err == nil {
		_ = handle.Close()
		t.Fatal("bootstrap.Open built an RPC handle over a DB with a pending migration")
	}
	if !strings.Contains(err.Error(), "requires migration") {
		t.Fatalf("error = %q, want a migration-readiness refusal", err)
	}
}
