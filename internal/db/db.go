package db

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	sqlite3 "github.com/mattn/go-sqlite3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB wraps a SQLite database connection
type DB struct {
	*sql.DB
	path string
}

// Open opens a SQLite database at the given path and applies the connection
// policy via DSN parameters.
//
// The pragmas are carried in the DSN — not applied with post-open db.Exec — so
// that go-sqlite3 reapplies them on EVERY physical connection database/sql
// opens, giving a uniform policy across the whole pool. _txlock=immediate is
// the load-bearing correctness mechanism: it makes every write transaction
// begin with BEGIN IMMEDIATE (reserving the writer lock up front) instead of
// the default deferred transaction that reads first and then loses an
// un-retryable lock upgrade under concurrent writers (the T-05066 "database is
// locked"/internal-error failure). busy_timeout then lets contending writers
// wait and serialize rather than failing instantly. See docs/wrkq-wrkf-rpc.md.
func Open(path string) (*DB, error) {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	dsn := path + sep + "_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_txlock=immediate"

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Fail-fast verification that the policy is actually live on a connection.
	// The DSN params above are the policy carrier; this is only a sanity check.
	for _, pragma := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to apply pragma %q: %w", pragma, err)
		}
	}

	// DB.path stays the clean path (no DSN params) so Path() and any consumer
	// that re-derives a DSN from it (e.g. workflow.withImmediateTx) does not
	// inherit doubly-encoded query parameters.
	return &DB{DB: db, path: path}, nil
}

// IsBusy reports whether err (anywhere in its chain) is a SQLite busy/locked
// contention error — i.e. a writer that lost the race for the write lock even
// after waiting out busy_timeout. Callers map this to a typed, retryable
// contention error rather than a generic internal error.
func IsBusy(err error) bool {
	if err == nil {
		return false
	}
	var se sqlite3.Error
	if errors.As(err, &se) {
		return se.Code == sqlite3.ErrBusy || se.Code == sqlite3.ErrLocked
	}
	return false
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

		if err := db.applyMigration(migration, content); err != nil {
			return err
		}
	}

	return nil
}

// BeginTx starts a new transaction
func (db *DB) BeginTx() (*sql.Tx, error) {
	return db.Begin()
}

// MigrateWithInfo runs all pending migrations and returns the list of applied migrations
func (db *DB) MigrateWithInfo() ([]string, error) {
	// Read migration files from embedded FS
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("failed to read migrations directory: %w", err)
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
		return nil, fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	var applied []string

	// Apply each migration
	for _, migration := range migrations {
		// Check if already applied
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE version = ?", migration).Scan(&count)
		if err != nil {
			return applied, fmt.Errorf("failed to check migration status for %s: %w", migration, err)
		}

		if count > 0 {
			// Already applied
			continue
		}

		// Read migration file
		content, err := migrationsFS.ReadFile(filepath.Join("migrations", migration))
		if err != nil {
			return applied, fmt.Errorf("failed to read migration %s: %w", migration, err)
		}

		if err := db.applyMigration(migration, content); err != nil {
			return applied, err
		}

		applied = append(applied, migration)
	}

	return applied, nil
}

func (db *DB) applyMigration(migration string, content []byte) error {
	foreignKeysOff := strings.Contains(string(content), "wrkq:foreign-keys-off")
	if foreignKeysOff {
		if _, err := db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
			return fmt.Errorf("failed to disable foreign keys for %s: %w", migration, err)
		}
		defer func() { _, _ = db.Exec("PRAGMA foreign_keys = ON") }()
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction for %s: %w", migration, err)
	}

	if _, err := tx.Exec(string(content)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed to execute migration %s: %w", migration, err)
	}

	if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", migration); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed to record migration %s: %w", migration, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit migration %s: %w", migration, err)
	}
	return nil
}

// MigrationStatus returns lists of applied and pending migrations
func (db *DB) MigrationStatus() (applied []string, pending []string, err error) {
	// Read migration files from embedded FS
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read migrations directory: %w", err)
	}

	// Sort migration files
	var allMigrations []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			allMigrations = append(allMigrations, entry.Name())
		}
	}
	sort.Strings(allMigrations)

	// Check if schema_migrations table exists
	var tableExists int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name='schema_migrations'
	`).Scan(&tableExists)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to check for schema_migrations table: %w", err)
	}

	if tableExists == 0 {
		// No migrations applied yet
		return nil, allMigrations, nil
	}

	// Get applied migrations
	appliedSet := make(map[string]bool)
	rows, err := db.Query("SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query schema_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, nil, fmt.Errorf("failed to scan migration version: %w", err)
		}
		appliedSet[version] = true
		applied = append(applied, version)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("error iterating migrations: %w", err)
	}

	// Determine pending migrations
	for _, m := range allMigrations {
		if !appliedSet[m] {
			pending = append(pending, m)
		}
	}

	return applied, pending, nil
}

// RequiresMigrationError checks if the database has pending migrations and returns
// a descriptive error including the database path and current schema version.
// Returns nil if no migrations are pending.
func (db *DB) RequiresMigrationError() error {
	applied, pending, err := db.MigrationStatus()
	if err != nil {
		return fmt.Errorf("failed to check migration status: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}

	// Determine current version (last applied migration, or "none")
	currentVersion := "none"
	if len(applied) > 0 {
		currentVersion = applied[len(applied)-1]
	}

	return fmt.Errorf("database at %s (version: %s) requires migration: %d pending migration(s). Run 'wrkqadm migrate' to update",
		db.path, currentVersion, len(pending))
}
