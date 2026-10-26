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

// createTestDB opens a freshly migrated wrkq database: fixtures run against
// the production schema and its triggers, never a hand-written double.
func createTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := wrkqdb.Open(filepath.Join(t.TempDir(), "wrkq.db"))
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database.DB
}

// seedTestData inserts one project under the root, one task and one comment.
func seedTestData(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `
		INSERT INTO containers (uuid, id, slug, title, kind, parent_uuid, etag, created_at, updated_at, created_by_principal_ref, updated_by_principal_ref)
		VALUES ('container-uuid-1', 'P-00001', 'test-project', 'Test Project', 'project', ?, 1, '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z', 'agent:test-actor', 'agent:test-actor')
	`, domain.RootContainerUUID)
	mustExec(t, db, `
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, workflow_preset, preset_version, phase, risk_class, state, priority, labels, description, etag, created_at, updated_at, created_by_principal_ref, updated_by_principal_ref)
		VALUES ('task-uuid-1', 'T-00001', 'test-task', 'Test Task', 'container-uuid-1', 'code_defect_fastlane', 1, 'open', 'medium', 'open', 2, '["label-b","label-a"]', 'Test description', 1, '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z', 'agent:test-actor', 'agent:test-actor')
	`)
	mustExec(t, db, `
		INSERT INTO comments (uuid, id, task_uuid, created_by_principal_ref, body, etag, created_at)
		VALUES ('comment-uuid-1', 'C-00001', 'task-uuid-1', 'agent:test-actor', 'Test comment', 1, '2025-01-01T00:00:00Z')
	`)
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
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
	if result.ContainerCount != 2 { // root + project
		t.Errorf("expected 2 containers, got %d", result.ContainerCount)
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
	// Canonical output carries no insignificant whitespace.
	if strings.Contains(strings.TrimSuffix(string(data), "\n"), "\n") {
		t.Errorf("canonical snapshot contains newlines:\n%s", data)
	}

	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}

	// Verify content
	if len(snap.Containers) != 2 {
		t.Errorf("expected 2 containers in snapshot, got %d", len(snap.Containers))
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

// TestImportRefusesSnapshotWithDanglingReferences: Import (the engine of
// `wrkqadm state import`) refuses a snapshot file whose references do not
// resolve inside it, naming the dangling uuid, and writes nothing; the same
// snapshot without the defect imports.
func TestImportRefusesSnapshotWithDanglingReferences(t *testing.T) {
	valid := func() *Snapshot {
		return &Snapshot{
			Meta: Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
			Containers: map[string]ContainerEntry{
				domain.RootContainerUUID: {ID: "P-00000", Slug: "wrkq-system-root", Kind: "root"},
				"container-1":            {ID: "P-00001", Slug: "proj", Title: "Project", Kind: "project", ParentUUID: domain.RootContainerUUID, CreatedByPrincipalRef: "agent:a", UpdatedByPrincipalRef: "agent:a", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
			},
			Tasks: map[string]TaskEntry{
				"task-1": {ID: "T-00001", Slug: "task", Title: "Task", ProjectUUID: "container-1", State: "open", Kind: "task", Priority: 2, CreatedByPrincipalRef: "agent:a", UpdatedByPrincipalRef: "agent:a", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
			},
			Comments: map[string]CommentEntry{
				"comment-1": {ID: "C-00001", TaskUUID: "task-1", CreatedByPrincipalRef: "agent:a", Body: "test", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z"},
			},
		}
	}

	tests := []struct {
		name    string
		break_  func(*Snapshot)
		wantErr string
	}{
		{"valid snapshot imports", func(*Snapshot) {}, ""},
		{"schema_version 0", func(s *Snapshot) { s.Meta.SchemaVersion = 0 }, "invalid schema_version: 0"},
		{"task references unknown container", func(s *Snapshot) {
			task := s.Tasks["task-1"]
			task.ProjectUUID = "unknown-container"
			s.Tasks["task-1"] = task
		}, "task task-1 references unknown container unknown-container"},
		{"comment references unknown task", func(s *Snapshot) {
			comment := s.Comments["comment-1"]
			comment.TaskUUID = "unknown-task"
			s.Comments["comment-1"] = comment
		}, "comment comment-1 references unknown task unknown-task"},
		{"container references unknown parent", func(s *Snapshot) {
			s.Containers["container-2"] = ContainerEntry{ID: "P-00002", Slug: "dir", Title: "Dir", Kind: "directory", ParentUUID: "unknown-parent", CreatedByPrincipalRef: "agent:a", UpdatedByPrincipalRef: "agent:a", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"}
		}, "container container-2 references unknown parent unknown-parent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap := valid()
			tt.break_(snap)
			data, err := json.Marshal(snap)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "state.json")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}

			target := createTestDB(t)
			_, err = Import(target, ImportOptions{InputPath: path})
			var tasks int
			if qerr := target.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&tasks); qerr != nil {
				t.Fatal(qerr)
			}
			if tt.wantErr == "" {
				if err != nil || tasks != 1 {
					t.Fatalf("valid snapshot: err=%v tasks=%d; want imported with 1 task", err, tasks)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Import err = %v; want it to name %q", err, tt.wantErr)
			}
			if tasks != 0 {
				t.Fatalf("refused import still wrote %d task(s)", tasks)
			}
		})
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
		uuid, project_uuid, container_uuid, type, principal_ref, summary,
		attributes, occurred_at
	) VALUES ('a0000000-0000-0000-0000-000000000042', 'project-stamp', 'container-stamp', 'smoke.posted',
		'agent:mable', 'smoke', '{"exact":"true"}', '2026-09-04T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	snap, _, err := ExportToSnapshot(database.DB, ExportOptions{IncludeEvents: true, Canonical: true})
	if err != nil {
		t.Fatal(err)
	}
	event, ok := snap.ProjectEvents["a0000000-0000-0000-0000-000000000042"]
	if !ok || event.Attributes != `{"exact":"true"}` || event.ProjectUUID != "project-stamp" {
		t.Fatalf("project event snapshot = %#v", event)
	}
}
