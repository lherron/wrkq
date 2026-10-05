//go:build wrkq_local

package rpccli

// task_outcome_acceptance_test.go — `wrkq set --outcome` from the CLI: set from
// a file, amend from stdin, clear with whitespace, `find --has-outcome`, and the
// task.outcome_set history that snapshots every value with the task's campaign
// stamps.

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestTaskOutcomeCLISetEditClearHistoryAndFind(t *testing.T) {
	f := newCampaignCLIFixture(t)
	outcomeFile := t.TempDir() + "/outcome.md"
	const initial = "Shipped the first behavior.\nPreserved the full snapshot.\n"
	if err := os.WriteFile(outcomeFile, []byte(initial), 0o600); err != nil {
		t.Fatalf("write outcome fixture: %v", err)
	}
	mustRunInput := func(input string, args ...string) string {
		t.Helper()
		out, err := runCampaignCLIInput(t, f.dbPath, input, args...)
		if err != nil {
			t.Fatalf("wrkq %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}
	findHasOutcome := func() string {
		t.Helper()
		return mustRunInput("", "--project", "campaign-cli-a", "find", "--has-outcome", "--type", "t", "--ndjson")
	}

	mustRunInput("", "set", f.residentID, "--outcome", "@"+outcomeFile)
	const amended = "Amended after verification.\n"
	mustRunInput(amended, "set", f.residentID, "--outcome", "-")
	if out := findHasOutcome(); !strings.Contains(out, "resident-member") || strings.Contains(out, "enrolled-member") {
		t.Fatalf("find --has-outcome after set = %q, want resident only", out)
	}

	mustRunInput("", "set", f.residentID, "--outcome", " \n\t")
	if out := findHasOutcome(); strings.Contains(out, "resident-member") {
		t.Fatalf("cleared task still returned by --has-outcome: %q", out)
	}

	// Completion without a current outcome must succeed.
	mustRunInput("", "set", f.residentID, "--state", "completed")

	database := f.openDB(t)
	defer func() { _ = database.Close() }()
	rows, err := database.Query(`
		SELECT payload
		  FROM event_log
		 WHERE resource_uuid = ? AND event_type = 'task.outcome_set'
		 ORDER BY id`, f.residentUUID)
	if err != nil {
		t.Fatalf("query outcome history: %v", err)
	}
	defer func() { _ = rows.Close() }()
	expected := []any{initial, amended, nil}
	var index int
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan outcome history: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("decode outcome payload %q: %v", raw, err)
		}
		if index >= len(expected) {
			t.Fatalf("unexpected extra outcome event: %#v", payload)
		}
		if payload["task_uuid"] != f.residentUUID ||
			payload["outcome"] != expected[index] ||
			payload["container_uuid"] != f.campaignAUUID ||
			payload["campaign_uuid"] != f.campaignAUUID {
			t.Errorf("outcome event %d payload = %#v, want snapshot=%#v and resident campaign stamps",
				index+1, payload, expected[index])
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate outcome history: %v", err)
	}
	if index != 3 {
		t.Fatalf("task.outcome_set count = %d, want 3", index)
	}

	var outcome sql.NullString
	var state string
	if err := database.QueryRow("SELECT outcome, state FROM tasks WHERE uuid = ?", f.residentUUID).Scan(&outcome, &state); err != nil {
		t.Fatalf("load final task state: %v", err)
	}
	if outcome.Valid || state != "completed" {
		t.Fatalf("final task outcome/state = %#v/%q, want NULL/completed", outcome, state)
	}
}
