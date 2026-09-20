//go:build wrkq_local

package db

import (
	"path/filepath"
	"testing"
)

func TestEnvelopeProjectAffiliationMigrationPreservesLegacyRowsWithoutBackfill(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "pre-envelope-project-affiliation.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`CREATE TABLE schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
	)`); err != nil {
		t.Fatal(err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "000062_envelope_project_affiliation.sql" {
			continue
		}
		content, err := migrationsFS.ReadFile(filepath.Join("migrations", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := database.applyMigration(entry.Name(), content); err != nil {
			t.Fatalf("apply %s: %v", entry.Name(), err)
		}
	}
	if _, err := database.Exec(`
		INSERT INTO rooms (id, kind, opened_by_principal_ref, created_by_principal_ref, updated_by_principal_ref)
		VALUES ('R-90062', 'adhoc', 'agent:legacy', 'agent:legacy', 'agent:legacy');
		INSERT INTO envelopes (
			id, room_uuid, from_principal_ref, from_scope_ref, to_principal_ref, to_scope_ref,
			obligation, body, state, created_by_principal_ref, updated_by_principal_ref
		) VALUES (
			'EN-90062', (SELECT uuid FROM rooms WHERE id = 'R-90062'), 'agent:legacy',
			'legacy@old-slug:primary', 'agent:recipient', 'recipient@old-slug:primary',
			'reply_required', 'do not guess this history', 'pending', 'agent:legacy', 'agent:legacy'
		);`); err != nil {
		t.Fatal(err)
	}
	content, err := migrationsFS.ReadFile("migrations/000062_envelope_project_affiliation.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.applyMigration("000062_envelope_project_affiliation.sql", content); err != nil {
		t.Fatal(err)
	}
	var body string
	var fromProject, toProject any
	if err := database.QueryRow(`SELECT body, from_project_uuid, to_project_uuid FROM envelopes WHERE id = 'EN-90062'`).Scan(&body, &fromProject, &toProject); err != nil {
		t.Fatal(err)
	}
	if body != "do not guess this history" || fromProject != nil || toProject != nil {
		t.Fatalf("legacy envelope after migration = body=%q from=%v to=%v, want preserved/null/null", body, fromProject, toProject)
	}
	var indexed int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'envelopes_group_project_affiliation_idx'`).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if indexed != 1 {
		t.Fatalf("project affiliation index count = %d, want 1", indexed)
	}
}
