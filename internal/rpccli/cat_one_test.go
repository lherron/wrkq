package rpccli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

func TestCatJSONLegacyArrayBytesAndOneObjectBytes(t *testing.T) {
	one := json.RawMessage(`{"id":"T-00001","title":"A \u003c B"}`)
	two := json.RawMessage(`{"id":"T-00002","title":"Two"}`)

	var got bytes.Buffer
	if err := writeCatJSON(&got, []json.RawMessage{one}, false); err != nil {
		t.Fatalf("write legacy one-array JSON: %v", err)
	}
	const wantLegacyOne = "[\n  {\n    \"id\": \"T-00001\",\n    \"title\": \"A \\u003c B\"\n  }\n]\n"
	if got.String() != wantLegacyOne {
		t.Fatalf("legacy one-array bytes changed:\n got: %q\nwant: %q", got.String(), wantLegacyOne)
	}

	got.Reset()
	if err := writeCatJSON(&got, []json.RawMessage{one, two}, false); err != nil {
		t.Fatalf("write legacy many-array JSON: %v", err)
	}
	const wantLegacyMany = "[\n  {\n    \"id\": \"T-00001\",\n    \"title\": \"A \\u003c B\"\n  },\n  {\n    \"id\": \"T-00002\",\n    \"title\": \"Two\"\n  }\n]\n"
	if got.String() != wantLegacyMany {
		t.Fatalf("legacy many-array bytes changed:\n got: %q\nwant: %q", got.String(), wantLegacyMany)
	}

	got.Reset()
	if err := writeCatOneJSON(&got, one, false); err != nil {
		t.Fatalf("write one object JSON: %v", err)
	}
	const wantOne = "{\n  \"id\": \"T-00001\",\n  \"title\": \"A \\u003c B\"\n}\n"
	if got.String() != wantOne {
		t.Fatalf("one-object bytes:\n got: %q\nwant: %q", got.String(), wantOne)
	}

	got.Reset()
	if err := writeCatOneJSON(&got, one, true); err != nil {
		t.Fatalf("write compact one object JSON: %v", err)
	}
	const wantCompactOne = "{\"id\":\"T-00001\",\"title\":\"A \\u003c B\"}\n"
	if got.String() != wantCompactOne {
		t.Fatalf("compact one-object bytes:\n got: %q\nwant: %q", got.String(), wantCompactOne)
	}
}

func TestCatOneLocalAndAuthenticatedRemoteCLIParity(t *testing.T) {
	dbPath, firstID := migratedDBWithTask(t)
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	const secondUUID = "00000000-2222-4000-8000-000000000002"
	if _, err := database.Exec(
		`INSERT INTO tasks (
			uuid, slug, title, project_uuid, state, priority, kind,
			created_by_principal_ref, updated_by_principal_ref
		) VALUES (?, 'rpccli-second-task', 'Second task', ?, 'open', 3, 'task', ?, ?)`,
		secondUUID, seedProject, seedActor, seedActor,
	); err != nil {
		t.Fatalf("seed second task: %v", err)
	}
	var secondID string
	if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", secondUUID).Scan(&secondID); err != nil {
		t.Fatalf("fetch second task id: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.DBPath = dbPath
	api, opts, err := bootstrap.Server(database, cfg)
	if err != nil {
		t.Fatalf("bootstrap.Server: %v", err)
	}
	rpcServer := workrpc.NewServer(nil)
	workrpc.RegisterAPI(rpcServer, api, opts)

	const token = "cat-one-test-token"
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		var req workrpc.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, `{"message":"bad request"}`, http.StatusBadRequest)
			return
		}
		resp, ok := rpcServer.HandleRequest(r.Context(), req)
		if !ok {
			t.Error("unexpected rpc exit")
			http.Error(w, `{"message":"unexpected rpc exit"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer httpServer.Close()
	t.Setenv("WRKQD_TOKEN", token)
	remoteLocator := "rpc://" + strings.TrimPrefix(httpServer.URL, "http://")

	tests := []struct {
		name        string
		args        []string
		wantSuccess bool
		wantError   string
	}{
		{name: "legacy-one-array", args: []string{"cat", firstID, "--json"}, wantSuccess: true},
		{name: "legacy-many-array", args: []string{"cat", firstID, secondID, "--json"}, wantSuccess: true},
		{name: "one-object", args: []string{"cat", firstID, "--json", "--one"}, wantSuccess: true},
		{name: "one-object-output-json", args: []string{"cat", firstID, "--output", "json", "--one"}, wantSuccess: true},
		{name: "one-object-compact", args: []string{"cat", firstID, "--json", "--one", "--porcelain"}, wantSuccess: true},
		{name: "shell-expansion-zero", args: []string{"cat", "--json", "--one"}, wantError: "--one requires exactly one explicit selector (got 0)"},
		{name: "shell-expansion-many", args: []string{"cat", firstID, secondID, "--json", "--one"}, wantError: "--one requires exactly one explicit selector (got 2)"},
		{name: "unmatched-glob", args: []string{"cat", "rpccli-test-proj/no-match-*", "--json", "--one"}, wantError: "task not found:"},
		{name: "not-found", args: []string{"cat", "T-09999999", "--json", "--one"}, wantError: "task not found:"},
		{name: "implicit-json-refused", args: []string{"cat", firstID, "--one"}, wantError: "--one requires explicit JSON output"},
		{name: "ndjson-refused", args: []string{"cat", firstID, "--ndjson", "--one"}, wantError: "cannot be combined with --ndjson"},
		{name: "raw-refused", args: []string{"cat", firstID, "--output", "raw", "--one"}, wantError: "cannot be combined with --output raw"},
		{name: "markdown-refused", args: []string{"cat", firstID, "--output", "markdown", "--one"}, wantError: "invalid output mode \"markdown\""},
		{name: "human-refused", args: []string{"cat", firstID, "--output", "human", "--one"}, wantError: "not supported for this command"},
		{name: "yaml-refused", args: []string{"cat", firstID, "--output", "yaml", "--one"}, wantError: "not supported for this command"},
		{name: "tsv-refused", args: []string{"cat", firstID, "--output", "tsv", "--one"}, wantError: "not supported for this command"},
		{name: "json-raw-conflict", args: []string{"cat", firstID, "--json", "--output", "raw", "--one"}, wantError: "cannot be combined with --output raw"},
		{name: "pretty-refused", args: []string{"cat", firstID, "--json", "--pretty", "--one"}, wantError: "cannot be combined with --pretty"},
		{name: "frontmatter-refused", args: []string{"cat", firstID, "--json", "--no-frontmatter", "--one"}, wantError: "cannot be combined with --no-frontmatter"},
		{name: "nul-refused", args: []string{"cat", firstID, "--json", "--one", "--nul"}, wantError: "unknown flag: --nul"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			local := runCatOneTestCLI(t, dbPath, tc.args)
			remote := runCatOneTestCLI(t, remoteLocator, tc.args)
			if local.stdout != remote.stdout || local.stderr != remote.stderr || local.errText != remote.errText {
				t.Fatalf("local/rpc mismatch:\nlocal:  %#v\nremote: %#v", local, remote)
			}
			if tc.wantSuccess {
				if local.errText != "" {
					t.Fatalf("unexpected error: %s", local.errText)
				}
				assertCatSuccessShape(t, tc.name, local.stdout, firstID, secondID)
				return
			}
			if local.stdout != "" {
				t.Fatalf("failure emitted partial stdout: %q", local.stdout)
			}
			if !strings.Contains(local.errText, tc.wantError) {
				t.Fatalf("error %q does not contain %q", local.errText, tc.wantError)
			}
		})
	}
}

func TestCatHelpLeadsSingletonAutomationToOne(t *testing.T) {
	result := runCatOneTestCLI(t, "", []string{"cat", "--help"})
	if result.errText != "" {
		t.Fatalf("cat --help: %s", result.errText)
	}
	for _, want := range []string{
		"JSON output is always array-shaped",
		"wrkq cat T-00001 --json --one",
		"--one",
		"exactly one explicit",
	} {
		if !strings.Contains(result.stdout, want) {
			t.Fatalf("cat help missing %q:\n%s", want, result.stdout)
		}
	}
}

type catOneCLIResult struct {
	stdout  string
	stderr  string
	errText string
}

func runCatOneTestCLI(t *testing.T, locator string, args []string) catOneCLIResult {
	t.Helper()
	root := NewRootCmdFor("wrkq")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	if locator != "" {
		args = append([]string{"--db", locator}, args...)
	}
	root.SetArgs(args)
	err := root.Execute()
	result := catOneCLIResult{stdout: stdout.String(), stderr: stderr.String()}
	if err != nil {
		result.errText = err.Error()
	}
	return result
}

func assertCatSuccessShape(t *testing.T, name, output, firstID, secondID string) {
	t.Helper()
	switch name {
	case "legacy-one-array":
		var rows []map[string]any
		if err := json.Unmarshal([]byte(output), &rows); err != nil {
			t.Fatalf("decode legacy one array: %v\n%s", err, output)
		}
		if len(rows) != 1 || rows[0]["id"] != firstID {
			t.Fatalf("legacy one shape = %#v", rows)
		}
	case "legacy-many-array":
		var rows []map[string]any
		if err := json.Unmarshal([]byte(output), &rows); err != nil {
			t.Fatalf("decode legacy many array: %v\n%s", err, output)
		}
		if len(rows) != 2 || rows[0]["id"] != firstID || rows[1]["id"] != secondID {
			t.Fatalf("legacy many shape/order = %#v", rows)
		}
	default:
		var row map[string]any
		if err := json.Unmarshal([]byte(output), &row); err != nil {
			t.Fatalf("decode one object: %v\n%s", err, output)
		}
		if row["id"] != firstID {
			t.Fatalf("one object id = %#v, want %s", row["id"], firstID)
		}
		if strings.HasPrefix(output, "[") {
			t.Fatalf("--one output retained array wrapper: %q", output)
		}
		if name == "one-object-compact" && strings.Contains(strings.TrimSuffix(output, "\n"), "\n") {
			t.Fatalf("--porcelain object is not compact: %q", output)
		}
	}
}
