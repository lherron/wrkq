package wrkfcli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

func TestConfiguredTransportWrkfReadParityLocalAndRemote(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wrkq.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	api, opts, err := bootstrap.Server(database, &config.Config{DBPath: dbPath, AttachmentsMaxMB: 50})
	if err != nil {
		t.Fatal(err)
	}
	rpcServer := workrpc.NewServer(nil)
	workrpc.RegisterAPI(rpcServer, api, opts)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req workrpc.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		resp, ok := rpcServer.HandleRequest(r.Context(), req)
		if !ok {
			t.Error("unexpected rpc exit")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(httpServer.Close)
	oldDB := flagDB
	t.Cleanup(func() { flagDB = oldDB })
	t.Setenv("WRKQD_TOKEN", "")
	t.Setenv("WRKQD_TOKEN_FILE", "")

	flagDB = dbPath
	local, _, closeLocal, err := openConfiguredTransport(nil)
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	localResult, err := local.Call(t.Context(), "wrkf.workflow.list", map[string]any{})
	if err != nil {
		t.Fatalf("local workflow.list: %v", err)
	}
	closeLocal()

	flagDB = "rpc://" + strings.TrimPrefix(httpServer.URL, "http://")
	remote, cfg, closeRemote, err := openConfiguredTransport(nil)
	if err != nil {
		t.Fatalf("open remote: %v", err)
	}
	defer closeRemote()
	if cfg.DBPath != "" || cfg.RemoteEndpoint == "" {
		t.Fatalf("remote locator not normalized: %#v", cfg)
	}
	remoteResult, err := remote.Call(t.Context(), "wrkf.workflow.list", map[string]any{})
	if err != nil {
		t.Fatalf("remote workflow.list: %v", err)
	}
	if string(remoteResult) != string(localResult) {
		t.Fatalf("workflow.list differs by transport:\nlocal  %s\nremote %s", localResult, remoteResult)
	}
}

func TestConfiguredTransportRejectsHookCatalogOverrideForRemote(t *testing.T) {
	oldDB, oldHook := flagDB, flagHookCatalog
	t.Cleanup(func() {
		flagDB = oldDB
		flagHookCatalog = oldHook
	})
	flagDB = "rpc://127.0.0.1:17171"
	flagHookCatalog = "/caller/workspace/hook-catalog.json"
	_, _, _, err := openConfiguredTransport(nil)
	if err == nil || !strings.Contains(err.Error(), "canonical-node configuration") {
		t.Fatalf("remote hook catalog override error=%v, want hard refusal", err)
	}
}
