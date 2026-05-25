package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// TestLsDefaultFilter verifies that ls only shows draft and open tasks by default
func TestLsDefaultFilter(t *testing.T) {
	database, dbPath := setupTestEnv(t)
	app := createTestApp(t, database, dbPath)

	// Create tasks with different states
	states := []struct {
		uuid  string
		id    string
		slug  string
		state string
	}{
		{"task-idea-uuid", "T-00001", "task-idea", "idea"},
		{"task-draft-uuid", "T-00002", "task-draft", "draft"},
		{"task-open-uuid", "T-00003", "task-open", "open"},
		{"task-inprog-uuid", "T-00004", "task-in-progress", "in_progress"},
		{"task-completed-uuid", "T-00005", "task-completed", "completed"},
		{"task-archived-uuid", "T-00006", "task-archived", "archived"},
		{"task-blocked-uuid", "T-00007", "task-blocked", "blocked"},
		{"task-cancelled-uuid", "T-00008", "task-cancelled", "cancelled"},
		{"task-deleted-uuid", "T-00009", "task-deleted", "deleted"},
	}

	for _, s := range states {
		_, err := database.Exec(`
			INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, ?, ?, ?, '00000000-0000-0000-0000-000000000002', ?, 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
		`, s.uuid, s.id, s.slug, s.slug, s.state)
		if err != nil {
			t.Fatalf("Failed to create task %s: %v", s.slug, err)
		}
	}

	// Test default ls (should only show draft and open)
	t.Run("default shows only draft and open", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)

		// Reset flags to default
		lsIncludeHidden = false
		lsJSON = false
		lsNDJSON = false
		lsPorcelain = false
		lsRecursive = false
		lsType = ""
		lsOne = false
		lsNul = false
		lsLimit = 0
		lsCursor = ""
		lsSort = "slug"
		lsReverse = false

		err := runLs(app, cmd, []string{"inbox"})
		if err != nil {
			t.Fatalf("runLs failed: %v", err)
		}

		output := buf.String()

		// Should contain draft and open
		if !strings.Contains(output, "task-draft") {
			t.Errorf("Expected output to contain 'task-draft', got: %s", output)
		}
		if !strings.Contains(output, "task-open") {
			t.Errorf("Expected output to contain 'task-open', got: %s", output)
		}

		// Should NOT contain other states
		shouldNotContain := []string{
			"task-idea",
			"task-in-progress",
			"task-completed",
			"task-archived",
			"task-blocked",
			"task-cancelled",
			"task-deleted",
		}
		for _, slug := range shouldNotContain {
			if strings.Contains(output, slug) {
				t.Errorf("Expected output to NOT contain '%s', but it did. Output: %s", slug, output)
			}
		}
	})

	// Test with -a flag (should show all states)
	t.Run("with -a flag shows all states", func(t *testing.T) {
		var buf bytes.Buffer
		cmd := &cobra.Command{}
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)

		// Set -a flag
		lsIncludeHidden = true
		lsJSON = false
		lsNDJSON = false
		lsPorcelain = false
		lsRecursive = false
		lsType = ""
		lsOne = false
		lsNul = false
		lsLimit = 0
		lsCursor = ""
		lsSort = "slug"
		lsReverse = false

		err := runLs(app, cmd, []string{"inbox"})
		if err != nil {
			t.Fatalf("runLs failed: %v", err)
		}

		output := buf.String()

		// Should contain all states (except maybe idea, depending on implementation)
		expectedStates := []string{
			"task-draft",
			"task-open",
			"task-in-progress",
			"task-completed",
			"task-archived",
			"task-blocked",
			"task-cancelled",
			"task-deleted",
		}
		for _, slug := range expectedStates {
			if !strings.Contains(output, slug) {
				t.Errorf("Expected output with -a to contain '%s', but it didn't. Output: %s", slug, output)
			}
		}
	})
}

func TestLsSortUpdatedAtReverseLimitAndTimestamps(t *testing.T) {
	database, dbPath := setupTestEnv(t)
	app := createTestApp(t, database, dbPath)
	t.Cleanup(func() {
		lsJSON = false
		lsNDJSON = false
		lsPorcelain = false
		lsRecursive = false
		lsType = ""
		lsOne = false
		lsNul = false
		lsLimit = 0
		lsCursor = ""
		lsIncludeHidden = false
		lsSort = "slug"
		lsReverse = false
	})

	tasks := []struct {
		uuid      string
		id        string
		slug      string
		createdAt string
		updatedAt string
	}{
		{"task-1-uuid", "T-00001", "task-1", "2026-05-25T10:00:00Z", "2026-05-25T10:30:00Z"},
		{"task-2-uuid", "T-00002", "task-2", "2026-05-25T10:05:00Z", "2026-05-25T10:10:00Z"},
		{"task-3-uuid", "T-00003", "task-3", "2026-05-25T10:15:00Z", "2026-05-25T10:45:00Z"},
	}

	for _, task := range tasks {
		_, err := database.Exec(`
			INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, ?, ?, ?, '00000000-0000-0000-0000-000000000002', 'open', 2, '', ?, ?, '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
		`, task.uuid, task.id, task.slug, task.slug, task.createdAt, task.updatedAt)
		if err != nil {
			t.Fatalf("Failed to create task %s: %v", task.id, err)
		}
	}

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	lsJSON = false
	lsNDJSON = true
	lsPorcelain = false
	lsRecursive = false
	lsType = "t"
	lsOne = false
	lsNul = false
	lsLimit = 2
	lsCursor = ""
	lsIncludeHidden = false
	lsSort = "updated_at"
	lsReverse = true

	err := runLs(app, cmd, []string{"inbox"})
	if err != nil {
		t.Fatalf("runLs failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("Expected 2 NDJSON rows, got %d: %s", len(lines), buf.String())
	}

	var first, second map[string]string
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("first row is invalid JSON: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatalf("second row is invalid JSON: %v", err)
	}

	if first["id"] != "T-00003" || second["id"] != "T-00001" {
		t.Fatalf("Expected updated_at descending order T-00003,T-00001; got %s,%s", first["id"], second["id"])
	}
	if first["created_at"] != "2026-05-25T10:15:00Z" || first["updated_at"] != "2026-05-25T10:45:00Z" {
		t.Fatalf("Expected timestamps in NDJSON, got first row: %v", first)
	}

	buf.Reset()
	lsNDJSON = false
	lsLimit = 1
	if err := runLs(app, cmd, []string{"inbox"}); err != nil {
		t.Fatalf("runLs table failed: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "CreatedAt") || !strings.Contains(output, "UpdatedAt") || !strings.Contains(output, "2026-05-25T10:45:00Z") {
		t.Fatalf("Expected table output to include timestamp columns, got: %s", output)
	}
}
