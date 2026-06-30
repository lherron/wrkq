package rpccli

import (
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

const (
	seedTaskUUID = "00000000-2222-4000-8000-000000000001"
	seedTaskSlug = "rpccli-smoke-task"
	seedActor    = "agent:wrkq-system" // wrkq-system principal ref
	seedProject  = "00000000-1111-4000-8000-000000000001"
)

// migratedDBWithTask creates a fresh migrated database in a temp dir, seeds one
// project + task via SQL, and returns the db path and the task's T-XXXXX id.
// Seeding uses SQL directly so the id is deterministic; business reads under
// test still go through the RPC seam.
func migratedDBWithTask(t *testing.T) (dbPath, taskID string) {
	t.Helper()
	dbPath = t.TempDir() + "/wrkq.db"
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := database.Exec(
		`INSERT OR IGNORE INTO containers (uuid, slug, title, parent_uuid, kind,
		     created_by_principal_ref, updated_by_principal_ref)
		 VALUES (?, 'rpccli-test-proj', 'rpccli Test Project',
		     (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		seedProject, seedActor, seedActor,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	// Seed a richly-populated task so the seam smoke exercises every DTO field
	// the transport can carry — unicode, quotes, multiline text, labels, nested
	// meta, timestamps, attribution — not just a bare task. (Plain passthrough
	// rendering means the value here is proving faithful byte transport of rich
	// content, which a minimal task would not exercise.)
	if _, err := database.Exec(
		`INSERT OR IGNORE INTO tasks (uuid, slug, title, description, specification,
		     project_uuid, state, priority, kind, labels, meta,
		     start_at, due_at, completed_at, acknowledged_at,
		     created_by_principal_ref, updated_by_principal_ref)
		 VALUES (?, ?, 'rpccli smoke ✓ "task"', 'desc ✓ with "quotes"', 'spec line1'||char(10)||'line2',
		     ?, 'completed', 1, 'bug', '["alpha","beta"]', '{"nested":{"k":1},"arr":[1,2,3]}',
		     '2026-06-25T00:00:00Z', '2026-12-01T00:00:00Z', '2026-06-22T00:00:00Z', '2026-06-22T00:00:00Z',
		     ?, ?)`,
		seedTaskUUID, seedTaskSlug, seedProject, seedActor, seedActor,
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", seedTaskUUID).Scan(&taskID); err != nil {
		t.Fatalf("fetch task id: %v", err)
	}
	return dbPath, taskID
}

// inProcessTransport builds an in-process transport against dbPath, hermetically
// (no config.Load): it constructs the Handle from an explicit config so tests do
// not depend on the developer's ~/.config/wrkq.
func inProcessTransport(t *testing.T, dbPath string) Transport {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	cfg := &config.Config{DBPath: dbPath, AttachmentsMaxMB: 50}
	// Mirror production: pull the env-driven search host settings (search T-05114
	// tests set WRKQ_SEARCH_* in their env) so the in-process server's search
	// config matches what `wrkq rpc --stdio` would build from config.Load().
	if loaded, lerr := config.Load(); lerr == nil {
		cfg.Search = loaded.Search
	}
	api, opts, err := bootstrap.Server(database, cfg)
	if err != nil {
		_ = database.Close()
		t.Fatalf("bootstrap.Server: %v", err)
	}
	tr, err := NewInProcess(&bootstrap.Handle{DB: database, API: api, Opts: opts})
	if err != nil {
		t.Fatalf("NewInProcess: %v", err)
	}
	return tr
}
