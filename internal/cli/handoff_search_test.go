package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/store"
)

type handoffSearchExecResult struct {
	stdout string
	stderr string
	code   int
	err    error
}

func runHandoffSearchCLI(t *testing.T, args []string) handoffSearchExecResult {
	t.Helper()

	var stdout, stderr bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetIn(strings.NewReader(""))

	err := rootCmd.Execute()
	result := handoffSearchExecResult{
		stdout: stdout.String(),
		stderr: stderr.String(),
		code:   ExitCodeForError(err),
		err:    err,
	}

	rootCmd.SetArgs(nil)
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	rootCmd.SetIn(os.Stdin)
	_ = rootCmd.PersistentFlags().Set("db", "")
	_ = rootCmd.PersistentFlags().Set("as", "")
	_ = rootCmd.PersistentFlags().Set("project", "")
	return result
}

func decodeHandoffSearchOutput(t *testing.T, raw string) handoffSearchOutput {
	t.Helper()
	var out handoffSearchOutput
	if err := json.Unmarshal([]byte(raw), &out); err == nil {
		return out
	}

	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var probe map[string]interface{}
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			t.Fatalf("failed to decode search NDJSON line: %v\n%s", err, raw)
		}
		if probe["type"] == "wrkq.pagination" {
			if cursor, ok := probe["next_cursor"].(string); ok && cursor != "" {
				out.NextCursor = &cursor
			}
			if truncated, ok := probe["truncated"].(bool); ok {
				out.Truncated = truncated
			}
			continue
		}
		var h handoffJSON
		if err := json.Unmarshal([]byte(line), &h); err != nil {
			t.Fatalf("failed to decode handoff search NDJSON row: %v\n%s", err, raw)
		}
		out.Handoffs = append(out.Handoffs, h)
	}
	return out
}

func decodeHandoffSearchError(t *testing.T, raw string) handoffErrorOutput {
	t.Helper()
	var out handoffErrorOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("failed to decode JSON error: %v\n%s", err, raw)
	}
	return out
}

func setSearchScopeEnv(t *testing.T, databasePath, agentID, projectID string) {
	t.Helper()
	t.Setenv("WRKQ_DB_PATH", databasePath)
	t.Setenv("ASP_SCOPE_REF", fmt.Sprintf("agent:%s:project:%s", agentID, projectID))
	t.Setenv("ASP_AGENT_ID", agentID)
	t.Setenv("ASP_PROJECT", projectID)
	t.Setenv("ASP_HANDLE", "")
	// Skip remote dense embedding for tests; FTS path is sufficient for assertions.
	t.Setenv("WRKQ_SEARCH_DENSE_PROVIDER", "none")
}

func TestHandoffSearchMatchesTitleBodyAndScope(t *testing.T) {
	database, _ := setupTestEnv(t)
	setSearchScopeEnv(t, database.Path(), "larry", "wrkq")

	titleMatch := seedHandoff(t, database, "larry", "wrkq", "Quartz deploy notes", "plain body")
	bodyMatch := seedHandoff(t, database, "larry", "wrkq", "Ordinary title", "Body mentions quartz retry steps")
	scopeMatch := seedHandoff(t, database, "larry", "wrkq", "Scope-only title", "plain body")
	seedHandoff(t, database, "larry", "wrkq", "No match", "plain body")
	seedHandoff(t, database, "cody", "wrkq", "Quartz other agent", "plain body")

	titleRes := runHandoffSearchCLI(t, []string{"handoff", "search", "quartz"})
	if titleRes.code != 0 || titleRes.err != nil {
		t.Fatalf("search failed code=%d err=%v stdout=%s stderr=%s", titleRes.code, titleRes.err, titleRes.stdout, titleRes.stderr)
	}
	titleOut := decodeHandoffSearchOutput(t, titleRes.stdout)
	got := map[string]bool{}
	for _, h := range titleOut.Handoffs {
		got[h.ID] = true
	}
	if !got[titleMatch.ID] || !got[bodyMatch.ID] {
		t.Fatalf("expected title/body matches, got ids=%+v", got)
	}
	if got[scopeMatch.ID] {
		t.Fatalf("scope-only record should not match query quartz")
	}

	scopeRes := runHandoffSearchCLI(t, []string{"handoff", "search", "agent:larry:project:wrkq"})
	if scopeRes.code != 0 || scopeRes.err != nil {
		t.Fatalf("scope search failed code=%d err=%v stdout=%s stderr=%s", scopeRes.code, scopeRes.err, scopeRes.stdout, scopeRes.stderr)
	}
	scopeOut := decodeHandoffSearchOutput(t, scopeRes.stdout)
	if len(scopeOut.Handoffs) != 4 {
		t.Fatalf("expected 4 current-scope matches, got %d", len(scopeOut.Handoffs))
	}
}

func TestHandoffSearchDefaultPendingAndStatusAll(t *testing.T) {
	database, _ := setupTestEnv(t)
	setSearchScopeEnv(t, database.Path(), "larry", "wrkq")

	pending := seedHandoff(t, database, "larry", "wrkq", "needle pending", "body")
	ack := seedHandoff(t, database, "larry", "wrkq", "needle acknowledged", "body")
	acknowledgeSeeded(t, database, ack.ID, "larry")

	defaultRes := runHandoffSearchCLI(t, []string{"handoff", "search", "needle"})
	if defaultRes.code != 0 || defaultRes.err != nil {
		t.Fatalf("default status search failed code=%d err=%v stdout=%s stderr=%s", defaultRes.code, defaultRes.err, defaultRes.stdout, defaultRes.stderr)
	}
	defaultOut := decodeHandoffSearchOutput(t, defaultRes.stdout)
	if len(defaultOut.Handoffs) != 1 || defaultOut.Handoffs[0].ID != pending.ID {
		t.Fatalf("default search should return only pending %s, got %+v", pending.ID, defaultOut.Handoffs)
	}

	allRes := runHandoffSearchCLI(t, []string{"handoff", "search", "needle", "--status", "all"})
	if allRes.code != 0 || allRes.err != nil {
		t.Fatalf("--status all search failed code=%d err=%v stdout=%s stderr=%s", allRes.code, allRes.err, allRes.stdout, allRes.stderr)
	}
	allOut := decodeHandoffSearchOutput(t, allRes.stdout)
	if len(allOut.Handoffs) != 2 {
		t.Fatalf("--status all should return 2 matches, got %d", len(allOut.Handoffs))
	}
	statuses := map[string]int{}
	for _, h := range allOut.Handoffs {
		statuses[h.Status]++
	}
	if statuses[store.HandoffStatusPending] != 1 || statuses[store.HandoffStatusAcknowledged] != 1 {
		t.Fatalf("expected 1 pending + 1 acknowledged, got %+v", statuses)
	}
}

func TestHandoffSearchScopeFilter(t *testing.T) {
	database, _ := setupTestEnv(t)
	setSearchScopeEnv(t, database.Path(), "larry", "wrkq")

	seedHandoff(t, database, "larry", "wrkq", "needle larry", "body")
	cody := seedHandoff(t, database, "cody", "wrkq", "needle cody", "body")

	res := runHandoffSearchCLI(t, []string{"handoff", "search", "needle", "--scope", "cody@wrkq"})
	if res.code != 0 || res.err != nil {
		t.Fatalf("scope-filtered search failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	out := decodeHandoffSearchOutput(t, res.stdout)
	if len(out.Handoffs) != 1 || out.Handoffs[0].ID != cody.ID {
		t.Fatalf("expected only cody match %s, got %+v", cody.ID, out.Handoffs)
	}
}

func TestHandoffSearchPagination(t *testing.T) {
	database, _ := setupTestEnv(t)
	setSearchScopeEnv(t, database.Path(), "larry", "wrkq")

	for i := 0; i < 5; i++ {
		seedHandoff(t, database, "larry", "wrkq", fmt.Sprintf("page needle %d", i), "body")
	}

	first := runHandoffSearchCLI(t, []string{"handoff", "search", "needle", "--limit", "2", "--porcelain", "--json"})
	if first.code != 0 || first.err != nil {
		t.Fatalf("first page failed code=%d err=%v stdout=%s stderr=%s", first.code, first.err, first.stdout, first.stderr)
	}
	firstOut := decodeHandoffSearchOutput(t, first.stdout)
	if len(firstOut.Handoffs) != 2 || firstOut.NextCursor == nil || !firstOut.Truncated {
		t.Fatalf("unexpected first page: %+v", firstOut)
	}
	if !strings.Contains(first.stderr, "next_cursor=") {
		t.Fatalf("expected porcelain next_cursor on stderr, got %q", first.stderr)
	}

	second := runHandoffSearchCLI(t, []string{"handoff", "search", "needle", "--limit", "2", "--cursor", *firstOut.NextCursor})
	if second.code != 0 || second.err != nil {
		t.Fatalf("second page failed code=%d err=%v stdout=%s stderr=%s", second.code, second.err, second.stdout, second.stderr)
	}
	secondOut := decodeHandoffSearchOutput(t, second.stdout)
	if len(secondOut.Handoffs) != 2 {
		t.Fatalf("second page should have 2 results, got %d", len(secondOut.Handoffs))
	}
	firstIDs := map[string]bool{}
	for _, h := range firstOut.Handoffs {
		firstIDs[h.ID] = true
	}
	for _, h := range secondOut.Handoffs {
		if firstIDs[h.ID] {
			t.Fatalf("handoff %s appeared on both pages", h.ID)
		}
	}
}

func TestHandoffSearchEmptyQueryValidation(t *testing.T) {
	database, _ := setupTestEnv(t)
	setSearchScopeEnv(t, database.Path(), "larry", "wrkq")

	for _, args := range [][]string{
		{"handoff", "search"},
		{"handoff", "search", "   "},
	} {
		res := runHandoffSearchCLI(t, args)
		if res.code != 1 {
			t.Fatalf("args=%v expected exit 1, got %d stdout=%s stderr=%s", args, res.code, res.stdout, res.stderr)
		}
		errOut := decodeHandoffSearchError(t, res.stderr)
		if errOut.Error.Code != "validation_error" || errOut.Error.Example == "" {
			t.Fatalf("expected validation_error with example, got %+v", errOut.Error)
		}
	}
}

func TestHandoffSearchNoMatches(t *testing.T) {
	database, _ := setupTestEnv(t)
	setSearchScopeEnv(t, database.Path(), "larry", "wrkq")

	seedHandoff(t, database, "larry", "wrkq", "ordinary", "body")

	jsonRes := runHandoffSearchCLI(t, []string{"handoff", "search", "missing"})
	if jsonRes.code != 0 || jsonRes.err != nil {
		t.Fatalf("no-match JSON search failed code=%d err=%v stdout=%s stderr=%s", jsonRes.code, jsonRes.err, jsonRes.stdout, jsonRes.stderr)
	}
	jsonOut := decodeHandoffSearchOutput(t, jsonRes.stdout)
	if len(jsonOut.Handoffs) != 0 || jsonOut.NextCursor != nil || jsonOut.Truncated {
		t.Fatalf("expected empty JSON result, got %+v", jsonOut)
	}

	humanRes := runHandoffSearchCLI(t, []string{"handoff", "search", "missing", "--human"})
	if humanRes.code != 0 || humanRes.err != nil {
		t.Fatalf("no-match human search failed code=%d err=%v stdout=%s stderr=%s", humanRes.code, humanRes.err, humanRes.stdout, humanRes.stderr)
	}
	if !strings.Contains(humanRes.stdout, `No handoffs match "missing" in agent:larry:project:wrkq.`) {
		t.Fatalf("unexpected human empty message: %q", humanRes.stdout)
	}
}

func TestHandoffSearchOutputModes(t *testing.T) {
	database, _ := setupTestEnv(t)
	setSearchScopeEnv(t, database.Path(), "larry", "wrkq")

	seedHandoff(t, database, "larry", "wrkq", "mode needle", "body")

	ndjsonDefault := runHandoffSearchCLI(t, []string{"handoff", "search", "needle"})
	if ndjsonDefault.code != 0 {
		t.Fatalf("default piped output should be NDJSON: code=%d stdout=%s stderr=%s", ndjsonDefault.code, ndjsonDefault.stdout, ndjsonDefault.stderr)
	}
	defaultLines := strings.Split(strings.TrimSpace(ndjsonDefault.stdout), "\n")
	if len(defaultLines) != 2 {
		t.Fatalf("default piped NDJSON should write 1 entry + 1 footer line, got %d lines: %q", len(defaultLines), ndjsonDefault.stdout)
	}
	for i, line := range defaultLines {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("default NDJSON line %d not valid JSON: %v\n%s", i, err, line)
		}
	}

	human := runHandoffSearchCLI(t, []string{"handoff", "search", "needle", "--human"})
	if human.code != 0 || !strings.Contains(human.stdout, "ID") || !strings.Contains(human.stdout, "Title") {
		t.Fatalf("--human did not produce table: code=%d stdout=%s stderr=%s", human.code, human.stdout, human.stderr)
	}

	ndjson := runHandoffSearchCLI(t, []string{"handoff", "search", "needle", "--ndjson"})
	if ndjson.code != 0 {
		t.Fatalf("--ndjson failed code=%d stdout=%s stderr=%s", ndjson.code, ndjson.stdout, ndjson.stderr)
	}
	lines := strings.Split(strings.TrimSpace(ndjson.stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("--ndjson should write 1 entry + 1 footer line, got %d lines: %q", len(lines), ndjson.stdout)
	}
	for i, line := range lines {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("ndjson line %d not valid JSON: %v\n%s", i, err, line)
		}
	}

	conflict := runHandoffSearchCLI(t, []string{"handoff", "search", "needle", "--json", "--human"})
	if conflict.code != 1 {
		t.Fatalf("conflicting modes should exit 1, got %d stdout=%s stderr=%s", conflict.code, conflict.stdout, conflict.stderr)
	}
}
