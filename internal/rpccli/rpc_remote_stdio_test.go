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

func TestServeRemoteStdioForwardsInitializeReadAndDomainError(t *testing.T) {
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
			resp = workrpc.Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer httpServer.Close()

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":"init","method":"rpc.initialize","params":{"protocolVersion":"2026-06-14"}}`,
		`{"jsonrpc":"2.0","id":"show","method":"wrkq.task.show","params":{"task":"` + taskID + `"}}`,
		`{"jsonrpc":"2.0","id":"missing","method":"wrkq.task.show","params":{"task":"T-99999"}}`,
		`{"jsonrpc":"2.0","id":"shutdown","method":"rpc.shutdown","params":{}}`,
		`{"jsonrpc":"2.0","id":"after-shutdown","method":"wrkq.task.show","params":{"task":"` + taskID + `"}}`,
		`{"jsonrpc":"2.0","method":"rpc.exit"}`,
		"",
	}, "\n")
	var out bytes.Buffer
	if err := workrpc.ServeRemoteStdio(t.Context(), strings.NewReader(input), &out, strings.TrimPrefix(httpServer.URL, "http://"), ""); err != nil {
		t.Fatalf("ServeRemoteStdio: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("response count=%d want 5\n%s", len(lines), out.String())
	}
	var initResp, showResp, missingResp, shutdownResp, afterShutdownResp workrpc.Response
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("decode init: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &showResp); err != nil {
		t.Fatalf("decode show: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[2]), &missingResp); err != nil {
		t.Fatalf("decode missing: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[3]), &shutdownResp); err != nil {
		t.Fatalf("decode shutdown: %v", err)
	}
	if err := json.Unmarshal([]byte(lines[4]), &afterShutdownResp); err != nil {
		t.Fatalf("decode after shutdown: %v", err)
	}
	if string(initResp.ID) != `"init"` || initResp.Error != nil {
		t.Fatalf("bad init response: %#v", initResp)
	}
	if string(showResp.ID) != `"show"` || !strings.Contains(string(showResp.Result), seedTaskSlug) {
		t.Fatalf("bad show response: %#v", showResp)
	}
	if string(missingResp.ID) != `"missing"` || missingResp.Error == nil {
		t.Fatalf("missing response did not preserve error/id: %#v", missingResp)
	}
	if string(shutdownResp.ID) != `"shutdown"` || shutdownResp.Error != nil {
		t.Fatalf("bad shutdown response: %#v", shutdownResp)
	}
	if string(afterShutdownResp.ID) != `"after-shutdown"` || afterShutdownResp.Error == nil {
		t.Fatalf("after-shutdown request did not fail locally: %#v", afterShutdownResp)
	}

	remoteStillAvailable, ok := rpcServer.HandleRequest(t.Context(), workrpc.Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"direct-after-shutdown"`),
		Method:  "wrkq.task.show",
		Params:  json.RawMessage(`{"task":"` + taskID + `"}`),
	})
	if !ok || remoteStillAvailable.Error != nil || !strings.Contains(string(remoteStillAvailable.Result), seedTaskSlug) {
		t.Fatalf("remote server was shut down by proxied shutdown: ok=%v resp=%#v", ok, remoteStillAvailable)
	}
}
