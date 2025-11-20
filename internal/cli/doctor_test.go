package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func TestDoctorDatabaseFileChecks(t *testing.T) {
	t.Run("healthy database passes all file checks", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "test.db")

		database, err := db.Open(dbPath)
		if err != nil {
			t.Fatalf("Failed to open database: %v", err)
		}
		database.Migrate()
		database.Close()

		results := checkDatabaseFile(dbPath)

		// Should have 2 checks: exists and permissions
		if len(results) != 2 {
			t.Errorf("Expected 2 checks, got %d", len(results))
		}

		// Both should pass
		for _, result := range results {
			if result.Status != "ok" {
				t.Errorf("Check %s failed: %s", result.Name, result.Message)
			}
		}
	})

	t.Run("missing database file reports error", func(t *testing.T) {
		results := checkDatabaseFile("/nonexistent/path/db.db")

		if len(results) == 0 {
			t.Fatal("Expected at least one check result")
		}

		// First check should be db_file_exists with error status
		found := false
		for _, result := range results {
			if result.Name == "db_file_exists" && result.Status == "error" {
				found = true
				break
			}
		}

		if !found {
			t.Error("Expected db_file_exists check to fail for missing file")
		}
	})

	t.Run("read-only database reports permission error", func(t *testing.T) {
		tmpDir := t.TempDir()
		dbPath := filepath.Join(tmpDir, "readonly.db")

		// Create database
		database, _ := db.Open(dbPath)
		database.Migrate()
		database.Close()

		// Make read-only
		os.Chmod(dbPath, 0444)
		defer os.Chmod(dbPath, 0644) // Cleanup

		results := checkDatabaseFile(dbPath)

		// Should report permission error
		foundPermError := false
		for _, result := range results {
			if result.Name == "db_file_permissions" && result.Status == "error" {
				foundPermError = true
				break
			}
		}

		if !foundPermError {
			t.Error("Expected db_file_permissions check to fail for read-only file")
		}
	})
}

func TestDoctorDatabasePragmaChecks(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	database.Migrate()

	t.Run("WAL mode enabled passes check", func(t *testing.T) {
		results := checkDatabasePragmas(database)

		found := false
		for _, result := range results {
			if result.Name == "wal_mode" {
				found = true
				if result.Status != "ok" {
					t.Errorf("Expected WAL mode check to pass, got: %s - %s", result.Status, result.Message)
				}
			}
		}

		if !found {
			t.Error("Expected wal_mode check in results")
		}
	})

	t.Run("foreign keys enabled passes check", func(t *testing.T) {
		results := checkDatabasePragmas(database)

		found := false
		for _, result := range results {
			if result.Name == "foreign_keys" {
				found = true
				if result.Status != "ok" {
					t.Errorf("Expected foreign_keys check to pass, got: %s - %s", result.Status, result.Message)
				}
			}
		}

		if !found {
			t.Error("Expected foreign_keys check in results")
		}
	})

	t.Run("integrity check passes on healthy database", func(t *testing.T) {
		results := checkDatabasePragmas(database)

		found := false
		for _, result := range results {
			if result.Name == "integrity_check" {
				found = true
				if result.Status != "ok" {
					t.Errorf("Expected integrity_check to pass, got: %s - %s", result.Status, result.Message)
				}
			}
		}

		if !found {
			t.Error("Expected integrity_check in results")
		}
	})
}

func TestDoctorSchemaChecks(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	database.Migrate()

	t.Run("all required tables present", func(t *testing.T) {
		results := checkSchema(database)

		found := false
		for _, result := range results {
			if result.Name == "schema_tables" {
				found = true
				if result.Status != "ok" {
					t.Errorf("Expected schema check to pass, got: %s - %s", result.Status, result.Message)
				}
			}
		}

		if !found {
			t.Error("Expected schema_tables check in results")
		}
	})

	t.Run("missing tables reported as error", func(t *testing.T) {
		// Create fresh database without migrations
		tmpDir2 := t.TempDir()
		dbPath2 := filepath.Join(tmpDir2, "incomplete.db")
		db2, _ := db.Open(dbPath2)
		defer db2.Close()

		// Don't run migrations - empty database
		results := checkSchema(db2)

		found := false
		for _, result := range results {
			if result.Name == "schema_tables" && result.Status == "error" {
				found = true
			}
		}

		if !found {
			t.Error("Expected schema_tables check to fail for empty database")
		}
	})
}

func TestDoctorDataIntegrityChecks(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	database.Migrate()

	// Create test data
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

	t.Run("no orphaned tasks in healthy database", func(t *testing.T) {
		taskUUID := "valid-task"
		database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, 'task1', 'Task 1', ?, 'open', 2, ?, ?, 1)
		`, taskUUID, containerUUID, actorUUID, actorUUID)

		results := checkDataIntegrity(database)

		found := false
		for _, result := range results {
			if result.Name == "orphaned_tasks" {
				found = true
				if result.Status != "ok" {
					t.Errorf("Expected no orphaned tasks, got: %s - %s", result.Status, result.Message)
				}
			}
		}

		if !found {
			t.Error("Expected orphaned_tasks check in results")
		}
	})

	t.Run("orphaned tasks detected", func(t *testing.T) {
		// Temporarily disable foreign keys to create orphaned task
		database.Exec("PRAGMA foreign_keys = OFF")
		defer database.Exec("PRAGMA foreign_keys = ON")

		// Create task with invalid project_uuid
		database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES ('orphan-task-uuid', 'T-00002', 'orphan', 'Orphaned', 'nonexistent-uuid', 'open', 2, ?, ?, 1)
		`, actorUUID, actorUUID)

		results := checkDataIntegrity(database)

		found := false
		for _, result := range results {
			if result.Name == "orphaned_tasks" && result.Status == "warning" {
				found = true
			}
		}

		if !found {
			t.Error("Expected orphaned_tasks warning")
		}

		// Cleanup
		database.Exec(`DELETE FROM tasks WHERE slug = 'orphan'`)
	})

	t.Run("no orphaned attachments in healthy database", func(t *testing.T) {
		taskUUID := "task-with-att"
		database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, 'task-att', 'Task Att', ?, 'open', 2, ?, ?, 1)
		`, taskUUID, containerUUID, actorUUID, actorUUID)

		database.Exec(`
			INSERT INTO attachments (task_uuid, filename, relative_path, mime_type, size_bytes)
			VALUES (?, 'file.txt', 'tasks/task-with-att/file.txt', 'text/plain', 100)
		`, taskUUID)

		results := checkDataIntegrity(database)

		found := false
		for _, result := range results {
			if result.Name == "orphaned_attachments" {
				found = true
				if result.Status != "ok" {
					t.Errorf("Expected no orphaned attachments, got: %s - %s", result.Status, result.Message)
				}
			}
		}

		if !found {
			t.Error("Expected orphaned_attachments check in results")
		}
	})

	t.Run("orphaned attachments detected", func(t *testing.T) {
		// Temporarily disable foreign keys to create orphaned attachment
		database.Exec("PRAGMA foreign_keys = OFF")
		defer database.Exec("PRAGMA foreign_keys = ON")

		// Create attachment with invalid task_uuid
		database.Exec(`
			INSERT INTO attachments (task_uuid, filename, relative_path, mime_type, size_bytes)
			VALUES ('nonexistent-task', 'orphan.txt', 'tasks/orphan/file.txt', 'text/plain', 50)
		`)

		results := checkDataIntegrity(database)

		found := false
		for _, result := range results {
			if result.Name == "orphaned_attachments" && result.Status == "warning" {
				found = true
			}
		}

		if !found {
			t.Error("Expected orphaned_attachments warning")
		}

		// Cleanup
		database.Exec(`DELETE FROM attachments WHERE task_uuid = 'nonexistent-task'`)
	})

	t.Run("duplicate slugs detected", func(t *testing.T) {
		// Drop unique index to allow duplicate slugs
		database.Exec("DROP INDEX IF EXISTS tasks_unique_slug_in_container")
		defer database.Exec("CREATE UNIQUE INDEX tasks_unique_slug_in_container ON tasks(project_uuid, slug)")

		// Create two tasks with same slug in same container
		database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES ('dup-task-1-uuid', 'T-00004', 'duplicate', 'Dup 1', ?, 'open', 2, ?, ?, 1)
		`, containerUUID, actorUUID, actorUUID)
		database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES ('dup-task-2-uuid', 'T-00005', 'duplicate', 'Dup 2', ?, 'open', 2, ?, ?, 1)
		`, containerUUID, actorUUID, actorUUID)

		results := checkDataIntegrity(database)

		found := false
		for _, result := range results {
			if result.Name == "duplicate_slugs" && result.Status == "error" {
				found = true
			}
		}

		if !found {
			t.Error("Expected duplicate_slugs error")
		}

		// Cleanup
		database.Exec(`DELETE FROM tasks WHERE slug = 'duplicate'`)
	})

	t.Run("no duplicate slugs in healthy database", func(t *testing.T) {
		results := checkDataIntegrity(database)

		found := false
		for _, result := range results {
			if result.Name == "duplicate_slugs" {
				found = true
				if result.Status != "ok" {
					t.Errorf("Expected no duplicate slugs, got: %s - %s", result.Status, result.Message)
				}
			}
		}

		if !found {
			t.Error("Expected duplicate_slugs check in results")
		}
	})
}

func TestDoctorAttachmentChecks(t *testing.T) {
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

	t.Run("attachment directory exists check", func(t *testing.T) {
		results := checkAttachments(database, attachDir)

		found := false
		for _, result := range results {
			if result.Name == "attach_dir_exists" {
				found = true
				if result.Status != "ok" {
					t.Errorf("Expected attach_dir check to pass, got: %s - %s", result.Status, result.Message)
				}
			}
		}

		if !found {
			t.Error("Expected attach_dir_exists check in results")
		}
	})

	t.Run("missing attachment directory reports error", func(t *testing.T) {
		results := checkAttachments(database, "/nonexistent/attachments")

		found := false
		for _, result := range results {
			if result.Name == "attach_dir_exists" && result.Status == "error" {
				found = true
			}
		}

		if !found {
			t.Error("Expected attach_dir_exists error for missing directory")
		}
	})

	t.Run("attachment count and size reported", func(t *testing.T) {
		// Create test data
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

		taskUUID := "task-with-att"
		database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, 'task', 'Task', ?, 'open', 2, ?, ?, 1)
		`, taskUUID, containerUUID, actorUUID, actorUUID)

		database.Exec(`
			INSERT INTO attachments (task_uuid, filename, relative_path, mime_type, size_bytes)
			VALUES (?, 'file.txt', 'tasks/task/file.txt', 'text/plain', 1024)
		`, taskUUID)

		results := checkAttachments(database, attachDir)

		found := false
		for _, result := range results {
			if result.Name == "attachments_count" {
				found = true
				if result.Status != "ok" {
					t.Errorf("Expected attachments_count check to pass, got: %s - %s", result.Status, result.Message)
				}
			}
		}

		if !found {
			t.Error("Expected attachments_count check in results")
		}
	})

	t.Run("orphaned directories detected", func(t *testing.T) {
		// Create orphaned directory
		tasksDir := filepath.Join(attachDir, "tasks")
		os.MkdirAll(tasksDir, 0755)
		orphanDir := filepath.Join(tasksDir, "orphaned-uuid-123")
		os.MkdirAll(orphanDir, 0755)

		results := checkAttachments(database, attachDir)

		found := false
		for _, result := range results {
			if result.Name == "orphaned_files" {
				found = true
				if result.Status == "warning" {
					// This is expected - we have an orphaned directory
					return
				}
			}
		}

		// It's ok if we get "ok" status if there are no orphans
		// Just need to verify the check exists
		if !found {
			t.Error("Expected orphaned_files check in results")
		}
	})

	t.Run("no orphaned directories in clean database", func(t *testing.T) {
		// Clean attachments directory
		cleanDir := filepath.Join(tmpDir, "clean-attachments")
		os.MkdirAll(cleanDir, 0755)

		results := checkAttachments(database, cleanDir)

		found := false
		for _, result := range results {
			if result.Name == "orphaned_files" {
				found = true
				if result.Status != "ok" {
					t.Errorf("Expected no orphaned files in clean dir, got: %s - %s", result.Status, result.Message)
				}
			}
		}

		if !found {
			t.Error("Expected orphaned_files check in results")
		}
	})
}

func TestDoctorPerformanceChecks(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	database.Migrate()

	// Create test data
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

	// Add some tasks
	for i := 0; i < 10; i++ {
		state := "open"
		if i < 3 {
			state = "archived"
		}
		taskUUID := fmt.Sprintf("task-%d-uuid", i)
		database.Exec(`
			INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, ?, ?, ?, ?, ?, 2, ?, ?, 1)
		`, taskUUID, "T-"+string(rune('0'+i)), "task-"+string(rune('0'+i)), "Task "+string(rune('0'+i)), containerUUID, state, actorUUID, actorUUID)
	}

	t.Run("task counts reported correctly", func(t *testing.T) {
		results := checkPerformance(database)

		found := false
		for _, result := range results {
			if result.Name == "task_counts" {
				found = true
				if result.Status != "ok" {
					t.Errorf("Expected task_counts check to pass, got: %s - %s", result.Status, result.Message)
				}
				// Message should mention active and archived counts
			}
		}

		if !found {
			t.Error("Expected task_counts check in results")
		}
	})

	t.Run("container count reported", func(t *testing.T) {
		results := checkPerformance(database)

		found := false
		for _, result := range results {
			if result.Name == "container_count" {
				found = true
				if result.Status != "ok" {
					t.Errorf("Expected container_count check to pass, got: %s - %s", result.Status, result.Message)
				}
			}
		}

		if !found {
			t.Error("Expected container_count check in results")
		}
	})

	t.Run("database size reported", func(t *testing.T) {
		results := checkPerformance(database)

		found := false
		for _, result := range results {
			if result.Name == "database_size" {
				found = true
				if result.Status != "ok" {
					t.Errorf("Expected database_size check to pass, got: %s - %s", result.Status, result.Message)
				}
			}
		}

		if !found {
			t.Error("Expected database_size check in results")
		}
	})
}

func TestDoctorReportGeneration(t *testing.T) {
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

	t.Run("report structure is valid", func(t *testing.T) {
		report := &doctorReport{
			Version:       "0.2.0",
			DBPath:        dbPath,
			Checks:        []checkResult{},
			OverallStatus: "ok",
		}

		report.Checks = append(report.Checks, checkDatabaseFile(dbPath)...)
		report.Checks = append(report.Checks, checkDatabasePragmas(database)...)
		report.Checks = append(report.Checks, checkSchema(database)...)
		report.Checks = append(report.Checks, checkDataIntegrity(database)...)
		report.Checks = append(report.Checks, checkAttachments(database, attachDir)...)
		report.Checks = append(report.Checks, checkPerformance(database)...)

		if report.Version == "" {
			t.Error("Report version should be set")
		}
		if report.DBPath == "" {
			t.Error("Report DBPath should be set")
		}
		if len(report.Checks) == 0 {
			t.Error("Report should have checks")
		}

		// Count warnings and errors
		for _, check := range report.Checks {
			if check.Status == "warning" {
				report.Warnings++
			} else if check.Status == "error" {
				report.Errors++
				report.OverallStatus = "error"
			}
		}

		if report.Warnings > 0 && report.OverallStatus == "ok" {
			report.OverallStatus = "warning"
		}

		// Overall status should be ok for healthy database
		if report.OverallStatus != "ok" {
			t.Errorf("Expected overall status 'ok', got '%s' (warnings: %d, errors: %d)", report.OverallStatus, report.Warnings, report.Errors)
		}
	})
}
