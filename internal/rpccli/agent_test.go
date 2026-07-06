package rpccli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentPassThroughParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds production wrkq binaries; skipped under -short")
	}
	bins := buildProductionBinaries(t)
	dir := t.TempDir()
	fakeDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(fakeDir, 0o755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}
	fake := filepath.Join(fakeDir, "hrcchat")
	script := `#!/bin/sh
printf 'ARGV:'
for arg in "$@"; do
  printf '<%s>' "$arg"
done
printf '\n'
if [ "$HRCCHAT_ECHO_STDIN" = "1" ]; then
  printf 'STDIN:'
  cat
  printf '\n'
fi
if [ "$HRCCHAT_FAIL" = "1" ]; then
  printf 'fake hrcchat failure\n' >&2
  exit 7
fi
printf 'fake hrcchat ok\n' >&2
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake hrcchat: %v", err)
	}

	run := func(bin string, extraEnv []string, stdin []byte, args ...string) cliResult {
		full := append([]string{"--db", filepath.Join(dir, "missing.db"), "--as", "agent:local-human", "agent"}, args...)
		cmd := exec.Command(bin, full...)
		cmd.Dir = dir
		cmd.Env = append(hermeticEnv(), append([]string{"PATH=" + fakeDir + string(os.PathListSeparator) + os.Getenv("PATH")}, extraEnv...)...)
		if stdin != nil {
			cmd.Stdin = bytes.NewReader(stdin)
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		exit := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				exit = ee.ExitCode()
			} else {
				t.Fatalf("run %s: %v", bin, err)
			}
		}
		return cliResult{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
	}

	cases := []struct {
		name     string
		args     []string
		env      []string
		stdin    []byte
		wantExit int
		wantOut  string
	}{
		{
			name:    "flags prompt passthrough",
			args:    []string{"--new", "--format", "json", "--follow", "1s", "hello", "world", "--", "--stall-after", "20m"},
			wantOut: "ARGV:<turn><--new><--format><json><--follow><1s><seshat><--stall-after><20m><hello world>\n",
		},
		{
			name:    "stdin prompt",
			env:     []string{"HRCCHAT_ECHO_STDIN=1"},
			stdin:   []byte("body from stdin"),
			wantOut: "ARGV:<turn><seshat><->\nSTDIN:body from stdin\n",
		},
		{
			name:     "child exit propagated",
			args:     []string{"will fail"},
			env:      []string{"HRCCHAT_FAIL=1"},
			wantExit: 7,
			wantOut:  "ARGV:<turn><seshat><will fail>\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldRes := run(bins.wrkq, tc.env, tc.stdin, tc.args...)
			newRes := run(bins.wrkq, tc.env, tc.stdin, tc.args...)
			if oldRes.exit != newRes.exit || oldRes.exit != tc.wantExit {
				t.Fatalf("exit: old=%d new=%d want=%d\nold stderr=%q\nnew stderr=%q", oldRes.exit, newRes.exit, tc.wantExit, oldRes.stderr, newRes.stderr)
			}
			if oldRes.stdout != newRes.stdout || oldRes.stdout != tc.wantOut {
				t.Fatalf("stdout mismatch:\nold=%q\nnew=%q\nwant=%q", oldRes.stdout, newRes.stdout, tc.wantOut)
			}
			if oldRes.stderr != newRes.stderr {
				t.Fatalf("stderr mismatch:\nold=%q\nnew=%q", oldRes.stderr, newRes.stderr)
			}
			if !strings.Contains(oldRes.stderr, "fake hrcchat") {
				t.Fatalf("fake hrcchat stderr did not propagate: %q", oldRes.stderr)
			}
		})
	}
}
