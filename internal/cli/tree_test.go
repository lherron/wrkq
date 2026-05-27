package cli

import (
	"strings"
	"testing"
)

func TestBuildTreeShowsOnlyContainersWithDraftOpenTaskDescendants(t *testing.T) {
	database, _ := setupTestEnv(t)

	_, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES
			('00000000-0000-0000-0000-000000000101', 'P-00101', 'active-work', 'Active Work', '00000000-0000-0000-0000-000000000002', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1),
			('00000000-0000-0000-0000-000000000102', 'P-00102', 'closed-work', 'Closed Work', '00000000-0000-0000-0000-000000000002', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1),
			('00000000-0000-0000-0000-000000000103', 'P-00103', 'nested', 'Nested', '00000000-0000-0000-0000-000000000102', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to seed containers: %v", err)
	}

	tasks := []struct {
		uuid        string
		id          string
		slug        string
		title       string
		projectUUID string
		state       string
	}{
		{"00000000-0000-0000-0000-000000000201", "T-00201", "draft-task", "Draft Task", "00000000-0000-0000-0000-000000000101", "draft"},
		{"00000000-0000-0000-0000-000000000202", "T-00202", "open-task", "Open Task", "00000000-0000-0000-0000-000000000101", "open"},
		{"00000000-0000-0000-0000-000000000203", "T-00203", "idea-task", "Idea Task", "00000000-0000-0000-0000-000000000101", "idea"},
		{"00000000-0000-0000-0000-000000000204", "T-00204", "progress-task", "Progress Task", "00000000-0000-0000-0000-000000000101", "in_progress"},
		{"00000000-0000-0000-0000-000000000205", "T-00205", "blocked-task", "Blocked Task", "00000000-0000-0000-0000-000000000101", "blocked"},
		{"00000000-0000-0000-0000-000000000206", "T-00206", "completed-task", "Completed Task", "00000000-0000-0000-0000-000000000102", "completed"},
		{"00000000-0000-0000-0000-000000000207", "T-00207", "cancelled-task", "Cancelled Task", "00000000-0000-0000-0000-000000000103", "cancelled"},
	}
	for _, task := range tasks {
		_, err := database.Exec(`
			INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, ?, ?, ?, ?, ?, 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
		`, task.uuid, task.id, task.slug, task.title, task.projectUUID, task.state)
		if err != nil {
			t.Fatalf("Failed to seed task %s: %v", task.id, err)
		}
	}

	root, err := buildTree(database, "inbox", 0, false, false, true, 0)
	if err != nil {
		t.Fatalf("buildTree failed: %v", err)
	}

	slugs := collectTreeSlugs(root)
	wantPresent := []string{"active-work", "draft-task", "open-task"}
	for _, slug := range wantPresent {
		if !slugs[slug] {
			t.Fatalf("Expected tree to include %q, got %#v", slug, slugs)
		}
	}

	wantHidden := []string{"closed-work", "nested", "idea-task", "progress-task", "blocked-task", "completed-task", "cancelled-task"}
	for _, slug := range wantHidden {
		if slugs[slug] {
			t.Fatalf("Expected tree to hide %q, got %#v", slug, slugs)
		}
	}
	if root.HiddenContainerCount != 2 {
		t.Fatalf("Expected 2 hidden containers, got %d", root.HiddenContainerCount)
	}
}

func TestBuildTreeOpenFlagExcludesDraftTasks(t *testing.T) {
	database, _ := setupTestEnv(t)

	tasks := []struct {
		uuid  string
		id    string
		slug  string
		state string
	}{
		{"00000000-0000-0000-0000-000000000301", "T-00301", "draft-task", "draft"},
		{"00000000-0000-0000-0000-000000000302", "T-00302", "open-task", "open"},
	}
	for _, task := range tasks {
		_, err := database.Exec(`
			INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
			VALUES (?, ?, ?, ?, '00000000-0000-0000-0000-000000000002', ?, 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
		`, task.uuid, task.id, task.slug, task.slug, task.state)
		if err != nil {
			t.Fatalf("Failed to seed task %s: %v", task.id, err)
		}
	}

	root, err := buildTree(database, "inbox", 0, false, true, true, 0)
	if err != nil {
		t.Fatalf("buildTree failed: %v", err)
	}

	slugs := collectTreeSlugs(root)
	if slugs["draft-task"] {
		t.Fatalf("Expected --open tree to hide draft-task, got %#v", slugs)
	}
	if !slugs["open-task"] {
		t.Fatalf("Expected --open tree to include open-task, got %#v", slugs)
	}
}

func TestBuildTreeAllShowsEmptyContainers(t *testing.T) {
	database, _ := setupTestEnv(t)

	_, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000501', 'P-00501', 'empty-container', 'Empty Container', '00000000-0000-0000-0000-000000000002', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to seed container: %v", err)
	}

	root, err := buildTree(database, "inbox", 0, true, false, false, 0)
	if err != nil {
		t.Fatalf("buildTree failed: %v", err)
	}

	slugs := collectTreeSlugs(root)
	if !slugs["empty-container"] {
		t.Fatalf("Expected --all tree to include empty-container, got %#v", slugs)
	}
	if root.HiddenContainerCount != 0 {
		t.Fatalf("Expected no hidden containers with --all, got %d", root.HiddenContainerCount)
	}
}

func TestBuildTreeAlwaysShowsInbox(t *testing.T) {
	database, _ := setupTestEnv(t)

	_, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000601', 'P-00601', 'empty-project', 'Empty Project', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to seed container: %v", err)
	}

	root, err := buildTree(database, "", 0, false, false, true, 0)
	if err != nil {
		t.Fatalf("buildTree failed: %v", err)
	}

	slugs := collectTreeSlugs(root)
	if !slugs["inbox"] {
		t.Fatalf("Expected empty inbox to remain visible, got %#v", slugs)
	}
	if slugs["empty-project"] {
		t.Fatalf("Expected empty non-inbox container to be hidden, got %#v", slugs)
	}
	if root.HiddenContainerCount != 1 {
		t.Fatalf("Expected 1 hidden container, got %d", root.HiddenContainerCount)
	}
}

func TestFormatNodeDisplayTaskShowsBareIDAndTitleWithoutSlug(t *testing.T) {
	display := formatNodeDisplay(&treeNode{
		Type:  "task",
		ID:    "T-12345",
		Slug:  "example-task",
		Title: "Example Task",
		State: "open",
	}, false)

	if !strings.HasPrefix(display, "T-12345 ") {
		t.Fatalf("Expected task display to start with bare ID, got %q", display)
	}
	if strings.Contains(display, "[T-12345]") {
		t.Fatalf("Expected task display not to contain bracketed ID, got %q", display)
	}
	if strings.Contains(display, "example-task") {
		t.Fatalf("Expected task display not to contain slug, got %q", display)
	}
	if !strings.Contains(display, "Example Task") {
		t.Fatalf("Expected task display to contain title, got %q", display)
	}
}

func collectTreeSlugs(root *treeNode) map[string]bool {
	slugs := make(map[string]bool)
	var walk func(*treeNode)
	walk = func(node *treeNode) {
		if node.Slug != "" {
			slugs[node.Slug] = true
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(root)
	return slugs
}
