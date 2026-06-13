// Package rotguard detects stale TDD-phase comments in *_test.go files.
//
// A comment group is "stale" when the scoped go test passes (TestsGreen == true)
// and the group contains a hard-fail marker ("RED TODAY", "compile failure") or a
// context-dependent marker ("does not exist", "not yet implemented") paired with one
// of those hard markers in the same group.
//
// Escape hatch: a group also containing "ARCH-EXCEPTION(T-NNNNN): <reason>" (non-empty
// reason) is exempt and counted in Result.ExceptionsByRule["stale-tdd-comment"].
//
// Scope: first-party *_test.go only (recursively under Scope.Dir). Comments only —
// never string literals. Uses go/ast comment groups.
//
// RED stub — implementation pending T-04368.
package rotguard

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
)

const ruleStaleTDDComment = "stale-tdd-comment"

var archExceptionPattern = regexp.MustCompile(`ARCH-EXCEPTION\(T-[0-9]+\):\s*\S`)
var quotedTextPattern = regexp.MustCompile(`"[^"\n]*"`)

// Scope defines the scan target and test-result precondition.
type Scope struct {
	Dir        string // directory scanned recursively for *_test.go
	TestsGreen bool   // whether the scoped `go test` passed
}

// Violation records a single stale TDD comment found in a test file.
//
// Message is §3-conformant: "<file>:<line>: stale TDD comment — <marker> found but
// tests pass, so this claim is false. Remove the stale comment or, if cleanup must
// be deferred, use ARCH-EXCEPTION(T-NNNNN): <reason>."
type Violation struct {
	File    string
	Line    int
	Marker  string
	Message string
}

// Result is the aggregate output of a Check call.
type Result struct {
	Violations       []Violation
	ExceptionsByRule map[string]int // ARCH-EXCEPTION counts, keyed "stale-tdd-comment"
}

// Check scans scope.Dir for stale TDD comments in *_test.go files.
//
// When scope.TestsGreen is false, no violations are returned — test failure is
// the controlling signal and the guard must not independently fire.
//
// When scope.TestsGreen is true, each comment group is evaluated per the
// daedalus ruling (C-04379):
//   - "RED TODAY" or "compile failure" in the group → violation
//   - "does not exist" only when the same group also carries a hard marker → violation
//   - "not yet implemented" only when paired with a hard marker → violation
//   - Any of the above + "ARCH-EXCEPTION(T-NNNNN): <reason>" in group → exempt + counted
func Check(scope Scope) (Result, error) {
	result := Result{ExceptionsByRule: make(map[string]int)}
	if !scope.TestsGreen {
		return result, nil
	}
	if scope.Dir == "" {
		return result, fmt.Errorf("scope dir is required")
	}

	fset := token.NewFileSet()
	err := filepath.WalkDir(scope.Dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		for _, group := range file.Comments {
			text := group.Text()
			markers := staleMarkers(text)
			if len(markers) == 0 {
				continue
			}
			if archExceptionPattern.MatchString(text) {
				result.ExceptionsByRule[ruleStaleTDDComment]++
				continue
			}

			position := fset.Position(group.Pos())
			for _, marker := range markers {
				result.Violations = append(result.Violations, Violation{
					File:   position.Filename,
					Line:   position.Line,
					Marker: marker,
					Message: fmt.Sprintf("%s:%d: stale TDD comment - %q found but tests pass, so this claim is false. Remove the stale comment or, if cleanup must be deferred, use ARCH-EXCEPTION(T-NNNNN): <reason>.",
						position.Filename, position.Line, marker),
				})
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}

	return result, nil
}

func staleMarkers(text string) []string {
	text = quotedTextPattern.ReplaceAllString(text, "")
	hardMarkers := []string{"RED TODAY", "compile failure"}
	softMarkers := []string{"does not exist", "not yet implemented"}

	markers := make([]string, 0, len(hardMarkers)+len(softMarkers))
	hasHardMarker := false
	for _, marker := range hardMarkers {
		if strings.Contains(text, marker) {
			hasHardMarker = true
			markers = append(markers, marker)
		}
	}
	if !hasHardMarker {
		return nil
	}
	for _, marker := range softMarkers {
		if strings.Contains(text, marker) {
			markers = append(markers, marker)
		}
	}
	return markers
}
