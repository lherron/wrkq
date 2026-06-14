// Package discovery_test is a LIGHT-RED contract test for T-04382.
// It will fail to compile until internal/discovery is implemented.
// RED REASON: package internal/discovery does not exist yet.
package discovery_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/discovery"
)

// repoRoot returns the absolute path to the wrkq repository root.
func repoRoot(t *testing.T) string {
	t.Helper()
	// Walk up from this test file's directory to find go.mod.
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod from test file path")
		}
		dir = parent
	}
}

// TestFindEntryPoints_State asserts that FindEntryPoints(root, "state") returns
// hits covering the Cobra set/touch commands AND the domain symbol sites
// ParseState/ValidateState, each with a non-empty File, Line > 0, and non-empty Role.
func TestFindEntryPoints_State(t *testing.T) {
	root := repoRoot(t)
	hits, err := discovery.FindEntryPoints(root, "state")
	if err != nil {
		t.Fatalf("FindEntryPoints: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("FindEntryPoints returned no hits for topic 'state'")
	}

	// Validate each hit has the required fields populated.
	for _, h := range hits {
		if h.File == "" {
			t.Errorf("hit has empty File: %+v", h)
		}
		if h.Line <= 0 {
			t.Errorf("hit has Line <= 0: %+v", h)
		}
		if h.Role == "" {
			t.Errorf("hit has empty Role: %+v", h)
		}
	}

	// Required hits — we check by file suffix, not absolute path, to be
	// root-agnostic while still being concrete.
	required := []struct {
		fileSuffix string
		desc       string
	}{
		{"internal/cli/set.go", "set command (Cobra Use: field)"},
		{"internal/cli/touch.go", "touch command (Cobra Use: field)"},
		{"internal/domain/state.go", "ParseState domain symbol"},
		{"internal/domain/validation.go", "ValidateState domain symbol"},
	}

	for _, req := range required {
		found := false
		for _, h := range hits {
			if strings.HasSuffix(filepath.ToSlash(h.File), req.fileSuffix) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("FindEntryPoints missing required hit for %s (%s)", req.fileSuffix, req.desc)
		}
	}
}

// TestExplainArea_Domain asserts that ExplainArea(root, "internal/domain") returns
// a non-empty Role, at least one dependent package (e.g. internal/cli imports domain),
// and key exported symbols present (ParseState, ValidateState, State).
func TestExplainArea_Domain(t *testing.T) {
	root := repoRoot(t)
	area, err := discovery.ExplainArea(root, "internal/domain")
	if err != nil {
		t.Fatalf("ExplainArea: %v", err)
	}

	if area.Role == "" {
		t.Error("ExplainArea returned empty Role for internal/domain")
	}

	// Must have dependents (packages that import internal/domain).
	if len(area.Dependents) == 0 {
		t.Error("ExplainArea returned no Dependents for internal/domain; expected at least internal/cli")
	}

	// internal/cli is a well-known dependent.
	foundCLI := false
	for _, dep := range area.Dependents {
		if strings.Contains(dep, "internal/cli") {
			foundCLI = true
			break
		}
	}
	if !foundCLI {
		t.Errorf("ExplainArea Dependents does not contain internal/cli; got: %v", area.Dependents)
	}

	// Key exported symbols must be present.
	requiredSymbols := []string{"ParseState", "ValidateState", "State"}
	for _, sym := range requiredSymbols {
		found := false
		for _, s := range area.Exports {
			if s == sym {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ExplainArea Exports missing %q; got: %v", sym, area.Exports)
		}
	}
}
