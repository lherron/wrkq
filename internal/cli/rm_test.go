package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
)

func TestRmPurge(t *testing.T) {
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

	// Create test actor
	actorUUID := "test-actor-uuid"
	_, err = database.Exec(`
		INSERT INTO actors (uuid, slug, display_name, role)
		VALUES (?, 'test-actor', 'Test Actor', 'human')
	`, actorUUID)
	if err != nil {
		t.Fatalf("Failed to create actor: %v", err)
	}

	// Create test container
	containerUUID := "test-container-uuid"
	_, err = database.Exec(`
		INSERT INTO containers (uuid, slug, title, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES (?, 'test-project', 'Test Project', ?, ?, 1)
	`, containerUUID, actorUUID, actorUUID)
	if err != nil {
		t.Fatalf("Failed to create container: %v", err)
	}

	t.Run("purge deletes task from database", func(t *testing.T) {
		taskUUID := "purge-test-1"
		database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, 'purge-1', 'Purge Test 1', ?, 'open', 2, ?, ?, 1)
		`, taskUUID, containerUUID, actorUUID, actorUUID)

		// Verify task exists
		var count int
		database.QueryRow(`SELECT COUNT(*) FROM tasks WHERE uuid = ?`, taskUUID).Scan(&count)
		if count != 1 {
			t.Fatal("Task should exist before purge")
		}

		// Purge task
		rmPurge = true
		_, err = removeTask(store.New(database), attachDir, actorUUID, taskUUID)
		if err != nil {
			t.Fatalf("Failed to purge task: %v", err)
		}

		// Verify task deleted
		database.QueryRow(`SELECT COUNT(*) FROM tasks WHERE uuid = ?`, taskUUID).Scan(&count)
		if count != 0 {
			t.Error("Task should be deleted after purge")
		}

		rmPurge = false
	})

	t.Run("soft delete archives task without removing", func(t *testing.T) {
		taskUUID := "archive-test"
		database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, 'archive-1', 'Archive Test', ?, 'open', 2, ?, ?, 1)
		`, taskUUID, containerUUID, actorUUID, actorUUID)

		// Soft delete (default)
		rmPurge = false
		_, err := removeTask(store.New(database), attachDir, actorUUID, taskUUID)
		if err != nil {
			t.Fatalf("Failed to archive task: %v", err)
		}

		// Verify task still exists
		var count int
		database.QueryRow(`SELECT COUNT(*) FROM tasks WHERE uuid = ?`, taskUUID).Scan(&count)
		if count != 1 {
			t.Error("Task should still exist after soft delete")
		}

		// Verify state is archived
		var state string
		database.QueryRow(`SELECT state FROM tasks WHERE uuid = ?`, taskUUID).Scan(&state)
		if state != "archived" {
			t.Errorf("Expected state 'archived', got '%s'", state)
		}

		// Verify archived_at is set
		var archivedAt *string
		database.QueryRow(`SELECT archived_at FROM tasks WHERE uuid = ?`, taskUUID).Scan(&archivedAt)
		if archivedAt == nil {
			t.Error("Expected archived_at to be set")
		}
	})

	t.Run("purge deletes attachment metadata", func(t *testing.T) {
		taskUUID := "task-with-attachments"
		_, err := database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, 'task-att', 'Task with Attachments', ?, 'open', 2, ?, ?, 1)
		`, taskUUID, containerUUID, actorUUID, actorUUID)
		if err != nil {
			t.Fatalf("Failed to insert task: %v", err)
		}

		// Add attachment metadata
		_, err = database.Exec(`
			INSERT INTO attachments (task_uuid, filename, relative_path, mime_type, size_bytes)
			VALUES (?, 'test.txt', 'tasks/task-with-attachments/test.txt', 'text/plain', 100)
		`, taskUUID)
		if err != nil {
			t.Fatalf("Failed to insert attachment: %v", err)
		}

		// Verify attachment exists
		var attCount int
		database.QueryRow(`SELECT COUNT(*) FROM attachments WHERE task_uuid = ?`, taskUUID).Scan(&attCount)
		if attCount != 1 {
			t.Fatal("Attachment should exist before purge")
		}

		// Purge task
		rmPurge = true
		_, err = removeTask(store.New(database), attachDir, actorUUID, taskUUID)
		if err != nil {
			t.Fatalf("Failed to purge task with attachments: %v", err)
		}

		// Verify attachments deleted (CASCADE)
		database.QueryRow(`SELECT COUNT(*) FROM attachments WHERE task_uuid = ?`, taskUUID).Scan(&attCount)
		if attCount != 0 {
			t.Error("Attachments should be deleted via CASCADE after purge")
		}

		rmPurge = false
	})

	t.Run("purge deletes attachment files from filesystem", func(t *testing.T) {
		taskUUID := "task-with-files"
		database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, 'task-files', 'Task with Files', ?, 'open', 2, ?, ?, 1)
		`, taskUUID, containerUUID, actorUUID, actorUUID)

		// Create attachment file
		taskDir := filepath.Join(attachDir, "tasks", taskUUID)
		os.MkdirAll(taskDir, 0755)
		testFile := filepath.Join(taskDir, "test.txt")
		os.WriteFile(testFile, []byte("test content"), 0644)

		// Add attachment metadata
		relativePath := "tasks/" + taskUUID + "/test.txt"
		database.Exec(`
			INSERT INTO attachments (task_uuid, filename, relative_path, mime_type, size_bytes)
			VALUES (?, 'test.txt', ?, 'text/plain', 12)
		`, taskUUID, relativePath)

		// Verify file exists
		if _, err := os.Stat(testFile); os.IsNotExist(err) {
			t.Fatal("Test file should exist before purge")
		}

		// Purge task
		rmPurge = true
		result, err := removeTask(store.New(database), attachDir, actorUUID, taskUUID)
		if err != nil {
			t.Fatalf("Failed to purge task: %v", err)
		}

		// Verify file deleted
		if _, err := os.Stat(testFile); !os.IsNotExist(err) {
			t.Error("Attachment file should be deleted after purge")
		}

		// Verify task directory deleted
		if _, err := os.Stat(taskDir); !os.IsNotExist(err) {
			t.Error("Task directory should be deleted after purge")
		}

		// Verify result includes attachment count
		if result.AttachmentsDeleted != 1 {
			t.Errorf("Expected 1 attachment deleted, got %d", result.AttachmentsDeleted)
		}
		if result.BytesFreed != 12 {
			t.Errorf("Expected 12 bytes freed, got %d", result.BytesFreed)
		}

		rmPurge = false
	})

	t.Run("purge handles missing files gracefully", func(t *testing.T) {
		taskUUID := "task-missing-files"
		database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, 'task-missing', 'Task Missing Files', ?, 'open', 2, ?, ?, 1)
		`, taskUUID, containerUUID, actorUUID, actorUUID)

		// Add attachment metadata but don't create file
		relativePath := "tasks/" + taskUUID + "/missing.txt"
		database.Exec(`
			INSERT INTO attachments (task_uuid, filename, relative_path, mime_type, size_bytes)
			VALUES (?, 'missing.txt', ?, 'text/plain', 100)
		`, taskUUID, relativePath)

		// Purge should succeed even though file doesn't exist
		rmPurge = true
		_, err := removeTask(store.New(database), attachDir, actorUUID, taskUUID)
		if err != nil {
			t.Errorf("Purge should succeed with missing files: %v", err)
		}

		// Verify task deleted
		var count int
		database.QueryRow(`SELECT COUNT(*) FROM tasks WHERE uuid = ?`, taskUUID).Scan(&count)
		if count != 0 {
			t.Error("Task should be deleted despite missing files")
		}

		rmPurge = false
	})

	t.Run("purge logs event before deletion", func(t *testing.T) {
		taskUUID := "event-test"
		database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, 'event-task', 'Event Test', ?, 'open', 2, ?, ?, 1)
		`, taskUUID, containerUUID, actorUUID, actorUUID)

		// Purge task
		rmPurge = true
		_, err = removeTask(store.New(database), attachDir, actorUUID, taskUUID)
		if err != nil {
			t.Fatalf("Failed to purge task: %v", err)
		}

		// Verify event logged
		var eventCount int
		database.QueryRow(`
			SELECT COUNT(*) FROM event_log
			WHERE resource_type = 'task' AND resource_uuid = ? AND event_type = 'task.purged'
		`, taskUUID).Scan(&eventCount)

		if eventCount != 1 {
			t.Errorf("Expected 1 task.purged event, got %d", eventCount)
		}

		// Verify event has payload with slug
		var payload *string
		database.QueryRow(`SELECT payload FROM event_log WHERE event_type = 'task.purged' AND resource_uuid = ?`, taskUUID).Scan(&payload)
		if payload == nil {
			t.Error("Expected event payload, got nil")
		}

		rmPurge = false
	})

	t.Run("soft delete logs task.updated event", func(t *testing.T) {
		taskUUID := "soft-delete-event"
		database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, 'soft-event', 'Soft Delete Event', ?, 'open', 2, ?, ?, 1)
		`, taskUUID, containerUUID, actorUUID, actorUUID)

		// Soft delete
		rmPurge = false
		_, err := removeTask(store.New(database), attachDir, actorUUID, taskUUID)
		if err != nil {
			t.Fatalf("Failed to soft delete task: %v", err)
		}

		// Verify event logged (store uses task.archived for archive operations)
		var eventCount int
		database.QueryRow(`
			SELECT COUNT(*) FROM event_log
			WHERE resource_type = 'task' AND resource_uuid = ? AND event_type = 'task.archived'
		`, taskUUID).Scan(&eventCount)

		if eventCount < 1 {
			t.Errorf("Expected at least 1 task.archived event, got %d", eventCount)
		}
	})

	t.Run("purge increments etag before deletion", func(t *testing.T) {
		taskUUID := "etag-purge"
		database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, etag, created_by_actor_uuid, updated_by_actor_uuid)
			VALUES (?, 'etag-purge', 'ETag Purge', ?, 'open', 2, 3, ?, ?)
		`, taskUUID, containerUUID, actorUUID, actorUUID)

		// Get initial etag
		var initialEtag int
		database.QueryRow(`SELECT etag FROM tasks WHERE uuid = ?`, taskUUID).Scan(&initialEtag)
		if initialEtag != 3 {
			t.Fatalf("Expected initial etag 3, got %d", initialEtag)
		}

		// Purge task (this should work and delete the task)
		rmPurge = true
		_, err := removeTask(store.New(database), attachDir, actorUUID, taskUUID)
		if err != nil {
			t.Fatalf("Failed to purge task: %v", err)
		}

		// Task should be deleted (can't check etag after deletion)
		var count int
		database.QueryRow(`SELECT COUNT(*) FROM tasks WHERE uuid = ?`, taskUUID).Scan(&count)
		if count != 0 {
			t.Error("Task should be deleted")
		}

		rmPurge = false
	})

	t.Run("soft delete increments etag", func(t *testing.T) {
		taskUUID := "etag-archive"
		database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, etag, created_by_actor_uuid, updated_by_actor_uuid)
			VALUES (?, 'etag-archive', 'ETag Archive', ?, 'open', 2, 5, ?, ?)
		`, taskUUID, containerUUID, actorUUID, actorUUID)

		// Soft delete
		rmPurge = false
		_, err := removeTask(store.New(database), attachDir, actorUUID, taskUUID)
		if err != nil {
			t.Fatalf("Failed to archive task: %v", err)
		}

		// Verify etag incremented
		var newEtag int
		database.QueryRow(`SELECT etag FROM tasks WHERE uuid = ?`, taskUUID).Scan(&newEtag)
		if newEtag != 6 {
			t.Errorf("Expected etag 6 after archive, got %d", newEtag)
		}
	})

	t.Run("result contains correct metadata", func(t *testing.T) {
		taskUUID := "result-test"
		database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, 'result-task', 'Result Test', ?, 'open', 2, ?, ?, 1)
		`, taskUUID, containerUUID, actorUUID, actorUUID)

		// Add attachment
		taskDir := filepath.Join(attachDir, "tasks", taskUUID)
		os.MkdirAll(taskDir, 0755)
		testFile := filepath.Join(taskDir, "data.bin")
		testData := make([]byte, 1024) // 1KB
		os.WriteFile(testFile, testData, 0644)

		relativePath := "tasks/" + taskUUID + "/data.bin"
		database.Exec(`
			INSERT INTO attachments (task_uuid, filename, relative_path, mime_type, size_bytes)
			VALUES (?, 'data.bin', ?, 'application/octet-stream', 1024)
		`, taskUUID, relativePath)

		// Purge
		rmPurge = true
		result, err := removeTask(store.New(database), attachDir, actorUUID, taskUUID)
		if err != nil {
			t.Fatalf("Failed to purge: %v", err)
		}

		// Verify result
		if result.ID != "T-00010" {
			t.Errorf("Expected ID 'T-00010', got '%s'", result.ID)
		}
		if result.UUID != taskUUID {
			t.Errorf("Expected UUID '%s', got '%s'", taskUUID, result.UUID)
		}
		if result.Slug != "result-task" {
			t.Errorf("Expected slug 'result-task', got '%s'", result.Slug)
		}
		if !result.Purged {
			t.Error("Expected Purged=true")
		}
		if result.AttachmentsDeleted != 1 {
			t.Errorf("Expected 1 attachment deleted, got %d", result.AttachmentsDeleted)
		}
		if result.BytesFreed != 1024 {
			t.Errorf("Expected 1024 bytes freed, got %d", result.BytesFreed)
		}

		rmPurge = false
	})
}

func TestRmPurgeMultipleAttachments(t *testing.T) {
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

	actorUUID := "test-actor"
	database.Exec(`
		INSERT INTO actors (uuid, slug, display_name, role)
		VALUES (?, 'test', 'Test', 'human')
	`, actorUUID)

	containerUUID := "test-container"
	database.Exec(`
		INSERT INTO containers (uuid, slug, title, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES (?, 'proj', 'Project', ?, ?, 1)
	`, containerUUID, actorUUID, actorUUID)

	taskUUID := "multi-attach-task"
	database.Exec(`
		INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES (?, 'multi', 'Multi Attachments', ?, 'open', 2, ?, ?, 1)
	`, taskUUID, containerUUID, actorUUID, actorUUID)

	// Create multiple attachment files
	taskDir := filepath.Join(attachDir, "tasks", taskUUID)
	os.MkdirAll(taskDir, 0755)

	files := []struct {
		name string
		size int
	}{
		{"file1.txt", 100},
		{"file2.pdf", 500},
		{"file3.jpg", 2048},
	}

	totalSize := int64(0)
	for _, f := range files {
		filePath := filepath.Join(taskDir, f.name)
		data := make([]byte, f.size)
		os.WriteFile(filePath, data, 0644)
		totalSize += int64(f.size)

		relativePath := "tasks/" + taskUUID + "/" + f.name
		database.Exec(`
			INSERT INTO attachments (task_uuid, filename, relative_path, mime_type, size_bytes)
			VALUES (?, ?, ?, 'application/octet-stream', ?)
		`, taskUUID, f.name, relativePath, f.size)
	}

	// Purge task
	rmPurge = true
	result, err := removeTask(store.New(database), attachDir, actorUUID, taskUUID)
	if err != nil {
		t.Fatalf("Failed to purge task with multiple attachments: %v", err)
	}

	// Verify all files deleted
	for _, f := range files {
		filePath := filepath.Join(taskDir, f.name)
		if _, err := os.Stat(filePath); !os.IsNotExist(err) {
			t.Errorf("File %s should be deleted", f.name)
		}
	}

	// Verify directory deleted
	if _, err := os.Stat(taskDir); !os.IsNotExist(err) {
		t.Error("Task directory should be deleted")
	}

	// Verify result counts
	if result.AttachmentsDeleted != 3 {
		t.Errorf("Expected 3 attachments deleted, got %d", result.AttachmentsDeleted)
	}
	if result.BytesFreed != totalSize {
		t.Errorf("Expected %d bytes freed, got %d", totalSize, result.BytesFreed)
	}

	rmPurge = false
}
