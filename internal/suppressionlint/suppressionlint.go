// Package suppressionlint scans first-party Go source for ungoverned nolint suppressions.
//
// The ONLY sanctioned suppression form is a same-physical-comment-line, rule-scoped annotation:
//
//	//nolint:<rule>[,<rule>...] // ARCH-EXCEPTION(T-NNNNN): <non-whitespace reason>
//
// where T-NNNNN matches T-[0-9]{5,} (format-only; no DB lookup) and the reason is non-empty
// after trim. Bare //nolint, //nolint:all, file/package-level blanket disables, and any
// //nolint without a valid ARCH-EXCEPTION are all violations.
//
// Scope: first-party Go only. The live Scan target excludes vendor/ and testdata/.
// Dir-level fixture tests pass excludeSegments that do NOT include "testdata" so fixtures
// under testdata/ are actually scanned.
package suppressionlint

// Finding represents a single ungoverned suppression found by the scanner.
//
// File and Line identify the source location.
// Kind categorizes the violation (see Kind* constants below).
// Message is a §3-conformant diagnostic: it must contain expected-vs-got, the exact
// sanctioned syntax (//nolint:<rule> ... ARCH-EXCEPTION(T-...: reason)), a one-line
// rationale, and resolve-or-ARCH-EXCEPTION channel guidance. Never a bare "disable not
// allowed" without a concrete fix path.
type Finding struct {
	File    string
	Line    int
	Kind    string
	Message string
}

// Kind constants — valid values for Finding.Kind.
const (
	// KindBareNolint: //nolint with no rule list.
	KindBareNolint = "bare-nolint"
	// KindNolintAll: //nolint:all (rule list "all" is banned).
	KindNolintAll = "nolint-all"
	// KindBlanketDisable: nolint comment appears at file/package scope (before package keyword).
	KindBlanketDisable = "blanket-disable"
	// KindUngoverned: rule-scoped nolint but ARCH-EXCEPTION is missing or malformed
	// (missing ticket, missing reason, or wrong ticket format).
	KindUngoverned = "ungoverned"
)

// Result is the aggregate output of a directory scan.
type Result struct {
	// Findings lists every ungoverned suppression found in the scanned tree.
	Findings []Finding

	// PerRuleCount tallies governed (ticketed, passing) suppressions by rule name.
	// This is the per-rule suppression-rate sensor. Only suppressions that fully
	// satisfy the ARCH-EXCEPTION grammar contribute; violations do not.
	// Multi-rule annotations count once per named rule.
	PerRuleCount map[string]int
}

// ScanSource scans Go source bytes and returns all ungoverned nolint findings.
// filename is used for diagnostics (Finding.File) only; no file I/O is performed.
// A nil or empty src is valid and returns no findings.
//
// This is the pure, unit-testable entry point. Drive table tests through this function.
func ScanSource(filename string, src []byte) ([]Finding, error) {
	panic("not implemented: see T-04347")
}

// Scan walks root recursively and scans every .go file whose path contains no segment
// listed in excludeSegments. It returns aggregated findings and per-rule suppression counts.
//
// The live target passes excludeSegments=[]string{"vendor","testdata"}.
// Dir-level fixture tests pass []string{"vendor"} (omitting "testdata") so that testdata/
// fixtures are actually scanned.
func Scan(root string, excludeSegments []string) (Result, error) {
	panic("not implemented: see T-04347")
}
