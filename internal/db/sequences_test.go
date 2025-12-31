package db

import (
	"path/filepath"
	"testing"
)

func TestSequenceDriftDetectAndFix(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer database.Close()

	if err := database.Migrate(); err != nil {
		t.Fatalf("failed to migrate database: %v", err)
	}

	// Insert actor with explicit friendly ID (bypasses sequence trigger).
	_, err = database.Exec(`
		INSERT INTO actors (uuid, id, slug, role)
		VALUES ('actor-uuid-1', 'A-00042', 'drift-actor', 'human')
	`)
	if err != nil {
		t.Fatalf("failed to insert actor: %v", err)
	}

	drifts, err := SequenceDrifts(database, DefaultSequenceSpecs())
	if err != nil {
		t.Fatalf("failed to detect sequence drift: %v", err)
	}

	foundActor := false
	for _, drift := range drifts {
		if drift.SeqTable == "actor_seq" {
			foundActor = true
			if drift.MaxID != 42 {
				t.Errorf("expected max ID 42, got %d", drift.MaxID)
			}
			if drift.SeqValue != 0 {
				t.Errorf("expected sequence 0 before fix, got %d", drift.SeqValue)
			}
		}
	}

	if !foundActor {
		t.Fatalf("expected actor_seq drift to be detected")
	}

	_, err = FixSequenceDrifts(database, DefaultSequenceSpecs())
	if err != nil {
		t.Fatalf("failed to fix sequence drift: %v", err)
	}

	var seq int
	if err := database.QueryRow("SELECT seq FROM sqlite_sequence WHERE name = 'actor_seq'").Scan(&seq); err != nil {
		t.Fatalf("failed to query sqlite_sequence: %v", err)
	}
	if seq != 42 {
		t.Fatalf("expected sqlite_sequence to be 42 after fix, got %d", seq)
	}

	drifts, err = SequenceDrifts(database, DefaultSequenceSpecs())
	if err != nil {
		t.Fatalf("failed to detect sequence drift after fix: %v", err)
	}
	if len(drifts) != 0 {
		t.Fatalf("expected no drift after fix, found %d", len(drifts))
	}
}
