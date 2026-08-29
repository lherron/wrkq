//go:build wrkq_local

package db

import (
	"path/filepath"
	"testing"
)

func TestDropRoomSubjectMigrationPreservesRoomsAndRelationships(t *testing.T) {
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
		if entry.Name() >= "000056_drop_room_subject.sql" {
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
		id, kind, subject, opened_by_principal_ref,
		created_by_principal_ref, updated_by_principal_ref
	) VALUES ('R-99998', 'adhoc', 'same topic', 'agent:cody', 'agent:cody', 'agent:cody')`); err != nil {
		t.Fatalf("seed room: %v", err)
	}
	var roomUUID string
	if err := database.QueryRow(`SELECT uuid FROM rooms WHERE id = 'R-99998'`).Scan(&roomUUID); err != nil {
		t.Fatalf("read seeded room: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO room_members (
		room_uuid, member_ref, member_principal_ref, scoped, source
	) VALUES (?, 'cody@wrkq:primary', 'agent:cody', 1, 'joined')`, roomUUID); err != nil {
		t.Fatalf("seed member: %v", err)
	}

	content, err := migrationsFS.ReadFile(filepath.Join("migrations", "000056_drop_room_subject.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if err := database.applyMigration("000056_drop_room_subject.sql", content); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	var subjectColumns int
	if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('rooms') WHERE name = 'subject'`).Scan(&subjectColumns); err != nil {
		t.Fatalf("inspect rooms: %v", err)
	}
	if subjectColumns != 0 {
		t.Fatalf("rooms.subject survived migration")
	}
	var members int
	if err := database.QueryRow(`SELECT COUNT(*) FROM room_members WHERE room_uuid = ?`, roomUUID).Scan(&members); err != nil {
		t.Fatalf("read preserved membership: %v", err)
	}
	if members != 1 {
		t.Fatalf("preserved memberships = %d, want 1", members)
	}
	var foreignKeyErrors int
	if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&foreignKeyErrors); err != nil {
		t.Fatalf("foreign key check: %v", err)
	}
	if foreignKeyErrors != 0 {
		t.Fatalf("foreign key errors after migration: %d", foreignKeyErrors)
	}
	if _, err := database.Exec(`INSERT INTO rooms (
		kind, opened_by_principal_ref, created_by_principal_ref, updated_by_principal_ref
	) VALUES ('adhoc', 'agent:mable', 'agent:mable', 'agent:mable')`); err != nil {
		t.Fatalf("create room after migration: %v", err)
	}
	var generatedUUID, generatedID string
	if err := database.QueryRow(`SELECT uuid, id FROM rooms WHERE opened_by_principal_ref = 'agent:mable'`).Scan(&generatedUUID, &generatedID); err != nil {
		t.Fatalf("read room created after migration: %v", err)
	}
	if generatedUUID == "" || generatedID == "" {
		t.Fatalf("post-migration room identity = uuid %q / id %q", generatedUUID, generatedID)
	}
}
