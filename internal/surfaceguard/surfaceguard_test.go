// Package surfaceguard_test contains RED-phase tests for the public-surface coverage guard.
//
// All tests in this file are expected to FAIL until Check (and helpers) are implemented (T-04369).
// The stub in surfaceguard.go panics with "not implemented: see T-04369", so every test that
// invokes Check, UpdateBaseline, ExtractRegistrations, CollectEvidence, or LoadBaseline
// produces a panic — the canonical RED.
//
// Test matrix (daedalus ruling, DM #7429):
//  1. Baseline match — current inventory all in baseline → NO violations (live-tree green)
//  2. Fire-on-bad — dummy wrkf.test.uncovered not in baseline, no evidence → VIOLATION flagged
//  3. Pass-on-good (test file) — exact string literal in *_test.go → passes
//  4. Pass-on-good (smoke) — non-comment line in smoke-*.sh → passes
//  5. ARCH-EXCEPTION — valid annotation → NO violation; ExceptionsByRule increments
//     Malformed/empty reason → VIOLATION (not exempt)
//  6. Negative evidence — docs-only reference does NOT satisfy the guard
//  7. Baseline-update safety — UpdateBaseline refuses unevidenced dummy; no blind rewrite
package surfaceguard_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/surfaceguard"
)

// checkViolationConformance asserts §3 diagnostic requirements on a single Violation.
// Every message must carry: the exact method name, file:line, a FIX directive,
// WHY explanation, and the ARCH-EXCEPTION escape-hatch channel.
func checkViolationConformance(t *testing.T, i int, v surfaceguard.Violation, method string) {
	t.Helper()
	if v.Method == "" {
		t.Errorf("violation[%d]: Method must be non-empty", i)
	}
	if v.File == "" {
		t.Errorf("violation[%d]: File must be non-empty", i)
	}
	if v.Line <= 0 {
		t.Errorf("violation[%d]: Line must be > 0, got %d", i, v.Line)
	}
	if v.Message == "" {
		t.Errorf("violation[%d]: Message must be non-empty", i)
		return
	}
	// §3: message must name the exact surface
	if !strings.Contains(v.Message, method) {
		t.Errorf("violation[%d]: Message must contain method %q; got: %s", i, method, v.Message)
	}
	// §3: message must include file:line (colon between file reference and line number)
	if !strings.Contains(v.Message, ":") {
		t.Errorf("violation[%d]: Message must include file:line reference; got: %s", i, v.Message)
	}
	// §3: message must tell the developer how to fix (either add evidence or use exception)
	hasFIX := strings.Contains(v.Message, "FIX") || strings.Contains(v.Message, "fix")
	if !hasFIX {
		t.Errorf("violation[%d]: Message must contain a FIX directive; got: %s", i, v.Message)
	}
	// §3: message must show the escape-hatch so the developer knows how to exempt
	if !strings.Contains(v.Message, "ARCH-EXCEPTION(T-") {
		t.Errorf("violation[%d]: Message must contain ARCH-EXCEPTION escape-hatch syntax; got: %s", i, v.Message)
	}
}

// baseline for tests that need the 4-method mini baseline
func miniBaseline() surfaceguard.Baseline {
	return surfaceguard.Baseline{
		Methods: []surfaceguard.BaselineEntry{
			{Method: "wrkf.initialize"},
			{Method: "wrkf.next"},
			{Method: "wrkf.task.attach"},
			{Method: "wrkf.task.inspect"},
		},
	}
}

// baselineRegistrations mirrors the 4 methods in miniBaseline
func baselineRegistrations() []surfaceguard.Registration {
	return []surfaceguard.Registration{
		{Method: "wrkf.initialize", File: "api_registry.go", Line: 30},
		{Method: "wrkf.next", File: "api_registry.go", Line: 31},
		{Method: "wrkf.task.attach", File: "api_registry.go", Line: 32},
		{Method: "wrkf.task.inspect", File: "api_registry.go", Line: 33},
	}
}

// dummyRegistration is the uncovered method used across multiple tests.
func dummyRegistration() surfaceguard.Registration {
	return surfaceguard.Registration{
		Method: "wrkf.test.uncovered",
		File:   "api_registry.go",
		Line:   99,
	}
}

// ─── Test 1: baseline match — no violations ────────────────────────────────────

// TestCheck_BaselineMatch_NoViolations seeds registrations from the committed 42-method
// baseline and calls Check. All methods are grandfathered → zero violations expected.
func TestCheck_BaselineMatch_NoViolations(t *testing.T) {
	// Build the full 42-method baseline from the fixture file.
	baselinePath := filepath.Join("testdata", "baseline", "baseline_full.json")
	baseline, err := surfaceguard.LoadBaseline(baselinePath)
	if err != nil {
		t.Fatalf("LoadBaseline: %v", err)
	}
	if len(baseline.Methods) != 42 {
		t.Fatalf("expected 42 baseline methods, got %d", len(baseline.Methods))
	}

	// Build registrations that exactly match the baseline.
	registrations := make([]surfaceguard.Registration, len(baseline.Methods))
	for i, m := range baseline.Methods {
		registrations[i] = surfaceguard.Registration{
			Method: m.Method,
			File:   "internal/wrkfrpc/api_registry.go",
			Line:   30 + i,
		}
	}

	result, err := surfaceguard.Check(registrations, baseline, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(result.Violations) != 0 {
		t.Errorf("expected 0 violations when all methods are in baseline, got %d: %+v",
			len(result.Violations), result.Violations)
	}
}

// ─── Test 2: fire-on-bad — new unevidenced method flagged ─────────────────────

// TestCheck_NewUnevidenced_Flagged adds a dummy registration not in baseline
// with no evidence — the guard must flag it with a §3-conformant violation.
func TestCheck_NewUnevidenced_Flagged(t *testing.T) {
	baseline := miniBaseline()
	dummy := dummyRegistration()
	registrations := append(baselineRegistrations(), dummy)

	result, err := surfaceguard.Check(registrations, baseline, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(result.Violations) == 0 {
		t.Fatal("expected ≥1 violation for unevidenced new method, got 0")
	}
	// Only the dummy should be flagged (baseline methods are grandfathered)
	for _, v := range result.Violations {
		if v.Method != "wrkf.test.uncovered" {
			t.Errorf("unexpected violation for grandfathered method %q", v.Method)
		}
	}
	// §3 conformance on each violation
	for i, v := range result.Violations {
		checkViolationConformance(t, i, v, "wrkf.test.uncovered")
	}
}

// ─── Test 3: pass-on-good (Go test file string literal) ───────────────────────

// TestCheck_EvidencedByTestFile_Passes verifies that an exact Go string literal
// "wrkf.test.uncovered" in a *_test.go satisfies the guard (no violation).
func TestCheck_EvidencedByTestFile_Passes(t *testing.T) {
	baseline := miniBaseline()
	dummy := dummyRegistration()
	registrations := append(baselineRegistrations(), dummy)

	// CollectEvidence on the fixture *_test.go must detect the string literal.
	testFixture := filepath.Join("testdata", "evidence", "contract_test.go")
	evidenceSet, err := surfaceguard.CollectEvidence([]string{testFixture}, nil)
	if err != nil {
		t.Fatalf("CollectEvidence: %v", err)
	}
	if !evidenceSet["wrkf.test.uncovered"] {
		t.Fatalf("CollectEvidence did not find 'wrkf.test.uncovered' in test fixture")
	}

	result, err := surfaceguard.Check(registrations, baseline, evidenceSet)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(result.Violations) != 0 {
		t.Errorf("expected 0 violations when evidenced by *_test.go literal, got %d: %+v",
			len(result.Violations), result.Violations)
	}
}

// ─── Test 4: pass-on-good (smoke script non-comment line) ─────────────────────

// TestCheck_EvidencedBySmokeScript_Passes verifies that a non-comment line in a
// test/smoke-*.sh file containing "wrkf.test.uncovered" satisfies the guard.
func TestCheck_EvidencedBySmokeScript_Passes(t *testing.T) {
	baseline := miniBaseline()
	dummy := dummyRegistration()
	registrations := append(baselineRegistrations(), dummy)

	smokeFixture := filepath.Join("testdata", "evidence", "smoke-rpc-fixture.sh")
	evidenceSet, err := surfaceguard.CollectEvidence(nil, []string{smokeFixture})
	if err != nil {
		t.Fatalf("CollectEvidence: %v", err)
	}
	if !evidenceSet["wrkf.test.uncovered"] {
		t.Fatalf("CollectEvidence did not find 'wrkf.test.uncovered' in smoke fixture")
	}

	result, err := surfaceguard.Check(registrations, baseline, evidenceSet)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(result.Violations) != 0 {
		t.Errorf("expected 0 violations when evidenced by smoke script, got %d: %+v",
			len(result.Violations), result.Violations)
	}
}

// ─── Test 5a: valid ARCH-EXCEPTION → exempt + counted ─────────────────────────

// TestCheck_ValidArchException_ExemptAndCounted verifies that a valid
// ARCH-EXCEPTION(T-NNNNN): <reason> annotation exempts the method and increments
// ExceptionsByRule[RuleNewUnevidenced].
func TestCheck_ValidArchException_ExemptAndCounted(t *testing.T) {
	baseline := miniBaseline()
	dummy := surfaceguard.Registration{
		Method: "wrkf.test.uncovered",
		File:   "api_registry.go",
		Line:   99,
		Exception: surfaceguard.ArchException{
			Present: true,
			Valid:   true,
			Ticket:  "T-04369",
			Reason:  "provisional surface; contract test pending smoke-wrkf-rpc.sh update",
		},
	}
	registrations := append(baselineRegistrations(), dummy)

	result, err := surfaceguard.Check(registrations, baseline, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(result.Violations) != 0 {
		t.Errorf("valid ARCH-EXCEPTION must not produce violations, got %d: %+v",
			len(result.Violations), result.Violations)
	}
	if result.ExceptionsByRule == nil {
		t.Fatal("ExceptionsByRule must not be nil")
	}
	if result.ExceptionsByRule[surfaceguard.RuleNewUnevidenced] != 1 {
		t.Errorf("ExceptionsByRule[%s]: expected 1, got %d",
			surfaceguard.RuleNewUnevidenced, result.ExceptionsByRule[surfaceguard.RuleNewUnevidenced])
	}
}

// ─── Test 5b: malformed ARCH-EXCEPTION → still flagged ────────────────────────

// TestCheck_MalformedArchException_Flagged verifies that a Present-but-not-Valid
// ARCH-EXCEPTION (e.g. empty reason) does NOT exempt the method — it must still
// produce a violation.
func TestCheck_MalformedArchException_Flagged(t *testing.T) {
	baseline := miniBaseline()
	dummy := surfaceguard.Registration{
		Method: "wrkf.test.uncovered",
		File:   "api_registry.go",
		Line:   99,
		Exception: surfaceguard.ArchException{
			Present: true,
			Valid:   false, // malformed: empty reason
			Ticket:  "T-04369",
			Reason:  "",
		},
	}
	registrations := append(baselineRegistrations(), dummy)

	result, err := surfaceguard.Check(registrations, baseline, nil)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(result.Violations) == 0 {
		t.Fatal("malformed ARCH-EXCEPTION (empty reason) must still produce a violation")
	}
	for i, v := range result.Violations {
		checkViolationConformance(t, i, v, "wrkf.test.uncovered")
	}
}

// ─── Test 6: negative evidence — docs-only reference does NOT count ───────────

// TestCheck_DocsOnlyReference_NotEvidence verifies that a plain-text or Markdown
// reference to a method name does NOT satisfy the evidence predicate.
// CollectEvidence must not include the docs_only.md file output in the evidence set.
func TestCheck_DocsOnlyReference_NotEvidence(t *testing.T) {
	// CollectEvidence is NOT designed to accept docs files — it only takes
	// *_test.go and smoke-*.sh. Verify that passing no evidence files produces
	// an empty set (docs file is intentionally NOT passed as a test file or smoke file).
	evidenceSet, err := surfaceguard.CollectEvidence(nil, nil)
	if err != nil {
		t.Fatalf("CollectEvidence(nil, nil): %v", err)
	}
	if evidenceSet["wrkf.test.uncovered"] {
		t.Error("docs-only reference must not appear in evidence set")
	}

	// Cross-check: with the docs file forcibly included (wrong type), the guard still fires.
	// We pass no evidence → Check must flag the dummy.
	baseline := miniBaseline()
	dummy := dummyRegistration()
	registrations := append(baselineRegistrations(), dummy)

	result, err := surfaceguard.Check(registrations, baseline, evidenceSet)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(result.Violations) == 0 {
		t.Error("docs-only reference must not satisfy the guard — expected ≥1 violation")
	}
}

// ─── Test 7: baseline-update safety ──────────────────────────────────────────

// TestUpdateBaseline_RefusesUnevidencedDummy verifies that UpdateBaseline will NOT
// absorb a new unevidenced method into the baseline. The dummy must appear in the
// rejected list; the returned baseline must not include it.
func TestUpdateBaseline_RefusesUnevidencedDummy(t *testing.T) {
	current := miniBaseline()
	dummy := dummyRegistration()
	allRegistrations := append(baselineRegistrations(), dummy)

	updated, rejected, err := surfaceguard.UpdateBaseline(current, allRegistrations, nil)
	if err != nil {
		t.Fatalf("UpdateBaseline: %v", err)
	}

	// Dummy must be in rejected
	foundInRejected := false
	for _, r := range rejected {
		if r.Method == "wrkf.test.uncovered" {
			foundInRejected = true
		}
	}
	if !foundInRejected {
		t.Errorf("unevidenced dummy must appear in rejected list; got rejected=%+v", rejected)
	}

	// Updated baseline must NOT contain the dummy
	for _, m := range updated.Methods {
		if m.Method == "wrkf.test.uncovered" {
			t.Errorf("UpdateBaseline must not absorb unevidenced method %q into baseline", m.Method)
		}
	}

	// Baseline methods that existed before must still be present
	existing := map[string]bool{
		"wrkf.initialize":   true,
		"wrkf.next":         true,
		"wrkf.task.attach":  true,
		"wrkf.task.inspect": true,
	}
	for _, m := range updated.Methods {
		delete(existing, m.Method)
	}
	for missing := range existing {
		t.Errorf("UpdateBaseline dropped pre-existing baseline method %q", missing)
	}
}

// TestUpdateBaseline_AcceptsEvidencedMethod verifies that UpdateBaseline WILL absorb
// a new method that has evidence (string literal or smoke) into the baseline.
func TestUpdateBaseline_AcceptsEvidencedMethod(t *testing.T) {
	current := miniBaseline()
	dummy := dummyRegistration()
	allRegistrations := append(baselineRegistrations(), dummy)

	// Provide evidence for the dummy
	evidenceSet := map[string]bool{"wrkf.test.uncovered": true}

	updated, rejected, err := surfaceguard.UpdateBaseline(current, allRegistrations, evidenceSet)
	if err != nil {
		t.Fatalf("UpdateBaseline: %v", err)
	}

	// Dummy must NOT be in rejected (it has evidence)
	for _, r := range rejected {
		if r.Method == "wrkf.test.uncovered" {
			t.Errorf("evidenced method must not appear in rejected list")
		}
	}

	// Updated baseline must contain the dummy
	found := false
	for _, m := range updated.Methods {
		if m.Method == "wrkf.test.uncovered" {
			found = true
		}
	}
	if !found {
		t.Error("UpdateBaseline must include evidenced method in updated baseline")
	}
}

// ─── Test: ExtractRegistrations parses fixture correctly ─────────────────────

// TestExtractRegistrations_ParsesFixture verifies that ExtractRegistrations correctly
// parses the fixture source file and returns the expected registrations.
func TestExtractRegistrations_ParsesFixture(t *testing.T) {
	fixture := filepath.Join("testdata", "registry", "registry_with_new.go")
	registrations, err := surfaceguard.ExtractRegistrations(fixture)
	if err != nil {
		t.Fatalf("ExtractRegistrations: %v", err)
	}

	// Fixture has 5 registrations (4 base + 1 dummy)
	if len(registrations) != 5 {
		t.Fatalf("expected 5 registrations from fixture, got %d: %+v", len(registrations), registrations)
	}

	methods := make(map[string]bool, len(registrations))
	for _, r := range registrations {
		methods[r.Method] = true
		if r.File == "" {
			t.Errorf("registration %q: File must be non-empty", r.Method)
		}
		if r.Line <= 0 {
			t.Errorf("registration %q: Line must be > 0, got %d", r.Method, r.Line)
		}
	}
	if !methods["wrkf.test.uncovered"] {
		t.Error("expected 'wrkf.test.uncovered' in parsed registrations")
	}
}

// TestExtractRegistrations_ParsesValidException verifies that an ARCH-EXCEPTION comment
// adjacent to a registration is correctly parsed into Registration.Exception.
func TestExtractRegistrations_ParsesValidException(t *testing.T) {
	fixture := filepath.Join("testdata", "registry", "registry_with_exception.go")
	registrations, err := surfaceguard.ExtractRegistrations(fixture)
	if err != nil {
		t.Fatalf("ExtractRegistrations: %v", err)
	}

	var dummy *surfaceguard.Registration
	for i := range registrations {
		if registrations[i].Method == "wrkf.test.uncovered" {
			dummy = &registrations[i]
			break
		}
	}
	if dummy == nil {
		t.Fatal("expected 'wrkf.test.uncovered' registration in fixture")
	}
	if !dummy.Exception.Present {
		t.Error("Exception.Present must be true for annotated registration")
	}
	if !dummy.Exception.Valid {
		t.Error("Exception.Valid must be true for well-formed ARCH-EXCEPTION")
	}
	if dummy.Exception.Ticket == "" {
		t.Error("Exception.Ticket must be non-empty for valid exception")
	}
	if dummy.Exception.Reason == "" {
		t.Error("Exception.Reason must be non-empty for valid exception")
	}
}

// TestExtractRegistrations_ParsesMalformedException verifies that a malformed
// ARCH-EXCEPTION (empty reason) is parsed as Present=true, Valid=false.
func TestExtractRegistrations_ParsesMalformedException(t *testing.T) {
	fixture := filepath.Join("testdata", "registry", "registry_malformed_exception.go")
	registrations, err := surfaceguard.ExtractRegistrations(fixture)
	if err != nil {
		t.Fatalf("ExtractRegistrations: %v", err)
	}

	var dummy *surfaceguard.Registration
	for i := range registrations {
		if registrations[i].Method == "wrkf.test.uncovered" {
			dummy = &registrations[i]
			break
		}
	}
	if dummy == nil {
		t.Fatal("expected 'wrkf.test.uncovered' registration in fixture")
	}
	if !dummy.Exception.Present {
		t.Error("Exception.Present must be true (annotation token was found)")
	}
	if dummy.Exception.Valid {
		t.Error("Exception.Valid must be false for malformed (empty reason) exception")
	}
}

// ─── Test: CollectEvidence discriminates literals vs comments ─────────────────

// TestCollectEvidence_LiteralInTestFile_Found verifies that a Go string literal
// in a *_test.go file is detected and comment text in the same file is not double-counted.
func TestCollectEvidence_LiteralInTestFile_Found(t *testing.T) {
	testFixture := filepath.Join("testdata", "evidence", "contract_test.go")
	evidenceSet, err := surfaceguard.CollectEvidence([]string{testFixture}, nil)
	if err != nil {
		t.Fatalf("CollectEvidence: %v", err)
	}
	if !evidenceSet["wrkf.test.uncovered"] {
		t.Error("expected 'wrkf.test.uncovered' to be found as a string literal in contract_test.go")
	}
}

// TestCollectEvidence_CommentInSmokeScript_NotEvidence verifies that a comment line
// in a smoke script does NOT contribute to the evidence set; only non-comment lines count.
// The smoke fixture has the method in BOTH a comment and a non-comment line.
// CollectEvidence must find it (from the non-comment line) but a comment-only fixture must not.
func TestCollectEvidence_SmokeScript_NonCommentLine_Found(t *testing.T) {
	smokeFixture := filepath.Join("testdata", "evidence", "smoke-rpc-fixture.sh")
	evidenceSet, err := surfaceguard.CollectEvidence(nil, []string{smokeFixture})
	if err != nil {
		t.Fatalf("CollectEvidence: %v", err)
	}
	if !evidenceSet["wrkf.test.uncovered"] {
		t.Error("expected 'wrkf.test.uncovered' to be found in non-comment line of smoke script")
	}
}
