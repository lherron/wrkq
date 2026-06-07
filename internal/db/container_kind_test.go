package db

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestContainerDirectoryKindDefaultAndProjectRootInvariant(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = database.Close() }()

	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Migration 000024 seeds exactly one path-invisible root container.
	const rootUUID = "00000000-0000-4000-8000-000000000001"
	var rootCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM containers WHERE kind = 'root'`).Scan(&rootCount); err != nil {
		t.Fatalf("count roots: %v", err)
	}
	if rootCount != 1 {
		t.Fatalf("root count=%d, want 1", rootCount)
	}

	if _, err := database.Exec(`
		INSERT INTO actors (uuid, id, slug, display_name, role)
		VALUES ('00000000-0000-4000-8000-000000000002', 'A-10001', 'tester', 'Tester', 'human')
	`); err != nil {
		t.Fatalf("insert actor: %v", err)
	}

	// A project must be a child of the root.
	if _, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('00000000-0000-4000-8000-000000000010', 'P-10001', 'proj', 'Proj', ?, 'project',
		        '00000000-0000-4000-8000-000000000002', '00000000-0000-4000-8000-000000000002')
	`, rootUUID); err != nil {
		t.Fatalf("project insert under root failed: %v", err)
	}

	// A nested container defaults to kind=directory.
	if _, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('00000000-0000-4000-8000-000000000011', 'P-10002', 'nested', 'Nested',
		        '00000000-0000-4000-8000-000000000010',
		        '00000000-0000-4000-8000-000000000002', '00000000-0000-4000-8000-000000000002')
	`); err != nil {
		t.Fatalf("nested directory insert failed: %v", err)
	}
	var kind string
	if err := database.QueryRow(`SELECT kind FROM containers WHERE id = 'P-10002'`).Scan(&kind); err != nil {
		t.Fatalf("query kind: %v", err)
	}
	if kind != "directory" {
		t.Fatalf("default kind=%q, want directory", kind)
	}

	// A project with a NULL parent is rejected: only the root may be null-parent.
	if _, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('00000000-0000-4000-8000-000000000012', 'P-10003', 'rootless', 'Rootless', 'project',
		        '00000000-0000-4000-8000-000000000002', '00000000-0000-4000-8000-000000000002')
	`); err == nil {
		t.Fatal("expected null-parent project insert to fail")
	}

	// A nested project (parent != root) is rejected.
	if _, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('00000000-0000-4000-8000-000000000013', 'P-10004', 'nestedproj', 'NestedProj',
		        '00000000-0000-4000-8000-000000000010', 'project',
		        '00000000-0000-4000-8000-000000000002', '00000000-0000-4000-8000-000000000002')
	`); err == nil {
		t.Fatal("expected nested project insert to fail")
	}
}

func TestContainerDirectoryKindMigrationPreservesLegacyNestedProjectTasks(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = database.Close() }()

	if _, err := database.Exec(`
		CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		)
	`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if name >= "000017_container_directory_kind.sql" {
			break
		}
		content, err := migrationsFS.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := database.applyMigration(name, content); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}

	if _, err := database.Exec(`
		INSERT INTO actors (uuid, id, slug, display_name, role)
		VALUES ('00000000-0000-4000-8000-000000000001', 'A-00001', 'tester', 'Tester', 'human');

		INSERT INTO containers (uuid, id, slug, title, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('00000000-0000-4000-8000-000000000010', 'P-00001', 'root', 'Root',
		        '00000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000001');

		INSERT INTO containers (uuid, id, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('00000000-0000-4000-8000-000000000011', 'P-00002', 'legacy', 'Legacy',
		        '00000000-0000-4000-8000-000000000010', 'project',
		        '00000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000001');

		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('00000000-0000-4000-8000-000000000012', 'T-00001', 'legacy-task', 'Legacy Task',
		        '00000000-0000-4000-8000-000000000011', 'open',
		        '00000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000001');
	`); err != nil {
		t.Fatalf("insert legacy data: %v", err)
	}

	content, err := migrationsFS.ReadFile("migrations/000017_container_directory_kind.sql")
	if err != nil {
		t.Fatalf("read 000017: %v", err)
	}
	if err := database.applyMigration("000017_container_directory_kind.sql", content); err != nil {
		t.Fatalf("apply 000017: %v", err)
	}

	var taskCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM tasks WHERE id = 'T-00001'`).Scan(&taskCount); err != nil {
		t.Fatalf("count task: %v", err)
	}
	if taskCount != 1 {
		t.Fatalf("task count=%d, want 1", taskCount)
	}

	var kind string
	var nested bool
	if err := database.QueryRow(`SELECT kind, parent_uuid IS NOT NULL FROM containers WHERE id = 'P-00002'`).Scan(&kind, &nested); err != nil {
		t.Fatalf("query legacy container: %v", err)
	}
	if kind != "project" || !nested {
		t.Fatalf("legacy container kind/nested=%q/%v, want project/true", kind, nested)
	}

	var defaultKind string
	if err := database.QueryRow(`SELECT dflt_value FROM pragma_table_info('containers') WHERE name = 'kind'`).Scan(&defaultKind); err != nil {
		t.Fatalf("query kind default: %v", err)
	}
	if defaultKind != "'directory'" {
		t.Fatalf("kind default=%q, want 'directory'", defaultKind)
	}
}

func TestFoldMiscContainerKindMigrationReclassifiesAndRejectsMisc(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = database.Close() }()

	if _, err := database.Exec(`
		CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		)
	`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if name >= "000018_fold_misc_container_kind.sql" {
			break
		}
		content, err := migrationsFS.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := database.applyMigration(name, content); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}

	if _, err := database.Exec(`
		INSERT INTO actors (uuid, id, slug, display_name, role)
		VALUES ('00000000-0000-4000-8000-000000000001', 'A-00001', 'tester', 'Tester', 'human');

		INSERT INTO containers (uuid, id, slug, title, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('00000000-0000-4000-8000-000000000010', 'P-00001', 'misc-root', 'Misc Root', 'misc',
		        '00000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000001');
	`); err != nil {
		t.Fatalf("insert legacy misc container: %v", err)
	}

	content, err := migrationsFS.ReadFile("migrations/000018_fold_misc_container_kind.sql")
	if err != nil {
		t.Fatalf("read 000018: %v", err)
	}
	if err := database.applyMigration("000018_fold_misc_container_kind.sql", content); err != nil {
		t.Fatalf("apply 000018: %v", err)
	}

	var kind string
	if err := database.QueryRow(`SELECT kind FROM containers WHERE id = 'P-00001'`).Scan(&kind); err != nil {
		t.Fatalf("query migrated kind: %v", err)
	}
	if kind != "directory" {
		t.Fatalf("migrated kind=%q, want directory", kind)
	}

	_, err = database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('00000000-0000-4000-8000-000000000011', 'P-00002', 'new-misc', 'New Misc', 'misc',
		        '00000000-0000-4000-8000-000000000001', '00000000-0000-4000-8000-000000000001')
	`)
	if err == nil {
		t.Fatal("expected misc insert to fail")
	}
	if !strings.Contains(err.Error(), "project, directory, feature, or area") {
		t.Fatalf("unexpected misc insert error: %v", err)
	}

	_, err = database.Exec(`UPDATE containers SET kind = 'misc' WHERE id = 'P-00001'`)
	if err == nil {
		t.Fatal("expected misc update to fail")
	}
	if !strings.Contains(err.Error(), "project, directory, feature, or area") {
		t.Fatalf("unexpected misc update error: %v", err)
	}
}
