package workflow

// run_binding_test.go — regression tests for T-01902 (wrkf-rpc Phase D).
//
// These tests guard the implemented Service run-layer contracts:
//
//   1. StartRun idempotency: same idempotencyKey + same params → same Run (replay),
//      not a second row.
//   2. StartRun conflict:    same idempotencyKey + different role/task →
//      WRKF_IDEMPOTENCY_MISMATCH.
//   3. BindExternal:         sets external_run_ref on the run; a SECOND bind with
//      a different non-empty externalRunRef → conflict.
//   4. FinishRun idempotency (leg a): FinishRun twice with IDENTICAL terminal payload
//      → existing terminal Run returned, no error.
//   4. FinishRun conflict    (leg b): FinishRun with a DIFFERENT terminal payload on
//      an already-terminal run → WRKF_IDEMPOTENCY_MISMATCH.
//
// Regression focus:
//   - StartRunOptions and BindExternalOptions preserve idempotency metadata.
//   - BindExternal prevents conflicting external refs.
//   - FinishRun handles terminal-state replay and conflict detection.
//
// Contract reference: docs/wrkf-rpc.md §5.1, §6 invariant 3.
//
// Shared helpers (wrkfCoder, wrkfCode, requireWrkfCode, setupCASFixture) live in
// transition_cas_test.go — do NOT redefine them here.

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Test fixture
// ---------------------------------------------------------------------------

// setupRunFixture returns a ready Service and a task UUID.
// It delegates to setupCASFixture (transition_cas_test.go) which installs the
// template, inserts an actor/container/task, and attaches the workflow instance.
func setupRunFixture(t *testing.T) (*Service, string) {
	t.Helper()
	svc, taskUUID, _ := setupCASFixture(t)
	return svc, taskUUID
}

// ---------------------------------------------------------------------------
// Test 1 — StartRun idempotency: same key → same run (replay)
// ---------------------------------------------------------------------------

// TestStartRunIdempotency verifies that calling StartRun twice with the SAME
// idempotencyKey returns the SAME run (replay), inserting no duplicate row.
//
// Contract §5.1 / §6 invariant 3:
//   same key → same Run (replay).
//
// Regression guard: StartRunOptions.IdempotencyKey must replay the original run.
func TestStartRunIdempotency(t *testing.T) {
	svc, taskUUID := setupRunFixture(t)

	const idemKey = "run-idem-replay-1"

	// --- First call: must create a new run ---
	run1, err := svc.StartRun(taskUUID, "coordinator", "human:tester", StartRunOptions{
		IdempotencyKey: idemKey,
		DeliveryRef:    "acp:msg-001",
	})
	if err != nil {
		t.Fatalf("first StartRun: %v", err)
	}
	if run1 == nil || run1.ID == "" {
		t.Fatalf("first StartRun returned nil/empty run")
	}

	// --- Second call: same key → must replay the same run, no new row ---
	run2, err := svc.StartRun(taskUUID, "coordinator", "human:tester", StartRunOptions{
		IdempotencyKey: idemKey,
		DeliveryRef:    "acp:msg-001",
	})
	if err != nil {
		t.Fatalf("replay StartRun: expected success (idempotent), got: %v", err)
	}
	if run2 == nil {
		t.Fatalf("replay StartRun returned nil run")
	}
	if run2.ID != run1.ID {
		t.Errorf("replay returned a DIFFERENT run: first=%q replay=%q (must be the same run)", run1.ID, run2.ID)
	}

	// Verify exactly ONE run row was stored for this idempotency key.
	var rowCount int
	if err := svc.db.QueryRow(
		`SELECT COUNT(*) FROM workflow_runs WHERE idempotency_key = ?`, idemKey,
	).Scan(&rowCount); err != nil {
		t.Fatalf("count runs for idem key: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("idempotency: expected 1 run for key %q, got %d (replay inserted a duplicate)", idemKey, rowCount)
	}
}

// ---------------------------------------------------------------------------
// Test 2 — StartRun conflict: same key + different role → WRKF_IDEMPOTENCY_MISMATCH
// ---------------------------------------------------------------------------

// TestStartRunIdempotencyMismatch verifies that presenting the same idempotencyKey
// with a different role returns WRKF_IDEMPOTENCY_MISMATCH, not a silent success.
//
// Contract §5.1 / §6 invariant 3:
//   same key + different role/task → conflict (WRKF_IDEMPOTENCY_MISMATCH).
//
// Regression guard: a reused idempotency key with different run identity must
// return WRKF_IDEMPOTENCY_MISMATCH.
func TestStartRunIdempotencyMismatch(t *testing.T) {
	svc, taskUUID := setupRunFixture(t)

	const idemKey = "run-conflict-key-1"

	// First call: role "coordinator".
	_, err := svc.StartRun(taskUUID, "coordinator", "human:tester", StartRunOptions{
		IdempotencyKey: idemKey,
	})
	if err != nil {
		t.Fatalf("first StartRun: %v", err)
	}

	// Second call: SAME key, DIFFERENT role → must return WRKF_IDEMPOTENCY_MISMATCH.
	_, err = svc.StartRun(taskUUID, "implementor", "human:tester", StartRunOptions{
		IdempotencyKey: idemKey,
	})
	requireWrkfCode(t, err, "WRKF_IDEMPOTENCY_MISMATCH")
}

// ---------------------------------------------------------------------------
// Test 3 — BindExternal: sets external_run_ref; second bind with different ref → conflict
// ---------------------------------------------------------------------------

// TestBindExternal verifies that BindExternal persists the externalRunRef on the
// run record, and that a second call with a DIFFERENT non-empty externalRunRef
// returns a conflict error.
//
// Contract §5.1:
//   wrkf.run.bindExternal sets external_run_ref (unique when non-empty).
//   Second bind with different non-empty ref → conflict.
//
// Regression guard: BindExternalOptions participates in the external-run
// binding contract, and conflicting refs must remain rejected.
func TestBindExternal(t *testing.T) {
	svc, taskUUID := setupRunFixture(t)

	// Start a run without an externalRunRef.
	run, err := svc.StartRun(taskUUID, "coordinator", "human:tester", StartRunOptions{
		DeliveryRef: "acp:msg-002",
	})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	const extRef1 = "gh-actions-run:12345"

	// --- First bind: must set external_run_ref ---
	bound, err := svc.BindExternal(run.ID, extRef1, BindExternalOptions{})
	if err != nil {
		t.Fatalf("first BindExternal: %v", err)
	}
	if bound == nil {
		t.Fatalf("BindExternal returned nil run")
	}
	if bound.ExternalRunRef != extRef1 {
		t.Errorf("BindExternal: ExternalRunRef = %q, want %q", bound.ExternalRunRef, extRef1)
	}

	// Verify the field was persisted to the DB.
	persisted, err := svc.ShowRun(run.ID)
	if err != nil {
		t.Fatalf("ShowRun after BindExternal: %v", err)
	}
	if persisted.ExternalRunRef != extRef1 {
		t.Errorf("persisted ExternalRunRef = %q, want %q", persisted.ExternalRunRef, extRef1)
	}

	// --- Second bind: different externalRunRef → conflict ---
	// Contract §6 invariant 3: external_run_ref has a partial unique index
	// (WHERE external_run_ref IS NOT NULL AND external_run_ref <> '').
	// Binding a different non-empty ref to the same run must conflict.
	const extRef2 = "gh-actions-run:99999"
	_, err = svc.BindExternal(run.ID, extRef2, BindExternalOptions{})
	if err == nil {
		t.Fatalf("second BindExternal with different ref: expected conflict error, got nil")
	}
	// Uniqueness violation on external_run_ref is surfaced as WRKF_IDEMPOTENCY_MISMATCH.
	code := wrkfCode(err)
	if code != "WRKF_IDEMPOTENCY_MISMATCH" {
		t.Errorf("second BindExternal conflict: expected WRKF_IDEMPOTENCY_MISMATCH, got %q (err: %v)", code, err)
	}
}

// ---------------------------------------------------------------------------
// Test 4 — FinishRun idempotency (legs a + b)
// ---------------------------------------------------------------------------

// TestFinishRunIdempotency verifies both legs of terminal-state idempotency:
//
//   leg a) FinishRun twice with IDENTICAL (status, summary) → existing terminal Run
//          returned, no error (pure idempotent replay).
//   leg b) FinishRun on an already-terminal run with a DIFFERENT summary →
//          WRKF_IDEMPOTENCY_MISMATCH (conflicting terminal payload).
//
// Contract §5.1 / §6 invariant 3:
//   "Repeated terminal with identical payload → existing terminal Run;
//    conflicting terminal payload → conflict."
//
// Regression guard: StartRunOptions creates distinct runs for the two legs, and
// FinishRun must reject a conflicting terminal payload instead of overwriting it.
func TestFinishRunIdempotency(t *testing.T) {
	svc, taskUUID := setupRunFixture(t)

	// Create two separate runs (distinct idempotency keys ensure no replay).
	run1, err := svc.StartRun(taskUUID, "coordinator", "human:tester", StartRunOptions{
		IdempotencyKey: "finish-idem-run-1",
	})
	if err != nil {
		t.Fatalf("StartRun run1: %v", err)
	}

	run2, err := svc.StartRun(taskUUID, "coordinator", "human:tester", StartRunOptions{
		IdempotencyKey: "finish-idem-run-2",
	})
	if err != nil {
		t.Fatalf("StartRun run2: %v", err)
	}

	const (
		termStatus   = "completed"
		termSummary  = "all checks passed"
		otherSummary = "conflicting outcome payload"
	)

	// -----------------------------------------------------------------------
	// Leg a: identical terminal payload → idempotent (no error, same run back)
	// -----------------------------------------------------------------------

	if _, err := svc.FinishRun(run1.ID, termStatus, termSummary); err != nil {
		t.Fatalf("leg-a first FinishRun: %v", err)
	}

	// Second call: same (status, summary) → must succeed, not create a new row.
	replayed, err := svc.FinishRun(run1.ID, termStatus, termSummary)
	if err != nil {
		t.Fatalf("leg-a second FinishRun (identical payload): expected nil error, got: %v", err)
	}
	if replayed == nil {
		t.Fatalf("leg-a second FinishRun returned nil")
	}
	if replayed.ID != run1.ID {
		t.Errorf("leg-a idempotent replay: returned different run ID %q vs %q", replayed.ID, run1.ID)
	}
	if replayed.Status != termStatus {
		t.Errorf("leg-a idempotent replay: status = %q, want %q", replayed.Status, termStatus)
	}

	// -----------------------------------------------------------------------
	// Leg b: conflicting terminal payload → WRKF_IDEMPOTENCY_MISMATCH
	// -----------------------------------------------------------------------

	if _, err := svc.FinishRun(run2.ID, termStatus, termSummary); err != nil {
		t.Fatalf("leg-b first FinishRun: %v", err)
	}

	// Different summary on an already-terminal run → must conflict.
	_, err = svc.FinishRun(run2.ID, termStatus, otherSummary)
	requireWrkfCode(t, err, "WRKF_IDEMPOTENCY_MISMATCH")
}
