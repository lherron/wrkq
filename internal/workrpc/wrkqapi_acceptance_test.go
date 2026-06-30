package workrpc_test

// wrkqapi_acceptance_test.go — RED acceptance tests for P2: internal/wrkqapi.
//
// These tests define the behavioral contract that wrkq.* RPC methods must satisfy
// once internal/wrkqapi is implemented (P2 of T-04424).
//
// They are expected to FAIL in their current state because:
//   - wrkq.task.create   returns a stub with empty id and missing DTO fields
//   - wrkq.task.show     returns WRKQ_VALIDATION "not implemented in P1"
//   - wrkq.task.list     returns WRKQ_VALIDATION "not implemented in P1"
//   - wrkq.task.update   returns WRKQ_VALIDATION "not implemented in P1"
//   - wrkq.comment.add   returns WRKQ_VALIDATION "not implemented in P1"
//   - wrkq.comment.list  returns WRKQ_VALIDATION "not implemented in P1"
//   - wrkq.workflow.attach  returns map[string]any (not named DTO), no idempotency
//
// Covered behaviors (per §6.2, §8.2, §9.1-§9.2, §13 of docs/wrkq-wrkf-rpc.md):
//   - WrkqTask DTO: camelCase fields, no DB column leaks (project_uuid etc.)
//   - wrkq.task.create: persists + idempotency replay + WRKQ_CONFLICT on hash mismatch
//   - wrkq.task.show: WRKQ_NOT_FOUND for unknown; returns WrkqTask for known
//   - wrkq.task.list: returns paginated items; state/kind/path filters; cursor/limit
//   - wrkq.task.update: patch applies; expectEtag CAS → WRKQ_CONFLICT with currentEtag
//   - wrkq.comment.add: persists + WrkqComment DTO
//   - wrkq.comment.list: returns WrkqCommentListResult with items
//   - wrkq.workflow.attach: WrkqWorkflowAttachResult{task,instance,attached=true on new};
//                           idempotency replay → attached=false, same instance id
//   - wrkq.workflow.inspect/timeline: task-scoped accessors return typed DTOs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// ─── helpers ────────────────────────────────────────────────────────────────

// mkRPC encodes a JSON-RPC 2.0 request line.
// Pass id="" for notifications (no id field → no response frame).
func mkRPC(id, method string, params any) string {
	req := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if id != "" {
		req["id"] = id
	}
	b, _ := json.Marshal(req)
	return string(b)
}

// p2Run runs a wrkq RPC session with the given business requests sandwiched
// between rpc.initialize and rpc.shutdown + rpc.exit.
// Returns all response frames (init + business requests + shutdown; exit has no frame).
func p2Run(t *testing.T, dbPath string, reqs ...string) []map[string]any {
	t.Helper()
	seq := []string{
		mkRPC("_init", "rpc.initialize", map[string]any{
			"protocolVersion": "2026-06-30",
			"client":          map[string]any{"name": "p2-smokey", "version": "0.0.1"},
		}),
	}
	seq = append(seq, reqs...)
	seq = append(seq,
		mkRPC("_sd", "rpc.shutdown", map[string]any{}),
		mkRPC("", "rpc.exit", nil), // notification → no frame
	)
	frames := runRPC(t, "wrkq", dbPath, seq)
	// expected frame count: 1 (init) + len(reqs) + 1 (shutdown)
	want := 2 + len(reqs)
	if len(frames) != want {
		t.Fatalf("p2Run: expected %d frames, got %d\nframes: %#v", want, len(frames), frames)
	}
	return frames
}

// p2ResultOrFail returns the result map from a frame, failing the test if the
// frame carries an error.
func p2ResultOrFail(t *testing.T, frame map[string]any, label string) map[string]any {
	t.Helper()
	if errObj, ok := frame["error"]; ok {
		t.Fatalf("%s: expected result, got error: %v", label, errObj)
	}
	return resultMap(t, frame)
}

// p2ErrCode extracts error.data.code from a response frame.
func p2ErrCode(frame map[string]any) string {
	errObj, _ := frame["error"].(map[string]any)
	data, _ := errObj["data"].(map[string]any)
	code, _ := data["code"].(string)
	return code
}

// p2ErrDataField extracts a field from error.data.
func p2ErrDataField(frame map[string]any, field string) any {
	errObj, _ := frame["error"].(map[string]any)
	data, _ := errObj["data"].(map[string]any)
	return data[field]
}

// p2AssertStr fails if result[key] is missing or not a non-empty string.
func p2AssertStr(t *testing.T, result map[string]any, key string) {
	t.Helper()
	v, ok := result[key]
	if !ok {
		t.Errorf("DTO missing required field %q", key)
		return
	}
	s, ok := v.(string)
	if !ok || s == "" {
		t.Errorf("DTO field %q must be a non-empty string, got: %T=%v", key, v, v)
	}
}

// p2AssertFieldEq fails if result[key] != want.
func p2AssertFieldEq(t *testing.T, result map[string]any, key string, want any) {
	t.Helper()
	got := result[key]
	// JSON numbers come back as float64; compare canonically.
	if got != want {
		t.Errorf("DTO field %q: want %v, got %v", key, want, got)
	}
}

// p2AssertAbsent fails if result has the given key (DB column leak check).
func p2AssertAbsent(t *testing.T, result map[string]any, key string) {
	t.Helper()
	if _, ok := result[key]; ok {
		t.Errorf("DTO must not expose DB column name %q", key)
	}
}

// p2AssertEtag fails if result["etag"] is missing or not a positive number.
func p2AssertEtag(t *testing.T, result map[string]any) {
	t.Helper()
	v, ok := result["etag"]
	if !ok {
		t.Errorf("DTO missing required field \"etag\"")
		return
	}
	n, ok := v.(float64)
	if !ok || n <= 0 {
		t.Errorf("DTO field \"etag\" must be a positive number, got: %T=%v", v, v)
	}
}

// p2AssertHasItems fails if result["items"] is missing or not a non-nil array.
func p2AssertHasItems(t *testing.T, result map[string]any, label string) {
	t.Helper()
	v, ok := result["items"]
	if !ok {
		t.Errorf("%s: result missing \"items\" field", label)
		return
	}
	if v == nil {
		t.Errorf("%s: \"items\" must not be null", label)
	}
}

func p2TaskItemByID(t *testing.T, result map[string]any, id string) map[string]any {
	t.Helper()
	items, _ := result["items"].([]any)
	for _, item := range items {
		m, _ := item.(map[string]any)
		if m["id"] == id {
			return m
		}
	}
	t.Fatalf("task.list result did not include %s; items=%#v", id, items)
	return nil
}

// mapKeys returns the keys of a map as a slice (for error messages).
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// p2WorkflowTemplatePath returns the path to the canonical wrkq-code-change template.
func p2WorkflowTemplatePath(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	path := filepath.Join(root, "wrkf", "templates", "wrkq-code-change.workflow.json")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("workflow template not found at %s: %v", path, err)
	}
	return path
}

// p2SeedTask inserts a project container + task directly into dbPath using SQL.
// Returns the task's wrkq ID (e.g. "T-00001").
// This is used for workflow tests that need a real persisted task before the
// wrkq.task.create RPC is implemented (so they don't depend on the P2 stub).
func p2SeedTask(t *testing.T, dbPath, taskUUID, slug, title string) string {
	t.Helper()

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("p2SeedTask: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	// Seed actor (use a fixed UUID so multiple calls don't conflict).
	actorUUID := "00000000-0000-4000-8000-0000000000a0" // wrkq-system actor from migration

	// Seed a project container.
	projUUID := "00000000-1111-4000-8000-000000000001"
	_, _ = database.Exec(
		`INSERT OR IGNORE INTO containers (uuid, slug, title, parent_uuid, kind,
		                                   created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'p2-test-proj', 'P2 Test Project',
		         (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		projUUID, actorUUID, actorUUID,
	)

	// Insert the task.
	_, err = database.Exec(
		`INSERT OR IGNORE INTO tasks (uuid, slug, title, description, project_uuid, state, priority, kind,
		                              created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, ?, ?, '', ?, 'open', 2, 'task', ?, ?)`,
		taskUUID, slug, title, projUUID, actorUUID, actorUUID,
	)
	if err != nil {
		t.Fatalf("p2SeedTask: INSERT task: %v", err)
	}

	// Fetch the auto-assigned T-XXXXX id.
	var taskID string
	if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", taskUUID).Scan(&taskID); err != nil {
		t.Fatalf("p2SeedTask: fetch task id: %v", err)
	}
	return taskID
}

// ─── WrkqTask DTO shape ──────────────────────────────────────────────────────

// TestWrkqTaskCreate_ReturnsNamedDTO verifies that wrkq.task.create returns a
// WrkqTask DTO with all required camelCase fields and no DB column name leaks.
//
// RED: currently the stub returns {"id":"","title":"...","state":"open",...}
// missing uuid, slug, projectUuid, etag, createdAt, updatedAt.
func TestWrkqTaskCreate_ReturnsNamedDTO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{
			"title": "Named DTO Test",
			"kind":  "task",
		}),
	)
	// frames[0]=init, frames[1]=create, frames[2]=shutdown
	result := p2ResultOrFail(t, frames[1], "wrkq.task.create")

	// Required camelCase fields per WrkqTask spec (§6.2 + §8.4).
	p2AssertStr(t, result, "uuid")
	p2AssertStr(t, result, "id")
	p2AssertStr(t, result, "slug")
	p2AssertStr(t, result, "title")
	p2AssertStr(t, result, "projectUuid") // must be camelCase, NOT project_uuid
	p2AssertStr(t, result, "path")
	p2AssertStr(t, result, "state")
	p2AssertStr(t, result, "kind")
	p2AssertStr(t, result, "createdAt")
	p2AssertStr(t, result, "updatedAt")
	p2AssertEtag(t, result)

	// DB column name leaks forbidden (§8.4).
	p2AssertAbsent(t, result, "project_uuid")
	p2AssertAbsent(t, result, "created_at")
	p2AssertAbsent(t, result, "updated_at")
	p2AssertAbsent(t, result, "created_by_scope_ref")
	p2AssertAbsent(t, result, "updated_by_principal_ref")
}

// TestWrkqTaskCreate_TitleRequired verifies that missing title → WRKQ_VALIDATION.
// This test should pass even with the current stub.
func TestWrkqTaskCreate_TitleRequired(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{}),
	)
	code := p2ErrCode(frames[1])
	if code != "WRKQ_VALIDATION" {
		t.Errorf("missing title: want WRKQ_VALIDATION, got %q", code)
	}
}

// ─── Persistence ────────────────────────────────────────────────────────────

// TestWrkqTaskCreate_Persists verifies that a created task can be retrieved
// with wrkq.task.show in a subsequent call.
//
// RED: wrkq.task.show returns WRKQ_VALIDATION "not implemented in P1".
func TestWrkqTaskCreate_Persists(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Session 1: create the task.
	createFrames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{
			"title": "Persist Test Task",
			"kind":  "task",
			"state": "open",
		}),
	)
	createResult := p2ResultOrFail(t, createFrames[1], "wrkq.task.create must succeed")
	taskID, _ := createResult["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test persistence")
	}

	// Session 2: show the task.
	showFrames := p2Run(t, dbPath,
		mkRPC("s1", "wrkq.task.show", map[string]any{"task": taskID}),
	)
	showResult := p2ResultOrFail(t, showFrames[1], "wrkq.task.show must return the created task")
	p2AssertStr(t, showResult, "uuid")
	p2AssertFieldEq(t, showResult, "title", "Persist Test Task")
}

func TestWrkqTaskRiskClass_CreateUpdateShowListAndValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{
			"title":     "Risk Class Task",
			"kind":      "task",
			"riskClass": "medium",
		}),
		mkRPC("badCreate", "wrkq.task.create", map[string]any{
			"title":     "Bad Risk Class Task",
			"kind":      "task",
			"riskClass": "critical",
		}),
	)
	created := p2ResultOrFail(t, frames[1], "wrkq.task.create with riskClass")
	if got, _ := created["riskClass"].(string); got != "medium" {
		t.Fatalf("create riskClass: want medium, got %q", got)
	}
	if code := p2ErrCode(frames[2]); code != "WRKQ_VALIDATION" {
		t.Fatalf("invalid create riskClass: want WRKQ_VALIDATION, got %q", code)
	}
	taskID, _ := created["id"].(string)
	etag, _ := created["etag"].(float64)

	frames = p2Run(t, dbPath,
		mkRPC("u1", "wrkq.task.update", map[string]any{
			"task":       taskID,
			"patch":      map[string]any{"riskClass": "high"},
			"expectEtag": etag,
		}),
		mkRPC("s1", "wrkq.task.show", map[string]any{"task": taskID}),
		mkRPC("l1", "wrkq.task.list", map[string]any{"state": "open"}),
		mkRPC("badUpdate", "wrkq.task.update", map[string]any{
			"task":  taskID,
			"patch": map[string]any{"riskClass": "critical"},
		}),
		mkRPC("badLegacy", "wrkq.task.update", map[string]any{
			"task":  taskID,
			"patch": map[string]any{"phase": "done", "workflowPreset": "legacy", "presetVersion": "1"},
		}),
	)
	updated := p2ResultOrFail(t, frames[1], "wrkq.task.update riskClass")
	if got, _ := updated["riskClass"].(string); got != "high" {
		t.Fatalf("update riskClass: want high, got %q", got)
	}
	shown := p2ResultOrFail(t, frames[2], "wrkq.task.show riskClass")
	if got, _ := shown["riskClass"].(string); got != "high" {
		t.Fatalf("show riskClass: want high, got %q", got)
	}
	listed := p2ResultOrFail(t, frames[3], "wrkq.task.list riskClass")
	items, _ := listed["items"].([]any)
	var found bool
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if item["id"] == taskID && item["riskClass"] == "high" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("task.list did not include updated riskClass for %s: %#v", taskID, items)
	}
	if code := p2ErrCode(frames[4]); code != "WRKQ_VALIDATION" {
		t.Fatalf("invalid update riskClass: want WRKQ_VALIDATION, got %q", code)
	}
	if code := p2ErrCode(frames[5]); code != "WRKQ_VALIDATION" {
		t.Fatalf("legacy phase/preset patch must reject: want WRKQ_VALIDATION, got %q", code)
	}
}

// ─── Idempotency ────────────────────────────────────────────────────────────

// TestWrkqTaskCreate_Idempotency_Replay verifies that calling wrkq.task.create
// twice with the same idempotencyKey and identical params returns the SAME task
// (no duplicate created).
//
// RED: the stub ignores idempotencyKey; each call returns an independent stub
// result (currently both have id="" but that's the wrong reason — once the stub
// is fixed the real check is same id + no extra row).
func TestWrkqTaskCreate_Idempotency_Replay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	params := map[string]any{
		"title":          "Idempotent Create",
		"kind":           "task",
		"idempotencyKey": "smokey:idem:create:replay-001",
	}
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", params),
		mkRPC("c2", "wrkq.task.create", params), // identical params + same key
	)
	// frames: init(0), create1(1), create2(2), shutdown(3)
	r1 := p2ResultOrFail(t, frames[1], "first create")
	r2 := p2ResultOrFail(t, frames[2], "second create (replay)")

	id1, _ := r1["id"].(string)
	id2, _ := r2["id"].(string)

	if id1 == "" {
		t.Error("first create returned empty id")
	}
	if id1 != id2 {
		t.Errorf("idempotency replay: expected same task id; first=%q second=%q", id1, id2)
	}
}

// TestWrkqTaskCreate_Idempotency_Mismatch verifies that the same idempotencyKey
// with different request content returns WRKQ_CONFLICT (§8.2).
//
// RED: stub ignores idempotencyKey; second call succeeds instead of conflicting.
func TestWrkqTaskCreate_Idempotency_Mismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	const key = "smokey:idem:create:mismatch-001"
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{
			"title":          "Original Title",
			"kind":           "task",
			"idempotencyKey": key,
		}),
		mkRPC("c2", "wrkq.task.create", map[string]any{
			"title":          "Different Title", // same key, different canonical hash
			"kind":           "task",
			"idempotencyKey": key,
		}),
	)
	// First create should succeed.
	p2ResultOrFail(t, frames[1], "first create")

	// Second create with same key but different title must return WRKQ_CONFLICT.
	code := p2ErrCode(frames[2])
	if code != "WRKQ_CONFLICT" {
		t.Errorf("idempotency mismatch: want WRKQ_CONFLICT, got error.data.code=%q", code)
	}
}

// ─── wrkq.task.show ─────────────────────────────────────────────────────────

// TestWrkqTaskShow_NotFound verifies that showing an unknown task returns
// WRKQ_NOT_FOUND (not the P1 stub WRKQ_VALIDATION).
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1".
func TestWrkqTaskShow_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("s1", "wrkq.task.show", map[string]any{"task": "T-99999999"}),
	)
	code := p2ErrCode(frames[1])
	if code != "WRKQ_NOT_FOUND" {
		t.Errorf("unknown task show: want WRKQ_NOT_FOUND, got %q", code)
	}
}

// TestWrkqTaskShow_ReturnsWrkqTask verifies that wrkq.task.show returns a full
// WrkqTask DTO for a task that exists.
//
// RED: wrkq.task.show returns WRKQ_VALIDATION "not implemented in P1".
func TestWrkqTaskShow_ReturnsWrkqTask(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Create via RPC first.
	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Show Test", "kind": "bug"}),
	)
	cr := p2ResultOrFail(t, cf[1], "create")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test show")
	}

	// Now show the task.
	sf := p2Run(t, dbPath,
		mkRPC("s1", "wrkq.task.show", map[string]any{"task": taskID}),
	)
	result := p2ResultOrFail(t, sf[1], "wrkq.task.show must return WrkqTask")

	p2AssertStr(t, result, "uuid")
	p2AssertStr(t, result, "id")
	p2AssertStr(t, result, "slug")
	p2AssertStr(t, result, "projectUuid")
	p2AssertStr(t, result, "path")
	p2AssertFieldEq(t, result, "title", "Show Test")
	p2AssertStr(t, result, "kind")
	p2AssertStr(t, result, "state")
	p2AssertEtag(t, result)
	p2AssertStr(t, result, "createdAt")
	p2AssertStr(t, result, "updatedAt")

	// No DB column name leaks.
	p2AssertAbsent(t, result, "project_uuid")
	p2AssertAbsent(t, result, "created_at")
}

// ─── wrkq.task.list ─────────────────────────────────────────────────────────

// TestWrkqTaskPath_CreateShowListConsistent verifies that wrkq RPC, not a
// downstream adapter, owns the canonical task path exposed on WrkqTask.
func TestWrkqTaskPath_CreateShowListConsistent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{
			"title": "Canonical Path Test",
			"kind":  "task",
			"state": "open",
		}),
	)
	created := p2ResultOrFail(t, cf[1], "create")
	taskID, _ := created["id"].(string)
	createPath, _ := created["path"].(string)
	if taskID == "" || createPath == "" {
		t.Fatalf("create returned id/path = %q/%q", taskID, createPath)
	}

	frames := p2Run(t, dbPath,
		mkRPC("s1", "wrkq.task.show", map[string]any{"task": taskID}),
		mkRPC("l1", "wrkq.task.list", map[string]any{"state": "open", "limit": 100}),
	)
	shown := p2ResultOrFail(t, frames[1], "show")
	if shown["path"] != createPath {
		t.Fatalf("show path mismatch: create=%q show=%q", createPath, shown["path"])
	}

	listed := p2ResultOrFail(t, frames[2], "list")
	items, _ := listed["items"].([]any)
	for _, item := range items {
		m, _ := item.(map[string]any)
		if m["id"] == taskID {
			if m["path"] != createPath {
				t.Fatalf("list path mismatch: create=%q list=%q", createPath, m["path"])
			}
			return
		}
	}
	t.Fatalf("task.list did not include created task %s; items=%#v", taskID, items)
}

// TestWrkqTaskList_ReturnsItemsArray verifies that wrkq.task.list returns a
// result with an "items" array (not an error).
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1".
func TestWrkqTaskList_ReturnsItemsArray(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{}),
	)
	result := p2ResultOrFail(t, frames[1], "wrkq.task.list must return a result")
	p2AssertHasItems(t, result, "wrkq.task.list")
}

// TestWrkqTaskList_StateFilter verifies that wrkq.task.list applies state
// filtering and returns only matching tasks.
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1".
func TestWrkqTaskList_StateFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Create one open and one in_progress task.
	p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Open Task", "state": "open"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "In-Progress Task", "state": "in_progress"}),
	)

	// List only open tasks.
	frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{"state": "open"}),
	)
	result := p2ResultOrFail(t, frames[1], "wrkq.task.list with state filter")
	items, _ := result["items"].([]any)
	for _, item := range items {
		m, _ := item.(map[string]any)
		st, _ := m["state"].(string)
		if st != "open" {
			t.Errorf("wrkq.task.list with state=open returned item with state=%q", st)
		}
	}
}

// TestWrkqTaskList_KindFilter verifies that wrkq.task.list applies kind filtering.
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1".
func TestWrkqTaskList_KindFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "A Bug", "kind": "bug"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "A Task", "kind": "task"}),
	)

	frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{"kind": "bug"}),
	)
	result := p2ResultOrFail(t, frames[1], "wrkq.task.list with kind filter")
	items, _ := result["items"].([]any)
	for _, item := range items {
		m, _ := item.(map[string]any)
		k, _ := m["kind"].(string)
		if k != "bug" {
			t.Errorf("wrkq.task.list with kind=bug returned item with kind=%q", k)
		}
	}
}

// TestWrkqTaskList_CursorPagination verifies that limit+cursor pagination works.
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1".
func TestWrkqTaskList_CursorPagination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Create 3 tasks.
	p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Page Task 1"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "Page Task 2"}),
		mkRPC("c3", "wrkq.task.create", map[string]any{"title": "Page Task 3"}),
	)

	// Fetch first page (limit=2).
	p1Frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{"limit": 2}),
	)
	page1 := p2ResultOrFail(t, p1Frames[1], "list page 1")
	items1, _ := page1["items"].([]any)
	if len(items1) > 2 {
		t.Errorf("limit=2: expected at most 2 items, got %d", len(items1))
	}
	nextCursor, _ := page1["nextCursor"].(string)
	if nextCursor == "" {
		// If we created 3 tasks and got only 2, nextCursor must be set.
		if len(items1) == 2 {
			t.Error("list page 1 with limit=2 and 3 existing tasks: nextCursor must be set")
		}
		return // only 2 or fewer items total; no cursor needed
	}

	// Fetch second page using cursor.
	p2Frames := p2Run(t, dbPath,
		mkRPC("l2", "wrkq.task.list", map[string]any{"cursor": nextCursor, "limit": 2}),
	)
	p2ResultOrFail(t, p2Frames[1], "list page 2 via cursor")
}

func TestWrkqTaskList_SummaryProjectionOmitsBodiesReportsPresence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	created := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{
			"title":         "Summary Bodies",
			"description":   "body text",
			"specification": "spec text",
			"state":         "open",
		}),
		mkRPC("c2", "wrkq.task.create", map[string]any{
			"title": "Summary Empty",
			"state": "open",
		}),
		mkRPC("c3", "wrkq.task.create", map[string]any{
			"title":         "Summary Whitespace",
			"description":   " \n\t ",
			"specification": "   ",
			"state":         "open",
		}),
	)
	bodyID, _ := p2ResultOrFail(t, created[1], "create body")["id"].(string)
	emptyID, _ := p2ResultOrFail(t, created[2], "create empty")["id"].(string)
	whiteID, _ := p2ResultOrFail(t, created[3], "create whitespace")["id"].(string)

	frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{"state": "open", "limit": 100, "summary": true}),
	)
	result := p2ResultOrFail(t, frames[1], "summary task.list")

	body := p2TaskItemByID(t, result, bodyID)
	if body["description"] != "" || body["specification"] != "" {
		t.Fatalf("summary=true must omit bodies for non-empty task, got description=%q specification=%q", body["description"], body["specification"])
	}
	if body["hasDescription"] != true || body["hasSpecification"] != true {
		t.Fatalf("summary=true non-empty task presence: want true/true, got hasDescription=%v hasSpecification=%v", body["hasDescription"], body["hasSpecification"])
	}

	empty := p2TaskItemByID(t, result, emptyID)
	if empty["description"] != "" || empty["specification"] != "" {
		t.Fatalf("summary=true must omit bodies for empty task, got description=%q specification=%q", empty["description"], empty["specification"])
	}
	if empty["hasDescription"] != false || empty["hasSpecification"] != false {
		t.Fatalf("summary=true empty task presence: want false/false, got hasDescription=%v hasSpecification=%v", empty["hasDescription"], empty["hasSpecification"])
	}

	white := p2TaskItemByID(t, result, whiteID)
	if white["description"] != "" || white["specification"] != "" {
		t.Fatalf("summary=true must omit bodies for whitespace task, got description=%q specification=%q", white["description"], white["specification"])
	}
	if white["hasDescription"] != false || white["hasSpecification"] != false {
		t.Fatalf("summary=true whitespace task presence must be trim-aware false/false, got hasDescription=%v hasSpecification=%v", white["hasDescription"], white["hasSpecification"])
	}
}

func TestWrkqTaskList_DefaultProjectionKeepsBodiesAndPresence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	created := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{
			"title":         "Full Bodies",
			"description":   "visible body",
			"specification": "visible spec",
			"state":         "open",
		}),
	)
	taskID, _ := p2ResultOrFail(t, created[1], "create full body")["id"].(string)

	frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{"state": "open", "limit": 100}),
	)
	item := p2TaskItemByID(t, p2ResultOrFail(t, frames[1], "default task.list"), taskID)
	if item["description"] != "visible body" || item["specification"] != "visible spec" {
		t.Fatalf("default task.list must keep full bodies, got description=%q specification=%q", item["description"], item["specification"])
	}
	if item["hasDescription"] != true || item["hasSpecification"] != true {
		t.Fatalf("default task.list presence: want true/true, got hasDescription=%v hasSpecification=%v", item["hasDescription"], item["hasSpecification"])
	}
}

func TestWrkqTaskLoadBackedPresenceBooleans(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	createdFrames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{
			"title":         "Load Presence",
			"specification": "initial spec",
		}),
	)
	created := p2ResultOrFail(t, createdFrames[1], "create load presence")
	taskID, _ := created["id"].(string)
	if created["hasDescription"] != false || created["hasSpecification"] != true {
		t.Fatalf("create DTO presence: want false/true, got hasDescription=%v hasSpecification=%v", created["hasDescription"], created["hasSpecification"])
	}

	updatedFrames := p2Run(t, dbPath,
		mkRPC("u1", "wrkq.task.update", map[string]any{
			"task": taskID,
			"patch": map[string]any{
				"description":   "now described",
				"specification": "   ",
			},
		}),
		mkRPC("s1", "wrkq.task.show", map[string]any{"task": taskID}),
	)
	updated := p2ResultOrFail(t, updatedFrames[1], "update load presence")
	if updated["description"] != "now described" || updated["specification"] != "   " {
		t.Fatalf("update DTO must keep full bodies, got description=%q specification=%q", updated["description"], updated["specification"])
	}
	if updated["hasDescription"] != true || updated["hasSpecification"] != false {
		t.Fatalf("update DTO presence must be trim-aware, got hasDescription=%v hasSpecification=%v", updated["hasDescription"], updated["hasSpecification"])
	}

	shown := p2ResultOrFail(t, updatedFrames[2], "show load presence")
	if shown["hasDescription"] != true || shown["hasSpecification"] != false {
		t.Fatalf("show DTO presence must match update DTO, got hasDescription=%v hasSpecification=%v", shown["hasDescription"], shown["hasSpecification"])
	}
}

func TestWrkqTaskList_SummaryPagination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Summary Page 1", "description": "d1", "specification": "s1"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "Summary Page 2", "description": "d2", "specification": "s2"}),
		mkRPC("c3", "wrkq.task.create", map[string]any{"title": "Summary Page 3", "description": "d3", "specification": "s3"}),
	)

	page1Frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{"limit": 2, "summary": true}),
	)
	page1 := p2ResultOrFail(t, page1Frames[1], "summary list page 1")
	items1, _ := page1["items"].([]any)
	if len(items1) != 2 {
		t.Fatalf("summary page 1: want 2 items, got %d", len(items1))
	}
	for _, raw := range items1 {
		item, _ := raw.(map[string]any)
		if item["description"] != "" || item["specification"] != "" {
			t.Fatalf("summary page 1 item must omit bodies: %#v", item)
		}
	}
	nextCursor, _ := page1["nextCursor"].(string)
	if nextCursor == "" {
		t.Fatal("summary page 1 with three tasks and limit=2 must return nextCursor")
	}

	page2Frames := p2Run(t, dbPath,
		mkRPC("l2", "wrkq.task.list", map[string]any{"limit": 2, "cursor": nextCursor, "summary": true}),
	)
	page2 := p2ResultOrFail(t, page2Frames[1], "summary list page 2")
	items2, _ := page2["items"].([]any)
	if len(items2) != 1 {
		t.Fatalf("summary page 2: want 1 item, got %d", len(items2))
	}
	item, _ := items2[0].(map[string]any)
	if item["description"] != "" || item["specification"] != "" {
		t.Fatalf("summary page 2 item must omit bodies: %#v", item)
	}
}

// ─── wrkq.task.update ───────────────────────────────────────────────────────

// TestWrkqTaskUpdate_PatchApplies verifies that a patch (title change) is
// reflected in a subsequent wrkq.task.show.
//
// RED: wrkq.task.update returns WRKQ_VALIDATION "not implemented in P1".
func TestWrkqTaskUpdate_PatchApplies(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Create task.
	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Original Title", "kind": "task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "create")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test update")
	}

	// Update title.
	uf := p2Run(t, dbPath,
		mkRPC("u1", "wrkq.task.update", map[string]any{
			"task":  taskID,
			"patch": map[string]any{"title": "Updated Title"},
		}),
	)
	p2ResultOrFail(t, uf[1], "wrkq.task.update must succeed")

	// Show and verify the new title.
	sf := p2Run(t, dbPath,
		mkRPC("s1", "wrkq.task.show", map[string]any{"task": taskID}),
	)
	showResult := p2ResultOrFail(t, sf[1], "wrkq.task.show after update")
	p2AssertFieldEq(t, showResult, "title", "Updated Title")
}

// TestWrkqTaskUpdate_StaleEtag_ReturnsConflict verifies the atomic expectEtag CAS:
// supplying a stale etag must return WRKQ_CONFLICT carrying the current etag (§9.1).
//
// RED: wrkq.task.update returns WRKQ_VALIDATION "not implemented in P1".
func TestWrkqTaskUpdate_StaleEtag_ReturnsConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Create task.
	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "CAS Test Task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "create")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test CAS")
	}

	// Update with a deliberately stale etag (0 is always wrong for a persisted task).
	uf := p2Run(t, dbPath,
		mkRPC("u1", "wrkq.task.update", map[string]any{
			"task":       taskID,
			"patch":      map[string]any{"title": "Should Not Apply"},
			"expectEtag": 0, // stale — real etag will be ≥ 1
		}),
	)
	// Must be WRKQ_CONFLICT.
	code := p2ErrCode(uf[1])
	if code != "WRKQ_CONFLICT" {
		t.Errorf("stale expectEtag: want WRKQ_CONFLICT, got %q", code)
	}
	// The error data must include the currentEtag.
	currentEtag := p2ErrDataField(uf[1], "currentEtag")
	if currentEtag == nil {
		t.Error("WRKQ_CONFLICT for stale etag must include currentEtag in error data")
	}
}

// TestWrkqTaskUpdate_RowsAffectedZero_ReturnsConflict verifies the rows-affected=0
// path: if the task no longer exists when the update runs, WRKQ_CONFLICT is returned.
//
// RED: wrkq.task.update returns WRKQ_VALIDATION "not implemented in P1".
func TestWrkqTaskUpdate_RowsAffectedZero_ReturnsConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	frames := p2Run(t, dbPath,
		mkRPC("u1", "wrkq.task.update", map[string]any{
			"task":  "T-99999999", // does not exist
			"patch": map[string]any{"title": "Ghost Update"},
		}),
	)
	code := p2ErrCode(frames[1])
	if code != "WRKQ_CONFLICT" && code != "WRKQ_NOT_FOUND" {
		t.Errorf("update non-existent task: want WRKQ_CONFLICT or WRKQ_NOT_FOUND, got %q", code)
	}
}

// ─── wrkq.comment.add / wrkq.comment.list ───────────────────────────────────

// TestWrkqCommentAdd_ReturnsWrkqComment verifies that wrkq.comment.add returns
// a WrkqComment DTO with required camelCase fields.
//
// RED: wrkq.comment.add returns WRKQ_VALIDATION "not implemented in P1".
func TestWrkqCommentAdd_ReturnsWrkqComment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Create a task to comment on.
	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Comment Host Task"}),
	)
	cr := p2ResultOrFail(t, cf[1], "create task for comment")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test comment.add")
	}

	// Add a comment.
	af := p2Run(t, dbPath,
		mkRPC("a1", "wrkq.comment.add", map[string]any{
			"task": taskID,
			"body": "This is a smoke-test comment.",
		}),
	)
	result := p2ResultOrFail(t, af[1], "wrkq.comment.add must return WrkqComment")

	// WrkqComment DTO fields (§6.2).
	p2AssertStr(t, result, "uuid")
	p2AssertStr(t, result, "id")
	p2AssertStr(t, result, "task")
	p2AssertStr(t, result, "body")
	p2AssertStr(t, result, "createdAt")
	p2AssertEtag(t, result)

	// No DB column leaks.
	p2AssertAbsent(t, result, "created_at")
	p2AssertAbsent(t, result, "updated_at")
}

// TestWrkqCommentAdd_UnknownTask_NotFound verifies that commenting on a
// non-existent task returns WRKQ_NOT_FOUND.
//
// RED: stub returns WRKQ_VALIDATION "not implemented in P1".
func TestWrkqCommentAdd_UnknownTask_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("a1", "wrkq.comment.add", map[string]any{
			"task": "T-99999999",
			"body": "Comment on ghost task",
		}),
	)
	code := p2ErrCode(frames[1])
	if code != "WRKQ_NOT_FOUND" {
		t.Errorf("comment on unknown task: want WRKQ_NOT_FOUND, got %q", code)
	}
}

// TestWrkqCommentList_ReturnsItems verifies that wrkq.comment.list returns a
// paginated result with "items" and that added comments appear in the list.
//
// RED: wrkq.comment.list returns WRKQ_VALIDATION "not implemented in P1".
func TestWrkqCommentList_ReturnsItems(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Create task and two comments.
	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Comment List Host"}),
	)
	cr := p2ResultOrFail(t, cf[1], "create task")
	taskID, _ := cr["id"].(string)
	if taskID == "" {
		t.Fatal("create returned empty id; cannot test comment.list")
	}

	p2Run(t, dbPath,
		mkRPC("a1", "wrkq.comment.add", map[string]any{"task": taskID, "body": "Comment 1"}),
		mkRPC("a2", "wrkq.comment.add", map[string]any{"task": taskID, "body": "Comment 2"}),
	)

	// List comments.
	lf := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.comment.list", map[string]any{"task": taskID}),
	)
	result := p2ResultOrFail(t, lf[1], "wrkq.comment.list must return items")
	p2AssertHasItems(t, result, "wrkq.comment.list")

	items, _ := result["items"].([]any)
	if len(items) < 2 {
		t.Errorf("expected ≥ 2 comments, got %d", len(items))
	}
}

// ─── wrkq.workflow.attach ───────────────────────────────────────────────────

// TestWrkqWorkflowAttach_ReturnsAttachResult verifies that wrkq.workflow.attach
// returns a WrkqWorkflowAttachResult{task, instance, attached=true} on first call.
//
// RED (partial): current handler returns map[string]any not a named DTO; the
// instance field is the raw *workflow.Instance. The "attached" field is correct
// on first call but there is no idempotency enforcement.
//
// Pre-condition: creates a task via CLI (since wrkq.task.create RPC is a stub).
func TestWrkqWorkflowAttach_ReturnsAttachResult(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)

	// Seed a real task so the workflow service can find it (pre-P2 workaround).
	taskID := p2SeedTask(t, dbPath,
		"a1000000-0000-4000-8000-000000000001",
		"wf-attach-test", "Workflow Attach Test Task")

	// Install workflow template and attach.
	frames := p2Run(t, dbPath,
		mkRPC("i1", "wrkf.workflow.install", map[string]any{"path": tplPath}),
		mkRPC("a1", "wrkq.workflow.attach", map[string]any{
			"task":           taskID,
			"workflow":       "wrkq-code-change@1",
			"idempotencyKey": "p2-smokey:attach:" + taskID + ":code-change@1",
		}),
	)
	// frames: init(0), install(1), attach(2), shutdown(3)
	p2ResultOrFail(t, frames[1], "wrkf.workflow.install")
	attachResult := p2ResultOrFail(t, frames[2], "wrkq.workflow.attach must return WrkqWorkflowAttachResult")

	// WrkqWorkflowAttachResult.task must be a WrkqTask DTO object (not a string ID).
	// RED: P1 returns task as a string ID; P2 must return the full DTO.
	taskDTO, ok := attachResult["task"].(map[string]any)
	if !ok {
		t.Errorf("wrkq.workflow.attach result field \"task\" must be a WrkqTask DTO object, got: %T=%v", attachResult["task"], attachResult["task"])
	} else {
		// Verify required WrkqTask fields are present on the embedded DTO.
		p2AssertStr(t, taskDTO, "uuid")
		p2AssertStr(t, taskDTO, "id")
		p2AssertStr(t, taskDTO, "slug")
		p2AssertStr(t, taskDTO, "projectUuid")
		p2AssertEtag(t, taskDTO)
	}

	if attachResult["attached"] != true {
		t.Errorf("first attach: expected attached=true, got %v", attachResult["attached"])
	}

	// "instance" must be a WrkfInstance-shaped object.
	inst, ok := attachResult["instance"].(map[string]any)
	if !ok || inst == nil {
		t.Fatalf("wrkq.workflow.attach result missing \"instance\" object, got: %T=%v", attachResult["instance"], attachResult["instance"])
	}
	p2AssertStr(t, inst, "id")
	p2AssertStr(t, inst, "templateId")
	p2AssertStr(t, inst, "templateVersion")
	p2AssertStr(t, inst, "status")
	p2AssertStr(t, inst, "contextHash")

	// No DB column leaks in instance.
	p2AssertAbsent(t, inst, "template_id")
	p2AssertAbsent(t, inst, "context_hash")
	p2AssertAbsent(t, inst, "template_version")
}

// TestWrkqWorkflowAttach_Idempotency_AttachedFalse verifies that replaying
// wrkq.workflow.attach with the same idempotencyKey returns the SAME instance
// with attached=false (§8.2 mandatory idempotency for wrkq.workflow.attach).
//
// RED: stub always returns attached=true; no idempotency enforcement.
//
// Pre-condition: creates a task via CLI (since wrkq.task.create RPC is a stub).
func TestWrkqWorkflowAttach_Idempotency_AttachedFalse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)

	// Seed a real task so the workflow service can find it (pre-P2 workaround).
	taskID := p2SeedTask(t, dbPath,
		"b1000000-0000-4000-8000-000000000001",
		"wf-idem-attach-test", "Workflow Idempotency Test Task")
	key := "p2-smokey:attach:" + taskID + ":code-change@1"

	frames := p2Run(t, dbPath,
		mkRPC("i1", "wrkf.workflow.install", map[string]any{"path": tplPath}),
		mkRPC("a1", "wrkq.workflow.attach", map[string]any{
			"task":           taskID,
			"workflow":       "wrkq-code-change@1",
			"idempotencyKey": key,
		}),
		mkRPC("a2", "wrkq.workflow.attach", map[string]any{
			"task":           taskID,
			"workflow":       "wrkq-code-change@1",
			"idempotencyKey": key, // same key → must replay
		}),
	)
	// frames: init(0), install(1), attach1(2), attach2(3), shutdown(4)
	p2ResultOrFail(t, frames[1], "install")
	r1 := p2ResultOrFail(t, frames[2], "first attach")
	r2 := p2ResultOrFail(t, frames[3], "second attach (replay)")

	// Both must succeed.
	inst1, _ := r1["instance"].(map[string]any)
	inst2, _ := r2["instance"].(map[string]any)
	if inst1 == nil || inst2 == nil {
		t.Fatal("both attach calls must return an instance")
	}
	id1, _ := inst1["id"].(string)
	id2, _ := inst2["id"].(string)
	if id1 != id2 {
		t.Errorf("idempotency replay: instance ids differ; first=%q second=%q", id1, id2)
	}

	// Replay must return attached=false.
	if r2["attached"] != false {
		t.Errorf("replay attach: expected attached=false, got %v", r2["attached"])
	}
}

// ─── wrkq.workflow.inspect / timeline ───────────────────────────────────────

// TestWrkqWorkflowInspect_ReturnsTypedDTO verifies that wrkq.workflow.inspect
// returns a typed result with an "instance" object (WrkfInstance-shaped).
//
// The current impl returns the raw *workflow.Instance (which may or may not be
// wrapped in a WrkqWorkflowInspectResult). This test pins the expected shape.
//
// Pre-condition: creates a task via CLI (since wrkq.task.create RPC is a stub).
func TestWrkqWorkflowInspect_ReturnsTypedDTO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)

	taskID := p2SeedTask(t, dbPath,
		"c1000000-0000-4000-8000-000000000001",
		"wf-inspect-test", "Workflow Inspect Test Task")

	frames := p2Run(t, dbPath,
		mkRPC("i1", "wrkf.workflow.install", map[string]any{"path": tplPath}),
		mkRPC("a1", "wrkq.workflow.attach", map[string]any{
			"task":     taskID,
			"workflow": "wrkq-code-change@1",
		}),
		mkRPC("ins1", "wrkq.workflow.inspect", map[string]any{"task": taskID}),
	)
	// frames: init(0), install(1), attach(2), inspect(3), shutdown(4)
	p2ResultOrFail(t, frames[1], "install")
	p2ResultOrFail(t, frames[2], "attach")
	inspectResult := p2ResultOrFail(t, frames[3], "wrkq.workflow.inspect must return typed DTO")

	// WrkqWorkflowInspectResult must be wrapped: {"instance": {...}}.
	// RED: P1 returns the raw workflow.Instance at the top level (not wrapped).
	inst, ok := inspectResult["instance"].(map[string]any)
	if !ok {
		t.Fatalf("wrkq.workflow.inspect result must be {\"instance\": WrkfInstanceDTO}, got top-level keys: %v", mapKeys(inspectResult))
	}

	// WrkfInstance required fields (camelCase).
	p2AssertStr(t, inst, "id")
	p2AssertStr(t, inst, "templateId")
	p2AssertStr(t, inst, "status")
	p2AssertStr(t, inst, "contextHash")
	p2AssertStr(t, inst, "createdAt")

	// No snake_case leaks.
	p2AssertAbsent(t, inst, "template_id")
	p2AssertAbsent(t, inst, "context_hash")
}

// TestWrkqWorkflowTimeline_ReturnsItems verifies that wrkq.workflow.timeline
// returns a result with "events" (or "items") after a workflow is attached.
//
// The current impl returns []workflow.Event directly (no wrapper). This test
// pins the expected response shape.
//
// Pre-condition: creates a task via CLI (since wrkq.task.create RPC is a stub).
func TestWrkqWorkflowTimeline_ReturnsItems(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)

	taskID := p2SeedTask(t, dbPath,
		"d1000000-0000-4000-8000-000000000001",
		"wf-timeline-test", "Workflow Timeline Test Task")

	frames := p2Run(t, dbPath,
		mkRPC("i1", "wrkf.workflow.install", map[string]any{"path": tplPath}),
		mkRPC("a1", "wrkq.workflow.attach", map[string]any{
			"task":     taskID,
			"workflow": "wrkq-code-change@1",
		}),
		mkRPC("tl1", "wrkq.workflow.timeline", map[string]any{"task": taskID}),
	)
	// frames: init(0), install(1), attach(2), timeline(3), shutdown(4)
	p2ResultOrFail(t, frames[1], "install")
	p2ResultOrFail(t, frames[2], "attach")

	// Timeline must return without error; result structure to be locked in P2.
	timelineResult := p2ResultOrFail(t, frames[3], "wrkq.workflow.timeline must return a result")

	// Accept either {"events":[...]} or {"items":[...]} or a raw array (via result map wrapping).
	hasEvents := false
	if _, ok := timelineResult["events"]; ok {
		hasEvents = true
	}
	if _, ok := timelineResult["items"]; ok {
		hasEvents = true
	}
	// If neither key, check if the entire result is an array encoded as map (shouldn't happen).
	if !hasEvents {
		t.Error("wrkq.workflow.timeline result must have \"events\" or \"items\" array")
	}
}

func TestWrkqWorkflowTimeline_TransitionEventPayload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)

	taskID := p2SeedTask(t, dbPath,
		"d1000000-0000-4000-8000-000000000002",
		"wf-timeline-transition-test", "Workflow Timeline Transition Test Task")

	frames := p2Run(t, dbPath,
		mkRPC("i1", "wrkf.workflow.install", map[string]any{"path": tplPath}),
		mkRPC("a1", "wrkq.workflow.attach", map[string]any{
			"task":     taskID,
			"workflow": "wrkq-code-change@1",
		}),
		mkRPC("e1", "wrkf.evidence.add", map[string]any{
			"task":  taskID,
			"kind":  "red_test",
			"ref":   "test/smokey/p2/timeline-transition-red",
			"principal_ref": "agent:tester",
			"role":  "tester",
			"facts": p3RedFacts(),
		}),
		mkRPC("tr1", "wrkf.transition.apply", map[string]any{
			"task":           taskID,
			"transition":     "author_red",
			"principal_ref":          "agent:tester",
			"role":           "tester",
			"expectRevision": float64(0),
			"idempotencyKey": "timeline-transition-author-red",
		}),
		mkRPC("tl1", "wrkq.workflow.timeline", map[string]any{"task": taskID}),
	)
	p2ResultOrFail(t, frames[1], "install")
	p2ResultOrFail(t, frames[2], "attach")
	p2ResultOrFail(t, frames[3], "add evidence")
	p2ResultOrFail(t, frames[4], "transition apply")
	timeline := p2ResultOrFail(t, frames[5], "timeline")

	events, _ := timeline["events"].([]any)
	for _, raw := range events {
		event, _ := raw.(map[string]any)
		if event["type"] != "workflow.transitioned" {
			continue
		}
		payload, _ := event["payload"].(map[string]any)
		if payload["transition"] == "author_red" && payload["outcome"] == "red_recorded" {
			if _, ok := payload["from"].(map[string]any); !ok {
				t.Fatalf("workflow.transitioned payload missing structured from state: %#v", payload)
			}
			if _, ok := payload["to"].(map[string]any); !ok {
				t.Fatalf("workflow.transitioned payload missing structured to state: %#v", payload)
			}
			return
		}
	}
	t.Fatalf("timeline missing typed workflow.transitioned event for author_red: %#v", events)
}

// ─── WRKQ_NOT_FOUND contract ─────────────────────────────────────────────────

// TestWrkqTaskCreate_UnknownContainer_NotFound verifies that creating a task in
// a non-existent container path returns an error (WRKQ_NOT_FOUND or WRKQ_VALIDATION),
// NOT a stub with an empty id.
//
// RED: current stub ignores the path param entirely and returns {"id":"", ...}.
func TestWrkqTaskCreate_UnknownContainer_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{
			"title": "Orphan Task",
			"path":  "nonexistent/deep/container",
		}),
	)
	// Must be an error, NOT a stub success with empty id.
	if _, hasResult := frames[1]["result"]; hasResult {
		result, _ := frames[1]["result"].(map[string]any)
		id, _ := result["id"].(string)
		// The stub succeeds with id="" — that is the failure this test catches.
		t.Errorf("create in unknown container returned stub result with id=%q; must return WRKQ_NOT_FOUND error", id)
		return
	}
	// If it returned an error, verify it's the right code.
	code := p2ErrCode(frames[1])
	if code != "WRKQ_NOT_FOUND" && code != "WRKQ_VALIDATION" {
		t.Errorf("create in unknown container: want WRKQ_NOT_FOUND or WRKQ_VALIDATION, got %q", code)
	}
}
