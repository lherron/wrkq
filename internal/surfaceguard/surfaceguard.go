// Package surfaceguard enforces delta-based public-surface coverage for wrkf RPC methods.
//
// A DELTA-based guard: a new wrkf.* RPC method registered after the committed baseline must
// point at executable test evidence (exact method string as a Go string literal in a *_test.go,
// or as a non-comment line in test/smoke-*.sh) OR carry a local ARCH-EXCEPTION(T-NNNNN): <reason>
// on the registration. Existing baseline methods are grandfathered.
//
// Escape hatch: ARCH-EXCEPTION(T-NNNNN): <reason> (valid ticket + non-empty reason) placed
// on or adjacent to the s.Register call exempts the method and increments
// Result.ExceptionsByRule[RuleNewUnevidenced].
//
// Evidence predicate (exact, no fuzzy substring): a new method wrkf.X is covered iff the exact
// method string appears as a Go string literal in *_test.go, OR as non-comment content in a
// test/smoke-*.sh. Docs/spec/comment-only references do NOT count.
//
// RED stub — implementation pending T-04369.
package surfaceguard

// RuleNewUnevidenced is the rule key for new RPC methods lacking test evidence.
const RuleNewUnevidenced = "new-unevidenced-surface"

// Registration is a single s.Register("wrkf.*", ...) call parsed from a Go source file.
type Registration struct {
	Method    string        // exact method string, e.g. "wrkf.foo.bar"
	File      string        // source file containing the registration
	Line      int           // line number of the s.Register call
	Exception ArchException // any ARCH-EXCEPTION annotation on or adjacent to the line
}

// ArchException records an ARCH-EXCEPTION annotation found on or adjacent to a registration.
type ArchException struct {
	Present bool   // annotation token found
	Valid   bool   // Present && well-formed ticket (T-NNNNN) + non-empty reason
	Ticket  string // e.g. "T-04369"
	Reason  string // trimmed reason text
}

// BaselineEntry is one method in the committed baseline.
type BaselineEntry struct {
	Method string `json:"method"`
}

// Baseline is the committed golden list of grandfathered RPC methods.
// It is read from internal/surfaceguard/baseline.json (the committed file).
type Baseline struct {
	Methods []BaselineEntry `json:"methods"`
}

// Violation is a single uncovered new RPC method.
//
// Message is §3-conformant: names the surface (method), the file:line where it was added,
// what evidence is missing, the exact fix (add contract test / smoke / ARCH-EXCEPTION),
// expected-vs-got, WHY, and the sanctioned escape-hatch channel.
type Violation struct {
	Method  string // exact method string, e.g. "wrkf.foo.bar"
	File    string // source file of the registration
	Line    int    // line number of the registration
	Message string // §3-conformant diagnostic
}

// Result is the aggregate output of a Check call.
type Result struct {
	Violations       []Violation
	ExceptionsByRule map[string]int // ARCH-EXCEPTION counts; key = RuleNewUnevidenced
}

// Check evaluates delta-based surface coverage.
//
// For each registration NOT in baseline, the method must either:
//   - appear verbatim in evidenceSet (sourced from *_test.go string literals or
//     non-comment lines of test/smoke-*.sh), OR
//   - carry a valid ARCH-EXCEPTION(T-NNNNN): <reason> annotation.
//
// Methods in baseline are grandfathered and never evaluated.
// A valid ARCH-EXCEPTION increments Result.ExceptionsByRule[RuleNewUnevidenced].
func Check(registrations []Registration, baseline Baseline, evidenceSet map[string]bool) (Result, error) {
	panic("not implemented: see T-04369")
}

// UpdateBaseline returns an updated Baseline by incorporating only registrations that
// already have evidence or a valid ARCH-EXCEPTION.
//
// Registrations that have neither are returned as the second value (rejected).
// This function enforces the invariant at the library level: callers MUST NOT blindly
// absorb all current registrations as legacy without evidence or exception.
func UpdateBaseline(current Baseline, registrations []Registration, evidenceSet map[string]bool) (Baseline, []Registration, error) {
	panic("not implemented: see T-04369")
}

// ExtractRegistrations parses a Go source file and returns all s.Register("wrkf.*", ...)
// registrations found within it, together with their file:line and any ARCH-EXCEPTION
// annotation on the same line or the immediately preceding comment line.
func ExtractRegistrations(filename string) ([]Registration, error) {
	panic("not implemented: see T-04369")
}

// CollectEvidence scans evidence sources for exact wrkf.* method strings.
//
//   - testFiles: paths to *_test.go files; only Go string literals count.
//   - smokeFiles: paths to test/smoke-*.sh files; only non-comment lines count.
//
// Exact string-boundary matching only. Comment text in test files, documentation
// files, and plain-text references do NOT satisfy the predicate.
func CollectEvidence(testFiles []string, smokeFiles []string) (map[string]bool, error) {
	panic("not implemented: see T-04369")
}

// LoadBaseline reads and parses a baseline JSON file.
func LoadBaseline(filename string) (Baseline, error) {
	panic("not implemented: see T-04369")
}
