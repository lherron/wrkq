//go:build wrkq_local

package db

import (
	"path/filepath"
	"testing"
)

// T-11305: 000070 rebuilds envelopes so `note` is a stored obligation,
// addressed like fyi, and every existing row, receipt, index and trigger
// survives the rebuild.
func TestEnvelopeNoteMigrationPreservesRowsAndAdmitsAddressedNotes(t *testing.T) {
	const target = "000070_envelope_note.sql"
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
		if entry.Name() >= target {
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
	if _, err := database.Exec(`INSERT INTO rooms (id, kind, opened_by_principal_ref, created_by_principal_ref, updated_by_principal_ref) VALUES ('R-99970','adhoc','agent:cody','agent:cody','agent:cody')`); err != nil {
		t.Fatal(err)
	}
	var room string
	if err := database.QueryRow("SELECT uuid FROM rooms WHERE id='R-99970'").Scan(&room); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO envelopes (id, room_uuid, from_principal_ref, to_scope_ref, to_principal_ref, to_project_uuid, obligation, body, state, created_by_principal_ref, updated_by_principal_ref)
		VALUES ('EN-99970',?,'agent:cody','clod@wrkq:primary','agent:clod','p-uuid','reply_required','preserve','pending','agent:cody','agent:cody')`, room); err != nil {
		t.Fatal(err)
	}
	var envelopeUUID string
	if err := database.QueryRow("SELECT uuid FROM envelopes WHERE id='EN-99970'").Scan(&envelopeUUID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO envelope_presentations (envelope_uuid, room_uuid, member_ref, runtime_id, presented_by_principal_ref)
		VALUES (?, ?, 'clod@wrkq:primary', 'rt-1', 'agent:hrc')`, envelopeUUID, room); err != nil {
		t.Fatal(err)
	}
	var indexesBefore int
	if err := database.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE tbl_name='envelopes' AND type IN ('index','trigger')").Scan(&indexesBefore); err != nil {
		t.Fatal(err)
	}

	content, err := migrationsFS.ReadFile("migrations/" + target)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.applyMigration(target, content); err != nil {
		t.Fatal(err)
	}

	var state, toProject string
	if err := database.QueryRow("SELECT state, to_project_uuid FROM envelopes WHERE id='EN-99970'").Scan(&state, &toProject); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || toProject != "p-uuid" {
		t.Fatalf("migrated row = %s/%s", state, toProject)
	}
	var receipts int
	if err := database.QueryRow("SELECT COUNT(*) FROM envelope_presentations WHERE envelope_uuid = ?", envelopeUUID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		t.Fatalf("presentation receipts after rebuild = %d, want 1", receipts)
	}
	var indexesAfter int
	if err := database.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE tbl_name='envelopes' AND type IN ('index','trigger')").Scan(&indexesAfter); err != nil {
		t.Fatal(err)
	}
	// Everything carried over, plus the two note seeks.
	if indexesAfter != indexesBefore+2 {
		t.Fatalf("indexes+triggers = %d after rebuild, want %d", indexesAfter, indexesBefore+2)
	}

	// An addressed note is admitted, and the friendly-id trigger still fires.
	if _, err := database.Exec(`INSERT INTO envelopes (room_uuid, from_principal_ref, to_principal_ref, obligation, body, state, terminal_actor, created_by_principal_ref, updated_by_principal_ref)
		VALUES (?,'agent:cody','agent:clod','note','noted','acked','agent:cody','agent:cody','agent:cody')`, room); err != nil {
		t.Fatalf("insert addressed note: %v", err)
	}
	var noteID string
	if err := database.QueryRow("SELECT id FROM envelopes WHERE obligation='note'").Scan(&noteID); err != nil || noteID == "" {
		t.Fatalf("note id = %q, %v", noteID, err)
	}
	// A note without an addressee is refused, as for fyi.
	if _, err := database.Exec(`INSERT INTO envelopes (room_uuid, from_principal_ref, obligation, body, state, created_by_principal_ref, updated_by_principal_ref)
		VALUES (?,'agent:cody','note','nobody','acked','agent:cody','agent:cody')`, room); err == nil {
		t.Fatal("an unaddressed note was admitted")
	}
	var fk int
	if err := database.QueryRow("SELECT COUNT(*) FROM pragma_foreign_key_check").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 0 {
		t.Fatalf("foreign key errors = %d", fk)
	}
}
