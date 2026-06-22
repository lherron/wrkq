package archrecords

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree materializes files (relpath→content) under a temp root and returns it.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// regen writes the projections so a subsequent check-mode run is diff-clean.
func regen(t *testing.T, root string) {
	t.Helper()
	if _, err := Run(Options{Root: root, Write: true}); err != nil {
		t.Fatalf("write: %v", err)
	}
}

const validInvariant = `id: demo.invariant
kind: invariant
status: active
owner_project: wrkq
scope: demo scope
predicate: demo predicate
authority:
  repo: wrkq
  surface: demo
source:
  - internal/demo/demo_test.go
required_tests:
  - TestDemo
last_verified:
  date: 2026-06-22
  evidence: green
supersedes: []
superseded_by: null
`

const demoTestFile = `package demo

import "testing"

func TestDemo(t *testing.T) {}
`

func findingsText(res Result) string {
	var b strings.Builder
	for _, f := range res.Findings {
		b.WriteString(f.Message)
		b.WriteString("\n")
	}
	return b.String()
}

func mustRun(t *testing.T, root string) Result {
	t.Helper()
	res, err := Run(Options{Root: root})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return res
}

func TestValidRecordsPass(t *testing.T) {
	root := writeTree(t, map[string]string{
		"architecture/records/invariants/demo.yaml": validInvariant,
		"internal/demo/demo_test.go":                demoTestFile,
	})
	regen(t, root)
	res := mustRun(t, root)
	if len(res.Findings) != 0 {
		t.Fatalf("expected clean, got findings:\n%s", findingsText(res))
	}
	if res.Active != 1 {
		t.Fatalf("expected 1 active record, got %d", res.Active)
	}
}

func TestDuplicateIDFails(t *testing.T) {
	root := writeTree(t, map[string]string{
		"architecture/records/invariants/a.yaml": validInvariant,
		"architecture/records/invariants/b.yaml": validInvariant,
		"internal/demo/demo_test.go":             demoTestFile,
	})
	regen(t, root)
	res := mustRun(t, root)
	if !strings.Contains(findingsText(res), "duplicate id") {
		t.Fatalf("expected duplicate id finding, got:\n%s", findingsText(res))
	}
}

func TestUnknownFieldFails(t *testing.T) {
	bad := validInvariant + "bogus_field: nope\n"
	root := writeTree(t, map[string]string{
		"architecture/records/invariants/demo.yaml": bad,
		"internal/demo/demo_test.go":                demoTestFile,
	})
	res := mustRun(t, root)
	if !strings.Contains(findingsText(res), "schema invalid") {
		t.Fatalf("expected schema finding, got:\n%s", findingsText(res))
	}
}

func TestBadKindFails(t *testing.T) {
	bad := strings.Replace(validInvariant, "kind: invariant", "kind: nonsense", 1)
	root := writeTree(t, map[string]string{
		"architecture/records/invariants/demo.yaml": bad,
		"internal/demo/demo_test.go":                demoTestFile,
	})
	res := mustRun(t, root)
	if !strings.Contains(findingsText(res), "illegal kind") {
		t.Fatalf("expected illegal kind finding, got:\n%s", findingsText(res))
	}
}

func TestMissingRequiredFieldFails(t *testing.T) {
	bad := strings.Replace(validInvariant, "predicate: demo predicate\n", "", 1)
	root := writeTree(t, map[string]string{
		"architecture/records/invariants/demo.yaml": bad,
		"internal/demo/demo_test.go":                demoTestFile,
	})
	res := mustRun(t, root)
	if !strings.Contains(findingsText(res), "missing required field") {
		t.Fatalf("expected missing field finding, got:\n%s", findingsText(res))
	}
}

func TestMissingSourcePathFails(t *testing.T) {
	bad := strings.Replace(validInvariant, "internal/demo/demo_test.go", "internal/demo/gone_test.go", 1)
	root := writeTree(t, map[string]string{
		"architecture/records/invariants/demo.yaml": bad,
		"internal/demo/demo_test.go":                demoTestFile,
	})
	res := mustRun(t, root)
	if !strings.Contains(findingsText(res), "unreachable source path") {
		t.Fatalf("expected unreachable source finding, got:\n%s", findingsText(res))
	}
}

// CORRECTNESS POINT #1: ^Test entries are grep-cross-checked; renaming the test reds the gate.
func TestRequiredTestFuncCrossCheck(t *testing.T) {
	renamedTest := strings.Replace(demoTestFile, "func TestDemo(", "func TestRenamed(", 1)
	root := writeTree(t, map[string]string{
		"architecture/records/invariants/demo.yaml": validInvariant, // still names TestDemo
		"internal/demo/demo_test.go":                renamedTest,
	})
	res := mustRun(t, root)
	if !strings.Contains(findingsText(res), "required test not found") {
		t.Fatalf("expected missing test func finding, got:\n%s", findingsText(res))
	}
}

// CORRECTNESS POINT #1: command/prose required_tests entries are documentation —
// present but never grep-cross-checked, never executed.
func TestProseRequiredTestNotGrepped(t *testing.T) {
	prose := strings.Replace(validInvariant, "  - TestDemo\n", "  - \"just verify exits 0\"\n", 1)
	// drop the _test.go from source since there is no Go test to bind anymore
	prose = strings.Replace(prose, "  - internal/demo/demo_test.go\n", "  - internal/demo/demo.go\n", 1)
	root := writeTree(t, map[string]string{
		"architecture/records/invariants/demo.yaml": prose,
		"internal/demo/demo.go":                     "package demo\n",
	})
	regen(t, root)
	res := mustRun(t, root)
	if len(res.Findings) != 0 {
		t.Fatalf("prose required_tests must not be cross-checked, got:\n%s", findingsText(res))
	}
}

// CORRECTNESS POINT #2: projections are generated-and-diffed; a stale projection reds the gate.
func TestStaleProjectionFails(t *testing.T) {
	root := writeTree(t, map[string]string{
		"architecture/records/invariants/demo.yaml": validInvariant,
		"internal/demo/demo_test.go":                demoTestFile,
	})
	regen(t, root)
	// Corrupt the generated projection.
	if err := os.WriteFile(filepath.Join(root, "architecture/INVARIANTS.md"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := mustRun(t, root)
	if !strings.Contains(findingsText(res), "INVARIANTS.md") {
		t.Fatalf("expected stale projection finding, got:\n%s", findingsText(res))
	}
}

func TestSupersededWithoutSuccessorFails(t *testing.T) {
	bad := strings.Replace(validInvariant, "status: active", "status: superseded", 1)
	root := writeTree(t, map[string]string{
		"architecture/records/invariants/demo.yaml": bad,
		"internal/demo/demo_test.go":                demoTestFile,
	})
	res := mustRun(t, root)
	if !strings.Contains(findingsText(res), "superseded without successor") {
		t.Fatalf("expected superseded-without-successor finding, got:\n%s", findingsText(res))
	}
}

func TestWriteThenCheckIsClean(t *testing.T) {
	root := writeTree(t, map[string]string{
		"architecture/records/invariants/demo.yaml": validInvariant,
		"internal/demo/demo_test.go":                demoTestFile,
	})
	regen(t, root)
	// RISKS.md is always generated even with no risks.
	if _, err := os.Stat(filepath.Join(root, "architecture/RISKS.md")); err != nil {
		t.Fatalf("expected RISKS.md to be generated: %v", err)
	}
	res := mustRun(t, root)
	if len(res.Findings) != 0 {
		t.Fatalf("write→check should be clean, got:\n%s", findingsText(res))
	}
}
