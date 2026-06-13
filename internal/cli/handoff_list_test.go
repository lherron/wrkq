package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
)

// ---- helpers --------------------------------------------------------------

type handoffListExecResult struct {
	stdout string
	stderr string
	code   int
	err    error
}

func runHandoffListCLI(t *testing.T, args []string) handoffListExecResult {
	t.Helper()

	var stdout, stderr bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetIn(strings.NewReader(""))

	err := rootCmd.Execute()
	result := handoffListExecResult{
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

func decodeHandoffListOutput(t *testing.T, raw string) handoffListOutput {
	t.Helper()
	var out handoffListOutput
	if err := json.Unmarshal([]byte(raw), &out); err == nil {
		return out
	}

	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var probe map[string]interface{}
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			t.Fatalf("failed to decode list NDJSON line: %v\n%s", err, raw)
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
			t.Fatalf("failed to decode handoff NDJSON row: %v\n%s", err, raw)
		}
		out.Handoffs = append(out.Handoffs, h)
	}
	return out
}

func decodeHandoffListError(t *testing.T, raw string) handoffErrorOutput {
	t.Helper()
	var out handoffErrorOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("failed to decode JSON error: %v\n%s", err, raw)
	}
	return out
}

// seedHandoff inserts a single handoff via the store layer (no CLI) for fast
// test setup. agentID/projectID drive the scopeRef as agent:<a>:project:<p>.
func seedHandoff(t *testing.T, database *db.DB, agentID, projectID, title, body string) store.Handoff {
	t.Helper()

	tx, err := database.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	scopeRef := fmt.Sprintf("agent:%s:project:%s", agentID, projectID)
	result, err := store.CreateHandoff(context.Background(), tx, store.CreateHandoffArgs{
		ScopeRef:         scopeRef,
		ScopeKind:        "project",
		AgentID:          agentID,
		ProjectID:        projectID,
		CreatedByAgentID: agentID,
		Title:            title,
		Body:             body,
	})
	if err != nil {
		t.Fatalf("seed handoff: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return result.Handoff
}

// acknowledgeSeeded marks the most recent matching handoff as acknowledged.
func acknowledgeSeeded(t *testing.T, database *db.DB, idOrUUID, actorAgentID string) {
	t.Helper()
	if _, err := store.AcknowledgeHandoff(context.Background(), database, idOrUUID, store.AcknowledgeHandoffArgs{
		ActorAgentID: actorAgentID,
	}); err != nil {
		t.Fatalf("acknowledge %s: %v", idOrUUID, err)
	}
}

// ---- tests ----------------------------------------------------------------

func TestHandoffListEmptyScope(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:curly:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "curly")
	t.Setenv("ASP_PROJECT", "wrkq")
	t.Setenv("ASP_HANDLE", "")

	res := runHandoffListCLI(t, []string{"handoff", "list"})
	if res.code != 0 || res.err != nil {
		t.Fatalf("list failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	out := decodeHandoffListOutput(t, res.stdout)
	if len(out.Handoffs) != 0 {
		t.Fatalf("expected empty list, got %d handoffs", len(out.Handoffs))
	}
	if out.NextCursor != nil {
		t.Errorf("expected nil next_cursor, got %v", *out.NextCursor)
	}
	if out.Truncated {
		t.Errorf("expected truncated=false")
	}
}

func TestHandoffListDefaultPendingHidesAcknowledged(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:curly:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "curly")
	t.Setenv("ASP_PROJECT", "wrkq")

	seedHandoff(t, database, "curly", "wrkq", "alpha", "alpha body")
	seedHandoff(t, database, "curly", "wrkq", "beta", "beta body")
	ackTarget := seedHandoff(t, database, "curly", "wrkq", "gamma", "gamma body")
	acknowledgeSeeded(t, database, ackTarget.ID, "curly")

	res := runHandoffListCLI(t, []string{"handoff", "list"})
	if res.code != 0 || res.err != nil {
		t.Fatalf("list failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	out := decodeHandoffListOutput(t, res.stdout)
	if len(out.Handoffs) != 2 {
		t.Fatalf("expected 2 pending handoffs, got %d: %+v", len(out.Handoffs), out.Handoffs)
	}
	for _, h := range out.Handoffs {
		if h.Status != store.HandoffStatusPending {
			t.Errorf("expected only pending, got %s for %s", h.Status, h.ID)
		}
	}
}

func TestHandoffListStatusAll(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:curly:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "curly")
	t.Setenv("ASP_PROJECT", "wrkq")

	seedHandoff(t, database, "curly", "wrkq", "p1", "p1 body")
	ackTarget := seedHandoff(t, database, "curly", "wrkq", "a1", "a1 body")
	acknowledgeSeeded(t, database, ackTarget.ID, "curly")

	res := runHandoffListCLI(t, []string{"handoff", "list", "--status", "all"})
	if res.code != 0 || res.err != nil {
		t.Fatalf("list --status all failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	out := decodeHandoffListOutput(t, res.stdout)
	if len(out.Handoffs) != 2 {
		t.Fatalf("expected 2 handoffs, got %d", len(out.Handoffs))
	}
	statuses := map[string]int{}
	for _, h := range out.Handoffs {
		statuses[h.Status]++
	}
	if statuses[store.HandoffStatusPending] != 1 || statuses[store.HandoffStatusAcknowledged] != 1 {
		t.Errorf("expected 1 pending + 1 acknowledged, got %+v", statuses)
	}
}

func TestHandoffListStatusAcknowledged(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:curly:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "curly")
	t.Setenv("ASP_PROJECT", "wrkq")

	seedHandoff(t, database, "curly", "wrkq", "pend", "pend body")
	ackTarget := seedHandoff(t, database, "curly", "wrkq", "ack", "ack body")
	acknowledgeSeeded(t, database, ackTarget.ID, "curly")

	res := runHandoffListCLI(t, []string{"handoff", "list", "--status", "acknowledged"})
	if res.code != 0 || res.err != nil {
		t.Fatalf("list --status acknowledged failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	out := decodeHandoffListOutput(t, res.stdout)
	if len(out.Handoffs) != 1 {
		t.Fatalf("expected 1 acknowledged, got %d", len(out.Handoffs))
	}
	if out.Handoffs[0].Status != store.HandoffStatusAcknowledged {
		t.Errorf("expected acknowledged, got %s", out.Handoffs[0].Status)
	}
}

func TestHandoffListInvalidStatus(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:curly:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "curly")
	t.Setenv("ASP_PROJECT", "wrkq")

	res := runHandoffListCLI(t, []string{"handoff", "list", "--status", "bogus"})
	if res.code != 1 {
		t.Fatalf("expected exit 1, got %d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	errOut := decodeHandoffListError(t, res.stderr)
	if errOut.Error.Code != "invalid_status" {
		t.Errorf("expected invalid_status, got %s", errOut.Error.Code)
	}
}

func TestHandoffListPaginationWithCursor(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:curly:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "curly")
	t.Setenv("ASP_PROJECT", "wrkq")

	// Seed 5 handoffs; default order is created_at DESC then id DESC, so we
	// expect the last-seeded to appear first.
	for i := 0; i < 5; i++ {
		seedHandoff(t, database, "curly", "wrkq", fmt.Sprintf("seed-%d", i), fmt.Sprintf("body-%d", i))
	}

	first := runHandoffListCLI(t, []string{"handoff", "list", "--limit", "2"})
	if first.code != 0 || first.err != nil {
		t.Fatalf("first page failed code=%d err=%v stdout=%s stderr=%s", first.code, first.err, first.stdout, first.stderr)
	}
	firstOut := decodeHandoffListOutput(t, first.stdout)
	if len(firstOut.Handoffs) != 2 {
		t.Fatalf("first page should have 2 handoffs, got %d", len(firstOut.Handoffs))
	}
	if firstOut.NextCursor == nil || *firstOut.NextCursor == "" {
		t.Fatalf("first page should report next_cursor, got %v", firstOut.NextCursor)
	}
	if !firstOut.Truncated {
		t.Errorf("first page should report truncated=true")
	}

	second := runHandoffListCLI(t, []string{"handoff", "list", "--limit", "2", "--cursor", *firstOut.NextCursor})
	if second.code != 0 || second.err != nil {
		t.Fatalf("second page failed code=%d err=%v stdout=%s stderr=%s", second.code, second.err, second.stdout, second.stderr)
	}
	secondOut := decodeHandoffListOutput(t, second.stdout)
	if len(secondOut.Handoffs) != 2 {
		t.Fatalf("second page should have 2 handoffs, got %d", len(secondOut.Handoffs))
	}
	// First and second page IDs must be disjoint
	firstIDs := map[string]bool{}
	for _, h := range firstOut.Handoffs {
		firstIDs[h.ID] = true
	}
	for _, h := range secondOut.Handoffs {
		if firstIDs[h.ID] {
			t.Errorf("handoff %s appeared on both pages", h.ID)
		}
	}

	// Walk one more page; should contain the last entry and have no cursor.
	third := runHandoffListCLI(t, []string{"handoff", "list", "--limit", "2", "--cursor", *secondOut.NextCursor})
	if third.code != 0 || third.err != nil {
		t.Fatalf("third page failed code=%d err=%v", third.code, third.err)
	}
	thirdOut := decodeHandoffListOutput(t, third.stdout)
	if len(thirdOut.Handoffs) != 1 {
		t.Fatalf("third page should have 1 handoff, got %d", len(thirdOut.Handoffs))
	}
	if thirdOut.NextCursor != nil {
		t.Errorf("third page should not have next_cursor, got %v", *thirdOut.NextCursor)
	}
}

func TestHandoffListScopeFilter(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	// Set runtime env to curly so the self-scope guard doesn't fire when we
	// pass --scope curly@wrkq, but the test specifically passes --scope to
	// override; cross-scope reads are allowed on list (only create enforces).
	t.Setenv("ASP_SCOPE_REF", "agent:curly:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "curly")
	t.Setenv("ASP_PROJECT", "wrkq")

	seedHandoff(t, database, "curly", "wrkq", "curly-one", "body")
	seedHandoff(t, database, "curly", "wrkq", "curly-two", "body")
	seedHandoff(t, database, "cody", "wrkq", "cody-only", "body")

	// Override to cody@wrkq via the short handle form.
	res := runHandoffListCLI(t, []string{"handoff", "list", "--scope", "cody@wrkq"})
	if res.code != 0 || res.err != nil {
		t.Fatalf("scope filter failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	out := decodeHandoffListOutput(t, res.stdout)
	if len(out.Handoffs) != 1 {
		t.Fatalf("expected 1 handoff for cody@wrkq, got %d", len(out.Handoffs))
	}
	if out.Handoffs[0].Title != "cody-only" {
		t.Errorf("expected cody-only, got %s", out.Handoffs[0].Title)
	}
	if out.Handoffs[0].AgentID != "cody" {
		t.Errorf("expected agent_id=cody, got %s", out.Handoffs[0].AgentID)
	}
}

func TestHandoffListOutputModes(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:curly:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "curly")
	t.Setenv("ASP_PROJECT", "wrkq")

	seedHandoff(t, database, "curly", "wrkq", "single", "body")

	// Piped (bytes.Buffer is not a TTY) -> NDJSON
	ndjsonDefault := runHandoffListCLI(t, []string{"handoff", "list"})
	if ndjsonDefault.code != 0 {
		t.Fatalf("default piped output should be NDJSON: code=%d stdout=%s", ndjsonDefault.code, ndjsonDefault.stdout)
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

	// --human forces a table header even when piped
	human := runHandoffListCLI(t, []string{"handoff", "list", "--human"})
	if human.code != 0 || !strings.Contains(human.stdout, "ID") || !strings.Contains(human.stdout, "Title") {
		t.Fatalf("--human did not produce table: code=%d stdout=%s", human.code, human.stdout)
	}

	// --ndjson outputs one JSON object per line plus a final cursor footer
	ndjson := runHandoffListCLI(t, []string{"handoff", "list", "--ndjson"})
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

	// --json + --human together = validation error (exit 1)
	conflict := runHandoffListCLI(t, []string{"handoff", "list", "--json", "--human"})
	if conflict.code != 1 {
		t.Fatalf("conflicting modes should exit 1, got %d stdout=%s stderr=%s", conflict.code, conflict.stdout, conflict.stderr)
	}
}

func TestHandoffListPorcelainCursor(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:curly:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "curly")
	t.Setenv("ASP_PROJECT", "wrkq")

	for i := 0; i < 3; i++ {
		seedHandoff(t, database, "curly", "wrkq", fmt.Sprintf("seed-%d", i), "body")
	}

	res := runHandoffListCLI(t, []string{"handoff", "list", "--limit", "1", "--porcelain", "--json"})
	if res.code != 0 || res.err != nil {
		t.Fatalf("porcelain list failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stderr, "next_cursor=") {
		t.Errorf("expected next_cursor= on stderr, got %q", res.stderr)
	}
}

func TestHandoffListLimitClampedAboveMax(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:curly:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "curly")
	t.Setenv("ASP_PROJECT", "wrkq")

	seedHandoff(t, database, "curly", "wrkq", "one", "body")

	res := runHandoffListCLI(t, []string{"handoff", "list", "--limit", "9999"})
	if res.code != 0 || res.err != nil {
		t.Fatalf("clamp test failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stderr, "clamping to 500") {
		t.Errorf("expected clamp warning on stderr, got %q", res.stderr)
	}
}

func TestHandoffListHumanEmptyMessage(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:curly:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "curly")
	t.Setenv("ASP_PROJECT", "wrkq")

	res := runHandoffListCLI(t, []string{"handoff", "list", "--human"})
	if res.code != 0 || res.err != nil {
		t.Fatalf("empty human failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, "No pending handoffs for agent:curly:project:wrkq") {
		t.Errorf("expected empty pending message, got %q", res.stdout)
	}
}
