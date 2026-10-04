//go:build wrkq_local

package clifunnel_test

// End-to-end pins for the CLI-standard fixes from the T-10175 cli-review: every
// case runs a freshly built binary against a scratch database and asserts the
// exit code and the message shape an agent sees. First lines and exit codes
// that predate the review are asserted unchanged; the review only added lines.

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type cliCase struct {
	name  string
	bin   string
	args  []string
	env   []string // extra KEY=VALUE pairs
	stdin string   // "devnull" (default) or "closed"
	exit  int
	// stdout/stderr substrings that must appear, and ones that must not.
	stdout, stderr       []string
	notStdout, notStderr []string
	// stderrLines, when > 0, pins the exact number of non-empty stderr lines.
	stderrLines int
}

func TestCLIStandardFunnelE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("builds every shipped binary")
	}
	bins := buildBinaries(t, "wrkq", "wrkf", "wrkc", "wrkp", "wrkqadm", "wrkqd")
	scratch := t.TempDir()
	dbPath := filepath.Join(scratch, "wrkq.db")
	catalog := filepath.Join(scratch, "hook-catalog.json")
	if err := os.WriteFile(catalog, []byte(`{"schemaVersion":"wrkf.hook-catalog.v0","hooks":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	baseEnv := []string{
		"PATH=" + filepath.Dir(bins["wrkq"]) + ":/usr/bin:/bin",
		"HOME=" + scratch,
		"TMPDIR=" + scratch,
		"WRKQ_DB=" + dbPath,
		"WRKQ_PRINCIPAL_REF=agent:clifunnel-e2e",
		"WRKF_HOOK_CATALOG=" + catalog,
		"EDITOR=false",
	}
	mustRun(t, bins, baseEnv, scratch, "wrkqadm", "init")
	mustRun(t, bins, baseEnv, scratch, "wrkq", "touch", "inbox/dup", "-t", "first")

	const down = "WRKQ_DB=rpc://127.0.0.1:1"
	cases := []cliCase{
		// §1: --help anywhere is inert; it never runs the command.
		{name: "help as a flag value never executes", bin: "wrkq", args: []string{"index", "pause", "--as", "--help"},
			exit: 0, stdout: []string{"Usage:\n  wrkq index pause"}, notStdout: []string{`"status"`}},
		{name: "help after a positional", bin: "wrkq", args: []string{"cat", "T-00042", "--help"},
			exit: 0, stdout: []string{"Usage:\n  wrkq cat"}, notStderr: []string{"not found"}},
		{name: "wrkc help as a flag value", bin: "wrkc", args: []string{"ls", "--output", "--help"},
			exit: 0, stdout: []string{"Usage:\n  wrkc ls"}},
		{name: "wrkp help as a flag value", bin: "wrkp", args: []string{"log", "--project", "--help"},
			exit: 0, stdout: []string{"Usage:\n  wrkp log"}},
		{name: "wrkf help as a flag value", bin: "wrkf", args: []string{"task", "inspect", "--task", "--help"},
			exit: 0, stdout: []string{"wrkf task inspect"}},
		{name: "wrkqadm help as a flag value", bin: "wrkqadm", args: []string{"db", "snapshot", "--out", "--help"},
			exit: 0, stdout: []string{"Usage:\n  wrkqadm db snapshot"}},
		{name: "wrkqd help after a positional does not start the daemon", bin: "wrkqd", args: []string{"foo", "--help"},
			exit: 0, stdout: []string{"-addr string"}},
		{name: "wrkqd help as a flag value", bin: "wrkqd", args: []string{"-db", "--help"},
			exit: 0, stdout: []string{"-addr string"}},
		{name: "wrkqd version help exits 0", bin: "wrkqd", args: []string{"version", "--help"},
			exit: 0, stdout: []string{"-json"}, notStderr: []string{"help requested"}},
		{name: "passthrough command hands --help to the wrapped tool", bin: "wrkp", args: []string{"just", "--help"},
			exit: 1, stderr: []string{"cannot find the just binary"}, notStdout: []string{"Usage:"}},

		// §4: usage errors name the usage line and the help command; exit codes unchanged.
		{name: "unknown flag suggests the nearest flag", bin: "wrkq", args: []string{"cat", "T-00001", "--outptu", "json"},
			exit: 2, stderr: []string{"Error: unknown flag: --outptu\nDid you mean --output?\nUsage: wrkq cat", "Run 'wrkq cat --help'"}},
		{name: "unknown flag far from any flag has no suggestion", bin: "wrkc", args: []string{"inbox", "--no-such-flag-xyz"},
			exit: 2, stderr: []string{"Error: unknown flag: --no-such-flag-xyz\nUsage: wrkc inbox", "Run 'wrkc inbox --help'"}, notStderr: []string{"Did you mean"}},
		{name: "missing argument names the usage line", bin: "wrkq", args: []string{"cat"},
			exit: 2, stderr: []string{"Error: requires at least 1 arg(s), only received 0\nUsage: wrkq cat", "Run 'wrkq cat --help'"}},
		{name: "wrkf missing argument names the usage line", bin: "wrkf", args: []string{"run", "bind"},
			exit: 2, stderr: []string{"Error: accepts 3 arg(s), received 0\nUsage: wrkf run bind", "Run 'wrkf run bind --help'"}},
		{name: "wrkqadm missing required flag names the usage line", bin: "wrkqadm", args: []string{"patch", "apply"},
			exit: 2, stderr: []string{`Error: required flag(s) "patch" not set` + "\nUsage: wrkqadm patch apply", "Run 'wrkqadm patch apply --help'"}},
		{name: "unknown root command keeps cobra's suggestion", bin: "wrkp", args: []string{"lgo"},
			exit: 2, stderr: []string{`Error: unknown command "lgo" for "wrkp"`, "log", "Run 'wrkp --help'"}},

		// §4: not-found names the valid forms and the listing command.
		{name: "task not found hint", bin: "wrkq", args: []string{"cat", "T-99999"},
			exit: 1, stderr: []string{"Error: task not found: T-99999\nhint: a task is T-<n>", "wrkq find --state all --type t"}},
		{name: "envelope not found hint", bin: "wrkc", args: []string{"show", "EN-99999"},
			exit: 1, stderr: []string{"envelope not found: EN-99999\nhint: an envelope is EN-<n>", "wrkc inbox"}},
		{name: "room not found hint", bin: "wrkc", args: []string{"hide", "R-99999"},
			exit: 1, stderr: []string{"room not found: R-99999\nhint: a room is R-<n>", "wrkc ls"}},
		{name: "project event not found hint", bin: "wrkp", args: []string{"show", "PE-99999"},
			exit: 1, stderr: []string{"project event not found: PE-99999\nhint: an event is PE-<n>", "wrkp log --project"}},
		{name: "rm target not found hint", bin: "wrkq", args: []string{"rm", "no-such-thing"},
			exit: 1, stderr: []string{"Error: target not found: no-such-thing\nhint: a selector is a task or container", "wrkq tree"}},
		{name: "missing parent container is named, not the task path", bin: "wrkq", args: []string{"touch", "/nope/inbox/x", "-t", "x"},
			exit: 1, stderr: []string{"container not found: /nope/inbox\nhint: a container is P-<n>"}, notStderr: []string{"/nope/inbox/x"}},
		{name: "duplicate slug is a named conflict, not a SQLite error", bin: "wrkq", args: []string{"touch", "inbox/dup", "-t", "second"},
			exit: 1, stderr: []string{`a task with slug "dup" already exists at inbox/dup`, "wrkq cat inbox/dup"}, notStderr: []string{"UNIQUE constraint"}},
		{name: "sync-meta on a missing task is not an internal error", bin: "wrkf", args: []string{"task", "sync-meta", "T-99999"},
			exit: 1, stderr: []string{"Error: task not found: T-99999\nhint: a task is T-<n>", "\ncode: WRKQ_NOT_FOUND\n"}, notStderr: []string{"internal error", "WRKQ_NOT_FOUND: "}},
		{name: "sync-meta on a task with no workflow is typed", bin: "wrkf", args: []string{"task", "sync-meta", "T-00001"},
			exit: 1, stderr: []string{"Error: workflow instance not found\nhint: the task has no workflow attached", "wrkf task attach", "\ncode: WRKF_NOT_FOUND\n"}, notStderr: []string{"internal error", "WRKF_NOT_FOUND: "}},
		{name: "wrkf keeps the kind and ref of a wrapped not-found", bin: "wrkf", args: []string{"effect", "claim", "EF-99999"},
			exit: 1, stderr: []string{"Error: task not found: EF-99999", "\ncode: WRKF_NOT_FOUND\n"}, notStderr: []string{"resource not found", "WRKF_NOT_FOUND: "}},

		// §11: daemon-unreachable is translated once, at the client.
		{name: "wrkq daemon unreachable", bin: "wrkq", args: []string{"cat", "T-00001"}, env: []string{down},
			exit: 1, stderr: []string{"wrkq daemon unreachable at rpc://127.0.0.1:1", "connection refused", "retrying is safe", "wrkq server health --addr 127.0.0.1:1"}},
		{name: "wrkc daemon unreachable", bin: "wrkc", args: []string{"inbox"}, env: []string{down},
			exit: 1, stderr: []string{"wrkq daemon unreachable at rpc://127.0.0.1:1", "wrkq server health --addr 127.0.0.1:1"}},
		{name: "wrkp daemon unreachable", bin: "wrkp", args: []string{"types"}, env: []string{down},
			exit: 1, stderr: []string{"wrkq daemon unreachable at rpc://127.0.0.1:1"}},
		{name: "wrkf daemon unreachable", bin: "wrkf", args: []string{"workflow", "list"}, env: []string{down, "WRKF_HOOK_CATALOG="},
			exit: 1, stderr: []string{"wrkq daemon unreachable at rpc://127.0.0.1:1"}},

		// §8: /dev/null and a closed stdin are empty input, never "a terminal".
		{name: "devnull stdin reads as empty", bin: "wrkq", args: []string{"comment", "add", "T-00001"},
			exit: 1, stderr: []string{"stdin is empty: comment body needs content"}, notStderr: []string{"terminal"}},
		{name: "closed stdin reads as empty", bin: "wrkq", args: []string{"comment", "add", "T-00001"}, stdin: "closed",
			exit: 1, stderr: []string{"stdin is empty: comment body needs content"}, notStderr: []string{"terminal"}},
		{name: "wrkc say from devnull reads as empty", bin: "wrkc", args: []string{"say", "T-00001", "-m", "-"},
			exit: 1, stderr: []string{"stdin is empty: body needs content"}, notStderr: []string{"terminal"}},
		{name: "wrkf stdin flag from devnull reads as empty", bin: "wrkf", args: []string{"action", "fail", "run_x", "--run-summary", "-"},
			exit: 1, stderr: []string{"--run-summary: stdin is empty"}, notStderr: []string{"terminal"}},

		{name: "monitor's own exit path carries the hint and prints once", bin: "wrkq", args: []string{"monitor", "watch", "T-99999"},
			exit: 2, stderr: []string{"task not found: T-99999\nhint: a task is T-<n>"}, stderrLines: 2},
		{name: "wrkf blank help descriptions are filled", bin: "wrkf", args: []string{"run", "--help"},
			exit: 0, stdout: []string{"  bind        Start a run bound to a role and delivery handle"}},

		// Missing dependencies and unconfigured state name the fix.
		{name: "wrkq agent without hrcchat names where it comes from", bin: "wrkq", args: []string{"agent", "x"},
			exit: 1, stderr: []string{"hrcchat not found on PATH", "ships with hrc-runtime"}},
		{name: "doctor names an unconfigured attachment dir", bin: "wrkqadm", args: []string{"doctor"},
			env: []string{"WRKQ_ATTACH_DIR="}, exit: 1, stdout: []string{"Attachment directory is not configured; set WRKQ_ATTACH_DIR"},
			notStdout: []string{"Attachment directory not found: \n"}},

		// T-10234 (2): a mistyped subcommand under a group is an unknown-command usage error.
		{name: "mistyped subcommand under a wrkq group", bin: "wrkq", args: []string{"comment", "adx"},
			exit: 2, stderr: []string{`Error: unknown command "adx" for "wrkq comment"`, "Did you mean add?", "Run 'wrkq comment --help'"}, notStdout: []string{"Usage:"}},
		{name: "mistyped subcommand under a wrkf group", bin: "wrkf", args: []string{"run", "lst"},
			exit: 2, stderr: []string{`Error: unknown command "lst" for "wrkf run"`, "Run 'wrkf run --help'"}, notStdout: []string{"Usage:"}},
		{name: "mistyped subcommand under a wrkqadm group", bin: "wrkqadm", args: []string{"db", "snapshto"},
			exit: 2, stderr: []string{`Error: unknown command "snapshto" for "wrkqadm db"`}, notStdout: []string{"Usage:"}},
		{name: "mistyped subcommand under a wrkp group", bin: "wrkp", args: []string{"git", "comit"},
			exit: 2, stderr: []string{`Error: unknown command "comit" for "wrkp git"`}, notStdout: []string{"Usage:"}},
		{name: "a bare group still renders its help", bin: "wrkq", args: []string{"comment"},
			exit: 0, stdout: []string{"Usage:\n  wrkq comment"}},
		{name: "group help after a mistyped subcommand still renders", bin: "wrkq", args: []string{"comment", "adx", "--help"},
			exit: 0, stdout: []string{"Usage:\n  wrkq comment"}},

		// T-10234 (6): --output is validated against the binary's vocabulary on every command.
		{name: "wrkq unrecognized --output is a usage error", bin: "wrkq", args: []string{"index", "pause", "--output", "bogusvalue"},
			exit: 2, stderr: []string{`invalid output mode "bogusvalue": choose table, human, json, ndjson, porcelain, yaml, tsv, or raw`, "Run 'wrkq index pause --help'"}},
		{name: "monitor watch refuses a bad --output instead of following", bin: "wrkq", args: []string{"monitor", "watch", "T-00001", "--output", "bogusvalue"},
			exit: 2, stderr: []string{`invalid output mode "bogusvalue"`}},
		{name: "wrkc unrecognized --output is a usage error", bin: "wrkc", args: []string{"version", "--output", "bogusvalue"},
			exit: 2, stderr: []string{`invalid output mode "bogusvalue"`}},
		{name: "wrkp unrecognized --output names wrkp's vocabulary", bin: "wrkp", args: []string{"log", "--output", "bogusvalue"},
			exit: 2, stderr: []string{`invalid output mode "bogusvalue": choose human, json, ndjson, porcelain, yaml, or tsv`}},
		{name: "a recognized --output still works", bin: "wrkq", args: []string{"whoami", "--output", "json"},
			exit: 0, notStderr: []string{"invalid output mode"}},

		// T-10234 (4): --json errors are structured on stderr, usage errors included.
		{name: "wrkq --json not-found is structured", bin: "wrkq", args: []string{"cat", "T-99999", "--json"},
			exit: 1, stderr: []string{`"code": "WRKQ_NOT_FOUND"`, `"message": "task not found: T-99999"`, `"hint": "a task is T-<n>`}, notStderr: []string{"Error:"}},
		{name: "wrkc --output json not-found is structured", bin: "wrkc", args: []string{"show", "R-99999", "--output", "json"},
			exit: 1, stderr: []string{`"code": "WRKQ_NOT_FOUND"`, `"message": "room not found: R-99999"`}, notStderr: []string{"Error:"}},
		{name: "wrkp --output ndjson not-found is one structured line", bin: "wrkp", args: []string{"show", "PE-99999", "--output", "ndjson"},
			exit: 1, stderr: []string{`{"error":{"code":"WRKQ_NOT_FOUND","message":"project event not found: PE-99999"`}, stderrLines: 1},
		{name: "wrkq --json usage error is structured", bin: "wrkq", args: []string{"cat", "--json"},
			exit: 2, stderr: []string{`"code": "WRKQ_USAGE"`, `"message": "requires at least 1 arg(s), only received 0"`, "Usage: wrkq cat"}, notStderr: []string{"Error:"}},
		{name: "wrkf --json usage error is structured", bin: "wrkf", args: []string{"run", "bind", "--json"},
			exit: 2, stderr: []string{`"code": "WRKF_USAGE"`}, notStderr: []string{"Error:"}},
		{name: "wrkf --json errors go to stderr, not stdout", bin: "wrkf", args: []string{"task", "sync-meta", "T-99999", "--json"},
			exit: 1, stderr: []string{`"code": "WRKQ_NOT_FOUND"`, `"message": "task not found: T-99999"`}, notStdout: []string{`"error"`}},
		{name: "wrkqadm --json usage error is structured", bin: "wrkqadm", args: []string{"patch", "apply", "--json"},
			exit: 2, stderr: []string{`"code": "WRKQ_USAGE"`}},
		{name: "agent-context --json names the error with a code", bin: "wrkq", args: []string{"agent-context", "--json"},
			env:  []string{"AGENT_SCOPE_REF=", "ASP_SCOPE_REF=", "ASP_HANDLE=", "ASP_AGENT_ID=", "ASP_PROJECT="},
			exit: 2, stdout: []string{`"error": {`, `"code": "scope_unresolvable"`}},
		{name: "handoff keeps its own structured error", bin: "wrkq", args: []string{"handoff", "get", "H-99999", "--json"},
			exit: 4, stderr: []string{`"code": "handoff_not_found"`}},

		// T-10234 (5): a validation error carries the typed invalid-param code.
		{name: "invalid room kind is WRKQ_VALIDATION, not internal", bin: "wrkc", args: []string{"ls", "--kind", "bogusvalue", "--output", "json"},
			exit: 1, stderr: []string{`"code": "WRKQ_VALIDATION"`, `invalid room kind \"bogusvalue\"`}, notStderr: []string{"WORKRPC_INTERNAL"}},

		// T-10234 (7a): wrkp log --follow terminates on --timeout / --stall-after like monitor watch.
		{name: "wrkp log --follow --timeout ends with a terminal line", bin: "wrkp", args: []string{"log", "inbox", "--follow", "--timeout", "1s", "--output", "ndjson"},
			exit: 0, stdout: []string{`{"type":"wrkp.log.terminal","result":"timeout","reason":"timed_out","unmet":[]`}},
		{name: "wrkp log --follow --stall-after ends with a terminal line", bin: "wrkp", args: []string{"log", "inbox", "--follow", "--stall-after", "1s", "--output", "ndjson"},
			exit: 0, stdout: []string{`{"type":"wrkp.log.terminal","result":"stall","reason":"stalled","unmet":[]`}},
		{name: "wrkp log clocks need --follow", bin: "wrkp", args: []string{"log", "--timeout", "1s"},
			exit: 2, stderr: []string{"--timeout and --stall-after bound a --follow"}},
		{name: "monitor watch --raw honors --timeout", bin: "wrkq", args: []string{"monitor", "watch", "--raw", "--timeout", "1s"},
			exit: 0, stdout: []string{`"type":"wrkq.monitor.terminal","result":"timeout"`}},

		// T-10234 (7b): wrkq watch is retired behind a pointer shim (CLI standard §13).
		{name: "retired wrkq watch points at monitor watch", bin: "wrkq", args: []string{"watch", "--since", "0"},
			exit: 2, stderr: []string{"wrkq watch was removed; use: wrkq monitor watch --raw"}, notStdout: []string{"event_type"}},
		{name: "retired wrkq watch refuses --help with the same pointer", bin: "wrkq", args: []string{"watch", "--help"},
			exit: 2, stderr: []string{"wrkq watch was removed; use: wrkq monitor watch --raw"}, notStdout: []string{"Usage:"}},

		// Rejected rulings stay unchanged: doctor keeps exit 1, wrkqd still ignores stray positionals.
		{name: "doctor still exits 1 on detected problems", bin: "wrkqadm", args: []string{"doctor"},
			env: []string{"WRKQ_ATTACH_DIR="}, exit: 1, stdout: []string{"Attachment directory is not configured"}},

		// A monitor usage error prints once.
		{name: "monitor usage error prints once", bin: "wrkq", args: []string{"monitor", "wait"},
			exit: 2, stderr: []string{"Error: monitor wait requires --until"}, stderrLines: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := runCase(t, bins, baseEnv, scratch, tc)
			if code != tc.exit {
				t.Errorf("exit = %d, want %d\nstdout: %s\nstderr: %s", code, tc.exit, stdout, stderr)
			}
			for _, want := range tc.stdout {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout missing %q\nstdout: %s", want, stdout)
				}
			}
			for _, bad := range tc.notStdout {
				if strings.Contains(stdout, bad) {
					t.Errorf("stdout contains %q\nstdout: %s", bad, stdout)
				}
			}
			for _, want := range tc.stderr {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr missing %q\nstderr: %s", want, stderr)
				}
			}
			for _, bad := range tc.notStderr {
				if strings.Contains(stderr, bad) {
					t.Errorf("stderr contains %q\nstderr: %s", bad, stderr)
				}
			}
			if tc.stderrLines > 0 {
				lines := 0
				for _, l := range strings.Split(stderr, "\n") {
					if strings.TrimSpace(l) != "" {
						lines++
					}
				}
				if lines != tc.stderrLines {
					t.Errorf("stderr has %d lines, want %d\nstderr: %s", lines, tc.stderrLines, stderr)
				}
			}
		})
	}
}

func runCase(t *testing.T, bins map[string]string, baseEnv []string, dir string, tc cliCase) (string, string, int) {
	t.Helper()
	argv := append([]string{bins[tc.bin]}, tc.args...)
	var cmd *exec.Cmd
	if tc.stdin == "closed" {
		quoted := make([]string, len(argv))
		for i, a := range argv {
			quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		cmd = exec.Command("/bin/sh", "-c", strings.Join(quoted, " ")+" 0<&-")
	} else {
		cmd = exec.Command(argv[0], argv[1:]...)
		cmd.Stdin = nil // /dev/null
	}
	cmd.Dir = dir
	cmd.Env = append(append([]string{}, baseEnv...), tc.env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %v: %v", argv, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		code := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else if err != nil {
			t.Fatalf("run %v: %v", argv, err)
		}
		return stdout.String(), stderr.String(), code
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("%v hung past 10s\nstdout: %s\nstderr: %s", argv, stdout.String(), stderr.String())
	}
	return "", "", -1
}

func mustRun(t *testing.T, bins map[string]string, env []string, dir, bin string, args ...string) {
	t.Helper()
	cmd := exec.Command(bins[bin], args...)
	cmd.Dir, cmd.Env = dir, env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", bin, args, err, out)
	}
}

func buildBinaries(t *testing.T, names ...string) map[string]string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "..")
	dir := t.TempDir()
	out := map[string]string{}
	for _, name := range names {
		path := filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-tags", "sqlite_fts5,wrkq_local", "-o", path, "./cmd/"+name)
		cmd.Dir = root
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build ./cmd/%s: %v\n%s", name, err, b)
		}
		out[name] = path
	}
	return out
}
