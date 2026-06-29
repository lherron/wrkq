package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

func TestDaemonWorkRPCRouteRequiresAuthAndDispatches(t *testing.T) {
	database, _ := setupTestEnv(t)
	cfg := &config.Config{DBPath: database.Path(), AttachmentsMaxMB: 50}
	api, opts, err := bootstrap.Server(database, cfg)
	if err != nil {
		t.Fatalf("bootstrap.Server: %v", err)
	}
	rpcServer := workrpc.NewServer(nil)
	workrpc.RegisterAPI(rpcServer, api, opts)
	s := &daemonServer{db: database, cfg: cfg, token: "secret", workrpc: rpcServer}
	handler := s.withAuth(s.handleWorkRPC)

	reqFrame := workrpc.Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "rpc.initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2026-06-14"}`),
	}
	body, _ := json.Marshal(reqFrame)

	unauth := httptest.NewRequest(http.MethodPost, "/v1/rpc", bytes.NewReader(body))
	unauthRec := httptest.NewRecorder()
	handler(unauthRec, unauth)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status=%d want %d body=%s", unauthRec.Code, http.StatusUnauthorized, unauthRec.Body.String())
	}

	auth := httptest.NewRequest(http.MethodPost, "/v1/rpc", bytes.NewReader(body))
	auth.Header.Set("Authorization", "Bearer secret")
	authRec := httptest.NewRecorder()
	handler(authRec, auth)
	if authRec.Code != http.StatusOK {
		t.Fatalf("auth status=%d want 200 body=%s", authRec.Code, authRec.Body.String())
	}
	var resp workrpc.Response
	if err := json.Unmarshal(authRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, authRec.Body.String())
	}
	if resp.Error != nil {
		t.Fatalf("rpc initialize error: %+v", resp.Error)
	}
	if len(resp.Result) == 0 {
		t.Fatal("expected initialize result")
	}
}

func TestServeDaemonRejectsNonLoopbackWithoutToken(t *testing.T) {
	database, _ := setupTestEnv(t)
	err := ServeDaemon(DaemonOptions{Addr: "0.0.0.0:0", DBPath: database.Path()})
	if err == nil {
		t.Fatal("expected non-loopback without token rejection")
	}
	if got := err.Error(); !strings.Contains(got, "non-loopback") || !strings.Contains(got, "--token") {
		t.Fatalf("unexpected error: %v", err)
	}
}
