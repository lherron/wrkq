package wrkqd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/nodeauth"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
	"github.com/lherron/wrkq/internal/wrkqapi"
)

func TestDaemonWorkRPCRouteRequiresAuthAndDispatches(t *testing.T) {
	database, _ := setupTestEnv(t)
	cfg := &config.Config{DBPath: database.Path(), AttachmentsMaxMB: 50}
	api, opts, err := bootstrap.Server(database, cfg)
	if err != nil {
		t.Fatalf("bootstrap.Server: %v", err)
	}
	opts.ServerVersion = "v-test-dirty"
	opts.ServerRevision = "0123456789abcdef"
	rpcServer := workrpc.NewServer(nil)
	workrpc.RegisterAPI(rpcServer, api, opts)
	s := &daemonServer{db: database, cfg: cfg, token: "secret", workrpc: rpcServer}
	handler := s.withAuth(s.handleWorkRPC)

	reqFrame := workrpc.Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "rpc.initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2026-06-30"}`),
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
	var init struct {
		ProtocolSchemaHash string `json:"protocolSchemaHash"`
		Server             struct {
			Version  string `json:"version"`
			Revision string `json:"revision"`
		} `json:"server"`
	}
	if err := json.Unmarshal(resp.Result, &init); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if init.ProtocolSchemaHash != workrpc.ProtocolSchemaHash() {
		t.Fatalf("protocolSchemaHash=%q want %q", init.ProtocolSchemaHash, workrpc.ProtocolSchemaHash())
	}
	if init.Server.Version != "v-test-dirty" || init.Server.Revision != "0123456789abcdef" {
		t.Fatalf("server build metadata=%+v", init.Server)
	}
}

func TestDaemonWorkRPCRejectsEnvelopeOverEightMiB(t *testing.T) {
	database, _ := setupTestEnv(t)
	cfg := &config.Config{DBPath: database.Path(), AttachmentsMaxMB: 50}
	api, opts, err := bootstrap.Server(database, cfg)
	if err != nil {
		t.Fatalf("bootstrap.Server: %v", err)
	}
	rpcServer := workrpc.NewServer(nil)
	workrpc.RegisterAPI(rpcServer, api, opts)
	s := &daemonServer{db: database, cfg: cfg, workrpc: rpcServer}

	body := `{"jsonrpc":"2.0","id":1,"method":"rpc.initialize","params":{"padding":"` + strings.Repeat("x", workrpc.DefaultMaxFrameBytes) + `"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/rpc", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleWorkRPC(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want %d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	var resp workrpc.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected bounded decode error")
	}
}

func TestDaemonClaimDerivesNodeFromBearerIdentity(t *testing.T) {
	database, _ := setupTestEnv(t)
	if _, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, description,
			created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES ('00000000-0000-0000-0000-000000000099', 'T-00099', 'claim-node', 'Claim node',
			'00000000-0000-0000-0000-000000000002', 'open', 2, '', datetime('now'), datetime('now'),
			'00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	cfg := &config.Config{DBPath: database.Path(), AttachmentsMaxMB: 50}
	api, opts, err := bootstrap.Server(database, cfg)
	if err != nil {
		t.Fatalf("bootstrap.Server: %v", err)
	}
	rpcServer := workrpc.NewServer(nil)
	workrpc.RegisterAPI(rpcServer, api, opts)
	nodes, err := nodeauth.ParseSpec("max3=tok-max3,lab=tok-lab")
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	s := &daemonServer{db: database, cfg: cfg, nodes: nodes, token: "shared-must-not-work", workrpc: rpcServer}
	handler := s.withAuth(s.handleWorkRPC)

	call := func(token, method string, params any) (int, workrpc.Response) {
		t.Helper()
		paramsJSON, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("marshal params: %v", err)
		}
		body, err := json.Marshal(workrpc.Request{
			JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method, Params: paramsJSON,
		})
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		req := httptest.NewRequest(http.MethodPost, "/v1/rpc", bytes.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		// This is deliberately false. The daemon must ignore it and resolve the
		// node exclusively from the authenticated bearer.
		req.Header.Set("X-Wrkq-Node", "lab")
		rec := httptest.NewRecorder()
		handler(rec, req)
		var response workrpc.Response
		_ = json.Unmarshal(rec.Body.Bytes(), &response)
		return rec.Code, response
	}

	if status, response := call("tok-max3", "rpc.initialize", map[string]any{"protocolVersion": "2026-06-30"}); status != http.StatusOK || response.Error != nil {
		t.Fatalf("initialize: status=%d response=%+v", status, response)
	}
	status, response := call("tok-max3", "wrkq.task.claim", wrkqapi.TaskClaimParams{
		Task: "T-00099", PrincipalRef: "agent:cody", Scope: "agent:cody:project:wrkq:task:T-00099",
	})
	if status != http.StatusOK || response.Error != nil {
		t.Fatalf("claim: status=%d response=%+v", status, response)
	}
	var claim wrkqapi.WrkqTaskClaim
	if err := json.Unmarshal(response.Result, &claim); err != nil {
		t.Fatalf("decode claim: %v", err)
	}
	if claim.ClaimedNode != "max3" {
		t.Fatalf("claimedNode=%q, want bearer-derived max3", claim.ClaimedNode)
	}
	var storedNode string
	if err := database.QueryRow("SELECT claimed_node FROM tasks WHERE id = 'T-00099'").Scan(&storedNode); err != nil {
		t.Fatalf("query stored node: %v", err)
	}
	if storedNode != "max3" {
		t.Fatalf("stored claimed_node=%q, want max3", storedNode)
	}
	for _, token := range []string{"", "unknown", "shared-must-not-work"} {
		if status, _ := call(token, "wrkq.task.claim", map[string]any{"task": "T-00099"}); status != http.StatusUnauthorized {
			t.Fatalf("token %q status=%d, want 401", token, status)
		}
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
