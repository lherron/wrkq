package indexdb

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

const SchemaVersion = "2"

type DB struct {
	*sql.DB
	path string
}

type Status struct {
	Path                 string  `json:"path"`
	Enabled              bool    `json:"enabled"`
	Status               string  `json:"status"`
	LastIndexedEventID   int64   `json:"last_indexed_event_id"`
	CanonicalMaxEventID  int64   `json:"canonical_max_event_id"`
	StaleEventCount      int64   `json:"stale_event_count"`
	DenseModelID         string  `json:"dense_model_id,omitempty"`
	DenseDimension       int     `json:"dense_dimension,omitempty"`
	DenseVectorCount     int64   `json:"dense_vector_count,omitempty"`
	LastError            *string `json:"last_error,omitempty"`
	SearchableChunkCount int64   `json:"searchable_chunk_count"`
}

func DefaultPath(canonicalDBPath string) string {
	return canonicalDBPath + ".search.sqlite"
}

func Open(path string) (*DB, error) {
	sqlitevec.Auto()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("failed to create search database directory: %w", err)
	}

	database, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open search database: %w", err)
	}

	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
	}
	for _, pragma := range pragmas {
		if _, err := database.Exec(pragma); err != nil {
			_ = database.Close()
			return nil, fmt.Errorf("failed to apply search pragma %q: %w", pragma, err)
		}
	}

	db := &DB{DB: database, path: path}
	if err := db.Migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Path() string {
	return db.path
}

func (db *DB) Migrate() error {
	stateSchema := `
CREATE TABLE IF NOT EXISTS search_index_state (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`
	if _, err := db.Exec(stateSchema); err != nil {
		return fmt.Errorf("failed to create search state table: %w", err)
	}
	storedVersion, _, _ := db.State("schema_version")
	if storedVersion != "" && storedVersion != SchemaVersion {
		if err := db.dropChunkTables(); err != nil {
			return err
		}
		if err := db.SetState("last_indexed_event_id", "0"); err != nil {
			return err
		}
		if err := db.SetState("last_successful_indexed_event_id", "0"); err != nil {
			return err
		}
	}

	schema := `
CREATE TABLE IF NOT EXISTS search_chunks (
  ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
  chunk_id TEXT NOT NULL UNIQUE,
  resource_type TEXT NOT NULL CHECK (resource_type IN ('task','comment','handoff')),
  resource_uuid TEXT NOT NULL,
  resource_id TEXT NOT NULL,
  task_uuid TEXT,
  comment_uuid TEXT,
  task_id TEXT,
  comment_id TEXT,
  scope_ref TEXT,
  status TEXT,
  path TEXT NOT NULL,
  state TEXT,
  kind TEXT,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  labels TEXT,
  content_hash TEXT NOT NULL,
  source_etag INTEGER NOT NULL,
  source_updated_at TEXT NOT NULL,
  indexed_event_id INTEGER NOT NULL,
  indexed_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS search_chunks_resource_idx ON search_chunks(resource_type, resource_uuid);
CREATE INDEX IF NOT EXISTS search_chunks_task_idx ON search_chunks(task_uuid);
CREATE INDEX IF NOT EXISTS search_chunks_path_idx ON search_chunks(path);
CREATE INDEX IF NOT EXISTS search_chunks_scope_status_idx ON search_chunks(scope_ref, status);
CREATE INDEX IF NOT EXISTS search_chunks_state_kind_idx ON search_chunks(state, kind);
CREATE INDEX IF NOT EXISTS search_chunks_hash_idx ON search_chunks(content_hash);

CREATE TABLE IF NOT EXISTS search_fts_plain (
  chunk_id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  body TEXT NOT NULL,
  path TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS search_failures (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  event_id INTEGER,
  task_uuid TEXT,
  stage TEXT NOT NULL,
  message TEXT NOT NULL,
  created_at TEXT NOT NULL
);
`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("failed to migrate search database: %w", err)
	}
	_, ftsErr := db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS search_fts USING fts5(
		  chunk_id UNINDEXED,
		  title,
		  body,
		  path
		)
	`)
	var ftsTableCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='search_fts'`).Scan(&ftsTableCount)
	if ftsTableCount > 0 {
		_ = db.SetState("fts_available", "true")
	} else {
		_ = db.SetState("fts_available", "false")
	}
	_ = ftsErr
	if err := db.SetState("schema_version", SchemaVersion); err != nil {
		return err
	}
	if _, err := db.Exec(`INSERT OR IGNORE INTO search_index_state (key, value) VALUES ('status', 'ready')`); err != nil {
		return fmt.Errorf("failed to initialize search state: %w", err)
	}
	return nil
}

func (db *DB) dropChunkTables() error {
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS search_fts`,
		`DROP TABLE IF EXISTS search_fts_plain`,
		`DROP TABLE IF EXISTS search_dense_vec`,
		`DROP TABLE IF EXISTS search_chunks`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to drop legacy search table: %w", err)
		}
	}
	if err := db.SetState("dense_dimension", "0"); err != nil {
		return err
	}
	if err := db.SetState("fts_available", "false"); err != nil {
		return err
	}
	return nil
}

func (db *DB) EnsureDenseTable(dimension int, modelID string) error {
	if dimension <= 0 {
		return nil
	}
	current, _ := db.IntState("dense_dimension")
	if current > 0 && current != dimension {
		if err := db.DropDenseTable(); err != nil {
			return err
		}
	}

	query := fmt.Sprintf(`CREATE VIRTUAL TABLE IF NOT EXISTS search_dense_vec USING vec0(embedding float[%d] distance_metric=cosine)`, dimension)
	if _, err := db.Exec(query); err != nil {
		return fmt.Errorf("failed to create sqlite-vec dense table: %w", err)
	}
	if err := db.SetState("dense_dimension", strconv.Itoa(dimension)); err != nil {
		return err
	}
	if modelID != "" {
		if err := db.SetState("dense_model_id", modelID); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) DropDenseTable() error {
	if _, err := db.Exec(`DROP TABLE IF EXISTS search_dense_vec`); err != nil {
		return fmt.Errorf("failed to drop dense vector table: %w", err)
	}
	if err := db.SetState("dense_dimension", "0"); err != nil {
		return err
	}
	return nil
}

func (db *DB) HasDenseTable() bool {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='search_dense_vec'`).Scan(&count)
	return err == nil && count > 0
}

func (db *DB) FTSAvailable() bool {
	value, ok, err := db.State("fts_available")
	return err == nil && ok && value == "true"
}

func (db *DB) State(key string) (string, bool, error) {
	var value string
	err := db.QueryRow(`SELECT value FROM search_index_state WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (db *DB) IntState(key string) (int, error) {
	value, ok, err := db.State(key)
	if err != nil || !ok || value == "" {
		return 0, err
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}

func (db *DB) Int64State(key string) (int64, error) {
	value, ok, err := db.State(key)
	if err != nil || !ok || value == "" {
		return 0, err
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}

func (db *DB) SetState(key, value string) error {
	_, err := db.Exec(`
		INSERT INTO search_index_state (key, value)
		VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	if err != nil {
		return fmt.Errorf("failed to set search state %s: %w", key, err)
	}
	return nil
}

func (db *DB) RecordFailure(eventID int64, taskUUID, stage string, err error) {
	if err == nil {
		return
	}
	_, _ = db.Exec(`
		INSERT INTO search_failures (event_id, task_uuid, stage, message, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, nullableInt64(eventID), nullableString(taskUUID), stage, err.Error(), time.Now().UTC().Format(time.RFC3339))
	_ = db.SetState("last_error", err.Error())
	_ = db.SetState("status", "error")
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullableInt64(v int64) interface{} {
	if v == 0 {
		return nil
	}
	return v
}
