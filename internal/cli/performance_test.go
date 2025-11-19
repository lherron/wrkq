package cli

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/lherron/todo/internal/db"
)

// TestPerformance_List5kTasks tests that listing 5000 tasks completes under 200ms p95
func TestPerformance_List5kTasks(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	database, _ := setupPerfTestEnv(t, 5000)

	// Run multiple iterations to get stable timing
	iterations := 20
	timings := make([]time.Duration, iterations)

	for i := 0; i < iterations; i++ {
		start := time.Now()

		rows, err := database.Query(`
			SELECT id, slug, title, state, priority, etag
			FROM tasks
			WHERE state != 'archived'
			ORDER BY priority, created_at
		`)
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}

		count := 0
		for rows.Next() {
			var id, slug, title, state string
			var priority int
			var etag int64
			if err := rows.Scan(&id, &slug, &title, &state, &priority, &etag); err != nil {
				rows.Close()
				t.Fatalf("Scan failed: %v", err)
			}
			count++
		}
		rows.Close()

		elapsed := time.Since(start)
		timings[i] = elapsed

		if count != 5000 {
			t.Errorf("Expected 5000 tasks, got %d", count)
		}
	}

	// Calculate percentiles
	p50, p95, p99 := calculatePercentiles(timings)

	t.Logf("Performance results for listing 5k tasks:")
	t.Logf("  p50: %v", p50)
	t.Logf("  p95: %v", p95)
	t.Logf("  p99: %v", p99)

	// M0 acceptance criteria: p95 < 200ms
	if p95 > 200*time.Millisecond {
		t.Errorf("p95 (%v) exceeds 200ms threshold", p95)
	}
}

// TestPerformance_List5kTasksJSON tests JSON serialization performance
func TestPerformance_List5kTasksJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	database, _ := setupPerfTestEnv(t, 5000)

	iterations := 10
	timings := make([]time.Duration, iterations)

	for i := 0; i < iterations; i++ {
		start := time.Now()

		rows, err := database.Query(`
			SELECT id, slug, title, state, priority
			FROM tasks
			WHERE state != 'archived'
		`)
		if err != nil {
			t.Fatalf("Query failed: %v", err)
		}

		type Task struct {
			ID       string
			Slug     string
			Title    string
			State    string
			Priority int
		}

		var tasks []Task
		for rows.Next() {
			var task Task
			if err := rows.Scan(&task.ID, &task.Slug, &task.Title, &task.State, &task.Priority); err != nil {
				rows.Close()
				t.Fatalf("Scan failed: %v", err)
			}
			tasks = append(tasks, task)
		}
		rows.Close()

		elapsed := time.Since(start)
		timings[i] = elapsed

		if len(tasks) != 5000 {
			t.Errorf("Expected 5000 tasks, got %d", len(tasks))
		}
	}

	p50, p95, p99 := calculatePercentiles(timings)

	t.Logf("Performance results for listing 5k tasks (JSON):")
	t.Logf("  p50: %v", p50)
	t.Logf("  p95: %v", p95)
	t.Logf("  p99: %v", p99)
}

// TestPerformance_CreateTask tests task creation performance
func TestPerformance_CreateTask(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	database, _ := setupPerfTestEnv(t, 0)

	iterations := 1000
	timings := make([]time.Duration, iterations)

	for i := 0; i < iterations; i++ {
		start := time.Now()

		taskUUID := fmt.Sprintf("perf-task-%d", i)
		taskID := fmt.Sprintf("T-%05d", i+1)
		taskSlug := fmt.Sprintf("task-%d", i)

		_, err := database.Exec(`
			INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, ?, ?, ?, '00000000-0000-0000-0000-000000000002', 'open', 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
		`, taskUUID, taskID, taskSlug, "Task "+string(rune(i)))
		if err != nil {
			t.Fatalf("Failed to create task: %v", err)
		}

		elapsed := time.Since(start)
		timings[i] = elapsed
	}

	p50, p95, p99 := calculatePercentiles(timings)

	t.Logf("Performance results for creating tasks:")
	t.Logf("  p50: %v", p50)
	t.Logf("  p95: %v", p95)
	t.Logf("  p99: %v", p99)

	// Should be fast (< 10ms p95)
	if p95 > 10*time.Millisecond {
		t.Errorf("p95 (%v) exceeds 10ms threshold", p95)
	}
}

// TestPerformance_UpdateTaskWithETag tests update performance with ETag checking
func TestPerformance_UpdateTaskWithETag(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	database, _ := setupPerfTestEnv(t, 1000)

	iterations := 500
	timings := make([]time.Duration, iterations)

	for i := 0; i < iterations; i++ {
		// Pick a random task to update
		taskIndex := i % 1000
		taskUUID := fmt.Sprintf("perf-task-%d", taskIndex)

		start := time.Now()

		// Read current etag
		var currentEtag int64
		err := database.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentEtag)
		if err != nil {
			t.Fatalf("Failed to read etag: %v", err)
		}

		// Update with etag check
		newEtag := currentEtag + 1
		_, err = database.Exec(`
			UPDATE tasks
			SET priority = ?, etag = ?, updated_at = datetime('now')
			WHERE uuid = ? AND etag = ?
		`, (i%4)+1, newEtag, taskUUID, currentEtag)
		if err != nil {
			t.Fatalf("Failed to update task: %v", err)
		}

		elapsed := time.Since(start)
		timings[i] = elapsed
	}

	p50, p95, p99 := calculatePercentiles(timings)

	t.Logf("Performance results for updating tasks with ETag:")
	t.Logf("  p50: %v", p50)
	t.Logf("  p95: %v", p95)
	t.Logf("  p99: %v", p99)
}

// TestPerformance_EventLogWrite tests event log write performance
// NOTE: Commented out until events table is added to schema
/*
func TestPerformance_EventLogWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	database, _ := setupPerfTestEnv(t, 0)

	iterations := 2000
	timings := make([]time.Duration, iterations)

	for i := 0; i < iterations; i++ {
		start := time.Now()

		_, err := database.Exec(`
			INSERT INTO events (timestamp, actor_uuid, resource_type, resource_uuid, event_type, etag)
			VALUES (datetime('now'), '00000000-0000-0000-0000-000000000001', 'task', 'uuid-123', 'updated', 1)
		`)
		if err != nil {
			t.Fatalf("Failed to insert event: %v", err)
		}

		elapsed := time.Since(start)
		timings[i] = elapsed
	}

	p50, p95, p99 := calculatePercentiles(timings)

	t.Logf("Performance results for event log writes:")
	t.Logf("  p50: %v", p50)
	t.Logf("  p95: %v", p95)
	t.Logf("  p99: %v", p99)

	// Event writes should be very fast (< 5ms p95)
	if p95 > 5*time.Millisecond {
		t.Errorf("p95 (%v) exceeds 5ms threshold", p95)
	}
}
*/

// BenchmarkListTasks benchmarks task listing at different scales
func BenchmarkListTasks(b *testing.B) {
	sizes := []int{100, 1000, 5000, 10000}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("size=%d", size), func(b *testing.B) {
			database, _ := setupBenchPerfEnv(b, size)
			defer database.Close()

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				rows, err := database.Query(`
					SELECT id, slug, title, state, priority
					FROM tasks
					WHERE state != 'archived'
				`)
				if err != nil {
					b.Fatalf("Query failed: %v", err)
				}

				count := 0
				for rows.Next() {
					var id, slug, title, state string
					var priority int
					rows.Scan(&id, &slug, &title, &state, &priority)
					count++
				}
				rows.Close()

				if count != size {
					b.Errorf("Expected %d tasks, got %d", size, count)
				}
			}
		})
	}
}

// BenchmarkCreateTask benchmarks task creation
func BenchmarkCreateTask(b *testing.B) {
	database, _ := setupBenchPerfEnv(b, 0)
	defer database.Close()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		taskUUID := fmt.Sprintf("bench-task-%d", i)
		taskID := fmt.Sprintf("T-%05d", i+1)
		taskSlug := fmt.Sprintf("bench-task-%d", i)

		_, err := database.Exec(`
			INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, ?, ?, ?, '00000000-0000-0000-0000-000000000002', 'open', 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
		`, taskUUID, taskID, taskSlug, "Benchmark Task")
		if err != nil {
			b.Fatalf("Failed to create task: %v", err)
		}
	}
}

// Helper functions

func setupPerfTestEnv(t *testing.T, numTasks int) (*db.DB, string) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "perf-test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	if err := database.Migrate(); err != nil {
		database.Close()
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Seed default actor
	_, err = database.Exec(`
		INSERT INTO actors (uuid, id, slug, display_name, role, created_at, updated_at)
		VALUES ('00000000-0000-0000-0000-000000000001', 'A-00001', 'test-user', 'Test User', 'human', datetime('now'), datetime('now'))
	`)
	if err != nil {
		database.Close()
		t.Fatalf("Failed to seed actor: %v", err)
	}

	// Seed inbox project
	_, err = database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000002', 'P-00001', 'inbox', 'Inbox', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		database.Close()
		t.Fatalf("Failed to seed inbox: %v", err)
	}

	// Create test tasks
	if numTasks > 0 {
		t.Logf("Creating %d test tasks...", numTasks)

		// Use a transaction for faster bulk insert
		tx, err := database.Begin()
		if err != nil {
			database.Close()
			t.Fatalf("Failed to begin transaction: %v", err)
		}

		stmt, err := tx.Prepare(`
			INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, ?, ?, ?, '00000000-0000-0000-0000-000000000002', 'open', ?, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
		`)
		if err != nil {
			tx.Rollback()
			database.Close()
			t.Fatalf("Failed to prepare statement: %v", err)
		}

		for i := 0; i < numTasks; i++ {
			taskUUID := fmt.Sprintf("perf-task-%d", i)
			taskID := fmt.Sprintf("T-%05d", i+1)
			taskSlug := fmt.Sprintf("task-%d", i)
			priority := (i % 4) + 1 // Distribute across priorities 1-4

			_, err := stmt.Exec(taskUUID, taskID, taskSlug, fmt.Sprintf("Task %d", i+1), priority)
			if err != nil {
				stmt.Close()
				tx.Rollback()
				database.Close()
				t.Fatalf("Failed to insert task %d: %v", i, err)
			}

			if (i+1)%1000 == 0 {
				t.Logf("Created %d/%d tasks", i+1, numTasks)
			}
		}

		stmt.Close()
		if err := tx.Commit(); err != nil {
			database.Close()
			t.Fatalf("Failed to commit transaction: %v", err)
		}

		t.Logf("Created %d test tasks", numTasks)
	}

	t.Cleanup(func() {
		database.Close()
	})

	return database, dbPath
}

func setupBenchPerfEnv(b *testing.B, numTasks int) (*sql.DB, string) {
	b.Helper()

	tmpDir := b.TempDir()
	dbPath := filepath.Join(tmpDir, "bench-perf.db")

	database, err := db.Open(dbPath)
	if err != nil {
		b.Fatalf("Failed to create bench database: %v", err)
	}

	if err := database.Migrate(); err != nil {
		database.Close()
		b.Fatalf("Failed to run migrations: %v", err)
	}

	// Seed data
	database.Exec(`
		INSERT INTO actors (uuid, id, slug, display_name, role, created_at, updated_at)
		VALUES ('00000000-0000-0000-0000-000000000001', 'A-00001', 'bench-user', 'Bench User', 'human', datetime('now'), datetime('now'))
	`)

	database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000002', 'P-00001', 'inbox', 'Inbox', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)

	// Create tasks
	if numTasks > 0 {
		tx, _ := database.Begin()
		stmt, _ := tx.Prepare(`
			INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, ?, ?, ?, '00000000-0000-0000-0000-000000000002', 'open', 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
		`)

		for i := 0; i < numTasks; i++ {
			taskUUID := fmt.Sprintf("bench-task-%d", i)
			taskID := fmt.Sprintf("T-%05d", i+1)
			taskSlug := fmt.Sprintf("task-%d", i)
			stmt.Exec(taskUUID, taskID, taskSlug, fmt.Sprintf("Task %d", i+1))
		}

		stmt.Close()
		tx.Commit()
	}

	return database.DB, dbPath
}

func calculatePercentiles(timings []time.Duration) (p50, p95, p99 time.Duration) {
	// Sort timings
	sorted := make([]time.Duration, len(timings))
	copy(sorted, timings)

	// Simple bubble sort (fine for small datasets)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[i] > sorted[j] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}

	n := len(sorted)
	p50 = sorted[n*50/100]
	p95 = sorted[n*95/100]
	p99 = sorted[n*99/100]

	return
}
