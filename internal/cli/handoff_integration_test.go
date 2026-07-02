package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

type handoffIntegrationExecResult struct {
	stdout string
	stderr string
	code   int
	err    error
}

func runHandoffIntegrationCLI(t *testing.T, args []string, stdin string) handoffIntegrationExecResult {
	t.Helper()
	t.Setenv("AGENT_SCOPE_REF", "")
	resetHandoffIntegrationState(t)

	var stdout, stderr bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetIn(strings.NewReader(stdin))

	err := rootCmd.Execute()
	result := handoffIntegrationExecResult{
		stdout: stdout.String(),
		stderr: stderr.String(),
		code:   ExitCodeForError(err),
		err:    err,
	}

	rootCmd.SetArgs(nil)
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	rootCmd.SetIn(os.Stdin)
	resetHandoffIntegrationState(t)
	return result
}

func resetHandoffIntegrationState(t *testing.T) {
	t.Helper()

	resetHandoffCreateFlags()
	resetHandoffListFlags()
	resetHandoffGetFlags()
	resetHandoffAckFlags(handoffAckCmd)
	resetHandoffSearchFlags()

	for _, name := range []string{"db", "as", "project"} {
		if err := rootCmd.PersistentFlags().Set(name, ""); err != nil {
			t.Fatalf("reset persistent flag %s: %v", name, err)
		}
		if f := rootCmd.PersistentFlags().Lookup(name); f != nil {
			f.Changed = false
		}
	}
}

func mustIntegrationCreate(t *testing.T, args []string, stdin string) handoffCreateOutput {
	t.Helper()
	res := runHandoffIntegrationCLI(t, args, stdin)
	if res.code != 0 || res.err != nil {
		t.Fatalf("create failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	var out handoffCreateOutput
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("decode create output: %v\n%s", err, res.stdout)
	}
	return out
}

func mustIntegrationList(t *testing.T, args []string) handoffListOutput {
	t.Helper()
	res := runHandoffIntegrationCLI(t, args, "")
	if res.code != 0 || res.err != nil {
		t.Fatalf("list failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	return decodeHandoffListOutput(t, res.stdout)
}

func mustIntegrationGet(t *testing.T, args []string) handoffJSON {
	t.Helper()
	res := runHandoffIntegrationCLI(t, args, "")
	if res.code != 0 || res.err != nil {
		t.Fatalf("get failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	var out handoffJSON
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("decode get output: %v\n%s", err, res.stdout)
	}
	return out
}

func mustIntegrationAck(t *testing.T, args []string) handoffAckOutput {
	t.Helper()
	res := runHandoffIntegrationCLI(t, args, "")
	if res.code != 0 || res.err != nil {
		t.Fatalf("acknowledge failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	var out handoffAckOutput
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("decode acknowledge output: %v\n%s", err, res.stdout)
	}
	return out
}

func mustIntegrationSearch(t *testing.T, args []string) handoffSearchOutput {
	t.Helper()
	res := runHandoffIntegrationCLI(t, args, "")
	if res.code != 0 || res.err != nil {
		t.Fatalf("search failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	return decodeHandoffSearchOutput(t, res.stdout)
}

func requireHandoffIDs(t *testing.T, handoffs []handoffJSON, want ...string) {
	t.Helper()
	got := map[string]bool{}
	for _, h := range handoffs {
		got[h.ID] = true
	}
	for _, id := range want {
		if !got[id] {
			t.Fatalf("missing handoff %s in %+v", id, got)
		}
	}
}

func requireOnlyHandoffIDs(t *testing.T, handoffs []handoffJSON, want ...string) {
	t.Helper()
	if len(handoffs) != len(want) {
		t.Fatalf("got %d handoffs, want %d: %+v", len(handoffs), len(want), handoffs)
	}
	requireHandoffIDs(t, handoffs, want...)
}

func TestHandoffIntegrationRoundTrip(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	// Force lexical-only search so the term-match assertions below are
	// deterministic regardless of any dense embedder configured on the host.
	t.Setenv("WRKQ_SEARCH_DENSE_PROVIDER", "none")
	t.Setenv("ASP_SCOPE_REF", "agent:larry:project:wrkq:task:T-01603")
	t.Setenv("ASP_AGENT_ID", "larry")
	t.Setenv("ASP_PROJECT", "wrkq")
	t.Setenv("ASP_HANDLE", "")
	scopeRef := "agent:larry:project:wrkq"

	first := mustIntegrationCreate(t,
		[]string{"handoff", "create", "-t", "first", "--body-file", "-"},
		"body of first\n")
	if first.Handoff.ID != "H-00001" || first.Handoff.Status != "pending" || first.Handoff.ScopeRef != scopeRef {
		t.Fatalf("unexpected first create: %+v", first.Handoff)
	}

	second := mustIntegrationCreate(t,
		[]string{"handoff", "create", "-t", "second", "--body-file", "-"},
		"body of second\n")
	if second.Handoff.ID != "H-00002" || second.Handoff.Status != "pending" {
		t.Fatalf("unexpected second create: %+v", second.Handoff)
	}

	idemArgs := []string{"handoff", "create", "-t", "first", "--body-file", "-", "--idempotency-key", "foo"}
	third := mustIntegrationCreate(t, idemArgs, "body of idempotent first\n")
	if third.Handoff.ID != "H-00003" || third.IdempotentReplay {
		t.Fatalf("unexpected first idempotent create: %+v", third)
	}
	replay := mustIntegrationCreate(t, idemArgs, "body of idempotent first\n")
	if !replay.IdempotentReplay || replay.Handoff.ID != third.Handoff.ID || replay.Handoff.UUID != third.Handoff.UUID {
		t.Fatalf("unexpected idempotent replay: first=%+v replay=%+v", third, replay)
	}

	pendingList := mustIntegrationList(t, []string{"handoff", "list"})
	requireOnlyHandoffIDs(t, pendingList.Handoffs, "H-00001", "H-00002", "H-00003")
	for _, h := range pendingList.Handoffs {
		if h.Status != "pending" {
			t.Fatalf("default list returned non-pending handoff: %+v", h)
		}
	}

	gotFirst := mustIntegrationGet(t, []string{"handoff", "get", "H-00001", "--json"})
	if gotFirst.ID != "H-00001" || gotFirst.Title != "first" || gotFirst.Body != "body of first" {
		t.Fatalf("unexpected get H-00001 result: %+v", gotFirst)
	}

	ack := mustIntegrationAck(t, []string{"handoff", "acknowledge", "H-00001", "--note", "consumed"})
	if ack.Handoff.ID != "H-00001" || ack.Handoff.Status != "acknowledged" {
		t.Fatalf("unexpected acknowledge result: %+v", ack)
	}
	if ack.Handoff.AcknowledgementNote == nil || *ack.Handoff.AcknowledgementNote != "consumed" {
		t.Fatalf("acknowledgement note did not round-trip: %+v", ack.Handoff)
	}

	pendingAfterAck := mustIntegrationList(t, []string{"handoff", "list"})
	requireOnlyHandoffIDs(t, pendingAfterAck.Handoffs, "H-00002", "H-00003")

	acknowledgedList := mustIntegrationList(t, []string{"handoff", "list", "--status", "acknowledged"})
	requireOnlyHandoffIDs(t, acknowledgedList.Handoffs, "H-00001")
	if acknowledgedList.Handoffs[0].Status != "acknowledged" {
		t.Fatalf("acknowledged list returned wrong status: %+v", acknowledgedList.Handoffs[0])
	}

	allList := mustIntegrationList(t, []string{"handoff", "list", "--status", "all"})
	requireOnlyHandoffIDs(t, allList.Handoffs, "H-00001", "H-00002", "H-00003")

	defaultSearch := mustIntegrationSearch(t, []string{"handoff", "search", "first"})
	requireOnlyHandoffIDs(t, defaultSearch.Handoffs, "H-00003")
	if defaultSearch.Handoffs[0].Status != "pending" {
		t.Fatalf("default search returned non-pending handoff: %+v", defaultSearch.Handoffs[0])
	}

	allSearch := mustIntegrationSearch(t, []string{"handoff", "search", "first", "--status", "all"})
	requireOnlyHandoffIDs(t, allSearch.Handoffs, "H-00001", "H-00003")

	scopeSearch := mustIntegrationSearch(t, []string{"handoff", "search", scopeRef})
	requireOnlyHandoffIDs(t, scopeSearch.Handoffs, "H-00002", "H-00003")

	var createEvents, ackEvents, handoffEvents int
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM event_log
		WHERE resource_type = 'handoff' AND event_type = 'handoff.created'
	`).Scan(&createEvents); err != nil {
		t.Fatalf("count create events: %v", err)
	}
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM event_log
		WHERE resource_type = 'handoff' AND event_type = 'handoff.acknowledged'
	`).Scan(&ackEvents); err != nil {
		t.Fatalf("count acknowledge events: %v", err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM event_log WHERE resource_type = 'handoff'`).Scan(&handoffEvents); err != nil {
		t.Fatalf("count handoff events: %v", err)
	}
	if createEvents != 3 || ackEvents != 1 || handoffEvents != 4 {
		t.Fatalf("unexpected event counts: created=%d acknowledged=%d total=%d", createEvents, ackEvents, handoffEvents)
	}
}

func TestHandoffIntegrationExitCodeContracts(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:larry:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "larry")
	t.Setenv("ASP_PROJECT", "wrkq")
	t.Setenv("ASP_HANDLE", "")

	ok := runHandoffIntegrationCLI(t,
		[]string{"handoff", "create", "-t", "exit-code seed", "--body-file", "-", "--idempotency-key", "exit-code-key"},
		"same body")
	if ok.code != 0 {
		t.Fatalf("successful create exit code=%d stdout=%s stderr=%s", ok.code, ok.stdout, ok.stderr)
	}

	cases := []struct {
		name  string
		args  []string
		stdin string
		want  int
	}{
		{
			name: "validation error exits 1",
			args: []string{"handoff", "list", "--status", "bogus"},
			want: 1,
		},
		{
			name: "unresolvable scope exits 2",
			args: []string{"handoff", "list", "--scope", "not a valid scope"},
			want: 2,
		},
		{
			name:  "idempotency payload mismatch exits 3",
			args:  []string{"handoff", "create", "-t", "exit-code seed", "--body-file", "-", "--idempotency-key", "exit-code-key"},
			stdin: "different body",
			want:  3,
		},
		{
			name: "get not found exits 4",
			args: []string{"handoff", "get", "H-99999", "--json"},
			want: 4,
		},
		{
			name: "already acknowledged exits 5",
			args: []string{"handoff", "acknowledge", "H-00001"},
			want: 5,
		},
		{
			name: "etag mismatch exits 6",
			args: []string{"handoff", "acknowledge", "H-00002", "--if-match", "999"},
			want: 6,
		},
	}

	ack := runHandoffIntegrationCLI(t, []string{"handoff", "acknowledge", "H-00001"}, "")
	if ack.code != 0 {
		t.Fatalf("seed acknowledge exit code=%d stdout=%s stderr=%s", ack.code, ack.stdout, ack.stderr)
	}
	createSecond := runHandoffIntegrationCLI(t, []string{"handoff", "create", "-t", "etag seed", "--body-file", "-"}, "body")
	if createSecond.code != 0 {
		t.Fatalf("seed second create exit code=%d stdout=%s stderr=%s", createSecond.code, createSecond.stdout, createSecond.stderr)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := runHandoffIntegrationCLI(t, tc.args, tc.stdin)
			if res.code != tc.want {
				t.Fatalf("exit code=%d, want %d; err=%v stdout=%s stderr=%s", res.code, tc.want, res.err, res.stdout, res.stderr)
			}
		})
	}
}
