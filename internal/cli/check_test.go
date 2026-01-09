package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

func setupCheckTestDB(t *testing.T) (*db.DB, string, string, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}

	if err := database.Migrate(); err != nil {
		database.Close()
		t.Fatalf("Failed to migrate database: %v", err)
	}

	// Create test actor
	actorUUID := "test-actor-uuid"
	_, err = database.Exec(`
		INSERT INTO actors (uuid, slug, display_name, role)
		VALUES (?, 'test-actor', 'Test Actor', 'human')
	`, actorUUID)
	if err != nil {
		database.Close()
		t.Fatalf("Failed to create actor: %v", err)
	}

	// Create test container
	containerUUID := "test-container-uuid"
	_, err = database.Exec(`
		INSERT INTO containers (uuid, slug, title, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES (?, 'test-project', 'Test Project', ?, ?, 1)
	`, containerUUID, actorUUID, actorUUID)
	if err != nil {
		database.Close()
		t.Fatalf("Failed to create container: %v", err)
	}

	cleanup := func() {
		database.Close()
	}

	return database, actorUUID, containerUUID, cleanup
}

func TestCheckBlocked_NoBlockers(t *testing.T) {
	database, actorUUID, containerUUID, cleanup := setupCheckTestDB(t)
	defer cleanup()

	s := store.New(database)

	// Create task with no blockers
	result, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:        "unblocked-task",
		Title:       "Unblocked Task",
		ProjectUUID: containerUUID,
		State:       "open",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("Failed to create task: %v", err)
	}

	// Check blockers
	blockers, err := s.Tasks.BlockedBy(result.UUID)
	if err != nil {
		t.Fatalf("BlockedBy failed: %v", err)
	}

	if len(blockers) != 0 {
		t.Errorf("Expected 0 blockers, got %d", len(blockers))
	}
}

func TestCheckBlocked_WithIncompleteBlocker(t *testing.T) {
	database, actorUUID, containerUUID, cleanup := setupCheckTestDB(t)
	defer cleanup()

	s := store.New(database)

	// Create blocker task (in_progress - incomplete)
	blockerResult, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:        "blocker-task",
		Title:       "Blocker Task",
		ProjectUUID: containerUUID,
		State:       "in_progress",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("Failed to create blocker task: %v", err)
	}

	// Create blocked task
	blockedResult, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:        "blocked-task",
		Title:       "Blocked Task",
		ProjectUUID: containerUUID,
		State:       "open",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("Failed to create blocked task: %v", err)
	}

	// Create blocks relation
	_, err = database.Exec(`
		INSERT INTO task_relations (from_task_uuid, to_task_uuid, kind, created_by_actor_uuid)
		VALUES (?, ?, 'blocks', ?)
	`, blockerResult.UUID, blockedResult.UUID, actorUUID)
	if err != nil {
		t.Fatalf("Failed to create relation: %v", err)
	}

	// Check blockers
	blockers, err := s.Tasks.BlockedBy(blockedResult.UUID)
	if err != nil {
		t.Fatalf("BlockedBy failed: %v", err)
	}

	if len(blockers) != 1 {
		t.Fatalf("Expected 1 blocker, got %d", len(blockers))
	}

	if blockers[0].UUID != blockerResult.UUID {
		t.Errorf("Expected blocker UUID %s, got %s", blockerResult.UUID, blockers[0].UUID)
	}
}

func TestCheckBlocked_CompletedBlockerNotReturned(t *testing.T) {
	database, actorUUID, containerUUID, cleanup := setupCheckTestDB(t)
	defer cleanup()

	s := store.New(database)

	// Create blocker task (completed - should not block)
	blockerResult, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:        "completed-blocker",
		Title:       "Completed Blocker",
		ProjectUUID: containerUUID,
		State:       "completed",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("Failed to create blocker task: %v", err)
	}

	// Create blocked task
	blockedResult, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:        "waiting-task",
		Title:       "Waiting Task",
		ProjectUUID: containerUUID,
		State:       "open",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("Failed to create blocked task: %v", err)
	}

	// Create blocks relation
	_, err = database.Exec(`
		INSERT INTO task_relations (from_task_uuid, to_task_uuid, kind, created_by_actor_uuid)
		VALUES (?, ?, 'blocks', ?)
	`, blockerResult.UUID, blockedResult.UUID, actorUUID)
	if err != nil {
		t.Fatalf("Failed to create relation: %v", err)
	}

	// Check blockers - should return empty since blocker is completed
	blockers, err := s.Tasks.BlockedBy(blockedResult.UUID)
	if err != nil {
		t.Fatalf("BlockedBy failed: %v", err)
	}

	if len(blockers) != 0 {
		t.Errorf("Expected 0 blockers (completed task shouldn't block), got %d", len(blockers))
	}
}

func TestCheckBlocked_JSONOutput(t *testing.T) {
	database, actorUUID, containerUUID, cleanup := setupCheckTestDB(t)
	defer cleanup()

	s := store.New(database)

	// Create blocker task
	blockerResult, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:        "blocker-json",
		Title:       "Blocker JSON",
		ProjectUUID: containerUUID,
		State:       "open",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("Failed to create blocker task: %v", err)
	}

	// Create blocked task
	blockedResult, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:        "blocked-json",
		Title:       "Blocked JSON",
		ProjectUUID: containerUUID,
		State:       "open",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("Failed to create blocked task: %v", err)
	}

	// Create blocks relation
	_, err = database.Exec(`
		INSERT INTO task_relations (from_task_uuid, to_task_uuid, kind, created_by_actor_uuid)
		VALUES (?, ?, 'blocks', ?)
	`, blockerResult.UUID, blockedResult.UUID, actorUUID)
	if err != nil {
		t.Fatalf("Failed to create relation: %v", err)
	}

	// Test JSON output structure
	blockers, err := s.Tasks.BlockedBy(blockedResult.UUID)
	if err != nil {
		t.Fatalf("BlockedBy failed: %v", err)
	}

	// Create the result struct as the command would
	result := BlockedResult{
		TaskID:    blockedResult.ID,
		TaskUUID:  blockedResult.UUID,
		IsBlocked: len(blockers) > 0,
		Blockers:  make([]BlockerEntry, len(blockers)),
	}
	for i, b := range blockers {
		result.Blockers[i] = BlockerEntry{
			ID:    b.ID,
			UUID:  b.UUID,
			Slug:  b.Slug,
			Title: b.Title,
			State: b.State,
		}
	}

	// Verify JSON serialization works
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		t.Fatalf("Failed to encode JSON: %v", err)
	}

	// Verify JSON structure by decoding
	var decoded BlockedResult
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("Failed to decode JSON: %v", err)
	}

	if decoded.TaskUUID != blockedResult.UUID {
		t.Errorf("Expected task UUID %s, got %s", blockedResult.UUID, decoded.TaskUUID)
	}
	if !decoded.IsBlocked {
		t.Error("Expected IsBlocked to be true")
	}
	if len(decoded.Blockers) != 1 {
		t.Errorf("Expected 1 blocker, got %d", len(decoded.Blockers))
	}
	if decoded.Blockers[0].UUID != blockerResult.UUID {
		t.Errorf("Expected blocker UUID %s, got %s", blockerResult.UUID, decoded.Blockers[0].UUID)
	}
}

func TestCheckBlocked_MultipleBlockers(t *testing.T) {
	database, actorUUID, containerUUID, cleanup := setupCheckTestDB(t)
	defer cleanup()

	s := store.New(database)

	// Create multiple blockers with different states
	blocker1, _ := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:        "blocker-open",
		Title:       "Blocker Open",
		ProjectUUID: containerUUID,
		State:       "open",
		Priority:    2,
	})
	blocker2, _ := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:        "blocker-progress",
		Title:       "Blocker In Progress",
		ProjectUUID: containerUUID,
		State:       "in_progress",
		Priority:    2,
	})
	blocker3, _ := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:        "blocker-completed",
		Title:       "Blocker Completed",
		ProjectUUID: containerUUID,
		State:       "completed",
		Priority:    2,
	})

	// Create blocked task
	blockedResult, _ := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:        "multi-blocked",
		Title:       "Multi Blocked",
		ProjectUUID: containerUUID,
		State:       "open",
		Priority:    2,
	})

	// Create blocks relations
	for _, blocker := range []*store.CreateResult{blocker1, blocker2, blocker3} {
		_, err := database.Exec(`
			INSERT INTO task_relations (from_task_uuid, to_task_uuid, kind, created_by_actor_uuid)
			VALUES (?, ?, 'blocks', ?)
		`, blocker.UUID, blockedResult.UUID, actorUUID)
		if err != nil {
			t.Fatalf("Failed to create relation: %v", err)
		}
	}

	// Check blockers - should return only incomplete ones (open, in_progress)
	blockers, err := s.Tasks.BlockedBy(blockedResult.UUID)
	if err != nil {
		t.Fatalf("BlockedBy failed: %v", err)
	}

	if len(blockers) != 2 {
		t.Fatalf("Expected 2 incomplete blockers, got %d", len(blockers))
	}

	// Verify only incomplete states
	for _, b := range blockers {
		if b.State != "open" && b.State != "in_progress" {
			t.Errorf("Unexpected blocker state: %s", b.State)
		}
	}
}

func TestCheckBlockedCmd_HelpText(t *testing.T) {
	// Verify command is properly configured
	cmd := checkBlockedCmd

	if cmd.Use != "blocked <task>" {
		t.Errorf("Unexpected Use: %s", cmd.Use)
	}

	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}

	// Verify flags exist
	if cmd.Flags().Lookup("json") == nil {
		t.Error("Expected --json flag")
	}
	if cmd.Flags().Lookup("quiet") == nil {
		t.Error("Expected --quiet flag")
	}
}

func TestCheckCmd_Subcommands(t *testing.T) {
	// Verify check command has blocked subcommand
	var foundBlocked bool
	for _, subcmd := range checkCmd.Commands() {
		if subcmd.Use == "blocked <task>" {
			foundBlocked = true
			break
		}
	}
	if !foundBlocked {
		t.Error("Expected 'blocked' subcommand")
	}
}

// mockCmd creates a cobra command with stdout/stderr buffers for testing
func mockCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	cmd := &cobra.Command{}
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	return cmd, stdout, stderr
}
