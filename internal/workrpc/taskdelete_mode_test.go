package workrpc_test

// taskdelete_mode_test.go — acceptance tests for the caller-owned-confirmation
// seam's server half (T-05100 / T-05088 Tranche B0).
//
// wrkq.task.delete gains an EXPLICIT `mode` param:
//   - absent  → legacy reversible delete PRESERVED (state=deleted + deleted_at).
//   - archive → state=archived + archived_at (legacy `wrkq rm` default).
//   - purge   → hard-delete the task row + attachment-file cleanup.
//   - invalid → WRKQ_VALIDATION.
//
// These run through the real JSON-RPC dispatch surface (go run ./cmd/wrkq rpc
// --stdio), which is itself the server-non-interactivity proof: the server only
// ever reads JSON-RPC frames from stdin and never prompts, inspects a TTY, or
// blocks on a separate confirmation read. Every disposition completes (or
// rejects) from the params alone.

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// taskRow reads the durable (state, archived_at, deleted_at) for a task UUID, or
// reports exists=false when the row is gone (purge).
func taskRow(t *testing.T, dbPath, uuid string) (exists bool, state, archivedAt, deletedAt string) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("taskRow: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var ar, dl sql.NullString
	err = database.QueryRow("SELECT state, archived_at, deleted_at FROM tasks WHERE uuid = ?", uuid).
		Scan(&state, &ar, &dl)
	if err == sql.ErrNoRows {
		return false, "", "", ""
	}
	if err != nil {
		t.Fatalf("taskRow query: %v", err)
	}
	return true, state, ar.String, dl.String
}

// createTaskRPC creates a task via wrkq.task.create and returns (id, uuid).
func createTaskRPC(t *testing.T, dbPath, title string) (id, uuid string) {
	t.Helper()
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": title, "kind": "task"}),
	)
	result := p2ResultOrFail(t, frames[1], "wrkq.task.create")
	id, _ = result["id"].(string)
	uuid, _ = result["uuid"].(string)
	if id == "" || uuid == "" {
		t.Fatalf("createTaskRPC: empty id/uuid: %#v", result)
	}
	return id, uuid
}

// TestTaskDelete_NoModePreserved proves the absent-mode contract is UNCHANGED:
// state=deleted + deleted_at tombstone, archived_at NOT set.
func TestTaskDelete_NoModePreserved(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	_, uuid := createTaskRPC(t, dbPath, "no-mode")

	frames := p2Run(t, dbPath, mkRPC("d1", "wrkq.task.delete", map[string]any{"task": uuid}))
	p2ResultOrFail(t, frames[1], "wrkq.task.delete (no mode)")

	exists, state, archivedAt, deletedAt := taskRow(t, dbPath, uuid)
	if !exists {
		t.Fatal("no-mode delete must NOT purge the row")
	}
	if state != "deleted" {
		t.Errorf("no-mode delete state: want deleted, got %q", state)
	}
	if deletedAt == "" {
		t.Error("no-mode delete must set deleted_at tombstone")
	}
	if archivedAt != "" {
		t.Errorf("no-mode delete must NOT set archived_at, got %q", archivedAt)
	}
}

// TestTaskDelete_ArchiveMode proves mode:archive sets the legacy soft-archive
// state (state=archived + archived_at), NOT the deleted tombstone.
func TestTaskDelete_ArchiveMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	_, uuid := createTaskRPC(t, dbPath, "archive-me")

	frames := p2Run(t, dbPath, mkRPC("d1", "wrkq.task.delete", map[string]any{"task": uuid, "mode": "archive"}))
	p2ResultOrFail(t, frames[1], "wrkq.task.delete (archive)")

	exists, state, archivedAt, deletedAt := taskRow(t, dbPath, uuid)
	if !exists {
		t.Fatal("archive must NOT purge the row")
	}
	if state != "archived" {
		t.Errorf("archive state: want archived, got %q", state)
	}
	if archivedAt == "" {
		t.Error("archive must set archived_at")
	}
	if deletedAt != "" {
		t.Errorf("archive must NOT set deleted_at, got %q", deletedAt)
	}
}

// TestTaskDelete_PurgeMode proves mode:purge hard-deletes the task row.
func TestTaskDelete_PurgeMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	_, uuid := createTaskRPC(t, dbPath, "purge-me")

	frames := p2Run(t, dbPath, mkRPC("d1", "wrkq.task.delete", map[string]any{"task": uuid, "mode": "purge"}))
	p2ResultOrFail(t, frames[1], "wrkq.task.delete (purge)")

	if exists, _, _, _ := taskRow(t, dbPath, uuid); exists {
		t.Error("purge must hard-delete the task row")
	}
}

// TestTaskDelete_PurgeCleansAttachments proves mode:purge removes the task's
// attachment DB rows (event-cascade cleanup), not just the task row.
func TestTaskDelete_PurgeCleansAttachments(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	_, uuid := createTaskRPC(t, dbPath, "purge-with-attachments")

	// Seed two attachment rows directly (file bytes are irrelevant to the DB-row
	// cleanup proof; the file unlink is best-effort and configured-attach-dir-gated).
	func() {
		database, err := db.Open(dbPath)
		if err != nil {
			t.Fatalf("seed attachments: db.Open: %v", err)
		}
		defer func() { _ = database.Close() }()
		for i, name := range []string{"a.txt", "b.bin"} {
			if _, err := database.Exec(
				`INSERT INTO attachments (task_uuid, filename, relative_path, mime_type, size_bytes)
				 VALUES (?, ?, ?, 'text/plain', ?)`,
				uuid, name, "tasks/"+uuid+"/"+name, (i+1)*100,
			); err != nil {
				t.Fatalf("seed attachment %s: %v", name, err)
			}
		}
	}()

	frames := p2Run(t, dbPath, mkRPC("d1", "wrkq.task.delete", map[string]any{"task": uuid, "mode": "purge"}))
	p2ResultOrFail(t, frames[1], "wrkq.task.delete (purge with attachments)")

	if exists, _, _, _ := taskRow(t, dbPath, uuid); exists {
		t.Error("purge must hard-delete the task row")
	}
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("verify attachments: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var n int
	if err := database.QueryRow("SELECT COUNT(*) FROM attachments WHERE task_uuid = ?", uuid).Scan(&n); err != nil {
		t.Fatalf("count attachments: %v", err)
	}
	if n != 0 {
		t.Errorf("purge must remove attachment rows; %d remain", n)
	}
}

// TestTaskDelete_PurgePhysicalFileCleanup proves mode:purge unlinks the actual
// attachment BYTES on disk AND removes the task attachment dir — not just the DB
// rows. It configures an explicit WRKQ_ATTACH_DIR, adds an attachment with real
// bytes via wrkq.attachment.add (which writes attachDir/tasks/<uuid>/<file>),
// asserts the file + task dir exist, purges, then asserts both are gone (plus the
// DB-row-gone guarantee).
func TestTaskDelete_PurgePhysicalFileCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	attachDir := t.TempDir()
	extraEnv := []string{"WRKQ_ATTACH_DIR=" + attachDir}

	// Create the task in the SAME attach-dir env so the server is consistently
	// configured across calls.
	cf := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "purge-physical", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "wrkq.task.create")
	taskID, _ := cr["id"].(string)
	taskUUID, _ := cr["uuid"].(string)
	if taskID == "" || taskUUID == "" {
		t.Fatalf("create returned empty id/uuid: %#v", cr)
	}

	// Add an attachment with real bytes on disk.
	srcPath := p4WriteTempFile(t, "payload.txt", "real attachment bytes that must be unlinked on purge")
	filename := filepath.Base(srcPath)
	af := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("a1", "wrkq.attachment.add", map[string]any{
			"task":     taskID,
			"path":     srcPath,
			"filename": filename,
		}),
	)
	p2ResultOrFail(t, af[1], "wrkq.attachment.add")

	storedFile := filepath.Join(attachDir, "tasks", taskUUID, filename)
	taskDir := filepath.Join(attachDir, "tasks", taskUUID)
	if _, err := os.Stat(storedFile); err != nil {
		t.Fatalf("precondition: attachment file must exist before purge at %s: %v", storedFile, err)
	}
	if _, err := os.Stat(taskDir); err != nil {
		t.Fatalf("precondition: task attachment dir must exist before purge at %s: %v", taskDir, err)
	}

	// Purge with the SAME attach-dir env so the server can resolve + unlink files.
	pf := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("d1", "wrkq.task.delete", map[string]any{"task": taskUUID, "mode": "purge"}),
	)
	p2ResultOrFail(t, pf[1], "wrkq.task.delete (purge physical)")

	// DB row gone.
	if exists, _, _, _ := taskRow(t, dbPath, taskUUID); exists {
		t.Error("purge must hard-delete the task row")
	}
	// Physical file gone.
	if _, err := os.Stat(storedFile); !os.IsNotExist(err) {
		t.Errorf("purge must unlink the attachment file %s (stat err=%v)", storedFile, err)
	}
	// Task attachment dir gone.
	if _, err := os.Stat(taskDir); !os.IsNotExist(err) {
		t.Errorf("purge must remove the task attachment dir %s (stat err=%v)", taskDir, err)
	}
}

// TestTaskDelete_InvalidMode proves an unknown mode → WRKQ_VALIDATION and leaves
// the task untouched.
func TestTaskDelete_InvalidMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	_, uuid := createTaskRPC(t, dbPath, "keep-me")

	frames := p2Run(t, dbPath, mkRPC("d1", "wrkq.task.delete", map[string]any{"task": uuid, "mode": "incinerate"}))
	if code := p2ErrCode(frames[1]); code != "WRKQ_VALIDATION" {
		t.Errorf("invalid mode: want WRKQ_VALIDATION, got %q (frame=%#v)", code, frames[1])
	}
	exists, state, _, _ := taskRow(t, dbPath, uuid)
	if !exists || state != "open" {
		t.Errorf("invalid mode must not mutate the task; exists=%v state=%q", exists, state)
	}
}

// TestTaskDelete_NonInteractive_FromParamsAlone proves the server is
// NON-INTERACTIVE: a purge completes purely from the JSON-RPC params with NO
// stdin beyond the framed requests and NO prompt. The whole session is framed
// I/O; if the server tried to read a confirmation it would block until the
// 30s subprocess timeout and the frame count assertion would fail.
func TestTaskDelete_NonInteractive_FromParamsAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	_, uuid := createTaskRPC(t, dbPath, "non-interactive")

	// Two destructive calls back-to-back in one framed session: archive then a
	// purge on a second task. Both must resolve from params with no interaction.
	_, uuid2 := createTaskRPC(t, dbPath, "non-interactive-2")
	frames := p2Run(t, dbPath,
		mkRPC("a1", "wrkq.task.delete", map[string]any{"task": uuid, "mode": "archive"}),
		mkRPC("p1", "wrkq.task.delete", map[string]any{"task": uuid2, "mode": "purge"}),
	)
	p2ResultOrFail(t, frames[1], "archive from params alone")
	p2ResultOrFail(t, frames[2], "purge from params alone")

	if _, state, archivedAt, _ := taskRow(t, dbPath, uuid); state != "archived" || archivedAt == "" {
		t.Errorf("archive: state=%q archivedAt=%q", state, archivedAt)
	}
	if exists, _, _, _ := taskRow(t, dbPath, uuid2); exists {
		t.Error("purge from params alone must hard-delete the row")
	}
}
