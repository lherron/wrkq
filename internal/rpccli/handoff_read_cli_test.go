//go:build wrkq_local

package rpccli

// handoff_read_cli_test.go — characterization of the handoff read and
// acknowledge verbs through the in-process wrkq CLI: list and search in their
// human, JSON and NDJSON modes (empty pages, the "more available" footer, the
// pagination footer and the stderr cursor echo), their shared validation
// errors, and acknowledge's success and already-acknowledged exit code.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

const handoffReadScope = "cody@rpccli-test-proj"

func newHandoffReadFixture(t *testing.T) string {
	t.Helper()
	dbPath, _ := migratedDBWithTask(t)
	t.Setenv("ASP_AGENT_ID", "cody")
	t.Setenv("WRKQD_TOKEN_FILE", "")
	return dbPath
}

func createHandoffForRead(t *testing.T, dbPath, title string) handoffJSON {
	t.Helper()
	return decodeCreatedHandoff(t, runHandoffCreateCLI(t, dbPath, "",
		"handoff", "create", "--scope", handoffReadScope, "--title", title, "--body", title+" body", "--json"))
}

func TestHandoffListAndSearchEmptyPages(t *testing.T) {
	dbPath := newHandoffReadFixture(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"list pending", []string{"handoff", "list", "--scope", handoffReadScope, "--human"},
			"No pending handoffs for agent:cody:project:rpccli-test-proj.\n"},
		{"list all", []string{"handoff", "list", "--scope", handoffReadScope, "--status", "all", "--human"},
			"No all handoffs for agent:cody:project:rpccli-test-proj.\n"},
		{"list acknowledged", []string{"handoff", "list", "--scope", handoffReadScope, "--status", "acknowledged", "--human"},
			"No acknowledged handoffs for agent:cody:project:rpccli-test-proj.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runHandoffCreateCLI(t, dbPath, "", tc.args...)
			if result.err != nil {
				t.Fatalf("%v\nstderr:\n%s", result.err, result.stderr)
			}
			if result.stdout != tc.want {
				t.Fatalf("stdout = %q, want %q", result.stdout, tc.want)
			}
		})
	}
}

func TestHandoffListPagesInEveryMode(t *testing.T) {
	dbPath := newHandoffReadFixture(t)
	first := createHandoffForRead(t, dbPath, "first handoff")
	second := createHandoffForRead(t, dbPath, "second handoff")

	t.Run("human footer names the cursor", func(t *testing.T) {
		result := runHandoffCreateCLI(t, dbPath, "", "handoff", "list", "--scope", handoffReadScope, "--limit", "1", "--human")
		if result.err != nil {
			t.Fatalf("%v\nstderr:\n%s", result.err, result.stderr)
		}
		lines := strings.Split(strings.TrimRight(result.stdout, "\n"), "\n")
		if len(lines) != 3 || !strings.HasPrefix(lines[0], "ID") || !strings.Contains(lines[0], "Scope") {
			t.Fatalf("human list = %q, want header, one row, footer", result.stdout)
		}
		if !strings.HasPrefix(lines[2], "(1 shown, more available — use --cursor ") {
			t.Fatalf("footer = %q", lines[2])
		}
		if result.stderr != "" {
			t.Fatalf("human list stderr = %q, want none", result.stderr)
		}
	})

	t.Run("ndjson rows, pagination footer and stderr cursor", func(t *testing.T) {
		result := runHandoffCreateCLI(t, dbPath, "", "handoff", "list", "--scope", handoffReadScope, "--limit", "1", "--ndjson")
		if result.err != nil {
			t.Fatalf("%v\nstderr:\n%s", result.err, result.stderr)
		}
		lines := strings.Split(strings.TrimRight(result.stdout, "\n"), "\n")
		if len(lines) != 2 {
			t.Fatalf("ndjson list = %q, want one row and a footer", result.stdout)
		}
		var footer map[string]any
		if err := json.Unmarshal([]byte(lines[1]), &footer); err != nil {
			t.Fatalf("decode footer: %v", err)
		}
		cursor, _ := footer["next_cursor"].(string)
		if footer["type"] != "wrkq.pagination" || footer["truncated"] != true || cursor == "" {
			t.Fatalf("footer = %#v", footer)
		}
		if result.stderr != "next_cursor="+cursor+"\n" {
			t.Fatalf("stderr = %q, want the cursor echo", result.stderr)
		}
	})

	t.Run("json page carries both handoffs", func(t *testing.T) {
		result := runHandoffCreateCLI(t, dbPath, "", "handoff", "list", "--scope", handoffReadScope, "--json")
		if result.err != nil {
			t.Fatalf("%v\nstderr:\n%s", result.err, result.stderr)
		}
		var page handoffListOutput
		if err := json.Unmarshal([]byte(result.stdout), &page); err != nil {
			t.Fatalf("decode json page: %v\n%s", err, result.stdout)
		}
		ids := map[string]bool{}
		for _, h := range page.Handoffs {
			ids[h.ID] = true
		}
		if len(page.Handoffs) != 2 || !ids[first.ID] || !ids[second.ID] || page.NextCursor != nil || page.Truncated {
			t.Fatalf("json page = %#v", page)
		}
		if !strings.HasPrefix(result.stdout, "{\n  \"handoffs\": [") {
			t.Fatalf("json page is not two-space indented: %q", result.stdout)
		}
	})
}

func TestHandoffListAndSearchValidationErrors(t *testing.T) {
	dbPath := newHandoffReadFixture(t)
	for _, tc := range []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{"list bad status", []string{"handoff", "list", "--scope", handoffReadScope, "--status", "bogus", "--json"}, 1,
			"{\n  \"error\": {\n    \"code\": \"invalid_status\",\n    \"message\": \"invalid --status \\\"bogus\\\": must be pending, acknowledged, or all\",\n    \"example\": \"wrkq handoff list --status pending|acknowledged|all\"\n  }\n}\n"},
		{"list negative limit", []string{"handoff", "list", "--scope", handoffReadScope, "--limit", "-1", "--ndjson"}, 1,
			"{\"error\":{\"code\":\"validation_error\",\"message\":\"--limit cannot be negative\"}}\n"},
		{"search bad status", []string{"handoff", "search", "quartz", "--scope", handoffReadScope, "--status", "bogus", "--human"}, 1,
			"Error: invalid --status \"bogus\": must be pending, acknowledged, or all\nExample: wrkq handoff search quartz --status pending|acknowledged|all\n"},
		{"search missing query", []string{"handoff", "search", "--scope", handoffReadScope, "--human"}, 1,
			"Error: query is required\nExample: wrkq handoff search quartz --scope cody@wrkq --status all\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runHandoffCreateCLI(t, dbPath, "", tc.args...)
			if result.err == nil {
				t.Fatalf("expected failure; stdout=%q", result.stdout)
			}
			if code := ExitCodeForError(result.err); code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d", code, tc.wantCode)
			}
			if result.stderr != tc.wantErr {
				t.Fatalf("stderr = %q, want %q", result.stderr, tc.wantErr)
			}
		})
	}
}

func TestHandoffSearchFindsByTitle(t *testing.T) {
	dbPath := newHandoffReadFixture(t)
	created := createHandoffForRead(t, dbPath, "quartz handoff")
	result := runHandoffCreateCLI(t, dbPath, "", "handoff", "search", "quartz", "--scope", handoffReadScope, "--json")
	if result.err != nil {
		// Search may be disabled in this build; then the failure must be the
		// typed search_unavailable error, not a generic one.
		if !strings.Contains(result.stderr, "\"code\": \"search_unavailable\"") {
			t.Fatalf("search failed: %v\nstderr:\n%s", result.err, result.stderr)
		}
		return
	}
	var page handoffListOutput
	if err := json.Unmarshal([]byte(result.stdout), &page); err != nil {
		t.Fatalf("decode search page: %v\n%s", err, result.stdout)
	}
	if len(page.Handoffs) != 1 || page.Handoffs[0].ID != created.ID {
		t.Fatalf("search page = %#v, want %s", page, created.ID)
	}
	createHandoffForRead(t, dbPath, "quartz again")
	paged := runHandoffCreateCLI(t, dbPath, "", "handoff", "search", "quartz", "--scope", handoffReadScope, "--limit", "1", "--human")
	if paged.err != nil {
		t.Fatalf("paged search: %v\nstderr:\n%s", paged.err, paged.stderr)
	}
	if lines := strings.Split(strings.TrimRight(paged.stdout, "\n"), "\n"); len(lines) != 3 ||
		!strings.HasPrefix(lines[2], "(1 shown, more available - use --cursor ") {
		t.Fatalf("paged human search = %q, want header, one row, footer", paged.stdout)
	}

	empty := runHandoffCreateCLI(t, dbPath, "", "handoff", "search", "quartz", "--scope", "cody@rpccli-empty-proj", "--human")
	if empty.err != nil {
		t.Fatalf("empty search: %v\nstderr:\n%s", empty.err, empty.stderr)
	}
	if want := "No handoffs match \"quartz\" in agent:cody:project:rpccli-empty-proj.\n"; empty.stdout != want {
		t.Fatalf("empty search stdout = %q, want %q", empty.stdout, want)
	}
}

func TestHandoffAcknowledgeThenAlreadyAcknowledged(t *testing.T) {
	dbPath := newHandoffReadFixture(t)
	created := createHandoffForRead(t, dbPath, "ack me")

	ack := runHandoffCreateCLI(t, dbPath, "", "--as", "agent:cody", "handoff", "acknowledge", created.ID, "--note", "consumed", "--json")
	if ack.err != nil {
		t.Fatalf("acknowledge: %v\nstderr:\n%s", ack.err, ack.stderr)
	}
	var out handoffAckOutput
	if err := json.Unmarshal([]byte(ack.stdout), &out); err != nil {
		t.Fatalf("decode ack: %v\n%s", err, ack.stdout)
	}
	if out.Handoff.Status != "acknowledged" || out.Handoff.AcknowledgementNote == nil || *out.Handoff.AcknowledgementNote != "consumed" {
		t.Fatalf("ack output = %#v", out)
	}

	again := runHandoffCreateCLI(t, dbPath, "", "--as", "agent:cody", "handoff", "acknowledge", created.ID, "--json")
	if again.err == nil {
		t.Fatalf("second acknowledge succeeded: %s", again.stdout)
	}
	if code := ExitCodeForError(again.err); code != 5 {
		t.Fatalf("second acknowledge exit = %d, want 5\nstderr:\n%s", code, again.stderr)
	}
	if !strings.Contains(again.stderr, "\"code\": \"already_acknowledged\"") || !strings.Contains(again.stderr, "acknowledged_at=") {
		t.Fatalf("second acknowledge stderr = %q", again.stderr)
	}
}

// A seat swap: cody writes a handoff addressed to clod with --to. The row is
// filed in clod's scope with cody as author, clod finds it in its own pending
// listing, and clod acknowledges it as itself.
func TestHandoffCreateToSuccessorIsConsumedBySuccessor(t *testing.T) {
	dbPath := newHandoffReadFixture(t)
	for _, key := range []string{"AGENT_SCOPE_REF", "ASP_SCOPE_REF", "ASP_HANDLE", "ASP_PROJECT"} {
		t.Setenv(key, "")
	}

	created := decodeCreatedHandoff(t, runHandoffCreateCLI(t, dbPath, "",
		"handoff", "create", "--to", "clod@rpccli-test-proj:T-00099", "--title", "seat swap", "--body", "carry on", "--json"))
	if created.ScopeRef != "agent:clod:project:rpccli-test-proj" || created.AgentID != "clod" || created.CreatedByAgentID != "cody" {
		t.Fatalf("created = scope %q agent %q author %q", created.ScopeRef, created.AgentID, created.CreatedByAgentID)
	}

	both := runHandoffCreateCLI(t, dbPath, "",
		"handoff", "create", "--to", "clod@rpccli-test-proj", "--scope", handoffReadScope, "--title", "x", "--body", "y", "--json")
	if both.err == nil || ExitCodeForError(both.err) != 2 || !strings.Contains(both.stderr, "--to and --scope") {
		t.Fatalf("--to with --scope: err=%v stderr=%s", both.err, both.stderr)
	}

	t.Setenv("ASP_AGENT_ID", "clod")
	list := runHandoffCreateCLI(t, dbPath, "", "handoff", "list", "--scope", "clod@rpccli-test-proj", "--human")
	if list.err != nil {
		t.Fatalf("list: %v\nstderr:\n%s", list.err, list.stderr)
	}
	if !strings.Contains(list.stdout, "From") || !strings.Contains(list.stdout, created.ID) || !strings.Contains(list.stdout, "cody") {
		t.Fatalf("successor listing lacks the handoff or its author:\n%s", list.stdout)
	}

	get := runHandoffCreateCLI(t, dbPath, "", "handoff", "get", created.ID, "--human")
	if get.err != nil || !strings.Contains(get.stdout, "From: cody\n") {
		t.Fatalf("get: %v\n%s", get.err, get.stdout)
	}

	ack := runHandoffCreateCLI(t, dbPath, "", "handoff", "acknowledge", created.ID, "--note", "consumed by clod", "--json")
	if ack.err != nil {
		t.Fatalf("successor acknowledge: %v\nstderr:\n%s", ack.err, ack.stderr)
	}
	var out handoffAckOutput
	if err := json.Unmarshal([]byte(ack.stdout), &out); err != nil {
		t.Fatalf("decode ack: %v\n%s", err, ack.stdout)
	}
	if out.Handoff.AcknowledgedByAgentID == nil || *out.Handoff.AcknowledgedByAgentID != "clod" {
		t.Fatalf("acknowledged by = %v, want clod", out.Handoff.AcknowledgedByAgentID)
	}
}

// Without --to the self-scope guard still refuses a handoff filed under
// another agent, and a same-author listing keeps its original columns.
func TestHandoffCreateScopeStillRefusesAnotherAgent(t *testing.T) {
	dbPath := newHandoffReadFixture(t)
	remote, requests := newCountingHandoffRemote(t, dbPath)
	refused := runHandoffCreateCLI(t, remote, "",
		"handoff", "create", "--scope", "clod@rpccli-test-proj", "--title", "x", "--body", "y", "--json")
	if refused.err == nil || !strings.Contains(refused.stderr, "self_scope_violation") {
		t.Fatalf("cross-agent --scope: err=%v stderr=%s", refused.err, refused.stderr)
	}
	// The self-scope gate is caller-side: the refusal never reaches the server.
	if got := requests.Load(); got != 0 {
		t.Fatalf("cross-agent --scope refusal sent %d RPC requests, want 0", got)
	}
	createHandoffForRead(t, remote, "own")
	if requests.Load() == 0 {
		t.Fatal("request counter never moved: the zero-request check above proves nothing")
	}
	list := runHandoffCreateCLI(t, dbPath, "", "handoff", "list", "--scope", handoffReadScope, "--human")
	if list.err != nil || strings.Contains(list.stdout, "From") {
		t.Fatalf("self-authored listing: %v\n%s", list.err, list.stdout)
	}
}

// newCountingHandoffRemote serves the real RPC registry over HTTP and counts
// every request, so a test can prove a refusal happened before the CLI opened
// the transport.
func newCountingHandoffRemote(t *testing.T, dbPath string) (string, *atomic.Int64) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	api, opts, err := bootstrap.Server(database, &config.Config{DBPath: dbPath, AttachmentsMaxMB: 50})
	if err != nil {
		t.Fatalf("bootstrap.Server: %v", err)
	}
	rpcServer := workrpc.NewServer(nil)
	workrpc.RegisterAPI(rpcServer, api, opts)
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var req workrpc.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp, _ := rpcServer.HandleRequest(r.Context(), req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(server.Close)
	t.Setenv("WRKQD_TOKEN", "")
	return "rpc://" + strings.TrimPrefix(server.URL, "http://"), &requests
}

func clearHandoffScopeEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"AGENT_SCOPE_REF", "ASP_SCOPE_REF", "ASP_HANDLE", "ASP_AGENT_ID", "ASP_PROJECT"} {
		t.Setenv(key, "")
	}
}

func getHandoff(t *testing.T, locator, id string) handoffJSON {
	t.Helper()
	result := runHandoffCreateCLI(t, locator, "", "handoff", "get", id, "--json")
	if result.err != nil {
		t.Fatalf("handoff get %s: %v\nstderr:\n%s", id, result.err, result.stderr)
	}
	var h handoffJSON
	if err := json.Unmarshal([]byte(result.stdout), &h); err != nil {
		t.Fatalf("decode handoff get: %v\n%s", err, result.stdout)
	}
	return h
}

func TestHandoffCreateDryRunWritesNothingIdempotencyKeyDedupesAndMetaRoundTrips(t *testing.T) {
	dbPath := newHandoffReadFixture(t)
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	dry := runHandoffCreateCLI(t, dbPath, "",
		"handoff", "create", "--scope", handoffReadScope, "--title", "dry", "--body", "b", "--dry-run", "--json")
	var dryOut handoffCreateOutput
	if dry.err != nil || json.Unmarshal([]byte(dry.stdout), &dryOut) != nil || !dryOut.DryRun || dryOut.Handoff.Title != "dry" {
		t.Fatalf("dry-run create: err=%v stdout=%s stderr=%s", dry.err, dry.stdout, dry.stderr)
	}
	if got := handoffRowCount(t, database); got != 0 {
		t.Fatalf("--dry-run wrote %d handoff rows, want 0", got)
	}

	create := func() handoffCreateOutput {
		t.Helper()
		result := runHandoffCreateCLI(t, dbPath, "",
			"handoff", "create", "--scope", handoffReadScope, "--title", "keyed", "--body", "b",
			"--idempotency-key", "k-1", "--meta", `{"k":"v"}`, "--json")
		var out handoffCreateOutput
		if result.err != nil || json.Unmarshal([]byte(result.stdout), &out) != nil {
			t.Fatalf("keyed create: err=%v stdout=%s stderr=%s", result.err, result.stdout, result.stderr)
		}
		return out
	}
	first, replay := create(), create()
	if first.IdempotentReplay || !replay.IdempotentReplay || replay.Handoff.ID != first.Handoff.ID {
		t.Fatalf("idempotency key did not dedupe: first=%s replay=%s replayed=%v", first.Handoff.ID, replay.Handoff.ID, replay.IdempotentReplay)
	}
	if got := handoffRowCount(t, database); got != 1 {
		t.Fatalf("same --idempotency-key twice wrote %d rows, want 1", got)
	}
	readBack := getHandoff(t, dbPath, first.Handoff.ID)
	if readBack.Meta == nil || *readBack.Meta != `{"k":"v"}` {
		t.Fatalf("--meta read back as %v, want {\"k\":\"v\"}", readBack.Meta)
	}
	if readBack.IdempotencyKey == nil || *readBack.IdempotencyKey != "k-1" {
		t.Fatalf("--idempotency-key read back as %v, want k-1", readBack.IdempotencyKey)
	}
}

func TestHandoffAcknowledgeStaleIfMatchAndDryRunLeaveItPending(t *testing.T) {
	dbPath := newHandoffReadFixture(t)
	created := createHandoffForRead(t, dbPath, "guarded")

	stale := runHandoffCreateCLI(t, dbPath, "", "--as", "agent:cody",
		"handoff", "acknowledge", created.ID, "--if-match", "999", "--json")
	if stale.err == nil || !strings.Contains(stale.stderr, "etag") {
		t.Fatalf("stale --if-match: err=%v stdout=%s stderr=%s", stale.err, stale.stdout, stale.stderr)
	}
	if got := getHandoff(t, dbPath, created.ID); got.Status != "pending" {
		t.Fatalf("after stale --if-match status = %q, want pending", got.Status)
	}

	dry := runHandoffCreateCLI(t, dbPath, "", "--as", "agent:cody",
		"handoff", "acknowledge", created.ID, "--dry-run", "--json")
	var dryOut handoffAckOutput
	if dry.err != nil || json.Unmarshal([]byte(dry.stdout), &dryOut) != nil || !dryOut.DryRun {
		t.Fatalf("dry-run acknowledge: err=%v stdout=%s stderr=%s", dry.err, dry.stdout, dry.stderr)
	}
	if got := getHandoff(t, dbPath, created.ID); got.Status != "pending" {
		t.Fatalf("after --dry-run status = %q, want pending", got.Status)
	}

	current := runHandoffCreateCLI(t, dbPath, "", "--as", "agent:cody",
		"handoff", "acknowledge", created.ID, "--if-match", strconv.FormatInt(created.ETag, 10), "--json")
	if current.err != nil {
		t.Fatalf("current --if-match: %v\nstderr:\n%s", current.err, current.stderr)
	}
	if got := getHandoff(t, dbPath, created.ID); got.Status != "acknowledged" {
		t.Fatalf("after current --if-match status = %q, want acknowledged", got.Status)
	}
}

// With no runtime identity in the environment, `handoff acknowledge` takes the
// actor from the handoff row itself; an explicit --as must name the same agent
// and be a canonical agent: principal.
func TestHandoffAcknowledgeIdentityComesFromRowUnlessAsConflicts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		as      []string
		wantErr []string
	}{
		{name: "no --as acknowledges as the row agent"},
		{name: "--as the row agent acknowledges in the row project", as: []string{"--as", "agent:curly"}},
		{name: "--as another agent is ambiguous", as: []string{"--as", "agent:cody"}, wantErr: []string{"ambiguous actor", "cody", "curly"}},
		{name: "bare slug --as is refused", as: []string{"--as", "curly"}, wantErr: []string{"invalid --as"}},
		{name: "non-agent --as is refused", as: []string{"--as", "system:scheduler"}, wantErr: []string{"invalid --as"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := newHandoffReadFixture(t)
			clearHandoffScopeEnv(t)
			t.Setenv("ASP_AGENT_ID", "curly")
			created := decodeCreatedHandoff(t, runHandoffCreateCLI(t, dbPath, "",
				"handoff", "create", "--scope", "curly@rpccli-test-proj", "--title", "for curly", "--body", "b", "--json"))
			t.Setenv("ASP_AGENT_ID", "")

			args := append(append([]string{}, tc.as...), "handoff", "acknowledge", created.ID, "--json")
			ack := runHandoffCreateCLI(t, dbPath, "", args...)
			got := getHandoff(t, dbPath, created.ID)
			if len(tc.wantErr) > 0 {
				if ack.err == nil {
					t.Fatalf("acknowledge succeeded: %s", ack.stdout)
				}
				for _, want := range tc.wantErr {
					if !strings.Contains(ack.err.Error()+ack.stderr, want) {
						t.Fatalf("refusal missing %q: err=%v stderr=%s", want, ack.err, ack.stderr)
					}
				}
				if got.Status != "pending" {
					t.Fatalf("refused acknowledge left status %q, want pending", got.Status)
				}
				return
			}
			if ack.err != nil {
				t.Fatalf("acknowledge: %v\nstderr:\n%s", ack.err, ack.stderr)
			}
			if got.Status != "acknowledged" || got.AcknowledgedByAgentID == nil || *got.AcknowledgedByAgentID != "curly" ||
				got.AcknowledgedByPrincipalRef == nil || *got.AcknowledgedByPrincipalRef != "agent:curly" {
				t.Fatalf("acknowledged = status %q by %v / %v, want curly / agent:curly", got.Status, got.AcknowledgedByAgentID, got.AcknowledgedByPrincipalRef)
			}
		})
	}
}

func TestHandoffCreateWithoutScopeFilesUnderTheEnvScopeRef(t *testing.T) {
	dbPath := newHandoffReadFixture(t)
	clearHandoffScopeEnv(t)
	t.Setenv("ASP_SCOPE_REF", "agent:cody:project:rpccli-test-proj")

	created := decodeCreatedHandoff(t, runHandoffCreateCLI(t, dbPath, "",
		"handoff", "create", "--title", "env scoped", "--body", "b", "--json"))
	if created.ScopeRef != "agent:cody:project:rpccli-test-proj" || created.AgentID != "cody" || created.ProjectID != "rpccli-test-proj" {
		t.Fatalf("env-scoped create = scope %q agent %q project %q", created.ScopeRef, created.AgentID, created.ProjectID)
	}
}
