package cli

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/domain"
)

// TestConcurrentWrites tests that concurrent writes detect ETag conflicts
func TestConcurrentWrites_ETagConflict(t *testing.T) {
	database, _ := setupTestEnv(t)

	// Create a test task
	taskUUID := "00000000-0000-0000-0000-000000000003"
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES (?, 'T-00001', 'test-task', 'Test Task', '00000000-0000-0000-0000-000000000002', 'open', 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`, taskUUID)
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Two goroutines try to update the same task with the same etag
	var wg sync.WaitGroup
	var successCount int
	var failureCount int
	var mu sync.Mutex

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Read current etag
			var currentEtag int64
			err := database.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentEtag)
			if err != nil {
				t.Errorf("Worker %d: Failed to read etag: %v", workerID, err)
				return
			}

			// Small delay to increase chance of race condition
			time.Sleep(10 * time.Millisecond)

			// Try to update with the etag we read
			newPriority := workerID + 1
			newEtag := currentEtag + 1

			result, err := database.Exec(`
				UPDATE tasks
				SET priority = ?, etag = ?, updated_at = datetime('now')
				WHERE uuid = ? AND etag = ?
			`, newPriority, newEtag, taskUUID, currentEtag)

			if err != nil {
				t.Errorf("Worker %d: Update failed: %v", workerID, err)
				return
			}

			rowsAffected, err := result.RowsAffected()
			if err != nil {
				t.Errorf("Worker %d: Failed to get rows affected: %v", workerID, err)
				return
			}

			mu.Lock()
			if rowsAffected == 0 {
				// ETag mismatch - update was rejected
				failureCount++
				t.Logf("Worker %d: Update rejected (ETag conflict)", workerID)
			} else {
				// Update succeeded
				successCount++
				t.Logf("Worker %d: Update succeeded", workerID)
			}
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// Exactly one update should succeed, one should fail due to ETag conflict
	if successCount != 1 {
		t.Errorf("Expected exactly 1 successful update, got %d", successCount)
	}
	if failureCount != 1 {
		t.Errorf("Expected exactly 1 failed update (ETag conflict), got %d", failureCount)
	}

	// Verify final etag is incremented by 1 (not 2)
	var finalEtag int64
	err = database.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", taskUUID).Scan(&finalEtag)
	if err != nil {
		t.Fatalf("Failed to read final etag: %v", err)
	}

	if finalEtag != 2 {
		t.Errorf("Expected final etag to be 2 (initial 1 + 1 successful update), got %d", finalEtag)
	}
}

// TestConcurrentReads tests that concurrent reads don't interfere with each other
func TestConcurrentReads(t *testing.T) {
	database, _ := setupTestEnv(t)

	// Create test tasks
	for i := 1; i <= 100; i++ {
		taskUUID := fmt.Sprintf("concurrent-read-task-%d", i)
		taskID := fmt.Sprintf("T-%05d", i)
		taskSlug := fmt.Sprintf("task-%d", i)
		taskTitle := fmt.Sprintf("Task %d", i)

		_, err := database.Exec(`
			INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, ?, ?, ?, '00000000-0000-0000-0000-000000000002', 'open', 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
		`, taskUUID, taskID, taskSlug, taskTitle)
		if err != nil {
			t.Fatalf("Failed to create task %d: %v", i, err)
		}
	}

	// Multiple goroutines read tasks concurrently
	var wg sync.WaitGroup
	numReaders := 10

	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()

			rows, err := database.Query("SELECT COUNT(*) FROM tasks WHERE state = 'open'")
			if err != nil {
				t.Errorf("Reader %d: Query failed: %v", readerID, err)
				return
			}
			defer rows.Close()

			var count int
			if rows.Next() {
				if err := rows.Scan(&count); err != nil {
					t.Errorf("Reader %d: Scan failed: %v", readerID, err)
					return
				}
			}

			if count != 100 {
				t.Errorf("Reader %d: Expected 100 tasks, got %d", readerID, count)
			}
		}(i)
	}

	wg.Wait()
}

// TestETagCheckFunction tests the domain.CheckETag function
func TestETagCheckFunction(t *testing.T) {
	tests := []struct {
		name     string
		expected int64
		actual   int64
		wantErr  bool
	}{
		{
			name:     "matching etags",
			expected: 5,
			actual:   5,
			wantErr:  false,
		},
		{
			name:     "mismatched etags",
			expected: 5,
			actual:   6,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := domain.CheckETag(tt.expected, tt.actual)
			if tt.wantErr && err == nil {
				t.Error("Expected ETag error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Unexpected ETag error: %v", err)
			}
		})
	}
}

// TestConcurrentWritesToDifferentTasks tests that writes to different tasks don't interfere
func TestConcurrentWritesToDifferentTasks(t *testing.T) {
	database, _ := setupTestEnv(t)

	// Create multiple test tasks
	numTasks := 10
	taskUUIDs := make([]string, numTasks)
	for i := 0; i < numTasks; i++ {
		taskUUID := fmt.Sprintf("task-uuid-%d", i)
		taskUUIDs[i] = taskUUID

		taskID := fmt.Sprintf("T-%05d", i+1)
		taskSlug := fmt.Sprintf("task-%d", i)
		taskTitle := fmt.Sprintf("Task %d", i)

		_, err := database.Exec(`
			INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, ?, ?, ?, '00000000-0000-0000-0000-000000000002', 'open', 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
		`, taskUUID, taskID, taskSlug, taskTitle)
		if err != nil {
			t.Fatalf("Failed to create task %d: %v", i, err)
		}
	}

	// Each goroutine updates a different task
	var wg sync.WaitGroup
	errors := make(chan error, numTasks)

	for i := 0; i < numTasks; i++ {
		wg.Add(1)
		go func(taskIndex int) {
			defer wg.Done()

			taskUUID := taskUUIDs[taskIndex]

			// Read current etag
			var currentEtag int64
			err := database.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentEtag)
			if err != nil {
				errors <- err
				return
			}

			// Update the task
			newEtag := currentEtag + 1
			result, err := database.Exec(`
				UPDATE tasks
				SET priority = ?, etag = ?, updated_at = datetime('now')
				WHERE uuid = ? AND etag = ?
			`, 1, newEtag, taskUUID, currentEtag)

			if err != nil {
				errors <- err
				return
			}

			rowsAffected, err := result.RowsAffected()
			if err != nil {
				errors <- err
				return
			}

			if rowsAffected != 1 {
				t.Errorf("Task %d: Expected 1 row affected, got %d", taskIndex, rowsAffected)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent write error: %v", err)
	}

	// Verify all tasks were updated
	var count int
	err := database.QueryRow("SELECT COUNT(*) FROM tasks WHERE priority = 1 AND etag = 2").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count updated tasks: %v", err)
	}

	if count != numTasks {
		t.Errorf("Expected %d tasks to be updated, got %d", numTasks, count)
	}
}

// TestEventLogConcurrency tests that event log writes are concurrent-safe
// NOTE: This test is commented out because the events table is not yet in the schema
// Uncomment when the events table is added to migrations
/*
func TestEventLogConcurrency(t *testing.T) {
	database, _ := setupTestEnv(t)

	// Multiple goroutines write events concurrently
	var wg sync.WaitGroup
	numWriters := 20
	eventsPerWriter := 10

	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()

			for j := 0; j < eventsPerWriter; j++ {
				_, err := database.Exec(`
					INSERT INTO events (timestamp, actor_uuid, resource_type, event_type)
					VALUES (datetime('now'), '00000000-0000-0000-0000-000000000001', 'task', 'created')
				`)
				if err != nil {
					t.Errorf("Writer %d: Failed to insert event %d: %v", writerID, j, err)
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify all events were written
	var count int
	err := database.QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count events: %v", err)
	}

	expectedCount := numWriters * eventsPerWriter
	if count != expectedCount {
		t.Errorf("Expected %d events, got %d", expectedCount, count)
	}

	// Verify event IDs are unique and sequential
	rows, err := database.Query("SELECT id FROM events ORDER BY id")
	if err != nil {
		t.Fatalf("Failed to query event IDs: %v", err)
	}
	defer rows.Close()

	seenIDs := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("Failed to scan event ID: %v", err)
		}

		if seenIDs[id] {
			t.Errorf("Duplicate event ID: %d", id)
		}
		seenIDs[id] = true
	}
}
*/

// BenchmarkConcurrentWrites benchmarks concurrent write performance
// NOTE: Commented out until events table is in schema
/*
func BenchmarkConcurrentWrites(b *testing.B) {
	// Setup
	tmpDir := b.TempDir()
	database, _ := setupBenchDB(b, tmpDir)
	defer database.Close()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		var wg sync.WaitGroup
		numWriters := 10

		for j := 0; j < numWriters; j++ {
			wg.Add(1)
			go func(writerID int) {
				defer wg.Done()

				_, err := database.Exec(`
					INSERT INTO events (timestamp, actor_uuid, resource_type, event_type)
					VALUES (datetime('now'), '00000000-0000-0000-0000-000000000001', 'task', 'created')
				`)
				if err != nil {
					b.Errorf("Failed to insert event: %v", err)
				}
			}(j)
		}

		wg.Wait()
	}
}

// Helper function for benchmark setup
func setupBenchDB(b *testing.B, tmpDir string) (*sql.DB, string) {
	b.Helper()

	dbPath := tmpDir + "/bench.db"
	database, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		b.Fatalf("Failed to create bench database: %v", err)
	}

	// Apply pragmas
	database.Exec("PRAGMA journal_mode = WAL")
	database.Exec("PRAGMA foreign_keys = ON")

	// Create minimal schema for benchmarking
	_, err = database.Exec(`
		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp TEXT NOT NULL,
			actor_uuid TEXT,
			resource_type TEXT NOT NULL,
			event_type TEXT NOT NULL
		)
	`)
	if err != nil {
		b.Fatalf("Failed to create events table: %v", err)
	}

	return database, dbPath
}
*/
