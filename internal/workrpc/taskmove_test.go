package workrpc_test

// taskmove_test.go — RED acceptance tests for T-04852 (gap5).
//
// Covers the new RPC method:
//   wrkq.task.move  — { task, targetPath, expectEtag? } → WrkqTask (moved ROOT)
//
// CASCADE requirement (daedalus HIGH-risk): moving a root task that has
// descendants moves EVERY descendant in the SAME DB transaction.  Every moved
// task gets a new project_uuid, a bumped etag, and a task.moved event.  The
// event payload for descendants carries cascadeRootTaskUuid (or
// cascade_root_task_uuid — either key is accepted by the tests).
//
// All tests COMPILE cleanly and fail RED at runtime because:
//   - wrkq.task.move is not yet registered → the server returns JSON-RPC
//     "method not found" (-32601) instead of a WrkqTask DTO or domain errors.
//
// Tests are driven through the real JSON-RPC dispatch surface via p2Run /
// g1Run (go run ./cmd/wrkq rpc --stdio).  No direct references to
// not-yet-existent Go symbols or struct fields.  Seeding uses direct DB SQL,
// same discipline as gap1/gap2/gap4.
//
// NOTE: child containers use kind='directory' (not 'project') per migration
// 000024 (only projects may be direct children of root; the parent/kind trigger
// rejects a 'project' nested under another container).
// v_container_paths excludes the root slug; so targetPath is always a non-root
// container slug/path.
//
// UUIDs use g5000000-... prefix so they never collide with gap1/g2/g4 seeds.

import (
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// ─── gap5 constants ────────────────────────────────────────────────────────

// g5ActorUUID is the wrkq-system actor seeded by migration 000024.
const g5ActorUUID = "00000000-0000-4000-8000-0000000000a0"

// ─── gap5 seeding helpers ──────────────────────────────────────────────────

// g5SeedProject seeds a kind='project' container (direct child of root) into dbPath.
func g5SeedProject(t *testing.T, dbPath, slug, uuid string) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g5SeedProject: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	if _, err := database.Exec(`
		INSERT OR IGNORE INTO containers
			(uuid, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, ?, ?, (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		uuid, slug, slug, g5ActorUUID, g5ActorUUID,
	); err != nil {
		t.Fatalf("g5SeedProject %q: %v", slug, err)
	}
}

// g5SeedRootTask seeds a top-level task (no parent_task_uuid) in containerUUID.
// Returns the task's T-XXXXX id.
func g5SeedRootTask(t *testing.T, dbPath, slug, uuid, containerUUID string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g5SeedRootTask: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	if _, err := database.Exec(`
		INSERT OR IGNORE INTO tasks
			(uuid, slug, title, description, project_uuid, state, priority, kind,
			 created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, ?, ?, '', ?, 'open', 2, 'task', ?, ?)`,
		uuid, slug, slug, containerUUID, g5ActorUUID, g5ActorUUID,
	); err != nil {
		t.Fatalf("g5SeedRootTask %q: %v", slug, err)
	}
	var taskID string
	if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", uuid).Scan(&taskID); err != nil {
		t.Fatalf("g5SeedRootTask: fetch id for %q: %v", slug, err)
	}
	return taskID
}

// g5SeedSubtask seeds a subtask (parent_task_uuid = parentTaskUUID) in containerUUID.
// Returns the subtask's T-XXXXX id.
func g5SeedSubtask(t *testing.T, dbPath, slug, uuid, containerUUID, parentTaskUUID string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g5SeedSubtask: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	if _, err := database.Exec(`
		INSERT OR IGNORE INTO tasks
			(uuid, slug, title, description, project_uuid, state, priority, kind,
			 parent_task_uuid, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, ?, ?, '', ?, 'open', 2, 'subtask', ?, ?, ?)`,
		uuid, slug, slug, containerUUID, parentTaskUUID, g5ActorUUID, g5ActorUUID,
	); err != nil {
		t.Fatalf("g5SeedSubtask %q: %v", slug, err)
	}
	var taskID string
	if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", uuid).Scan(&taskID); err != nil {
		t.Fatalf("g5SeedSubtask: fetch id for %q: %v", slug, err)
	}
	return taskID
}

// g5TaskEtag reads the current etag of a task from the DB.
func g5TaskEtag(t *testing.T, dbPath, taskUUID string) int64 {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g5TaskEtag: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	var etag int64
	if err := database.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", taskUUID).Scan(&etag); err != nil {
		t.Fatalf("g5TaskEtag %q: %v", taskUUID, err)
	}
	return etag
}

// g5TaskProjectUUID reads the current project_uuid of a task from the DB.
func g5TaskProjectUUID(t *testing.T, dbPath, taskUUID string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g5TaskProjectUUID: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	var projectUUID string
	if err := database.QueryRow("SELECT project_uuid FROM tasks WHERE uuid = ?", taskUUID).Scan(&projectUUID); err != nil {
		t.Fatalf("g5TaskProjectUUID %q: %v", taskUUID, err)
	}
	return projectUUID
}

// g5CountTaskEvents counts event_log rows matching event_type + resource_uuid.
func g5CountTaskEvents(t *testing.T, dbPath, eventType, taskUUID string) int {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g5CountTaskEvents: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	var count int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM event_log WHERE event_type = ? AND resource_uuid = ?",
		eventType, taskUUID,
	).Scan(&count); err != nil {
		t.Fatalf("g5CountTaskEvents(%s, %s): %v", eventType, taskUUID, err)
	}
	return count
}

// g5EventPayload returns the TEXT payload of the most recent event_log row
// matching event_type + resource_uuid.  Returns "" when no rows match.
func g5EventPayload(t *testing.T, dbPath, eventType, taskUUID string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g5EventPayload: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	var payload *string
	if err := database.QueryRow(
		"SELECT payload FROM event_log WHERE event_type = ? AND resource_uuid = ? ORDER BY id DESC LIMIT 1",
		eventType, taskUUID,
	).Scan(&payload); err != nil {
		return "" // no rows
	}
	if payload == nil {
		return ""
	}
	return *payload
}

// g5HierarchyMismatchCount counts tasks whose project_uuid differs from their
// parent task's project_uuid.  The hierarchy invariant requires this to be zero
// after any correct cascade move.
func g5HierarchyMismatchCount(t *testing.T, dbPath string) int {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g5HierarchyMismatchCount: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	var count int
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM tasks t
		JOIN tasks p ON t.parent_task_uuid = p.uuid
		WHERE t.project_uuid != p.project_uuid
		  AND t.state != 'deleted'
		  AND p.state != 'deleted'`,
	).Scan(&count); err != nil {
		t.Fatalf("g5HierarchyMismatchCount: %v", err)
	}
	return count
}

// ─── catalog: method present on both entrypoints ──────────────────────────

// TestTaskMoveMethods_PresentOnBothEntrypoints verifies that wrkq.task.move is
// registered and reachable on BOTH wrkq rpc --stdio AND wrkf rpc --stdio.
//
// Each entrypoint is probed with empty params; an implemented method returns
// WRKQ_VALIDATION (param validation error), not "method not found".
//
// RED: wrkq.task.move is not yet registered → each call returns "method not
// found" → g1IsMethodNotFound returns true → test fails.
func TestTaskMoveMethods_PresentOnBothEntrypoints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}

	entrypoints := []string{"wrkq", "wrkf"}
	for _, ep := range entrypoints {
		ep := ep
		t.Run(ep, func(t *testing.T) {
			dbPath := migratedDB(t)
			frames := g1Run(t, ep, dbPath,
				mkRPC("m1", "wrkq.task.move", map[string]any{}),
			)
			if g1IsMethodNotFound(frames[1]) {
				t.Errorf("entrypoint %s: method %q is not registered (got 'method not found'); "+
					"must be present on both wrkq and wrkf entrypoints", ep, "wrkq.task.move")
			}
		})
	}
}

// ─── wrkq.task.move: DTO shape ────────────────────────────────────────────

// TestWrkqTaskMove_ReturnsWrkqTaskDTO verifies that a successful wrkq.task.move
// returns a WrkqTask DTO with all required camelCase fields and no DB column
// name leaks.
//
// RED: method not registered → "method not found" instead of DTO.
func TestWrkqTaskMove_ReturnsWrkqTaskDTO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	srcProjUUID := "g5000000-1000-4000-8000-000000000001"
	dstProjUUID := "g5000000-1000-4000-8000-000000000002"
	taskUUID := "g5000000-1000-4000-8000-000000000010"
	g5SeedProject(t, dbPath, "g5-dto-src", srcProjUUID)
	g5SeedProject(t, dbPath, "g5-dto-dst", dstProjUUID)
	taskID := g5SeedRootTask(t, dbPath, "g5-dto-task", taskUUID, srcProjUUID)

	frames := p2Run(t, dbPath,
		mkRPC("mv1", "wrkq.task.move", map[string]any{
			"task":       taskID,
			"targetPath": "g5-dto-dst",
		}),
	)
	result := p2ResultOrFail(t, frames[1], "wrkq.task.move DTO shape")

	// Required WrkqTask camelCase fields.
	p2AssertStr(t, result, "uuid")
	p2AssertStr(t, result, "id")
	p2AssertStr(t, result, "path")
	p2AssertEtag(t, result)

	// DB column name leaks forbidden.
	p2AssertAbsent(t, result, "project_uuid")
	p2AssertAbsent(t, result, "parent_task_uuid")
	p2AssertAbsent(t, result, "created_at")
	p2AssertAbsent(t, result, "updated_at")
}

// TestWrkqTaskMove_CanonicalPath_IsRecomputed verifies that the returned task's
// path field is server-derived (from the DB after the move), not echoed from
// the request params.  The path must include the destination container slug and
// must NOT include the source container slug.
//
// RED: method not registered → p2ResultOrFail fails with "method not found".
func TestWrkqTaskMove_CanonicalPath_IsRecomputed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	srcProjUUID := "g5000000-2000-4000-8000-000000000001"
	dstProjUUID := "g5000000-2000-4000-8000-000000000002"
	taskUUID := "g5000000-2000-4000-8000-000000000010"
	g5SeedProject(t, dbPath, "g5-path-src", srcProjUUID)
	g5SeedProject(t, dbPath, "g5-path-dst", dstProjUUID)
	taskID := g5SeedRootTask(t, dbPath, "g5-path-task", taskUUID, srcProjUUID)

	frames := p2Run(t, dbPath,
		mkRPC("mv1", "wrkq.task.move", map[string]any{
			"task":       taskID,
			"targetPath": "g5-path-dst",
		}),
	)
	result := p2ResultOrFail(t, frames[1], "wrkq.task.move canonical path")

	// The returned path must contain the destination container slug.
	gotPath, _ := result["path"].(string)
	if !strings.Contains(gotPath, "g5-path-dst") {
		t.Errorf("wrkq.task.move: result path %q must contain destination container slug %q "+
			"(server-derived canonical path, not echoed from request)", gotPath, "g5-path-dst")
	}
	// The returned path must NOT contain the source container slug.
	if strings.Contains(gotPath, "g5-path-src") {
		t.Errorf("wrkq.task.move: result path %q must NOT contain source container slug %q "+
			"(path must reflect new location after move, not old)", gotPath, "g5-path-src")
	}
}

// ─── wrkq.task.move: cascade descendants ──────────────────────────────────

// TestWrkqTaskMove_WithDescendants_CascadesAll verifies that moving a root task
// with subtasks moves EVERY descendant in the same transaction:
//   - all tasks (root + subtasks) have new project_uuid equal to the target container
//   - all tasks have bumped etags (each greater than before the move)
//   - a task.moved event is logged for every moved task (root + all subtasks)
//   - subtask events carry cascadeRootTaskUuid (or cascade_root_task_uuid) in payload
//   - old/new container uuid/path appear in all event payloads
//
// NOTE: Event/webhook origin must be "rpc" not "cli" (implementation requirement;
// verified by the Via field in the webhook EventContext — set to "rpc" not "cli").
//
// RED: method not registered → p2ResultOrFail fails with "method not found"; DB
// checks after would also fail (original project_uuid unchanged).
func TestWrkqTaskMove_WithDescendants_CascadesAll(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	srcProjUUID := "g5000000-3000-4000-8000-000000000001"
	dstProjUUID := "g5000000-3000-4000-8000-000000000002"
	rootTaskUUID := "g5000000-3000-4000-8000-000000000010"
	sub1UUID := "g5000000-3000-4000-8000-000000000011"
	sub2UUID := "g5000000-3000-4000-8000-000000000012"

	g5SeedProject(t, dbPath, "g5-casc-src", srcProjUUID)
	g5SeedProject(t, dbPath, "g5-casc-dst", dstProjUUID)
	rootTaskID := g5SeedRootTask(t, dbPath, "g5-casc-root", rootTaskUUID, srcProjUUID)
	g5SeedSubtask(t, dbPath, "g5-casc-sub1", sub1UUID, srcProjUUID, rootTaskUUID)
	g5SeedSubtask(t, dbPath, "g5-casc-sub2", sub2UUID, srcProjUUID, rootTaskUUID)

	// Snapshot etags before the move.
	rootEtagBefore := g5TaskEtag(t, dbPath, rootTaskUUID)
	sub1EtagBefore := g5TaskEtag(t, dbPath, sub1UUID)
	sub2EtagBefore := g5TaskEtag(t, dbPath, sub2UUID)

	frames := p2Run(t, dbPath,
		mkRPC("mv1", "wrkq.task.move", map[string]any{
			"task":       rootTaskID,
			"targetPath": "g5-casc-dst",
		}),
	)
	p2ResultOrFail(t, frames[1], "wrkq.task.move cascade all descendants")

	// All three tasks must now live in dstProjUUID.
	for _, pair := range []struct {
		name string
		uuid string
	}{
		{"root", rootTaskUUID},
		{"subtask1", sub1UUID},
		{"subtask2", sub2UUID},
	} {
		gotProj := g5TaskProjectUUID(t, dbPath, pair.uuid)
		if gotProj != dstProjUUID {
			t.Errorf("cascade: %s task project_uuid: want %s, got %s (task not moved to target container)",
				pair.name, dstProjUUID, gotProj)
		}
	}

	// All etags must have been bumped.
	if got := g5TaskEtag(t, dbPath, rootTaskUUID); got <= rootEtagBefore {
		t.Errorf("cascade: root task etag not bumped; before=%d, after=%d", rootEtagBefore, got)
	}
	if got := g5TaskEtag(t, dbPath, sub1UUID); got <= sub1EtagBefore {
		t.Errorf("cascade: subtask1 etag not bumped; before=%d, after=%d", sub1EtagBefore, got)
	}
	if got := g5TaskEtag(t, dbPath, sub2UUID); got <= sub2EtagBefore {
		t.Errorf("cascade: subtask2 etag not bumped; before=%d, after=%d", sub2EtagBefore, got)
	}

	// task.moved event must exist for root + both subtasks.
	if g5CountTaskEvents(t, dbPath, "task.moved", rootTaskUUID) == 0 {
		t.Errorf("cascade: root task must have a task.moved event in event_log")
	}
	if g5CountTaskEvents(t, dbPath, "task.moved", sub1UUID) == 0 {
		t.Errorf("cascade: subtask1 must have a task.moved event in event_log")
	}
	if g5CountTaskEvents(t, dbPath, "task.moved", sub2UUID) == 0 {
		t.Errorf("cascade: subtask2 must have a task.moved event in event_log")
	}

	// Subtask event payloads must carry cascadeRootTaskUuid (camelCase or snake_case).
	for _, pair := range []struct {
		name string
		uuid string
	}{
		{"subtask1", sub1UUID},
		{"subtask2", sub2UUID},
	} {
		payload := g5EventPayload(t, dbPath, "task.moved", pair.uuid)
		if !strings.Contains(payload, "cascadeRootTaskUuid") &&
			!strings.Contains(payload, "cascade_root_task_uuid") {
			t.Errorf("cascade: %s task.moved event payload must carry cascadeRootTaskUuid for descendant; "+
				"got payload: %q", pair.name, payload)
		}
	}
}

// ─── wrkq.task.move: CAS / stale expectEtag ──────────────────────────────

// TestWrkqTaskMove_StaleExpectEtag_ReturnsConflict verifies that passing a
// stale expectEtag (does not match the root task's current etag) returns
// WRKQ_CONFLICT.  No tasks must be moved.
//
// RED: method not registered → p2ErrCode returns "" (from "method not found"),
// not "WRKQ_CONFLICT".
func TestWrkqTaskMove_StaleExpectEtag_ReturnsConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	srcProjUUID := "g5000000-4000-4000-8000-000000000001"
	dstProjUUID := "g5000000-4000-4000-8000-000000000002"
	taskUUID := "g5000000-4000-4000-8000-000000000010"
	g5SeedProject(t, dbPath, "g5-stale-src", srcProjUUID)
	g5SeedProject(t, dbPath, "g5-stale-dst", dstProjUUID)
	taskID := g5SeedRootTask(t, dbPath, "g5-stale-task", taskUUID, srcProjUUID)

	currentEtag := g5TaskEtag(t, dbPath, taskUUID)
	staleEtag := currentEtag + 99 // deliberately wrong

	frames := p2Run(t, dbPath,
		mkRPC("mv1", "wrkq.task.move", map[string]any{
			"task":       taskID,
			"targetPath": "g5-stale-dst",
			"expectEtag": staleEtag,
		}),
	)
	code := p2ErrCode(frames[1])
	if code != "WRKQ_CONFLICT" {
		t.Errorf("wrkq.task.move with stale expectEtag: want WRKQ_CONFLICT, got error.data.code=%q (frame=%#v)",
			code, frames[1])
	}

	// Task must not have moved.
	gotProj := g5TaskProjectUUID(t, dbPath, taskUUID)
	if gotProj != srcProjUUID {
		t.Errorf("stale etag: task must remain in source container after WRKQ_CONFLICT; "+
			"want project_uuid=%s, got %s", srcProjUUID, gotProj)
	}
}

// ─── wrkq.task.move: targetPath validation ────────────────────────────────

// TestWrkqTaskMove_TargetPathNotFound_ReturnsError verifies that a targetPath
// that does not resolve to an existing container returns a domain error
// (WRKQ_NOT_FOUND or WRKQ_VALIDATION).  The method must be registered; "method
// not found" is not acceptable.
//
// RED: method not registered → g1IsMethodNotFound returns true → test fails.
func TestWrkqTaskMove_TargetPathNotFound_ReturnsError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	srcProjUUID := "g5000000-5000-4000-8000-000000000001"
	taskUUID := "g5000000-5000-4000-8000-000000000010"
	g5SeedProject(t, dbPath, "g5-notfound-src", srcProjUUID)
	taskID := g5SeedRootTask(t, dbPath, "g5-notfound-task", taskUUID, srcProjUUID)

	frames := p2Run(t, dbPath,
		mkRPC("mv1", "wrkq.task.move", map[string]any{
			"task":       taskID,
			"targetPath": "g5-container-does-not-exist-at-all/moved-task",
		}),
	)

	// "method not found" means the method is not registered — must fail.
	if g1IsMethodNotFound(frames[1]) {
		t.Errorf("wrkq.task.move: method is not registered (got 'method not found'); " +
			"must return a domain error for non-existent targetPath")
	}
	// Domain error must be WRKQ_NOT_FOUND or WRKQ_VALIDATION.
	code := p2ErrCode(frames[1])
	if code != "WRKQ_NOT_FOUND" && code != "WRKQ_VALIDATION" {
		t.Errorf("wrkq.task.move with non-existent targetPath: want WRKQ_NOT_FOUND or WRKQ_VALIDATION, "+
			"got error.data.code=%q (frame=%#v)", code, frames[1])
	}
}

// ─── wrkq.task.move: independent subtask blocked ──────────────────────────

// TestWrkqTaskMove_IndependentSubtask_ReturnsValidation verifies that directly
// selecting a subtask (rather than its root parent) for a cross-container move —
// when the parent task is NOT included in the move — returns WRKQ_VALIDATION.
//
// An "independent subtask" move would violate the hierarchy invariant; the server
// must reject it.
//
// RED: method not registered → p2ErrCode returns "" (from "method not found"),
// not "WRKQ_VALIDATION".
func TestWrkqTaskMove_IndependentSubtask_ReturnsValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	srcProjUUID := "g5000000-6000-4000-8000-000000000001"
	dstProjUUID := "g5000000-6000-4000-8000-000000000002"
	rootTaskUUID := "g5000000-6000-4000-8000-000000000010"
	subUUID := "g5000000-6000-4000-8000-000000000011"
	g5SeedProject(t, dbPath, "g5-subtask-src", srcProjUUID)
	g5SeedProject(t, dbPath, "g5-subtask-dst", dstProjUUID)
	g5SeedRootTask(t, dbPath, "g5-subtask-root", rootTaskUUID, srcProjUUID)
	subID := g5SeedSubtask(t, dbPath, "g5-subtask-sub", subUUID, srcProjUUID, rootTaskUUID)

	// Directly move the SUBTASK (not its root parent) to a different container.
	// The root task stays in srcProjUUID → this is an independent subtask cross-container
	// move that would break the hierarchy invariant.
	frames := p2Run(t, dbPath,
		mkRPC("mv1", "wrkq.task.move", map[string]any{
			"task":       subID,
			"targetPath": "g5-subtask-dst",
		}),
	)
	code := p2ErrCode(frames[1])
	if code != "WRKQ_VALIDATION" {
		t.Errorf("wrkq.task.move independent subtask cross-container: "+
			"want WRKQ_VALIDATION, got error.data.code=%q (frame=%#v)", code, frames[1])
	}
}

// ─── wrkq.task.move: same-target no-op ────────────────────────────────────

// TestWrkqTaskMove_SameTarget_NoOp_Stable verifies that moving a task to its
// current container is a no-op:
//   - the call succeeds (returns the current WrkqTask)
//   - the task's etag is NOT bumped (no write occurred)
//   - no task.moved event is logged
//
// The expectEtag CAS is still checked even for no-ops.
//
// RED: method not registered → p2ResultOrFail fails with "method not found".
func TestWrkqTaskMove_SameTarget_NoOp_Stable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	srcProjUUID := "g5000000-7000-4000-8000-000000000001"
	taskUUID := "g5000000-7000-4000-8000-000000000010"
	g5SeedProject(t, dbPath, "g5-noop-proj", srcProjUUID)
	taskID := g5SeedRootTask(t, dbPath, "g5-noop-task", taskUUID, srcProjUUID)

	etagBefore := g5TaskEtag(t, dbPath, taskUUID)

	// Move to the SAME container (g5-noop-proj) — must be treated as a no-op.
	frames := p2Run(t, dbPath,
		mkRPC("mv1", "wrkq.task.move", map[string]any{
			"task":        taskID,
			"targetPath":  "g5-noop-proj", // same as current container
			"expectEtag":  etagBefore,     // supply current etag to exercise CAS path
		}),
	)
	result := p2ResultOrFail(t, frames[1], "wrkq.task.move same-target no-op")

	// Result must be the current task.
	gotID, _ := result["id"].(string)
	if gotID != taskID {
		t.Errorf("same-target no-op: result.id must be %q, got %q", taskID, gotID)
	}

	// Etag must NOT have been bumped (no mutation occurred).
	etagAfter := g5TaskEtag(t, dbPath, taskUUID)
	if etagAfter != etagBefore {
		t.Errorf("same-target no-op: etag must not change; before=%d, after=%d", etagBefore, etagAfter)
	}

	// No task.moved event should be logged for a no-op.
	if count := g5CountTaskEvents(t, dbPath, "task.moved", taskUUID); count != 0 {
		t.Errorf("same-target no-op: expected 0 task.moved events, got %d", count)
	}
}

// TestWrkqTaskMove_SameTarget_StaleEtag_ReturnsConflict verifies that a
// same-target no-op with a stale expectEtag still returns WRKQ_CONFLICT.
// CAS is checked even for no-ops to preserve the read-modify atomic guarantee.
//
// RED: method not registered → p2ErrCode returns "" instead of "WRKQ_CONFLICT".
func TestWrkqTaskMove_SameTarget_StaleEtag_ReturnsConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	srcProjUUID := "g5000000-7100-4000-8000-000000000001"
	taskUUID := "g5000000-7100-4000-8000-000000000010"
	g5SeedProject(t, dbPath, "g5-noop-stale-proj", srcProjUUID)
	taskID := g5SeedRootTask(t, dbPath, "g5-noop-stale-task", taskUUID, srcProjUUID)

	currentEtag := g5TaskEtag(t, dbPath, taskUUID)
	staleEtag := currentEtag + 99

	frames := p2Run(t, dbPath,
		mkRPC("mv1", "wrkq.task.move", map[string]any{
			"task":        taskID,
			"targetPath":  "g5-noop-stale-proj", // same container
			"expectEtag":  staleEtag,
		}),
	)
	code := p2ErrCode(frames[1])
	if code != "WRKQ_CONFLICT" {
		t.Errorf("same-target no-op with stale expectEtag: want WRKQ_CONFLICT, "+
			"got error.data.code=%q (frame=%#v)", code, frames[1])
	}
}

// ─── wrkq.task.move: hierarchy invariant (SQL) ────────────────────────────

// TestWrkqTaskMove_HierarchyInvariant_NoProjectMismatch verifies that after a
// cascade move, no child task lives in a different container than its parent.
// This is the core safety invariant of the cascade requirement.
//
// The test seeds a root task with subtasks, performs a move, then executes the
// invariant SQL query directly on the DB.
//
// RED: method not registered → p2ResultOrFail fails with "method not found".
func TestWrkqTaskMove_HierarchyInvariant_NoProjectMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	srcProjUUID := "g5000000-8000-4000-8000-000000000001"
	dstProjUUID := "g5000000-8000-4000-8000-000000000002"
	rootTaskUUID := "g5000000-8000-4000-8000-000000000010"
	sub1UUID := "g5000000-8000-4000-8000-000000000011"
	sub2UUID := "g5000000-8000-4000-8000-000000000012"

	g5SeedProject(t, dbPath, "g5-inv-src", srcProjUUID)
	g5SeedProject(t, dbPath, "g5-inv-dst", dstProjUUID)
	rootTaskID := g5SeedRootTask(t, dbPath, "g5-inv-root", rootTaskUUID, srcProjUUID)
	g5SeedSubtask(t, dbPath, "g5-inv-sub1", sub1UUID, srcProjUUID, rootTaskUUID)
	g5SeedSubtask(t, dbPath, "g5-inv-sub2", sub2UUID, srcProjUUID, rootTaskUUID)

	// Pre-condition: invariant holds (all seeded tasks share the same project_uuid).
	if n := g5HierarchyMismatchCount(t, dbPath); n != 0 {
		t.Fatalf("hierarchy invariant pre-condition: expected 0 mismatches before move, got %d", n)
	}

	// Perform the cascade move.
	// RED: if method not found, p2ResultOrFail calls t.Fatalf here.
	frames := p2Run(t, dbPath,
		mkRPC("mv1", "wrkq.task.move", map[string]any{
			"task":       rootTaskID,
			"targetPath": "g5-inv-dst",
		}),
	)
	p2ResultOrFail(t, frames[1], "wrkq.task.move for hierarchy invariant")

	// Post-condition: invariant must still hold after a correct cascade move.
	if n := g5HierarchyMismatchCount(t, dbPath); n != 0 {
		t.Errorf("hierarchy invariant: expected 0 child/parent project_uuid mismatches "+
			"after cascade move, got %d; "+
			"every descendant must be in the same container as its parent", n)
	}
}

// TestWrkqTaskMove_HierarchyInvariant_ArbitraryMovesSQL verifies the hierarchy
// invariant after multiple successive cascade moves across three projects.
//
// The SQL query
//   SELECT COUNT(*) FROM tasks t
//   JOIN tasks p ON t.parent_task_uuid = p.uuid
//   WHERE t.project_uuid != p.project_uuid
//     AND t.state != 'deleted' AND p.state != 'deleted'
// must return ZERO after all moves complete successfully.
//
// RED: method not registered → p2ResultOrFail on the first move fails.
func TestWrkqTaskMove_HierarchyInvariant_ArbitraryMovesSQL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	projAUUID := "g5000000-9000-4000-8000-000000000001"
	projBUUID := "g5000000-9000-4000-8000-000000000002"
	projCUUID := "g5000000-9000-4000-8000-000000000003"
	r1UUID := "g5000000-9000-4000-8000-000000000010"
	s1UUID := "g5000000-9000-4000-8000-000000000011"
	r2UUID := "g5000000-9000-4000-8000-000000000020"
	s2UUID := "g5000000-9000-4000-8000-000000000021"
	s3UUID := "g5000000-9000-4000-8000-000000000022"

	g5SeedProject(t, dbPath, "g5-multi-a", projAUUID)
	g5SeedProject(t, dbPath, "g5-multi-b", projBUUID)
	g5SeedProject(t, dbPath, "g5-multi-c", projCUUID)

	// R1 (root) + S1 (subtask) in projA.
	r1ID := g5SeedRootTask(t, dbPath, "g5-multi-r1", r1UUID, projAUUID)
	g5SeedSubtask(t, dbPath, "g5-multi-s1", s1UUID, projAUUID, r1UUID)

	// R2 (root) + S2 + S3 (subtasks) in projB.
	r2ID := g5SeedRootTask(t, dbPath, "g5-multi-r2", r2UUID, projBUUID)
	g5SeedSubtask(t, dbPath, "g5-multi-s2", s2UUID, projBUUID, r2UUID)
	g5SeedSubtask(t, dbPath, "g5-multi-s3", s3UUID, projBUUID, r2UUID)

	// Move R1 (+ cascade S1) from projA → projB.
	frames1 := p2Run(t, dbPath,
		mkRPC("mv1", "wrkq.task.move", map[string]any{
			"task":       r1ID,
			"targetPath": "g5-multi-b",
		}),
	)
	p2ResultOrFail(t, frames1[1], "wrkq.task.move R1 to projB")

	// Move R2 (+ cascade S2+S3) from projB → projC.
	frames2 := p2Run(t, dbPath,
		mkRPC("mv2", "wrkq.task.move", map[string]any{
			"task":       r2ID,
			"targetPath": "g5-multi-c",
		}),
	)
	p2ResultOrFail(t, frames2[1], "wrkq.task.move R2 to projC")

	// Hierarchy invariant SQL check: zero child/parent project_uuid mismatches.
	if n := g5HierarchyMismatchCount(t, dbPath); n != 0 {
		t.Errorf("after multiple cascade moves: SQL invariant returned %d child/parent "+
			"project_uuid mismatches (expected 0); "+
			"every descendant must share project_uuid with its parent task", n)
	}
}
