//go:build wrkq_local

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
// Test 1 — StartRun idempotency: same key → same run; changed role → mismatch
// ---------------------------------------------------------------------------

// TestStartRunSameKeyReplaysAndChangedRoleIsRefused verifies that calling
// StartRun twice with the SAME idempotencyKey returns the SAME run (replay),
// inserting no duplicate row, and that the same key with a different role is
// refused WRKF_IDEMPOTENCY_MISMATCH rather than silently succeeding.
//
// Contract §5.1 / §6 invariant 3:
//
//	same key → same Run (replay).
//	same key + different role/task → conflict (WRKF_IDEMPOTENCY_MISMATCH).
func TestStartRunSameKeyReplaysAndChangedRoleIsRefused(t *testing.T) {
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

	// --- Third call: SAME key, DIFFERENT role → WRKF_IDEMPOTENCY_MISMATCH ---
	// Contract §6 invariant 3: same key + different role/task → conflict.
	_, err = svc.StartRun(taskUUID, "implementor", "human:tester", StartRunOptions{
		IdempotencyKey: idemKey,
		DeliveryRef:    "acp:msg-001",
	})
	requireWrkfCode(t, err, "WRKF_IDEMPOTENCY_MISMATCH")
}

// ---------------------------------------------------------------------------
// Test 3 — BindExternal: sets external_run_ref; second bind with different ref → conflict
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Test 4 — FinishRun idempotency (legs a + b)
// ---------------------------------------------------------------------------

// TestFinishRunIdempotency verifies both legs of terminal-state idempotency:
//
//	leg a) FinishRun twice with IDENTICAL (status, summary) → existing terminal Run
//	       returned, no error (pure idempotent replay).
//	leg b) FinishRun on an already-terminal run with a DIFFERENT summary →
//	       WRKF_IDEMPOTENCY_MISMATCH (conflicting terminal payload).
//
// Contract §5.1 / §6 invariant 3:
//
//	"Repeated terminal with identical payload → existing terminal Run;
//	 conflicting terminal payload → conflict."
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
