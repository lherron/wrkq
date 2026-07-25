package rpccli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

type handoffCLIResult struct {
	stdout string
	stderr string
	err    error
}

func runHandoffCreateCLI(t *testing.T, locator, stdin string, args ...string) handoffCLIResult {
	t.Helper()
	root := NewRootCmdFor("wrkq")
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append([]string{"--db", locator}, args...))
	err := root.Execute()
	return handoffCLIResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func decodeCreatedHandoff(t *testing.T, result handoffCLIResult) handoffJSON {
	t.Helper()
	if result.err != nil {
		t.Fatalf("handoff create failed: %v\nstderr:\n%s", result.err, result.stderr)
	}
	var output handoffCreateOutput
	if err := json.Unmarshal([]byte(result.stdout), &output); err != nil {
		t.Fatalf("decode handoff create output: %v\nstdout:\n%s", err, result.stdout)
	}
	return output.Handoff
}

func TestHandoffCreateBodySourcesLocalAndAuthenticatedRemoteParity(t *testing.T) {
	dbPath, _ := migratedDBWithTask(t)
	t.Setenv("ASP_AGENT_ID", "cody")
	t.Setenv("WRKQD_TOKEN_FILE", "")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

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

	const token = "handoff-body-test-token"
	var requestCount atomic.Int64
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
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

	bodyPath := t.TempDir() + "/handoff.md"
	if err := os.WriteFile(bodyPath, []byte("# File body\n\n- exact\n"), 0o600); err != nil {
		t.Fatalf("write handoff body fixture: %v", err)
	}

	tests := []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{
			name: "inline-multiline-markdown",
			args: []string{"--body", "# Inline body\n\n- alpha\n- `code`"},
			want: "# Inline body\n\n- alpha\n- `code`",
		},
		{
			name: "body-file-path",
			args: []string{"--body-file", bodyPath},
			want: "# File body\n\n- exact",
		},
		{
			name:  "body-file-stdin",
			args:  []string{"--body-file", "-"},
			stdin: "stdin body\n\n- exact\n",
			want:  "stdin body\n\n- exact",
		},
		{
			name:  "inline-literal-dash-does-not-read-stdin",
			args:  []string{"--body", "-"},
			stdin: "must remain unread",
			want:  "-",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var transportBodies []string
			for _, transport := range []struct {
				name    string
				locator string
			}{
				{name: "local", locator: dbPath},
				{name: "authenticated-rpc", locator: remoteLocator},
			} {
				t.Run(transport.name, func(t *testing.T) {
					args := []string{
						"handoff", "create",
						"--scope", "cody@rpccli-test-proj",
						"--title", tc.name + "-" + transport.name,
						"--json",
					}
					args = append(args, tc.args...)
					handoff := decodeCreatedHandoff(t, runHandoffCreateCLI(t, transport.locator, tc.stdin, args...))
					if handoff.Body != tc.want {
						t.Fatalf("body mismatch:\nwant %q\ngot  %q", tc.want, handoff.Body)
					}

					getResult := runHandoffCreateCLI(t, transport.locator, "",
						"handoff", "get", handoff.ID, "--json")
					if getResult.err != nil {
						t.Fatalf("handoff get failed: %v\nstderr:\n%s", getResult.err, getResult.stderr)
					}
					var readBack handoffJSON
					if err := json.Unmarshal([]byte(getResult.stdout), &readBack); err != nil {
						t.Fatalf("decode handoff get output: %v\nstdout:\n%s", err, getResult.stdout)
					}
					if readBack.Body != tc.want {
						t.Fatalf("read-back body mismatch:\nwant %q\ngot  %q", tc.want, readBack.Body)
					}
					transportBodies = append(transportBodies, readBack.Body)
				})
			}
			if transportBodies[0] != transportBodies[1] {
				t.Fatalf("local/rpc body mismatch: local=%q rpc=%q", transportBodies[0], transportBodies[1])
			}
		})
	}

	for _, tc := range []struct {
		name      string
		sourceArg []string
		wantError string
	}{
		{
			name:      "both-sources",
			sourceArg: []string{"--body", "inline", "--body-file", bodyPath},
			wantError: "exactly one body source is required",
		},
		{
			name:      "neither-source",
			wantError: "exactly one body source is required",
		},
		{
			name:      "explicit-empty-inline",
			sourceArg: []string{"--body", ""},
			wantError: "body cannot be empty",
		},
		{
			name:      "explicit-empty-body-file",
			sourceArg: []string{"--body-file", ""},
			wantError: "body cannot be empty",
		},
	} {
		t.Run(tc.name+"-preflight", func(t *testing.T) {
			beforeRequests := requestCount.Load()
			beforeRows := handoffRowCount(t, database)
			args := []string{
				"handoff", "create",
				"--scope", "cody@rpccli-test-proj",
				"--title", tc.name,
				"--json",
			}
			args = append(args, tc.sourceArg...)
			result := runHandoffCreateCLI(t, remoteLocator, "", args...)
			if result.err == nil {
				t.Fatal("expected CLI preflight failure")
			}
			if !strings.Contains(result.stderr, tc.wantError) {
				t.Fatalf("stderr %q does not contain %q", result.stderr, tc.wantError)
			}
			if got := requestCount.Load(); got != beforeRequests {
				t.Fatalf("preflight opened RPC transport: requests before=%d after=%d", beforeRequests, got)
			}
			if got := handoffRowCount(t, database); got != beforeRows {
				t.Fatalf("preflight created a handoff row: rows before=%d after=%d", beforeRows, got)
			}
		})
	}
}

func TestHandoffCreateHelpExplainsExclusiveBodySources(t *testing.T) {
	result := runHandoffCreateCLI(t, "", "", "handoff", "create", "--help")
	if result.err != nil {
		t.Fatalf("handoff create --help: %v\n%s", result.err, result.stderr)
	}
	for _, want := range []string{
		"Exactly one body source must be explicitly selected",
		"--body string",
		"--body-file string",
		"mutually exclusive",
	} {
		if !strings.Contains(result.stdout, want) {
			t.Fatalf("help missing %q:\n%s", want, result.stdout)
		}
	}
}

func handoffRowCount(t *testing.T, database *db.DB) int {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM handoffs").Scan(&count); err != nil {
		t.Fatalf("count handoffs: %v", err)
	}
	return count
}
