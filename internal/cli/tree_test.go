package cli

import (
	"bytes"
	"encoding/json"
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
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, kind, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000601', 'P-00601', 'empty-project', 'Empty Project', (SELECT uuid FROM containers WHERE kind = 'root'), 'project', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
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

func TestDisplayTreeNDJSONFlattensVisibleNodes(t *testing.T) {
	database, _ := setupTestEnv(t)

	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000701', 'T-00701', 'open-task', 'Open Task', '00000000-0000-0000-0000-000000000002', 'open', 2, '', '2026-06-12T12:04:05Z', '2026-06-12T12:04:05Z', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to seed task: %v", err)
	}

	var out bytes.Buffer
	err = displayTree(&out, database, "inbox", 0, false, true, outputSelection{Mode: outputModeNDJSON}, false)
	if err != nil {
		t.Fatalf("displayTree failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("Expected one NDJSON line, got %d: %q", len(lines), out.String())
	}

	var got treeStreamEntry
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("NDJSON line was not valid JSON: %v\n%s", err, lines[0])
	}
	if got.ID != "T-00701" || got.Path != "inbox/open-task" || got.Depth != 0 || got.State != "open" {
		t.Fatalf("Unexpected NDJSON entry: %+v", got)
	}
	if got.CreatedAt != "2026-06-12T12:04:05Z" {
		t.Fatalf("Expected created_at in NDJSON entry, got %+v", got)
	}
	if got.OpenedAt == nil || *got.OpenedAt != "2026-06-12T12:04:05Z" {
		t.Fatalf("Expected opened_at in NDJSON entry, got %+v", got)
	}
}

func TestDisplayTreeHumanShowsExternalChildBacklinkOnlyInHumanMode(t *testing.T) {
	database, _ := setupTestEnv(t)

	_, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, kind, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES
			('00000000-0000-0000-0000-000000000901', 'P-00901', 'tree-a', 'Tree A', (SELECT uuid FROM containers WHERE kind = 'root'), 'project', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1),
			('00000000-0000-0000-0000-000000000902', 'P-00902', 'tree-b', 'Tree B', (SELECT uuid FROM containers WHERE kind = 'root'), 'project', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("seed containers: %v", err)
	}
	_, err = database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, kind, parent_task_uuid, description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES
			('00000000-0000-0000-0000-000000000903', 'T-00903', 'parent-task', 'Parent Task', '00000000-0000-0000-0000-000000000901', 'open', 2, 'task', NULL, '', '2026-06-12T12:04:05Z', '2026-06-12T12:04:05Z', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1),
			('00000000-0000-0000-0000-000000000904', 'T-00904', 'external-child', 'External Child', '00000000-0000-0000-0000-000000000902', 'completed', 2, 'subtask', '00000000-0000-0000-0000-000000000903', '', '2026-06-12T12:04:06Z', '2026-06-12T12:04:06Z', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("seed tasks: %v", err)
	}

	var out bytes.Buffer
	if err := displayTree(&out, database, "tree-a", 0, true, false, outputSelection{Mode: outputModeHuman}, false); err != nil {
		t.Fatalf("human displayTree failed: %v", err)
	}
	human := out.String()
	if !strings.Contains(human, "Parent Task") || !strings.Contains(human, "External Child") {
		t.Fatalf("human tree should show parent and external child, got:\n%s", human)
	}
	if !strings.Contains(human, "(external: tree-b P-00902)") {
		t.Fatalf("human tree should mark external child with project context, got:\n%s", human)
	}

	out.Reset()
	if err := displayTree(&out, database, "tree-a", 0, true, false, outputSelection{Mode: outputModeJSON}, false); err != nil {
		t.Fatalf("json displayTree failed: %v", err)
	}
	if strings.Contains(out.String(), "External Child") || strings.Contains(out.String(), "external_child") {
		t.Fatalf("JSON tree must remain residency-scoped, got:\n%s", out.String())
	}

	out.Reset()
	if err := displayTree(&out, database, "tree-a", 0, true, false, outputSelection{Mode: outputModeNDJSON}, false); err != nil {
		t.Fatalf("ndjson displayTree failed: %v", err)
	}
	if strings.Contains(out.String(), "External Child") || strings.Contains(out.String(), "external-child") {
		t.Fatalf("NDJSON tree must remain residency-scoped, got:\n%s", out.String())
	}

	out.Reset()
	if err := displayTree(&out, database, "tree-b", 0, true, false, outputSelection{Mode: outputModeHuman}, false); err != nil {
		t.Fatalf("resident human displayTree failed: %v", err)
	}
	resident := out.String()
	if !strings.Contains(resident, "External Child") {
		t.Fatalf("resident --all tree should show completed cross-project child, got:\n%s", resident)
	}
	if strings.Contains(resident, "(external:") {
		t.Fatalf("resident tree should not mark its own task as external, got:\n%s", resident)
	}
}

func TestFormatNodeDisplayTaskShowsBareIDAndTitleWithoutSlug(t *testing.T) {
	display := formatNodeDisplay(&treeNode{
		Type:      "task",
		ID:        "T-12345",
		Slug:      "example-task",
		Title:     "Example Task",
		State:     "open",
		CreatedAt: "2026-06-12T15:04:05Z",
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
	if !strings.Contains(display, "<opened ") || !strings.Contains(display, " ago>") {
		t.Fatalf("Expected open task display to contain opened duration, got %q", display)
	}
	if strings.Contains(display, "<open>") {
		t.Fatalf("Expected open task display not to contain bare open state, got %q", display)
	}
	if strings.Contains(display, "opened on") {
		t.Fatalf("Expected open task display not to contain opened date, got %q", display)
	}
}

func TestFormatTreeTaskStateFallsBackToStateWithoutCreatedAt(t *testing.T) {
	got := formatTreeTaskState(&treeNode{State: "open"})
	if got != "open" {
		t.Fatalf("Expected missing created_at to fall back to open, got %q", got)
	}

	got = formatTreeTaskState(&treeNode{State: "draft", CreatedAt: "2026-06-12T15:04:05Z"})
	if got != "draft" {
		t.Fatalf("Expected non-open state to remain unchanged, got %q", got)
	}
}

// Relative-age formatting moved to internal/style; see TestFormatOpenedAge there.

func TestDisplayTreeHeaderShowsTopLevelProjectID(t *testing.T) {
	database, _ := setupTestEnv(t)

	// Seed a project with a nested container and an open task so the tree has
	// visible content under both the project root and the subpath.
	_, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, kind, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES
			('00000000-0000-0000-0000-000000000801', 'P-00801', 'proj', 'Proj', (SELECT uuid FROM containers WHERE kind = 'root'), 'project', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1),
			('00000000-0000-0000-0000-000000000802', 'P-00802', 'sub', 'Sub', '00000000-0000-0000-0000-000000000801', 'directory', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to seed containers: %v", err)
	}
	_, err = database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000803', 'T-00803', 'open-task', 'Open Task', '00000000-0000-0000-0000-000000000802', 'open', 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to seed task: %v", err)
	}

	// Project root header carries the project's own friendly ID.
	var out bytes.Buffer
	if err := displayTree(&out, database, "proj", 0, false, false, outputSelection{Mode: outputModeHuman}, false); err != nil {
		t.Fatalf("displayTree failed: %v", err)
	}
	header := strings.SplitN(out.String(), "\n", 2)[0]
	if header != "proj [P-00801]" {
		t.Fatalf("Expected project root header %q, got %q", "proj [P-00801]", header)
	}

	// A subpath header still anchors to the top-level parent project's ID.
	out.Reset()
	if err := displayTree(&out, database, "proj/sub", 0, false, false, outputSelection{Mode: outputModeHuman}, false); err != nil {
		t.Fatalf("displayTree failed: %v", err)
	}
	header = strings.SplitN(out.String(), "\n", 2)[0]
	if header != "proj/sub [P-00801]" {
		t.Fatalf("Expected subpath header %q, got %q", "proj/sub [P-00801]", header)
	}

	// JSON output exposes the project_id field.
	out.Reset()
	if err := displayTree(&out, database, "proj", 0, false, false, outputSelection{Mode: outputModeJSON}, false); err != nil {
		t.Fatalf("displayTree failed: %v", err)
	}
	var got treeOutput
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("tree JSON was not valid: %v\n%s", err, out.String())
	}
	if got.ProjectID != "P-00801" {
		t.Fatalf("Expected JSON project_id P-00801, got %q", got.ProjectID)
	}

	// Porcelain output stays machine-parseable: no annotation on the header.
	out.Reset()
	if err := displayTree(&out, database, "proj", 0, false, false, outputSelection{Mode: outputModeRaw}, true); err != nil {
		t.Fatalf("displayTree failed: %v", err)
	}
	header = strings.SplitN(out.String(), "\n", 2)[0]
	if header != "proj" {
		t.Fatalf("Expected porcelain header %q, got %q", "proj", header)
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
