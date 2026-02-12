package cli

import (
	"bytes"
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
