package db

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func openPrePresentationInputIDFixture(t *testing.T) *DB {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "pre-presentation-input-id.db"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(`CREATE TABLE schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
	)`); err != nil {
		t.Fatalf("create migration ledger: %v", err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if name == "000054_presentation_input_id.sql" {
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
	return database
}

func TestPresentationInputIDMigrationPreservesExistingRowsWithoutBackfill(t *testing.T) {
	database := openPrePresentationInputIDFixture(t)
	const roomUUID = "10000000-0000-4000-8000-000000000054"
	const envelopeUUID = "20000000-0000-4000-8000-000000000054"
	if _, err := database.Exec(`
		INSERT INTO rooms (uuid, kind, opened_by_principal_ref, created_by_principal_ref, updated_by_principal_ref)
		VALUES (?, 'adhoc', 'agent:legacy', 'agent:legacy', 'agent:legacy');
		INSERT INTO envelopes (
			uuid, id, room_uuid, from_principal_ref, to_scope_ref, to_principal_ref,
			obligation, body, state, created_by_principal_ref, updated_by_principal_ref
		) VALUES (?, 'EN-9054', ?, 'agent:legacy', 'cody@wrkq:primary', 'agent:cody',
			'reply_required', 'existing receipt', 'presented', 'agent:legacy', 'agent:legacy');
		INSERT INTO envelope_presentations (
			envelope_uuid, room_uuid, member_ref, runtime_id, delivery_outcome, presented_by_principal_ref
		) VALUES (?, ?, 'cody@wrkq:primary', 'runtime-before', 'kicker', 'agent:hrc')
	`, roomUUID, envelopeUUID, roomUUID, envelopeUUID, roomUUID); err != nil {
		t.Fatalf("seed existing presentation: %v", err)
	}
	content, err := migrationsFS.ReadFile(filepath.Join("migrations", "000054_presentation_input_id.sql"))
	if err != nil {
		t.Fatalf("read inputId migration: %v", err)
	}
	if err := database.applyMigration("000054_presentation_input_id.sql", content); err != nil {
		t.Fatalf("apply inputId migration: %v", err)
	}
	var count int
	var inputID any
	if err := database.QueryRow(`SELECT COUNT(*), input_id FROM envelope_presentations WHERE envelope_uuid = ?`, envelopeUUID).Scan(&count, &inputID); err != nil {
		t.Fatalf("read preserved presentation: %v", err)
	}
	if count != 1 || inputID != nil {
		t.Fatalf("existing presentation after migration: count=%d input_id=%v, want 1/null", count, inputID)
	}
}
