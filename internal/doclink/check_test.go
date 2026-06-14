package doclink_test

// RED test — T-04375
// This file will NOT compile until internal/doclink is implemented by larry.
// Compile failure IS the intended red signal.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/doclink"
)

// assertSection3Message checks that a Violation.Message satisfies §3 diagnostic format:
// must contain "✗", "FIX →", "WHY →", "EXCEPTION →", and a do-not-suppress line.
func assertSection3Message(t *testing.T, msg string) {
	t.Helper()
	markers := []string{"✗", "FIX →", "WHY →", "EXCEPTION →"}
	for _, m := range markers {
		if !strings.Contains(msg, m) {
			t.Errorf("§3 conformance: message missing marker %q; got:\n%s", m, msg)
		}
	}
	// Must include a do-not-suppress directive (any casing / phrasing).
	doNotSuppress := strings.Contains(msg, "do not suppress") ||
		strings.Contains(msg, "do-not-suppress") ||
		strings.Contains(msg, "DO NOT SUPPRESS") ||
		strings.Contains(msg, "do not hide") ||
		strings.Contains(msg, "DO NOT HIDE")
	if !doNotSuppress {
		t.Errorf("§3 conformance: message missing do-not-suppress line; got:\n%s", msg)
	}
}

// writeFile creates a file at path (creating parent dirs) with the given content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Test 1: pass-on-good — all references resolve → zero violations
// ---------------------------------------------------------------------------

func TestCheck_PassOnGood(t *testing.T) {
	root := t.TempDir()

	// Create target files the doc references.
	writeFile(t, filepath.Join(root, "internal", "search", "search.go"), "package search\n")
	writeFile(t, filepath.Join(root, "docs", "spec.md"), "# Spec\n")

	// Doc with references that all resolve.
	docContent := `# Router

See [search package](internal/search/search.go) for FTS5 details.
See [spec](docs/spec.md) for the full specification.

External links are ignored: https://example.com and mailto:ops@example.com.
Pure anchors are ignored: [jump](#section).

A backticked path: ` + "`internal/search/search.go`" + `
`
	docPath := "AGENTS.md"
	writeFile(t, filepath.Join(root, docPath), docContent)

	violations, err := doclink.Check(root, []string{docPath})
	if err != nil {
		t.Fatalf("Check: unexpected error: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("pass-on-good: expected 0 violations, got %d:", len(violations))
		for _, v := range violations {
			t.Errorf("  %+v", v)
		}
	}
}

// ---------------------------------------------------------------------------
// Test 2: fire-on-bad — dead links produce ≥1 violation with §3 message
// ---------------------------------------------------------------------------

func TestCheck_FireOnBad(t *testing.T) {
	root := t.TempDir()

	// Create one real target so we can distinguish it from the dead ones.
	writeFile(t, filepath.Join(root, "internal", "real", "thing.go"), "package real\n")

	// Doc with two dead references:
	//   (a) markdown link to a non-existent file
	//   (b) backticked token with a known source extension that doesn't exist
	docContent := `# Router

Good link: [real](internal/real/thing.go)

Dead markdown link: [missing](does/not/exist.md)

Dead backtick: ` + "`internal/nope/missing.go`" + `
`
	docPath := "AGENTS.md"
	writeFile(t, filepath.Join(root, docPath), docContent)

	violations, err := doclink.Check(root, []string{docPath})
	if err != nil {
		t.Fatalf("Check: unexpected error: %v", err)
	}
	if len(violations) == 0 {
		t.Fatal("fire-on-bad: expected >=1 violation, got 0")
	}

	// Collect reported refs for assertion.
	refs := make(map[string]doclink.Violation, len(violations))
	for _, v := range violations {
		refs[v.Ref] = v
	}

	// Both dead references must be reported.
	for _, deadRef := range []string{"does/not/exist.md", "internal/nope/missing.go"} {
		v, ok := refs[deadRef]
		if !ok {
			t.Errorf("expected violation for ref %q; violations: %+v", deadRef, violations)
			continue
		}
		// Doc field must be the repo-relative doc path.
		if v.Doc != docPath {
			t.Errorf("violation.Doc: want %q, got %q", docPath, v.Doc)
		}
		// Line must be positive.
		if v.Line <= 0 {
			t.Errorf("violation.Line must be >0 for ref %q, got %d", deadRef, v.Line)
		}
		// Message must be §3-conformant.
		assertSection3Message(t, v.Message)
	}
}

// ---------------------------------------------------------------------------
// Test 3: skip-external — http(s), mailto, pure #anchor → zero violations
// ---------------------------------------------------------------------------

func TestCheck_SkipExternal(t *testing.T) {
	root := t.TempDir()

	docContent := `# External only

- [Website](https://example.com)
- [Secure](https://secure.example.org/path/page)
- [Mail](mailto:ops@example.com)
- [Anchor only](#section-heading)
- [HTTP plain](http://legacy.example.com/api)
`
	docPath := "docs/external.md"
	writeFile(t, filepath.Join(root, docPath), docContent)

	violations, err := doclink.Check(root, []string{docPath})
	if err != nil {
		t.Fatalf("Check: unexpected error: %v", err)
	}
	if len(violations) != 0 {
		t.Errorf("skip-external: expected 0 violations, got %d:", len(violations))
		for _, v := range violations {
			t.Errorf("  %+v", v)
		}
	}
}
