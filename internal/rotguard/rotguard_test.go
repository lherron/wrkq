// Package rotguard_test contains RED-phase tests for the stale-TDD-comment sensor.
//
// All tests in this file are expected to FAIL until Check is implemented (T-04368).
// The stub in rotguard.go panics with "not implemented: see T-04368" so every test
// that calls Check produces a panic — the canonical RED.
//
// Test matrix (daedalus ruling, comment C-04379):
//  1. stale "compile failure" / "RED TODAY" with TestsGreen=true    → VIOLATIONS (fire-on-bad)
//  2. benign "does not exist" / "not yet implemented" (unpaired)    → NO violations (no FP)
//  3. stale comments present but TestsGreen=false                   → NO violations (predicate yields)
//  4. ARCH-EXCEPTION comment group                                  → NO violations; ExceptionsByRule["stale-tdd-comment"]==1
package rotguard_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/rotguard"
)

// checkViolationConformance asserts §3 diagnostic requirements on a single Violation.
// Every message must carry: "stale TDD" rationale + ARCH-EXCEPTION escape-hatch channel.
func checkViolationConformance(t *testing.T, i int, v rotguard.Violation) {
	t.Helper()
	if v.File == "" {
		t.Errorf("violation[%d]: File must be non-empty", i)
	}
	if v.Line <= 0 {
		t.Errorf("violation[%d]: Line must be > 0, got %d", i, v.Line)
	}
	if v.Marker == "" {
		t.Errorf("violation[%d]: Marker must be non-empty", i)
	}
	if v.Message == "" {
		t.Errorf("violation[%d]: Message must be non-empty", i)
		return
	}
	// §3: message must mention "stale TDD" so the developer knows what rule fired
	if !strings.Contains(v.Message, "stale TDD") {
		t.Errorf("violation[%d]: Message must mention 'stale TDD'; got: %s", i, v.Message)
	}
	// §3: message must show the escape-hatch channel so the developer knows how to exempt
	if !strings.Contains(v.Message, "ARCH-EXCEPTION(T-") {
		t.Errorf("violation[%d]: Message must contain ARCH-EXCEPTION escape-hatch syntax; got: %s", i, v.Message)
	}
}

// 1. stale "compile failure" / "RED TODAY" comment groups, TestsGreen=true → VIOLATIONS
//
// Fixture: testdata/stale/effect_lease_test.go contains two separate comment groups,
// one carrying "RED TODAY" and one carrying "compile failure". Both should fire.
func TestCheck_StaleComments_TestsGreen_Flags(t *testing.T) {
	scope := rotguard.Scope{
		Dir:        filepath.Join("testdata", "stale"),
		TestsGreen: true,
	}
	result, err := rotguard.Check(scope)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	// Fixture has two distinct comment groups — expect exactly 2 violations
	if len(result.Violations) < 2 {
		t.Fatalf("expected ≥2 violations (one per stale comment group), got %d: %+v",
			len(result.Violations), result.Violations)
	}
	for i, v := range result.Violations {
		checkViolationConformance(t, i, v)
	}
	// Both hard markers must appear
	markers := make(map[string]bool)
	for _, v := range result.Violations {
		markers[v.Marker] = true
	}
	if !markers["RED TODAY"] {
		t.Error("expected a violation with Marker='RED TODAY'")
	}
	if !markers["compile failure"] {
		t.Error("expected a violation with Marker='compile failure'")
	}
}

// 2. benign unpaired comments, TestsGreen=true → NO violations (no false positives)
//
// Fixture: testdata/benign/todo_test.go contains:
//   - "// TODO: future work — this feature does not exist yet"   (generic, no hard marker)
//   - "// The old API does not exist in this version"            (standalone "does not exist")
//   - "// TODO: not yet implemented — waiting on upstream"       (standalone, no hard marker)
// None of these are paired with "RED TODAY" or "compile failure" → all must pass.
func TestCheck_BenignComments_NotFlagged(t *testing.T) {
	scope := rotguard.Scope{
		Dir:        filepath.Join("testdata", "benign"),
		TestsGreen: true,
	}
	result, err := rotguard.Check(scope)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(result.Violations) != 0 {
		t.Errorf("expected 0 violations for benign comments, got %d: %+v",
			len(result.Violations), result.Violations)
	}
}

// 3. same stale fixture, TestsGreen=false → NOT flagged (test failure is controlling signal)
func TestCheck_StaleComments_TestsRed_NoViolations(t *testing.T) {
	scope := rotguard.Scope{
		Dir:        filepath.Join("testdata", "stale"),
		TestsGreen: false,
	}
	result, err := rotguard.Check(scope)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(result.Violations) != 0 {
		t.Errorf("expected 0 violations when TestsGreen=false (test failure controls), got %d: %+v",
			len(result.Violations), result.Violations)
	}
}

// 4. ARCH-EXCEPTION in comment group → exempt (no violation); ExceptionsByRule["stale-tdd-comment"]==1
//
// Fixture: testdata/arch-exception/effect_lease_test.go has ONE comment group containing
// both "RED TODAY" / "compile failure" AND "ARCH-EXCEPTION(T-04368): <reason>".
// This single group must not produce a violation but must increment the exception counter.
func TestCheck_ArchException_NotFlagged_Counted(t *testing.T) {
	scope := rotguard.Scope{
		Dir:        filepath.Join("testdata", "arch-exception"),
		TestsGreen: true,
	}
	result, err := rotguard.Check(scope)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(result.Violations) != 0 {
		t.Errorf("ARCH-EXCEPTION group must not produce violations, got %d: %+v",
			len(result.Violations), result.Violations)
	}
	if result.ExceptionsByRule == nil {
		t.Fatal("ExceptionsByRule must not be nil")
	}
	if result.ExceptionsByRule["stale-tdd-comment"] != 1 {
		t.Errorf("ExceptionsByRule[stale-tdd-comment]: expected 1, got %d",
			result.ExceptionsByRule["stale-tdd-comment"])
	}
}
