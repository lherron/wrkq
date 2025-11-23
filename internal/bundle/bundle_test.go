package bundle

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifest(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a valid manifest
	manifestPath := filepath.Join(tmpDir, "manifest.json")
	manifest := `{
		"machine_interface_version": 1,
		"version": "0.1.0",
		"timestamp": "2025-11-20T12:00:00Z",
		"with_attachments": true,
		"with_events": true
	}`

	if err := os.WriteFile(manifestPath, []byte(manifest), 0644); err != nil {
		t.Fatalf("Failed to write manifest: %v", err)
	}

	// Test loading
	m, err := LoadManifest(tmpDir)
	if err != nil {
		t.Fatalf("LoadManifest failed: %v", err)
	}

	if m.MachineInterfaceVersion != 1 {
		t.Errorf("Expected version 1, got %d", m.MachineInterfaceVersion)
	}

	if m.Version != "0.1.0" {
		t.Errorf("Expected version 0.1.0, got %s", m.Version)
	}
}

func TestLoadManifest_MissingVersion(t *testing.T) {
	tmpDir := t.TempDir()

	// Create manifest without machine_interface_version
	manifestPath := filepath.Join(tmpDir, "manifest.json")
	manifest := `{
		"timestamp": "2025-11-20T12:00:00Z"
	}`

	if err := os.WriteFile(manifestPath, []byte(manifest), 0644); err != nil {
		t.Fatalf("Failed to write manifest: %v", err)
	}

	// Test loading should fail
	_, err := LoadManifest(tmpDir)
	if err == nil {
		t.Fatal("Expected error for missing machine_interface_version")
	}
}

func TestLoadContainers(t *testing.T) {
	tmpDir := t.TempDir()

	// Create containers.txt
	containersPath := filepath.Join(tmpDir, "containers.txt")
	containers := `# This is a comment
portal
portal/auth
portal/api

# Another comment
backend
`

	if err := os.WriteFile(containersPath, []byte(containers), 0644); err != nil {
		t.Fatalf("Failed to write containers.txt: %v", err)
	}

	// Test loading
	c, err := LoadContainers(tmpDir)
	if err != nil {
		t.Fatalf("LoadContainers failed: %v", err)
	}

	expected := []string{"portal", "portal/auth", "portal/api", "backend"}
	if len(c) != len(expected) {
		t.Errorf("Expected %d containers, got %d", len(expected), len(c))
	}

	for i, container := range expected {
		if c[i] != container {
			t.Errorf("Expected container %s, got %s", container, c[i])
		}
	}
}

func TestLoadContainers_Missing(t *testing.T) {
	tmpDir := t.TempDir()

	// Test loading when containers.txt doesn't exist
	c, err := LoadContainers(tmpDir)
	if err != nil {
		t.Fatalf("LoadContainers failed: %v", err)
	}

	if len(c) != 0 {
		t.Errorf("Expected empty containers, got %v", c)
	}
}

func TestParseTaskDocument(t *testing.T) {
	content := `---
uuid: 123e4567-e89b-12d3-a456-426614174000
path: portal/auth/login
base_etag: 42
---

# Task Title

This is the task body.
`

	task, err := ParseTaskDocument(content)
	if err != nil {
		t.Fatalf("ParseTaskDocument failed: %v", err)
	}

	if task.UUID != "123e4567-e89b-12d3-a456-426614174000" {
		t.Errorf("Expected UUID 123e4567-e89b-12d3-a456-426614174000, got %s", task.UUID)
	}

	if task.Path != "portal/auth/login" {
		t.Errorf("Expected path portal/auth/login, got %s", task.Path)
	}

	if task.BaseEtag != 42 {
		t.Errorf("Expected base_etag 42, got %d", task.BaseEtag)
	}

	if task.Description != "# Task Title\n\nThis is the task body." {
		t.Errorf("Unexpected description: %s", task.Description)
	}
}

func TestParseTaskDocument_NoFrontmatter(t *testing.T) {
	content := `# Task Title

This is the task body without frontmatter.
`

	task, err := ParseTaskDocument(content)
	if err != nil {
		t.Fatalf("ParseTaskDocument failed: %v", err)
	}

	if task.Description != content {
		t.Errorf("Expected entire content as description")
	}
}

func TestLoadTasks(t *testing.T) {
	tmpDir := t.TempDir()
	tasksDir := filepath.Join(tmpDir, "tasks")

	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		t.Fatalf("Failed to create tasks dir: %v", err)
	}

	// Create a task file
	taskPath := filepath.Join(tasksDir, "portal", "auth")
	if err := os.MkdirAll(taskPath, 0755); err != nil {
		t.Fatalf("Failed to create task subdirs: %v", err)
	}

	taskFile := filepath.Join(taskPath, "login.md")
	taskContent := `---
uuid: 123e4567-e89b-12d3-a456-426614174000
base_etag: 10
---

# Login Task

Task description.
`

	if err := os.WriteFile(taskFile, []byte(taskContent), 0644); err != nil {
		t.Fatalf("Failed to write task file: %v", err)
	}

	// Test loading
	tasks, err := LoadTasks(tmpDir)
	if err != nil {
		t.Fatalf("LoadTasks failed: %v", err)
	}

	if len(tasks) != 1 {
		t.Fatalf("Expected 1 task, got %d", len(tasks))
	}

	task := tasks[0]
	if task.UUID != "123e4567-e89b-12d3-a456-426614174000" {
		t.Errorf("Expected UUID 123e4567-e89b-12d3-a456-426614174000, got %s", task.UUID)
	}

	if task.Path != filepath.Join("portal", "auth", "login") {
		t.Errorf("Expected path portal/auth/login, got %s", task.Path)
	}

	if task.BaseEtag != 10 {
		t.Errorf("Expected base_etag 10, got %d", task.BaseEtag)
	}
}

func TestLoad(t *testing.T) {
	tmpDir := t.TempDir()

	// Create complete bundle structure
	manifestPath := filepath.Join(tmpDir, "manifest.json")
	manifest := `{
		"machine_interface_version": 1,
		"timestamp": "2025-11-20T12:00:00Z"
	}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0644); err != nil {
		t.Fatalf("Failed to write manifest: %v", err)
	}

	containersPath := filepath.Join(tmpDir, "containers.txt")
	containers := "portal\nbackend\n"
	if err := os.WriteFile(containersPath, []byte(containers), 0644); err != nil {
		t.Fatalf("Failed to write containers: %v", err)
	}

	// Create a task
	tasksDir := filepath.Join(tmpDir, "tasks")
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		t.Fatalf("Failed to create tasks dir: %v", err)
	}

	taskFile := filepath.Join(tasksDir, "test.md")
	taskContent := "# Test Task"
	if err := os.WriteFile(taskFile, []byte(taskContent), 0644); err != nil {
		t.Fatalf("Failed to write task: %v", err)
	}

	// Test loading complete bundle
	bundle, err := Load(tmpDir)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if bundle.Manifest.MachineInterfaceVersion != 1 {
		t.Errorf("Expected version 1, got %d", bundle.Manifest.MachineInterfaceVersion)
	}

	if len(bundle.Containers) != 2 {
		t.Errorf("Expected 2 containers, got %d", len(bundle.Containers))
	}

	if len(bundle.Tasks) != 1 {
		t.Errorf("Expected 1 task, got %d", len(bundle.Tasks))
	}
}
