package snapshot

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/attribution"
	wrkqdb "github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/store"
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
			campaign_uuid TEXT REFERENCES containers(uuid),
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
			task_uuid TEXT REFERENCES tasks(uuid) ON DELETE CASCADE,
			container_uuid TEXT REFERENCES containers(uuid) ON DELETE CASCADE,
			created_by_principal_ref TEXT,
			body TEXT NOT NULL,
			meta TEXT,
			etag INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT,
			deleted_at TEXT,
			deleted_by_principal_ref TEXT,
			CHECK (
				(task_uuid IS NOT NULL AND container_uuid IS NULL) OR
				(task_uuid IS NULL AND container_uuid IS NOT NULL)
			)
		);

		CREATE TABLE promises (
			uuid TEXT PRIMARY KEY,
			id TEXT NOT NULL UNIQUE,
			owner_principal_ref TEXT NOT NULL,
			subject TEXT NOT NULL,
			review_question TEXT,
			subject_task_uuid TEXT REFERENCES tasks(uuid) ON DELETE SET NULL,
			subject_container_uuid TEXT REFERENCES containers(uuid) ON DELETE SET NULL,
			review_at TEXT NOT NULL,
			state TEXT NOT NULL,
			closed_at TEXT,
			last_reviewed_at TEXT,
			last_review_note TEXT,
			meta TEXT,
			etag INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			created_by_principal_ref TEXT NOT NULL,
			created_by_scope_ref TEXT,
			updated_by_principal_ref TEXT NOT NULL,
			updated_by_scope_ref TEXT
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

		CREATE TABLE actors (id TEXT);
		CREATE TABLE attachments (id TEXT);
		CREATE TABLE evidence_items (id TEXT);
		CREATE TABLE rooms (id TEXT);
		CREATE TABLE envelopes (id TEXT);
		CREATE TABLE task_transitions (id TEXT);
		CREATE TABLE comment_sequences (name TEXT PRIMARY KEY, value INTEGER NOT NULL);
		INSERT INTO comment_sequences (name, value) VALUES ('next_comment', 0);
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

func TestExportContainerComment(t *testing.T) {
	db := createTestDB(t)
	defer func() { _ = db.Close() }()
	seedTestData(t, db)

	_, err := db.Exec(`
		INSERT INTO comments (uuid, id, container_uuid, created_by_principal_ref, body, etag, created_at)
		VALUES ('container-comment-uuid', 'C-00002', 'container-uuid-1', 'agent:test-actor', 'Container comment', 1, '2025-01-02T00:00:00Z')
	`)
	if err != nil {
		t.Fatalf("failed to insert container comment: %v", err)
	}

	outputPath := filepath.Join(t.TempDir(), "state.json")
	_, err = Export(db, ExportOptions{
		OutputPath: outputPath,
		Canonical:  true,
	})
	if err != nil {
		t.Fatalf("failed to export container comment: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read container-comment snapshot: %v", err)
	}
	var exported Snapshot
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatalf("failed to decode container-comment snapshot: %v", err)
	}
	comment := exported.Comments["container-comment-uuid"]
	if comment.TaskUUID != "" || comment.ContainerUUID != "container-uuid-1" {
		t.Fatalf("exported comment subject = task %q, container %q", comment.TaskUUID, comment.ContainerUUID)
	}

	target := createTestDB(t)
	defer func() { _ = target.Close() }()
	if _, err := Import(target, ImportOptions{InputPath: outputPath}); err != nil {
		t.Fatalf("failed to import container-comment snapshot: %v", err)
	}
	var importedTask sql.NullString
	var importedContainer string
	if err := target.QueryRow(`
		SELECT task_uuid, container_uuid FROM comments WHERE uuid = 'container-comment-uuid'
	`).Scan(&importedTask, &importedContainer); err != nil {
		t.Fatalf("failed to read imported container comment: %v", err)
	}
	if importedTask.Valid || importedContainer != "container-uuid-1" {
		t.Fatalf("imported comment subject = task %#v, container %q", importedTask, importedContainer)
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

func TestPromiseExportImportRoundTripPreservesLastReviewFields(t *testing.T) {
	openMigrated := func(name string) *wrkqdb.DB {
		t.Helper()
		database, err := wrkqdb.Open(filepath.Join(t.TempDir(), name+".db"))
		if err != nil {
			t.Fatalf("open %s database: %v", name, err)
		}
		if err := database.Migrate(); err != nil {
			t.Fatalf("migrate %s database: %v", name, err)
		}
		t.Cleanup(func() { _ = database.Close() })
		return database
	}

	source := openMigrated("source")
	attr := attribution.Attribution{PrincipalRef: "agent:cody", ScopeRef: "agent:cody:project:wrkq:task:T-07489"}
	s := store.New(source)
	var containerUUID string
	if err := source.QueryRow("SELECT uuid FROM containers WHERE kind = 'root'").Scan(&containerUUID); err != nil {
		t.Fatalf("find root container: %v", err)
	}
	question := "What changed?"
	promise, err := s.Promises.CreateWithAttribution(attr, store.PromiseCreateParams{
		OwnerPrincipalRef: attr.PrincipalRef, Subject: "Review snapshot behavior",
		ReviewQuestion: &question, SubjectContainerUUID: &containerUUID,
		ReviewAt: "2000-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("create promise: %v", err)
	}
	note := "The snapshot must remember this review"
	promise, err = s.Promises.RenewWithAttribution(attr, promise.UUID, store.PromiseReviewParams{
		ReviewAt: "2099-01-01T00:00:00Z", Note: &note,
	}, promise.ETag)
	if err != nil {
		t.Fatalf("renew promise: %v", err)
	}

	outputPath := filepath.Join(t.TempDir(), "promises.json")
	result, err := Export(source.DB, ExportOptions{OutputPath: outputPath, Canonical: true})
	if err != nil {
		t.Fatalf("export promises: %v", err)
	}
	if result.PromiseCount != 1 {
		t.Fatalf("export promise count = %d, want 1", result.PromiseCount)
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if strings.Index(string(data), `"promises"`) < strings.Index(string(data), `"tasks"`) {
		t.Fatalf("canonical top-level order places promises before tasks: %s", data)
	}

	target := createTestDB(t)
	defer func() { _ = target.Close() }()
	imported, err := Import(target, ImportOptions{InputPath: outputPath})
	if err != nil {
		t.Fatalf("import promises: %v", err)
	}
	if imported.PromiseCount != 1 {
		t.Fatalf("import promise count = %d, want 1", imported.PromiseCount)
	}
	var reloadedID, reloadedContainer, reloadedReviewedAt, reloadedNote, reloadedReviewAt string
	var reloadedETag int64
	if err := target.QueryRow(`
		SELECT id, subject_container_uuid, last_reviewed_at, last_review_note, review_at, etag
		  FROM promises WHERE uuid = ?
	`, promise.UUID).Scan(&reloadedID, &reloadedContainer, &reloadedReviewedAt, &reloadedNote, &reloadedReviewAt, &reloadedETag); err != nil {
		t.Fatalf("read imported promise: %v", err)
	}
	if reloadedID != promise.ID || reloadedContainer != containerUUID ||
		reloadedReviewedAt != *promise.LastReviewedAt || reloadedNote != note ||
		reloadedReviewAt != promise.ReviewAt || reloadedETag != promise.ETag {
		t.Fatalf("imported promise mismatch: id=%s container=%s reviewed=%s note=%s review_at=%s etag=%d",
			reloadedID, reloadedContainer, reloadedReviewedAt, reloadedNote, reloadedReviewAt, reloadedETag)
	}
	var importedEvents int
	if err := target.QueryRow("SELECT COUNT(*) FROM event_log WHERE resource_type = 'promise'").Scan(&importedEvents); err != nil {
		t.Fatalf("count imported promise events: %v", err)
	}
	if importedEvents != 0 {
		t.Fatalf("snapshot import replayed %d promise events, want 0", importedEvents)
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

// TestSnapshotPreservesCampaignEnrollment pins that cross-project campaign
// ENROLMENT survives a snapshot round-trip. Before T-07701 the task tuple
// carried no campaign column at all, so a restore silently unenrolled every
// enrolled member and the campaign lost its cross-project slots.
func TestSnapshotPreservesCampaignEnrollment(t *testing.T) {
	openMigrated := func(name string) *wrkqdb.DB {
		t.Helper()
		database, err := wrkqdb.Open(filepath.Join(t.TempDir(), name+".db"))
		if err != nil {
			t.Fatalf("open %s database: %v", name, err)
		}
		if err := database.Migrate(); err != nil {
			t.Fatalf("migrate %s database: %v", name, err)
		}
		t.Cleanup(func() { _ = database.Close() })
		return database
	}

	source := openMigrated("source")
	var rootUUID string
	if err := source.QueryRow("SELECT uuid FROM containers WHERE kind = 'root'").Scan(&rootUUID); err != nil {
		t.Fatalf("find root container: %v", err)
	}
	attr := attribution.Attribution{PrincipalRef: "agent:clod", ScopeRef: "agent:clod:project:wrkq:task:T-07701"}
	s := store.New(source)

	project, err := s.Containers.CreateWithAttribution(attr, store.ContainerCreateParams{
		Slug: "alpha", Title: "alpha", ParentUUID: &rootUUID, Kind: "project",
	})
	if err != nil {
		t.Fatalf("create project container: %v", err)
	}
	campaign, err := s.Containers.CreateWithAttribution(attr, store.ContainerCreateParams{
		Slug: "camp", Title: "camp", ParentUUID: &project.UUID,
	})
	if err != nil {
		t.Fatalf("create campaign container: %v", err)
	}
	if _, err := source.Exec(
		"UPDATE containers SET campaign_state = 'active' WHERE uuid = ?", campaign.UUID,
	); err != nil {
		t.Fatalf("adorn campaign: %v", err)
	}
	created, err := s.Tasks.CreateWithAttribution(attr, store.CreateParams{
		Slug: "enrolled-slot", Title: "enrolled slot", ProjectUUID: project.UUID,
		State: domain.StateOpen, Priority: 3, CampaignUUID: &campaign.UUID,
	})
	if err != nil {
		t.Fatalf("create enrolled task: %v", err)
	}

	outputPath := filepath.Join(t.TempDir(), "state.json")
	if _, err := Export(source.DB, ExportOptions{OutputPath: outputPath, Canonical: true}); err != nil {
		t.Fatalf("export: %v", err)
	}

	// The export must CARRY the enrolment, not just re-derive it on read.
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !strings.Contains(string(data), `"campaign_uuid": "`+campaign.UUID+`"`) &&
		!strings.Contains(string(data), `"campaign_uuid":"`+campaign.UUID+`"`) {
		t.Fatalf("exported snapshot does not carry campaign_uuid %s: %s", campaign.UUID, data)
	}

	// Import into the plain-schema fixture DB: the migrated-target path cannot
	// round-trip containers at all (T-07498), so this proves the task tuple.
	target := createTestDB(t)
	defer func() { _ = target.Close() }()
	if _, err := Import(target, ImportOptions{InputPath: outputPath}); err != nil {
		t.Fatalf("import: %v", err)
	}
	var restored sql.NullString
	if err := target.QueryRow(
		"SELECT campaign_uuid FROM tasks WHERE uuid = ?", created.UUID,
	).Scan(&restored); err != nil {
		t.Fatalf("read restored task: %v", err)
	}
	if !restored.Valid || restored.String != campaign.UUID {
		t.Fatalf("restored campaign_uuid = %v, want %s", restored, campaign.UUID)
	}
}

func TestSnapshotIncludesProjectEventsWithEventExport(t *testing.T) {
	database, err := wrkqdb.Open(filepath.Join(t.TempDir(), "project-events.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO project_events (
		fid, project_uuid, container_uuid, type, source, principal_ref, summary,
		payload, occurred_at
	) VALUES ('PE-00042', 'project-stamp', 'container-stamp', 'smoke.posted',
		'smoke', 'agent:mable', 'smoke', '{"exact":true}', '2026-09-04T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	snap, _, err := ExportToSnapshot(database.DB, ExportOptions{IncludeEvents: true, Canonical: true})
	if err != nil {
		t.Fatal(err)
	}
	event, ok := snap.ProjectEvents["PE-00042"]
	if !ok || event.Payload == nil || *event.Payload != `{"exact":true}` || event.ProjectUUID != "project-stamp" {
		t.Fatalf("project event snapshot = %#v", event)
	}
}
