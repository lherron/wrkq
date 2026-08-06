//go:build wrkq_local

package rpccli

import (
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

func TestRemoteTransportCallsWorkRPCRegistry(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	api, opts, err := bootstrap.Server(database, &config.Config{DBPath: dbPath, AttachmentsMaxMB: 50})
	if err != nil {
		t.Fatalf("bootstrap.Server: %v", err)
	}
	rpcServer := workrpc.NewServer(nil)
	workrpc.RegisterAPI(rpcServer, api, opts)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	tr, err := NewRemote(strings.TrimPrefix(httpServer.URL, "http://"), "")
	if err != nil {
		t.Fatalf("NewRemote: %v", err)
	}
	defer func() { _ = tr.Close() }()

	raw, err := tr.Call(t.Context(), "wrkq.task.show", map[string]string{"task": taskID})
	if err != nil {
		t.Fatalf("remote task.show: %v", err)
	}
	if !strings.Contains(string(raw), seedTaskSlug) {
		t.Fatalf("remote result missing seeded task slug: %s", raw)
	}
}