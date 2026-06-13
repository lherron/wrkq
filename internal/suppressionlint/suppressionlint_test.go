// Package suppressionlint_test contains RED-phase tests for the suppression meta-lint scanner.
//
// All tests in this file are expected to FAIL until the scanner is implemented (T-04347).
// The stub in suppressionlint.go panics with "not implemented: see T-04347" so every test
// that calls ScanSource or Scan will produce a panic — the canonical RED.
//
// Test matrix (daedalus ruling, comment C-04362):
//  1. bare //nolint (no rule)                        → FINDING
//  2. ticketed single-rule ARCH-EXCEPTION             → NO finding + PerRuleCount[errcheck]=1
//  3a. malformed ARCH-EXCEPTION — missing ticket      → FINDING
//  3b. malformed ARCH-EXCEPTION — empty reason        → FINDING
//  4. file/package-level blanket disable              → FINDING
//  5. //nolint:all                                    → FINDING
//  6. ticketed multi-rule ARCH-EXCEPTION              → NO finding + counted once per rule
//  7. vendor fixture ignored when "vendor" excluded   → IGNORED by Scan
//  8. diagnostic conformance §3                       → message contains required fields
package suppressionlint_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/suppressionlint"
)

// ── Unit tests via ScanSource ─────────────────────────────────────────────────

// 1. bare //nolint (no rule) → FINDING
func TestScanSource_BareNolint_Finds(t *testing.T) {
	src := []byte(`package testdata

import "fmt"

func BareNolint() {
	fmt.Println("hello") //nolint
}
`)
	findings, err := suppressionlint.ScanSource("test.go", src)
	if err != nil {
		t.Fatalf("ScanSource error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for bare //nolint, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.File != "test.go" {
		t.Errorf("expected File=%q, got %q", "test.go", f.File)
	}
	if f.Line != 6 {
		t.Errorf("expected Line=6 (the nolint line), got %d", f.Line)
	}
	if f.Kind != suppressionlint.KindBareNolint {
		t.Errorf("expected Kind=%q, got %q", suppressionlint.KindBareNolint, f.Kind)
	}
}

// 2. ticketed single-rule → NO FINDING; PerRuleCount tested via Scan below.
func TestScanSource_ValidSingleRule_NoFinding(t *testing.T) {
	src := []byte(`package testdata

import "fmt"

func ValidSingle() {
	fmt.Println("hello") //nolint:errcheck // ARCH-EXCEPTION(T-04347): specific real reason
}
`)
	findings, err := suppressionlint.ScanSource("valid_single.go", src)
	if err != nil {
		t.Fatalf("ScanSource error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings for valid single-rule ARCH-EXCEPTION, got %d: %+v", len(findings), findings)
	}
}

// 3a. malformed ARCH-EXCEPTION — missing ticket → FINDING
func TestScanSource_MalformedARCH_MissingTicket_Finds(t *testing.T) {
	src := []byte(`package testdata

import "fmt"

func MalformedNoTicket() {
	fmt.Println("hello") //nolint:errcheck // ARCH-EXCEPTION(): reason without ticket
}
`)
	findings, err := suppressionlint.ScanSource("malformed_no_ticket.go", src)
	if err != nil {
		t.Fatalf("ScanSource error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for malformed ARCH-EXCEPTION (no ticket), got %d: %+v", len(findings), findings)
	}
	if findings[0].Kind != suppressionlint.KindUngoverned {
		t.Errorf("expected Kind=%q, got %q", suppressionlint.KindUngoverned, findings[0].Kind)
	}
}

// 3b. malformed ARCH-EXCEPTION — empty reason after colon → FINDING
func TestScanSource_MalformedARCH_EmptyReason_Finds(t *testing.T) {
	src := []byte(`package testdata

import "fmt"

func MalformedEmptyReason() {
	fmt.Println("hello") //nolint:errcheck // ARCH-EXCEPTION(T-04347):
}
`)
	findings, err := suppressionlint.ScanSource("malformed_empty_reason.go", src)
	if err != nil {
		t.Fatalf("ScanSource error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for malformed ARCH-EXCEPTION (empty reason), got %d: %+v", len(findings), findings)
	}
	if findings[0].Kind != suppressionlint.KindUngoverned {
		t.Errorf("expected Kind=%q, got %q", suppressionlint.KindUngoverned, findings[0].Kind)
	}
}

// 4. file/package-level blanket disable → FINDING (comment appears before package keyword)
func TestScanSource_FileLevelBlanketDisable_Finds(t *testing.T) {
	src := []byte(`//nolint:errcheck
package testdata

func BlanketDisable() {}
`)
	findings, err := suppressionlint.ScanSource("blanket_disable.go", src)
	if err != nil {
		t.Fatalf("ScanSource error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for file-level blanket disable, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Line != 1 {
		t.Errorf("expected Line=1 (pre-package comment), got %d", f.Line)
	}
	if f.Kind != suppressionlint.KindBlanketDisable {
		t.Errorf("expected Kind=%q, got %q", suppressionlint.KindBlanketDisable, f.Kind)
	}
}

// 5. //nolint:all → FINDING (banned regardless of ARCH-EXCEPTION)
func TestScanSource_NolintAll_Finds(t *testing.T) {
	src := []byte(`package testdata

import "fmt"

func NolintAll() {
	fmt.Println("hello") //nolint:all
}
`)
	findings, err := suppressionlint.ScanSource("nolint_all.go", src)
	if err != nil {
		t.Fatalf("ScanSource error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for //nolint:all, got %d: %+v", len(findings), findings)
	}
	if findings[0].Kind != suppressionlint.KindNolintAll {
		t.Errorf("expected Kind=%q, got %q", suppressionlint.KindNolintAll, findings[0].Kind)
	}
}

// 5b. //nolint:all with an ARCH-EXCEPTION still → FINDING (all-rules is unconditionally banned)
func TestScanSource_NolintAll_WithArchException_StillFinds(t *testing.T) {
	src := []byte(`package testdata

import "fmt"

func NolintAllTicketed() {
	fmt.Println("hello") //nolint:all // ARCH-EXCEPTION(T-04347): even with ticket, all-rules is banned
}
`)
	findings, err := suppressionlint.ScanSource("nolint_all_ticketed.go", src)
	if err != nil {
		t.Fatalf("ScanSource error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding for //nolint:all (even ticketed), got %d: %+v", len(findings), findings)
	}
	if findings[0].Kind != suppressionlint.KindNolintAll {
		t.Errorf("expected Kind=%q, got %q", suppressionlint.KindNolintAll, findings[0].Kind)
	}
}

// 6. ticketed multi-rule → NO FINDING; PerRuleCount counted once per rule (tested via Scan).
func TestScanSource_ValidMultiRule_NoFinding(t *testing.T) {
	src := []byte(`package testdata

import "fmt"

func ValidMulti() {
	fmt.Println("hello") //nolint:errcheck,gocyclo // ARCH-EXCEPTION(T-04347): reason spanning both rules
}
`)
	findings, err := suppressionlint.ScanSource("valid_multi.go", src)
	if err != nil {
		t.Fatalf("ScanSource error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings for valid multi-rule ARCH-EXCEPTION, got %d: %+v", len(findings), findings)
	}
}

// ── Diagnostic conformance (§3) ───────────────────────────────────────────────
//
// Every Finding.Message must contain:
//   - the exact sanctioned syntax: ARCH-EXCEPTION(T-  (so the reader knows the channel)
//   - the expected nolint form:    //nolint:<rule>
//   - fix/resolve guidance:        "resolve" or "fix" or "FIX"
//
// Finding.File and Finding.Line are tested by the unit tests above; Message carries the
// remaining §3 fields (expected-vs-got, rationale, channel syntax).

// 8. Diagnostic conformance on each violation kind.
func TestScanSource_DiagnosticConformance(t *testing.T) {
	cases := []struct {
		name string
		src  []byte
		file string
	}{
		{
			name: "bare_nolint",
			src:  []byte("package testdata\nvar x = 1 //nolint\n"),
			file: "bare.go",
		},
		{
			name: "malformed_no_ticket",
			src:  []byte("package testdata\nvar x = 1 //nolint:errcheck // ARCH-EXCEPTION(): reason\n"),
			file: "malformed_ticket.go",
		},
		{
			name: "malformed_empty_reason",
			src:  []byte("package testdata\nvar x = 1 //nolint:errcheck // ARCH-EXCEPTION(T-04347):\n"),
			file: "malformed_reason.go",
		},
		{
			name: "nolint_all",
			src:  []byte("package testdata\nvar x = 1 //nolint:all\n"),
			file: "all.go",
		},
		{
			name: "blanket_disable",
			src:  []byte("//nolint:errcheck\npackage testdata\nfunc F() {}\n"),
			file: "blanket.go",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := suppressionlint.ScanSource(tc.file, tc.src)
			if err != nil {
				t.Fatalf("ScanSource error: %v", err)
			}
			if len(findings) == 0 {
				t.Fatal("expected at least 1 finding for conformance check, got none")
			}
			for i, f := range findings {
				checkConformance(t, i, tc.file, f)
			}
		})
	}
}

// checkConformance asserts §3 diagnostic requirements on a single Finding.
func checkConformance(t *testing.T, idx int, file string, f suppressionlint.Finding) {
	t.Helper()

	if f.File == "" {
		t.Errorf("finding[%d]: File must be non-empty", idx)
	}
	if f.Line <= 0 {
		t.Errorf("finding[%d]: Line must be > 0, got %d", idx, f.Line)
	}
	if f.Kind == "" {
		t.Errorf("finding[%d]: Kind must be non-empty", idx)
	}
	if f.Message == "" {
		t.Errorf("finding[%d]: Message must be non-empty", idx)
		return // rest of checks are moot on empty message
	}
	// Must contain the sanctioned channel syntax so the reader knows what to write.
	if !strings.Contains(f.Message, "ARCH-EXCEPTION(T-") {
		t.Errorf("finding[%d] conformance: Message must contain sanctioned channel syntax \"ARCH-EXCEPTION(T-...\";\ngot: %s", idx, f.Message)
	}
	// Must show the expected nolint form with a rule scope.
	if !strings.Contains(f.Message, "//nolint:") {
		t.Errorf("finding[%d] conformance: Message must show expected form \"//nolint:<rule>...\";\ngot: %s", idx, f.Message)
	}
	// Must carry fix/resolve guidance (not a bare "disable not allowed").
	if !strings.Contains(f.Message, "resolve") &&
		!strings.Contains(f.Message, "fix") &&
		!strings.Contains(f.Message, "FIX") {
		t.Errorf("finding[%d] conformance: Message must contain \"resolve\" or \"fix\" guidance;\ngot: %s", idx, f.Message)
	}
}

// ── Directory scan tests via Scan ─────────────────────────────────────────────

func testdataDir(t *testing.T) string {
	t.Helper()
	return filepath.Join("testdata")
}

// 2+6. PerRuleCount accumulates governed suppressions, one entry per rule.
// testdata/ (excluding vendor/) contains:
//   valid_single.go  → errcheck=1
//   valid_multi.go   → errcheck=1, gocyclo=1
// Total expected: errcheck=2, gocyclo=1.
func TestScan_PerRuleCount(t *testing.T) {
	result, err := suppressionlint.Scan(testdataDir(t), []string{"vendor"})
	if err != nil {
		t.Fatalf("Scan error: %v", err)
	}
	if result.PerRuleCount == nil {
		t.Fatal("PerRuleCount must not be nil")
	}
	if got := result.PerRuleCount["errcheck"]; got != 2 {
		t.Errorf("PerRuleCount[errcheck]: expected 2, got %d", got)
	}
	if got := result.PerRuleCount["gocyclo"]; got != 1 {
		t.Errorf("PerRuleCount[gocyclo]: expected 1, got %d", got)
	}
}

// 7. Vendor fixture is ignored when excludeSegments contains "vendor".
func TestScan_VendorExcluded(t *testing.T) {
	dir := testdataDir(t)

	// With "vendor" excluded: no finding from vendor/somepkg/bare_nolint.go.
	excluded, err := suppressionlint.Scan(dir, []string{"vendor"})
	if err != nil {
		t.Fatalf("Scan (vendor excluded) error: %v", err)
	}
	for _, f := range excluded.Findings {
		if strings.Contains(filepath.ToSlash(f.File), "/vendor/") ||
			strings.HasPrefix(filepath.ToSlash(f.File), "vendor/") {
			t.Errorf("vendor finding should be excluded, got: %+v", f)
		}
	}

	// Without "vendor" excluded: bare_nolint inside vendor/ IS reported.
	all, err := suppressionlint.Scan(dir, []string{})
	if err != nil {
		t.Fatalf("Scan (no exclusions) error: %v", err)
	}
	vendorCount := 0
	for _, f := range all.Findings {
		if strings.Contains(filepath.ToSlash(f.File), "/vendor/") ||
			strings.Contains(filepath.ToSlash(f.File), "vendor/") {
			vendorCount++
		}
	}
	if vendorCount == 0 {
		t.Error("expected at least 1 finding from vendor/ when not excluded, got 0")
	}
}

// ── Fixture-based ScanSource round-trips ──────────────────────────────────────
// These verify that testdata/*.go files parse correctly and produce the expected signal.

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("readFixture %q: %v", name, err)
	}
	return src
}

func TestFixture_BareNolint(t *testing.T) {
	src := readFixture(t, "bare_nolint.go")
	findings, err := suppressionlint.ScanSource("bare_nolint.go", src)
	if err != nil {
		t.Fatalf("ScanSource error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("bare_nolint.go: expected 1 finding, got %d: %+v", len(findings), findings)
	}
}

func TestFixture_ValidSingle(t *testing.T) {
	src := readFixture(t, "valid_single.go")
	findings, err := suppressionlint.ScanSource("valid_single.go", src)
	if err != nil {
		t.Fatalf("ScanSource error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("valid_single.go: expected no findings, got %d: %+v", len(findings), findings)
	}
}

func TestFixture_MalformedNoTicket(t *testing.T) {
	src := readFixture(t, "malformed_no_ticket.go")
	findings, err := suppressionlint.ScanSource("malformed_no_ticket.go", src)
	if err != nil {
		t.Fatalf("ScanSource error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("malformed_no_ticket.go: expected 1 finding, got %d: %+v", len(findings), findings)
	}
}

func TestFixture_MalformedEmptyReason(t *testing.T) {
	src := readFixture(t, "malformed_empty_reason.go")
	findings, err := suppressionlint.ScanSource("malformed_empty_reason.go", src)
	if err != nil {
		t.Fatalf("ScanSource error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("malformed_empty_reason.go: expected 1 finding, got %d: %+v", len(findings), findings)
	}
}

func TestFixture_BlanketDisable(t *testing.T) {
	src := readFixture(t, "blanket_disable.go")
	findings, err := suppressionlint.ScanSource("blanket_disable.go", src)
	if err != nil {
		t.Fatalf("ScanSource error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("blanket_disable.go: expected 1 finding, got %d: %+v", len(findings), findings)
	}
}

func TestFixture_NolintAll(t *testing.T) {
	src := readFixture(t, "nolint_all.go")
	findings, err := suppressionlint.ScanSource("nolint_all.go", src)
	if err != nil {
		t.Fatalf("ScanSource error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("nolint_all.go: expected 1 finding, got %d: %+v", len(findings), findings)
	}
}

func TestFixture_ValidMulti(t *testing.T) {
	src := readFixture(t, "valid_multi.go")
	findings, err := suppressionlint.ScanSource("valid_multi.go", src)
	if err != nil {
		t.Fatalf("ScanSource error: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("valid_multi.go: expected no findings, got %d: %+v", len(findings), findings)
	}
}
