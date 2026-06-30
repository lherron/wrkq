package workrpc_test

// wrkqapi_deferred_acceptance_test.go — RED acceptance tests for T-04448.
//
// These tests drive the real server via subprocess (go run ./cmd/{wrkq,wrkf} --db <tmp> rpc --stdio).
// All 14 tested methods are currently stubs returning WRKQ_VALIDATION
// "method is registered but not implemented in P1".
//
// Every test must FAIL now for that reason and PASS when the methods are implemented.
//
// Covered methods:
//   - wrkq.task.delete
//   - wrkq.task.restore
//   - wrkq.task.acknowledge
//   - wrkq.attachment.add
//   - wrkq.attachment.list
//   - wrkq.attachment.remove
//   - wrkq.relation.add
//   - wrkq.relation.list
//   - wrkq.relation.remove
//   - wrkq.container.show
//   - wrkq.container.list
//
// NOTE: Size limit enforcement (WRKQ_ATTACH_MAX_MB) requires env var support;
// implementer should add WRKQ_ATTACH_MAX_MB env var support to config.Load and
// add a dedicated test.

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/db"
)

// ─── p4 helpers ─────────────────────────────────────────────────────────────

// runRPCWithEnv is like runRPC but appends extraEnv after a default caller
// principal. Like runRPC, it injects WRKQ_PRINCIPAL_REF=agent:smokey so seed
// writes have a valid principal-only attribution; extraEnv is appended last so a
// test may still override it.
func runRPCWithEnv(t *testing.T, entrypoint, dbPath string, requests []string, extraEnv []string) []map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	args := []string{"run", "./cmd/" + entrypoint, "--db", dbPath, "rpc", "--stdio"}
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = repoRoot(t)
	cmd.Env = append(append(os.Environ(), "WRKQ_PRINCIPAL_REF=agent:smokey"), extraEnv...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("runRPCWithEnv: stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("runRPCWithEnv: stdout pipe: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("runRPCWithEnv: start %s: %v", entrypoint, err)
	}
	for _, req := range requests {
		if _, err := stdin.Write([]byte(req + "\n")); err != nil {
			t.Fatalf("runRPCWithEnv: %s write request: %v", entrypoint, err)
		}
	}
	_ = stdin.Close()

	var frames []map[string]any
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		var frame map[string]any
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("runRPCWithEnv: %s stdout contained non-JSON-RPC line %q: %v; stderr=%s", entrypoint, line, err, stderr.String())
		}
		if frame["jsonrpc"] != "2.0" {
			t.Fatalf("runRPCWithEnv: %s stdout frame missing jsonrpc=2.0: %s", entrypoint, line)
		}
		frames = append(frames, frame)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("runRPCWithEnv: %s stdout scan: %v", entrypoint, err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("runRPCWithEnv: %s failed: %v; stderr=%s", entrypoint, err, stderr.String())
	}
	return frames
}

// pdRunEnv is like p2Run but parameterized over the entrypoint (wrkq or wrkf)
func pdRunEnv(t *testing.T, entrypoint, dbPath string, extraEnv []string, reqs ...string) []map[string]any {
	t.Helper()
	seq := []string{
		mkRPC("_init", "rpc.initialize", map[string]any{
			"protocolVersion": "2026-06-14",
			"client":          map[string]any{"name": "p4-smokey", "version": "0.0.1"},
		}),
	}
	seq = append(seq, reqs...)
	seq = append(seq,
		mkRPC("_sd", "rpc.shutdown", map[string]any{}),
		mkRPC("", "rpc.exit", nil),
	)
	frames := runRPCWithEnv(t, entrypoint, dbPath, seq, extraEnv)
	want := 2 + len(reqs)
	if len(frames) != want {
		t.Fatalf("pdRunEnv(%s): expected %d frames, got %d\nframes: %#v", entrypoint, want, len(frames), frames)
	}
	return frames
}

// p4IsStubError returns true if the frame's error message contains "not implemented in P1".
func p4IsStubError(frame map[string]any) bool {
	errObj, _ := frame["error"].(map[string]any)
	if errObj == nil {
		return false
	}
	msg, _ := errObj["message"].(string)
	return strings.Contains(msg, "not implemented in P1")
}

// p4AssertNotStub calls t.Errorf if p4IsStubError(frame) is true.
func p4AssertNotStub(t *testing.T, frame map[string]any, label string) {
	t.Helper()
	if p4IsStubError(frame) {
		t.Errorf("%s: got stub error 'not implemented in P1'; method must be fully implemented", label)
	}
}

// p4SeedDeletedTask inserts a deleted task directly into the DB.
// Returns the wrkq task ID (T-XXXXX).
func p4SeedDeletedTask(t *testing.T, dbPath, taskUUID, slug, title string) string {
	t.Helper()

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("p4SeedDeletedTask: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	actorUUID := "00000000-0000-4000-8000-0000000000a0"
	projUUID := "00000000-1111-4000-8000-000000000001"

	_, _ = database.Exec(
		`INSERT OR IGNORE INTO containers (uuid, slug, title, parent_uuid, kind,
		                                   created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'p2-test-proj', 'P2 Test Project',
		         (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		projUUID, actorUUID, actorUUID,
	)

	_, err = database.Exec(
		`INSERT OR IGNORE INTO tasks (uuid, slug, title, description, project_uuid, state, priority, kind,
		                              deleted_at, created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, ?, ?, '', ?, 'deleted', 2, 'task', datetime('now'), ?, ?)`,
		taskUUID, slug, title, projUUID, actorUUID, actorUUID,
	)
	if err != nil {
		t.Fatalf("p4SeedDeletedTask: INSERT task: %v", err)
	}

	var taskID string
	if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", taskUUID).Scan(&taskID); err != nil {
		t.Fatalf("p4SeedDeletedTask: fetch task id: %v", err)
	}
	return taskID
}

// p4SeedArchivedTask inserts an archived task directly into the DB.
// Returns the wrkq task ID (T-XXXXX).
func p4SeedArchivedTask(t *testing.T, dbPath, taskUUID, slug, title string) string {
	t.Helper()

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("p4SeedArchivedTask: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	actorUUID := "00000000-0000-4000-8000-0000000000a0"
	projUUID := "00000000-1111-4000-8000-000000000001"

	_, _ = database.Exec(
		`INSERT OR IGNORE INTO containers (uuid, slug, title, parent_uuid, kind,
		                                   created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'p2-test-proj', 'P2 Test Project',
		         (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		projUUID, actorUUID, actorUUID,
	)

	_, err = database.Exec(
		`INSERT OR IGNORE INTO tasks (uuid, slug, title, description, project_uuid, state, priority, kind,
		                              archived_at, created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, ?, ?, '', ?, 'archived', 2, 'task', datetime('now'), ?, ?)`,
		taskUUID, slug, title, projUUID, actorUUID, actorUUID,
	)
	if err != nil {
		t.Fatalf("p4SeedArchivedTask: INSERT task: %v", err)
	}

	var taskID string
	if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", taskUUID).Scan(&taskID); err != nil {
		t.Fatalf("p4SeedArchivedTask: fetch task id: %v", err)
	}
	return taskID
}

// p4SeedAcknowledgedTask inserts a completed+acknowledged task directly into the DB.
// Returns the wrkq task ID (T-XXXXX).
func p4SeedAcknowledgedTask(t *testing.T, dbPath, taskUUID, slug, title string) string {
	t.Helper()

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("p4SeedAcknowledgedTask: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	actorUUID := "00000000-0000-4000-8000-0000000000a0"
	projUUID := "00000000-1111-4000-8000-000000000001"

	_, _ = database.Exec(
		`INSERT OR IGNORE INTO containers (uuid, slug, title, parent_uuid, kind,
		                                   created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'p2-test-proj', 'P2 Test Project',
		         (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		projUUID, actorUUID, actorUUID,
	)

	_, err = database.Exec(
		`INSERT OR IGNORE INTO tasks (uuid, slug, title, description, project_uuid, state, priority, kind,
		                              acknowledged_at, created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, ?, ?, '', ?, 'completed', 2, 'task', datetime('now'), ?, ?)`,
		taskUUID, slug, title, projUUID, actorUUID, actorUUID,
	)
	if err != nil {
		t.Fatalf("p4SeedAcknowledgedTask: INSERT task: %v", err)
	}

	var taskID string
	if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", taskUUID).Scan(&taskID); err != nil {
		t.Fatalf("p4SeedAcknowledgedTask: fetch task id: %v", err)
	}
	return taskID
}

// p4SeedSubtask inserts a child task with parent_task_uuid set.
// Returns the child's wrkq task ID (T-XXXXX).
func p4SeedSubtask(t *testing.T, dbPath, parentUUID, childUUID, childSlug, childTitle string) string {
	t.Helper()

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("p4SeedSubtask: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	actorUUID := "00000000-0000-4000-8000-0000000000a0"
	projUUID := "00000000-1111-4000-8000-000000000001"

	_, _ = database.Exec(
		`INSERT OR IGNORE INTO containers (uuid, slug, title, parent_uuid, kind,
		                                   created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'p2-test-proj', 'P2 Test Project',
		         (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		projUUID, actorUUID, actorUUID,
	)

	_, err = database.Exec(
		`INSERT OR IGNORE INTO tasks (uuid, slug, title, description, project_uuid, state, priority, kind,
		                              parent_task_uuid, created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, ?, ?, '', ?, 'open', 2, 'task', ?, ?, ?)`,
		childUUID, childSlug, childTitle, projUUID, parentUUID, actorUUID, actorUUID,
	)
	if err != nil {
		t.Fatalf("p4SeedSubtask: INSERT child task: %v", err)
	}

	var taskID string
	if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", childUUID).Scan(&taskID); err != nil {
		t.Fatalf("p4SeedSubtask: fetch child task id: %v", err)
	}
	return taskID
}

// p4WriteTempFile creates a file with the given name and content in t.TempDir().
// Returns the full path to the created file.
func p4WriteTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("p4WriteTempFile: write %s: %v", path, err)
	}
	return path
}

// filterEnv returns a copy of env without entries whose key matches the given prefix.
func filterEnv(env []string, key string) []string {
	prefix := key + "="
	var out []string
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return out
}

// ─── Group 1: task.delete / task.restore ────────────────────────────────────

// TestWrkqTaskDelete_SetsDeletedState creates a live task, deletes it, and asserts
// that the result has state=deleted + deletedAt non-empty, and archivedAt not set.
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1" → p2ResultOrFail fires.
func TestWrkqTaskDelete_SetsDeletedState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Delete Me", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "wrkq.task.create")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test task.delete")
	}

	df := p2Run(t, dbPath,
		mkRPC("d1", "wrkq.task.delete", map[string]any{"task": taskID}),
	)
	result := p2ResultOrFail(t, df[1], "wrkq.task.delete must return result")

	p2AssertFieldEq(t, result, "state", "deleted")

	deletedAt, _ := result["deletedAt"].(string)
	if deletedAt == "" {
		t.Errorf("wrkq.task.delete: result must have non-empty deletedAt, got: %v", result["deletedAt"])
	}

	// archivedAt must not be set.
	if v, ok := result["archivedAt"]; ok {
		if s, _ := v.(string); s != "" {
			t.Errorf("wrkq.task.delete: archivedAt must not be set, got %q", s)
		}
	}
}

// TestWrkqTaskDelete_CascadesSubtasks seeds a parent+subtask, deletes the parent,
// and asserts that the subtask is also marked deleted.
//
// RED: stub returns WRKQ_VALIDATION → p2ResultOrFail fires on delete.
func TestWrkqTaskDelete_CascadesSubtasks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	parentUUID := "f0000001-0000-4000-8000-000000000001"
	childUUID := "f0000001-0000-4000-8000-000000000002"

	parentID := p2SeedTask(t, dbPath, parentUUID, "cascade-parent", "Cascade Parent")
	_ = p4SeedSubtask(t, dbPath, parentUUID, childUUID, "cascade-child", "Cascade Child")

	// Delete parent.
	df := p2Run(t, dbPath,
		mkRPC("d1", "wrkq.task.delete", map[string]any{"task": parentID}),
	)
	p2ResultOrFail(t, df[1], "wrkq.task.delete parent")

	// Show child and assert it is deleted.
	sf := p2Run(t, dbPath,
		mkRPC("s1", "wrkq.task.show", map[string]any{"task": childUUID}),
	)
	childResult := p2ResultOrFail(t, sf[1], "wrkq.task.show subtask after parent delete")
	p2AssertFieldEq(t, childResult, "state", "deleted")
}

// TestWrkqTaskDelete_Redelete_NoOp seeds a deleted task and calls delete again.
// Should succeed and return the same deletedAt.
//
// RED: stub returns WRKQ_VALIDATION → p2ResultOrFail fires.
func TestWrkqTaskDelete_Redelete_NoOp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	taskUUID := "f0000002-0000-4000-8000-000000000001"
	taskID := p4SeedDeletedTask(t, dbPath, taskUUID, "redelete-noop", "Redelete No-Op")

	df := p2Run(t, dbPath,
		mkRPC("d1", "wrkq.task.delete", map[string]any{"task": taskID}),
	)
	result := p2ResultOrFail(t, df[1], "wrkq.task.delete on already-deleted task")
	p2AssertFieldEq(t, result, "state", "deleted")
}

// TestWrkqTaskRestore_FromDeleted_SetsOpen seeds a deleted task and restores it.
// Expects state=open and deletedAt absent/empty.
//
// RED: stub returns WRKQ_VALIDATION → p2ResultOrFail fires.
func TestWrkqTaskRestore_FromDeleted_SetsOpen(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	taskUUID := "f0000003-0000-4000-8000-000000000001"
	taskID := p4SeedDeletedTask(t, dbPath, taskUUID, "restore-from-deleted", "Restore From Deleted")

	rf := p2Run(t, dbPath,
		mkRPC("r1", "wrkq.task.restore", map[string]any{"task": taskID}),
	)
	result := p2ResultOrFail(t, rf[1], "wrkq.task.restore from deleted")

	p2AssertFieldEq(t, result, "state", "open")

	if v, ok := result["deletedAt"]; ok {
		if s, _ := v.(string); s != "" {
			t.Errorf("wrkq.task.restore: deletedAt must be absent or empty, got %q", s)
		}
	}
}

// TestWrkqTaskRestore_FromArchived_SetsOpen seeds an archived task and restores it.
// Expects state=open and archivedAt absent/empty.
//
// RED: stub returns WRKQ_VALIDATION → p2ResultOrFail fires.
func TestWrkqTaskRestore_FromArchived_SetsOpen(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	taskUUID := "f0000004-0000-4000-8000-000000000001"
	taskID := p4SeedArchivedTask(t, dbPath, taskUUID, "restore-from-archived", "Restore From Archived")

	rf := p2Run(t, dbPath,
		mkRPC("r1", "wrkq.task.restore", map[string]any{"task": taskID}),
	)
	result := p2ResultOrFail(t, rf[1], "wrkq.task.restore from archived")

	p2AssertFieldEq(t, result, "state", "open")

	if v, ok := result["archivedAt"]; ok {
		if s, _ := v.(string); s != "" {
			t.Errorf("wrkq.task.restore: archivedAt must be absent or empty, got %q", s)
		}
	}
}

// TestWrkqTaskRestore_RejectsLiveTask creates a live (open) task and calls restore.
// Expects WRKQ_VALIDATION that is NOT the stub error.
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1" → p4AssertNotStub fires.
func TestWrkqTaskRestore_RejectsLiveTask(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Live Task For Restore", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "wrkq.task.create")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test restore rejection")
	}

	rf := p2Run(t, dbPath,
		mkRPC("r1", "wrkq.task.restore", map[string]any{"task": taskID}),
	)
	code := p2ErrCode(rf[1])
	if code != "WRKQ_VALIDATION" {
		t.Errorf("restore live task: want WRKQ_VALIDATION, got %q", code)
	}
	p4AssertNotStub(t, rf[1], "restore live task must be real domain error, not stub")
}

// TestWrkqTaskRestore_RejectsDeletedTargetState seeds a deleted task and calls
// restore with state="deleted". Expects WRKQ_VALIDATION that is NOT the stub error.
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1" → p4AssertNotStub fires.
func TestWrkqTaskRestore_RejectsDeletedTargetState(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	taskUUID := "f0000005-0000-4000-8000-000000000001"
	taskID := p4SeedDeletedTask(t, dbPath, taskUUID, "restore-bad-state", "Restore Bad State")

	rf := p2Run(t, dbPath,
		mkRPC("r1", "wrkq.task.restore", map[string]any{"task": taskID, "state": "deleted"}),
	)
	code := p2ErrCode(rf[1])
	if code != "WRKQ_VALIDATION" {
		t.Errorf("restore with state=deleted: want WRKQ_VALIDATION, got %q", code)
	}
	p4AssertNotStub(t, rf[1], "restore with invalid target state must be real domain error, not stub")
}

// TestWrkqTaskRestore_ExtendedFields restores an archived task while applying the
// server-side flag set carried by the extended wrkq.task.restore (T-05100 item 4):
// target state + field updates (priority) + a comment, in one atomic call. The DTO
// reflects the applied state/priority; the method never prompts or reads stdin —
// every input arrives as an explicit param.
func TestWrkqTaskRestore_ExtendedFields(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	taskUUID := "f0000003-0000-4000-8000-00000000aa01"
	taskID := p4SeedArchivedTask(t, dbPath, taskUUID, "restore-extended", "Restore Extended")

	rf := p2Run(t, dbPath,
		mkRPC("r1", "wrkq.task.restore", map[string]any{
			"task":     taskID,
			"state":    "in_progress",
			"priority": 1,
			"comment":  "back online",
		}),
	)
	result := p2ResultOrFail(t, rf[1], "wrkq.task.restore extended fields")
	p2AssertFieldEq(t, result, "state", "in_progress")
	p2AssertFieldEq(t, result, "priority", float64(1))
}

// TestWrkqTaskRestore_IfMatchMismatch proves the conditional --if-match
// precondition: a non-matching expected etag yields WRKQ_CONFLICT (not a stub),
// with no mutation. The server decides purely from the supplied param.
func TestWrkqTaskRestore_IfMatchMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	taskUUID := "f0000003-0000-4000-8000-00000000aa02"
	taskID := p4SeedArchivedTask(t, dbPath, taskUUID, "restore-ifmatch", "Restore IfMatch")

	rf := p2Run(t, dbPath,
		mkRPC("r1", "wrkq.task.restore", map[string]any{"task": taskID, "ifMatch": 999}),
	)
	code := p2ErrCode(rf[1])
	if code != "WRKQ_CONFLICT" {
		t.Errorf("restore with stale ifMatch: want WRKQ_CONFLICT, got %q", code)
	}
	p4AssertNotStub(t, rf[1], "restore ifMatch mismatch must be a real conflict, not stub")
}

// TestWrkqTaskRestore_InvalidPriority proves an out-of-range --priority is a real
// WRKQ_VALIDATION (server-side flag validation), not a stub.
func TestWrkqTaskRestore_InvalidPriority(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	taskUUID := "f0000003-0000-4000-8000-00000000aa03"
	taskID := p4SeedArchivedTask(t, dbPath, taskUUID, "restore-badprio", "Restore BadPrio")

	rf := p2Run(t, dbPath,
		mkRPC("r1", "wrkq.task.restore", map[string]any{"task": taskID, "priority": 99}),
	)
	code := p2ErrCode(rf[1])
	if code != "WRKQ_VALIDATION" {
		t.Errorf("restore with priority=99: want WRKQ_VALIDATION, got %q", code)
	}
	p4AssertNotStub(t, rf[1], "restore invalid priority must be a real validation error, not stub")
}

// ─── Group 2: task.acknowledge ───────────────────────────────────────────────

// TestWrkqTaskAcknowledge_Completed_Succeeds creates a task, updates it to
// completed, then acknowledges it. Expects result with acknowledgedAt non-empty.
//
// RED: stub returns WRKQ_VALIDATION → p2ResultOrFail fires on acknowledge.
func TestWrkqTaskAcknowledge_Completed_Succeeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Ack Completed", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "wrkq.task.create")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test acknowledge")
	}

	p2Run(t, dbPath,
		mkRPC("u1", "wrkq.task.update", map[string]any{
			"task":  taskID,
			"patch": map[string]any{"state": "completed"},
		}),
	)

	af := p2Run(t, dbPath,
		mkRPC("a1", "wrkq.task.acknowledge", map[string]any{"task": taskID}),
	)
	result := p2ResultOrFail(t, af[1], "wrkq.task.acknowledge completed task")

	ackedAt, _ := result["acknowledgedAt"].(string)
	if ackedAt == "" {
		t.Errorf("wrkq.task.acknowledge: result must have non-empty acknowledgedAt, got: %v", result["acknowledgedAt"])
	}
}

// TestWrkqTaskAcknowledge_Cancelled_Succeeds creates a task, updates it to
// cancelled, then acknowledges it. Expects result with acknowledgedAt non-empty.
//
// RED: stub returns WRKQ_VALIDATION → p2ResultOrFail fires on acknowledge.
func TestWrkqTaskAcknowledge_Cancelled_Succeeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Ack Cancelled", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "wrkq.task.create")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test acknowledge cancelled")
	}

	p2Run(t, dbPath,
		mkRPC("u1", "wrkq.task.update", map[string]any{
			"task":  taskID,
			"patch": map[string]any{"state": "cancelled"},
		}),
	)

	af := p2Run(t, dbPath,
		mkRPC("a1", "wrkq.task.acknowledge", map[string]any{"task": taskID}),
	)
	result := p2ResultOrFail(t, af[1], "wrkq.task.acknowledge cancelled task")

	ackedAt, _ := result["acknowledgedAt"].(string)
	if ackedAt == "" {
		t.Errorf("wrkq.task.acknowledge: result must have non-empty acknowledgedAt, got: %v", result["acknowledgedAt"])
	}
}

// TestWrkqTaskAcknowledge_Open_NoForce_Fails creates an open task and acknowledges
// it without force. Expects WRKQ_VALIDATION that is NOT the stub error.
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1" → p4AssertNotStub fires.
func TestWrkqTaskAcknowledge_Open_NoForce_Fails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Ack Open No Force", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "wrkq.task.create")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test acknowledge open task")
	}

	af := p2Run(t, dbPath,
		mkRPC("a1", "wrkq.task.acknowledge", map[string]any{"task": taskID}),
	)
	code := p2ErrCode(af[1])
	if code != "WRKQ_VALIDATION" {
		t.Errorf("acknowledge open task without force: want WRKQ_VALIDATION, got %q", code)
	}
	p4AssertNotStub(t, af[1], "acknowledge open task must be real domain error, not stub")
}

// TestWrkqTaskAcknowledge_Force_Succeeds creates an open task and acknowledges it
// with force=true. Expects result with acknowledgedAt non-empty.
//
// RED: stub returns WRKQ_VALIDATION → p2ResultOrFail fires.
func TestWrkqTaskAcknowledge_Force_Succeeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Ack Force Open", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "wrkq.task.create")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test force acknowledge")
	}

	af := p2Run(t, dbPath,
		mkRPC("a1", "wrkq.task.acknowledge", map[string]any{"task": taskID, "force": true}),
	)
	result := p2ResultOrFail(t, af[1], "wrkq.task.acknowledge with force=true")

	ackedAt, _ := result["acknowledgedAt"].(string)
	if ackedAt == "" {
		t.Errorf("wrkq.task.acknowledge force: result must have non-empty acknowledgedAt, got: %v", result["acknowledgedAt"])
	}
}

// TestWrkqTaskAcknowledge_AlreadyAcked_NoOp seeds an already-acknowledged task
// and calls acknowledge again. Expects result (not error) with stable acknowledgedAt.
//
// RED: stub returns WRKQ_VALIDATION → p2ResultOrFail fires.
func TestWrkqTaskAcknowledge_AlreadyAcked_NoOp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	taskUUID := "f0000006-0000-4000-8000-000000000001"
	taskID := p4SeedAcknowledgedTask(t, dbPath, taskUUID, "already-acked", "Already Acknowledged")

	af := p2Run(t, dbPath,
		mkRPC("a1", "wrkq.task.acknowledge", map[string]any{"task": taskID}),
	)
	result := p2ResultOrFail(t, af[1], "wrkq.task.acknowledge already-acked task must succeed (no-op)")

	ackedAt, _ := result["acknowledgedAt"].(string)
	if ackedAt == "" {
		t.Errorf("wrkq.task.acknowledge already-acked: acknowledgedAt must remain non-empty, got: %v", result["acknowledgedAt"])
	}
}

// TestWrkqTaskDTO_AcknowledgedAtField creates a task, acknowledges it with force,
// then shows it and asserts the DTO has a non-empty acknowledgedAt field.
//
// RED: stub returns WRKQ_VALIDATION → p2ResultOrFail fires on acknowledge.
func TestWrkqTaskDTO_AcknowledgedAtField(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "DTO Ack Field", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "wrkq.task.create")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test DTO acknowledgedAt field")
	}

	af := p2Run(t, dbPath,
		mkRPC("a1", "wrkq.task.acknowledge", map[string]any{"task": taskID, "force": true}),
	)
	p2ResultOrFail(t, af[1], "wrkq.task.acknowledge with force")

	sf := p2Run(t, dbPath,
		mkRPC("s1", "wrkq.task.show", map[string]any{"task": taskID}),
	)
	showResult := p2ResultOrFail(t, sf[1], "wrkq.task.show after acknowledge")

	ackedAt, _ := showResult["acknowledgedAt"].(string)
	if ackedAt == "" {
		t.Errorf("wrkq.task.show after acknowledge: DTO must have non-empty acknowledgedAt, got: %v", showResult["acknowledgedAt"])
	}
}

// ─── Group 3: attachment entrypoint equivalence ──────────────────────────────

// TestWrkqAttachment_WrkqEntrypoint_UsesAttachDir verifies that wrkq entrypoint
// correctly stores an attachment in WRKQ_ATTACH_DIR.
//
// RED: stub returns WRKQ_VALIDATION → p2ResultOrFail fires.
func TestWrkqAttachment_WrkqEntrypoint_UsesAttachDir(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	attachDir := t.TempDir()
	extraEnv := []string{"WRKQ_ATTACH_DIR=" + attachDir}

	cf := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Attach Wrkq Entrypoint", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "wrkq.task.create")
	taskID, _ := cr["id"].(string)
	taskUUID, _ := cr["uuid"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test attachment")
	}

	filePath := p4WriteTempFile(t, "test-attach.txt", "attachment content for wrkq entrypoint")
	filename := filepath.Base(filePath)

	af := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("a1", "wrkq.attachment.add", map[string]any{
			"task":     taskID,
			"path":     filePath,
			"filename": filename,
		}),
	)
	result := p2ResultOrFail(t, af[1], "wrkq.attachment.add via wrkq entrypoint")

	p2AssertStr(t, result, "checksum")
	p2AssertStr(t, result, "uuid")
	p2AssertStr(t, result, "filename")
	p2AssertStr(t, result, "taskUuid")
	p2AssertAbsent(t, result, "sha256")

	// Verify file exists at attachDir/tasks/<taskUUID>/<filename>.
	if taskUUID != "" {
		expectedPath := filepath.Join(attachDir, "tasks", taskUUID, filename)
		if _, err := os.Stat(expectedPath); err != nil {
			t.Errorf("attachment file not found at expected path %s: %v", expectedPath, err)
		}
	}
}

// TestWrkqAttachment_WrkfEntrypoint_SameAttachDir verifies that wrkf entrypoint
// correctly stores an attachment in WRKQ_ATTACH_DIR.
//
// RED: stub returns WRKQ_VALIDATION → p2ResultOrFail fires.
func TestWrkqAttachment_WrkfEntrypoint_SameAttachDir(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	attachDir := t.TempDir()
	// The wrkf entrypoint derives caller attribution from its --actor (WRKF_ACTOR);
	// supply a canonical principal so writes pass principal-only validation.
	extraEnv := []string{"WRKQ_ATTACH_DIR=" + attachDir, "WRKF_ACTOR=agent:smokey"}

	cf := pdRunEnv(t, "wrkf", dbPath, extraEnv,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Attach Wrkf Entrypoint", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "wrkq.task.create via wrkf")
	taskID, _ := cr["id"].(string)
	taskUUID, _ := cr["uuid"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test attachment via wrkf")
	}

	filePath := p4WriteTempFile(t, "test-attach-wrkf.txt", "attachment content for wrkf entrypoint")
	filename := filepath.Base(filePath)

	af := pdRunEnv(t, "wrkf", dbPath, extraEnv,
		mkRPC("a1", "wrkq.attachment.add", map[string]any{
			"task":     taskID,
			"path":     filePath,
			"filename": filename,
		}),
	)
	result := p2ResultOrFail(t, af[1], "wrkq.attachment.add via wrkf entrypoint")

	p2AssertStr(t, result, "checksum")
	p2AssertStr(t, result, "uuid")
	p2AssertStr(t, result, "filename")
	p2AssertStr(t, result, "taskUuid")
	p2AssertAbsent(t, result, "sha256")

	// Verify file exists at attachDir/tasks/<taskUUID>/<filename>.
	if taskUUID != "" {
		expectedPath := filepath.Join(attachDir, "tasks", taskUUID, filename)
		if _, err := os.Stat(expectedPath); err != nil {
			t.Errorf("attachment file not found at expected path %s: %v", expectedPath, err)
		}
	}
}

// TestWrkqAttachment_MissingAttachDir_Fails verifies that attachment.add fails
// with WRKQ_VALIDATION when WRKQ_ATTACH_DIR is not set.
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1" → p4AssertNotStub fires.
func TestWrkqAttachment_MissingAttachDir_Fails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Seed a task directly (no attach dir needed for task creation).
	taskUUID := "f0000007-0000-4000-8000-000000000001"
	taskID := p2SeedTask(t, dbPath, taskUUID, "attach-no-dir", "Attach No Dir Task")

	filePath := p4WriteTempFile(t, "test-no-dir.txt", "content")
	filename := filepath.Base(filePath)

	// Strip WRKQ_ATTACH_DIR from environment so it is unset in the subprocess.
	cleanEnv := filterEnv(os.Environ(), "WRKQ_ATTACH_DIR")

	seq := []string{
		mkRPC("_init", "rpc.initialize", map[string]any{
			"protocolVersion": "2026-06-14",
			"client":          map[string]any{"name": "p4-smokey", "version": "0.0.1"},
		}),
		mkRPC("a1", "wrkq.attachment.add", map[string]any{
			"task":     taskID,
			"path":     filePath,
			"filename": filename,
		}),
		mkRPC("_sd", "rpc.shutdown", map[string]any{}),
		mkRPC("", "rpc.exit", nil),
	}
	frames := runRPCWithEnv(t, "wrkq", dbPath, seq, cleanEnv)
	// frames: init(0), add(1), shutdown(2)
	if len(frames) != 3 {
		t.Fatalf("missing attach dir: expected 3 frames, got %d", len(frames))
	}

	code := p2ErrCode(frames[1])
	if code != "WRKQ_VALIDATION" {
		t.Errorf("missing attach dir: want WRKQ_VALIDATION, got %q", code)
	}
	p4AssertNotStub(t, frames[1], "missing attach dir must be real config error, not stub")
}

// ─── Group 4: attachment.add/list/remove ────────────────────────────────────

// TestWrkqAttachmentAdd_ReturnsDTO_ChecksumField verifies the attachment DTO shape.
//
// RED: stub returns WRKQ_VALIDATION → p2ResultOrFail fires.
func TestWrkqAttachmentAdd_ReturnsDTO_ChecksumField(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	attachDir := t.TempDir()
	extraEnv := []string{"WRKQ_ATTACH_DIR=" + attachDir}

	cf := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Attach DTO Test", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "wrkq.task.create")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test attachment DTO")
	}

	filePath := p4WriteTempFile(t, "dto-test.txt", "checksum field test content")
	filename := filepath.Base(filePath)

	af := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("a1", "wrkq.attachment.add", map[string]any{
			"task":     taskID,
			"path":     filePath,
			"filename": filename,
		}),
	)
	result := p2ResultOrFail(t, af[1], "wrkq.attachment.add DTO shape")

	// Required DTO fields.
	p2AssertStr(t, result, "checksum")
	p2AssertStr(t, result, "uuid")
	p2AssertStr(t, result, "filename")
	p2AssertStr(t, result, "taskUuid")

	// sha256 is NOT the field name; checksum is.
	p2AssertAbsent(t, result, "sha256")
}

// TestWrkqAttachmentAdd_DuplicateFilename_Conflict adds the same filename twice
// and asserts the second call returns WRKQ_CONFLICT.
//
// RED: stub returns WRKQ_VALIDATION on first add → p2ResultOrFail fires.
func TestWrkqAttachmentAdd_DuplicateFilename_Conflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	attachDir := t.TempDir()
	extraEnv := []string{"WRKQ_ATTACH_DIR=" + attachDir}

	cf := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Attach Duplicate Test", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "wrkq.task.create")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test duplicate attachment")
	}

	filePath := p4WriteTempFile(t, "dup-test.txt", "duplicate content")
	filename := filepath.Base(filePath)

	// First add.
	af1 := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("a1", "wrkq.attachment.add", map[string]any{
			"task":     taskID,
			"path":     filePath,
			"filename": filename,
		}),
	)
	p2ResultOrFail(t, af1[1], "first wrkq.attachment.add")

	// Second add with same filename.
	af2 := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("a2", "wrkq.attachment.add", map[string]any{
			"task":     taskID,
			"path":     filePath,
			"filename": filename,
		}),
	)
	code := p2ErrCode(af2[1])
	if code != "WRKQ_CONFLICT" {
		t.Errorf("duplicate attachment: want WRKQ_CONFLICT, got %q", code)
	}
	p4AssertNotStub(t, af2[1], "duplicate attachment must be real domain error, not stub")
}

// TestWrkqAttachmentAdd_Idempotency_Replay adds an attachment with an idempotencyKey
// twice and verifies both calls return the same uuid with only one file on disk.
//
// RED: stub returns WRKQ_VALIDATION on first add → p2ResultOrFail fires.
func TestWrkqAttachmentAdd_Idempotency_Replay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	attachDir := t.TempDir()
	extraEnv := []string{"WRKQ_ATTACH_DIR=" + attachDir}

	cf := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Attach Idempotency Test", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "wrkq.task.create")
	taskID, _ := cr["id"].(string)
	taskUUID, _ := cr["uuid"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test attachment idempotency")
	}

	filePath := p4WriteTempFile(t, "idem-test.txt", "idempotency test content")
	filename := filepath.Base(filePath)
	idemKey := "p4-smokey:attach:idem:" + taskID + ":idem-test.txt"

	addReq := mkRPC("a1", "wrkq.attachment.add", map[string]any{
		"task":           taskID,
		"path":           filePath,
		"filename":       filename,
		"idempotencyKey": idemKey,
	})

	// First add.
	af1 := pdRunEnv(t, "wrkq", dbPath, extraEnv, addReq)
	r1 := p2ResultOrFail(t, af1[1], "first wrkq.attachment.add with idempotencyKey")
	uuid1, _ := r1["uuid"].(string)

	// Second add with same key.
	af2 := pdRunEnv(t, "wrkq", dbPath, extraEnv, addReq)
	r2 := p2ResultOrFail(t, af2[1], "second wrkq.attachment.add replay")
	uuid2, _ := r2["uuid"].(string)

	if uuid1 == "" || uuid1 != uuid2 {
		t.Errorf("attachment idempotency replay: expected same uuid; first=%q second=%q", uuid1, uuid2)
	}

	// Only one file should exist on disk.
	if taskUUID != "" {
		taskDir := filepath.Join(attachDir, "tasks", taskUUID)
		entries, err := os.ReadDir(taskDir)
		if err != nil {
			t.Logf("could not read task attach dir %s: %v", taskDir, err)
		} else if len(entries) != 1 {
			t.Errorf("attachment idempotency: expected 1 file on disk, found %d", len(entries))
		}
	}
}

// TestWrkqAttachmentAdd_Idempotency_Mismatch adds an attachment with a key, then
// adds a different file with the same key. Expects WRKQ_CONFLICT on second add.
//
// RED: stub returns WRKQ_VALIDATION on first add → p2ResultOrFail fires.
func TestWrkqAttachmentAdd_Idempotency_Mismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	attachDir := t.TempDir()
	extraEnv := []string{"WRKQ_ATTACH_DIR=" + attachDir}

	cf := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Attach Idem Mismatch", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "wrkq.task.create")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test attachment idempotency mismatch")
	}

	idemKey := "p4-smokey:attach:idem-mismatch:" + taskID

	file1 := p4WriteTempFile(t, "mismatch-1.txt", "original content")
	file2 := p4WriteTempFile(t, "mismatch-2.txt", "different content entirely")

	// First add.
	af1 := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("a1", "wrkq.attachment.add", map[string]any{
			"task":           taskID,
			"path":           file1,
			"filename":       "mismatch.txt",
			"idempotencyKey": idemKey,
		}),
	)
	p2ResultOrFail(t, af1[1], "first wrkq.attachment.add")

	// Second add with same key but different content.
	af2 := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("a2", "wrkq.attachment.add", map[string]any{
			"task":           taskID,
			"path":           file2,
			"filename":       "mismatch.txt",
			"idempotencyKey": idemKey,
		}),
	)
	code := p2ErrCode(af2[1])
	if code != "WRKQ_CONFLICT" {
		t.Errorf("attachment idempotency mismatch: want WRKQ_CONFLICT, got %q", code)
	}
	p4AssertNotStub(t, af2[1], "attachment idempotency mismatch must be real domain error, not stub")
}

// TestWrkqAttachmentList_ReturnsItems creates a task, adds an attachment, and
// lists attachments. Expects result with items array containing at least 1 item.
//
// RED: stub returns WRKQ_VALIDATION on add → p2ResultOrFail fires.
func TestWrkqAttachmentList_ReturnsItems(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	attachDir := t.TempDir()
	extraEnv := []string{"WRKQ_ATTACH_DIR=" + attachDir}

	cf := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Attach List Test", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "wrkq.task.create")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test attachment list")
	}

	filePath := p4WriteTempFile(t, "list-test.txt", "list test content")
	filename := filepath.Base(filePath)

	af := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("a1", "wrkq.attachment.add", map[string]any{
			"task":     taskID,
			"path":     filePath,
			"filename": filename,
		}),
	)
	p2ResultOrFail(t, af[1], "wrkq.attachment.add")

	lf := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("l1", "wrkq.attachment.list", map[string]any{"task": taskID}),
	)
	result := p2ResultOrFail(t, lf[1], "wrkq.attachment.list")
	p2AssertHasItems(t, result, "wrkq.attachment.list")

	items, _ := result["items"].([]any)
	if len(items) < 1 {
		t.Errorf("wrkq.attachment.list: expected at least 1 item, got %d", len(items))
	}
}

// TestWrkqAttachmentRemove_NotFound calls attachment.remove with a nonexistent
// uuid and expects WRKQ_NOT_FOUND that is NOT the stub error.
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1" → p4AssertNotStub fires.
func TestWrkqAttachmentRemove_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	attachDir := t.TempDir()
	extraEnv := []string{"WRKQ_ATTACH_DIR=" + attachDir}

	rf := pdRunEnv(t, "wrkq", dbPath, extraEnv,
		mkRPC("r1", "wrkq.attachment.remove", map[string]any{"id": "00000000-0000-0000-0000-nonexistent00"}),
	)
	code := p2ErrCode(rf[1])
	if code != "WRKQ_NOT_FOUND" {
		t.Errorf("remove nonexistent attachment: want WRKQ_NOT_FOUND, got %q", code)
	}
	p4AssertNotStub(t, rf[1], "remove nonexistent attachment must be real domain error, not stub")
}

// ─── Group 5: relation.add/list/remove ──────────────────────────────────────

// TestWrkqRelationAdd_ValidKind_Succeeds creates two tasks and adds a relation.
// Expects result with fromTask, kind, toTask, direction fields; no "id" field.
//
// RED: stub returns WRKQ_VALIDATION → p2ResultOrFail fires.
func TestWrkqRelationAdd_ValidKind_Succeeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Relation Task A", "kind": "task"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "Relation Task B", "kind": "task"}),
	)
	crA := p2ResultOrFail(t, cf[1], "create task A")
	crB := p2ResultOrFail(t, cf[2], "create task B")
	taskA, _ := crA["id"].(string)
	taskB, _ := crB["id"].(string)
	if taskA == "" || taskB == "" {
		t.Fatal("create returned empty ids; cannot test relation.add")
	}

	rf := p2Run(t, dbPath,
		mkRPC("r1", "wrkq.relation.add", map[string]any{
			"fromTask": taskA,
			"kind":     "blocks",
			"toTask":   taskB,
		}),
	)
	result := p2ResultOrFail(t, rf[1], "wrkq.relation.add")

	p2AssertStr(t, result, "fromTask")
	p2AssertStr(t, result, "kind")
	p2AssertStr(t, result, "toTask")
	p2AssertStr(t, result, "direction")

	// No "id" field — relations are identified by composite key.
	p2AssertAbsent(t, result, "id")
}

// TestWrkqRelationAdd_InvalidKind_Fails creates two tasks and adds a relation
// with an invalid kind. Expects WRKQ_VALIDATION that is NOT the stub error.
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1" → p4AssertNotStub fires.
func TestWrkqRelationAdd_InvalidKind_Fails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Relation Bad Kind A", "kind": "task"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "Relation Bad Kind B", "kind": "task"}),
	)
	crA := p2ResultOrFail(t, cf[1], "create task A")
	crB := p2ResultOrFail(t, cf[2], "create task B")
	taskA, _ := crA["id"].(string)
	taskB, _ := crB["id"].(string)
	if taskA == "" || taskB == "" {
		t.Fatal("create returned empty ids; cannot test relation.add invalid kind")
	}

	rf := p2Run(t, dbPath,
		mkRPC("r1", "wrkq.relation.add", map[string]any{
			"fromTask": taskA,
			"kind":     "invalid_kind",
			"toTask":   taskB,
		}),
	)
	code := p2ErrCode(rf[1])
	if code != "WRKQ_VALIDATION" {
		t.Errorf("relation.add invalid kind: want WRKQ_VALIDATION, got %q", code)
	}
	p4AssertNotStub(t, rf[1], "relation.add invalid kind must be real domain error, not stub")
}

// TestWrkqRelationAdd_SelfRelation_Fails creates a task and adds a relation from
// it to itself. Expects WRKQ_VALIDATION that is NOT the stub error.
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1" → p4AssertNotStub fires.
func TestWrkqRelationAdd_SelfRelation_Fails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Self Relation Task", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "create task")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test self-relation")
	}

	rf := p2Run(t, dbPath,
		mkRPC("r1", "wrkq.relation.add", map[string]any{
			"fromTask": taskID,
			"kind":     "blocks",
			"toTask":   taskID,
		}),
	)
	code := p2ErrCode(rf[1])
	if code != "WRKQ_VALIDATION" {
		t.Errorf("relation.add self-relation: want WRKQ_VALIDATION, got %q", code)
	}
	p4AssertNotStub(t, rf[1], "relation.add self-relation must be real domain error, not stub")
}

// TestWrkqRelationAdd_NotFound_Fails calls relation.add with a nonexistent fromTask.
// Expects WRKQ_NOT_FOUND that is NOT the stub error.
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1" → p4AssertNotStub fires.
func TestWrkqRelationAdd_NotFound_Fails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Relation NotFound Target", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "create task")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test relation.add not found")
	}

	rf := p2Run(t, dbPath,
		mkRPC("r1", "wrkq.relation.add", map[string]any{
			"fromTask": "T-99999999",
			"kind":     "blocks",
			"toTask":   taskID,
		}),
	)
	code := p2ErrCode(rf[1])
	if code != "WRKQ_NOT_FOUND" {
		t.Errorf("relation.add with nonexistent fromTask: want WRKQ_NOT_FOUND, got %q", code)
	}
	p4AssertNotStub(t, rf[1], "relation.add not found must be real domain error, not stub")
}

// TestWrkqRelationAdd_Duplicate_Conflict creates two tasks, adds a relation,
// then adds the same relation again. Expects WRKQ_CONFLICT on second add.
//
// RED: stub returns WRKQ_VALIDATION on first add → p2ResultOrFail fires.
func TestWrkqRelationAdd_Duplicate_Conflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Dup Relation A", "kind": "task"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "Dup Relation B", "kind": "task"}),
	)
	crA := p2ResultOrFail(t, cf[1], "create task A")
	crB := p2ResultOrFail(t, cf[2], "create task B")
	taskA, _ := crA["id"].(string)
	taskB, _ := crB["id"].(string)
	if taskA == "" || taskB == "" {
		t.Fatal("create returned empty ids; cannot test duplicate relation")
	}

	// First add.
	rf1 := p2Run(t, dbPath,
		mkRPC("r1", "wrkq.relation.add", map[string]any{
			"fromTask": taskA,
			"kind":     "blocks",
			"toTask":   taskB,
		}),
	)
	p2ResultOrFail(t, rf1[1], "first wrkq.relation.add")

	// Second add — same composite key.
	rf2 := p2Run(t, dbPath,
		mkRPC("r2", "wrkq.relation.add", map[string]any{
			"fromTask": taskA,
			"kind":     "blocks",
			"toTask":   taskB,
		}),
	)
	code := p2ErrCode(rf2[1])
	if code != "WRKQ_CONFLICT" {
		t.Errorf("duplicate relation: want WRKQ_CONFLICT, got %q", code)
	}
	p4AssertNotStub(t, rf2[1], "duplicate relation must be real domain error, not stub")
}

// TestWrkqRelationList_IncludesDirectionField creates two tasks, adds a relation,
// then lists relations for the to-task. Expects items with a "direction" field.
//
// RED: stub returns WRKQ_VALIDATION on add → p2ResultOrFail fires.
func TestWrkqRelationList_IncludesDirectionField(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Rel List A", "kind": "task"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "Rel List B", "kind": "task"}),
	)
	crA := p2ResultOrFail(t, cf[1], "create task A")
	crB := p2ResultOrFail(t, cf[2], "create task B")
	taskA, _ := crA["id"].(string)
	taskB, _ := crB["id"].(string)
	if taskA == "" || taskB == "" {
		t.Fatal("create returned empty ids; cannot test relation.list direction field")
	}

	p2Run(t, dbPath,
		mkRPC("r1", "wrkq.relation.add", map[string]any{
			"fromTask": taskA,
			"kind":     "blocks",
			"toTask":   taskB,
		}),
	)

	lf := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.relation.list", map[string]any{"task": taskB}),
	)
	result := p2ResultOrFail(t, lf[1], "wrkq.relation.list")
	p2AssertHasItems(t, result, "wrkq.relation.list")

	items, _ := result["items"].([]any)
	if len(items) < 1 {
		t.Errorf("wrkq.relation.list: expected at least 1 item, got %d", len(items))
		return
	}
	for _, item := range items {
		m, _ := item.(map[string]any)
		if _, ok := m["direction"]; !ok {
			t.Errorf("wrkq.relation.list item missing \"direction\" field: %#v", m)
		}
	}
}

// TestWrkqRelationRemove_ByCompositeKey creates two tasks, adds a relation, then
// removes it by composite key. Expects remove to succeed without an "id" param.
//
// RED: stub returns WRKQ_VALIDATION on add → p2ResultOrFail fires.
func TestWrkqRelationRemove_ByCompositeKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Rel Remove A", "kind": "task"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "Rel Remove B", "kind": "task"}),
	)
	crA := p2ResultOrFail(t, cf[1], "create task A")
	crB := p2ResultOrFail(t, cf[2], "create task B")
	taskA, _ := crA["id"].(string)
	taskB, _ := crB["id"].(string)
	if taskA == "" || taskB == "" {
		t.Fatal("create returned empty ids; cannot test relation.remove")
	}

	p2Run(t, dbPath,
		mkRPC("r1", "wrkq.relation.add", map[string]any{
			"fromTask": taskA,
			"kind":     "blocks",
			"toTask":   taskB,
		}),
	)

	rmf := p2Run(t, dbPath,
		mkRPC("rm1", "wrkq.relation.remove", map[string]any{
			"fromTask": taskA,
			"kind":     "blocks",
			"toTask":   taskB,
		}),
	)
	p2ResultOrFail(t, rmf[1], "wrkq.relation.remove by composite key")
}

// ─── Group 6: contract hygiene ───────────────────────────────────────────────

// TestWrkqDeferredMethods_NoneReturnNotImplemented verifies that none of the 14
// deferred methods return the stub error "not implemented in P1".
//
// RED: all 14 methods return the stub error → this test reports 14 failures.
func TestWrkqDeferredMethods_NoneReturnNotImplemented(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	attachDir := t.TempDir()

	// Seed minimal data for methods that require it.
	taskUUID := "f0000008-0000-4000-8000-000000000001"
	taskID := p2SeedTask(t, dbPath, taskUUID, "contract-test-task", "Contract Hygiene Task")

	// Probe each deferred method with minimal params in one session.
	type probe struct {
		id     string
		method string
		params map[string]any
	}
	probes := []probe{
		{"p01", "wrkq.task.delete", map[string]any{"task": taskID}},
		{"p02", "wrkq.task.restore", map[string]any{"task": taskID}},
		{"p03", "wrkq.task.acknowledge", map[string]any{"task": taskID, "force": true}},
		{"p04", "wrkq.attachment.add", map[string]any{"task": taskID, "path": "/tmp/x", "filename": "x.txt"}},
		{"p05", "wrkq.attachment.list", map[string]any{"task": taskID}},
		{"p06", "wrkq.attachment.remove", map[string]any{"id": "00000000-0000-0000-0000-000000000000"}},
		{"p07", "wrkq.relation.add", map[string]any{"fromTask": taskID, "kind": "blocks", "toTask": "T-99999999"}},
		{"p08", "wrkq.relation.list", map[string]any{"task": taskID}},
		{"p09", "wrkq.relation.remove", map[string]any{"fromTask": taskID, "kind": "blocks", "toTask": "T-99999999"}},
		{"p10", "wrkq.container.show", map[string]any{"path": "nonexistent"}},
		{"p11", "wrkq.container.list", map[string]any{}},
	}

	reqs := make([]string, len(probes))
	for i, p := range probes {
		reqs[i] = mkRPC(p.id, p.method, p.params)
	}

	extraEnv := []string{"WRKQ_ATTACH_DIR=" + attachDir}

	seq := []string{
		mkRPC("_init", "rpc.initialize", map[string]any{
			"protocolVersion": "2026-06-14",
			"client":          map[string]any{"name": "p4-contract", "version": "0.0.1"},
		}),
	}
	seq = append(seq, reqs...)
	seq = append(seq,
		mkRPC("_sd", "rpc.shutdown", map[string]any{}),
		mkRPC("", "rpc.exit", nil),
	)
	frames := runRPCWithEnv(t, "wrkq", dbPath, seq, extraEnv)
	// frames: init(0) + N probes + shutdown
	want := 2 + len(probes)
	if len(frames) != want {
		t.Fatalf("contract hygiene: expected %d frames, got %d", want, len(frames))
	}

	for i, p := range probes {
		frame := frames[1+i] // offset by init frame
		if p4IsStubError(frame) {
			t.Errorf("method %s returned stub error 'not implemented in P1'; must be implemented", p.method)
		}
	}
}

// TestWrkqRelationDTO_NoIdField creates two tasks, adds a relation, and asserts
// that the WrkqRelation result has NO "id" field.
//
// RED: stub returns WRKQ_VALIDATION → p2ResultOrFail fires.
func TestWrkqRelationDTO_NoIdField(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Rel DTO A", "kind": "task"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "Rel DTO B", "kind": "task"}),
	)
	crA := p2ResultOrFail(t, cf[1], "create task A")
	crB := p2ResultOrFail(t, cf[2], "create task B")
	taskA, _ := crA["id"].(string)
	taskB, _ := crB["id"].(string)
	if taskA == "" || taskB == "" {
		t.Fatal("create returned empty ids; cannot test relation DTO no-id contract")
	}

	rf := p2Run(t, dbPath,
		mkRPC("r1", "wrkq.relation.add", map[string]any{
			"fromTask": taskA,
			"kind":     "blocks",
			"toTask":   taskB,
		}),
	)
	result := p2ResultOrFail(t, rf[1], "wrkq.relation.add")

	// WrkqRelation must NOT have an "id" field.
	p2AssertAbsent(t, result, "id")
}

// ─── Group 7: container.show/list ───────────────────────────────────────────

// TestWrkqContainerShow_NotFound calls container.show with a nonexistent path.
// Expects WRKQ_NOT_FOUND that is NOT the stub error.
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1" → p4AssertNotStub fires.
func TestWrkqContainerShow_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("cs1", "wrkq.container.show", map[string]any{"path": "nonexistent/path/xyz"}),
	)
	code := p2ErrCode(cf[1])
	if code != "WRKQ_NOT_FOUND" {
		t.Errorf("container.show nonexistent: want WRKQ_NOT_FOUND, got %q", code)
	}
	p4AssertNotStub(t, cf[1], "container.show nonexistent must be real domain error, not stub")
}

// TestWrkqContainerShow_CamelCaseDTO_IncludesPath seeds a project container via
// p2SeedTask (which inserts slug="p2-test-proj") and shows it. Asserts camelCase
// DTO fields and no DB column leaks.
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1" → p2ResultOrFail fires.
func TestWrkqContainerShow_CamelCaseDTO_IncludesPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// p2SeedTask seeds a project container with slug="p2-test-proj".
	p2SeedTask(t, dbPath,
		"f0000009-0000-4000-8000-000000000001",
		"p4-container-show", "Container Show Task")

	cf := p2Run(t, dbPath,
		mkRPC("cs1", "wrkq.container.show", map[string]any{"path": "p2-test-proj"}),
	)
	result := p2ResultOrFail(t, cf[1], "wrkq.container.show known project")

	// Required camelCase fields.
	p2AssertStr(t, result, "uuid")
	p2AssertStr(t, result, "slug")
	p2AssertStr(t, result, "title")
	p2AssertStr(t, result, "kind")
	p2AssertStr(t, result, "path")

	// DB column leaks forbidden.
	p2AssertAbsent(t, result, "project_uuid")
	p2AssertAbsent(t, result, "parent_uuid")
	p2AssertAbsent(t, result, "created_at")
	p2AssertAbsent(t, result, "updated_at")
}

// TestWrkqContainerList_ReturnsItems calls container.list and asserts a non-empty
// items array (the root and at least one project container should exist after seeding).
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1" → p2ResultOrFail fires.
func TestWrkqContainerList_ReturnsItems(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Seed at least one container.
	p2SeedTask(t, dbPath,
		"f000000a-0000-4000-8000-000000000001",
		"p4-container-list", "Container List Task")

	cf := p2Run(t, dbPath,
		mkRPC("cl1", "wrkq.container.list", map[string]any{}),
	)
	result := p2ResultOrFail(t, cf[1], "wrkq.container.list")
	p2AssertHasItems(t, result, "wrkq.container.list")

	items, _ := result["items"].([]any)
	if len(items) < 1 {
		t.Errorf("wrkq.container.list: expected at least 1 item, got %d", len(items))
	}
}
