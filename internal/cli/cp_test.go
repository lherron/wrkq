package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func TestCpCommand(t *testing.T) {
	// Create temporary database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	attachDir := filepath.Join(tmpDir, "attachments")
	os.MkdirAll(attachDir, 0755)

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	// Initialize database
	if err := database.Migrate(); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}

	// Create test actor
	actorUUID := "test-actor-uuid"
	_, err = database.Exec(`INSERT INTO actors (uuid, id, slug, display_name, role) VALUES (?, 'A-00001', 'test-actor', 'Test Actor', 'human')`, actorUUID)
	if err != nil {
		t.Fatalf("Failed to create actor: %v", err)
	}
	database.Exec(`UPDATE actors SET uuid = ? WHERE slug = 'test-actor'`, actorUUID)

	// Create test container
	containerUUID := "test-container-uuid"
	_, err = database.Exec(`INSERT INTO containers (id, slug, name, created_by_actor_uuid, updated_by_actor_uuid) VALUES ('P-00001', 'test-project', 'Test Project', ?, ?)`, actorUUID, actorUUID)
	if err != nil {
		t.Fatalf("Failed to create container: %v", err)
	}
	database.Exec(`UPDATE containers SET uuid = ? WHERE slug = 'test-project'`, containerUUID)

	// Create destination container
	destUUID := "dest-container-uuid"
	database.Exec(`INSERT INTO containers (id, slug, name, created_by_actor_uuid, updated_by_actor_uuid) VALUES ('P-00002', 'dest', 'Destination', ?, ?)`, actorUUID, actorUUID)
	database.Exec(`UPDATE containers SET uuid = ? WHERE slug = 'dest'`, destUUID)

	t.Run("copy single task creates new UUID", func(t *testing.T) {
		// Create source task
		sourceUUID := "source-task-uuid"
		_, err := database.Exec(`
			INSERT INTO tasks (id, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid)
			VALUES ('T-00001', 'source-task', 'Source Task', ?, 'open', 2, ?, ?)
		`, containerUUID, actorUUID, actorUUID)
		if err != nil {
			t.Fatalf("Failed to create source task: %v", err)
		}
		database.Exec(`UPDATE tasks SET uuid = ? WHERE slug = 'source-task'`, sourceUUID)

		// Copy task
		result, err := copyTask(database, attachDir, actorUUID, sourceUUID, destUUID)
		if err != nil {
			t.Fatalf("Failed to copy task: %v", err)
		}

		// Verify new UUID created
		if result.DestUUID == sourceUUID {
			t.Error("Expected new UUID, got same as source")
		}

		// Verify new friendly ID
		if result.DestID == "T-00001" {
			t.Error("Expected new friendly ID")
		}

		// Verify task exists in destination
		var count int
		database.QueryRow(`SELECT COUNT(*) FROM tasks WHERE uuid = ? AND project_uuid = ?`, result.DestUUID, destUUID).Scan(&count)
		if count != 1 {
			t.Errorf("Expected 1 task in destination, got %d", count)
		}

		// Verify metadata copied
		var title, state string
		var priority int
		database.QueryRow(`SELECT title, state, priority FROM tasks WHERE uuid = ?`, result.DestUUID).Scan(&title, &state, &priority)
		if title != "Source Task" {
			t.Errorf("Expected title 'Source Task', got '%s'", title)
		}
		if state != "open" {
			t.Errorf("Expected state 'open', got '%s'", state)
		}
		if priority != 2 {
			t.Errorf("Expected priority 2, got %d", priority)
		}
	})

	t.Run("copy with attachments metadata only", func(t *testing.T) {
		// Create task with attachments
		taskUUID := "task-with-attachments"
		database.Exec(`
			INSERT INTO tasks (id, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid)
			VALUES ('T-00002', 'task-att', 'Task with Attachments', ?, 'open', 2, ?, ?)
		`, containerUUID, actorUUID, actorUUID)
		database.Exec(`UPDATE tasks SET uuid = ? WHERE slug = 'task-att'`, taskUUID)

		// Add attachment metadata
		database.Exec(`
			INSERT INTO attachments (id, task_uuid, filename, relative_path, mime_type, size_bytes)
			VALUES ('', ?, 'test.txt', 'tasks/task-with-attachments/test.txt', 'text/plain', 100)
		`, taskUUID)

		// Copy without --with-attachments flag (metadata only)
		cpWithAttachments = false
		cpShallow = false
		result, err := copyTask(database, attachDir, actorUUID, taskUUID, destUUID)
		if err != nil {
			t.Fatalf("Failed to copy task with attachments: %v", err)
		}

		// Verify attachment metadata copied
		var attCount int
		database.QueryRow(`SELECT COUNT(*) FROM attachments WHERE task_uuid = ?`, result.DestUUID).Scan(&attCount)
		if attCount != 1 {
			t.Errorf("Expected 1 attachment in destination, got %d", attCount)
		}

		// Verify metadata only (same relative path)
		var relativePath string
		database.QueryRow(`SELECT relative_path FROM attachments WHERE task_uuid = ?`, result.DestUUID).Scan(&relativePath)
		if relativePath != "tasks/task-with-attachments/test.txt" {
			t.Errorf("Expected same relative path for metadata-only copy, got %s", relativePath)
		}
	})

	t.Run("copy with --shallow skips attachments", func(t *testing.T) {
		// Create task with attachments
		taskUUID := "task-shallow"
		database.Exec(`
			INSERT INTO tasks (id, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid)
			VALUES ('T-00003', 'task-shallow', 'Shallow Task', ?, 'open', 2, ?, ?)
		`, containerUUID, actorUUID, actorUUID)
		database.Exec(`UPDATE tasks SET uuid = ? WHERE slug = 'task-shallow'`, taskUUID)

		database.Exec(`
			INSERT INTO attachments (id, task_uuid, filename, relative_path, mime_type, size_bytes)
			VALUES ('', ?, 'file.pdf', 'tasks/task-shallow/file.pdf', 'application/pdf', 500)
		`, taskUUID)

		// Copy with --shallow
		cpShallow = true
		result, err := copyTask(database, attachDir, actorUUID, taskUUID, destUUID)
		if err != nil {
			t.Fatalf("Failed to copy task shallow: %v", err)
		}

		// Verify no attachments copied
		var attCount int
		database.QueryRow(`SELECT COUNT(*) FROM attachments WHERE task_uuid = ?`, result.DestUUID).Scan(&attCount)
		if attCount != 0 {
			t.Errorf("Expected 0 attachments with --shallow, got %d", attCount)
		}

		// Reset flag
		cpShallow = false
	})

	t.Run("copy with --overwrite replaces existing", func(t *testing.T) {
		// Create source task
		sourceUUID := "source-overwrite"
		database.Exec(`
			INSERT INTO tasks (id, slug, title, project_uuid, state, priority, body, created_by_actor_uuid, updated_by_actor_uuid)
			VALUES ('T-00004', 'overwrite-source', 'Source Version', ?, 'open', 1, 'Original body', ?, ?)
		`, containerUUID, actorUUID, actorUUID)
		database.Exec(`UPDATE tasks SET uuid = ? WHERE slug = 'overwrite-source'`, sourceUUID)

		// Create existing task in destination with same slug
		existingUUID := "existing-task"
		database.Exec(`
			INSERT INTO tasks (id, slug, title, project_uuid, state, priority, body, created_by_actor_uuid, updated_by_actor_uuid)
			VALUES ('T-00005', 'overwrite-source', 'Existing Version', ?, 'completed', 3, 'Old body', ?, ?)
		`, destUUID, actorUUID, actorUUID)
		database.Exec(`UPDATE tasks SET uuid = ? WHERE id = 'T-00005'`, existingUUID)

		// Try copy without --overwrite (should fail)
		cpOverwrite = false
		_, err := copyTask(database, attachDir, actorUUID, sourceUUID, destUUID)
		if err == nil {
			t.Error("Expected error when copying to existing slug without --overwrite")
		}

		// Copy with --overwrite
		cpOverwrite = true
		result, err := copyTask(database, attachDir, actorUUID, sourceUUID, destUUID)
		if err != nil {
			t.Fatalf("Failed to copy with --overwrite: %v", err)
		}

		// Verify existing UUID preserved
		if result.DestUUID != existingUUID {
			t.Errorf("Expected existing UUID %s, got %s", existingUUID, result.DestUUID)
		}

		// Verify friendly ID preserved
		if result.DestID != "T-00005" {
			t.Errorf("Expected existing ID T-00005, got %s", result.DestID)
		}

		// Verify content updated
		var title, body string
		var priority int
		database.QueryRow(`SELECT title, priority, body FROM tasks WHERE uuid = ?`, existingUUID).Scan(&title, &priority, &body)
		if title != "Source Version" {
			t.Errorf("Expected updated title 'Source Version', got '%s'", title)
		}
		if priority != 1 {
			t.Errorf("Expected updated priority 1, got %d", priority)
		}
		if body != "Original body" {
			t.Errorf("Expected updated body, got '%s'", body)
		}

		// Reset flag
		cpOverwrite = false
	})

	t.Run("copy resets timestamps", func(t *testing.T) {
		// Create completed task with completed_at timestamp
		sourceUUID := "completed-task"
		database.Exec(`
			INSERT INTO tasks (id, slug, title, project_uuid, state, priority, completed_at, created_by_actor_uuid, updated_by_actor_uuid)
			VALUES ('T-00006', 'completed', 'Completed Task', ?, 'completed', 2, '2025-01-01T00:00:00Z', ?, ?)
		`, containerUUID, actorUUID, actorUUID)
		database.Exec(`UPDATE tasks SET uuid = ? WHERE slug = 'completed'`, sourceUUID)

		// Copy task
		result, err := copyTask(database, attachDir, actorUUID, sourceUUID, destUUID)
		if err != nil {
			t.Fatalf("Failed to copy completed task: %v", err)
		}

		// Verify completed_at is null (even though source was completed)
		var completedAt *string
		database.QueryRow(`SELECT completed_at FROM tasks WHERE uuid = ?`, result.DestUUID).Scan(&completedAt)
		if completedAt != nil {
			t.Errorf("Expected completed_at to be null, got %v", *completedAt)
		}

		// Note: In production, we'd also verify created_at is recent, but that requires
		// handling SQLite's datetime functions which are tricky in tests
	})

	t.Run("copy preserves labels and dates", func(t *testing.T) {
		sourceUUID := "task-with-metadata"
		database.Exec(`
			INSERT INTO tasks (id, slug, title, project_uuid, state, priority, labels, start_at, due_at, created_by_actor_uuid, updated_by_actor_uuid)
			VALUES ('T-00007', 'metadata', 'Task with Metadata', ?, 'open', 2, 'bug,urgent', '2025-01-15T00:00:00Z', '2025-01-20T00:00:00Z', ?, ?)
		`, containerUUID, actorUUID, actorUUID)
		database.Exec(`UPDATE tasks SET uuid = ? WHERE slug = 'metadata'`, sourceUUID)

		result, err := copyTask(database, attachDir, actorUUID, sourceUUID, destUUID)
		if err != nil {
			t.Fatalf("Failed to copy task with metadata: %v", err)
		}

		var labels, startAt, dueAt *string
		database.QueryRow(`SELECT labels, start_at, due_at FROM tasks WHERE uuid = ?`, result.DestUUID).Scan(&labels, &startAt, &dueAt)

		if labels == nil || *labels != "bug,urgent" {
			t.Errorf("Expected labels 'bug,urgent', got %v", labels)
		}
		if startAt == nil || *startAt != "2025-01-15T00:00:00Z" {
			t.Errorf("Expected start_at preserved, got %v", startAt)
		}
		if dueAt == nil || *dueAt != "2025-01-20T00:00:00Z" {
			t.Errorf("Expected due_at preserved, got %v", dueAt)
		}
	})

	t.Run("copy with etag check", func(t *testing.T) {
		sourceUUID := "etag-task"
		database.Exec(`
			INSERT INTO tasks (id, slug, title, project_uuid, state, priority, etag, created_by_actor_uuid, updated_by_actor_uuid)
			VALUES ('T-00008', 'etag-task', 'ETag Task', ?, 'open', 2, 5, ?, ?)
		`, containerUUID, actorUUID, actorUUID)
		database.Exec(`UPDATE tasks SET uuid = ? WHERE slug = 'etag-task'`, sourceUUID)

		// Try copy with wrong etag
		cpIfMatch = 3
		_, err := copyTask(database, attachDir, actorUUID, sourceUUID, destUUID)
		if err == nil {
			t.Error("Expected etag mismatch error")
		}

		// Copy with correct etag
		cpIfMatch = 5
		_, err = copyTask(database, attachDir, actorUUID, sourceUUID, destUUID)
		if err != nil {
			t.Errorf("Expected copy to succeed with correct etag: %v", err)
		}

		// Reset flag
		cpIfMatch = 0
	})
}

func TestCpCommandEventLogging(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	attachDir := filepath.Join(tmpDir, "attachments")
	os.MkdirAll(attachDir, 0755)

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	database.Migrate()

	// Create actor and containers
	actorUUID := "test-actor"
	database.Exec(`INSERT INTO actors (id, slug, display_name, role) VALUES ('A-00001', 'test', 'Test', 'human')`)
	database.Exec(`UPDATE actors SET uuid = ? WHERE slug = 'test'`, actorUUID)

	containerUUID := "source-container"
	database.Exec(`INSERT INTO containers (id, slug, name, created_by_actor_uuid, updated_by_actor_uuid) VALUES ('P-00001', 'src', 'Source', ?, ?)`, actorUUID, actorUUID)
	database.Exec(`UPDATE containers SET uuid = ? WHERE slug = 'src'`, containerUUID)

	destUUID := "dest-container"
	database.Exec(`INSERT INTO containers (id, slug, name, created_by_actor_uuid, updated_by_actor_uuid) VALUES ('P-00002', 'dst', 'Dest', ?, ?)`, actorUUID, actorUUID)
	database.Exec(`UPDATE containers SET uuid = ? WHERE slug = 'dst'`, destUUID)

	// Create source task
	sourceUUID := "source-task"
	database.Exec(`
		INSERT INTO tasks (id, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('T-00001', 'task', 'Task', ?, 'open', 2, ?, ?)
	`, containerUUID, actorUUID, actorUUID)
	database.Exec(`UPDATE tasks SET uuid = ? WHERE slug = 'task'`, sourceUUID)

	// Copy task
	result, err := copyTask(database, attachDir, actorUUID, sourceUUID, destUUID)
	if err != nil {
		t.Fatalf("Failed to copy task: %v", err)
	}

	// Verify event logged
	var eventCount int
	database.QueryRow(`
		SELECT COUNT(*) FROM event_log
		WHERE resource_type = 'task' AND resource_uuid = ? AND event_type = 'task.copied'
	`, result.DestUUID).Scan(&eventCount)

	if eventCount != 1 {
		t.Errorf("Expected 1 task.copied event, got %d", eventCount)
	}

	// Verify payload contains source info
	var payload *string
	database.QueryRow(`SELECT payload FROM event_log WHERE event_type = 'task.copied' AND resource_uuid = ?`, result.DestUUID).Scan(&payload)
	if payload == nil {
		t.Error("Expected event payload, got nil")
	}
}
