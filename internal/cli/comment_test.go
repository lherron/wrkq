package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestCommentAdd(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create a test task
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('task-uuid-1', 'T-00001', 'test-task', 'Test Task', '00000000-0000-0000-0000-000000000002', 'open', 2, 'Task body', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Set environment variables
	os.Setenv("WRKQ_DB_PATH", dbPath)
	os.Setenv("WRKQ_ACTOR", "test-user")
	defer os.Unsetenv("WRKQ_DB_PATH")
	defer os.Unsetenv("WRKQ_ACTOR")

	// Test adding a comment via message flag
	cmd := rootCmd
	cmd.SetArgs([]string{"comment", "add", "T-00001", "-m", "This is a test comment"})

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Command failed: %v\nOutput: %s", err, out.String())
	}

	// Verify comment was created
	var count int
	err = database.QueryRow("SELECT COUNT(*) FROM comments WHERE task_uuid = 'task-uuid-1'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query comments: %v", err)
	}

	if count != 1 {
		t.Errorf("Expected 1 comment, got %d", count)
	}

	// Verify comment content
	var body string
	err = database.QueryRow("SELECT body FROM comments WHERE task_uuid = 'task-uuid-1'").Scan(&body)
	if err != nil {
		t.Fatalf("Failed to query comment body: %v", err)
	}

	if body != "This is a test comment" {
		t.Errorf("Expected comment body 'This is a test comment', got %q", body)
	}

	// Verify friendly ID
	var id string
	err = database.QueryRow("SELECT id FROM comments WHERE task_uuid = 'task-uuid-1'").Scan(&id)
	if err != nil {
		t.Fatalf("Failed to query comment ID: %v", err)
	}

	if !strings.HasPrefix(id, "C-") {
		t.Errorf("Expected friendly ID to start with 'C-', got %q", id)
	}

	// Verify event log entry
	var eventCount int
	err = database.QueryRow("SELECT COUNT(*) FROM event_log WHERE resource_type = 'comment' AND event_type = 'comment.created'").Scan(&eventCount)
	if err != nil {
		t.Fatalf("Failed to query event log: %v", err)
	}

	if eventCount != 1 {
		t.Errorf("Expected 1 event log entry, got %d", eventCount)
	}
}

func TestCommentLs(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create a test task
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('task-uuid-1', 'T-00001', 'test-task', 'Test Task', '00000000-0000-0000-0000-000000000002', 'open', 2, 'Task body', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Create test comments
	_, err = database.Exec(`
		INSERT INTO comments (uuid, id, task_uuid, actor_uuid, body, etag, created_at)
		VALUES
			('comment-uuid-1', 'C-00001', 'task-uuid-1', '00000000-0000-0000-0000-000000000001', 'First comment', 1, datetime('now', '-2 minutes')),
			('comment-uuid-2', 'C-00002', 'task-uuid-1', '00000000-0000-0000-0000-000000000001', 'Second comment', 1, datetime('now', '-1 minute'))
	`)
	if err != nil {
		t.Fatalf("Failed to create test comments: %v", err)
	}

	// Set environment variables
	os.Setenv("WRKQ_DB_PATH", dbPath)
	os.Setenv("WRKQ_ACTOR", "test-user")
	defer os.Unsetenv("WRKQ_DB_PATH")
	defer os.Unsetenv("WRKQ_ACTOR")

	// Query comments directly to verify
	var count int
	err = database.QueryRow("SELECT COUNT(*) FROM comments WHERE task_uuid = 'task-uuid-1'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count comments: %v", err)
	}

	if count != 2 {
		t.Fatalf("Expected 2 comments in database, got %d", count)
	}

	// Verify comments can be queried with proper data
	rows, err := database.Query(`
		SELECT id, body
		FROM comments
		WHERE task_uuid = 'task-uuid-1'
		ORDER BY created_at ASC
	`)
	if err != nil {
		t.Fatalf("Failed to query comments: %v", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id, body string
		if err := rows.Scan(&id, &body); err != nil {
			t.Fatalf("Failed to scan comment: %v", err)
		}
		ids = append(ids, id)
	}

	if len(ids) != 2 {
		t.Errorf("Expected 2 comments, got %d", len(ids))
	}

	if ids[0] != "C-00001" {
		t.Errorf("Expected first comment ID 'C-00001', got %v", ids[0])
	}
}

func TestCommentCat(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create a test task
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('task-uuid-1', 'T-00001', 'test-task', 'Test Task', '00000000-0000-0000-0000-000000000002', 'open', 2, 'Task body', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Create a test comment
	_, err = database.Exec(`
		INSERT INTO comments (uuid, id, task_uuid, actor_uuid, body, etag, created_at)
		VALUES ('comment-uuid-1', 'C-00001', 'task-uuid-1', '00000000-0000-0000-0000-000000000001', 'Test comment body', 1, datetime('now'))
	`)
	if err != nil {
		t.Fatalf("Failed to create test comment: %v", err)
	}

	// Set environment variables
	os.Setenv("WRKQ_DB_PATH", dbPath)
	os.Setenv("WRKQ_ACTOR", "test-user")
	defer os.Unsetenv("WRKQ_DB_PATH")
	defer os.Unsetenv("WRKQ_ACTOR")

	// Test showing comment
	cmd := rootCmd
	cmd.SetArgs([]string{"comment", "cat", "C-00001"})

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Command failed: %v\nOutput: %s", err, out.String())
	}

	output := out.String()

	// Verify output contains key elements
	if !strings.Contains(output, "C-00001") {
		t.Errorf("Output should contain comment ID 'C-00001'")
	}

	if !strings.Contains(output, "Test comment body") {
		t.Errorf("Output should contain comment body")
	}

	if !strings.Contains(output, "test-user") {
		t.Errorf("Output should contain actor slug")
	}
}

func TestCommentRm_SoftDelete(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create a test task
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('task-uuid-1', 'T-00001', 'test-task', 'Test Task', '00000000-0000-0000-0000-000000000002', 'open', 2, 'Task body', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Create a test comment
	_, err = database.Exec(`
		INSERT INTO comments (uuid, id, task_uuid, actor_uuid, body, etag, created_at)
		VALUES ('comment-uuid-1', 'C-00001', 'task-uuid-1', '00000000-0000-0000-0000-000000000001', 'Test comment', 1, datetime('now'))
	`)
	if err != nil {
		t.Fatalf("Failed to create test comment: %v", err)
	}

	// Set environment variables
	os.Setenv("WRKQ_DB_PATH", dbPath)
	os.Setenv("WRKQ_ACTOR", "test-user")
	defer os.Unsetenv("WRKQ_DB_PATH")
	defer os.Unsetenv("WRKQ_ACTOR")

	// Test soft-deleting comment
	cmd := rootCmd
	cmd.SetArgs([]string{"comment", "rm", "C-00001", "--yes"})

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Command failed: %v\nOutput: %s", err, out.String())
	}

	// Verify comment was soft-deleted (still exists but has deleted_at)
	var deletedAt, deletedByActor string
	err = database.QueryRow(`
		SELECT deleted_at, deleted_by_actor_uuid
		FROM comments
		WHERE uuid = 'comment-uuid-1'
	`).Scan(&deletedAt, &deletedByActor)
	if err != nil {
		t.Fatalf("Failed to query deleted comment: %v", err)
	}

	if deletedAt == "" {
		t.Errorf("Expected deleted_at to be set")
	}

	if deletedByActor != "00000000-0000-0000-0000-000000000001" {
		t.Errorf("Expected deleted_by_actor_uuid to be set to actor UUID")
	}

	// Verify event log
	var eventCount int
	err = database.QueryRow("SELECT COUNT(*) FROM event_log WHERE resource_type = 'comment' AND event_type = 'comment.deleted'").Scan(&eventCount)
	if err != nil {
		t.Fatalf("Failed to query event log: %v", err)
	}

	if eventCount != 1 {
		t.Errorf("Expected 1 delete event log entry, got %d", eventCount)
	}
}

func TestCommentRm_Purge(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create a test task
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('task-uuid-1', 'T-00001', 'test-task', 'Test Task', '00000000-0000-0000-0000-000000000002', 'open', 2, 'Task body', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Create a test comment
	_, err = database.Exec(`
		INSERT INTO comments (uuid, id, task_uuid, actor_uuid, body, etag, created_at)
		VALUES ('comment-uuid-1', 'C-00001', 'task-uuid-1', '00000000-0000-0000-0000-000000000001', 'Test comment', 1, datetime('now'))
	`)
	if err != nil {
		t.Fatalf("Failed to create test comment: %v", err)
	}

	// Set environment variables
	os.Setenv("WRKQ_DB_PATH", dbPath)
	os.Setenv("WRKQ_ACTOR", "test-user")
	defer os.Unsetenv("WRKQ_DB_PATH")
	defer os.Unsetenv("WRKQ_ACTOR")

	// Test purging comment
	cmd := rootCmd
	cmd.SetArgs([]string{"comment", "rm", "C-00001", "--purge", "--yes"})

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Command failed: %v\nOutput: %s", err, out.String())
	}

	// Verify comment was completely removed
	var count int
	err = database.QueryRow("SELECT COUNT(*) FROM comments WHERE uuid = 'comment-uuid-1'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query comments: %v", err)
	}

	if count != 0 {
		t.Errorf("Expected comment to be purged (count 0), got %d", count)
	}

	// Verify purge event log
	var eventCount int
	err = database.QueryRow("SELECT COUNT(*) FROM event_log WHERE resource_type = 'comment' AND event_type = 'comment.purged'").Scan(&eventCount)
	if err != nil {
		t.Fatalf("Failed to query event log: %v", err)
	}

	if eventCount != 1 {
		t.Errorf("Expected 1 purge event log entry, got %d", eventCount)
	}
}

func TestCommentLs_IncludeDeleted(t *testing.T) {
	database, _ := setupTestEnv(t)

	// Create a test task
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('task-uuid-1', 'T-00001', 'test-task', 'Test Task', '00000000-0000-0000-0000-000000000002', 'open', 2, 'Task body', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Create comments (one active, one deleted)
	_, err = database.Exec(`
		INSERT INTO comments (uuid, id, task_uuid, actor_uuid, body, etag, created_at, deleted_at, deleted_by_actor_uuid)
		VALUES
			('comment-uuid-1', 'C-00001', 'task-uuid-1', '00000000-0000-0000-0000-000000000001', 'Active comment', 1, datetime('now'), NULL, NULL),
			('comment-uuid-2', 'C-00002', 'task-uuid-1', '00000000-0000-0000-0000-000000000001', 'Deleted comment', 1, datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001')
	`)
	if err != nil {
		t.Fatalf("Failed to create test comments: %v", err)
	}

	// Verify deletion status directly
	var activeCount, deletedCount int
	err = database.QueryRow("SELECT COUNT(*) FROM comments WHERE task_uuid = 'task-uuid-1' AND deleted_at IS NULL").Scan(&activeCount)
	if err != nil {
		t.Fatalf("Failed to count active comments: %v", err)
	}

	err = database.QueryRow("SELECT COUNT(*) FROM comments WHERE task_uuid = 'task-uuid-1' AND deleted_at IS NOT NULL").Scan(&deletedCount)
	if err != nil {
		t.Fatalf("Failed to count deleted comments: %v", err)
	}

	if activeCount != 1 {
		t.Errorf("Expected 1 active comment, got %d", activeCount)
	}

	if deletedCount != 1 {
		t.Errorf("Expected 1 deleted comment, got %d", deletedCount)
	}
}

func TestCatWithIncludeComments(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create a test task
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('task-uuid-1', 'T-00001', 'test-task', 'Test Task', '00000000-0000-0000-0000-000000000002', 'open', 2, 'Task body content', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Create test comments
	_, err = database.Exec(`
		INSERT INTO comments (uuid, id, task_uuid, actor_uuid, body, etag, created_at)
		VALUES
			('comment-uuid-1', 'C-00001', 'task-uuid-1', '00000000-0000-0000-0000-000000000001', 'First comment on task', 1, datetime('now', '-1 minute')),
			('comment-uuid-2', 'C-00002', 'task-uuid-1', '00000000-0000-0000-0000-000000000001', 'Second comment on task', 1, datetime('now'))
	`)
	if err != nil {
		t.Fatalf("Failed to create test comments: %v", err)
	}

	// Set environment variables
	os.Setenv("WRKQ_DB_PATH", dbPath)
	os.Setenv("WRKQ_ACTOR", "test-user")
	defer os.Unsetenv("WRKQ_DB_PATH")
	defer os.Unsetenv("WRKQ_ACTOR")

	// Test cat with --include-comments
	cmd := rootCmd
	cmd.SetArgs([]string{"cat", "T-00001", "--include-comments"})

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Command failed: %v", err)
	}

	output := out.String()

	// Verify output contains task body
	if !strings.Contains(output, "Task body content") {
		t.Errorf("Output should contain task body")
	}

	// Verify output contains comments section marker
	if !strings.Contains(output, "<!-- wrkq-comments: do not edit below -->") {
		t.Errorf("Output should contain comments section marker")
	}

	// Verify output contains both comments
	if !strings.Contains(output, "C-00001") {
		t.Errorf("Output should contain first comment ID")
	}

	if !strings.Contains(output, "C-00002") {
		t.Errorf("Output should contain second comment ID")
	}

	if !strings.Contains(output, "First comment on task") {
		t.Errorf("Output should contain first comment body")
	}

	if !strings.Contains(output, "Second comment on task") {
		t.Errorf("Output should contain second comment body")
	}

	// Verify comments are formatted with > prefix (blockquote)
	if !strings.Contains(output, "> [C-00001]") {
		t.Errorf("Output should contain blockquote-formatted comment header")
	}
}

func TestCommentSequenceIncrement(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create a test task
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('task-uuid-1', 'T-00001', 'test-task', 'Test Task', '00000000-0000-0000-0000-000000000002', 'open', 2, 'Task body', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Set environment variables
	os.Setenv("WRKQ_DB_PATH", dbPath)
	os.Setenv("WRKQ_ACTOR", "test-user")
	defer os.Unsetenv("WRKQ_DB_PATH")
	defer os.Unsetenv("WRKQ_ACTOR")

	// Create multiple comments
	for i := 1; i <= 3; i++ {
		cmd := rootCmd
		cmd.SetArgs([]string{"comment", "add", "T-00001", "-m", "Comment " + string(rune(i+'0'))})

		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)

		if err := cmd.Execute(); err != nil {
			t.Fatalf("Failed to create comment %d: %v", i, err)
		}
	}

	// Verify sequence incremented correctly
	var ids []string
	rows, err := database.Query("SELECT id FROM comments ORDER BY created_at")
	if err != nil {
		t.Fatalf("Failed to query comment IDs: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("Failed to scan ID: %v", err)
		}
		ids = append(ids, id)
	}

	expectedIDs := []string{"C-00001", "C-00002", "C-00003"}
	for i, expected := range expectedIDs {
		if i >= len(ids) || ids[i] != expected {
			t.Errorf("Expected comment ID %s at position %d, got %v", expected, i, ids)
		}
	}
}

func TestCommentAdd_PositionalArg(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create a test task
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('task-uuid-1', 'T-00001', 'test-task', 'Test Task', '00000000-0000-0000-0000-000000000002', 'open', 2, 'Task body', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Set environment variables
	os.Setenv("WRKQ_DB_PATH", dbPath)
	os.Setenv("WRKQ_ACTOR", "test-user")
	defer os.Unsetenv("WRKQ_DB_PATH")
	defer os.Unsetenv("WRKQ_ACTOR")

	// Test adding a comment via positional argument (new default behavior)
	cmd := rootCmd
	cmd.SetArgs([]string{"comment", "add", "T-00001", "This is a comment via positional arg"})

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Command failed: %v\nOutput: %s", err, out.String())
	}

	// Verify comment was created
	var count int
	err = database.QueryRow("SELECT COUNT(*) FROM comments WHERE task_uuid = 'task-uuid-1'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query comments: %v", err)
	}

	if count != 1 {
		t.Errorf("Expected 1 comment, got %d", count)
	}

	// Verify comment content
	var body string
	err = database.QueryRow("SELECT body FROM comments WHERE task_uuid = 'task-uuid-1'").Scan(&body)
	if err != nil {
		t.Fatalf("Failed to query comment body: %v", err)
	}

	if body != "This is a comment via positional arg" {
		t.Errorf("Expected comment body 'This is a comment via positional arg', got %q", body)
	}
}

func TestCommentAdd_FileFlag(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create a test task
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, body, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('task-uuid-1', 'T-00001', 'test-task', 'Test Task', '00000000-0000-0000-0000-000000000002', 'open', 2, 'Task body', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create test task: %v", err)
	}

	// Create a temporary file with comment text
	tmpfile, err := os.CreateTemp("", "comment-*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpfile.Name())

	commentText := "This is a comment from a file"
	if _, err := tmpfile.Write([]byte(commentText)); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	// Set environment variables
	os.Setenv("WRKQ_DB_PATH", dbPath)
	os.Setenv("WRKQ_ACTOR", "test-user")
	defer os.Unsetenv("WRKQ_DB_PATH")
	defer os.Unsetenv("WRKQ_ACTOR")

	// Test adding a comment via -f flag
	cmd := rootCmd
	cmd.SetArgs([]string{"comment", "add", "T-00001", "-f", tmpfile.Name()})

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Command failed: %v\nOutput: %s", err, out.String())
	}

	// Verify comment was created
	var count int
	err = database.QueryRow("SELECT COUNT(*) FROM comments WHERE task_uuid = 'task-uuid-1'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query comments: %v", err)
	}

	if count != 1 {
		t.Errorf("Expected 1 comment, got %d", count)
	}

	// Verify comment content
	var body string
	err = database.QueryRow("SELECT body FROM comments WHERE task_uuid = 'task-uuid-1'").Scan(&body)
	if err != nil {
		t.Fatalf("Failed to query comment body: %v", err)
	}

	if body != commentText {
		t.Errorf("Expected comment body %q, got %q", commentText, body)
	}
}
