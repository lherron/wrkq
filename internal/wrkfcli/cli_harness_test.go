package wrkfcli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The CLI tests drive the shipped surface: cmd/wrkf is built once per test
// process with the canonical tags and run as a subprocess, so argv, stdin,
// stdout, stderr and the exit status are exactly what a caller sees.
var (
	wrkfBinaryOnce sync.Once
	wrkfBinaryPath string
	wrkfBinaryErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if wrkfBinaryPath != "" {
		_ = os.RemoveAll(filepath.Dir(wrkfBinaryPath))
	}
	os.Exit(code)
}

func wrkfBinary(t *testing.T) string {
	t.Helper()
	wrkfBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wrkfcli-bin-")
		if err != nil {
			wrkfBinaryErr = err
			return
		}
		wrkfBinaryPath = filepath.Join(dir, "wrkf")
		// GOFLAGS (e.g. -overlay for mutation runs) is inherited by the build.
		build := exec.Command("go", "build", "-tags", "sqlite_fts5,wrkq_local", "-o", wrkfBinaryPath, "./cmd/wrkf")
		build.Dir = filepath.Join("..", "..")
		if out, err := build.CombinedOutput(); err != nil {
			wrkfBinaryErr = fmt.Errorf("build wrkf: %v\n%s", err, out)
		}
	})
	if wrkfBinaryErr != nil {
		t.Fatal(wrkfBinaryErr)
	}
	return wrkfBinaryPath
}

type wrkfResult struct {
	stdout   string
	stderr   string
	exitCode int
}

// runWrkf runs the built wrkf in a scratch cwd with a hermetic environment:
// HOME is a scratch dir and no ambient WRKF_/WRKQ_/scope variable leaks in;
// env adds the caller's KEY=VALUE pairs.
func runWrkf(t *testing.T, stdin string, env []string, args ...string) wrkfResult {
	t.Helper()
	return runWrkfIn(t, t.TempDir(), stdin, env, args...)
}

func runWrkfIn(t *testing.T, dir, stdin string, env []string, args ...string) wrkfResult {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), wrkfBinary(t), args...)
	cmd.Dir = dir
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "TMPDIR=" + os.TempDir()}, env...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	res := wrkfResult{}
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("run wrkf %v: %v", args, err)
		}
		res.exitCode = exit.ExitCode()
	}
	res.stdout, res.stderr = stdout.String(), stderr.String()
	return res
}

// requireWrkfRefusal fails unless wrkf exited non-zero and said want on stderr.
func requireWrkfRefusal(t *testing.T, res wrkfResult, want string) {
	t.Helper()
	if res.exitCode == 0 {
		t.Fatalf("wrkf succeeded, want refusal %q\nstdout: %s", want, res.stdout)
	}
	if !strings.Contains(res.stderr, want) {
		t.Fatalf("wrkf exit %d stderr does not say %q:\n%s", res.exitCode, want, res.stderr)
	}
}
