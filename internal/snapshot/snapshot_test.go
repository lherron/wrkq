package snapshot

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// createTestDB creates an in-memory SQLite database with the wrkq schema
func createTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("failed to enable foreign keys: %v", err)
	}

	// Create minimal schema for testing
	schema := `
		CREATE TABLE actors (
			uuid TEXT PRIMARY KEY,
			id TEXT UNIQUE,
			slug TEXT NOT NULL UNIQUE,
			display_name TEXT,
			role TEXT NOT NULL CHECK (role IN ('human','agent','system')),
			meta TEXT,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		);

		CREATE TABLE containers (
			uuid TEXT PRIMARY KEY,
			id TEXT UNIQUE,
			slug TEXT NOT NULL,
			title TEXT NOT NULL,
			parent_uuid TEXT REFERENCES containers(uuid) ON DELETE CASCADE,
			etag INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
			archived_at TEXT,
			created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid),
			updated_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid)
		);

		CREATE TABLE tasks (
			uuid TEXT PRIMARY KEY,
			id TEXT UNIQUE,
			slug TEXT NOT NULL,
			title TEXT NOT NULL,
			project_uuid TEXT NOT NULL REFERENCES containers(uuid),
			requested_by_project_id TEXT,
			assigned_project_id TEXT,
			acknowledged_at TEXT,
			resolution TEXT,
			state TEXT NOT NULL CHECK (state IN ('idea','draft','open','in_progress','completed','archived','blocked','cancelled','deleted')),
			priority INTEGER NOT NULL DEFAULT 3,
			start_at TEXT,
			due_at TEXT,
			labels TEXT,
			description TEXT NOT NULL DEFAULT '',
			etag INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
			completed_at TEXT,
			archived_at TEXT,
			created_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid),
			updated_by_actor_uuid TEXT NOT NULL REFERENCES actors(uuid)
		);

		CREATE TABLE comments (
			uuid TEXT PRIMARY KEY,
			id TEXT NOT NULL UNIQUE,
			task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
			actor_uuid TEXT NOT NULL REFERENCES actors(uuid),
			body TEXT NOT NULL,
			meta TEXT,
			etag INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT,
			deleted_at TEXT,
			deleted_by_actor_uuid TEXT REFERENCES actors(uuid)
		);

		CREATE TABLE event_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
			actor_uuid TEXT,
			resource_type TEXT,
			resource_uuid TEXT,
			event_type TEXT NOT NULL,
			etag INTEGER,
			payload TEXT
		);
	`

	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	return db
}

// seedTestData inserts test data into the database
func seedTestData(t *testing.T, db *sql.DB) {
	t.Helper()

	// Insert actor
	_, err := db.Exec(`
		INSERT INTO actors (uuid, id, slug, display_name, role, created_at, updated_at)
		VALUES ('actor-uuid-1', 'A-00001', 'test-actor', 'Test Actor', 'human', '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z')
	`)
	if err != nil {
		t.Fatalf("failed to insert actor: %v", err)
	}

	// Insert container
	_, err = db.Exec(`
		INSERT INTO containers (uuid, id, slug, title, etag, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('container-uuid-1', 'P-00001', 'test-project', 'Test Project', 1, '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z', 'actor-uuid-1', 'actor-uuid-1')
	`)
	if err != nil {
		t.Fatalf("failed to insert container: %v", err)
	}

	// Insert task
	_, err = db.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, labels, description, etag, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('task-uuid-1', 'T-00001', 'test-task', 'Test Task', 'container-uuid-1', 'open', 2, '["label-b","label-a"]', 'Test description', 1, '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z', 'actor-uuid-1', 'actor-uuid-1')
	`)
	if err != nil {
		t.Fatalf("failed to insert task: %v", err)
	}

	// Insert comment
	_, err = db.Exec(`
		INSERT INTO comments (uuid, id, task_uuid, actor_uuid, body, etag, created_at)
		VALUES ('comment-uuid-1', 'C-00001', 'task-uuid-1', 'actor-uuid-1', 'Test comment', 1, '2025-01-01T00:00:00Z')
	`)
	if err != nil {
		t.Fatalf("failed to insert comment: %v", err)
	}
}

func TestCanonicalJSON(t *testing.T) {
	snap := &Snapshot{
		Meta: Meta{
			SchemaVersion:           1,
			MachineInterfaceVersion: 1,
		},
		Actors: map[string]ActorEntry{
			"uuid-2": {ID: "A-00002", Slug: "actor-b", Role: "agent", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
			"uuid-1": {ID: "A-00001", Slug: "actor-a", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]ContainerEntry{},
		Tasks:      map[string]TaskEntry{},
		Comments:   map[string]CommentEntry{},
		Links:      map[string]LinkEntry{},
	}

	// Generate canonical JSON
	data1, err := CanonicalJSON(snap)
	if err != nil {
		t.Fatalf("failed to generate canonical JSON: %v", err)
	}

	// Generate again - should be identical
	data2, err := CanonicalJSON(snap)
	if err != nil {
		t.Fatalf("failed to generate canonical JSON second time: %v", err)
	}

	if string(data1) != string(data2) {
		t.Errorf("canonical JSON is not deterministic:\n%s\nvs\n%s", string(data1), string(data2))
	}

	// Verify key ordering (actors should be sorted by UUID)
	str := string(data1)
	uuid1Pos := strings.Index(str, "uuid-1")
	uuid2Pos := strings.Index(str, "uuid-2")

	if uuid1Pos > uuid2Pos {
		t.Errorf("UUIDs not sorted lexicographically: uuid-1 at %d, uuid-2 at %d", uuid1Pos, uuid2Pos)
	}

	// Verify no insignificant whitespace (no newlines)
	if strings.Contains(str, "\n") {
		t.Error("canonical JSON contains newlines")
	}
}

func TestComputeSnapshotRev(t *testing.T) {
	data := []byte(`{"test":"data"}`)

	rev := ComputeSnapshotRev(data)

	if !strings.HasPrefix(rev, "sha256:") {
		t.Errorf("snapshot_rev should start with 'sha256:', got: %s", rev)
	}

	// Same data should produce same rev
	rev2 := ComputeSnapshotRev(data)
	if rev != rev2 {
		t.Errorf("same data should produce same rev: %s vs %s", rev, rev2)
	}

	// Different data should produce different rev
	rev3 := ComputeSnapshotRev([]byte(`{"test":"other"}`))
	if rev == rev3 {
		t.Error("different data should produce different rev")
	}
}

func TestExport(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()
	seedTestData(t, db)

	// Create temp directory for output
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "state.json")

	opts := ExportOptions{
		OutputPath:    outputPath,
		Canonical:     true,
		IncludeEvents: false,
	}

	result, err := Export(db, opts)
	if err != nil {
		t.Fatalf("failed to export: %v", err)
	}

	// Check result
	if result.OutputPath != outputPath {
		t.Errorf("wrong output path: %s", result.OutputPath)
	}
	if result.ActorCount != 1 {
		t.Errorf("expected 1 actor, got %d", result.ActorCount)
	}
	if result.ContainerCount != 1 {
		t.Errorf("expected 1 container, got %d", result.ContainerCount)
	}
	if result.TaskCount != 1 {
		t.Errorf("expected 1 task, got %d", result.TaskCount)
	}
	if result.CommentCount != 1 {
		t.Errorf("expected 1 comment, got %d", result.CommentCount)
	}
	if !strings.HasPrefix(result.SnapshotRev, "sha256:") {
		t.Errorf("expected sha256 prefix, got: %s", result.SnapshotRev)
	}

	// Verify file exists and is valid JSON
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	// Verify content
	if len(snap.Actors) != 1 {
		t.Errorf("expected 1 actor in snapshot, got %d", len(snap.Actors))
	}
	if len(snap.Tasks) != 1 {
		t.Errorf("expected 1 task in snapshot, got %d", len(snap.Tasks))
	}
}

func TestExportWithLabelsSorted(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()
	seedTestData(t, db)

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "state.json")

	opts := ExportOptions{
		OutputPath: outputPath,
		Canonical:  true,
	}

	_, err := Export(db, opts)
	if err != nil {
		t.Fatalf("failed to export: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	// Check that labels are sorted (inserted as ["label-b","label-a"], should be ["label-a","label-b"])
	task := snap.Tasks["task-uuid-1"]
	if len(task.Labels) != 2 {
		t.Fatalf("expected 2 labels, got %d", len(task.Labels))
	}
	if task.Labels[0] != "label-a" || task.Labels[1] != "label-b" {
		t.Errorf("labels not sorted: %v", task.Labels)
	}
}

func TestRoundTrip(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()
	seedTestData(t, db)

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "state.json")

	// Export
	opts := ExportOptions{
		OutputPath: outputPath,
		Canonical:  true,
	}

	result, err := Export(db, opts)
	if err != nil {
		t.Fatalf("failed to export: %v", err)
	}

	// Verify
	verifyResult, err := Verify(db, outputPath)
	if err != nil {
		t.Fatalf("failed to verify: %v", err)
	}

	if !verifyResult.Valid {
		t.Errorf("round-trip verification failed: %s", verifyResult.Message)
	}

	if verifyResult.SnapshotRev != result.SnapshotRev {
		t.Errorf("snapshot_rev mismatch: %s vs %s", verifyResult.SnapshotRev, result.SnapshotRev)
	}
}

func TestImportDryRun(t *testing.T) {
	db := createTestDB(t)
	defer db.Close()
	seedTestData(t, db)

	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "state.json")

	// Export current state
	exportOpts := ExportOptions{
		OutputPath: outputPath,
		Canonical:  true,
	}

	_, err := Export(db, exportOpts)
	if err != nil {
		t.Fatalf("failed to export: %v", err)
	}

	// Import with dry run
	importOpts := ImportOptions{
		InputPath: outputPath,
		DryRun:    true,
	}

	result, err := Import(db, importOpts)
	if err != nil {
		t.Fatalf("failed to import dry run: %v", err)
	}

	if !result.DryRun {
		t.Error("dry run flag not set in result")
	}
	if result.ActorCount != 1 {
		t.Errorf("expected 1 actor, got %d", result.ActorCount)
	}
}

func TestValidateSnapshot(t *testing.T) {
	tests := []struct {
		name    string
		snap    *Snapshot
		wantErr bool
	}{
		{
			name: "valid snapshot",
			snap: &Snapshot{
				Meta: Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
				Actors: map[string]ActorEntry{
					"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
				},
				Containers: map[string]ContainerEntry{
					"container-1": {ID: "P-00001", Slug: "proj", Title: "Project", CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
				},
				Tasks: map[string]TaskEntry{
					"task-1": {ID: "T-00001", Slug: "task", Title: "Task", ProjectUUID: "container-1", State: "open", Priority: 2, CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
				},
				Comments: map[string]CommentEntry{
					"comment-1": {ID: "C-00001", TaskUUID: "task-1", ActorUUID: "actor-1", Body: "test", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z"},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid schema version",
			snap: &Snapshot{
				Meta: Meta{SchemaVersion: 0, MachineInterfaceVersion: 1},
			},
			wantErr: true,
		},
		{
			name: "task references unknown container",
			snap: &Snapshot{
				Meta:       Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
				Actors:     map[string]ActorEntry{},
				Containers: map[string]ContainerEntry{},
				Tasks: map[string]TaskEntry{
					"task-1": {ID: "T-00001", ProjectUUID: "unknown-container"},
				},
			},
			wantErr: true,
		},
		{
			name: "comment references unknown task",
			snap: &Snapshot{
				Meta:       Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
				Actors:     map[string]ActorEntry{"actor-1": {}},
				Containers: map[string]ContainerEntry{},
				Tasks:      map[string]TaskEntry{},
				Comments: map[string]CommentEntry{
					"comment-1": {TaskUUID: "unknown-task", ActorUUID: "actor-1"},
				},
			},
			wantErr: true,
		},
		{
			name: "container references unknown parent",
			snap: &Snapshot{
				Meta:   Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
				Actors: map[string]ActorEntry{},
				Containers: map[string]ContainerEntry{
					"container-1": {ID: "P-00001", ParentUUID: "unknown-parent"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSnapshot(tt.snap)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSnapshot() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFormatTimestamp(t *testing.T) {
	ts, _ := ParseTimestamp("2025-01-15T10:30:00Z")
	formatted := FormatTimestamp(ts)

	if formatted != "2025-01-15T10:30:00Z" {
		t.Errorf("expected 2025-01-15T10:30:00Z, got %s", formatted)
	}
}
