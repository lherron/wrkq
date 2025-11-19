package testutil

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// TempDB creates a temporary SQLite database for testing
func TempDB(t *testing.T) (*sql.DB, string) {
	t.Helper()

	// Create temp directory
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Initialize database
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	// Run migrations
	if err := database.Migrate(); err != nil {
		database.Close()
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Clean up on test completion
	t.Cleanup(func() {
		database.Close()
	})

	return database.DB, dbPath
}

// TempDir creates a temporary directory for testing
func TempDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// WriteFile writes content to a file in a temporary directory
func WriteFile(t *testing.T, dir, filename, content string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write file %s: %v", path, err)
	}
	return path
}

// ReadFile reads content from a file
func ReadFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read file %s: %v", path, err)
	}
	return string(data)
}

// AssertNoError asserts that an error is nil
func AssertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
}

// AssertError asserts that an error is not nil
func AssertError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
}

// AssertEqual asserts that two values are equal
func AssertEqual(t *testing.T, expected, actual interface{}) {
	t.Helper()
	if expected != actual {
		t.Fatalf("Expected %v, got %v", expected, actual)
	}
}

// AssertStringContains asserts that a string contains a substring
func AssertStringContains(t *testing.T, str, substr string) {
	t.Helper()
	if !contains(str, substr) {
		t.Fatalf("Expected string to contain %q, got %q", substr, str)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
