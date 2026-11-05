package rpccli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The just shim must never pick itself as the real just, however it is
// reached. 2026-11-05 (T-12652): ~/.local/shims/just, a symlink to
// ~/.local/bin/just, led PATH; invoked as ~/.local/bin/just with
// WRKP_JUST_BIN empty, the shim's directory-only self-skip chose the symlink,
// and shim → wrkp just → shim exec'd forever under one pid.
func TestJustShimNeverResolvesItselfAsRealJust(t *testing.T) {
	shim, err := os.ReadFile(filepath.Join("..", "..", "tools", "just", "just"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	bin := filepath.Join(home, ".local", "bin")
	shims := filepath.Join(home, ".local", "shims")
	real := filepath.Join(root, "real")
	for _, dir := range []string{bin, shims, real} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	installed := filepath.Join(bin, "just")
	write(installed, string(shim))
	if err := os.Symlink(installed, filepath.Join(shims, "just")); err != nil {
		t.Fatal(err)
	}
	// A relative symlink chain too: shims/just2 -> just -> bin/just.
	if err := os.Symlink("just", filepath.Join(shims, "just2")); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(real, "just"), "#!/bin/sh\necho \"REAL just $*\"\n")
	// Stands in for `wrkp just`: exec WRKP_JUST_BIN as the real one does, and
	// fail instead of hanging when it is re-entered.
	write(filepath.Join(bin, "wrkp"), `#!/bin/sh
d=$(( ${FAKE_DEPTH:-0} + 1 )); export FAKE_DEPTH=$d
[ $d -gt 3 ] && { echo "LOOP: wrkp re-entered with WRKP_JUST_BIN=$WRKP_JUST_BIN" >&2; exit 99; }
shift; [ "$1" = "--" ] && shift
exec "$WRKP_JUST_BIN" "$@"
`)

	path := strings.Join([]string{shims, bin, real, "/usr/bin", "/bin"}, ":")
	cases := []struct {
		name, argv0, pin string
		withWrkp         bool
	}{
		{"installed path, pin cleared (the install recipe's probe)", installed, "", true},
		{"via the shims symlink", filepath.Join(shims, "just"), "", true},
		{"via a relative symlink chain", filepath.Join(shims, "just2"), "", true},
		{"pin names the shim through a symlink", installed, filepath.Join(shims, "just"), true},
		{"pin names the shim itself", filepath.Join(shims, "just"), installed, true},
		{"no wrkp installed", installed, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrkp := filepath.Join(bin, "wrkp")
			if !tc.withWrkp {
				hidden := wrkp + ".off"
				if err := os.Rename(wrkp, hidden); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = os.Rename(hidden, wrkp) }()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, tc.argv0, "--version")
			cmd.Dir = root
			cmd.Env = []string{"HOME=" + home, "PATH=" + path, "WRKP_JUST_BIN=" + tc.pin}
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("shim hung: %s", out)
			}
			if err != nil || strings.TrimSpace(string(out)) != "REAL just --version" {
				t.Fatalf("want the real just to run once, got err=%v out=%q", err, out)
			}
		})
	}
}
