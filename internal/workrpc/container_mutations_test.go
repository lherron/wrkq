//go:build wrkq_local

package workrpc_test

// container_mutations_test.go — RED acceptance tests for T-04849 (gap1).
//
// Covers the three new RPC methods:
//   wrkq.container.create       — persist container, return WrkqContainer DTO, log event
//   wrkq.container.delete       — empty-only hard delete with etag CAS, log event
//   wrkq.container.deleteRecursive — dryRun preflight + commit with exact-impact CAS
//
// All tests COMPILE cleanly and fail RED at runtime because:
//   - wrkq.container.create, wrkq.container.delete, wrkq.container.deleteRecursive
//     are not yet registered → server returns JSON-RPC "method not found" (-32601)
//     instead of WRKQ_VALIDATION / WRKQ_CONFLICT / WrkqContainer DTO.
//   - The catalog test verifies the methods are also present on the wrkf entrypoint.
//
// Tests are driven through the real JSON-RPC dispatch surface via p2Run
// (go run ./cmd/wrkq rpc --stdio) — NO direct references to not-yet-existent
// Go DTO symbols or struct fields.  Seeding uses direct DB SQL (same as gap4).
//
// NOTE: child containers must use kind='directory' (not 'project') per migration
// 000024: only projects may be direct children of root; the parent/kind trigger
// rejects a 'project' nested under another 'project'.

import (
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// ─── gap1 constants & seeding helpers ────────────────────────────────────────

// g1ActorUUID is the wrkq-system actor seeded by migration 000024.
const g1ActorUUID = "00000000-0000-4000-8000-0000000000a0"

// g1SeedProject seeds a kind='project' container (direct child of root) into dbPath.
// slug and uuid are the caller-controlled identity.
func g1SeedProject(t *testing.T, dbPath, slug, uuid string) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g1SeedProject: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	if _, err := database.Exec(`
		INSERT OR IGNORE INTO containers
			(uuid, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, ?, ?, (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		uuid, slug, slug, g1ActorUUID, g1ActorUUID,
	); err != nil {
		t.Fatalf("g1SeedProject %q: %v", slug, err)
	}
}

// g1SeedDirectory seeds a kind='directory' container under parentUUID.
func g1SeedDirectory(t *testing.T, dbPath, slug, uuid, parentUUID string) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g1SeedDirectory: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	if _, err := database.Exec(`
		INSERT OR IGNORE INTO containers
			(uuid, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, ?, ?, ?, 'directory', ?, ?)`,
		uuid, slug, slug, parentUUID, g1ActorUUID, g1ActorUUID,
	); err != nil {
		t.Fatalf("g1SeedDirectory %q: %v", slug, err)
	}
}

// g1SeedTask seeds a task inside containerUUID and returns its T-XXXXX id.
func g1SeedTask(t *testing.T, dbPath, slug, uuid, containerUUID string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g1SeedTask: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	if _, err := database.Exec(`
		INSERT OR IGNORE INTO tasks
			(uuid, slug, title, description, project_uuid, state, priority, kind,
			 created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, ?, ?, '', ?, 'open', 2, 'task', ?, ?)`,
		uuid, slug, slug, containerUUID, g1ActorUUID, g1ActorUUID,
	); err != nil {
		t.Fatalf("g1SeedTask %q: %v", slug, err)
	}
	var taskID string
	if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", uuid).Scan(&taskID); err != nil {
		t.Fatalf("g1SeedTask: fetch id: %v", err)
	}
	return taskID
}

// g1EtagOf reads the current etag of a container from the DB.
func g1EtagOf(t *testing.T, dbPath, containerUUID string) int64 {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g1EtagOf: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	var etag int64
	if err := database.QueryRow("SELECT etag FROM containers WHERE uuid = ?", containerUUID).Scan(&etag); err != nil {
		t.Fatalf("g1EtagOf %q: %v", containerUUID, err)
	}
	return etag
}

// g1RowExists returns true if the given table has a row matching whereClause.
func g1RowExists(t *testing.T, dbPath, table, whereClause string, args ...any) bool {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g1RowExists: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	var count int
	q := "SELECT COUNT(*) FROM " + table + " WHERE " + whereClause
	if err := database.QueryRow(q, args...).Scan(&count); err != nil {
		t.Fatalf("g1RowExists(%s): %v", table, err)
	}
	return count > 0
}

// g1CountEvents counts event_log rows matching event_type (and optionally resource_uuid).
// Pass "" for resourceUUID to count all events of that type.
func g1CountEvents(t *testing.T, dbPath, eventType, resourceUUID string) int {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g1CountEvents: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	var count int
	var qErr error
	if resourceUUID != "" {
		qErr = database.QueryRow(
			"SELECT COUNT(*) FROM event_log WHERE event_type = ? AND resource_uuid = ?",
			eventType, resourceUUID,
		).Scan(&count)
	} else {
		qErr = database.QueryRow(
			"SELECT COUNT(*) FROM event_log WHERE event_type = ?", eventType,
		).Scan(&count)
	}
	if qErr != nil {
		t.Fatalf("g1CountEvents(%s): %v", eventType, qErr)
	}
	return count
}

// g1IsMethodNotFound returns true when a frame carries the JSON-RPC method-not-found
// protocol error (message="method not found", no error.data.code).
func g1IsMethodNotFound(frame map[string]any) bool {
	errObj, _ := frame["error"].(map[string]any)
	if errObj == nil {
		return false
	}
	msg, _ := errObj["message"].(string)
	return msg == "method not found"
}

// g1Run is like p2Run but accepts the entrypoint name (wrkq or wrkf) explicitly.
// It wraps the requests in rpc.initialize / rpc.shutdown / rpc.exit and uses runRPC.
func g1Run(t *testing.T, entrypoint, dbPath string, reqs ...string) []map[string]any {
	t.Helper()
	seq := []string{
		mkRPC("_init", "rpc.initialize", map[string]any{
			"protocolVersion": "2026-06-30",
			"client":          map[string]any{"name": "g1-smokey", "version": "0.0.1"},
		}),
	}
	seq = append(seq, reqs...)
	seq = append(seq,
		mkRPC("_sd", "rpc.shutdown", map[string]any{}),
		mkRPC("", "rpc.exit", nil),
	)
	frames := runRPC(t, entrypoint, dbPath, seq)
	want := 2 + len(reqs)
	if len(frames) != want {
		t.Fatalf("g1Run(%s): expected %d frames, got %d\nframes: %#v", entrypoint, want, len(frames), frames)
	}
	return frames
}

// ─── wrkq.container.create ───────────────────────────────────────────────────

// TestWrkqContainerCreate_ReturnsDTO verifies that wrkq.container.create returns a
// WrkqContainer DTO with all required camelCase fields and no DB column name leaks.
//
// RED: method not registered → server returns "method not found" instead of a DTO.
func TestWrkqContainerCreate_ReturnsDTO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.container.create", map[string]any{
			"slug":  "g1-proj-dto",
			"title": "G1 Project DTO",
			"kind":  "project",
		}),
	)
	// frames[0]=init, frames[1]=create, frames[2]=shutdown
	result := p2ResultOrFail(t, frames[1], "wrkq.container.create")

	// Required camelCase fields per WrkqContainer spec.
	p2AssertStr(t, result, "uuid")
	p2AssertStr(t, result, "id")
	p2AssertStr(t, result, "slug")
	p2AssertStr(t, result, "title")
	p2AssertStr(t, result, "kind")
	p2AssertStr(t, result, "path")
	p2AssertStr(t, result, "createdAt")
	p2AssertStr(t, result, "updatedAt")
	p2AssertEtag(t, result)

	// DB column name leaks forbidden.
	p2AssertAbsent(t, result, "parent_uuid")
	p2AssertAbsent(t, result, "created_at")
	p2AssertAbsent(t, result, "updated_at")
}

// TestWrkqContainerCreate_NestedDirectory verifies that creating a directory inside
// an existing project returns the correct computed path ("project/dir").
//
// RED: method not registered → "method not found".
func TestWrkqContainerCreate_NestedDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Seed a parent project via SQL (so the test doesn't depend on create working).
	projUUID := "g1000000-1111-4000-8000-000000000001"
	projSlug := "g1-nest-parent"
	g1SeedProject(t, dbPath, projSlug, projUUID)

	// Create a directory inside the project.
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.container.create", map[string]any{
			"path":  projSlug, // parent path
			"slug":  "g1-nested-dir",
			"title": "G1 Nested Directory",
			"kind":  "directory",
		}),
	)
	result := p2ResultOrFail(t, frames[1], "wrkq.container.create nested directory")

	// Path must be "project-slug/dir-slug" (computed from v_container_paths).
	expectedPath := projSlug + "/g1-nested-dir"
	gotPath, _ := result["path"].(string)
	if gotPath != expectedPath {
		t.Errorf("nested directory path: want %q, got %q", expectedPath, gotPath)
	}
	// parentUuid must be set to the project's UUID.
	parentUUID, _ := result["parentUuid"].(string)
	if parentUUID != projUUID {
		t.Errorf("nested directory parentUuid: want %q, got %q", projUUID, parentUUID)
	}
}

// TestWrkqContainerCreate_EventLogged verifies that creating a container logs a
// container.created event in the event_log table.
//
// RED: method not registered → no container/event is created.
func TestWrkqContainerCreate_EventLogged(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.container.create", map[string]any{
			"slug":  "g1-event-proj",
			"title": "G1 Event Project",
			"kind":  "project",
		}),
	)
	result := p2ResultOrFail(t, frames[1], "wrkq.container.create")

	// Extract the newly-created container UUID and verify event was logged.
	newUUID, _ := result["uuid"].(string)
	if newUUID == "" {
		t.Fatal("create returned no uuid; cannot check event_log")
	}
	count := g1CountEvents(t, dbPath, "container.created", newUUID)
	if count == 0 {
		t.Errorf("container.created event not found in event_log for uuid=%s", newUUID)
	}
}

// ─── wrkq.container.delete (empty-only hard delete) ─────────────────────────

// TestWrkqContainerDelete_NonEmptyFails verifies that trying to delete a container
// that still has tasks returns WRKQ_VALIDATION (not-empty guard).
//
// RED: method not registered → "method not found" instead of WRKQ_VALIDATION.
func TestWrkqContainerDelete_NonEmptyFails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	projUUID := "g1000000-2100-4000-8000-000000000001"
	projSlug := "g1-del-nonempty"
	g1SeedProject(t, dbPath, projSlug, projUUID)
	g1SeedTask(t, dbPath, "g1-task-nonempty", "g1000000-2100-4000-8000-000000000002", projUUID)

	frames := p2Run(t, dbPath,
		mkRPC("d1", "wrkq.container.delete", map[string]any{
			"path": projSlug,
		}),
	)
	code := p2ErrCode(frames[1])
	if code != "WRKQ_VALIDATION" {
		t.Errorf("delete non-empty container: want WRKQ_VALIDATION, got error.data.code=%q (frame=%#v)",
			code, frames[1])
	}
}

// TestWrkqContainerDelete_EmptySucceeds verifies that deleting an empty container
// succeeds and a subsequent show returns WRKQ_NOT_FOUND.
//
// RED: method not registered → "method not found" (p2ResultOrFail fails).
func TestWrkqContainerDelete_EmptySucceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	projUUID := "g1000000-2200-4000-8000-000000000001"
	projSlug := "g1-del-empty"
	g1SeedProject(t, dbPath, projSlug, projUUID)

	frames := p2Run(t, dbPath,
		mkRPC("d1", "wrkq.container.delete", map[string]any{
			"path": projSlug,
		}),
		mkRPC("s1", "wrkq.container.show", map[string]any{
			"path": projSlug,
		}),
	)
	// frames[1] = delete result, frames[2] = show result.

	// Delete must succeed.
	p2ResultOrFail(t, frames[1], "wrkq.container.delete empty container")

	// Subsequent show must return WRKQ_NOT_FOUND.
	code := p2ErrCode(frames[2])
	if code != "WRKQ_NOT_FOUND" {
		t.Errorf("show after delete: want WRKQ_NOT_FOUND, got error.data.code=%q", code)
	}
}

// TestWrkqContainerDelete_StaleEtagConflict verifies that passing an expectEtag that
// doesn't match the current container etag returns WRKQ_CONFLICT.
//
// RED: method not registered → "method not found" instead of WRKQ_CONFLICT.
func TestWrkqContainerDelete_StaleEtagConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	projUUID := "g1000000-2300-4000-8000-000000000001"
	projSlug := "g1-del-stale"
	g1SeedProject(t, dbPath, projSlug, projUUID)

	currentEtag := g1EtagOf(t, dbPath, projUUID)
	staleEtag := currentEtag + 99 // deliberately wrong

	frames := p2Run(t, dbPath,
		mkRPC("d1", "wrkq.container.delete", map[string]any{
			"path":       projSlug,
			"expectEtag": staleEtag,
		}),
	)
	code := p2ErrCode(frames[1])
	if code != "WRKQ_CONFLICT" {
		t.Errorf("delete with stale etag: want WRKQ_CONFLICT, got error.data.code=%q (frame=%#v)",
			code, frames[1])
	}
}

// TestWrkqContainerDelete_RootContainerRejected verifies that attempting to delete
// the path-invisible root container is rejected (it must never be deletable).
//
// RED: method not registered → "method not found"; no specific root-rejection error.
func TestWrkqContainerDelete_RootContainerRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// The root container has a fixed UUID from migration 000024.
	// It is path-invisible (not in v_container_paths), so the delete method
	// must reject it (WRKQ_VALIDATION or WRKQ_NOT_FOUND — either is acceptable
	// as long as the root is not deleted).
	const rootContainerUUID = "00000000-0000-4000-8000-000000000001"

	frames := p2Run(t, dbPath,
		mkRPC("d1", "wrkq.container.delete", map[string]any{
			"path": rootContainerUUID, // by UUID as path selector
		}),
	)
	// The rejection must come from a registered method (domain error), NOT from
	// "method not found" — which would mean the method isn't implemented at all.
	// RED: method not registered → g1IsMethodNotFound returns true → fails.
	if g1IsMethodNotFound(frames[1]) {
		t.Errorf("delete root container: method 'wrkq.container.delete' is not registered; " +
			"must be implemented and reject root deletions with a domain error")
	}
	// Once implemented, the call must return an error (WRKQ_VALIDATION or WRKQ_NOT_FOUND).
	if frames[1]["error"] == nil {
		t.Errorf("delete root container: expected rejection error, got success result: %v", frames[1]["result"])
	}
	// The root must never be removed from the DB.
	if !g1RowExists(t, dbPath, "containers", "uuid = ? AND kind = 'root'", rootContainerUUID) {
		t.Errorf("root container was deleted — this must never happen")
	}
}

// TestWrkqContainerDelete_EventLogged verifies that a successful delete logs a
// container.deleted event in event_log.
//
// RED: method not registered → no deletion/event.
func TestWrkqContainerDelete_EventLogged(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	projUUID := "g1000000-2500-4000-8000-000000000001"
	projSlug := "g1-del-event"
	g1SeedProject(t, dbPath, projSlug, projUUID)

	frames := p2Run(t, dbPath,
		mkRPC("d1", "wrkq.container.delete", map[string]any{
			"path": projSlug,
		}),
	)
	p2ResultOrFail(t, frames[1], "wrkq.container.delete for event check")

	// After delete, a container.deleted event must be in event_log.
	count := g1CountEvents(t, dbPath, "container.deleted", projUUID)
	if count == 0 {
		t.Errorf("container.deleted event not found in event_log for uuid=%s", projUUID)
	}
}

// ─── wrkq.container.deleteRecursive ─────────────────────────────────────────

// g1SeedSubtree seeds a three-level hierarchy: project → directory → nested directory,
// with tasks in each container.  Returns (projUUID, dirUUID, nestedDirUUID, task IDs).
//
//	g1-del-rec-proj/
//	  g1-del-rec-dir/      ← 1 task
//	    g1-del-rec-nested/ ← 1 task
func g1SeedSubtree(t *testing.T, dbPath string) (projUUID, dirUUID, nestedUUID, taskInDir, taskInNested string) {
	t.Helper()
	projUUID = "g1000000-3000-4000-8000-000000000001"
	dirUUID = "g1000000-3000-4000-8000-000000000002"
	nestedUUID = "g1000000-3000-4000-8000-000000000003"
	taskDirUUID := "g1000000-3000-4000-8000-000000000004"
	taskNestedUUID := "g1000000-3000-4000-8000-000000000005"

	g1SeedProject(t, dbPath, "g1-del-rec-proj", projUUID)
	g1SeedDirectory(t, dbPath, "g1-del-rec-dir", dirUUID, projUUID)
	g1SeedDirectory(t, dbPath, "g1-del-rec-nested", nestedUUID, dirUUID)
	taskInDir = g1SeedTask(t, dbPath, "g1-task-dir", taskDirUUID, dirUUID)
	taskInNested = g1SeedTask(t, dbPath, "g1-task-nested", taskNestedUUID, nestedUUID)
	return
}

// TestWrkqContainerDeleteRecursive_DryRunReturnsCounts verifies that dryRun:true
// returns an impact summary {container, containers, tasks, attachments, bytes}
// without deleting anything.
//
// RED: method not registered → "method not found" instead of impact summary.
func TestWrkqContainerDeleteRecursive_DryRunReturnsCounts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	projUUID, _, _, _, _ := g1SeedSubtree(t, dbPath)
	_ = projUUID

	frames := p2Run(t, dbPath,
		mkRPC("dr1", "wrkq.container.deleteRecursive", map[string]any{
			"path":   "g1-del-rec-proj",
			"dryRun": true,
		}),
	)
	result := p2ResultOrFail(t, frames[1], "wrkq.container.deleteRecursive dryRun")

	// The impact summary must include numeric counts for all four resource types.
	// (Values must be ≥ 0; exact values depend on seeded data.)
	for _, field := range []string{"containers", "tasks", "attachments", "bytes"} {
		if _, ok := result[field]; !ok {
			t.Errorf("dryRun result missing field %q", field)
		}
	}
	// Containers must reflect the seeded subtree: 2 descendant containers (dir + nested).
	containers, _ := result["containers"].(float64)
	if containers < 2 {
		t.Errorf("dryRun containers: want ≥ 2, got %v", containers)
	}
	// Tasks: 2 seeded tasks.
	tasks, _ := result["tasks"].(float64)
	if tasks < 2 {
		t.Errorf("dryRun tasks: want ≥ 2, got %v", tasks)
	}

	// dryRun must NOT have deleted anything from the DB.
	if !g1RowExists(t, dbPath, "containers", "uuid = ?", "g1000000-3000-4000-8000-000000000002") {
		t.Errorf("dryRun deleted dir container — must not mutate on dryRun=true")
	}
}

// TestWrkqContainerDeleteRecursive_CommitSucceeds verifies the commit path:
// passing matching `expected` counts succeeds and returns the deletion summary.
//
// RED: method not registered → "method not found".
func TestWrkqContainerDeleteRecursive_CommitSucceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	projUUID, _, _, _, _ := g1SeedSubtree(t, dbPath)
	_ = projUUID

	// First, call dryRun to obtain the current impact counts.
	drFrames := p2Run(t, dbPath,
		mkRPC("dr1", "wrkq.container.deleteRecursive", map[string]any{
			"path":   "g1-del-rec-proj",
			"dryRun": true,
		}),
	)
	drResult := p2ResultOrFail(t, drFrames[1], "deleteRecursive dryRun before commit")
	drContainers, _ := drResult["containers"].(float64)
	drTasks, _ := drResult["tasks"].(float64)
	drAttachments, _ := drResult["attachments"].(float64)
	drBytes, _ := drResult["bytes"].(float64)

	// Commit with the exact expected counts from the dryRun.
	commitFrames := p2Run(t, dbPath,
		mkRPC("dc1", "wrkq.container.deleteRecursive", map[string]any{
			"path": "g1-del-rec-proj",
			"expected": map[string]any{
				"containers":  drContainers,
				"tasks":       drTasks,
				"attachments": drAttachments,
				"bytes":       drBytes,
			},
		}),
	)
	commitResult := p2ResultOrFail(t, commitFrames[1], "deleteRecursive commit")

	// Must return {deleted: true, containersDeleted: N, tasksDeleted: N, ...}
	deleted, _ := commitResult["deleted"].(bool)
	if !deleted {
		t.Errorf("deleteRecursive commit: result.deleted must be true, got %v", commitResult["deleted"])
	}
	for _, field := range []string{"containersDeleted", "tasksDeleted", "attachmentsDeleted", "bytesFreed"} {
		if _, ok := commitResult[field]; !ok {
			t.Errorf("deleteRecursive commit result missing field %q", field)
		}
	}
}

// TestWrkqContainerDeleteRecursive_CountsMismatchConflict verifies that if the
// real impact has changed between the dryRun and the commit call, the commit
// returns WRKQ_CONFLICT.
//
// RED: method not registered → "method not found" at dryRun step.
func TestWrkqContainerDeleteRecursive_CountsMismatchConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	_, dirUUID, _, _, _ := g1SeedSubtree(t, dbPath)

	// Step 1: dryRun to capture current counts.
	drFrames := p2Run(t, dbPath,
		mkRPC("dr1", "wrkq.container.deleteRecursive", map[string]any{
			"path":   "g1-del-rec-proj",
			"dryRun": true,
		}),
	)
	drResult := p2ResultOrFail(t, drFrames[1], "deleteRecursive dryRun for CAS test")
	drTasks, _ := drResult["tasks"].(float64)
	drContainers, _ := drResult["containers"].(float64)
	drAttachments, _ := drResult["attachments"].(float64)
	drBytes, _ := drResult["bytes"].(float64)

	// Step 2: Add an extra task to the subtree between dryRun and commit.
	g1SeedTask(t, dbPath, "g1-extra-task", "g1000000-3000-4000-8000-000000000099", dirUUID)

	// Step 3: Commit with the ORIGINAL (now stale) expected counts.
	commitFrames := p2Run(t, dbPath,
		mkRPC("dc1", "wrkq.container.deleteRecursive", map[string]any{
			"path": "g1-del-rec-proj",
			"expected": map[string]any{
				"containers":  drContainers,
				"tasks":       drTasks, // stale: +1 task was added
				"attachments": drAttachments,
				"bytes":       drBytes,
			},
		}),
	)
	code := p2ErrCode(commitFrames[1])
	if code != "WRKQ_CONFLICT" {
		t.Errorf("deleteRecursive with stale expected counts: want WRKQ_CONFLICT, got error.data.code=%q (frame=%#v)",
			code, commitFrames[1])
	}
}

// TestWrkqContainerDeleteRecursive_RemovesAllDescendantsAndLogs verifies that after
// a successful commit:
//   - All descendant containers are gone from the containers table.
//   - All tasks in descendant containers are gone from the tasks table.
//   - Per-task and per-container events are logged in event_log.
//   - The containers table still passes FK/integrity invariants.
//
// RED: method not registered → commit returns "method not found" → descendants
// still exist → the DB checks assert their continued presence → test fails RED
// at the "deleted from DB" assertions.
func TestWrkqContainerDeleteRecursive_RemovesAllDescendantsAndLogs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	projUUID, dirUUID, nestedUUID, _, _ := g1SeedSubtree(t, dbPath)
	_ = projUUID

	// Dry run to get counts.
	drFrames := p2Run(t, dbPath,
		mkRPC("dr1", "wrkq.container.deleteRecursive", map[string]any{
			"path":   "g1-del-rec-proj",
			"dryRun": true,
		}),
	)
	drResult := p2ResultOrFail(t, drFrames[1], "deleteRecursive dryRun before removal check")

	// Commit with matching counts.
	commitFrames := p2Run(t, dbPath,
		mkRPC("dc1", "wrkq.container.deleteRecursive", map[string]any{
			"path": "g1-del-rec-proj",
			"expected": map[string]any{
				"containers":  drResult["containers"],
				"tasks":       drResult["tasks"],
				"attachments": drResult["attachments"],
				"bytes":       drResult["bytes"],
			},
		}),
	)
	p2ResultOrFail(t, commitFrames[1], "deleteRecursive commit for removal check")

	// All descendant containers must be gone.
	if g1RowExists(t, dbPath, "containers", "uuid = ?", dirUUID) {
		t.Errorf("dir container %s still exists after deleteRecursive commit", dirUUID)
	}
	if g1RowExists(t, dbPath, "containers", "uuid = ?", nestedUUID) {
		t.Errorf("nested container %s still exists after deleteRecursive commit", nestedUUID)
	}

	// All tasks that were in those containers must be gone.
	if g1RowExists(t, dbPath, "tasks", "project_uuid = ?", dirUUID) {
		t.Errorf("tasks in dir container still exist after deleteRecursive commit")
	}
	if g1RowExists(t, dbPath, "tasks", "project_uuid = ?", nestedUUID) {
		t.Errorf("tasks in nested container still exist after deleteRecursive commit")
	}

	// Per-container events must be logged.
	if g1CountEvents(t, dbPath, "container.deleted", dirUUID) == 0 {
		t.Errorf("container.deleted event not logged for dir container %s", dirUUID)
	}
	if g1CountEvents(t, dbPath, "container.deleted", nestedUUID) == 0 {
		t.Errorf("container.deleted event not logged for nested container %s", nestedUUID)
	}
}

// ─── catalog: methods present on BOTH entrypoints ────────────────────────────

// TestContainerMutationMethods_PresentOnBothEntrypoints verifies that
// wrkq.container.create, wrkq.container.delete, wrkq.container.deleteRecursive
// are registered and reachable on BOTH wrkq rpc --stdio AND wrkf rpc --stdio.
//
// The test calls each method with deliberately invalid/empty params. An implemented
// method returns WRKQ_VALIDATION; an unregistered method returns "method not found"
// (JSON-RPC code -32601, message "method not found") — distinguishable because
// registered domain methods always carry error.data.code.
//
// RED: all three methods are unregistered on both entrypoints → each call returns
// "method not found" → g1IsMethodNotFound returns true → test fails.
func TestContainerMutationMethods_PresentOnBothEntrypoints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}

	methods := []string{
		"wrkq.container.create",
		"wrkq.container.delete",
		"wrkq.container.deleteRecursive",
	}
	entrypoints := []string{"wrkq", "wrkf"}

	for _, ep := range entrypoints {
		ep := ep
		t.Run(ep, func(t *testing.T) {
			dbPath := migratedDB(t)
			for _, method := range methods {
				method := method
				t.Run(method, func(t *testing.T) {
					// Call the method with empty params — should return WRKQ_VALIDATION
					// (param validation), NOT "method not found".
					frames := g1Run(t, ep, dbPath,
						mkRPC("m1", method, map[string]any{}),
					)
					if g1IsMethodNotFound(frames[1]) {
						t.Errorf("entrypoint %s: method %q is not registered (got 'method not found'); "+
							"must be present on both wrkq and wrkf entrypoints", ep, method)
					}
				})
			}
		})
	}
}