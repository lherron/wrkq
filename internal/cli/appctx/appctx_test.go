package appctx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/spf13/cobra"
)

func TestBootstrap_ConfigOnly(t *testing.T) {
	// Create temp directory for test database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Set environment for test
	_ = os.Setenv("WRKQ_DB_PATH", dbPath)
	defer func() { _ = os.Unsetenv("WRKQ_DB_PATH") }()

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
	if app.PrincipalRef != "" {
		t.Error("PrincipalRef should be empty when NeedsActor is false")
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
	_ = database.Close()

	// Set environment for test
	_ = os.Setenv("WRKQ_DB_PATH", dbPath)
	defer func() { _ = os.Unsetenv("WRKQ_DB_PATH") }()

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
	_ = database.Close()

	database2, err := db.Open(overridePath)
	if err != nil {
		t.Fatalf("Failed to open override database: %v", err)
	}
	if err := database2.Migrate(); err != nil {
		t.Fatalf("Failed to run migrations on override: %v", err)
	}
	_ = database2.Close()

	// Set environment to point to one path
	_ = os.Setenv("WRKQ_DB_PATH", dbPath)
	defer func() { _ = os.Unsetenv("WRKQ_DB_PATH") }()

	// Create a test command with --db flag set to override path
	cmd := &cobra.Command{}
	cmd.Flags().String("db", "", "Database path")
	cmd.Flags().String("as", "", "Actor")
	_ = cmd.ParseFlags([]string{"--db", overridePath})

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
	clearAttributionEnv(t)

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
	_ = database.Close()

	// Set environment for test. Attribution is principal-only: a canonical
	// principal ref is required.
	t.Setenv("WRKQ_DB_PATH", dbPath)
	t.Setenv("WRKQ_PRINCIPAL_REF", "agent:test-actor")

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

	if app.PrincipalRef != "agent:test-actor" {
		t.Errorf("PrincipalRef should be 'agent:test-actor', got %q", app.PrincipalRef)
	}
}

func TestBootstrap_WithActor_RejectsBareSlug(t *testing.T) {
	clearAttributionEnv(t)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}
	_ = database.Close()

	oldCwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldCwd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change to temp directory: %v", err)
	}

	t.Setenv("WRKQ_DB_PATH", dbPath)

	// A bare slug (not agent:<id>) is rejected as caller attribution.
	for _, bad := range []string{"local-human", "A-00001", "system:scheduler", "agent:scope:project:p:task:t"} {
		cmd := &cobra.Command{}
		cmd.Flags().String("db", "", "Database path")
		cmd.Flags().String("as", "", "Actor")
		_ = cmd.ParseFlags([]string{"--as", bad})

		if _, err := Bootstrap(cmd, WithActor()); err == nil {
			t.Errorf("Bootstrap should reject caller attribution %q", bad)
		}
	}
}

func TestBootstrap_WithScopeOnlyAttribution(t *testing.T) {
	clearAttributionEnv(t)

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}
	_ = database.Close()

	oldCwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldCwd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change to temp directory: %v", err)
	}

	t.Setenv("WRKQ_DB_PATH", dbPath)
	t.Setenv("ASP_SCOPE_REF", "agent:scope-cody:project:wrkq:task:primary")

	cmd := &cobra.Command{}
	cmd.Flags().String("db", "", "Database path")
	cmd.Flags().String("as", "", "Principal")
	cmd.Flags().String("scope", "", "Scope")

	app, err := Bootstrap(cmd, WithActor())
	if err != nil {
		t.Fatalf("Bootstrap failed: %v", err)
	}
	defer app.Close()

	if app.PrincipalRef != "agent:scope-cody" {
		t.Errorf("PrincipalRef should be derived from scope, got %q", app.PrincipalRef)
	}
	if app.ScopeRef != "agent:scope-cody:project:wrkq:task:primary" {
		t.Errorf("ScopeRef should preserve full scope, got %q", app.ScopeRef)
	}
}

func TestBootstrap_PrincipalNotConfigured(t *testing.T) {
	clearAttributionEnv(t)

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
	_ = database.Close()

	// Change to temp dir to avoid finding parent .env.local files
	oldCwd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldCwd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change to temp directory: %v", err)
	}

	// Set DB path but NOT actor/principal/scope.
	t.Setenv("WRKQ_DB_PATH", dbPath)

	// Create a test command
	cmd := &cobra.Command{}
	cmd.Flags().String("db", "", "Database path")
	cmd.Flags().String("as", "", "Actor")

	// Bootstrap with actor should fail
	_, err = Bootstrap(cmd, WithActor())
	if err == nil {
		t.Fatal("Expected error when principal is not configured")
	}

	expectedMsg := "no principal configured"
	if !strings.Contains(err.Error(), expectedMsg) {
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

func clearAttributionEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"WRKQ_PRINCIPAL_REF",
		"WRKQ_ACTOR",
		"WRKQ_ACTOR_ID",
		"ASP_SCOPE_REF",
		"ASP_HANDLE",
		"ASP_AGENT_ID",
		"ASP_PROJECT",
	} {
		t.Setenv(key, "")
	}
}
