package workflow

// transition_cas_test.go — RED tests for T-01901 (wrkf-rpc Phase C).
//
// These tests are intentionally FAILING today. They describe the required
// behaviour for Service.Transition once the P2 hardening lands:
//
//   1. CAS race:         two goroutines race with the same ExpectRevision →
//                        exactly ONE commits; the loser gets WRKF_STALE_REVISION.
//                        RED TODAY: both succeed (preflight-only CAS, no atomic WHERE clause).
//
//   2. Idempotency replay ordering:
//                        a second call with the same idempotencyKey+same params
//                        (even with a now-stale ExpectRevision) must replay the
//                        original committed result rather than error.
//                        RED TODAY: CAS check fires before idempotency check so the
//                        second call returns a "revision mismatch" error instead of
//                        replaying. Also, the first call result does not contain
//                        eventId (contract §5.1 requires it).
//
//   3. Idempotency mismatch:
//                        same idempotencyKey + different actor (different request
//                        hash) → WRKF_IDEMPOTENCY_MISMATCH.
//                        RED TODAY: no request-hash stored; always returns
//                        idempotent=true with no error.
//
// Contract reference: docs/wrkf-rpc.md §5.1, §6 invariants 1 & 2.

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// casTestTemplate is a minimal workflow template for CAS / idempotency tests.
// It has a single no-evidence, no-check transition "complete" that creates one
// effect so that TransitionResult.effects is non-empty and verifiable.
const casTestTemplate = `{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "cas_test",
  "version": "1",
  "kind": "agent_first_workflow",
  "initial": { "status": "active", "phase": "ready" },
  "roles": {
    "coordinator": { "description": "Test coordinator role" }
  },
  "states": [
    { "status": "active", "phase": "ready" },
    { "status": "closed", "outcome": "done" }
  ],
  "transitions": [
    {
      "id": "complete",
      "from": { "status": "active", "phase": "ready" },
      "by": ["coordinator"],
      "outcomes": [
        {
          "id": "done",
          "when": { "always": true },
          "to": { "status": "closed", "outcome": "done" },
          "effects": [
            { "kind": "wake_role", "role": "coordinator", "reason": "completed" }
          ]
        }
      ]
    }
  ]
}`

// ---------------------------------------------------------------------------
// Error-code helpers
// ---------------------------------------------------------------------------

// wrkfCoder is the interface typed WRKF domain errors MUST implement.
// The implementation lives in internal/wrkfapi/errors.go (not yet created).
// When errors from Service.Transition implement this interface the tests pass;
// until then they fail with a clear message.
type wrkfCoder interface {
	Code() string
}

// wrkfCode returns the WRKF_* error code carried by err, or "" if err is nil
// or if the error does not implement wrkfCoder.
func wrkfCode(err error) string {
	if err == nil {
		return ""
	}
	var c wrkfCoder
	if errors.As(err, &c) {
		return c.Code()
	}
	return ""
}

// requireWrkfCode asserts that err is non-nil and carries the expected WRKF_* code.
func requireWrkfCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with WRKF code %q, got nil", want)
	}
	got := wrkfCode(err)
	if got != want {
		t.Fatalf("expected WRKF code %q, got %q (raw error: %v)", want, got, err)
	}
}

// ---------------------------------------------------------------------------
// Test fixture
// ---------------------------------------------------------------------------

// setupCASFixture opens a fresh temp database, installs casTestTemplate,
// creates a task, attaches it to the template, and returns a ready Service
// plus the task UUID string (usable directly as a task selector).
func setupCASFixture(t *testing.T) (*Service, string, *db.DB) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "cas_test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	svc := NewService(database)

	// Write the template to a temp file so InstallTemplate can load it.
	tplPath := filepath.Join(tmpDir, "cas_test.json")
	if err := os.WriteFile(tplPath, []byte(casTestTemplate), 0644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	if _, err := svc.InstallTemplate(tplPath, "test-actor", nil); err != nil {
		t.Fatalf("InstallTemplate: %v", err)
	}

	// Insert an actor (needed for FK constraints on containers).
	actorUUID := "cccccccc-cccc-4ccc-cccc-000000000001"
	if _, err := database.Exec(
		`INSERT INTO actors (uuid, slug, role) VALUES (?, 'cas-actor', 'system')`,
		actorUUID,
	); err != nil {
		t.Fatalf("insert actor: %v", err)
	}

	// Insert a root project container. containers requires actor FKs + title + kind.
	containerUUID := "aaaaaaaa-aaaa-4aaa-aaaa-000000000001"
	if _, err := database.Exec(
		`INSERT INTO containers (uuid, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'cas-project', 'CAS Project', (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		containerUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert container: %v", err)
	}

	// Insert a task that references the container.
	// uuid must be standard UUID format (36 chars, 4 hyphens) for ResolveTask.
	taskUUID := "bbbbbbbb-bbbb-4bbb-bbbb-000000000001"
	if _, err := database.Exec(
		`INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, kind,
		                    created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'cas-task', 'CAS Test Task', ?, 'open', 2, 'task', ?, ?)`,
		taskUUID, containerUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	// Attach workflow instance. taskUUID is accepted as a selector via UUID path.
	if _, err := svc.AttachTask(taskUUID, "cas_test@1", "test-actor"); err != nil {
		t.Fatalf("AttachTask: %v", err)
	}

	return svc, taskUUID, database
}

// ---------------------------------------------------------------------------
// Test 1 — CAS race
// ---------------------------------------------------------------------------

// TestTransitionCASRace verifies that when N goroutines concurrently apply the
// same transition with the same ExpectRevision exactly ONE succeeds and every
// other fails with WRKF_STALE_REVISION (contract §6 invariant 1).
//
// RED TODAY:
//   - The current implementation performs the revision check as a preflight
//     (outside the transaction) and does NOT use a "WHERE revision = ?" clause
//     in the UPDATE.  As a result multiple goroutines can pass the preflight and
//     all commit, so successCount > 1.
//   - Even if only one goroutine commits (due to SQLite serialisation), the
//     failing goroutines return a plain fmt.Errorf string, not a typed error that
//     implements wrkfCoder with code "WRKF_STALE_REVISION".
func TestTransitionCASRace(t *testing.T) {
	svc, taskUUID, _ := setupCASFixture(t)

	const racers = 5
	rev0 := int64(0)

	type raceResult struct {
		out map[string]interface{}
		err error
	}
	results := make(chan raceResult, racers)

	// Use a channel as a starting pistol so all goroutines begin at roughly the same time.
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			out, err := svc.Transition(taskUUID, "complete", TransitionOptions{
				Actor:          "human:test",
				Role:           "coordinator",
				ExpectRevision: &rev0,
			})
			results <- raceResult{out, err}
		}()
	}
	close(start) // release all goroutines simultaneously
	wg.Wait()
	close(results)

	var successCount, staleCount int
	for r := range results {
		if r.err == nil {
			successCount++
		} else {
			code := wrkfCode(r.err)
			if code == "WRKF_STALE_REVISION" {
				staleCount++
			} else {
				// An error that is NOT a typed WRKF_STALE_REVISION is also a failure.
				t.Errorf("racer got non-WRKF error: %v (wrkfCode=%q)", r.err, code)
			}
		}
	}

	// Invariant: exactly one commit, all others stale.
	if successCount != 1 {
		t.Errorf("CAS race: expected exactly 1 success, got %d", successCount)
	}
	if staleCount != racers-1 {
		t.Errorf("CAS race: expected %d WRKF_STALE_REVISION errors, got %d", racers-1, staleCount)
	}

	// Verify exactly one event was recorded in the DB (one commit, not N).
	// This catches the case where multiple goroutines each wrote an event.
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	var eventCount int
	if err := svc.db.QueryRow(
		`SELECT COUNT(*) FROM workflow_events WHERE instance_id = ? AND type = 'workflow.transitioned'`,
		inst.ID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 1 {
		t.Errorf("CAS race: expected 1 transition event in DB, got %d (multiple commits leaked)", eventCount)
	}
}

// ---------------------------------------------------------------------------
// Test 2 — Idempotency replay
// ---------------------------------------------------------------------------

// TestTransitionIdempotencyReplay verifies the replay semantics of §5.1:
//
//	same idempotencyKey + same request hash → return the ORIGINAL committed
//	TransitionResult (same eventId), not a fresh event.
//
// RED TODAY on two counts:
//
//	a) The first successful call result does not carry "eventId" (contract §5.1
//	   requires TransitionResult.eventId to be set on every commit, not only
//	   on the replay path).
//
//	b) The CAS check (ExpectRevision) is performed BEFORE the idempotency check.
//	   The contract (§6 invariant 2 ordering: load → idempotency → CAS → …) requires
//	   the opposite.  Therefore a second call with the original ExpectRevision=0
//	   fails today with a "revision mismatch" error rather than replaying.
func TestTransitionIdempotencyReplay(t *testing.T) {
	svc, taskUUID, database := setupCASFixture(t)

	rev0 := int64(0)
	const idemKey = "replay-key-1"

	// ------ First call: must commit ------
	result1, err := svc.Transition(taskUUID, "complete", TransitionOptions{
		Actor:          "human:test-actor",
		Role:           "coordinator",
		IdempotencyKey: idemKey,
		ExpectRevision: &rev0,
	})
	if err != nil {
		t.Fatalf("first transition: %v", err)
	}

	// Contract §5.1: TransitionResult must include eventId.
	// RED TODAY: the success path does not include "eventId" in the returned map.
	eventID1, _ := result1["eventId"].(string)
	if eventID1 == "" {
		t.Errorf("first transition result missing eventId (contract §5.1); full result: %v", result1)
	}

	// Verify an event was actually persisted and capture its ID from the DB.
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	var committedEventID string
	if err := database.QueryRow(
		`SELECT id FROM workflow_events WHERE instance_id = ? AND idempotency_key = ?`,
		inst.ID, idemKey,
	).Scan(&committedEventID); err != nil {
		t.Fatalf("no committed event found for idempotency key %q: %v", idemKey, err)
	}
	if committedEventID == "" {
		t.Fatalf("committed event ID is empty")
	}

	// Count events before the replay call.
	var eventsBefore int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM workflow_events WHERE instance_id = ?`, inst.ID,
	).Scan(&eventsBefore); err != nil {
		t.Fatalf("count events before replay: %v", err)
	}

	// ------ Second call: same params including now-stale ExpectRevision=0 ------
	// Contract (§6 invariant 2, load order): idempotency is checked BEFORE CAS.
	// Therefore the replay must succeed even though the instance is now at revision=1.
	// RED TODAY: current code checks CAS before idempotency; the call will return
	// a "revision mismatch" error instead of the replay result.
	result2, err := svc.Transition(taskUUID, "complete", TransitionOptions{
		Actor:          "human:test-actor",
		Role:           "coordinator",
		IdempotencyKey: idemKey,
		ExpectRevision: &rev0, // stale, but idempotency must win
	})
	if err != nil {
		t.Fatalf("replay transition: expected success (idempotency > CAS), got: %v", err)
	}

	// Replay must return the SAME eventId as the original commit.
	eventID2, _ := result2["eventId"].(string)
	if eventID2 == "" {
		t.Errorf("replay result missing eventId; full result: %v", result2)
	} else if eventID2 != committedEventID {
		t.Errorf("replay returned different eventId: committed=%q, replayed=%q", committedEventID, eventID2)
	}

	// Replay MUST NOT insert a new event.
	var eventsAfter int
	if err := database.QueryRow(
		`SELECT COUNT(*) FROM workflow_events WHERE instance_id = ?`, inst.ID,
	).Scan(&eventsAfter); err != nil {
		t.Fatalf("count events after replay: %v", err)
	}
	if eventsAfter != eventsBefore {
		t.Errorf("replay inserted %d new event(s); want 0 (before=%d, after=%d)",
			eventsAfter-eventsBefore, eventsBefore, eventsAfter)
	}
}

// ---------------------------------------------------------------------------
// Test 3 — Idempotency mismatch
// ---------------------------------------------------------------------------

// TestTransitionIdempotencyMismatch verifies that presenting the same
// idempotencyKey with a different request payload returns WRKF_IDEMPOTENCY_MISMATCH
// (contract §3 error table, §5.1, §6 invariant 2).
//
// RED TODAY on two counts:
//
//	a) State-match is checked BEFORE idempotency in the current code, so the call
//	   fails with "transition … cannot run from current state" (plain string error,
//	   not WRKF_IDEMPOTENCY_MISMATCH) rather than reaching the mismatch check.
//	b) Even if ordering were correct, no request hash is stored alongside the
//	   idempotency key, so the mismatch would go undetected (idempotent=true, nil error).
func TestTransitionIdempotencyMismatch(t *testing.T) {
	svc, taskUUID, _ := setupCASFixture(t)

	rev0 := int64(0)
	const idemKey = "mismatch-key-1"

	// ------ First call: commit with actor "human:alice" ------
	_, err := svc.Transition(taskUUID, "complete", TransitionOptions{
		Actor:          "human:alice",
		Role:           "coordinator",
		IdempotencyKey: idemKey,
		ExpectRevision: &rev0,
	})
	if err != nil {
		t.Fatalf("first transition: %v", err)
	}

	// ------ Second call: SAME key, DIFFERENT actor (different request hash) ------
	// Contract §5.1: same key + different request hash → WRKF_IDEMPOTENCY_MISMATCH.
	// We deliberately omit ExpectRevision so the call is NOT rejected by CAS first —
	// the mismatch check must fire before any CAS comparison.
	_, err = svc.Transition(taskUUID, "complete", TransitionOptions{
		Actor:          "human:bob", // different from "human:alice"
		Role:           "coordinator",
		IdempotencyKey: idemKey,
		// No ExpectRevision: let idempotency check reach mismatch detection.
	})
	// RED TODAY: fails with "transition … cannot run from current state" (wrong order)
	// rather than WRKF_IDEMPOTENCY_MISMATCH (idempotency check not reached; no hash stored).
	requireWrkfCode(t, err, "WRKF_IDEMPOTENCY_MISMATCH")
}
