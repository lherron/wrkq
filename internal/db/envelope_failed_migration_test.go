//go:build wrkq_local

package db

import (
	"path/filepath"
	"testing"
)

func TestEnvelopeFailedMigrationPreservesRowsAndClassifiesDeadAsLegacy(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "wrkq.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = database.Close() }()
	if _, err := database.Exec(`CREATE TABLE schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
	)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() >= "000057_envelope_failed_state.sql" {
			continue
		}
		content, rerr := migrationsFS.ReadFile(filepath.Join("migrations", entry.Name()))
		if rerr != nil {
			t.Fatalf("read %s: %v", entry.Name(), rerr)
		}
		if aerr := database.applyMigration(entry.Name(), content); aerr != nil {
			t.Fatalf("apply %s: %v", entry.Name(), aerr)
		}
	}
	if _, err := database.Exec(`INSERT INTO rooms (
		id, kind, opened_by_principal_ref, created_by_principal_ref, updated_by_principal_ref
	) VALUES ('R-99997', 'adhoc', 'agent:cody', 'agent:cody', 'agent:cody')`); err != nil {
		t.Fatalf("seed room: %v", err)
	}
	var roomUUID string
	if err := database.QueryRow(`SELECT uuid FROM rooms WHERE id = 'R-99997'`).Scan(&roomUUID); err != nil {
		t.Fatalf("read room: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO envelopes (
		id, room_uuid, from_principal_ref, to_principal_ref, obligation, body, state,
		terminal_actor, terminal_at, created_by_principal_ref, updated_by_principal_ref
	) VALUES ('EN-99997', ?, 'agent:cody', 'agent:clod', 'reply_required', 'legacy',
		'dead', 'agent:hrc', '2026-08-29T00:00:00Z', 'agent:cody', 'agent:hrc')`, roomUUID); err != nil {
		t.Fatalf("seed envelope: %v", err)
	}
	var envelopeUUID string
	if err := database.QueryRow(`SELECT uuid FROM envelopes WHERE id = 'EN-99997'`).Scan(&envelopeUUID); err != nil {
		t.Fatalf("read envelope: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO envelope_presentations (
		envelope_uuid, room_uuid, member_ref, runtime_id, presented_by_principal_ref
	) VALUES (?, ?, 'clod@wrkq:primary', 'rt-legacy', 'agent:hrc')`, envelopeUUID, roomUUID); err != nil {
		t.Fatalf("seed presentation: %v", err)
	}
	content, err := migrationsFS.ReadFile("migrations/000057_envelope_failed_state.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if err := database.applyMigration("000057_envelope_failed_state.sql", content); err != nil {
		t.Fatalf("apply migration: %v", err)
	}
	var state, reason string
	if err := database.QueryRow(`SELECT state, failure_reason FROM envelopes WHERE id = 'EN-99997'`).Scan(&state, &reason); err != nil {
		t.Fatalf("read migrated envelope: %v", err)
	}
	if state != "failed" || reason != "legacy" {
		t.Fatalf("migrated envelope = %s{%s}, want failed{legacy}", state, reason)
	}
	var rounds int
	if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('envelopes') WHERE name = 'round_count'`).Scan(&rounds); err != nil {
		t.Fatalf("inspect schema: %v", err)
	}
	if rounds != 0 {
		t.Fatal("round_count survived migration")
	}
	var presentations int
	if err := database.QueryRow(`SELECT COUNT(*) FROM envelope_presentations WHERE envelope_uuid = ?`, envelopeUUID).Scan(&presentations); err != nil {
		t.Fatalf("read preserved presentation: %v", err)
	}
	if presentations != 1 {
		t.Fatalf("preserved presentations = %d, want 1", presentations)
	}
	var foreignKeyErrors int
	if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&foreignKeyErrors); err != nil {
		t.Fatalf("foreign key check: %v", err)
	}
	if foreignKeyErrors != 0 {
		t.Fatalf("foreign key errors after migration: %d", foreignKeyErrors)
	}
}
