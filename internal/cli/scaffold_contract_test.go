package cli

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestScaffoldContract is the RED gate for T-04374 (S7 — just new-command scaffold).
//
// It invokes the real generator (scripts/new-cli-command) with a temp OUT_DIR and
// name=demo, then asserts the generator-output contract:
//
//  1. internal/cli/demo.go is emitted and contains the blessed RunE shape.
//  2. It contains output-mode selection (resolveOutputMode).
//  3. It contains self-registration (rootCmd.AddCommand).
//  4. It is valid Go (go/parser check).
//  5. The matching demo_test.go stub was emitted.
//
// This test MUST remain RED until scripts/new-cli-command exists.
func TestScaffoldContract(t *testing.T) {
	// Go tests run with CWD = package dir (internal/cli).
	// ../.. walks up to the repo root.
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("cannot resolve repo root: %v", err)
	}

	script := filepath.Join(repoRoot, "scripts", "new-cli-command")
	outDir := t.TempDir()

	cmd := exec.Command(script, "demo")
	cmd.Env = append(os.Environ(), "OUT_DIR="+outDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generator script failed: %v\noutput: %s", err, out)
	}

	// 1. demo.go must be emitted.
	implFile := filepath.Join(outDir, "demo.go")
	src, err := os.ReadFile(implFile)
	if err != nil {
		t.Fatalf("demo.go not emitted at %s: %v", implFile, err)
	}

	content := string(src)

	// 2. Blessed RunE shape: appctx.WithApp(appctx.WithActor(), runDemo)
	if !strings.Contains(content, "appctx.WithApp(appctx.WithActor(), runDemo)") {
		t.Errorf("demo.go missing blessed RunE shape: want appctx.WithApp(appctx.WithActor(), runDemo)")
	}

	// 3. Output-mode selection.
	if !strings.Contains(content, "resolveOutputMode(") {
		t.Errorf("demo.go missing output-mode selection: want resolveOutputMode(...)")
	}

	// 4. Self-registration via init().
	if !strings.Contains(content, "rootCmd.AddCommand(") {
		t.Errorf("demo.go missing self-registration: want rootCmd.AddCommand(...)")
	}

	// 5. Valid Go syntax (full compilation is covered by the coordinator's just verify check).
	fset := token.NewFileSet()
	if _, parseErr := parser.ParseFile(fset, implFile, src, 0); parseErr != nil {
		t.Errorf("demo.go is not valid Go: %v", parseErr)
	}

	// 6. Matching test stub must also be emitted.
	testFile := filepath.Join(outDir, "demo_test.go")
	if _, statErr := os.Stat(testFile); os.IsNotExist(statErr) {
		t.Errorf("demo_test.go stub not emitted at %s", testFile)
	}
}
