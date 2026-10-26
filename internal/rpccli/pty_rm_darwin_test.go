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

	"github.com/lherron/wrkq/internal/db"
)

// runCLIPromptTTY runs a CLI with stdin+stdout+stderr attached to a real
// pseudo-terminal (so isStdoutTTY reports true and the purge prompt engages its
// interactive path), writes `input` to the terminal master, and returns the
// captured terminal output + exit code.
//
// This is the prompt-ownership harness for the caller-owned-confirmation seam:
// the CLI (not the RPC server) renders each command's confirmation and honors
// the typed answer.
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

// ptyRowCount reads durable state straight from the fixture DB after the
// prompted command has exited, so each row asserts what the answer DID, not
// what it printed.
func ptyRowCount(t *testing.T, dir, query string, args ...any) int {
	t.Helper()
	database, err := db.Open(filepath.Join(dir, "wrkq.db"))
	if err != nil {
		t.Fatalf("row-state open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var n int
	if err := database.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("row-state query %q: %v", query, err)
	}
	return n
}

// promptCase is one typed answer at one command's confirmation: the exit code
// it must produce, prompt text that must (and must not) appear on the terminal,
// and the durable row state it must leave behind.
type promptCase struct {
	name     string
	args     []string
	input    string
	wantExit int
	wantText []string
	notText  []string
	wantRows func(t *testing.T, dir string)
}

func runPromptCases(t *testing.T, bins binaries, setup [][]string, cases []promptCase) {
	t.Helper()
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := seedFixture(t, bins, setup)
			out, exit := runCLIPromptTTY(t, bins.wrkq, dir, tc.args, tc.input)
			if exit != tc.wantExit {
				t.Errorf("exit = %d, want %d; terminal:\n%q", exit, tc.wantExit, out)
			}
			for _, want := range tc.wantText {
				if !strings.Contains(out, want) {
					t.Errorf("terminal missing %q; got %q", want, out)
				}
			}
			for _, not := range tc.notText {
				if strings.Contains(out, not) {
					t.Errorf("terminal must not contain %q; got %q", not, out)
				}
			}
			tc.wantRows(t, dir)
		})
	}
}

// TestRmPurgePromptOwnership: on a real TTY, `rm --purge` renders the WARNING
// block + "Type 'yes' to confirm:", purges the task only on "yes" (exit 0),
// leaves it in place on "no" (exit 1), and --yes skips the prompt and purges.
func TestRmPurgePromptOwnership(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + drives a real pty")
	}
	bins := buildProductionBinaries(t)

	prompt := []string{"WARNING: This will permanently delete", "Type 'yes' to confirm:"}
	taskRows := func(want int) func(*testing.T, string) {
		return func(t *testing.T, dir string) {
			t.Helper()
			if got := ptyRowCount(t, dir, `SELECT COUNT(*) FROM tasks WHERE slug = 'aaa'`); got != want {
				t.Errorf("tasks with slug aaa = %d, want %d", got, want)
			}
		}
	}
	runPromptCases(t, bins, [][]string{{"touch", "inbox/aaa", "-t", "Alpha"}}, []promptCase{
		{name: "yes purges", args: []string{"rm", "inbox/aaa", "--purge"}, input: "yes\n",
			wantExit: 0, wantText: prompt, wantRows: taskRows(0)},
		{name: "no aborts and keeps the task", args: []string{"rm", "inbox/aaa", "--purge"}, input: "no\n",
			wantExit: 1, wantText: prompt, wantRows: taskRows(1)},
		{name: "--yes skips the prompt and purges", args: []string{"rm", "inbox/aaa", "--purge", "--yes"},
			wantExit: 0, notText: prompt, wantRows: taskRows(0)},
	})
}

// TestCommentRmPromptOwnership: on a real TTY, `comment rm` prompts with an
// inline "[y/N]:" line (never rm's "Type 'yes'" shape) EVEN for soft-delete,
// accepts exactly y/Y (soft-deletes, or purges under --purge), treats "n" as a
// per-comment skip that leaves the comment live, and --yes skips the prompt.
func TestCommentRmPromptOwnership(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + drives a real pty")
	}
	bins := buildProductionBinaries(t)

	prompt := []string{"comment C-00001 (task T-00001)? [y/N]:"}
	rmShape := []string{"Type 'yes' to confirm"}
	commentState := func(wantRows, wantDeleted int) func(*testing.T, string) {
		return func(t *testing.T, dir string) {
			t.Helper()
			if got := ptyRowCount(t, dir, `SELECT COUNT(*) FROM comments WHERE id = 'C-00001'`); got != wantRows {
				t.Errorf("comment C-00001 rows = %d, want %d", got, wantRows)
			}
			if got := ptyRowCount(t, dir, `SELECT COUNT(*) FROM comments WHERE id = 'C-00001' AND deleted_at IS NOT NULL`); got != wantDeleted {
				t.Errorf("comment C-00001 soft-deleted rows = %d, want %d", got, wantDeleted)
			}
		}
	}
	setup := [][]string{{"touch", "inbox/cm", "-t", "CM"}, {"comment", "add", "inbox/cm", "a comment body"}}
	runPromptCases(t, bins, setup, []promptCase{
		{name: "y soft-deletes", args: []string{"comment", "rm", "C-00001"}, input: "y\n",
			wantExit: 0, wantText: prompt, notText: rmShape, wantRows: commentState(1, 1)},
		{name: "Y soft-deletes", args: []string{"comment", "rm", "C-00001"}, input: "Y\n",
			wantExit: 0, wantText: prompt, notText: rmShape, wantRows: commentState(1, 1)},
		{name: "n skips and keeps the comment live", args: []string{"comment", "rm", "C-00001"}, input: "n\n",
			wantExit: 0, wantText: prompt, notText: rmShape, wantRows: commentState(1, 0)},
		{name: "--purge prompts too and y purges", args: []string{"comment", "rm", "C-00001", "--purge"}, input: "y\n",
			wantExit: 0, wantText: prompt, notText: rmShape, wantRows: commentState(0, 0)},
		{name: "--yes skips the prompt and soft-deletes", args: []string{"comment", "rm", "C-00001", "--yes"},
			wantExit: 0, notText: append(append([]string{}, prompt...), rmShape...), wantRows: commentState(1, 1)},
	})
}

// TestCpPromptOwnership: on a real TTY, `cp` of more than five sources prompts
// "Copy 6 tasks? [y/N]" (never rm's "Type 'yes'" shape), copies all six into the
// destination on "y" (exit 0), copies nothing on "n" (exit 1), and --yes skips
// the prompt and copies.
func TestCpPromptOwnership(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + drives a real pty")
	}
	bins := buildProductionBinaries(t)

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
	prompt := []string{"Copy 6 tasks? [y/N]"}
	destTasks := func(want int) func(*testing.T, string) {
		return func(t *testing.T, dir string) {
			t.Helper()
			got := ptyRowCount(t, dir, `SELECT COUNT(*) FROM tasks WHERE project_uuid = (SELECT uuid FROM containers WHERE slug = 'dest')`)
			if got != want {
				t.Errorf("tasks under dest = %d, want %d", got, want)
			}
		}
	}
	runPromptCases(t, bins, setup, []promptCase{
		{name: "y copies all six", args: cpArgs(), input: "y\n",
			wantExit: 0, wantText: prompt, notText: []string{"Type 'yes' to confirm"}, wantRows: destTasks(6)},
		{name: "n aborts and copies nothing", args: cpArgs(), input: "n\n",
			wantExit: 1, wantText: prompt, wantRows: destTasks(0)},
		{name: "--yes skips the prompt and copies", args: cpArgs("--yes"),
			wantExit: 0, notText: prompt, wantRows: destTasks(6)},
	})
}

// TestRmdirForcePromptOwnership: on a real TTY, `rmdir --force` of a non-empty
// project renders the WARNING block + "Are you sure? (yes/no):", deletes the
// container and its tasks only on exactly "yes", keeps both on "no" and on "y"
// (nonzero exit), and --yes does NOT bypass the typed confirmation for a
// project.
func TestRmdirForcePromptOwnership(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries + drives a real pty")
	}
	bins := buildProductionBinaries(t)

	prompt := []string{"WARNING: This will permanently delete", "Are you sure? (yes/no):"}
	doomed := func(wantContainers, wantTasks int) func(*testing.T, string) {
		return func(t *testing.T, dir string) {
			t.Helper()
			if got := ptyRowCount(t, dir, `SELECT COUNT(*) FROM containers WHERE slug = 'doomed'`); got != wantContainers {
				t.Errorf("containers named doomed = %d, want %d", got, wantContainers)
			}
			if got := ptyRowCount(t, dir, `SELECT COUNT(*) FROM tasks WHERE slug IN ('t1', 't2')`); got != wantTasks {
				t.Errorf("doomed tasks = %d, want %d", got, wantTasks)
			}
		}
	}
	setup := [][]string{{"mkdir", "doomed"}, {"touch", "doomed/t1", "-t", "One"}, {"touch", "doomed/t2", "-t", "Two"}}
	runPromptCases(t, bins, setup, []promptCase{
		{name: "yes deletes container and tasks", args: []string{"rmdir", "doomed", "--force"}, input: "yes\n",
			wantExit: 0, wantText: prompt, wantRows: doomed(0, 0)},
		{name: "no aborts and keeps everything", args: []string{"rmdir", "doomed", "--force"}, input: "no\n",
			wantExit: 1, wantText: prompt, wantRows: doomed(1, 2)},
		{name: "y is not yes and keeps everything", args: []string{"rmdir", "doomed", "--force"}, input: "y\n",
			wantExit: 1, wantText: prompt, wantRows: doomed(1, 2)},
		{name: "--yes on a project still prompts and honors yes", args: []string{"rmdir", "doomed", "--force", "--yes"}, input: "yes\n",
			wantExit: 0, wantText: prompt, wantRows: doomed(0, 0)},
		{name: "--yes on a project still refuses no", args: []string{"rmdir", "doomed", "--force", "--yes"}, input: "no\n",
			wantExit: 1, wantText: prompt, wantRows: doomed(1, 2)},
	})
}
