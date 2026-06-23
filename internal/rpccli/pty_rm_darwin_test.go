//go:build darwin

package rpccli

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// runCLIPromptTTY runs a CLI with stdin+stdout+stderr attached to a real
// pseudo-terminal (so isStdoutTTY reports true and the legacy purge prompt
// engages its interactive path), writes `input` to the terminal master after a
// brief settle, and returns the captured terminal output + exit code.
//
// This is the prompt-ownership oracle for the caller-owned-confirmation seam: it
// proves the MIRROR (not the RPC server) renders the legacy purge warning + the
// "Type 'yes' to confirm:" line and honors the typed answer, byte-for-byte with
// legacy `wrkq rm --purge`.
func runCLIPromptTTY(t *testing.T, bin, dir string, args []string, input string) (out string, exit int) {
	t.Helper()
	master, slaveName := openPTY(t)
	defer func() { _ = master.Close() }()

	slave, err := os.OpenFile(slaveName, os.O_RDWR, 0)
	if err != nil {
		t.Skipf("pty unavailable (open slave %s): %v", slaveName, err)
	}

	full := append([]string{"--db", filepath.Join(dir, "wrkq.db"), "--as", "local-human"}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Dir = dir
	cmd.Env = hermeticEnv()
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave

	var buf []byte
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); buf, _ = io.ReadAll(master) }()

	if err := cmd.Start(); err != nil {
		_ = slave.Close()
		t.Fatalf("start %s on tty: %v", bin, err)
	}
	// Feed the typed answer. The prompt reads a single line; the terminal echoes
	// it (ONLCR), which is identical for both binaries so byte parity holds.
	if input != "" {
		_, _ = master.WriteString(input)
	}

	runErr := cmd.Wait()
	_ = slave.Close()
	wg.Wait()

	exit = 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			t.Fatalf("wait %s on tty: %v", bin, runErr)
		}
	}
	return string(buf), exit
}

// TestRmPurgePromptOwnership proves the mirror owns the legacy `rm --purge`
// confirmation prompt on a real TTY: the warning block + "Type 'yes' to
// confirm:" line, the accept ("yes" → delete, exit 0) and abort ("no" → aborted,
// exit 1) branches, and the --yes skip (no prompt). For each case the mirror's
// terminal output + exit code byte-match legacy `wrkq rm --purge`.
func TestRmPurgePromptOwnership(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + drives a real pty")
	}
	bins := buildParityBinaries(t)

	cases := []struct {
		name  string
		args  []string
		input string
	}{
		{name: "accept", args: []string{"rm", "inbox/aaa", "--purge"}, input: "yes\n"},
		{name: "abort", args: []string{"rm", "inbox/aaa", "--purge"}, input: "no\n"},
		{name: "yes-flag-skips-prompt", args: []string{"rm", "inbox/aaa", "--purge", "--yes"}, input: ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			setup := [][]string{{"touch", "inbox/aaa", "-t", "Alpha"}}
			base := seedFixture(t, bins, setup)
			oldDir := copyFixture(t, base)
			newDir := copyFixture(t, base)

			oldOut, oldExit := runCLIPromptTTY(t, bins.wrkq, oldDir, tc.args, tc.input)
			newOut, newExit := runCLIPromptTTY(t, bins.mirror, newDir, tc.args, tc.input)

			if oldExit != newExit {
				t.Errorf("exit code: old=%d new=%d\n old: %q\n new: %q", oldExit, newExit, oldOut, newOut)
			}
			if normalize(newOut) != normalize(oldOut) {
				t.Errorf("prompt tty output mismatch:\n old: %q\n new: %q", oldOut, newOut)
			}

			// Guard against a false "both produced nothing" parity: the prompting
			// cases MUST render the legacy warning + confirm line on the legacy oracle.
			if tc.name != "yes-flag-skips-prompt" {
				for _, want := range []string{"WARNING: This will permanently delete", "Type 'yes' to confirm:"} {
					if !strings.Contains(oldOut, want) {
						t.Fatalf("legacy purge prompt missing %q (got %q)", want, oldOut)
					}
				}
			}
			// Snapshot parity: accept + --yes delete the row; abort leaves it.
			if got, want := snapshot(t, newDir), snapshot(t, oldDir); got != want {
				t.Errorf("durable snapshot mismatch:\n old:\n%s\n new:\n%s", want, got)
			}
		})
	}
}
