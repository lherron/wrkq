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

	// Every racer must succeed (an exhausted pool yields an empty claim, not an
	// error), no effect ID may appear in more than one claim result, and
	// together the racers must have leased the whole pool.
	seen := map[string]int{} // effectID → racer index that claimed it
	for i, r := range resultSlice {
		if r.err != nil {
			t.Fatalf("racer %d ClaimEffects: %v", i, r.err)
		}
		if r.claim == nil {
			t.Fatalf("racer %d returned nil EffectClaim", i)
		}
		for _, eff := range r.claim.Effects {
			if prev, dup := seen[eff.ID]; dup {
				t.Errorf("effect %q claimed by racer %d AND racer %d (duplicate lease violates §6 inv 4)",
					eff.ID, prev, i)
			}
			seen[eff.ID] = i
		}
	}
	if len(seen) != totalEffects {
		t.Errorf("racers leased %d distinct effects, want the whole pool of %d", len(seen), totalEffects)
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

	// With more pending effects than the limit, exactly limit are leased.
	if len(claim.Effects) != limit {
		t.Errorf("ClaimEffects returned %d effects, want exactly %d of the %d pending",
			len(claim.Effects), limit, totalEffects)
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
// Test 3 — terminal paths (ack, fail) clear the lease
// ---------------------------------------------------------------------------

// TestEffectTerminalPathsClearLease verifies that AckEffect and a
// non-retryable FailEffect, given the claim's lease token, move the effect to
// its terminal status and clear the lease fields.
//
// Contract §5.1:
//
//	"wrkf.effect.ack params: { effectId, leaseToken, receipt? }"
//	"wrkf.effect.fail params: { effectId, leaseToken, reason, retryable? }"
//
// Contract §6 inv 4:
//
//	"terminal/retry paths MUST clear lease_token/leased_by/leased_until."
func TestEffectTerminalPathsClearLease(t *testing.T) {
	cases := []struct {
		name       string
		settle     func(svc *Service, effectID, token string) (*Effect, error)
		wantStatus string
	}{
		{
			name: "ack",
			settle: func(svc *Service, effectID, token string) (*Effect, error) {
				return svc.AckEffect(effectID, token)
			},
			wantStatus: "delivered",
		},
		{
			name: "fail-not-retryable",
			settle: func(svc *Service, effectID, token string) (*Effect, error) {
				return svc.FailEffect(effectID, token, "deliberate test failure", false)
			},
			wantStatus: "failed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := setupEffectFixture(t, 2)

			claim, err := svc.ClaimEffects("test-adapter", 1, 30_000, "", "")
			if err != nil || claim == nil || len(claim.Effects) == 0 {
				t.Fatalf("ClaimEffects: err=%v claim=%v", err, claim)
			}

			eff, err := tc.settle(svc, claim.Effects[0].ID, claim.LeaseToken)
			if err != nil {
				t.Fatalf("%s with correct token: expected success, got %v", tc.name, err)
			}
			if eff == nil {
				t.Fatalf("%s returned nil Effect", tc.name)
			}
			if eff.Status != tc.wantStatus {
				t.Errorf("%s: expected status %q, got %q", tc.name, tc.wantStatus, eff.Status)
			}
			if eff.LeasedBy != "" {
				t.Errorf("%s: LeasedBy not cleared: %q", tc.name, eff.LeasedBy)
			}
			if eff.LeasedUntil != "" {
				t.Errorf("%s: LeasedUntil not cleared: %q", tc.name, eff.LeasedUntil)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test 4 — ack/fail without the current lease → WRKF_LEASE_CONFLICT
// ---------------------------------------------------------------------------

// TestEffectAckAndFailRefuseWithoutCurrentLease verifies the lease fence on the
// terminal paths: a wrong token, or the right token after the lease expired,
// is refused WRKF_LEASE_CONFLICT.
//
// Contract §5.1 / §3 error table:
//
//	"Wrong/expired token → WRKF_LEASE_CONFLICT"  (data.code, retryable=true)
//
// Contract §6 inv 4:
//
//	"ack/fail MUST update only WHERE id=? AND status='leased' AND
//	 lease_token=? AND leased_until > now (else WRKF_LEASE_CONFLICT)"
func TestEffectAckAndFailRefuseWithoutCurrentLease(t *testing.T) {
	cases := []struct {
		name string
		// leaseMs is the claim's lease; expire sleeps past it before settling.
		leaseMs int64
		expire  bool
		settle  func(svc *Service, effectID, token string) error
	}{
		{
			name:    "ack-wrong-token",
			leaseMs: 30_000,
			settle: func(svc *Service, effectID, _ string) error {
				_, err := svc.AckEffect(effectID, "totally-wrong-lease-token")
				return err
			},
		},
		{
			// The token is correct; only leased_until has passed.
			name:    "ack-after-lease-expiry",
			leaseMs: 50,
			expire:  true,
			settle: func(svc *Service, effectID, token string) error {
				_, err := svc.AckEffect(effectID, token)
				return err
			},
		},
		{
			name:    "fail-wrong-token",
			leaseMs: 30_000,
			settle: func(svc *Service, effectID, _ string) error {
				_, err := svc.FailEffect(effectID, "wrong-lease-token", "deliberate test failure", false)
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := setupEffectFixture(t, 2)

			claim, err := svc.ClaimEffects("test-adapter", 1, tc.leaseMs, "", "")
			if err != nil || claim == nil || len(claim.Effects) == 0 {
				t.Fatalf("ClaimEffects: err=%v claim=%v", err, claim)
			}
			if tc.expire {
				time.Sleep(time.Duration(2*tc.leaseMs) * time.Millisecond)
			}

			requireWrkfCode(t, tc.settle(svc, claim.Effects[0].ID, claim.LeaseToken), "WRKF_LEASE_CONFLICT")
		})
	}
}
