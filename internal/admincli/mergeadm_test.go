package admincli

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

const (
	testActorUUID = "00000000-0000-0000-0000-000000000001"
	testActorID   = "A-00001"
)

func setupMergeDB(t *testing.T) (*db.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}
	_, err = database.Exec(`
		INSERT INTO actors (uuid, id, slug, display_name, role, created_at, updated_at)
		VALUES (?, ?, 'test-user', 'Test User', 'human', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')
	`, testActorUUID, testActorID)
	if err != nil {
		t.Fatalf("failed to seed actor: %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	return database, path
}

// TestMergeIsHardGated asserts that the legacy cross-database merge data mover
// is disabled under principal-only attribution. The mover relied on the actors
// table and *_actor_uuid remapping, which are no longer a write surface.
func TestMergeIsHardGated(t *testing.T) {
	srcDB, _ := setupMergeDB(t)
	destDB, _ := setupMergeDB(t)

	_, err := mergeProjectIntoCanonical(mergeOptions{
		SourceDB:        srcDB,
		DestDB:          destDB,
		SourceAttachDir: t.TempDir(),
		DestAttachDir:   t.TempDir(),
		ProjectSelector: "proj",
		PathPrefix:      "canonical",
		DryRun:          false,
	})
	if err == nil {
		t.Fatal("expected merge to be hard-gated, got nil error")
	}
	if !errors.Is(err, errLegacyActorMovement) {
		t.Fatalf("expected errLegacyActorMovement, got %v", err)
	}
}
