//go:build wrkq_local

package rpccli

// handoff_read_cli_test.go — characterization of the handoff read and
// acknowledge verbs through the in-process wrkq CLI: list and search in their
// human, JSON and NDJSON modes (empty pages, the "more available" footer, the
// pagination footer and the stderr cursor echo), their shared validation
// errors, and acknowledge's success and already-acknowledged exit code.

import (
	"encoding/json"
	"strings"
	"testing"
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
