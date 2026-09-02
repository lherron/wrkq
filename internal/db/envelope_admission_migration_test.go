//go:build wrkq_local

package db

import (
	"path/filepath"
	"testing"
)

func TestEnvelopeAdmissionMigrationPreservesRowsAndAddsDeliveryTTL(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "wrkq.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	if _, err := database.Exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')))`); err != nil {
		t.Fatal(err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "000059_envelope_admission.sql" {
			continue
		}
		content, rerr := migrationsFS.ReadFile(filepath.Join("migrations", entry.Name()))
		if rerr != nil {
			t.Fatal(rerr)
		}
		if err := database.applyMigration(entry.Name(), content); err != nil {
			t.Fatalf("apply %s: %v", entry.Name(), err)
		}
	}
	if _, err := database.Exec(`INSERT INTO rooms (id, kind, opened_by_principal_ref, created_by_principal_ref, updated_by_principal_ref) VALUES ('R-99959','adhoc','agent:cody','agent:cody','agent:cody')`); err != nil {
		t.Fatal(err)
	}
	var room string
	if err := database.QueryRow("SELECT uuid FROM rooms WHERE id='R-99959'").Scan(&room); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO envelopes (id, room_uuid, from_principal_ref, to_principal_ref, obligation, body, state, created_by_principal_ref, updated_by_principal_ref) VALUES ('EN-99959',?,'agent:cody','agent:clod','reply_required','preserve','pending','agent:cody','agent:cody')`, room); err != nil {
		t.Fatal(err)
	}
	content, err := migrationsFS.ReadFile("migrations/000059_envelope_admission.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.applyMigration("000059_envelope_admission.sql", content); err != nil {
		t.Fatal(err)
	}
	var state, delivery string
	var expires any
	if err := database.QueryRow("SELECT state, delivery, expires_at FROM envelopes WHERE id='EN-99959'").Scan(&state, &delivery, &expires); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || delivery != "queue" || expires != nil {
		t.Fatalf("migrated = %s/%s/%v", state, delivery, expires)
	}
	var index int
	if err := database.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name IN ('envelopes_expiry_idx','envelopes_message_seq_idx')").Scan(&index); err != nil {
		t.Fatal(err)
	}
	if index != 2 {
		t.Fatalf("preserved/new indexes = %d", index)
	}
	var fk int
	if err := database.QueryRow("SELECT COUNT(*) FROM pragma_foreign_key_check").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 0 {
		t.Fatalf("foreign key errors = %d", fk)
	}
}
