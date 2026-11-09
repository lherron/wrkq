//go:build wrkq_local

package rpccli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/clifunnel"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// T-12864: one `wrkp log --follow` process follows several projects over the
// existing per-project timeline. Every follow here starts from cursors taken
// before the posts and ends on a short --timeout, so the entries under test are
// already in the opening drain: nothing depends on when a post lands.

const multiProjB = "rpccli-test-proj-b"

// wrkpCall is one RPC the test daemon served, in arrival order.
type wrkpCall struct {
	method    string
	container string
	inFlight  int // commit.wait calls in flight when this one arrived, itself included
}

type wrkpMultiFixture struct {
	endpoint string
	mu       sync.Mutex
	calls    []wrkpCall
	waiting  int
}

func newWrkpMultiFixture(t *testing.T) *wrkpMultiFixture {
	t.Helper()
	dbPath, _ := migratedDBWithTask(t)
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.Exec(
		`INSERT INTO containers (uuid, slug, title, parent_uuid, kind, created_by_principal_ref, updated_by_principal_ref)
		 VALUES ('00000000-1111-4000-8000-0000000000b2', ?, 'second project',
		     (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		multiProjB, seedActor, seedActor); err != nil {
		t.Fatalf("seed second project: %v", err)
	}
	api, opts, err := bootstrap.Server(database, &config.Config{DBPath: dbPath, AttachmentsMaxMB: 50})
	if err != nil {
		t.Fatal(err)
	}
	server := workrpc.NewServer(nil)
	workrpc.RegisterAPI(server, api, opts)
	f := &wrkpMultiFixture{}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req workrpc.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		var params struct {
			Container string `json:"container"`
		}
		_ = json.Unmarshal(req.Params, &params)
		f.mu.Lock()
		call := wrkpCall{method: req.Method, container: params.Container}
		if req.Method == "wrkq.commit.wait" {
			f.waiting++
			call.inFlight = f.waiting
		}
		f.calls = append(f.calls, call)
		f.mu.Unlock()
		resp, _ := server.HandleRequest(r.Context(), req)
		if req.Method == "wrkq.commit.wait" {
			f.mu.Lock()
			f.waiting--
			f.mu.Unlock()
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(httpServer.Close)
	t.Setenv("WRKQD_TOKEN_FILE", "")
	t.Setenv("WRKQD_TOKEN", "test-token")
	f.endpoint = "rpc://" + strings.TrimPrefix(httpServer.URL, "http://")
	return f
}

func (f *wrkpMultiFixture) wrkp(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := NewWrkpRootCmd()
	cmd.SetArgs(append([]string{"--db", f.endpoint, "--principal-ref", "agent:wrkp-test"}, args...))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err = cmd.ExecuteContext(t.Context())
	return out.String(), errOut.String(), err
}

func (f *wrkpMultiFixture) mustWrkp(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, err := f.wrkp(t, args...)
	if err != nil {
		t.Fatalf("wrkp %s: %v\n%s", strings.Join(args, " "), err, errOut)
	}
	return out
}

func (f *wrkpMultiFixture) post(t *testing.T, project, summary string) {
	t.Helper()
	f.mustWrkp(t, "post", project, "--type", "test.multi", "--attr", "k=v", "-m", summary)
}

func (f *wrkpMultiFixture) resetCalls() {
	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()
}

func (f *wrkpMultiFixture) observed() []wrkpCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

type multiLine struct {
	Type    string            `json:"type"`
	Project string            `json:"project"`
	Summary string            `json:"summary"`
	Cursor  string            `json:"cursor"`
	Cursors map[string]string `json:"cursors"`
	PE      *struct {
		Summary string `json:"summary"`
	} `json:"projectEvent"`
}

// parseMultiFollow splits a follow's stdout into project→summaries and the
// terminal record.
func parseMultiFollow(t *testing.T, stdout string) (map[string][]string, multiLine) {
	t.Helper()
	got := map[string][]string{}
	var terminal multiLine
	terminals := 0
	for _, raw := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if raw == "" {
			continue
		}
		var line multiLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("bad ndjson line %q: %v", raw, err)
		}
		if line.Type == "wrkp.log.terminal" {
			terminal = line
			terminals++
			continue
		}
		if line.Project == "" {
			t.Fatalf("multi-project entry without a project key: %s", raw)
		}
		summary := ""
		if line.PE != nil {
			summary = line.PE.Summary
		}
		got[line.Project] = append(got[line.Project], summary)
	}
	if terminals != 1 {
		t.Fatalf("want exactly one terminal record, got %d:\n%s", terminals, stdout)
	}
	return got, terminal
}

// cursorAfters turns `wrkp cursor a b` output into --after flags.
func cursorAfters(t *testing.T, out string, projects ...string) []string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != len(projects) {
		t.Fatalf("wrkp cursor printed %d lines for %d projects: %q", len(lines), len(projects), out)
	}
	flags := []string{}
	for i, line := range lines {
		name, cursor, ok := strings.Cut(line, "=")
		if !ok || name != projects[i] || cursor == "" {
			t.Fatalf("wrkp cursor line %d = %q, want %s=<cursor> in argument order", i, line, projects[i])
		}
		flags = append(flags, "--after", line)
	}
	return flags
}

func TestWrkpMultiFollowTagsEachProjectAndResumesPerProject(t *testing.T) {
	f := newWrkpMultiFixture(t)
	a, b := "rpccli-test-proj", multiProjB

	afters := cursorAfters(t, f.mustWrkp(t, "cursor", a, b), a, b)
	f.post(t, a, "a-one")
	f.post(t, b, "b-one")

	f.resetCalls()
	out := f.mustWrkp(t, append([]string{"log", "--follow", a, b, "--ndjson", "--type", "test.multi", "--timeout", "1s"}, afters...)...)
	got, terminal := parseMultiFollow(t, out)
	if !slices.Equal(got[a], []string{"a-one"}) || !slices.Equal(got[b], []string{"b-one"}) || len(got) != 2 {
		t.Fatalf("one process, both projects, each tagged: got %v\n%s", got, out)
	}
	if terminal.Cursor != "" || len(terminal.Cursors) != 2 || terminal.Cursors[a] == "" || terminal.Cursors[b] == "" {
		t.Fatalf("terminal must carry cursors for every project in place of cursor: %+v", terminal)
	}

	assertOneCommitWait(t, f.observed(), a, b, 1)

	// Wakes after the opening drain: the forced-read bound (the existing test
	// hook) makes every park return, so each wake must read both projects.
	previous := wrkpFollowForcedReadInterval
	wrkpFollowForcedReadInterval = 200 * time.Millisecond
	f.resetCalls()
	f.mustWrkp(t, append([]string{"log", "--follow", a, b, "--ndjson", "--type", "test.multi", "--timeout", "1s"}, afters...)...)
	wrkpFollowForcedReadInterval = previous
	assertOneCommitWait(t, f.observed(), a, b, 2)

	// Down: post one more in each, then resume from the terminal record.
	f.post(t, a, "a-two")
	f.post(t, b, "b-two")
	resume := []string{"log", "--follow", a, b, "--ndjson", "--type", "test.multi", "--timeout", "1s"}
	for _, p := range []string{a, b} {
		resume = append(resume, "--after", p+"="+terminal.Cursors[p])
	}
	got, terminal2 := parseMultiFollow(t, f.mustWrkp(t, resume...))
	if !slices.Equal(got[a], []string{"a-two"}) || !slices.Equal(got[b], []string{"b-two"}) || len(got) != 2 {
		t.Fatalf("resume from terminal cursors must deliver exactly the down-time entries once: %v", got)
	}

	// The same from `wrkp cursor` values taken at the resume point.
	afters = cursorAfters(t, f.mustWrkp(t, "cursor", a, b), a, b)
	f.post(t, a, "a-three")
	f.post(t, b, "b-three")
	got, _ = parseMultiFollow(t, f.mustWrkp(t, append([]string{"log", "--follow", a, b, "--ndjson", "--type", "test.multi", "--timeout", "1s"}, afters...)...))
	if !slices.Equal(got[a], []string{"a-three"}) || !slices.Equal(got[b], []string{"b-three"}) || len(got) != 2 {
		t.Fatalf("resume from wrkp cursor values: %v", got)
	}

	// A project with no --after starts at its head: only b has a cursor, so
	// a's backlog is not replayed and a delivers nothing.
	got, _ = parseMultiFollow(t, f.mustWrkp(t, "log", "--follow", a, b, "--ndjson", "--type", "test.multi", "--timeout", "1s",
		"--after", b+"="+terminal2.Cursors[b]))
	if len(got[a]) != 0 || !slices.Equal(got[b], []string{"b-three"}) {
		t.Fatalf("a project without --after starts at its head: %v", got)
	}
}

// assertOneCommitWait: one commit wait for the whole process. Never two in
// flight, none read before the first, and every wait that is followed by
// reads is followed by reads of BOTH projects (a wait-per-project loop reads
// one per wait). minRounds counts read rounds, the opening drain included.
func assertOneCommitWait(t *testing.T, calls []wrkpCall, a, b string, minRounds int) {
	t.Helper()
	if first := slices.IndexFunc(calls, func(c wrkpCall) bool { return c.method != "rpc.initialize" }); first < 0 || calls[first].method != "wrkq.commit.wait" {
		t.Fatalf("token must be taken before the first read: %+v", calls)
	}
	var round map[string]bool
	waits, rounds := 0, 0
	checkRound := func() {
		if len(round) > 0 && (!round[a] || !round[b]) {
			t.Fatalf("a wake read only %v, want both projects: %+v", round, calls)
		}
	}
	for _, call := range calls {
		switch call.method {
		case "wrkq.commit.wait":
			if call.inFlight != 1 {
				t.Fatalf("%d commit waits in flight; one process must hold one: %+v", call.inFlight, calls)
			}
			checkRound()
			if len(round) > 0 {
				rounds++
			}
			round = map[string]bool{}
			waits++
		case "wrkq.container.timelineView":
			round[call.container] = true
		}
	}
	checkRound()
	if len(round) > 0 {
		rounds++
	}
	if waits < 2 || rounds < minRounds {
		t.Fatalf("follow parked %d times over %d read rounds, want >= 2 and >= %d: %+v", waits, rounds, minRounds, calls)
	}
}

func TestWrkpMultiProjectUsageErrors(t *testing.T) {
	f := newWrkpMultiFixture(t)
	a, b := "rpccli-test-proj", multiProjB
	cursor := strings.TrimSpace(f.mustWrkp(t, "cursor", a))
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no-follow", []string{"log", a, b, "--ndjson"}, "--follow"},
		{"no-ndjson", []string{"log", a, b, "--follow"}, "--ndjson"},
		{"json", []string{"log", a, b, "--follow", "--ndjson", "--json"}, "--ndjson"},
		{"task", []string{"log", a, b, "--follow", "--ndjson", "--task", "T-00001"}, "--task"},
		{"before", []string{"log", a, b, "--follow", "--ndjson", "--before", "2030-01-01T00:00:00Z"}, "--before"},
		{"limit", []string{"log", a, b, "--follow", "--ndjson", "--limit", "5"}, "--limit"},
		{"since", []string{"log", a, b, "--follow", "--ndjson", "--since", "1h"}, "--since"},
		{"bare-after", []string{"log", a, b, "--follow", "--ndjson", "--after", cursor}, "<project>=<cursor>"},
		{"unknown-after", []string{"log", a, b, "--follow", "--ndjson", "--after", "nope=" + cursor}, "nope"},
		{"repeated-after", []string{"log", a, b, "--follow", "--ndjson", "--after", a + "=" + cursor, "--after", a + "=" + cursor}, a},
		{"duplicate-project", []string{"log", a, a, "--follow", "--ndjson"}, a},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, _, err := f.wrkp(t, tc.args...)
			var usage *clifunnel.UsageError
			if !errors.As(err, &usage) {
				t.Fatalf("want a usage error (exit 2), got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("usage error must say %q: %v", tc.want, err)
			}
			if stdout != "" {
				t.Fatalf("a usage error writes no entries: %q", stdout)
			}
		})
	}
}

// A project the server refuses fails the whole command, names the project, and
// keeps the server's typed error -- before any entry of the good project.
func TestWrkpMultiFollowRefusedProjectFailsBeforeAnyEntry(t *testing.T) {
	f := newWrkpMultiFixture(t)
	a := "rpccli-test-proj"
	afters := []string{"--after", a + "=" + strings.TrimSpace(f.mustWrkp(t, "cursor", a))}
	f.post(t, a, "a-before-refusal")
	stdout, _, err := f.wrkp(t, append([]string{"log", "--follow", a, "no-such-project", "--ndjson", "--timeout", "1s"}, afters...)...)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || !strings.Contains(err.Error(), "no-such-project") {
		t.Fatalf("want the server's typed error naming the project, got %T %v", err, err)
	}
	if stdout != "" {
		t.Fatalf("no entry may be written before every project resolves: %q", stdout)
	}
	if _, _, err := f.wrkp(t, "cursor", a, "no-such-project"); err == nil || !strings.Contains(err.Error(), "no-such-project") {
		t.Fatalf("wrkp cursor must fail naming the refused project: %v", err)
	}
}

// Single-project output is unchanged: no project key on entries, a single
// cursor on the terminal record, and `wrkp cursor` prints the bare cursor.
func TestWrkpSingleProjectOutputUnchanged(t *testing.T) {
	f := newWrkpMultiFixture(t)
	a := "rpccli-test-proj"
	cursor := strings.TrimSpace(f.mustWrkp(t, "cursor", a))
	if strings.Contains(cursor, "=") || strings.Contains(cursor, "\n") || cursor == "" {
		t.Fatalf("single-project wrkp cursor prints one bare cursor: %q", cursor)
	}
	f.post(t, a, "single")
	out := f.mustWrkp(t, "log", a, "--follow", "--ndjson", "--type", "test.multi", "--timeout", "1s", "--after", cursor)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("want one entry and the terminal: %q", out)
	}
	var entry, terminal map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &terminal); err != nil {
		t.Fatal(err)
	}
	if _, ok := entry["project"]; ok {
		t.Fatalf("single-project entries carry no project key: %s", lines[0])
	}
	if terminal["type"] != "wrkp.log.terminal" || terminal["cursor"] == nil || terminal["cursors"] != nil {
		t.Fatalf("single-project terminal keeps its one cursor: %s", lines[1])
	}
	// A bounded forward read from the same cursor renders the same entry line.
	bounded := f.mustWrkp(t, "log", a, "--ndjson", "--type", "test.multi", "--after", cursor)
	if strings.TrimSpace(bounded) != lines[0] {
		t.Fatalf("follow and bounded read disagree on the entry line:\n%s\n%s", bounded, lines[0])
	}
}
