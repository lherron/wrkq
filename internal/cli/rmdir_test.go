package cli

import (
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func TestRmdir(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	database.Migrate()

	// Create test actor
	actorUUID := "test-actor-uuid"
	_, err = database.Exec(`
		INSERT INTO actors (uuid, slug, display_name, role)
		VALUES (?, 'test-actor', 'Test Actor', 'human')
	`, actorUUID)
	if err != nil {
		t.Fatalf("Failed to create actor: %v", err)
	}

	t.Run("removes empty container", func(t *testing.T) {
		slug := "empty-test"
		_, err := database.Exec(`
			INSERT INTO containers (id, slug, title, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES ('', ?, 'Empty Test', ?, ?, 1)
		`, slug, actorUUID, actorUUID)
		if err != nil {
			t.Fatalf("Failed to create container: %v", err)
		}

		// Get friendly ID
		var containerID string
		database.QueryRow(`SELECT id FROM containers WHERE slug = ?`, slug).Scan(&containerID)

		// Verify container exists
		var count int
		database.QueryRow(`SELECT COUNT(*) FROM containers WHERE slug = ?`, slug).Scan(&count)
		if count != 1 {
			t.Fatal("Container should exist before removal")
		}

		// Remove container
		rmdirForce = false
		err = removeContainer(rmdirCmd, database, actorUUID, containerID)
		if err != nil {
			t.Fatalf("Failed to remove empty container: %v", err)
		}

		// Verify container deleted
		database.QueryRow(`SELECT COUNT(*) FROM containers WHERE slug = ?`, slug).Scan(&count)
		if count != 0 {
			t.Error("Container should be deleted")
		}
	})

	t.Run("fails to remove non-empty container without force", func(t *testing.T) {
		slug := "non-empty"
		database.Exec(`
			INSERT INTO containers (id, slug, title, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES ('', ?, 'Non Empty', ?, ?, 1)
		`, slug, actorUUID, actorUUID)

		// Get container UUID and ID
		var containerUUID, containerID string
		database.QueryRow(`SELECT uuid, id FROM containers WHERE slug = ?`, slug).Scan(&containerUUID, &containerID)

		// Add a task to the container
		database.Exec(`
			INSERT INTO tasks (id, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES ('', 'task-1', 'Task 1', ?, 'open', 2, ?, ?, 1)
		`, containerUUID, actorUUID, actorUUID)

		// Try to remove without force (should fail)
		rmdirForce = false
		err := removeContainer(rmdirCmd, database, actorUUID, containerID)
		if err == nil {
			t.Error("Expected error when removing non-empty container without force")
		}

		// Verify container still exists
		var count int
		database.QueryRow(`SELECT COUNT(*) FROM containers WHERE uuid = ?`, containerUUID).Scan(&count)
		if count != 1 {
			t.Error("Container should still exist after failed removal")
		}
	})

	t.Run("removes non-empty container with force", func(t *testing.T) {
		slug := "force-test"
		database.Exec(`
			INSERT INTO containers (id, slug, title, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES ('', ?, 'Force Test', ?, ?, 1)
		`, slug, actorUUID, actorUUID)

		// Get container UUID and ID
		var containerUUID, containerID string
		database.QueryRow(`SELECT uuid, id FROM containers WHERE slug = ?`, slug).Scan(&containerUUID, &containerID)

		// Add tasks to the container
		for i := 1; i <= 3; i++ {
			database.Exec(`
				INSERT INTO tasks (id, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
				VALUES ('', ?, 'Task', ?, 'open', 2, ?, ?, 1)
			`, "task-"+string(rune('0'+i)), containerUUID, actorUUID, actorUUID)
		}

		// Remove with force and yes
		rmdirForce = true
		rmdirYes = true
		err := removeContainer(rmdirCmd, database, actorUUID, containerID)
		if err != nil {
			t.Fatalf("Failed to remove container with force: %v", err)
		}

		// Verify container deleted
		var count int
		database.QueryRow(`SELECT COUNT(*) FROM containers WHERE uuid = ?`, containerUUID).Scan(&count)
		if count != 0 {
			t.Error("Container should be deleted with force")
		}

		// Verify tasks deleted
		database.QueryRow(`SELECT COUNT(*) FROM tasks WHERE project_uuid = ?`, containerUUID).Scan(&count)
		if count != 0 {
			t.Error("Tasks should be deleted with container")
		}

		rmdirForce = false
		rmdirYes = false
	})

	t.Run("removes container with child containers when forced", func(t *testing.T) {
		database.Exec(`
			INSERT INTO containers (id, slug, title, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES ('', 'parent', 'Parent', ?, ?, 1)
		`, actorUUID, actorUUID)

		var parentUUID, parentID string
		database.QueryRow(`SELECT uuid, id FROM containers WHERE slug = 'parent'`).Scan(&parentUUID, &parentID)

		database.Exec(`
			INSERT INTO containers (id, slug, title, parent_uuid, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES ('', 'child', 'Child', ?, ?, ?, 1)
		`, parentUUID, actorUUID, actorUUID)

		var childUUID string
		database.QueryRow(`SELECT uuid FROM containers WHERE slug = 'child'`).Scan(&childUUID)

		// Remove parent with force
		rmdirForce = true
		rmdirYes = true
		err := removeContainer(rmdirCmd, database, actorUUID, parentID)
		if err != nil {
			t.Fatalf("Failed to remove parent container: %v", err)
		}

		// Verify both containers deleted
		var count int
		database.QueryRow(`SELECT COUNT(*) FROM containers WHERE uuid IN (?, ?)`, parentUUID, childUUID).Scan(&count)
		if count != 0 {
			t.Error("Both parent and child containers should be deleted")
		}

		rmdirForce = false
		rmdirYes = false
	})

	t.Run("logs container.deleted event", func(t *testing.T) {
		slug := "event-test"
		database.Exec(`
			INSERT INTO containers (id, slug, title, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES ('', ?, 'Event Test', ?, ?, 1)
		`, slug, actorUUID, actorUUID)

		var containerUUID, containerID string
		database.QueryRow(`SELECT uuid, id FROM containers WHERE slug = ?`, slug).Scan(&containerUUID, &containerID)

		// Remove container
		rmdirForce = false
		err := removeContainer(rmdirCmd, database, actorUUID, containerID)
		if err != nil {
			t.Fatalf("Failed to remove container: %v", err)
		}

		// Verify event logged
		var eventCount int
		database.QueryRow(`
			SELECT COUNT(*) FROM event_log
			WHERE resource_type = 'container' AND resource_uuid = ? AND event_type = 'container.deleted'
		`, containerUUID).Scan(&eventCount)

		if eventCount != 1 {
			t.Errorf("Expected 1 container.deleted event, got %d", eventCount)
		}

		// Verify event has payload
		var payload *string
		database.QueryRow(`SELECT payload FROM event_log WHERE event_type = 'container.deleted' AND resource_uuid = ?`, containerUUID).Scan(&payload)
		if payload == nil {
			t.Error("Expected event payload, got nil")
		}
	})

	t.Run("fails to remove container with child containers without force", func(t *testing.T) {
		database.Exec(`
			INSERT INTO containers (id, slug, title, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES ('', 'parent-nf', 'Parent No Force', ?, ?, 1)
		`, actorUUID, actorUUID)

		var parentUUID, parentID string
		database.QueryRow(`SELECT uuid, id FROM containers WHERE slug = 'parent-nf'`).Scan(&parentUUID, &parentID)

		database.Exec(`
			INSERT INTO containers (id, slug, title, parent_uuid, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES ('', 'child-nf', 'Child No Force', ?, ?, ?, 1)
		`, parentUUID, actorUUID, actorUUID)

		// Try to remove without force (should fail)
		rmdirForce = false
		err := removeContainer(rmdirCmd, database, actorUUID, parentID)
		if err == nil {
			t.Error("Expected error when removing container with children without force")
		}

		// Verify container still exists
		var count int
		database.QueryRow(`SELECT COUNT(*) FROM containers WHERE uuid = ?`, parentUUID).Scan(&count)
		if count != 1 {
			t.Error("Container should still exist after failed removal")
		}
	})
}
