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
package surfaceguard

import (
	"bufio"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// RuleNewUnevidenced is the rule key for new RPC methods lacking test evidence.
const RuleNewUnevidenced = "new-unevidenced-surface"

// archExceptionPattern matches a well-formed ARCH-EXCEPTION(T-NNNNN): <reason> annotation.
// Group 1 is the ticket; group 2 is the trailing reason text (may be empty when malformed).
var archExceptionPattern = regexp.MustCompile(`ARCH-EXCEPTION\((T-[0-9]{5,})\):\s*(.*)`)

// methodTokenPattern matches an exact wrkf.* method identifier with token boundaries.
var methodTokenPattern = regexp.MustCompile(`wrkf(?:\.[A-Za-z0-9_]+)+`)

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
	result := Result{ExceptionsByRule: make(map[string]int)}

	grandfathered := make(map[string]bool, len(baseline.Methods))
	for _, m := range baseline.Methods {
		grandfathered[m.Method] = true
	}

	for _, reg := range registrations {
		if grandfathered[reg.Method] {
			continue // existing surface — never evaluated
		}
		// A valid, local ARCH-EXCEPTION exempts the method and is counted.
		if reg.Exception.Present && reg.Exception.Valid {
			result.ExceptionsByRule[RuleNewUnevidenced]++
			continue
		}
		// Executable evidence satisfies the predicate.
		if evidenceSet[reg.Method] {
			continue
		}
		result.Violations = append(result.Violations, Violation{
			Method:  reg.Method,
			File:    reg.File,
			Line:    reg.Line,
			Message: violationMessage(reg),
		})
	}

	return result, nil
}

// violationMessage builds a §3-conformant diagnostic for an unevidenced new surface.
func violationMessage(reg Registration) string {
	return fmt.Sprintf(
		"%s:%d: new public RPC surface %q is registered after the T-04369 baseline but has no executable test evidence. "+
			"Expected: the exact string %q as a Go string literal in a *_test.go, or on a non-comment line of test/smoke-*.sh; got: no such reference (docs/comments do not count). "+
			"WHY: green-means-correct — a newly registered RPC method must be exercised by a contract or smoke test so the suite proves it works before it ships. "+
			"FIX: add a contract test or smoke reference to %q, or, if coverage must be deferred, annotate the registration with ARCH-EXCEPTION(T-NNNNN): <reason> via the sanctioned exception channel.",
		reg.File, reg.Line, reg.Method, reg.Method, reg.Method)
}

// UpdateBaseline returns an updated Baseline by incorporating only registrations that
// already have evidence or a valid ARCH-EXCEPTION.
//
// Registrations that have neither are returned as the second value (rejected).
// This function enforces the invariant at the library level: callers MUST NOT blindly
// absorb all current registrations as legacy without evidence or exception.
func UpdateBaseline(current Baseline, registrations []Registration, evidenceSet map[string]bool) (Baseline, []Registration, error) {
	present := make(map[string]bool, len(current.Methods))
	updated := make([]BaselineEntry, len(current.Methods))
	copy(updated, current.Methods)
	for _, m := range current.Methods {
		present[m.Method] = true
	}

	var rejected []Registration
	for _, reg := range registrations {
		if present[reg.Method] {
			continue // already in baseline
		}
		evidenced := evidenceSet[reg.Method] || (reg.Exception.Present && reg.Exception.Valid)
		if !evidenced {
			rejected = append(rejected, reg)
			continue
		}
		present[reg.Method] = true
		updated = append(updated, BaselineEntry{Method: reg.Method})
	}

	sort.Slice(updated, func(i, j int) bool { return updated[i].Method < updated[j].Method })
	return Baseline{Methods: updated}, rejected, nil
}

// ExtractRegistrations parses a Go source file and returns all s.Register("wrkf.*", ...)
// registrations found within it, together with their file:line and any ARCH-EXCEPTION
// annotation on the same line or the immediately preceding comment line.
func ExtractRegistrations(filename string) ([]Registration, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", filename, err)
	}

	// Map comment end-line -> comment text, so a registration can find an annotation
	// on its own line (trailing) or on the immediately preceding line.
	commentByLine := make(map[int]string)
	for _, group := range file.Comments {
		for _, c := range group.List {
			line := fset.Position(c.Slash).Line
			commentByLine[line] = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(c.Text), "//"))
		}
	}

	var registrations []Registration
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Register" || len(call.Args) == 0 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		method, err := strconv.Unquote(lit.Value)
		if err != nil || !strings.HasPrefix(method, "wrkf.") {
			return true
		}

		line := fset.Position(call.Pos()).Line
		annotation := commentByLine[line]      // trailing comment on same line
		if annotation == "" {
			annotation = commentByLine[line-1] // immediately preceding comment line
		}

		registrations = append(registrations, Registration{
			Method:    method,
			File:      filename,
			Line:      line,
			Exception: parseException(annotation),
		})
		return true
	})

	return registrations, nil
}

// parseException interprets the text of a comment as a possible ARCH-EXCEPTION annotation.
func parseException(text string) ArchException {
	if !strings.Contains(text, "ARCH-EXCEPTION") {
		return ArchException{}
	}
	exc := ArchException{Present: true}
	if m := archExceptionPattern.FindStringSubmatch(text); m != nil {
		exc.Ticket = m[1]
		exc.Reason = strings.TrimSpace(m[2])
		exc.Valid = exc.Reason != ""
	}
	return exc
}

// CollectEvidence scans evidence sources for exact wrkf.* method strings.
//
//   - testFiles: paths to *_test.go files; only Go string literals count.
//   - smokeFiles: paths to test/smoke-*.sh files; only non-comment lines count.
//
// Exact string-boundary matching only. Comment text in test files, documentation
// files, and plain-text references do NOT satisfy the predicate.
func CollectEvidence(testFiles []string, smokeFiles []string) (map[string]bool, error) {
	evidence := make(map[string]bool)

	for _, path := range testFiles {
		if err := collectTestFileEvidence(path, evidence); err != nil {
			return nil, err
		}
	}
	for _, path := range smokeFiles {
		if err := collectSmokeEvidence(path, evidence); err != nil {
			return nil, err
		}
	}

	return evidence, nil
}

// collectTestFileEvidence records every wrkf.* Go string literal in a *_test.go file.
// Comment text is ignored — only ast.BasicLit STRING nodes are inspected.
func collectTestFileEvidence(path string, evidence map[string]bool) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0) // no ParseComments: comments excluded
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		if strings.HasPrefix(value, "wrkf.") {
			evidence[value] = true
		}
		return true
	})
	return nil
}

// collectSmokeEvidence records wrkf.* tokens found on non-comment lines of a smoke script.
// A line whose first non-whitespace character is '#' is a comment and is skipped entirely.
func collectSmokeEvidence(path string, evidence map[string]bool) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue // blank or comment line — not evidence
		}
		for _, match := range methodTokenPattern.FindAllString(line, -1) {
			evidence[match] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan %s: %w", path, err)
	}
	return nil
}

// LoadBaseline reads and parses a baseline JSON file.
func LoadBaseline(filename string) (Baseline, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return Baseline{}, fmt.Errorf("read baseline %s: %w", filename, err)
	}
	var baseline Baseline
	if err := json.Unmarshal(data, &baseline); err != nil {
		return Baseline{}, fmt.Errorf("parse baseline %s: %w", filename, err)
	}
	return baseline, nil
}
