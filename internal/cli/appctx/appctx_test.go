package appctx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/spf13/cobra"
)

func TestBootstrap_ConfigOnly(t *testing.T) {
	// Create temp directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Set environment for test
	os.Setenv("WRKQ_DB_PATH", dbPath)
	defer os.Unsetenv("WRKQ_DB_PATH")

	// Create a test command with the db flag
	cmd := &cobra.Command{}
	cmd.Flags().String("db", "", "Database path")
	cmd.Flags().String("as", "", "Actor")

	// Bootstrap with NeedsDB=false (config only)
	app, err := Bootstrap(cmd, Options{NeedsDB: false, NeedsActor: false})
	if err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}
	defer app.Close()

	if app.Config == nil {
		t.Error("Config should not be nil")
	}
	if app.DB != nil {
		t.Error("DB should be nil when NeedsDB is false")
	}
	if app.ActorUUID != "" {
		t.Error("ActorUUID should be empty when NeedsActor is false")
	}
}

func TestBootstrap_WithDB(t *testing.T) {
	// Create temp directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Initialize test database with migrations
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}
	database.Close()

	// Set environment for test
	os.Setenv("WRKQ_DB_PATH", dbPath)
	defer os.Unsetenv("WRKQ_DB_PATH")

	// Create a test command
	cmd := &cobra.Command{}
	cmd.Flags().String("db", "", "Database path")
	cmd.Flags().String("as", "", "Actor")

	// Bootstrap with default options (NeedsDB=true)
	app, err := Bootstrap(cmd, DefaultOptions())
	if err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}
	defer app.Close()

	if app.Config == nil {
		t.Error("Config should not be nil")
	}
	if app.DB == nil {
		t.Error("DB should not be nil when NeedsDB is true")
	}
}

func TestBootstrap_DBFlagOverride(t *testing.T) {
	// Create temp directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	overridePath := filepath.Join(tmpDir, "override.db")

	// Initialize both databases with migrations
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}
	database.Close()

	database2, err := db.Open(overridePath)
	if err != nil {
		t.Fatalf("Failed to open override database: %v", err)
	}
	if err := database2.Migrate(); err != nil {
		t.Fatalf("Failed to run migrations on override: %v", err)
	}
	database2.Close()

	// Set environment to point to one path
	os.Setenv("WRKQ_DB_PATH", dbPath)
	defer os.Unsetenv("WRKQ_DB_PATH")

	// Create a test command with --db flag set to override path
	cmd := &cobra.Command{}
	cmd.Flags().String("db", "", "Database path")
	cmd.Flags().String("as", "", "Actor")
	cmd.ParseFlags([]string{"--db", overridePath})

	// Bootstrap
	app, err := Bootstrap(cmd, DefaultOptions())
	if err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}
	defer app.Close()

	// Verify the override path was used
	if app.Config.DBPath != overridePath {
		t.Errorf("DBPath should be override path %q, got %q", overridePath, app.Config.DBPath)
	}
}

func TestBootstrap_WithActor(t *testing.T) {
	// Create temp directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Initialize test database with migrations
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	// Create a test actor
	_, err = database.Exec(`
		INSERT INTO actors (id, slug, role) VALUES ('A-00001', 'test-actor', 'human')
	`)
	if err != nil {
		t.Fatalf("Failed to create test actor: %v", err)
	}
	database.Close()

	// Set environment for test
	os.Setenv("WRKQ_DB_PATH", dbPath)
	os.Setenv("WRKQ_ACTOR", "test-actor")
	defer os.Unsetenv("WRKQ_DB_PATH")
	defer os.Unsetenv("WRKQ_ACTOR")

	// Create a test command
	cmd := &cobra.Command{}
	cmd.Flags().String("db", "", "Database path")
	cmd.Flags().String("as", "", "Actor")

	// Bootstrap with actor
	app, err := Bootstrap(cmd, WithActor())
	if err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}
	defer app.Close()

	if app.ActorUUID == "" {
		t.Error("ActorUUID should not be empty when NeedsActor is true")
	}
	if app.ActorID != "A-00001" {
		t.Errorf("ActorID should be 'A-00001', got %q", app.ActorID)
	}
}

func TestBootstrap_ActorNotConfigured(t *testing.T) {
	// Create temp directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Initialize test database with migrations
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}
	database.Close()

	// Change to temp dir to avoid finding parent .env.local files
	oldCwd, _ := os.Getwd()
	defer os.Chdir(oldCwd)
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change to temp directory: %v", err)
	}

	// Set DB path but NOT actor
	os.Setenv("WRKQ_DB_PATH", dbPath)
	os.Unsetenv("WRKQ_ACTOR")
	os.Unsetenv("WRKQ_ACTOR_ID")
	defer os.Unsetenv("WRKQ_DB_PATH")

	// Create a test command
	cmd := &cobra.Command{}
	cmd.Flags().String("db", "", "Database path")
	cmd.Flags().String("as", "", "Actor")

	// Bootstrap with actor should fail
	_, err = Bootstrap(cmd, WithActor())
	if err == nil {
		t.Fatal("Expected error when actor is not configured")
	}

	expectedMsg := "no actor configured"
	if !containsSubstring(err.Error(), expectedMsg) {
		t.Errorf("Error message should contain %q, got %q", expectedMsg, err.Error())
	}
}

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()
	if !opts.NeedsDB {
		t.Error("DefaultOptions should have NeedsDB=true")
	}
	if opts.NeedsActor {
		t.Error("DefaultOptions should have NeedsActor=false")
	}
}

func TestWithActor(t *testing.T) {
	opts := WithActor()
	if !opts.NeedsDB {
		t.Error("WithActor should have NeedsDB=true")
	}
	if !opts.NeedsActor {
		t.Error("WithActor should have NeedsActor=true")
	}
}

func TestApp_Close_Multiple(t *testing.T) {
	// Close should be safe to call multiple times
	app := &App{}
	app.Close()
	app.Close() // Should not panic
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstringHelper(s, substr))
}

func containsSubstringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
