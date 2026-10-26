//go:build wrkq_local

package workrpc_test

// wrkqapi_task_lifecycle_acceptance_test.go — wrkq.task.delete, wrkq.task.restore
// and wrkq.task.acknowledge over the real JSON-RPC stdio surface (T-04448).

import (
	"database/sql"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// seedTaskIn inserts a task already in a lifecycle state the RPC surface would
// need several calls to reach, with the matching timestamp column set.
func seedTaskIn(t *testing.T, dbPath, state, stampColumn, uuid, slug string) string {
	t.Helper()
	return seedTaskRow(t, dbPath, taskSeed{uuid: uuid, slug: slug, title: slug, state: state, stampColumn: stampColumn})
}

// ─── task.delete ─────────────────────────────────────────────────────────────

// TestWrkqTaskDelete_CascadesSubtasks deletes a parent and asserts its subtask
// is also marked deleted.
func TestWrkqTaskDelete_CascadesSubtasks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	parentUUID := "f0000001-0000-4000-8000-000000000001"
	childUUID := "f0000001-0000-4000-8000-000000000002"
	parentID := p2SeedTask(t, dbPath, parentUUID, "cascade-parent", "Cascade Parent")
	seedTaskRow(t, dbPath, taskSeed{uuid: childUUID, slug: "cascade-child", title: "Cascade Child", parentUUID: parentUUID})

	df := p2Run(t, dbPath, mkRPC("d1", "wrkq.task.delete", map[string]any{"task": parentID}))
	p2ResultOrFail(t, df[1], "wrkq.task.delete parent")

	sf := p2Run(t, dbPath, mkRPC("s1", "wrkq.task.show", map[string]any{"task": childUUID}))
	childResult := p2ResultOrFail(t, sf[1], "wrkq.task.show subtask after parent delete")
	p2AssertFieldEq(t, childResult, "state", "deleted")
}

// taskStampAndEtag reads a task's durable timestamp column and etag.
func taskStampAndEtag(t *testing.T, dbPath, uuid, column string) (stamp string, etag int64) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("taskStampAndEtag: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var ns sql.NullString
	if err := database.QueryRow("SELECT "+column+", etag FROM tasks WHERE uuid = ?", uuid).Scan(&ns, &etag); err != nil {
		t.Fatalf("taskStampAndEtag(%s): %v", column, err)
	}
	return ns.String, etag
}

// assertNoOpKeptStampAndEtag fails unless a no-op lifecycle call left the task's
// timestamp column and etag exactly as seeded, in the DB and in the returned DTO.
func assertNoOpKeptStampAndEtag(t *testing.T, dbPath, uuid, column string, beforeStamp string, beforeEtag int64, result map[string]any, label string) {
	t.Helper()
	afterStamp, afterEtag := taskStampAndEtag(t, dbPath, uuid, column)
	if afterStamp != beforeStamp {
		t.Errorf("%s: %s re-stamped %q -> %q; a no-op must not write", label, column, beforeStamp, afterStamp)
	}
	if afterEtag != beforeEtag {
		t.Errorf("%s: etag bumped %d -> %d; a no-op must not write", label, beforeEtag, afterEtag)
	}
	if got, _ := result["etag"].(float64); int64(got) != beforeEtag {
		t.Errorf("%s: DTO etag = %v, want unchanged %d", label, result["etag"], beforeEtag)
	}
}

// TestWrkqTaskDelete_Redelete_NoOp deletes an already-deleted task: it succeeds,
// stays deleted, and writes nothing — deleted_at and etag are unchanged.
func TestWrkqTaskDelete_Redelete_NoOp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	const uuid = "f0000002-0000-4000-8000-000000000001"
	taskID := seedTaskIn(t, dbPath, "deleted", "deleted_at", uuid, "redelete-noop")
	beforeStamp, beforeEtag := taskStampAndEtag(t, dbPath, uuid, "deleted_at")

	df := p2Run(t, dbPath, mkRPC("d1", "wrkq.task.delete", map[string]any{"task": taskID}))
	result := p2ResultOrFail(t, df[1], "wrkq.task.delete on already-deleted task")
	p2AssertFieldEq(t, result, "state", "deleted")
	assertNoOpKeptStampAndEtag(t, dbPath, uuid, "deleted_at", beforeStamp, beforeEtag, result, "re-delete")
}

// ─── task.restore ────────────────────────────────────────────────────────────

// TestWrkqTaskRestore_FromTombstone restores a deleted and an archived task:
// each comes back open with its tombstone timestamp cleared.
func TestWrkqTaskRestore_FromTombstone(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	cases := []struct {
		name, state, stamp, uuid, slug, clearedField string
	}{
		{"FromDeleted", "deleted", "deleted_at", "f0000003-0000-4000-8000-000000000001", "restore-from-deleted", "deletedAt"},
		{"FromArchived", "archived", "archived_at", "f0000004-0000-4000-8000-000000000001", "restore-from-archived", "archivedAt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := migratedDB(t)
			taskID := seedTaskIn(t, dbPath, tc.state, tc.stamp, tc.uuid, tc.slug)

			rf := p2Run(t, dbPath, mkRPC("r1", "wrkq.task.restore", map[string]any{"task": taskID}))
			result := p2ResultOrFail(t, rf[1], "wrkq.task.restore from "+tc.state)

			p2AssertFieldEq(t, result, "state", "open")
			assertBlankOrAbsent(t, result, tc.clearedField, "wrkq.task.restore")
		})
	}
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
	taskID := seedTaskIn(t, dbPath, "archived", "archived_at", "f0000003-0000-4000-8000-00000000aa01", "restore-extended")

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

// TestWrkqTaskRestore_Rejections proves each invalid restore is a real domain
// error decided purely from the supplied params, never the P1 stub: restoring a
// live task, targeting state=deleted, a stale ifMatch etag, and an out-of-range
// priority.
func TestWrkqTaskRestore_Rejections(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	cases := []struct {
		name     string
		seed     func(t *testing.T, dbPath string) string
		params   map[string]any
		wantCode string
	}{
		{
			name: "LiveTask",
			seed: func(t *testing.T, dbPath string) string {
				id, _ := createTaskRPC(t, dbPath, "Live Task For Restore")
				return id
			},
			wantCode: "WRKQ_VALIDATION",
		},
		{
			name: "DeletedTargetState",
			seed: func(t *testing.T, dbPath string) string {
				return seedTaskIn(t, dbPath, "deleted", "deleted_at", "f0000005-0000-4000-8000-000000000001", "restore-bad-state")
			},
			params:   map[string]any{"state": "deleted"},
			wantCode: "WRKQ_VALIDATION",
		},
		{
			name: "IfMatchMismatch",
			seed: func(t *testing.T, dbPath string) string {
				return seedTaskIn(t, dbPath, "archived", "archived_at", "f0000003-0000-4000-8000-00000000aa02", "restore-ifmatch")
			},
			params:   map[string]any{"ifMatch": 999},
			wantCode: "WRKQ_CONFLICT",
		},
		{
			name: "InvalidPriority",
			seed: func(t *testing.T, dbPath string) string {
				return seedTaskIn(t, dbPath, "archived", "archived_at", "f0000003-0000-4000-8000-00000000aa03", "restore-badprio")
			},
			params:   map[string]any{"priority": 99},
			wantCode: "WRKQ_VALIDATION",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := migratedDB(t)
			params := map[string]any{"task": tc.seed(t, dbPath)}
			for k, v := range tc.params {
				params[k] = v
			}
			rf := p2Run(t, dbPath, mkRPC("r1", "wrkq.task.restore", params))
			p4AssertDomainError(t, rf[1], tc.wantCode, "wrkq.task.restore "+tc.name)
		})
	}
}

// ─── task.acknowledge ────────────────────────────────────────────────────────

// TestWrkqTaskAcknowledge_Succeeds acknowledges a task that is completed,
// cancelled, or still open with force=true; each result carries acknowledgedAt,
// and a following wrkq.task.show reads the same acknowledgedAt back.
func TestWrkqTaskAcknowledge_Succeeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	cases := []struct {
		name, title, closeState string
		force                   bool
	}{
		{"Completed", "Ack Completed", "completed", false},
		{"Cancelled", "Ack Cancelled", "cancelled", false},
		{"ForceOpen", "Ack Force Open", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := migratedDB(t)
			taskID, _ := createTaskRPC(t, dbPath, tc.title)
			if tc.closeState != "" {
				p2Run(t, dbPath, mkRPC("u1", "wrkq.task.update", map[string]any{
					"task":  taskID,
					"patch": map[string]any{"state": tc.closeState},
				}))
			}
			params := map[string]any{"task": taskID}
			if tc.force {
				params["force"] = true
			}
			af := p2Run(t, dbPath,
				mkRPC("a1", "wrkq.task.acknowledge", params),
				mkRPC("s1", "wrkq.task.show", map[string]any{"task": taskID}),
			)
			result := p2ResultOrFail(t, af[1], "wrkq.task.acknowledge "+tc.name)
			p2AssertStr(t, result, "acknowledgedAt")
			// The acknowledgement is durable: a later task.show carries the same stamp.
			show := p2ResultOrFail(t, af[2], "wrkq.task.show after acknowledge "+tc.name)
			p2AssertFieldEq(t, show, "acknowledgedAt", result["acknowledgedAt"])
		})
	}
}

// TestWrkqTaskAcknowledge_Open_NoForce_Fails acknowledges an open task without
// force: a real WRKQ_VALIDATION, not the stub.
func TestWrkqTaskAcknowledge_Open_NoForce_Fails(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID, _ := createTaskRPC(t, dbPath, "Ack Open No Force")

	af := p2Run(t, dbPath, mkRPC("a1", "wrkq.task.acknowledge", map[string]any{"task": taskID}))
	p4AssertDomainError(t, af[1], "WRKQ_VALIDATION", "acknowledge open task without force")
}

// TestWrkqTaskAcknowledge_AlreadyAcked_NoOp re-acknowledges an acknowledged
// task: a result (not an error) that writes nothing — acknowledged_at and etag
// are unchanged.
func TestWrkqTaskAcknowledge_AlreadyAcked_NoOp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	const uuid = "f0000006-0000-4000-8000-000000000001"
	taskID := seedTaskIn(t, dbPath, "completed", "acknowledged_at", uuid, "already-acked")
	beforeStamp, beforeEtag := taskStampAndEtag(t, dbPath, uuid, "acknowledged_at")

	af := p2Run(t, dbPath, mkRPC("a1", "wrkq.task.acknowledge", map[string]any{"task": taskID}))
	result := p2ResultOrFail(t, af[1], "wrkq.task.acknowledge already-acked task must succeed (no-op)")
	p2AssertStr(t, result, "acknowledgedAt")
	assertNoOpKeptStampAndEtag(t, dbPath, uuid, "acknowledged_at", beforeStamp, beforeEtag, result, "re-acknowledge")
}
