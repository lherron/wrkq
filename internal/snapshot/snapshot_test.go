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

	// Create minimal principal-only schema for testing. There is no actors
	// table: write attribution is carried solely by *_principal_ref columns.
	schema := `
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
			created_by_principal_ref TEXT,
			updated_by_principal_ref TEXT
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
			workflow_preset TEXT,
			preset_version INTEGER,
			phase TEXT,
			risk_class TEXT,
			state TEXT NOT NULL CHECK (state IN ('idea','draft','open','in_progress','completed','archived','blocked','cancelled','deleted')),
			priority INTEGER NOT NULL DEFAULT 3,
			start_at TEXT,
			due_at TEXT,
			labels TEXT,
			description TEXT NOT NULL DEFAULT '',
			specification TEXT NOT NULL DEFAULT '',
			etag INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
			updated_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
			completed_at TEXT,
			archived_at TEXT,
			created_by_principal_ref TEXT,
			updated_by_principal_ref TEXT
		);

		CREATE TABLE comments (
			uuid TEXT PRIMARY KEY,
			id TEXT NOT NULL UNIQUE,
			task_uuid TEXT NOT NULL REFERENCES tasks(uuid) ON DELETE CASCADE,
			created_by_principal_ref TEXT,
			body TEXT NOT NULL,
			meta TEXT,
			etag INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT,
			deleted_at TEXT,
			deleted_by_principal_ref TEXT
		);

		CREATE TABLE event_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
			principal_ref TEXT,
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

	// Insert container
	_, err := db.Exec(`
		INSERT INTO containers (uuid, id, slug, title, etag, created_at, updated_at, created_by_principal_ref, updated_by_principal_ref)
		VALUES ('container-uuid-1', 'P-00001', 'test-project', 'Test Project', 1, '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z', 'agent:test-actor', 'agent:test-actor')
	`)
	if err != nil {
		t.Fatalf("failed to insert container: %v", err)
	}

	// Insert task
	_, err = db.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, workflow_preset, preset_version, phase, risk_class, state, priority, labels, description, etag, created_at, updated_at, created_by_principal_ref, updated_by_principal_ref)
		VALUES ('task-uuid-1', 'T-00001', 'test-task', 'Test Task', 'container-uuid-1', 'code_defect_fastlane', 1, 'open', 'medium', 'open', 2, '["label-b","label-a"]', 'Test description', 1, '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z', 'agent:test-actor', 'agent:test-actor')
	`)
	if err != nil {
		t.Fatalf("failed to insert task: %v", err)
	}

	// Insert comment
	_, err = db.Exec(`
		INSERT INTO comments (uuid, id, task_uuid, created_by_principal_ref, body, etag, created_at)
		VALUES ('comment-uuid-1', 'C-00001', 'task-uuid-1', 'agent:test-actor', 'Test comment', 1, '2025-01-01T00:00:00Z')
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
		Containers: map[string]ContainerEntry{
			"uuid-2": {ID: "P-00002", Slug: "proj-b", Title: "B", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z", CreatedByPrincipalRef: "agent:a", UpdatedByPrincipalRef: "agent:a"},
			"uuid-1": {ID: "P-00001", Slug: "proj-a", Title: "A", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z", CreatedByPrincipalRef: "agent:a", UpdatedByPrincipalRef: "agent:a"},
		},
		Tasks:    map[string]TaskEntry{},
		Comments: map[string]CommentEntry{},
		Links:    map[string]LinkEntry{},
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

	// Verify key ordering (containers should be sorted by UUID)
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
	defer func() { _ = db.Close() }()
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
	if len(snap.Containers) != 1 {
		t.Errorf("expected 1 container in snapshot, got %d", len(snap.Containers))
	}
	if len(snap.Tasks) != 1 {
		t.Errorf("expected 1 task in snapshot, got %d", len(snap.Tasks))
	}

	// Principal attribution is preserved; no actor scaffolding leaks.
	task := snap.Tasks["task-uuid-1"]
	if task.CreatedByPrincipalRef != "agent:test-actor" {
		t.Errorf("expected created_by_principal_ref agent:test-actor, got %q", task.CreatedByPrincipalRef)
	}
	if strings.Contains(string(data), "\"actors\"") || strings.Contains(string(data), "actor_uuid") {
		t.Errorf("snapshot must not contain actor scaffolding: %s", string(data))
	}
}

func TestExportWithLabelsSorted(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()
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
	defer func() { _ = db.Close() }()
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
	defer func() { _ = db.Close() }()
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
	if result.ContainerCount != 1 {
		t.Errorf("expected 1 container, got %d", result.ContainerCount)
	}
}

// TestImportRejectsLegacyActorSnapshot proves that an actor-bearing snapshot is
// hard-gated rather than lossily imported. wrkq is principal-only.
func TestImportRejectsLegacyActorSnapshot(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()

	legacy := []string{
		// Top-level actors map.
		`{"meta":{"schema_version":1,"machine_interface_version":1},"actors":{"u1":{"id":"A-00001","slug":"x","role":"human","created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}}}`,
		// Comment carrying actor_uuid.
		`{"meta":{"schema_version":1,"machine_interface_version":1},"comments":{"c1":{"id":"C-00001","task_uuid":"t1","actor_uuid":"u1","body":"x","etag":1,"created_at":"2025-01-01T00:00:00Z"}}}`,
		// Container carrying legacy bare created_by.
		`{"meta":{"schema_version":1,"machine_interface_version":1},"containers":{"k1":{"id":"P-00001","slug":"p","created_by":"u1","updated_by":"u1","etag":1,"created_at":"2025-01-01T00:00:00Z","updated_at":"2025-01-01T00:00:00Z"}}}`,
	}

	for i, body := range legacy {
		tmpDir := t.TempDir()
		path := filepath.Join(tmpDir, "state.json")
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatalf("case %d: write: %v", i, err)
		}
		_, err := Import(db, ImportOptions{InputPath: path})
		if err == nil {
			t.Errorf("case %d: expected legacy actor snapshot to be rejected, got nil error", i)
			continue
		}
		if !strings.Contains(err.Error(), "principal-only") {
			t.Errorf("case %d: expected principal-only rejection, got: %v", i, err)
		}
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
				Containers: map[string]ContainerEntry{
					"container-1": {ID: "P-00001", Slug: "proj", Title: "Project", CreatedByPrincipalRef: "agent:a", UpdatedByPrincipalRef: "agent:a", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
				},
				Tasks: map[string]TaskEntry{
					"task-1": {ID: "T-00001", Slug: "task", Title: "Task", ProjectUUID: "container-1", State: "open", Priority: 2, CreatedByPrincipalRef: "agent:a", UpdatedByPrincipalRef: "agent:a", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
				},
				Comments: map[string]CommentEntry{
					"comment-1": {ID: "C-00001", TaskUUID: "task-1", CreatedByPrincipalRef: "agent:a", Body: "test", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z"},
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
				Containers: map[string]ContainerEntry{},
				Tasks:      map[string]TaskEntry{},
				Comments: map[string]CommentEntry{
					"comment-1": {TaskUUID: "unknown-task", CreatedByPrincipalRef: "agent:a"},
				},
			},
			wantErr: true,
		},
		{
			name: "container references unknown parent",
			snap: &Snapshot{
				Meta: Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
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
