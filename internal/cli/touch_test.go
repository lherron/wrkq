package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func resetTouchGlobals() {
	touchTitle = ""
	touchDescription = ""
	touchSpecification = ""
	touchState = "open"
	touchPriority = 3
	touchKind = ""
	touchParentTask = ""
	touchAssignee = ""
	touchRequestedBy = ""
	touchAssignedProject = ""
	touchResolution = ""
	touchLabels = ""
	touchMeta = ""
	touchMetaFile = ""
	touchDueAt = ""
	touchStartAt = ""
	touchForceUUID = ""
	touchJSON = false
}

func resetCatGlobals() {
	catNoFrontmatter = false
	catExcludeComments = false
	catJSON = false
	catNDJSON = false
	catPorcelain = false
}

func TestTouchCreatesArtifactDirAndCatShowsHint(t *testing.T) {
	database, dbPath := setupTestEnv(t)
	praesidiumHome := filepath.Join(t.TempDir(), "praesidium")
	t.Setenv("PRAESIDIUM_HOME", praesidiumHome)

	app := createTestApp(t, database, dbPath)
	resetTouchGlobals()

	touchBuf := &bytes.Buffer{}
	touchCmd := &cobra.Command{}
	touchCmd.SetOut(touchBuf)
	touchCmd.SetErr(touchBuf)

	if err := runTouch(app, touchCmd, []string{"inbox/artifact-smoke"}); err != nil {
		t.Fatalf("runTouch failed: %v", err)
	}

	var taskID string
	if err := database.QueryRow(`SELECT id FROM tasks WHERE slug = ?`, "artifact-smoke").Scan(&taskID); err != nil {
		t.Fatalf("Failed to load created task: %v", err)
	}

	expectedDir := filepath.Join(praesidiumHome, "var", "wrkq-artifacts", taskID)
	if stat, err := os.Stat(expectedDir); err != nil {
		t.Fatalf("expected artifact directory to exist at %s: %v", expectedDir, err)
	} else if !stat.IsDir() {
		t.Fatalf("expected artifact path to be a directory: %s", expectedDir)
	}

	resetCatGlobals()
	catBuf := &bytes.Buffer{}
	catCmd := &cobra.Command{}
	catCmd.SetOut(catBuf)
	catCmd.SetErr(catBuf)

	if err := runCat(app, catCmd, []string{taskID}); err != nil {
		t.Fatalf("runCat failed: %v", err)
	}

	if !strings.Contains(catBuf.String(), "artifact_dir: "+expectedDir) {
		t.Fatalf("cat output missing artifact_dir hint %q:\n%s", expectedDir, catBuf.String())
	}
}

func TestTouchProjectOverride(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	otherUUID := "00000000-0000-0000-0000-000000000011"
	featureUUID := "00000000-0000-0000-0000-000000000012"

	insertContainer(t, database, otherUUID, "P-00003", "other", "Other", "", "2024-01-01T00:00:00Z")
	insertContainer(t, database, featureUUID, "P-00004", "feature", "Feature", otherUUID, "2024-01-01T00:00:00Z")

	app := createTestApp(t, database, dbPath)
	// Simulate --project other (now handled via Config.ProjectRoot override in Bootstrap)
	app.Config.ProjectRoot = "other"

	resetTouchGlobals()

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	if err := runTouch(app, cmd, []string{"feature/new-task"}); err != nil {
		t.Fatalf("runTouch failed: %v", err)
	}

	var projectUUID string
	if err := database.QueryRow(`SELECT project_uuid FROM tasks WHERE slug = ?`, "new-task").Scan(&projectUUID); err != nil {
		t.Fatalf("Failed to load created task: %v", err)
	}
	if projectUUID != featureUUID {
		t.Fatalf("Expected task in feature container %s, got %s", featureUUID, projectUUID)
	}

	parentTaskUUID := "00000000-0000-0000-0000-000000000020"
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES (?, 'T-00010', 'parent-task', 'Parent Task', ?, 'open', 3, datetime('now'), datetime('now'), ?, ?, 1)
	`, parentTaskUUID, featureUUID, testActorUUID, testActorUUID)
	if err != nil {
		t.Fatalf("Failed to seed parent task: %v", err)
	}

	resetTouchGlobals()
	touchParentTask = "feature/parent-task"

	if err := runTouch(app, cmd, []string{"feature/child-task"}); err != nil {
		t.Fatalf("runTouch with parent task failed: %v", err)
	}

	var gotParentUUID string
	if err := database.QueryRow(`SELECT parent_task_uuid FROM tasks WHERE slug = ?`, "child-task").Scan(&gotParentUUID); err != nil {
		t.Fatalf("Failed to load child task: %v", err)
	}
	if gotParentUUID != parentTaskUUID {
		t.Fatalf("Expected parent task UUID %s, got %s", parentTaskUUID, gotParentUUID)
	}
}

func TestTouchProjectRootHandlesAlreadyRootedPaths(t *testing.T) {
	// With the global --project flag approach, paths that already include the project root
	// are handled gracefully (not re-prefixed), not rejected with an error.
	database, dbPath := setupTestEnv(t)

	otherUUID := "00000000-0000-0000-0000-000000000030"
	featureUUID := "00000000-0000-0000-0000-000000000031"
	insertContainer(t, database, otherUUID, "P-00005", "other", "Other", "", "2024-01-01T00:00:00Z")
	insertContainer(t, database, featureUUID, "P-00006", "feature", "Feature", otherUUID, "2024-01-01T00:00:00Z")

	app := createTestApp(t, database, dbPath)
	// Simulate --project other
	app.Config.ProjectRoot = "other"

	resetTouchGlobals()

	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	// Path "other/feature/rooted-task" already starts with "other" - should work without double-prefixing
	if err := runTouch(app, cmd, []string{"other/feature/rooted-task"}); err != nil {
		t.Fatalf("runTouch with already-rooted path should succeed: %v", err)
	}

	// Verify task was created in the right container
	var projectUUID string
	if err := database.QueryRow(`SELECT project_uuid FROM tasks WHERE slug = ?`, "rooted-task").Scan(&projectUUID); err != nil {
		t.Fatalf("Failed to load created task: %v", err)
	}
	if projectUUID != featureUUID {
		t.Fatalf("Expected task in feature container %s, got %s", featureUUID, projectUUID)
	}
}
