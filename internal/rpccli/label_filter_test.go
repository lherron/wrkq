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

func runLabelFilterCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func TestLabelFilterLocalAndAuthenticatedRemoteCLIParity(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	sidecarPath := t.TempDir() + "/search.sqlite"
	t.Setenv("WRKQ_SEARCH_ENABLED", "1")
	t.Setenv("WRKQ_SEARCH_DENSE_PROVIDER", "none")
	t.Setenv("WRKQ_SEARCH_DB_PATH", sidecarPath)

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

	const token = "label-filter-test-token"
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, `{"message":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		var req workrpc.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		resp, ok := rpcServer.HandleRequest(r.Context(), req)
		if !ok {
			t.Fatalf("unexpected rpc exit")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer httpServer.Close()
	t.Setenv("WRKQD_TOKEN", token)
	remoteLocator := "rpc://" + strings.TrimPrefix(httpServer.URL, "http://")

	if _, stderr, err := runLabelFilterCLI(t,
		"--db", dbPath, "--project", "rpccli-test-proj",
		"index", "rebuild",
	); err != nil {
		t.Fatalf("local index rebuild: %v\n%s", err, stderr)
	}

	for _, tc := range []struct {
		name string
		args []string
	}{
		{
			name: "find-repeatable-label",
			args: []string{"find", "--type", "t", "--state", "all", "--label", "alpha", "--label", "beta", "--sort", "id", "--ndjson"},
		},
		{
			name: "search-label",
			args: []string{"search", "smoke", "--state", "all", "--label", "alpha", "--sort", "updated_at", "--ndjson"},
		},
	} {
		localArgs := append([]string{"--db", dbPath, "--project", "rpccli-test-proj"}, tc.args...)
		localOut, localErrOut, localErr := runLabelFilterCLI(t, localArgs...)
		if localErr != nil {
			t.Fatalf("%s local: %v\n%s", tc.name, localErr, localErrOut)
		}
		remoteArgs := append([]string{"--db", remoteLocator, "--project", "rpccli-test-proj"}, tc.args...)
		remoteOut, remoteErrOut, remoteErr := runLabelFilterCLI(t, remoteArgs...)
		if remoteErr != nil {
			t.Fatalf("%s remote: %v\n%s", tc.name, remoteErr, remoteErrOut)
		}
		if localOut != remoteOut {
			t.Fatalf("%s local/rpc output differs:\nlocal: %s\nremote: %s", tc.name, localOut, remoteOut)
		}
		if !strings.Contains(localOut, taskID) {
			t.Fatalf("%s output missing exact-label task %s:\n%s", tc.name, taskID, localOut)
		}
	}

	localOut, stderr, err := runLabelFilterCLI(t,
		"--db", dbPath, "--project", "rpccli-test-proj",
		"find", "--type", "t", "--state", "all", "--label", "alph", "--ndjson",
	)
	if err != nil {
		t.Fatalf("find substring negative: %v\n%s", err, stderr)
	}
	if localOut != "" {
		t.Fatalf("substring label unexpectedly matched exact label: %s", localOut)
	}
}
