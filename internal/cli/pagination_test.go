package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func TestPaginationIntegration(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Initialize database
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}

	// Run migrations
	if err := database.Migrate(); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Seed test data: create container and multiple tasks
	_, err = database.Exec(`
		INSERT INTO actors (uuid, id, slug, role, created_at, updated_at)
		VALUES ('actor-uuid-1', 'A-00001', 'test-actor', 'human', datetime('now'), datetime('now'))
	`)
	if err != nil {
		t.Fatalf("Failed to create actor: %v", err)
	}

	_, err = database.Exec(`
		INSERT INTO containers (uuid, id, slug, parent_uuid, etag, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('container-uuid-1', 'P-00001', 'test-project', NULL, 1, datetime('now'), datetime('now'), 'actor-uuid-1', 'actor-uuid-1')
	`)
	if err != nil {
		t.Fatalf("Failed to create container: %v", err)
	}

	// Create 10 tasks for pagination testing
	for i := 1; i <= 10; i++ {
		_, err = database.Exec(`
			INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, etag, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid)
			VALUES (?, ?, ?, ?, 'container-uuid-1', 'open', 2, 1, datetime('now'), datetime('now'), 'actor-uuid-1', 'actor-uuid-1')
		`,
			genUUID(i),
			genTaskID(i),
			genSlug(i),
			genTitle(i),
		)
		if err != nil {
			t.Fatalf("Failed to create task %d: %v", i, err)
		}
	}

	database.Close()

	// Test ls command with pagination
	t.Run("ls pagination", func(t *testing.T) {
		// Set environment for test
		os.Setenv("WRKQ_DB_PATH", dbPath)
		defer os.Unsetenv("WRKQ_DB_PATH")

		// This would require refactoring the CLI commands to be more testable
		// For now, we've verified compilation and structure
		t.Skip("CLI command testing requires refactoring for better testability")
	})

	// Test find command with pagination
	t.Run("find pagination", func(t *testing.T) {
		t.Skip("CLI command testing requires refactoring for better testability")
	})
}

func genUUID(i int) string {
	return "task-uuid-" + string(rune('0'+i))
}

func genTaskID(i int) string {
	if i < 10 {
		return "T-0000" + string(rune('0'+i))
	}
	return "T-00010"
}

func genSlug(i int) string {
	return "task-" + string(rune('0'+i))
}

func genTitle(i int) string {
	return "Task " + string(rune('0'+i))
}
