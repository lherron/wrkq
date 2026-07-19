package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/nodeauth"
)

// healthNode drives the /v1/health route through withAuth and returns the
// status plus the nodeId the daemon resolved server-side.
func healthNode(t *testing.T, s *daemonServer, token string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.withAuth(s.handleHealth)(rec, req)

	var payload struct {
		Node string `json:"node"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	return rec.Code, payload.Node
}

func TestDaemonPerNodeTokenResolvesCallerNode(t *testing.T) {
	nodes, err := nodeauth.ParseSpec("max3=tok-max3,mini.lab=tok-lab")
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	s := &daemonServer{nodes: nodes}

	for token, want := range map[string]string{"tok-max3": "max3", "tok-lab": "mini.lab"} {
		status, node := healthNode(t, s, token)
		if status != http.StatusOK || node != want {
			t.Fatalf("token %q: status=%d node=%q; want 200 %q", token, status, node, want)
		}
	}
}

func TestDaemonPerNodeAuthRefusesMissingAndUnknownTokens(t *testing.T) {
	nodes, err := nodeauth.ParseSpec("max3=tok-max3")
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	// A shared token configured alongside per-node identity must not open a
	// back door: per-node auth supersedes it.
	s := &daemonServer{nodes: nodes, token: "shared-legacy"}

	for _, token := range []string{"", "tok-unknown", "shared-legacy"} {
		if status, node := healthNode(t, s, token); status != http.StatusUnauthorized {
			t.Fatalf("token %q: status=%d node=%q; want 401", token, status, node)
		}
	}
}

func TestDaemonSharedTokenStaysBackwardCompatible(t *testing.T) {
	s := &daemonServer{token: "secret"}

	status, node := healthNode(t, s, "secret")
	if status != http.StatusOK {
		t.Fatalf("status=%d; want 200", status)
	}
	if node != "" {
		t.Fatalf("shared-token deployment reported node %q; want none", node)
	}
	if status, _ := healthNode(t, s, "wrong"); status != http.StatusUnauthorized {
		t.Fatalf("wrong token status=%d; want 401", status)
	}
}

func TestDaemonNeverTrustsCallerSuppliedNode(t *testing.T) {
	nodes, err := nodeauth.ParseSpec("max3=tok-max3")
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	s := &daemonServer{nodes: nodes}

	req := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	req.Header.Set("Authorization", "Bearer tok-max3")
	req.Header.Set("X-Wrkq-Node", "mini.lab")
	rec := httptest.NewRecorder()
	s.withAuth(s.handleHealth)(rec, req)

	var payload struct {
		Node string `json:"node"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Node != "max3" {
		t.Fatalf("node=%q; want max3 (caller-supplied identity must be ignored)", payload.Node)
	}
}

func TestLoadNodeRegistrySources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.tokens")
	if err := os.WriteFile(path, []byte("max3=tok-max3\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	fromFile, err := loadNodeRegistry(DaemonOptions{NodeTokensFile: path})
	if err != nil {
		t.Fatalf("file source: %v", err)
	}
	if node, ok := fromFile.Resolve("tok-max3"); !ok || node != "max3" {
		t.Fatalf("file source resolved %q,%v; want max3,true", node, ok)
	}

	inline, err := loadNodeRegistry(DaemonOptions{NodeTokens: "max3=tok-max3"})
	if err != nil {
		t.Fatalf("inline source: %v", err)
	}
	if node, ok := inline.Resolve("tok-max3"); !ok || node != "max3" {
		t.Fatalf("inline source resolved %q,%v; want max3,true", node, ok)
	}

	if _, err := loadNodeRegistry(DaemonOptions{NodeTokens: "max3=tok-max3", NodeTokensFile: path}); err == nil {
		t.Fatal("configuring both sources should be a config error")
	}

	none, err := loadNodeRegistry(DaemonOptions{})
	if err != nil {
		t.Fatalf("unconfigured: %v", err)
	}
	if none.Enabled() {
		t.Fatal("unconfigured registry should be disabled")
	}
}

func TestServeDaemonRejectsBadNodeTokenConfig(t *testing.T) {
	if _, err := loadNodeRegistry(DaemonOptions{NodeTokens: "max3=dupe,lab=dupe"}); err == nil {
		t.Fatal("duplicate-mapped token should be rejected at load")
	}
	if _, err := loadNodeRegistry(DaemonOptions{NodeTokens: "local=tok"}); err == nil {
		t.Fatal("reserved nodeId should be rejected at load")
	}
}
