//go:build wrkq_local

package workrpc_test

// wrkqapi_task_write_acceptance_test.go — wrkq.task.create and wrkq.task.update
// over the real JSON-RPC stdio surface (T-04424 P2; docs/wrkq-wrkf-rpc.md §6.2,
// §8.2, §9.1): the WrkqTask DTO, idempotency, expectEtag CAS, and riskClass.

import "testing"

// assertWrkqTaskDTO checks the WrkqTask DTO (§6.2 + §8.4): required camelCase
// fields and no DB column name leaks.
func assertWrkqTaskDTO(t *testing.T, result map[string]any) {
	t.Helper()
	for _, key := range []string{"uuid", "id", "slug", "title", "projectUuid", "path", "state", "kind", "createdAt", "updatedAt"} {
		p2AssertStr(t, result, key)
	}
	p2AssertEtag(t, result)
	for _, column := range []string{"project_uuid", "created_at", "updated_at", "created_by_scope_ref", "updated_by_principal_ref"} {
		p2AssertAbsent(t, result, column)
	}
}

// ─── wrkq.task.create ────────────────────────────────────────────────────────

// TestWrkqTaskCreate_ReturnsNamedDTO verifies that wrkq.task.create returns a
// WrkqTask DTO.
func TestWrkqTaskCreate_ReturnsNamedDTO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Named DTO Test", "kind": "task"}),
	)
	assertWrkqTaskDTO(t, p2ResultOrFail(t, frames[1], "wrkq.task.create"))
}

// TestWrkqTaskCreate_Rejections covers creates that must fail: a missing title
// (WRKQ_VALIDATION) and an unknown container path, which must be an error
// (WRKQ_NOT_FOUND or WRKQ_VALIDATION), never a result.
func TestWrkqTaskCreate_Rejections(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "Orphan Task", "path": "nonexistent/deep/container"}),
	)
	if code := p2ErrCode(frames[1]); code != "WRKQ_VALIDATION" {
		t.Errorf("missing title: want WRKQ_VALIDATION, got %q", code)
	}
	if result, hasResult := frames[2]["result"].(map[string]any); hasResult {
		t.Errorf("create in unknown container returned a result with id=%v; must return WRKQ_NOT_FOUND error", result["id"])
	} else if code := p2ErrCode(frames[2]); code != "WRKQ_NOT_FOUND" && code != "WRKQ_VALIDATION" {
		t.Errorf("create in unknown container: want WRKQ_NOT_FOUND or WRKQ_VALIDATION, got %q", code)
	}
}

// TestWrkqTaskCreate_Persists verifies that a created task can be retrieved
// with wrkq.task.show in a subsequent session.
func TestWrkqTaskCreate_Persists(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID, _ := createTaskRPC(t, dbPath, "Persist Test Task")

	showFrames := p2Run(t, dbPath, mkRPC("s1", "wrkq.task.show", map[string]any{"task": taskID}))
	showResult := p2ResultOrFail(t, showFrames[1], "wrkq.task.show must return the created task")
	p2AssertStr(t, showResult, "uuid")
	p2AssertFieldEq(t, showResult, "title", "Persist Test Task")
}

// TestWrkqTaskCreate_Idempotency_Replay verifies that two creates with the same
// idempotencyKey and identical params return the SAME task.
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
		mkRPC("c2", "wrkq.task.create", params),
	)
	id1, _ := p2ResultOrFail(t, frames[1], "first create")["id"].(string)
	id2, _ := p2ResultOrFail(t, frames[2], "second create (replay)")["id"].(string)
	if id1 == "" {
		t.Error("first create returned empty id")
	}
	if id1 != id2 {
		t.Errorf("idempotency replay: expected same task id; first=%q second=%q", id1, id2)
	}
}

// TestWrkqTaskCreate_Idempotency_Mismatch verifies that the same idempotencyKey
// with different request content returns WRKQ_CONFLICT (§8.2).
func TestWrkqTaskCreate_Idempotency_Mismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	const key = "smokey:idem:create:mismatch-001"
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Original Title", "kind": "task", "idempotencyKey": key}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "Different Title", "kind": "task", "idempotencyKey": key}),
	)
	p2ResultOrFail(t, frames[1], "first create")
	if code := p2ErrCode(frames[2]); code != "WRKQ_CONFLICT" {
		t.Errorf("idempotency mismatch: want WRKQ_CONFLICT, got error.data.code=%q", code)
	}
}

// ─── wrkq.task.update ────────────────────────────────────────────────────────

// TestWrkqTaskUpdate_PatchApplies verifies that a title patch is reflected in a
// subsequent wrkq.task.show.
func TestWrkqTaskUpdate_PatchApplies(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID, _ := createTaskRPC(t, dbPath, "Original Title")

	frames := p2Run(t, dbPath,
		mkRPC("u1", "wrkq.task.update", map[string]any{"task": taskID, "patch": map[string]any{"title": "Updated Title"}}),
		mkRPC("s1", "wrkq.task.show", map[string]any{"task": taskID}),
	)
	p2ResultOrFail(t, frames[1], "wrkq.task.update must succeed")
	p2AssertFieldEq(t, p2ResultOrFail(t, frames[2], "wrkq.task.show after update"), "title", "Updated Title")
}

// TestWrkqTaskUpdate_Conflicts covers the CAS paths (§9.1): a stale expectEtag
// is WRKQ_CONFLICT carrying currentEtag, and updating a task that does not
// exist is WRKQ_CONFLICT or WRKQ_NOT_FOUND.
func TestWrkqTaskUpdate_Conflicts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID, _ := createTaskRPC(t, dbPath, "CAS Test Task")

	frames := p2Run(t, dbPath,
		mkRPC("u1", "wrkq.task.update", map[string]any{
			"task":       taskID,
			"patch":      map[string]any{"title": "Should Not Apply"},
			"expectEtag": 0, // stale — real etag will be ≥ 1
		}),
		mkRPC("u2", "wrkq.task.update", map[string]any{
			"task":  "T-99999999", // does not exist
			"patch": map[string]any{"title": "Ghost Update"},
		}),
	)
	if code := p2ErrCode(frames[1]); code != "WRKQ_CONFLICT" {
		t.Errorf("stale expectEtag: want WRKQ_CONFLICT, got %q", code)
	}
	if p2ErrDataField(frames[1], "currentEtag") == nil {
		t.Error("WRKQ_CONFLICT for stale etag must include currentEtag in error data")
	}
	if code := p2ErrCode(frames[2]); code != "WRKQ_CONFLICT" && code != "WRKQ_NOT_FOUND" {
		t.Errorf("update non-existent task: want WRKQ_CONFLICT or WRKQ_NOT_FOUND, got %q", code)
	}
}

// TestWrkqTaskRiskClass_CreateUpdateShowListAndValidation round-trips riskClass
// through create, update, show and list, and rejects values outside the
// vocabulary as well as legacy phase/preset patches.
func TestWrkqTaskRiskClass_CreateUpdateShowListAndValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Risk Class Task", "kind": "task", "riskClass": "medium"}),
		mkRPC("badCreate", "wrkq.task.create", map[string]any{"title": "Bad Risk Class Task", "kind": "task", "riskClass": "critical"}),
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
	for i, label := range []string{"update", "show"} {
		if got, _ := p2ResultOrFail(t, frames[1+i], "wrkq.task."+label+" riskClass")["riskClass"].(string); got != "high" {
			t.Fatalf("%s riskClass: want high, got %q", label, got)
		}
	}
	listed := p2TaskItemByID(t, p2ResultOrFail(t, frames[3], "wrkq.task.list riskClass"), taskID)
	if listed["riskClass"] != "high" {
		t.Fatalf("task.list did not include updated riskClass for %s: %#v", taskID, listed)
	}
	if code := p2ErrCode(frames[4]); code != "WRKQ_VALIDATION" {
		t.Fatalf("invalid update riskClass: want WRKQ_VALIDATION, got %q", code)
	}
	if code := p2ErrCode(frames[5]); code != "WRKQ_VALIDATION" {
		t.Fatalf("legacy phase/preset patch must reject: want WRKQ_VALIDATION, got %q", code)
	}
}
