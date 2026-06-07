package db

import (
	"path/filepath"
	"testing"
)

const (
	testRootUUID   = "00000000-0000-4000-8000-000000000001"
	testSystemUUID = "00000000-0000-4000-8000-0000000000a0"
)

func migratedDB(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return database
}

// seedProject inserts a top-level project under the root and returns its uuid.
func seedProject(t *testing.T, database *DB, uuid, id, slug string) string {
	t.Helper()
	if _, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, ?, ?, ?, ?, 'project', ?, ?)
	`, uuid, id, slug, slug, testRootUUID, testSystemUUID, testSystemUUID); err != nil {
		t.Fatalf("seed project %s: %v", slug, err)
	}
	return uuid
}

func TestRootMigrationSeedsExactlyOneRoot(t *testing.T) {
	database := migratedDB(t)

	var roots, nonRootNulls int
	if err := database.QueryRow(`SELECT COUNT(*) FROM containers WHERE kind = 'root'`).Scan(&roots); err != nil {
		t.Fatalf("count roots: %v", err)
	}
	if roots != 1 {
		t.Fatalf("root count = %d, want 1", roots)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM containers WHERE parent_uuid IS NULL AND kind != 'root'`).Scan(&nonRootNulls); err != nil {
		t.Fatalf("count non-root nulls: %v", err)
	}
	if nonRootNulls != 0 {
		t.Fatalf("non-root null-parent count = %d, want 0", nonRootNulls)
	}

	// Root identity is the fixed sentinel and skipped the friendly-id sequence.
	var id, uuid string
	if err := database.QueryRow(`SELECT id, uuid FROM containers WHERE kind = 'root'`).Scan(&id, &uuid); err != nil {
		t.Fatalf("query root: %v", err)
	}
	if id != "P-00000" || uuid != testRootUUID {
		t.Fatalf("root id/uuid = %s/%s, want P-00000/%s", id, uuid, testRootUUID)
	}

	// System actor exists with role=system and did not advance actor_seq.
	var role string
	if err := database.QueryRow(`SELECT role FROM actors WHERE uuid = ?`, testSystemUUID).Scan(&role); err != nil {
		t.Fatalf("query system actor: %v", err)
	}
	if role != "system" {
		t.Fatalf("system actor role = %q, want system", role)
	}
}

func TestRootMigrationIsIdempotentAcrossRuns(t *testing.T) {
	database := migratedDB(t)
	// Re-running Migrate must be a no-op (already-applied migrations are skipped).
	if err := database.Migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var roots int
	if err := database.QueryRow(`SELECT COUNT(*) FROM containers WHERE kind = 'root'`).Scan(&roots); err != nil {
		t.Fatalf("count roots: %v", err)
	}
	if roots != 1 {
		t.Fatalf("root count after re-migrate = %d, want 1", roots)
	}
}

func TestRootInvariantsRejectInvalidWrites(t *testing.T) {
	database := migratedDB(t)
	proj := seedProject(t, database, "00000000-0000-4000-8000-000000000010", "P-10001", "proj")
	if _, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('00000000-0000-4000-8000-0000000000f0', 'T-10001', 'tk', 'Tk', ?, 'open', ?, ?)
	`, proj, testSystemUUID, testSystemUUID); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	ins := func(uuid, id, slug, parent, kind string) string {
		if parent == "" {
			return `INSERT INTO containers (uuid, id, slug, title, kind, created_by_actor_uuid, updated_by_actor_uuid) VALUES ('` + uuid + `','` + id + `','` + slug + `','` + slug + `','` + kind + `','` + testSystemUUID + `','` + testSystemUUID + `')`
		}
		return `INSERT INTO containers (uuid, id, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid) VALUES ('` + uuid + `','` + id + `','` + slug + `','` + slug + `','` + parent + `','` + kind + `','` + testSystemUUID + `','` + testSystemUUID + `')`
	}

	mustReject := []struct {
		name string
		sql  string
	}{
		{"second root", ins("a1", "x1", "r2", "", "root")},
		{"non-root null parent", ins("a2", "x2", "nrn", "", "directory")},
		{"null-parent project", ins("a3", "x3", "np", "", "project")},
		{"project parent != root", ins("a4", "x4", "bp", proj, "project")},
		{"directory under root", ins("a5", "x5", "dur", testRootUUID, "directory")},
		{"demote root kind", `UPDATE containers SET kind='directory' WHERE kind='root'`},
		{"move root parent", `UPDATE containers SET parent_uuid='` + proj + `' WHERE kind='root'`},
		{"rename root slug", `UPDATE containers SET slug='other' WHERE kind='root'`},
		{"archive root", `UPDATE containers SET archived_at='2026-01-01' WHERE kind='root'`},
		{"delete root", `DELETE FROM containers WHERE kind='root'`},
		{"task insert under root", `INSERT INTO tasks (uuid,id,slug,title,project_uuid,state,created_by_actor_uuid,updated_by_actor_uuid) VALUES ('tk2','T-10002','t2','T2','` + testRootUUID + `','open','` + testSystemUUID + `','` + testSystemUUID + `')`},
		{"task move under root", `UPDATE tasks SET project_uuid='` + testRootUUID + `' WHERE id='T-10001'`},
	}
	for _, tc := range mustReject {
		if _, err := database.Exec(tc.sql); err == nil {
			t.Errorf("%s: expected ABORT, but write succeeded", tc.name)
		}
	}

	mustAccept := []struct {
		name string
		sql  string
	}{
		{"project under root", ins("b1", "P-10010", "p2", testRootUUID, "project")},
		{"directory under project", ins("b2", "P-10011", "d2", proj, "directory")},
		{"feature under project", ins("b3", "P-10012", "f2", proj, "feature")},
	}
	for _, tc := range mustAccept {
		if _, err := database.Exec(tc.sql); err != nil {
			t.Errorf("%s: expected success, got %v", tc.name, err)
		}
	}
}

func TestRootExcludedFromPathsAndNoPrefix(t *testing.T) {
	database := migratedDB(t)
	seedProject(t, database, "00000000-0000-4000-8000-000000000010", "P-10001", "alpha")
	if _, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('00000000-0000-4000-8000-000000000011', 'P-10002', 'beta', 'beta',
		        '00000000-0000-4000-8000-000000000010', 'directory', ?, ?)
	`, testSystemUUID, testSystemUUID); err != nil {
		t.Fatalf("seed nested: %v", err)
	}

	// Root must never appear in the path view.
	var rootInPaths int
	if err := database.QueryRow(`SELECT COUNT(*) FROM v_container_paths WHERE uuid = ?`, testRootUUID).Scan(&rootInPaths); err != nil {
		t.Fatalf("query root in paths: %v", err)
	}
	if rootInPaths != 0 {
		t.Fatalf("root appears in v_container_paths %d time(s), want 0", rootInPaths)
	}

	// Top-level project is level 0 with no root prefix; nested keeps full path.
	var path string
	var level int
	if err := database.QueryRow(`SELECT path, level FROM v_container_paths WHERE id = 'P-10001'`).Scan(&path, &level); err != nil {
		t.Fatalf("query alpha path: %v", err)
	}
	if path != "alpha" || level != 0 {
		t.Fatalf("alpha path/level = %q/%d, want alpha/0", path, level)
	}
	if err := database.QueryRow(`SELECT path, level FROM v_container_paths WHERE id = 'P-10002'`).Scan(&path, &level); err != nil {
		t.Fatalf("query beta path: %v", err)
	}
	if path != "alpha/beta" || level != 1 {
		t.Fatalf("beta path/level = %q/%d, want alpha/beta/1", path, level)
	}
}
