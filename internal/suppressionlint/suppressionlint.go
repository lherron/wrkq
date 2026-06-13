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

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

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
	findings, _, err := scanSource(filename, src)
	return findings, err
}

// Scan walks root recursively and scans every .go file whose path contains no segment
// listed in excludeSegments. It returns aggregated findings and per-rule suppression counts.
//
// The live target passes excludeSegments=[]string{"vendor","testdata"}.
// Dir-level fixture tests pass []string{"vendor"} (omitting "testdata") so that testdata/
// fixtures are actually scanned.
func Scan(root string, excludeSegments []string) (Result, error) {
	result := Result{
		PerRuleCount: map[string]int{},
	}

	if root == "" {
		root = "."
	}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		if d.IsDir() {
			if path != root && slices.Contains(excludeSegments, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		if filepath.Ext(path) != ".go" || hasExcludedSegment(root, path, excludeSegments) {
			return nil
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		findings, governedRules, err := scanSource(path, src)
		if err != nil {
			return err
		}
		result.Findings = append(result.Findings, findings...)
		for _, rule := range governedRules {
			result.PerRuleCount[rule]++
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}

	return result, nil
}

var archExceptionRE = regexp.MustCompile(`ARCH-EXCEPTION\(T-[0-9]{5,}\):\s*\S`)

func scanSource(filename string, src []byte) ([]Finding, []string, error) {
	if len(strings.TrimSpace(string(src))) == 0 {
		return nil, nil, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, nil, err
	}

	var findings []Finding
	var governedRules []string
	for _, group := range file.Comments {
		for _, comment := range group.List {
			directive, ok := parseNolintDirective(comment.Text)
			if !ok {
				continue
			}

			line := fset.Position(comment.Slash).Line
			if comment.Slash < file.Package {
				findings = append(findings, newFinding(filename, line, KindBlanketDisable, directive.raw, "blanket file/package-level nolint before the package declaration"))
				continue
			}

			switch {
			case len(directive.rules) == 0:
				findings = append(findings, newFinding(filename, line, KindBareNolint, directive.raw, "bare nolint has no rule list"))
			case containsRule(directive.rules, "all"):
				findings = append(findings, newFinding(filename, line, KindNolintAll, directive.raw, "nolint:all disables every rule"))
			case !archExceptionRE.MatchString(directive.raw):
				findings = append(findings, newFinding(filename, line, KindUngoverned, directive.raw, "missing or malformed ARCH-EXCEPTION ticket/reason"))
			default:
				governedRules = append(governedRules, directive.rules...)
			}
		}
	}

	return findings, governedRules, nil
}

type nolintDirective struct {
	raw   string
	rules []string
}

func parseNolintDirective(text string) (nolintDirective, bool) {
	body, ok := strings.CutPrefix(text, "//")
	if !ok {
		return nolintDirective{}, false
	}

	body = strings.TrimLeft(body, " \t")
	if !strings.HasPrefix(body, "nolint") {
		return nolintDirective{}, false
	}
	if len(body) > len("nolint") && body[len("nolint")] != ':' && body[len("nolint")] != ' ' && body[len("nolint")] != '\t' {
		return nolintDirective{}, false
	}

	directive := nolintDirective{raw: text}
	if len(body) == len("nolint") || body[len("nolint")] != ':' {
		return directive, true
	}

	rest := body[len("nolint")+1:]
	ruleList := rest
	if fields := strings.Fields(rest); len(fields) > 0 {
		ruleList = fields[0]
	}

	for _, rule := range strings.Split(ruleList, ",") {
		rule = strings.TrimSpace(rule)
		if rule != "" {
			directive.rules = append(directive.rules, rule)
		}
	}
	return directive, true
}

func newFinding(filename string, line int, kind, got, problem string) Finding {
	return Finding{
		File:    filename,
		Line:    line,
		Kind:    kind,
		Message: fmt.Sprintf("%s:%d: %s: got %q; expected rule-scoped `//nolint:<rule>[,<rule>...] // ARCH-EXCEPTION(T-00000): <specific reason>`. Resolve or fix the underlying issue first; if suppression is still required, use the ARCH-EXCEPTION(T-id) channel with a real cause so suppressions remain auditable.", filename, line, problem, got),
	}
}

func containsRule(rules []string, want string) bool {
	for _, rule := range rules {
		if rule == want {
			return true
		}
	}
	return false
}

func hasExcludedSegment(root, path string, excludeSegments []string) bool {
	if len(excludeSegments) == 0 {
		return false
	}

	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = path
	}
	for _, segment := range strings.Split(filepath.ToSlash(rel), "/") {
		if slices.Contains(excludeSegments, segment) {
			return true
		}
	}
	return false
}
