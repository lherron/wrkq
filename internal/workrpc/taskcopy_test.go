//go:build wrkq_local

package workrpc_test

// taskcopy_test.go — acceptance tests for T-05111 (Tranche B, part of T-05088).
//
// Covers the new server-owned deep-copy method:
//
//	wrkq.task.copy
//	  { source, destination, overwrite?, withAttachments?, shallow?, expectEtag?,
//	    actor?, idempotencyKey? }
//	  → WrkqTaskCopyResult { source_id, source_uuid, dest_id, dest_uuid, dest_path,
//	                         attachments_copied?, with_files? }
//
// The SERVER owns the one source-task copy envelope: source resolution,
// destination-container resolution, source expectEtag CAS, create-or-overwrite
// decision, the task-row write, the attachment-metadata cascade, the optional
// same-store attachment-file copy, the task.copied event, and the post-commit
// created webhook carrying a synthetic source_uuid change (daedalus
// hrcchat#10196). These tests drive the real JSON-RPC dispatch surface via
// p2Run / g1Run and assert DTO shape, durable effects, error taxonomy, attachment
// modes, the event payload, and idempotency. They reuse the g5* seeding helpers.
//
// UUIDs use a gC------ prefix so they never collide with the gap5 move seeds.

import (
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// gcSeedTaskFull seeds a root task with description/specification/priority/state so
// the copy can be checked to carry the field values over. Returns the T-id.
func gcSeedTaskFull(t *testing.T, dbPath, slug, uuid, containerUUID string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("gcSeedTaskFull: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	if _, err := database.Exec(`
		INSERT OR IGNORE INTO tasks
			(uuid, slug, title, description, specification, project_uuid, state, priority, kind,
			 created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, ?, ?, 'desc body', 'spec body', ?, 'in_progress', 1, 'task', ?, ?)`,
		uuid, slug, slug, containerUUID, g5ActorUUID, g5ActorUUID,
	); err != nil {
		t.Fatalf("gcSeedTaskFull %q: %v", slug, err)
	}
	var taskID string
	if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", uuid).Scan(&taskID); err != nil {
		t.Fatalf("gcSeedTaskFull: fetch id for %q: %v", slug, err)
	}
	return taskID
}

// gcSeedAttachmentRow inserts an attachment METADATA row for a task (no file on
// disk). Sufficient for the metadata-only cascade test; withAttachments file copy
// is validated via the rpccli parity harness (real shared store).
func gcSeedAttachmentRow(t *testing.T, dbPath, taskUUID, filename string) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("gcSeedAttachmentRow: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	rel := "tasks/" + taskUUID + "/" + filename
	if _, err := database.Exec(`
		INSERT INTO attachments
			(id, task_uuid, filename, relative_path, mime_type, size_bytes, checksum,
			 created_by_actor_uuid, created_by_principal_ref, created_by_scope_ref)
		VALUES ('', ?, ?, ?, 'text/plain', 11, 'abc', ?, 'agent:wrkq-system', NULL)`,
		taskUUID, filename, rel, g5ActorUUID,
	); err != nil {
		t.Fatalf("gcSeedAttachmentRow %q: %v", filename, err)
	}
}

// gcCountAttachments counts attachment rows for a task.
func gcCountAttachments(t *testing.T, dbPath, taskUUID string) int {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("gcCountAttachments: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var n int
	if err := database.QueryRow("SELECT COUNT(*) FROM attachments WHERE task_uuid = ?", taskUUID).Scan(&n); err != nil {
		t.Fatalf("gcCountAttachments: %v", err)
	}
	return n
}

// gcDestTaskUUID resolves the copied task's uuid by (project, slug).
func gcDestTaskUUID(t *testing.T, dbPath, projectUUID, slug string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("gcDestTaskUUID: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var uuid string
	if err := database.QueryRow("SELECT uuid FROM tasks WHERE project_uuid = ? AND slug = ?", projectUUID, slug).Scan(&uuid); err != nil {
		t.Fatalf("gcDestTaskUUID(%s,%s): %v", projectUUID, slug, err)
	}
	return uuid
}

// ─── catalog: method present on both entrypoints ──────────────────────────────

func TestTaskCopyMethod_PresentOnBothEntrypoints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	for _, ep := range []string{"wrkq", "wrkf"} {
		ep := ep
		t.Run(ep, func(t *testing.T) {
			dbPath := migratedDB(t)
			frames := g1Run(t, ep, dbPath, mkRPC("c1", "wrkq.task.copy", map[string]any{}))
			if g1IsMethodNotFound(frames[1]) {
				t.Errorf("entrypoint %s: wrkq.task.copy is not registered (got 'method not found')", ep)
			}
		})
	}
}

// ─── DTO shape + field order + new-copy durable effect ────────────────────────

func TestTaskCopy_NewCopy_DTOAndDurable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	srcProj := "gc000000-1000-4000-8000-000000000001"
	dstProj := "gc000000-1000-4000-8000-000000000002"
	srcTask := "gc000000-1000-4000-8000-000000000010"
	g5SeedProject(t, dbPath, "gc-new-src", srcProj)
	g5SeedProject(t, dbPath, "gc-new-dst", dstProj)
	srcID := gcSeedTaskFull(t, dbPath, "gc-new-task", srcTask, srcProj)

	frames := p2Run(t, dbPath, mkRPC("c1", "wrkq.task.copy", map[string]any{
		"source":      srcID,
		"destination": "gc-new-dst",
	}))
	result := p2ResultOrFail(t, frames[1], "wrkq.task.copy new-copy")

	// Result DTO field set (legacy copyResult snake_case keys).
	p2AssertStr(t, result, "source_id")
	p2AssertStr(t, result, "source_uuid")
	p2AssertStr(t, result, "dest_id")
	p2AssertStr(t, result, "dest_uuid")
	if _, ok := result["dest_path"]; !ok {
		t.Errorf("DTO missing dest_path")
	}
	// source identity echoed exactly.
	p2AssertFieldEq(t, result, "source_id", srcID)
	p2AssertFieldEq(t, result, "source_uuid", srcTask)
	// A brand-new copy → a distinct dest task.
	if du, _ := result["dest_uuid"].(string); du == srcTask {
		t.Errorf("new-copy must create a new task; dest_uuid == source_uuid (%s)", du)
	}
	// no attachments → omitempty fields absent.
	p2AssertAbsent(t, result, "attachments_copied")

	// Durable: the new task lives in the destination project with copied fields.
	destUUID := gcDestTaskUUID(t, dbPath, dstProj, "gc-new-task")
	database, _ := db.Open(dbPath)
	defer func() { _ = database.Close() }()
	var gotProj, gotState, gotDesc, gotSpec string
	var gotPrio int
	if err := database.QueryRow(
		"SELECT project_uuid, state, priority, description, specification FROM tasks WHERE uuid = ?",
		destUUID,
	).Scan(&gotProj, &gotState, &gotPrio, &gotDesc, &gotSpec); err != nil {
		t.Fatalf("read copied task: %v", err)
	}
	if gotProj != dstProj {
		t.Errorf("copied task project_uuid: want %s, got %s", dstProj, gotProj)
	}
	if gotState != "in_progress" || gotPrio != 1 || gotDesc != "desc body" || gotSpec != "spec body" {
		t.Errorf("copied task fields not carried over: state=%q prio=%d desc=%q spec=%q",
			gotState, gotPrio, gotDesc, gotSpec)
	}

	// Event: task.copied logged on the NEW task carrying source id/uuid.
	if n := g5CountTaskEvents(t, dbPath, "task.copied", destUUID); n != 1 {
		t.Errorf("expected exactly 1 task.copied event on the copy, got %d", n)
	}
	payload := g5EventPayload(t, dbPath, "task.copied", destUUID)
	if !strings.Contains(payload, srcID) || !strings.Contains(payload, "source_uuid") {
		t.Errorf("task.copied payload must carry source id + source_uuid; got %q", payload)
	}
}

// ─── overwrite update path ────────────────────────────────────────────────────

func TestTaskCopy_Overwrite_UpdatesExisting(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	srcProj := "gc000000-2000-4000-8000-000000000001"
	dstProj := "gc000000-2000-4000-8000-000000000002"
	srcTask := "gc000000-2000-4000-8000-000000000010"
	dstTask := "gc000000-2000-4000-8000-000000000020"
	g5SeedProject(t, dbPath, "gc-ow-src", srcProj)
	g5SeedProject(t, dbPath, "gc-ow-dst", dstProj)
	srcID := gcSeedTaskFull(t, dbPath, "gc-ow-task", srcTask, srcProj)
	// Pre-existing dest task with the SAME slug (stale content).
	g5SeedRootTask(t, dbPath, "gc-ow-task", dstTask, dstProj)

	etagBefore := g5TaskEtag(t, dbPath, dstTask)
	frames := p2Run(t, dbPath, mkRPC("c1", "wrkq.task.copy", map[string]any{
		"source":      srcID,
		"destination": "gc-ow-dst",
		"overwrite":   true,
	}))
	result := p2ResultOrFail(t, frames[1], "wrkq.task.copy overwrite")

	// Overwrite targets the EXISTING dest row (same uuid/id), not a new task.
	if du, _ := result["dest_uuid"].(string); du != dstTask {
		t.Errorf("overwrite must target the existing dest row; want %s, got %s", dstTask, du)
	}
	if got := g5TaskEtag(t, dbPath, dstTask); got <= etagBefore {
		t.Errorf("overwrite must bump the existing task etag; before=%d after=%d", etagBefore, got)
	}
	// The stale dest fields are replaced by the source fields.
	database, _ := db.Open(dbPath)
	defer func() { _ = database.Close() }()
	var gotState, gotDesc string
	var gotPrio int
	_ = database.QueryRow("SELECT state, priority, description FROM tasks WHERE uuid = ?", dstTask).
		Scan(&gotState, &gotPrio, &gotDesc)
	if gotState != "in_progress" || gotPrio != 1 || gotDesc != "desc body" {
		t.Errorf("overwrite did not copy source fields: state=%q prio=%d desc=%q", gotState, gotPrio, gotDesc)
	}
}

// ─── source expectEtag CAS (stale → conflict, no copy) ────────────────────────

func TestTaskCopy_StaleExpectEtag_ReturnsConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	srcProj := "gc000000-3000-4000-8000-000000000001"
	dstProj := "gc000000-3000-4000-8000-000000000002"
	srcTask := "gc000000-3000-4000-8000-000000000010"
	g5SeedProject(t, dbPath, "gc-cas-src", srcProj)
	g5SeedProject(t, dbPath, "gc-cas-dst", dstProj)
	srcID := gcSeedTaskFull(t, dbPath, "gc-cas-task", srcTask, srcProj)

	stale := g5TaskEtag(t, dbPath, srcTask) + 99
	frames := p2Run(t, dbPath, mkRPC("c1", "wrkq.task.copy", map[string]any{
		"source":      srcID,
		"destination": "gc-cas-dst",
		"expectEtag":  stale,
	}))
	if code := p2ErrCode(frames[1]); code != "WRKQ_CONFLICT" {
		t.Errorf("stale source expectEtag: want WRKQ_CONFLICT, got %q (frame=%#v)", code, frames[1])
	}
	// No copy created in the destination.
	if n := gcCountAttachments(t, dbPath, srcTask); n != 0 {
		_ = n // attachments unrelated here; assert no dest task instead
	}
	database, _ := db.Open(dbPath)
	defer func() { _ = database.Close() }()
	var n int
	_ = database.QueryRow("SELECT COUNT(*) FROM tasks WHERE project_uuid = ? AND slug = 'gc-cas-task'", dstProj).Scan(&n)
	if n != 0 {
		t.Errorf("conflict must not create a copy; found %d dest tasks", n)
	}
}

// ─── dest slug-conflict without overwrite → conflict ──────────────────────────

func TestTaskCopy_SlugConflict_NoOverwrite_ReturnsConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	srcProj := "gc000000-4000-4000-8000-000000000001"
	dstProj := "gc000000-4000-4000-8000-000000000002"
	srcTask := "gc000000-4000-4000-8000-000000000010"
	dstTask := "gc000000-4000-4000-8000-000000000020"
	g5SeedProject(t, dbPath, "gc-sc-src", srcProj)
	g5SeedProject(t, dbPath, "gc-sc-dst", dstProj)
	srcID := gcSeedTaskFull(t, dbPath, "gc-sc-task", srcTask, srcProj)
	g5SeedRootTask(t, dbPath, "gc-sc-task", dstTask, dstProj)

	etagBefore := g5TaskEtag(t, dbPath, dstTask)
	frames := p2Run(t, dbPath, mkRPC("c1", "wrkq.task.copy", map[string]any{
		"source":      srcID,
		"destination": "gc-sc-dst",
	}))
	if code := p2ErrCode(frames[1]); code != "WRKQ_CONFLICT" {
		t.Errorf("slug conflict without overwrite: want WRKQ_CONFLICT, got %q (frame=%#v)", code, frames[1])
	}
	// Existing dest row must be untouched (no etag bump).
	if got := g5TaskEtag(t, dbPath, dstTask); got != etagBefore {
		t.Errorf("conflict must not mutate the existing dest row; before=%d after=%d", etagBefore, got)
	}
}

// ─── not-found taxonomy (source / destination) ────────────────────────────────

func TestTaskCopy_SourceNotFound_ReturnsNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	g5SeedProject(t, dbPath, "gc-nf-dst", "gc000000-5000-4000-8000-000000000002")
	frames := p2Run(t, dbPath, mkRPC("c1", "wrkq.task.copy", map[string]any{
		"source":      "T-09999999",
		"destination": "gc-nf-dst",
	}))
	if code := p2ErrCode(frames[1]); code != "WRKQ_NOT_FOUND" {
		t.Errorf("unknown source: want WRKQ_NOT_FOUND, got %q (frame=%#v)", code, frames[1])
	}
}

func TestTaskCopy_DestNotFound_ReturnsNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	srcProj := "gc000000-5100-4000-8000-000000000001"
	srcTask := "gc000000-5100-4000-8000-000000000010"
	g5SeedProject(t, dbPath, "gc-nfd-src", srcProj)
	srcID := gcSeedTaskFull(t, dbPath, "gc-nfd-task", srcTask, srcProj)
	frames := p2Run(t, dbPath, mkRPC("c1", "wrkq.task.copy", map[string]any{
		"source":      srcID,
		"destination": "gc-no-such-container",
	}))
	if code := p2ErrCode(frames[1]); code != "WRKQ_NOT_FOUND" {
		t.Errorf("unknown destination: want WRKQ_NOT_FOUND, got %q (frame=%#v)", code, frames[1])
	}
}

// ─── attachment modes ─────────────────────────────────────────────────────────

func TestTaskCopy_DefaultMode_CopiesAttachmentMetadataOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	srcProj := "gc000000-6000-4000-8000-000000000001"
	dstProj := "gc000000-6000-4000-8000-000000000002"
	srcTask := "gc000000-6000-4000-8000-000000000010"
	g5SeedProject(t, dbPath, "gc-att-src", srcProj)
	g5SeedProject(t, dbPath, "gc-att-dst", dstProj)
	srcID := gcSeedTaskFull(t, dbPath, "gc-att-task", srcTask, srcProj)
	gcSeedAttachmentRow(t, dbPath, srcTask, "note.txt")

	frames := p2Run(t, dbPath, mkRPC("c1", "wrkq.task.copy", map[string]any{
		"source":      srcID,
		"destination": "gc-att-dst",
	}))
	result := p2ResultOrFail(t, frames[1], "wrkq.task.copy metadata-only")
	// attachments_copied = 1, with_files omitted (false → omitempty).
	p2AssertFieldEq(t, result, "attachments_copied", float64(1))
	p2AssertAbsent(t, result, "with_files")

	destUUID := gcDestTaskUUID(t, dbPath, dstProj, "gc-att-task")
	if n := gcCountAttachments(t, dbPath, destUUID); n != 1 {
		t.Errorf("metadata-only: expected 1 copied attachment row, got %d", n)
	}
}

func TestTaskCopy_ShallowMode_SkipsAttachments(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	srcProj := "gc000000-7000-4000-8000-000000000001"
	dstProj := "gc000000-7000-4000-8000-000000000002"
	srcTask := "gc000000-7000-4000-8000-000000000010"
	g5SeedProject(t, dbPath, "gc-sh-src", srcProj)
	g5SeedProject(t, dbPath, "gc-sh-dst", dstProj)
	srcID := gcSeedTaskFull(t, dbPath, "gc-sh-task", srcTask, srcProj)
	gcSeedAttachmentRow(t, dbPath, srcTask, "note.txt")

	frames := p2Run(t, dbPath, mkRPC("c1", "wrkq.task.copy", map[string]any{
		"source":      srcID,
		"destination": "gc-sh-dst",
		"shallow":     true,
	}))
	result := p2ResultOrFail(t, frames[1], "wrkq.task.copy shallow")
	p2AssertAbsent(t, result, "attachments_copied")

	destUUID := gcDestTaskUUID(t, dbPath, dstProj, "gc-sh-task")
	if n := gcCountAttachments(t, dbPath, destUUID); n != 0 {
		t.Errorf("shallow: expected 0 copied attachment rows, got %d", n)
	}
}

func TestTaskCopy_WithAttachmentsAndShallow_ReturnsValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	srcProj := "gc000000-8000-4000-8000-000000000001"
	dstProj := "gc000000-8000-4000-8000-000000000002"
	srcTask := "gc000000-8000-4000-8000-000000000010"
	g5SeedProject(t, dbPath, "gc-ms-src", srcProj)
	g5SeedProject(t, dbPath, "gc-ms-dst", dstProj)
	srcID := gcSeedTaskFull(t, dbPath, "gc-ms-task", srcTask, srcProj)

	frames := p2Run(t, dbPath, mkRPC("c1", "wrkq.task.copy", map[string]any{
		"source":          srcID,
		"destination":     "gc-ms-dst",
		"withAttachments": true,
		"shallow":         true,
	}))
	if code := p2ErrCode(frames[1]); code != "WRKQ_VALIDATION" {
		t.Errorf("withAttachments+shallow: want WRKQ_VALIDATION, got %q (frame=%#v)", code, frames[1])
	}
}

func TestTaskCopy_WithAttachments_MissingFile_ReturnsNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	srcProj := "gc000000-8500-4000-8000-000000000001"
	dstProj := "gc000000-8500-4000-8000-000000000002"
	srcTask := "gc000000-8500-4000-8000-000000000010"
	g5SeedProject(t, dbPath, "gc-mf-src", srcProj)
	g5SeedProject(t, dbPath, "gc-mf-dst", dstProj)
	srcID := gcSeedTaskFull(t, dbPath, "gc-mf-task", srcTask, srcProj)
	// Metadata row present, but no file on disk (no attach store configured).
	gcSeedAttachmentRow(t, dbPath, srcTask, "ghost.txt")

	frames := p2Run(t, dbPath, mkRPC("c1", "wrkq.task.copy", map[string]any{
		"source":          srcID,
		"destination":     "gc-mf-dst",
		"withAttachments": true,
	}))
	// No attach dir configured under the subprocess → withAttachments with a real
	// attachment row is a VALIDATION (storage not configured) failure, with NO
	// RPC-visible partial durable state (no copied task row left behind).
	code := p2ErrCode(frames[1])
	if code != "WRKQ_VALIDATION" && code != "WRKQ_NOT_FOUND" {
		t.Errorf("withAttachments missing storage/file: want WRKQ_VALIDATION or WRKQ_NOT_FOUND, got %q (frame=%#v)", code, frames[1])
	}
	// Deterministic rollback: the destination must have NO copied task (no partial
	// durable state around the failed file copy).
	database, _ := db.Open(dbPath)
	defer func() { _ = database.Close() }()
	var n int
	_ = database.QueryRow("SELECT COUNT(*) FROM tasks WHERE project_uuid = ? AND slug = 'gc-mf-task'", dstProj).Scan(&n)
	if n != 0 {
		t.Errorf("failed file copy must roll back the task row; found %d dest tasks (partial durable state)", n)
	}
}

// ─── idempotency: replayed copy does not duplicate ────────────────────────────

func TestTaskCopy_IdempotencyKey_ReplayNoDuplicate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	srcProj := "gc000000-9000-4000-8000-000000000001"
	dstProj := "gc000000-9000-4000-8000-000000000002"
	srcTask := "gc000000-9000-4000-8000-000000000010"
	g5SeedProject(t, dbPath, "gc-idem-src", srcProj)
	g5SeedProject(t, dbPath, "gc-idem-dst", dstProj)
	srcID := gcSeedTaskFull(t, dbPath, "gc-idem-task", srcTask, srcProj)

	params := map[string]any{
		"source":         srcID,
		"destination":    "gc-idem-dst",
		"idempotencyKey": "copy-once-key",
	}
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.copy", params),
		mkRPC("c2", "wrkq.task.copy", params),
	)
	r1 := p2ResultOrFail(t, frames[1], "wrkq.task.copy first")
	r2 := p2ResultOrFail(t, frames[2], "wrkq.task.copy replay")

	// Replay returns the SAME dest uuid (no second task).
	u1, _ := r1["dest_uuid"].(string)
	u2, _ := r2["dest_uuid"].(string)
	if u1 == "" || u1 != u2 {
		t.Errorf("idempotent replay must return the same dest_uuid; first=%q replay=%q", u1, u2)
	}
	// Exactly ONE copy in the destination project.
	database, _ := db.Open(dbPath)
	defer func() { _ = database.Close() }()
	var n int
	_ = database.QueryRow("SELECT COUNT(*) FROM tasks WHERE project_uuid = ? AND slug = 'gc-idem-task'", dstProj).Scan(&n)
	if n != 1 {
		t.Errorf("idempotent copy must not duplicate; found %d dest tasks", n)
	}
}