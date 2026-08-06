//go:build wrkq_local

package workflow

// effect_lease_test.go — regression tests for T-01903 (wrkf-rpc Phase E: effect claim/lease).
//
// These tests guard the implemented Service.ClaimEffects / AckEffect /
// FailEffect contracts:
//
//   1. ClaimEffects: EffectClaim results are leased atomically with no
//      duplicate effect claims across concurrent callers.
//
//   2. FailEffect: terminal/retry paths require the lease token and retryable
//      flag, and must clear lease metadata when moving an effect terminal.
//
//   3. AckEffect: correct tokens succeed, while wrong or expired tokens return
//      WRKF_LEASE_CONFLICT.
//
// Contract reference: docs/wrkf-rpc.md §5.1, §6 invariant 4.
//
// Shared helpers (wrkfCoder, wrkfCode, requireWrkfCode, setupCASFixture)
// live in transition_cas_test.go — do NOT redefine them here.

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// EffectClaim is the workflow DTO returned by ClaimEffects.
//
// Expected definition (frozen by docs/wrkf-rpc.md §5.1):
//
//   type EffectClaim struct {
//       Effects        []Effect
//       LeaseToken     string
//       LeaseExpiresAt string
//   }

// ---------------------------------------------------------------------------
// Test fixture
// ---------------------------------------------------------------------------

// setupEffectFixture returns a ready Service with n pending effects pre-seeded
// directly into the database. It delegates the basic environment setup to
// setupCASFixture (transition_cas_test.go: temp DB, actor, container, task,
// attached workflow instance).
func setupEffectFixture(t *testing.T, n int) *Service {
	t.Helper()

	svc, taskUUID, database := setupCASFixture(t)

	// Resolve the workflow instance to obtain its ID (FK for workflow_effects).
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("setupEffectFixture: LatestInstance: %v", err)
	}

	// Insert n pending effects directly so that ClaimEffects tests have a
	// deterministic pool regardless of which transitions have fired.
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("eff-lease-%04d", i)
		idemKey := fmt.Sprintf("eff-lease-idem-%04d", i)
		_, err := database.Exec(`
			INSERT INTO workflow_effects
				(id, instance_id, revision, kind, payload_json, status, idempotency_key,
				 created_at, updated_at)
			VALUES
				(?, ?, ?, 'wake_role', '{"role":"coordinator"}', 'pending', ?,
				 datetime('now'), datetime('now'))
		`, id, inst.ID, inst.Revision, idemKey)
		if err != nil {
			t.Fatalf("setupEffectFixture: insert effect %d: %v", i, err)
		}
	}

	return svc
}

// ---------------------------------------------------------------------------
// Test 1 — Concurrent claim → disjoint effect sets
// ---------------------------------------------------------------------------

// TestClaimEffectsAtomicity verifies that when N goroutines concurrently call
// ClaimEffects over the same pool of pending effects, every effect ends up
// leased by AT MOST one goroutine (no duplicate leases).
//
// Contract §6 invariant 4:
//
//	"claim atomically selects pending/failed/expired effects + sets
//	 status='leased', leased_by, leased_until, lease_token."
//	Two concurrent claimers → disjoint effect sets.
//
// Regression guard: ClaimEffects must keep this atomic selection/update as one
// indivisible operation so concurrent adapters cannot lease the same effect.
func TestClaimEffectsAtomicity(t *testing.T) {
	const totalEffects = 6
	const racers = 3

	svc := setupEffectFixture(t, totalEffects)

	type raceResult struct {
		claim *EffectClaim
		err   error
	}

	resultSlice := make([]raceResult, racers)
	var mu sync.Mutex
	var wg sync.WaitGroup

	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		idx := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// Each claimer requests up to all effects; atomicity ensures no
			// effect is returned to two callers.
			claim, err := svc.ClaimEffects("test-adapter", totalEffects, 30_000, "", "")
			mu.Lock()
			resultSlice[idx] = raceResult{claim, err}
			mu.Unlock()
		}()
	}
	close(start) // release all goroutines simultaneously
	wg.Wait()

	// Verify no effect ID appears in more than one claim result.
	seen := map[string]int{} // effectID → racer index that claimed it
	for i, r := range resultSlice {
		if r.err != nil {
			// It is acceptable for a claimer to return an error when no
			// effects remain to claim; a nil error with empty Effects is
			// also acceptable.
			t.Logf("racer %d returned error (may be ok if pool exhausted): %v", i, r.err)
			continue
		}
		if r.claim == nil {
			continue
		}
		for _, eff := range r.claim.Effects {
			if prev, dup := seen[eff.ID]; dup {
				t.Errorf("effect %q claimed by racer %d AND racer %d (duplicate lease violates §6 inv 4)",
					eff.ID, prev, i)
			}
			seen[eff.ID] = i
		}
	}
}

// ---------------------------------------------------------------------------
// Test 2 — Claim respects the limit argument
// ---------------------------------------------------------------------------

// TestClaimEffectsLimit verifies that ClaimEffects never returns more effects
// than the requested limit, and that the EffectClaim DTO carries a non-empty
// LeaseToken and a parseable RFC3339 LeaseExpiresAt in the future.
//
// Contract §5.1:
//
//	"wrkf.effect.claim params: { adapter, limit, leaseMs, task?, kind? } → EffectClaim"
//
// Regression guard: the returned EffectClaim must respect the caller's limit
// and expose enough lease metadata for subsequent ack/fail calls.
func TestClaimEffectsLimit(t *testing.T) {
	const totalEffects = 8
	const limit = 3

	svc := setupEffectFixture(t, totalEffects)

	claim, err := svc.ClaimEffects("test-adapter", limit, 30_000, "", "")
	if err != nil {
		t.Fatalf("ClaimEffects: %v", err)
	}
	if claim == nil {
		t.Fatal("ClaimEffects returned nil EffectClaim")
	}

	// Limit must be respected — never more than requested.
	if len(claim.Effects) > limit {
		t.Errorf("ClaimEffects returned %d effects, want ≤ %d (limit not respected)",
			len(claim.Effects), limit)
	}

	// LeaseToken must be non-empty.
	if claim.LeaseToken == "" {
		t.Errorf("ClaimEffects returned empty LeaseToken")
	}

	// LeaseExpiresAt must be a valid RFC3339 time in the future.
	if claim.LeaseExpiresAt == "" {
		t.Errorf("ClaimEffects returned empty LeaseExpiresAt")
	} else {
		expiresAt, parseErr := time.Parse(time.RFC3339, claim.LeaseExpiresAt)
		if parseErr != nil {
			t.Errorf("LeaseExpiresAt is not RFC3339: %q: %v", claim.LeaseExpiresAt, parseErr)
		} else if !expiresAt.After(time.Now()) {
			t.Errorf("LeaseExpiresAt is not in the future: %v", expiresAt)
		}
	}

	// Each claimed effect must have status 'leased'.
	for _, eff := range claim.Effects {
		if eff.Status != "leased" {
			t.Errorf("claimed effect %q has status %q, want 'leased'", eff.ID, eff.Status)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 3 — AckEffect with correct token succeeds
// ---------------------------------------------------------------------------

// TestAckEffectCorrectToken verifies that AckEffect(effectID, leaseToken)
// succeeds and marks the effect as 'delivered' when the token matches.
//
// Contract §5.1:
//
//	"wrkf.effect.ack params: { effectId, leaseToken, receipt? }"
//
// Regression guard: AckEffect must validate the supplied lease token and clear
// lease metadata when delivery succeeds.
func TestAckEffectCorrectToken(t *testing.T) {
	svc := setupEffectFixture(t, 2)

	claim, err := svc.ClaimEffects("test-adapter", 1, 30_000, "", "")
	if err != nil || claim == nil || len(claim.Effects) == 0 {
		t.Fatalf("ClaimEffects: err=%v claim=%v", err, claim)
	}
	effectID := claim.Effects[0].ID
	token := claim.LeaseToken

	eff, err := svc.AckEffect(effectID, token)
	if err != nil {
		t.Fatalf("AckEffect with correct token: expected success, got %v", err)
	}
	if eff == nil {
		t.Fatal("AckEffect returned nil Effect")
	}
	if eff.Status != "delivered" {
		t.Errorf("AckEffect: expected status 'delivered', got %q", eff.Status)
	}
	// Contract §6 inv 4: terminal paths MUST clear lease fields.
	if eff.LeasedBy != "" {
		t.Errorf("AckEffect: LeasedBy not cleared after ack: %q", eff.LeasedBy)
	}
	if eff.LeasedUntil != "" {
		t.Errorf("AckEffect: LeasedUntil not cleared after ack: %q", eff.LeasedUntil)
	}
}

// ---------------------------------------------------------------------------
// Test 4 — AckEffect with wrong token → WRKF_LEASE_CONFLICT
// ---------------------------------------------------------------------------

// TestAckEffectWrongToken verifies that AckEffect returns WRKF_LEASE_CONFLICT
// when the supplied token does not match the stored lease_token.
//
// Contract §5.1 / §3 error table:
//
//	"Wrong/expired token → WRKF_LEASE_CONFLICT"  (data.code, retryable=true)
//
// Regression guard: AckEffect must reject a token that does not match the
// stored lease token.
func TestAckEffectWrongToken(t *testing.T) {
	svc := setupEffectFixture(t, 2)

	claim, err := svc.ClaimEffects("test-adapter", 1, 30_000, "", "")
	if err != nil || claim == nil || len(claim.Effects) == 0 {
		t.Fatalf("ClaimEffects: err=%v claim=%v", err, claim)
	}
	effectID := claim.Effects[0].ID

	_, err = svc.AckEffect(effectID, "totally-wrong-lease-token")
	// Wrong tokens are rejected with the lease-conflict code.
	requireWrkfCode(t, err, "WRKF_LEASE_CONFLICT")
}

// ---------------------------------------------------------------------------
// Test 5 — AckEffect with expired token → WRKF_LEASE_CONFLICT
// ---------------------------------------------------------------------------

// TestAckEffectExpiredToken verifies that AckEffect returns WRKF_LEASE_CONFLICT
// when the lease has expired (leased_until < now), even if the token string
// is otherwise correct.
//
// Contract §6 inv 4:
//
//	"ack/fail MUST update only WHERE id=? AND status='leased' AND
//	 lease_token=? AND leased_until > now (else WRKF_LEASE_CONFLICT)"
//
// Regression guard: AckEffect must enforce leased_until, not only token
// equality.
func TestAckEffectExpiredToken(t *testing.T) {
	svc := setupEffectFixture(t, 2)

	// Claim with a 50 ms lease — short enough to expire in this test.
	claim, err := svc.ClaimEffects("test-adapter", 1, 50, "", "")
	if err != nil || claim == nil || len(claim.Effects) == 0 {
		t.Fatalf("ClaimEffects (50ms lease): err=%v claim=%v", err, claim)
	}
	effectID := claim.Effects[0].ID
	token := claim.LeaseToken

	// Wait for the lease to expire.
	time.Sleep(100 * time.Millisecond)

	_, err = svc.AckEffect(effectID, token)
	// Expired leases are rejected with the lease-conflict code.
	requireWrkfCode(t, err, "WRKF_LEASE_CONFLICT")
}

// ---------------------------------------------------------------------------
// Test 6 — FailEffect with wrong token → WRKF_LEASE_CONFLICT
// ---------------------------------------------------------------------------

// TestFailEffectWrongToken verifies that FailEffect returns WRKF_LEASE_CONFLICT
// when the supplied token does not match the stored lease_token.
//
// Contract §5.1:
//
//	"wrkf.effect.fail params: { effectId, leaseToken, reason, retryable? }"
//	"Wrong/expired token → WRKF_LEASE_CONFLICT"
//
// Regression guard: FailEffect must require the lease token and reject
// mismatches before mutating the effect.
func TestFailEffectWrongToken(t *testing.T) {
	svc := setupEffectFixture(t, 2)

	claim, err := svc.ClaimEffects("test-adapter", 1, 30_000, "", "")
	if err != nil || claim == nil || len(claim.Effects) == 0 {
		t.Fatalf("ClaimEffects: err=%v claim=%v", err, claim)
	}
	effectID := claim.Effects[0].ID

	// Wrong token + retryable=false.
	_, err = svc.FailEffect(effectID, "wrong-lease-token", "deliberate test failure", false)
	requireWrkfCode(t, err, "WRKF_LEASE_CONFLICT")
}

// ---------------------------------------------------------------------------
// Test 7 — FailEffect with correct token clears lease fields
// ---------------------------------------------------------------------------

// TestFailEffectCorrectToken verifies that FailEffect(id, leaseToken, reason,
// retryable) succeeds when the token matches, marks the effect 'failed', and
// clears the lease fields (leased_by, leased_until).
//
// Contract §6 inv 4:
//
//	"terminal/retry paths MUST clear lease_token/leased_by/leased_until."
//
// Regression guard: FailEffect must accept the token/retryable contract and
// clear lease metadata on successful terminal failure.
func TestFailEffectCorrectToken(t *testing.T) {
	svc := setupEffectFixture(t, 2)

	claim, err := svc.ClaimEffects("test-adapter", 1, 30_000, "", "")
	if err != nil || claim == nil || len(claim.Effects) == 0 {
		t.Fatalf("ClaimEffects: err=%v claim=%v", err, claim)
	}
	effectID := claim.Effects[0].ID
	token := claim.LeaseToken

	eff, err := svc.FailEffect(effectID, token, "deliberate test failure", false)
	if err != nil {
		t.Fatalf("FailEffect with correct token: expected success, got %v", err)
	}
	if eff == nil {
		t.Fatal("FailEffect returned nil Effect")
	}
	if eff.Status != "failed" {
		t.Errorf("FailEffect: expected status 'failed', got %q", eff.Status)
	}
	// Contract §6 inv 4: terminal paths MUST clear lease fields.
	if eff.LeasedBy != "" {
		t.Errorf("FailEffect: LeasedBy not cleared: %q", eff.LeasedBy)
	}
	if eff.LeasedUntil != "" {
		t.Errorf("FailEffect: LeasedUntil not cleared: %q", eff.LeasedUntil)
	}
}