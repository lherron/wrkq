package db_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func TestRequiresMigrationError(t *testing.T) {
	// Create a temporary database with only some migrations applied
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Open and create schema_migrations table with only first migration
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("could not open db: %v", err)
	}
	defer database.Close()

	// Create schema_migrations table and add only first migration
	_, err = database.Exec(`
		CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		)
	`)
	if err != nil {
		t.Fatalf("could not create schema_migrations: %v", err)
	}

	_, err = database.Exec(`INSERT INTO schema_migrations (version) VALUES ('000001_baseline.sql')`)
	if err != nil {
		t.Fatalf("could not insert migration: %v", err)
	}

	// Test RequiresMigrationError
	migErr := database.RequiresMigrationError()
	if migErr == nil {
		t.Fatal("expected migration error, got nil")
	}

	errStr := migErr.Error()
	t.Logf("Error message: %s", errStr)

	// Check that error contains db path
	if !strings.Contains(errStr, dbPath) {
		t.Errorf("error should contain db path '%s', got: %s", dbPath, errStr)
	}

	// Check that error contains version
	if !strings.Contains(errStr, "000001_baseline.sql") {
		t.Errorf("error should contain version '000001_baseline.sql', got: %s", errStr)
	}

	// Check that error mentions pending migrations
	if !strings.Contains(errStr, "pending migration") {
		t.Errorf("error should mention pending migrations, got: %s", errStr)
	}

	// Check that error suggests wrkqadm migrate
	if !strings.Contains(errStr, "wrkqadm migrate") {
		t.Errorf("error should suggest 'wrkqadm migrate', got: %s", errStr)
	}
}

func TestRequiresMigrationErrorFreshDB(t *testing.T) {
	// Test with no migrations applied (fresh db)
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("could not open db: %v", err)
	}
	defer database.Close()

	migErr := database.RequiresMigrationError()
	if migErr == nil {
		t.Fatal("expected migration error for fresh db, got nil")
	}

	errStr := migErr.Error()
	t.Logf("Fresh DB Error: %s", errStr)

	if !strings.Contains(errStr, "version: none") {
		t.Errorf("fresh db error should contain 'version: none', got: %s", errStr)
	}

	if !strings.Contains(errStr, dbPath) {
		t.Errorf("error should contain db path '%s', got: %s", dbPath, errStr)
	}
}

func TestRequiresMigrationErrorFullyMigrated(t *testing.T) {
	// Test with fully migrated database (should return nil)
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("could not open db: %v", err)
	}
	defer database.Close()

	// Run all migrations
	if err := database.Migrate(); err != nil {
		t.Fatalf("could not run migrations: %v", err)
	}

	// Should return nil when fully migrated
	migErr := database.RequiresMigrationError()
	if migErr != nil {
		t.Errorf("expected nil for fully migrated db, got: %v", migErr)
	}
}
