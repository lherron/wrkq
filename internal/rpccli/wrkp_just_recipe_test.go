package rpccli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// A typo is not a run: `wrkp just no-such-recipe` must pass straight through
// to just (which refuses it) and post no run.settled, or foundry raises a red
// that no green run can clear (R-00620, 2026-11-08: `just info` in arris).
// A justfile just cannot parse still counts as a run, because that red is real.
func TestWrkpJustRecipeKnownSeparatesTyposFromRuns(t *testing.T) {
	justBin, err := exec.LookPath("just")
	if err != nil {
		t.Fatalf("real just is required for this test: %v", err)
	}
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("tools/mod.just", "lint:\n\ttrue\n")
	justfile := write("justfile", "# wrkp: run.settled\nmod tools\n\nalias b := build\n\ndefault:\n\ttrue\n\nbuild arg='x':\n\ttrue\n")
	broken := write("broken/justfile", "# wrkp: run.settled\nbuild:\n\ttrue\n  bad indentation {{\n")

	cases := []struct {
		name     string
		justfile string
		args     []string
		want     bool
	}{
		{"named recipe", justfile, []string{"build"}, true},
		{"named recipe with an argument", justfile, []string{"build", "y"}, true},
		{"alias", justfile, []string{"b"}, true},
		{"default recipe", justfile, nil, true},
		{"module path", justfile, []string{"tools::lint"}, true},
		{"module, space separated", justfile, []string{"tools", "lint"}, true},
		{"typo", justfile, []string{"info"}, false},
		{"typo after an assignment", justfile, []string{"x=1", "nope"}, false},
		{"unknown module recipe", justfile, []string{"tools::nope"}, false},
		{"unparseable justfile still observed", broken, []string{"build"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inv := parseWrkpJustArgs(tc.args)
			if got := wrkpJustRecipeKnown(justBin, tc.justfile, inv); got != tc.want {
				t.Fatalf("wrkpJustRecipeKnown(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}
