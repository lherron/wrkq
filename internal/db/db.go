package db

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB wraps a SQLite database connection
type DB struct {
	*sql.DB
	path string
}

// Open opens a SQLite database at the given path and applies pragmas
func Open(path string) (*DB, error) {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Apply pragmas
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to apply pragma %q: %w", pragma, err)
		}
	}

	return &DB{DB: db, path: path}, nil
}

// Path returns the database file path
func (db *DB) Path() string {
	return db.path
}

// Migrate runs all pending migrations
func (db *DB) Migrate() error {
	// Read migration files from embedded FS
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	// Sort migration files
	var migrations []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			migrations = append(migrations, entry.Name())
		}
	}
	sort.Strings(migrations)

	// Create migrations tracking table if it doesn't exist
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	// Apply each migration
	for _, migration := range migrations {
		// Check if already applied
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", migration).Scan(&count)
		if err != nil {
			return fmt.Errorf("failed to check migration status for %s: %w", migration, err)
		}

		if count > 0 {
			// Already applied
			continue
		}

		// Read migration file
		content, err := migrationsFS.ReadFile(filepath.Join("migrations", migration))
		if err != nil {
			return fmt.Errorf("failed to read migration %s: %w", migration, err)
		}

		// Execute migration in a transaction
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction for %s: %w", migration, err)
		}

		_, err = tx.Exec(string(content))
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to execute migration %s: %w", migration, err)
		}

		// Record migration as applied
		_, err = tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", migration)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to record migration %s: %w", migration, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration %s: %w", migration, err)
		}
	}

	return nil
}

// BeginTx starts a new transaction
func (db *DB) BeginTx() (*sql.Tx, error) {
	return db.Begin()
}
