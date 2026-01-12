package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/spf13/cobra"
)

func TestApplyProjectRootHelpers(t *testing.T) {
	cfg := &config.Config{ProjectRoot: "demo"}

	t.Run("applyProjectRootToPath", func(t *testing.T) {
		cases := []struct {
			name          string
			input         string
			defaultToRoot bool
			want          string
		}{
			{name: "empty-no-default", input: "", defaultToRoot: false, want: ""},
			{name: "empty-default", input: "", defaultToRoot: true, want: "demo"},
			{name: "relative", input: "inbox", defaultToRoot: false, want: "demo/inbox"},
			{name: "already-prefixed", input: "demo/inbox", defaultToRoot: false, want: "demo/inbox"},
			{name: "trim-slashes", input: "/demo/inbox/", defaultToRoot: false, want: "demo/inbox"},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				got := applyProjectRootToPath(cfg, tc.input, tc.defaultToRoot)
				if got != tc.want {
					t.Fatalf("expected %q, got %q", tc.want, got)
				}
			})
		}
	})

	t.Run("applyProjectRootToSelector", func(t *testing.T) {
		cases := []struct {
			name          string
			input         string
			defaultToRoot bool
			want          string
		}{
			{name: "friendly-id", input: "T-00001", defaultToRoot: false, want: "T-00001"},
			{name: "uuid", input: "00000000-0000-0000-0000-000000000001", defaultToRoot: false, want: "00000000-0000-0000-0000-000000000001"},
			{name: "typed-friendly-id", input: "t:T-00001", defaultToRoot: false, want: "t:T-00001"},
			{name: "typed-path", input: "t:inbox/task", defaultToRoot: false, want: "t:demo/inbox/task"},
			{name: "path", input: "inbox/task", defaultToRoot: false, want: "demo/inbox/task"},
			{name: "already-prefixed", input: "demo/inbox/task", defaultToRoot: false, want: "demo/inbox/task"},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				got := applyProjectRootToSelector(cfg, tc.input, tc.defaultToRoot)
				if got != tc.want {
					t.Fatalf("expected %q, got %q", tc.want, got)
				}
			})
		}
	})

	t.Run("applyProjectRootToPaths", func(t *testing.T) {
		cases := []struct {
			name          string
			input         []string
			defaultToRoot bool
			want          []string
		}{
			{name: "none-no-default", input: nil, defaultToRoot: false, want: nil},
			{name: "none-default", input: nil, defaultToRoot: true, want: []string{"demo"}},
			{name: "single", input: []string{"inbox"}, defaultToRoot: false, want: []string{"demo/inbox"}},
		}

		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				got := applyProjectRootToPaths(cfg, tc.input, tc.defaultToRoot)
				if len(got) != len(tc.want) {
					t.Fatalf("expected %d entries, got %d", len(tc.want), len(got))
				}
				for i := range got {
					if got[i] != tc.want[i] {
						t.Fatalf("expected %q, got %q", tc.want[i], got[i])
					}
				}
			})
		}
	})
}

func TestGlobalProjectFlag_OverridesConfig(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create two root projects: "alpha" and "beta"
	_, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000010', 'P-00002', 'alpha', 'Alpha Project', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create alpha project: %v", err)
	}

	_, err = database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000011', 'P-00003', 'beta', 'Beta Project', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create beta project: %v", err)
	}

	// Create a task in alpha/inbox (reuse inbox from setupTestEnv, but under alpha)
	_, err = database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000012', 'P-00004', 'inbox', 'Inbox', '00000000-0000-0000-0000-000000000010', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create alpha/inbox: %v", err)
	}

	_, err = database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000020', 'T-00001', 'alpha-task', 'Alpha Task', '00000000-0000-0000-0000-000000000012', 'open', 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create alpha task: %v", err)
	}

	// Create a task in beta (using root inbox)
	_, err = database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000013', 'P-00005', 'inbox', 'Inbox', '00000000-0000-0000-0000-000000000011', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create beta/inbox: %v", err)
	}

	_, err = database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000021', 'T-00002', 'beta-task', 'Beta Task', '00000000-0000-0000-0000-000000000013', 'open', 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create beta task: %v", err)
	}

	// Test: With WRKQ_PROJECT_ROOT=beta, --project alpha should find alpha-task
	app := createTestApp(t, database, dbPath)
	app.Config.ProjectRoot = "beta" // Simulates WRKQ_PROJECT_ROOT=beta

	// Now manually override to alpha (simulating --project alpha behavior)
	app.Config.ProjectRoot = "alpha"

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	lsJSON = true
	defer func() { lsJSON = false }()

	// List inbox - should show alpha/inbox content
	if err := runLs(app, cmd, []string{"inbox"}); err != nil {
		t.Fatalf("runLs failed: %v", err)
	}

	// Parse output
	var entries []struct {
		Slug string `json:"slug"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(buf.Bytes(), &entries); err != nil {
		t.Fatalf("Failed to parse JSON: %v\nOutput: %s", err, buf.String())
	}

	// Should find alpha-task, not beta-task
	foundAlpha := false
	for _, e := range entries {
		if e.Slug == "alpha-task" {
			foundAlpha = true
		}
		if e.Slug == "beta-task" {
			t.Error("Found beta-task when project was set to alpha")
		}
	}
	if !foundAlpha {
		t.Error("Expected to find alpha-task in alpha/inbox")
	}
}

func TestGlobalProjectFlag_FindCommand(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create two root projects
	_, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000030', 'P-00010', 'proj-one', 'Project One', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create proj-one: %v", err)
	}

	_, err = database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000031', 'P-00011', 'proj-two', 'Project Two', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create proj-two: %v", err)
	}

	// Create tasks in each project
	_, err = database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000040', 'T-00010', 'task-one', 'Task One', '00000000-0000-0000-0000-000000000030', 'open', 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create task-one: %v", err)
	}

	_, err = database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000041', 'T-00011', 'task-two', 'Task Two', '00000000-0000-0000-0000-000000000031', 'open', 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create task-two: %v", err)
	}

	app := createTestApp(t, database, dbPath)
	app.Config.ProjectRoot = "proj-one" // Simulates --project proj-one

	// Use find to search within proj-one
	results, _, err := findTasks(database, findOptions{
		paths: applyProjectRootToPaths(app.Config, []string{}, true),
		state: "open",
	}, true)
	if err != nil {
		t.Fatalf("findTasks failed: %v", err)
	}

	// Should only find task-one (in proj-one), not task-two
	foundOne := false
	for _, r := range results {
		if r.Slug == "task-one" {
			foundOne = true
		}
		if r.Slug == "task-two" {
			t.Error("Found task-two when project was set to proj-one")
		}
	}
	if !foundOne {
		t.Error("Expected to find task-one in proj-one")
	}
}

func TestGlobalProjectFlag_ResolvesByPath(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create a nested project structure: parent/child
	_, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000050', 'P-00020', 'parent', 'Parent', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create parent: %v", err)
	}

	_, err = database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000051', 'P-00021', 'child', 'Child', '00000000-0000-0000-0000-000000000050', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create child: %v", err)
	}

	// Create a task in child
	_, err = database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000052', 'T-00020', 'child-task', 'Child Task', '00000000-0000-0000-0000-000000000051', 'open', 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create child task: %v", err)
	}

	app := createTestApp(t, database, dbPath)
	// Set project root to "parent/child" - tests path-based resolution
	app.Config.ProjectRoot = "parent/child"

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	lsJSON = true
	defer func() { lsJSON = false }()

	// List with empty path - should default to project root
	if err := runLs(app, cmd, []string{}); err != nil {
		t.Fatalf("runLs failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "child-task") {
		t.Errorf("Expected to find child-task in parent/child, got: %s", output)
	}
}
