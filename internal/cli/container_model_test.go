package cli

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestFindRecursesAndLsShowsChildRollups(t *testing.T) {
	database, dbPath := setupTestEnv(t)
	app := createTestApp(t, database, dbPath)
	resetFindFlagsForTest(t)
	resetLsFlagsForTest(t)

	// Simulate a legacy nested project row that predates the root-project
	// invariant. Phase 2 reclassifies these rows, but Phase 1 must keep them
	// visible until that data repair runs.
	if _, err := database.Exec(`DROP TRIGGER IF EXISTS containers_project_root_insert`); err != nil {
		t.Fatalf("drop legacy insert trigger: %v", err)
	}

	if _, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, kind, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES
		  ('00000000-0000-0000-0000-000000000100', 'P-00100', 'legacy-project', 'Legacy Project',
		   '00000000-0000-0000-0000-000000000002', 'project', '2026-05-25T10:00:00Z', '2026-05-25T10:00:00Z',
		   '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1),
		  ('00000000-0000-0000-0000-000000000101', 'P-00101', 'deep', 'Deep',
		   '00000000-0000-0000-0000-000000000100', 'directory', '2026-05-25T10:00:00Z', '2026-05-25T10:00:00Z',
		   '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1),
		  ('00000000-0000-0000-0000-000000000102', 'P-00102', 'inbox-extra', 'Inbox Extra',
		   NULL, 'directory', '2026-05-25T10:00:00Z', '2026-05-25T10:00:00Z',
		   '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`); err != nil {
		t.Fatalf("insert containers: %v", err)
	}

	taskRows := []struct {
		uuid      string
		id        string
		slug      string
		container string
		state     string
		deletedAt *string
	}{
		{"00000000-0000-0000-0000-000000000110", "T-00110", "direct-open", "00000000-0000-0000-0000-000000000002", "open", nil},
		{"00000000-0000-0000-0000-000000000111", "T-00111", "legacy-open", "00000000-0000-0000-0000-000000000100", "open", nil},
		{"00000000-0000-0000-0000-000000000112", "T-00112", "deep-blocked", "00000000-0000-0000-0000-000000000101", "blocked", nil},
		{"00000000-0000-0000-0000-000000000113", "T-00113", "deep-completed", "00000000-0000-0000-0000-000000000101", "completed", nil},
		{"00000000-0000-0000-0000-000000000114", "T-00114", "deep-deleted", "00000000-0000-0000-0000-000000000101", "deleted", strPtrForTest("2026-05-25T10:00:00Z")},
		{"00000000-0000-0000-0000-000000000115", "T-00115", "sibling-prefix", "00000000-0000-0000-0000-000000000102", "open", nil},
	}
	for _, row := range taskRows {
		_, err := database.Exec(`
			INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, description, created_at, updated_at, deleted_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, ?, ?, ?, ?, ?, 2, '', '2026-05-25T10:00:00Z', '2026-05-25T10:00:00Z', ?, '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
		`, row.uuid, row.id, row.slug, row.slug, row.container, row.state, row.deletedAt)
		if err != nil {
			t.Fatalf("insert task %s: %v", row.id, err)
		}
	}

	findType = "t"
	findState = "all"
	findSort = "path"

	findResults, _, err := executeFindQuery(database, findOptions{
		paths:          []string{"inbox"},
		typeFilter:     "t",
		state:          "all",
		sortField:      "path",
		sortDescending: false,
	})
	if err != nil {
		t.Fatalf("executeFindQuery: %v", err)
	}

	gotFindIDs := make([]string, 0, len(findResults))
	for _, result := range findResults {
		gotFindIDs = append(gotFindIDs, result.ID)
	}
	sort.Strings(gotFindIDs)
	wantFindIDs := []string{"T-00110", "T-00111", "T-00112", "T-00113", "T-00114"}
	if strings.Join(gotFindIDs, ",") != strings.Join(wantFindIDs, ",") {
		t.Fatalf("find subtree IDs=%v, want %v", gotFindIDs, wantFindIDs)
	}

	resetLsFlagsForTest(t)
	lsJSON = true
	lsIncludeHidden = true

	var lsBuf bytes.Buffer
	lsCmd := &cobra.Command{}
	lsCmd.SetOut(&lsBuf)
	lsCmd.SetErr(&lsBuf)
	if err := runLs(app, lsCmd, []string{"inbox"}); err != nil {
		t.Fatalf("runLs: %v", err)
	}

	var entries []lsEntry
	if err := json.Unmarshal(lsBuf.Bytes(), &entries); err != nil {
		t.Fatalf("parse ls JSON: %v\n%s", err, lsBuf.String())
	}

	foundRollup := false
	foundDirect := false
	for _, entry := range entries {
		switch entry.ID {
		case "P-00100":
			foundRollup = true
			if entry.Kind != "project" {
				t.Fatalf("legacy child kind=%q, want project", entry.Kind)
			}
			if entry.TaskCount == nil || *entry.TaskCount != 4 {
				t.Fatalf("task_count=%v, want 4", entry.TaskCount)
			}
			if entry.ActiveTaskCount == nil || *entry.ActiveTaskCount != 2 {
				t.Fatalf("active_task_count=%v, want 2", entry.ActiveTaskCount)
			}
		case "T-00110":
			foundDirect = true
		}
	}
	if !foundRollup {
		t.Fatal("ls did not include child container rollup")
	}
	if !foundDirect {
		t.Fatal("ls did not include direct task")
	}

	resetLsFlagsForTest(t)
	var humanBuf bytes.Buffer
	humanCmd := &cobra.Command{}
	humanCmd.SetOut(&humanBuf)
	humanCmd.SetErr(&humanBuf)
	if err := runLs(app, humanCmd, []string{"inbox"}); err != nil {
		t.Fatalf("runLs human: %v", err)
	}
	human := humanBuf.String()
	if !strings.Contains(human, "legacy-project/") ||
		!strings.Contains(human, "[project] 4 tasks (2 active)") {
		t.Fatalf("human ls missing explicit rollup: %s", human)
	}
}

func TestMkdirDefaultsToDirectoryAndRestrictsNestedProject(t *testing.T) {
	database, dbPath := setupTestEnv(t)
	app := createTestApp(t, database, dbPath)
	if _, err := database.Exec(`INSERT INTO container_seq (id) VALUES (1)`); err != nil {
		t.Fatalf("prime container sequence: %v", err)
	}
	t.Cleanup(func() {
		mkdirParents = false
		mkdirKind = ""
	})

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	mkdirKind = ""
	if err := runMkdir(app, cmd, []string{"inbox/child-dir"}); err != nil {
		t.Fatalf("mkdir child dir: %v", err)
	}
	var kind string
	if err := database.QueryRow(`SELECT kind FROM containers WHERE slug = 'child-dir'`).Scan(&kind); err != nil {
		t.Fatalf("query child kind: %v", err)
	}
	if kind != "directory" {
		t.Fatalf("child kind=%q, want directory", kind)
	}

	mkdirKind = "project"
	if err := runMkdir(app, cmd, []string{"inbox/nested-project"}); err == nil {
		t.Fatal("expected nested project mkdir to fail")
	}

	mkdirKind = "project"
	if err := runMkdir(app, cmd, []string{"root-project"}); err != nil {
		t.Fatalf("root project mkdir failed: %v", err)
	}
	if err := database.QueryRow(`SELECT kind FROM containers WHERE slug = 'root-project'`).Scan(&kind); err != nil {
		t.Fatalf("query root project kind: %v", err)
	}
	if kind != "project" {
		t.Fatalf("root kind=%q, want project", kind)
	}

	mkdirKind = "misc"
	if err := runMkdir(app, cmd, []string{"inbox/misc-folder"}); err == nil {
		t.Fatal("expected misc mkdir to fail")
	}
}

func resetLsFlagsForTest(t *testing.T) {
	t.Helper()
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
}

func resetFindFlagsForTest(t *testing.T) {
	t.Helper()
	findType = ""
	findSlugGlob = ""
	findState = ""
	findDueBefore = ""
	findDueAfter = ""
	findKind = ""
	findAssignee = ""
	findParentTask = ""
	findRequestedBy = ""
	findAssignedProject = ""
	findAckPending = false
	findLimit = 0
	findCursor = ""
	findSort = ""
	findReverse = false
	findPorcelain = false
	findJSON = false
	findNDJSON = false
	findPrint0 = false
}

func strPtrForTest(value string) *string {
	return &value
}
