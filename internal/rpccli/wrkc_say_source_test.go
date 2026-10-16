//go:build wrkq_local

package rpccli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/clifunnel"
	"github.com/lherron/wrkq/internal/db"
)

// T-11007: wrkc say takes exactly one positional, the ref, and an explicit body
// source: -m <body> or a positional "-" that reads stdin. Under an agent
// harness stdin is an open socket that never sends EOF, so every refusal here
// runs against a stdin held open by a writer that never closes. A refusal that
// read stdin first would hang, not fail.

// runWrkcHeldStdin runs wrkc with stdin as the caller supplies it, returns its
// stdout, and fails the test if the command has not returned within three
// seconds.
func runWrkcHeldStdin(t *testing.T, dbPath, principal string, stdin io.Reader, args ...string) (string, error) {
	t.Helper()
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		cmd := NewWrkcRootCmd()
		cmd.SetArgs(append([]string{"--db", dbPath, "--principal-ref", principal}, args...))
		cmd.SetIn(stdin)
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		err := cmd.Execute()
		done <- result{stdout.String(), err}
	}()
	select {
	case r := <-done:
		return r.out, r.err
	case <-time.After(3 * time.Second):
		t.Fatalf("wrkc %v blocked reading stdin", args)
		return "", nil
	}
}

// heldOpenStdin is a pipe whose writer stays open for the whole test: a read
// from it never reaches EOF.
func heldOpenStdin(t *testing.T) io.Reader {
	t.Helper()
	reader, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	return reader
}

func countWrkcEnvelopes(t *testing.T, dbPath string) int {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM envelopes`).Scan(&n); err != nil {
		t.Fatalf("count envelopes: %v", err)
	}
	return n
}

// seedWrkcEnvelope sends one envelope from alice to bob and returns its id, the
// EN-x every case below replies to.
func seedWrkcEnvelope(t *testing.T, f wrkcFixture) string {
	t.Helper()
	out, err := runWrkc(t, f.dbPath, "agent:alice", "say", f.taskID, "-m", "question for bob",
		"--to", "bob@wrkc-proj:primary", "--json")
	if err != nil {
		t.Fatalf("seed say: %v\n%s", err, out)
	}
	var said roomSayResultWire
	if err := json.Unmarshal([]byte(out), &said); err != nil || len(said.Envelopes) != 1 {
		t.Fatalf("decode seed say: %v\n%s", err, out)
	}
	return said.Envelopes[0].ID
}

func TestWrkcSayWithoutAnExplicitBodySourceRefusesBeforeReadingStdin(t *testing.T) {
	f := newWrkcFixture(t)
	en := seedWrkcEnvelope(t, f)
	to := "alice@wrkc-proj:primary"
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		// The four shapes the task names.
		{"one-word body where the ref goes", []string{"say", "--to", to, "--fyi", "hello"}, "-m <body>"},
		{"multi-word body where the ref goes", []string{"say", "--to", to, "--fyi", "multi word"}, "not a room ref"},
		{"forgotten heredoc", []string{"say", en, "--to", to}, "<<'EOF'"},
		{"positional body and an extra", []string{"say", en, "--to", to, "body", "extra"}, "one positional"},
		// The rest of the contract.
		{"positional body", []string{"say", en, "--to", to, "body"}, "one positional"},
		{"dash then an extra", []string{"say", en, "--to", to, "-", "extra"}, "one positional"},
		{"-m - is not stdin", []string{"say", en, "--to", to, "-m", "-"}, "positional -"},
		{"both sources", []string{"say", en, "--to", to, "-m", "hi", "-"}, "not both"},
		{"no ref", []string{"say", "--to", to, "-m", "hi"}, "room ref"},
		{"dash without a ref", []string{"say", "--to", to, "-"}, "room ref"},
		{"no ref no to", []string{"say"}, "room ref"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := countWrkcEnvelopes(t, f.dbPath)
			out, err := runWrkcHeldStdin(t, f.dbPath, "agent:bob", heldOpenStdin(t), tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want a %q refusal, got err=%v output=%q", tc.want, err, out)
			}
			var usage *clifunnel.UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("refusal is not a usage error (exit %d): %v", clifunnel.ExitCode(err, 1), err)
			}
			if after := countWrkcEnvelopes(t, f.dbPath); after != before {
				t.Fatalf("a refused say wrote %d envelopes", after-before)
			}
		})
	}
}

// Rule 3: "-" asks for stdin, but a terminal or a socket never carries a
// heredoc or a pipe, so wrkc refuses instead of parking in read().
func TestWrkcSayDashRefusesASocketStdinWithoutReading(t *testing.T) {
	f := newWrkcFixture(t)
	en := seedWrkcEnvelope(t, f)
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	socket, peer := os.NewFile(uintptr(fds[0]), "stdin-socket"), os.NewFile(uintptr(fds[1]), "peer")
	t.Cleanup(func() { _ = socket.Close(); _ = peer.Close() })

	before := countWrkcEnvelopes(t, f.dbPath)
	out, err := runWrkcHeldStdin(t, f.dbPath, "agent:bob", socket, "say", en, "--to", "alice@wrkc-proj:primary", "-")
	if err == nil || !strings.Contains(err.Error(), "use a heredoc (<<'EOF') or -m") {
		t.Fatalf("want the socket refusal, got err=%v output=%q", err, out)
	}
	var usage *clifunnel.UsageError
	if !errors.As(err, &usage) {
		t.Fatalf("socket refusal is not a usage error: %v", err)
	}
	if after := countWrkcEnvelopes(t, f.dbPath); after != before {
		t.Fatalf("a refused say wrote %d envelopes", after-before)
	}
}

func TestWrkcSayDeliversFromAnExplicitBodySource(t *testing.T) {
	f := newWrkcFixture(t)
	en := seedWrkcEnvelope(t, f)
	to := "alice@wrkc-proj:primary"

	// zsh hands a heredoc over as a regular file, bash 5.1+ as a pipe.
	heredocFile := t.TempDir() + "/heredoc"
	if err := os.WriteFile(heredocFile, []byte("heredoc body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	asFile, err := os.Open(heredocFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = asFile.Close() })
	pipeReader, pipeWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pipeReader.Close() })
	go func() { _, _ = pipeWriter.WriteString("piped body\n"); _ = pipeWriter.Close() }()
	bodyFile := t.TempDir() + "/body.md"
	if err := os.WriteFile(bodyFile, []byte("file body\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		stdin io.Reader
		args  []string
		want  string
	}{
		{"dash from a heredoc file", asFile, []string{"say", en, "--to", to, "-"}, "heredoc body\n"},
		{"dash from a pipe", pipeReader, []string{"say", en, "--to", to, "-"}, "piped body\n"},
		{"-m literal never touches stdin", heldOpenStdin(t), []string{"say", en, "--to", to, "-m", "hi"}, "hi"},
		{"-m @file never touches stdin", heldOpenStdin(t), []string{"say", en, "--to", to, "-m", "@" + bodyFile}, "file body\n"},
		{"agent@project ref routes as before", heldOpenStdin(t), []string{"say", to, "--to", to, "--fyi", "-m", "adhoc"}, "adhoc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runWrkcHeldStdin(t, f.dbPath, "agent:bob", tc.stdin, append(tc.args, "--json")...)
			if err != nil {
				t.Fatalf("say: %v\n%s", err, out)
			}
			var said roomSayResultWire
			if err := json.Unmarshal([]byte(out), &said); err != nil {
				t.Fatalf("decode say: %v\n%s", err, out)
			}
			if len(said.Envelopes) != 1 || said.Envelopes[0].Body != strings.TrimSpace(tc.want) || said.Envelopes[0].To == nil {
				t.Fatalf("say delivered %+v, want one addressed envelope with body %q", said.Envelopes, tc.want)
			}
		})
	}
}
