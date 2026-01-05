package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindEnvLocal_InCurrentDir(t *testing.T) {
	// Create temp directory structure
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env.local")
	if err := os.WriteFile(envPath, []byte("TEST=value"), 0644); err != nil {
		t.Fatal(err)
	}

	// Change to temp dir
	oldCwd, _ := os.Getwd()
	defer os.Chdir(oldCwd)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	result := findEnvLocal()
	if result == "" {
		t.Error("expected to find .env.local in current directory")
	}
}

func TestFindEnvLocal_InParentDir(t *testing.T) {
	// Create temp directory structure: parent/.env.local, parent/child/
	tmpDir := t.TempDir()
	childDir := filepath.Join(tmpDir, "child")
	if err := os.Mkdir(childDir, 0755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(tmpDir, ".env.local")
	if err := os.WriteFile(envPath, []byte("TEST=parent"), 0644); err != nil {
		t.Fatal(err)
	}

	// Change to child dir
	oldCwd, _ := os.Getwd()
	defer os.Chdir(oldCwd)
	if err := os.Chdir(childDir); err != nil {
		t.Fatal(err)
	}

	result := findEnvLocal()
	if result == "" {
		t.Error("expected to find .env.local in parent directory")
	}
	// Resolve symlinks for comparison (macOS /var -> /private/var)
	expectedResolved, _ := filepath.EvalSymlinks(envPath)
	resultResolved, _ := filepath.EvalSymlinks(result)
	if resultResolved != expectedResolved {
		t.Errorf("expected %s, got %s", expectedResolved, resultResolved)
	}
}

func TestFindEnvLocal_InGrandparentDir(t *testing.T) {
	// Create: grandparent/.env.local, grandparent/parent/child/
	tmpDir := t.TempDir()
	parentDir := filepath.Join(tmpDir, "parent")
	childDir := filepath.Join(parentDir, "child")
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(tmpDir, ".env.local")
	if err := os.WriteFile(envPath, []byte("TEST=grandparent"), 0644); err != nil {
		t.Fatal(err)
	}

	// Change to grandchild dir
	oldCwd, _ := os.Getwd()
	defer os.Chdir(oldCwd)
	if err := os.Chdir(childDir); err != nil {
		t.Fatal(err)
	}

	result := findEnvLocal()
	if result == "" {
		t.Error("expected to find .env.local in grandparent directory")
	}
	// Resolve symlinks for comparison (macOS /var -> /private/var)
	expectedResolved, _ := filepath.EvalSymlinks(envPath)
	resultResolved, _ := filepath.EvalSymlinks(result)
	if resultResolved != expectedResolved {
		t.Errorf("expected %s, got %s", expectedResolved, resultResolved)
	}
}

func TestFindEnvLocal_ClosestWins(t *testing.T) {
	// Create: grandparent/.env.local, grandparent/parent/.env.local, grandparent/parent/child/
	tmpDir := t.TempDir()
	parentDir := filepath.Join(tmpDir, "parent")
	childDir := filepath.Join(parentDir, "child")
	if err := os.MkdirAll(childDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create .env.local in both grandparent and parent
	if err := os.WriteFile(filepath.Join(tmpDir, ".env.local"), []byte("TEST=grandparent"), 0644); err != nil {
		t.Fatal(err)
	}
	parentEnvPath := filepath.Join(parentDir, ".env.local")
	if err := os.WriteFile(parentEnvPath, []byte("TEST=parent"), 0644); err != nil {
		t.Fatal(err)
	}

	// Change to child dir
	oldCwd, _ := os.Getwd()
	defer os.Chdir(oldCwd)
	if err := os.Chdir(childDir); err != nil {
		t.Fatal(err)
	}

	result := findEnvLocal()
	// Resolve symlinks for comparison (macOS /var -> /private/var)
	expectedResolved, _ := filepath.EvalSymlinks(parentEnvPath)
	resultResolved, _ := filepath.EvalSymlinks(result)
	if resultResolved != expectedResolved {
		t.Errorf("expected closest .env.local (%s), got %s", expectedResolved, resultResolved)
	}
}

func TestFindEnvLocal_NotFound(t *testing.T) {
	// Create temp directory with no .env.local
	tmpDir := t.TempDir()

	// Change to temp dir
	oldCwd, _ := os.Getwd()
	defer os.Chdir(oldCwd)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	result := findEnvLocal()
	if result != "" {
		t.Errorf("expected empty string when no .env.local found, got %s", result)
	}
}
