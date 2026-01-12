package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestProjectsCommand_ListsAllRootProjects(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create additional root projects
	_, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000010', 'P-00002', 'project-alpha', 'Project Alpha', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create project-alpha: %v", err)
	}

	_, err = database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000011', 'P-00003', 'project-beta', 'Project Beta', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create project-beta: %v", err)
	}

	// Create a child container (should NOT be listed by projects command)
	_, err = database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000012', 'P-00004', 'subproject', 'Subproject', '00000000-0000-0000-0000-000000000010', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create subproject: %v", err)
	}

	app := createTestApp(t, database, dbPath)

	// Set up command with JSON output
	projectsJSON = true
	defer func() { projectsJSON = false }()

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	if err := runProjects(app, cmd, nil); err != nil {
		t.Fatalf("runProjects failed: %v", err)
	}

	// Parse JSON output
	var projects []struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Slug  string `json:"slug"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(buf.Bytes(), &projects); err != nil {
		t.Fatalf("Failed to parse JSON: %v\nOutput: %s", err, buf.String())
	}

	// Should have 3 root projects (inbox from setupTestEnv + alpha + beta)
	if len(projects) != 3 {
		t.Fatalf("Expected 3 projects, got %d: %v", len(projects), projects)
	}

	// Verify all are type "project"
	for _, p := range projects {
		if p.Type != "project" {
			t.Errorf("Expected type 'project', got %q", p.Type)
		}
	}

	// Verify subproject is NOT included
	for _, p := range projects {
		if p.Slug == "subproject" {
			t.Error("Subproject should not be listed - only root projects")
		}
	}

	// Verify expected slugs are present
	slugs := make(map[string]bool)
	for _, p := range projects {
		slugs[p.Slug] = true
	}

	expectedSlugs := []string{"inbox", "project-alpha", "project-beta"}
	for _, slug := range expectedSlugs {
		if !slugs[slug] {
			t.Errorf("Expected slug %q to be present", slug)
		}
	}
}

func TestProjectsCommand_IgnoresProjectRoot(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create two root projects
	_, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000020', 'P-00002', 'demo', 'Demo Project', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create demo project: %v", err)
	}

	// Create app with ProjectRoot set
	app := createTestApp(t, database, dbPath)
	app.Config.ProjectRoot = "demo" // This should be IGNORED by the projects command

	// Set up command with one-per-line output
	projectsOne = true
	defer func() { projectsOne = false }()

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	if err := runProjects(app, cmd, nil); err != nil {
		t.Fatalf("runProjects failed: %v", err)
	}

	// Parse output
	output := strings.TrimSpace(buf.String())
	slugs := strings.Split(output, "\n")

	// Should have 2 projects (inbox + demo) - NOT filtered by ProjectRoot
	if len(slugs) != 2 {
		t.Fatalf("Expected 2 projects (ProjectRoot should be ignored), got %d: %v", len(slugs), slugs)
	}

	// Verify both projects are present
	slugMap := make(map[string]bool)
	for _, slug := range slugs {
		slugMap[slug] = true
	}

	if !slugMap["inbox"] {
		t.Error("Expected 'inbox' to be present")
	}
	if !slugMap["demo"] {
		t.Error("Expected 'demo' to be present")
	}
}

func TestProjectsCommand_NDJSON(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create a project
	_, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000030', 'P-00002', 'test-project', 'Test Project', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create test-project: %v", err)
	}

	app := createTestApp(t, database, dbPath)

	// Set up command with NDJSON output
	projectsNDJSON = true
	defer func() { projectsNDJSON = false }()

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	if err := runProjects(app, cmd, nil); err != nil {
		t.Fatalf("runProjects failed: %v", err)
	}

	// Parse NDJSON output
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")

	if len(lines) != 2 {
		t.Fatalf("Expected 2 NDJSON lines, got %d", len(lines))
	}

	// Verify each line is valid JSON
	for i, line := range lines {
		var project map[string]interface{}
		if err := json.Unmarshal([]byte(line), &project); err != nil {
			t.Errorf("Line %d is not valid JSON: %v", i, err)
		}
	}
}

func TestProjectsCommand_ExcludesArchivedByDefault(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create an archived project
	_, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, archived_at, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000040', 'P-00002', 'archived-project', 'Archived Project', datetime('now'), datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create archived project: %v", err)
	}

	app := createTestApp(t, database, dbPath)

	// Default: should NOT include archived
	projectsOne = true
	defer func() { projectsOne = false }()

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	if err := runProjects(app, cmd, nil); err != nil {
		t.Fatalf("runProjects failed: %v", err)
	}

	output := strings.TrimSpace(buf.String())

	// Should only have inbox (archived project excluded)
	if output != "inbox" {
		t.Errorf("Expected only 'inbox', got %q", output)
	}
}

func TestProjectsCommand_IncludesArchivedWithFlag(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	// Create an archived project
	_, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, archived_at, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000050', 'P-00002', 'archived-project', 'Archived Project', datetime('now'), datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create archived project: %v", err)
	}

	app := createTestApp(t, database, dbPath)

	// With --all flag: should include archived
	projectsOne = true
	projectsIncludeArchived = true
	defer func() {
		projectsOne = false
		projectsIncludeArchived = false
	}()

	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	if err := runProjects(app, cmd, nil); err != nil {
		t.Fatalf("runProjects failed: %v", err)
	}

	output := strings.TrimSpace(buf.String())
	slugs := strings.Split(output, "\n")

	// Should have 2 projects (inbox + archived)
	if len(slugs) != 2 {
		t.Fatalf("Expected 2 projects with --all flag, got %d: %v", len(slugs), slugs)
	}

	// Verify archived project is included
	found := false
	for _, slug := range slugs {
		if slug == "archived-project" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected 'archived-project' to be present with --all flag")
	}
}

func TestProjectsCommand_TableOutput(t *testing.T) {
	database, dbPath := setupTestEnv(t)

	app := createTestApp(t, database, dbPath)

	// Default table output (no flags set)
	buf := &bytes.Buffer{}
	cmd := &cobra.Command{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	if err := runProjects(app, cmd, nil); err != nil {
		t.Fatalf("runProjects failed: %v", err)
	}

	output := buf.String()

	// Table output should contain headers
	if !strings.Contains(output, "ID") {
		t.Error("Table output should contain 'ID' header")
	}
	if !strings.Contains(output, "Slug") {
		t.Error("Table output should contain 'Slug' header")
	}
	if !strings.Contains(output, "Title") {
		t.Error("Table output should contain 'Title' header")
	}

	// Should contain inbox
	if !strings.Contains(output, "inbox") {
		t.Error("Table output should contain 'inbox'")
	}
}
