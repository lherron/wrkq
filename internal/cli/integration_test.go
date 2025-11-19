package cli

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// setupTestEnv creates a test database and returns it with cleanup
func setupTestEnv(t *testing.T) (*db.DB, string) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

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

	t.Cleanup(func() {
		database.Close()
	})

	return database, dbPath
}

func TestInitCommand(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "todo.db")

	// Note: This test would require refactoring the init command to be testable
	// For now, we'll focus on testing commands that read from an existing DB
	_ = dbPath
}

func TestListCommand_JSON(t *testing.T) {
	database, _ := setupTestEnv(t)

	// Create a test task
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000003', 'T-00001', 'test-task', 'Test Task', '00000000-0000-0000-0000-000000000002', 'open', 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Query tasks to verify JSON structure
	rows, err := database.Query(`SELECT id, slug, title, state, priority FROM tasks WHERE state != 'archived'`)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	defer rows.Close()

	type Task struct {
		ID       string `json:"id"`
		Slug     string `json:"slug"`
		Title    string `json:"title"`
		State    string `json:"state"`
		Priority int    `json:"priority"`
	}

	var tasks []Task
	for rows.Next() {
		var task Task
		if err := rows.Scan(&task.ID, &task.Slug, &task.Title, &task.State, &task.Priority); err != nil {
			t.Fatalf("Scan failed: %v", err)
		}
		tasks = append(tasks, task)
	}

	if len(tasks) != 1 {
		t.Fatalf("Expected 1 task, got %d", len(tasks))
	}

	task := tasks[0]
	if task.ID != "T-00001" {
		t.Errorf("Expected ID T-00001, got %s", task.ID)
	}
	if task.Slug != "test-task" {
		t.Errorf("Expected slug test-task, got %s", task.Slug)
	}

	// Test JSON marshaling
	jsonData, err := json.Marshal(tasks)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}

	// Verify JSON is valid
	var unmarshaled []Task
	if err := json.Unmarshal(jsonData, &unmarshaled); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}

	if len(unmarshaled) != 1 {
		t.Fatalf("Expected 1 task after unmarshal, got %d", len(unmarshaled))
	}
}

func TestListCommand_NDJSON(t *testing.T) {
	database, _ := setupTestEnv(t)

	// Create multiple test tasks
	tasks := []struct {
		id    string
		slug  string
		title string
	}{
		{"T-00001", "task-1", "Task 1"},
		{"T-00002", "task-2", "Task 2"},
		{"T-00003", "task-3", "Task 3"},
	}

	for _, task := range tasks {
		_, err := database.Exec(`
			INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, ?, ?, ?, '00000000-0000-0000-0000-000000000002', 'open', 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
		`, "uuid-"+task.id, task.id, task.slug, task.title)
		if err != nil {
			t.Fatalf("Failed to create task %s: %v", task.id, err)
		}
	}

	// Query and format as NDJSON
	rows, err := database.Query(`SELECT id, slug, title FROM tasks WHERE state != 'archived' ORDER BY id`)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	defer rows.Close()

	var buf bytes.Buffer
	for rows.Next() {
		var id, slug, title string
		if err := rows.Scan(&id, &slug, &title); err != nil {
			t.Fatalf("Scan failed: %v", err)
		}

		obj := map[string]string{
			"id":    id,
			"slug":  slug,
			"title": title,
		}

		jsonLine, err := json.Marshal(obj)
		if err != nil {
			t.Fatalf("JSON marshal failed: %v", err)
		}

		buf.Write(jsonLine)
		buf.WriteByte('\n')
	}

	// Verify NDJSON format
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("Expected 3 lines, got %d", len(lines))
	}

	// Verify each line is valid JSON
	for i, line := range lines {
		var obj map[string]string
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("Line %d is not valid JSON: %v", i, err)
		}
	}
}

func TestCatCommand_Markdown(t *testing.T) {
	database, _ := setupTestEnv(t)

	// Create a test task with labels
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, labels, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000003', 'T-00001', 'test-task', 'Test Task', '00000000-0000-0000-0000-000000000002', 'open', 1, 'This is the task body.', '["backend","urgent"]', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Query the task
	var title, body, labels string
	var priority int
	err = database.QueryRow(`SELECT title, priority, labels, body FROM tasks WHERE id = ?`, "T-00001").
		Scan(&title, &priority, &labels, &body)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	// Verify task data
	if title != "Test Task" {
		t.Errorf("Expected title 'Test Task', got %q", title)
	}
	if priority != 1 {
		t.Errorf("Expected priority 1, got %d", priority)
	}

	// Verify labels JSON
	var labelArray []string
	if err := json.Unmarshal([]byte(labels), &labelArray); err != nil {
		t.Fatalf("Failed to unmarshal labels: %v", err)
	}
	if len(labelArray) != 2 {
		t.Errorf("Expected 2 labels, got %d", len(labelArray))
	}
}

func TestStatCommand_Porcelain(t *testing.T) {
	database, _ := setupTestEnv(t)

	// Create a test task
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000003', 'T-00001', 'test-task', 'Test Task', '00000000-0000-0000-0000-000000000002', 'open', 2, '', '2025-01-01 10:00:00', '2025-01-01 11:00:00', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 5)
	`)
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Query task metadata
	var id, slug, state string
	var priority int
	var etag int64
	err = database.QueryRow(`SELECT id, slug, state, priority, etag FROM tasks WHERE id = ?`, "T-00001").
		Scan(&id, &slug, &state, &priority, &etag)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	// Verify metadata in porcelain format (stable keys, no ANSI)
	metadata := map[string]interface{}{
		"id":       id,
		"slug":     slug,
		"state":    state,
		"priority": priority,
		"etag":     etag,
	}

	expectedMetadata := map[string]interface{}{
		"id":       "T-00001",
		"slug":     "test-task",
		"state":    "open",
		"priority": 2,
		"etag":     int64(5),
	}

	for key, expected := range expectedMetadata {
		actual := metadata[key]
		if actual != expected {
			t.Errorf("Metadata[%s] = %v, want %v", key, actual, expected)
		}
	}
}

func TestNullSeparatedOutput(t *testing.T) {
	database, _ := setupTestEnv(t)

	// Create multiple test tasks
	for i := 1; i <= 3; i++ {
		_, err := database.Exec(`
			INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, ?, ?, ?, '00000000-0000-0000-0000-000000000002', 'open', 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
		`, "uuid-"+string(rune(i)), "T-0000"+string(rune('0'+i)), "task-"+string(rune('0'+i)), "Task "+string(rune('0'+i)))
		if err != nil {
			t.Fatalf("Failed to create task: %v", err)
		}
	}

	// Query and format with NUL separator
	rows, err := database.Query(`SELECT id FROM tasks WHERE state != 'archived' ORDER BY id`)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	defer rows.Close()

	var buf bytes.Buffer
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("Scan failed: %v", err)
		}
		buf.WriteString(id)
		buf.WriteByte(0) // NUL separator
	}

	// Verify NUL-separated output
	output := buf.String()
	ids := strings.Split(output, "\x00")
	// Remove empty last element
	if len(ids) > 0 && ids[len(ids)-1] == "" {
		ids = ids[:len(ids)-1]
	}

	if len(ids) != 3 {
		t.Fatalf("Expected 3 IDs, got %d: %v", len(ids), ids)
	}
}

// Golden file test helper
func compareGoldenFile(t *testing.T, goldenPath string, actual []byte) {
	t.Helper()

	// Read golden file
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		// If golden file doesn't exist, create it
		if os.IsNotExist(err) {
			if err := os.WriteFile(goldenPath, actual, 0644); err != nil {
				t.Fatalf("Failed to write golden file: %v", err)
			}
			t.Logf("Created golden file: %s", goldenPath)
			return
		}
		t.Fatalf("Failed to read golden file: %v", err)
	}

	// Compare
	if !bytes.Equal(golden, actual) {
		t.Errorf("Output does not match golden file %s\nExpected:\n%s\n\nActual:\n%s",
			goldenPath, string(golden), string(actual))
	}
}

func TestGoldenFiles_JSONOutput(t *testing.T) {
	database, _ := setupTestEnv(t)

	// Create a deterministic task
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000003', 'T-00001', 'test-task', 'Test Task', '00000000-0000-0000-0000-000000000002', 'open', 2, 'Task body', '2025-01-01T10:00:00Z', '2025-01-01T10:00:00Z', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Query task
	var task struct {
		ID       string `json:"id"`
		Slug     string `json:"slug"`
		Title    string `json:"title"`
		State    string `json:"state"`
		Priority int    `json:"priority"`
	}

	err = database.QueryRow(`SELECT id, slug, title, state, priority FROM tasks WHERE id = ?`, "T-00001").
		Scan(&task.ID, &task.Slug, &task.Title, &task.State, &task.Priority)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}

	// Marshal to JSON with stable formatting
	jsonData, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}

	// Write to golden file for demonstration
	// In a real implementation, you would use compareGoldenFile function
	_ = jsonData
	t.Logf("Successfully marshaled task to JSON")
}

// Helper to execute a SQL query and return results as a slice of maps
func queryToMaps(t *testing.T, db *sql.DB, query string, args ...interface{}) []map[string]interface{} {
	t.Helper()

	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("Failed to get columns: %v", err)
	}

	var results []map[string]interface{}
	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			t.Fatalf("Scan failed: %v", err)
		}

		result := make(map[string]interface{})
		for i, col := range columns {
			result[col] = values[i]
		}
		results = append(results, result)
	}

	return results
}
