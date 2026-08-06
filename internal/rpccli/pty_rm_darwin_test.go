//go:build darwin && wrkq_local

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

	full := append([]string{"--db", filepath.Join(dir, "wrkq.db"), "--as", "agent:local-human"}, args...)
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
	bins := buildProductionBinaries(t)

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
			newOut, newExit := runCLIPromptTTY(t, bins.wrkq, newDir, tc.args, tc.input)

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

// TestCommentRmPromptOwnership proves the mirror owns the legacy `comment rm`
// confirmation on a real TTY: the inline "[y/N]: " line (NO warning block; rm's
// "yes" shape is deliberately NOT reused — daedalus #10190), prompting EVEN for
// soft-delete, accepting EXACTLY "y"/"Y", the abort branch ("n" → per-comment
// skip), and --yes (no prompt). The mirror's terminal output + exit code + the
// durable comment snapshot byte-match legacy `wrkq comment rm`.
func TestCommentRmPromptOwnership(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + drives a real pty")
	}
	bins := buildProductionBinaries(t)

	cases := []struct {
		name  string
		args  []string
		input string
	}{
		{name: "accept-y", args: []string{"comment", "rm", "C-00001"}, input: "y\n"},
		{name: "accept-Y", args: []string{"comment", "rm", "C-00001"}, input: "Y\n"},
		{name: "abort-n", args: []string{"comment", "rm", "C-00001"}, input: "n\n"},
		{name: "purge-prompts-too", args: []string{"comment", "rm", "C-00001", "--purge"}, input: "y\n"},
		{name: "yes-flag-skips-prompt", args: []string{"comment", "rm", "C-00001", "--yes"}, input: ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			setup := [][]string{{"touch", "inbox/cm", "-t", "CM"}, {"comment", "add", "inbox/cm", "a comment body"}}
			base := seedFixture(t, bins, setup)
			oldDir := copyFixture(t, base)
			newDir := copyFixture(t, base)

			oldOut, oldExit := runCLIPromptTTY(t, bins.wrkq, oldDir, tc.args, tc.input)
			newOut, newExit := runCLIPromptTTY(t, bins.wrkq, newDir, tc.args, tc.input)

			if oldExit != newExit {
				t.Errorf("exit code: old=%d new=%d\n old: %q\n new: %q", oldExit, newExit, oldOut, newOut)
			}
			if normalize(newOut) != normalize(oldOut) {
				t.Errorf("prompt tty output mismatch:\n old: %q\n new: %q", oldOut, newOut)
			}

			// The prompting cases MUST render the legacy "[y/N]:" line (and NO
			// "Type 'yes' to confirm" — that is rm's distinct shape).
			if tc.name != "yes-flag-skips-prompt" {
				if !strings.Contains(oldOut, "[y/N]:") {
					t.Fatalf("legacy comment-rm prompt missing %q (got %q)", "[y/N]:", oldOut)
				}
				if strings.Contains(oldOut, "Type 'yes' to confirm") {
					t.Fatalf("comment-rm must NOT reuse rm's confirm shape (got %q)", oldOut)
				}
			}
			if got, want := snapshot(t, newDir), snapshot(t, oldDir); got != want {
				t.Errorf("durable snapshot mismatch:\n old:\n%s\n new:\n%s", want, got)
			}
		})
	}
}

// TestCpPromptOwnership proves the mirror owns the legacy `cp` >5-source
// confirmation on a real TTY: the inline "Copy <n> tasks? [y/N] " line on stderr
// (no warning block; distinct from rm's "yes"-token shape), accepting a
// y-prefixed answer, the abort branch ("n" → aborted, exit 1), and --yes (no
// prompt). The prompt only engages for >5 sources; the terminal output + exit
// code + the durable snapshot byte-match legacy `wrkq cp`.
func TestCpPromptOwnership(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + drives a real pty")
	}
	bins := buildProductionBinaries(t)

	// Six sources so the >5 prompt engages; a destination container to copy into.
	setup := [][]string{
		{"mkdir", "dest"},
		{"touch", "inbox/c1", "-t", "C1"}, {"touch", "inbox/c2", "-t", "C2"},
		{"touch", "inbox/c3", "-t", "C3"}, {"touch", "inbox/c4", "-t", "C4"},
		{"touch", "inbox/c5", "-t", "C5"}, {"touch", "inbox/c6", "-t", "C6"},
	}
	srcs := []string{"inbox/c1", "inbox/c2", "inbox/c3", "inbox/c4", "inbox/c5", "inbox/c6"}
	cpArgs := func(extra ...string) []string {
		return append(append([]string{"cp"}, srcs...), append([]string{"dest"}, extra...)...)
	}

	cases := []struct {
		name  string
		args  []string
		input string
	}{
		{name: "accept-y", args: cpArgs(), input: "y\n"},
		{name: "abort-n", args: cpArgs(), input: "n\n"},
		{name: "yes-flag-skips-prompt", args: cpArgs("--yes"), input: ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base := seedFixture(t, bins, setup)
			oldDir := copyFixture(t, base)
			newDir := copyFixture(t, base)

			oldOut, oldExit := runCLIPromptTTY(t, bins.wrkq, oldDir, tc.args, tc.input)
			newOut, newExit := runCLIPromptTTY(t, bins.wrkq, newDir, tc.args, tc.input)

			if oldExit != newExit {
				t.Errorf("exit code: old=%d new=%d\n old: %q\n new: %q", oldExit, newExit, oldOut, newOut)
			}
			if normalize(newOut) != normalize(oldOut) {
				t.Errorf("prompt tty output mismatch:\n old: %q\n new: %q", oldOut, newOut)
			}

			// The prompting cases MUST render the legacy "Copy N tasks? [y/N]" line
			// (and NOT rm's "Type 'yes' to confirm" shape).
			if tc.name != "yes-flag-skips-prompt" {
				if !strings.Contains(oldOut, "Copy 6 tasks? [y/N]") {
					t.Fatalf("legacy cp prompt missing %q (got %q)", "Copy 6 tasks? [y/N]", oldOut)
				}
				if strings.Contains(oldOut, "Type 'yes' to confirm") {
					t.Fatalf("cp must NOT reuse rm's confirm shape (got %q)", oldOut)
				}
			}
			if got, want := snapshot(t, newDir), snapshot(t, oldDir); got != want {
				t.Errorf("durable snapshot mismatch:\n old:\n%s\n new:\n%s", want, got)
			}
		})
	}
}

// TestRmdirForcePromptOwnership proves the mirror owns the legacy `rmdir --force`
// confirmation on a real TTY: the destructive WARNING block (with the rendered
// counts) + "Are you sure? (yes/no): " requiring EXACTLY "yes", the accept/abort
// branches, and --yes (no prompt). The terminal output + exit code + durable
// snapshot byte-match legacy `wrkq rmdir --force`.
func TestRmdirForcePromptOwnership(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + drives a real pty")
	}
	bins := buildProductionBinaries(t)

	cases := []struct {
		name  string
		args  []string
		input string
	}{
		{name: "accept", args: []string{"rmdir", "doomed", "--force"}, input: "yes\n"},
		{name: "abort-no", args: []string{"rmdir", "doomed", "--force"}, input: "no\n"},
		{name: "abort-y-not-yes", args: []string{"rmdir", "doomed", "--force"}, input: "y\n"},
		{name: "yes-flag-skips-prompt", args: []string{"rmdir", "doomed", "--force", "--yes"}, input: ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			setup := [][]string{{"mkdir", "doomed"}, {"touch", "doomed/t1", "-t", "One"}, {"touch", "doomed/t2", "-t", "Two"}}
			base := seedFixture(t, bins, setup)
			oldDir := copyFixture(t, base)
			newDir := copyFixture(t, base)

			oldOut, oldExit := runCLIPromptTTY(t, bins.wrkq, oldDir, tc.args, tc.input)
			newOut, newExit := runCLIPromptTTY(t, bins.wrkq, newDir, tc.args, tc.input)

			if oldExit != newExit {
				t.Errorf("exit code: old=%d new=%d\n old: %q\n new: %q", oldExit, newExit, oldOut, newOut)
			}
			if normalize(newOut) != normalize(oldOut) {
				t.Errorf("prompt tty output mismatch:\n old: %q\n new: %q", oldOut, newOut)
			}

			if tc.name != "yes-flag-skips-prompt" {
				for _, want := range []string{"WARNING: This will permanently delete", "Are you sure? (yes/no):"} {
					if !strings.Contains(oldOut, want) {
						t.Fatalf("legacy rmdir --force prompt missing %q (got %q)", want, oldOut)
					}
				}
			}
			if got, want := snapshot(t, newDir), snapshot(t, oldDir); got != want {
				t.Errorf("durable snapshot mismatch:\n old:\n%s\n new:\n%s", want, got)
			}
		})
	}
}
