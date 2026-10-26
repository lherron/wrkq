package wrkqd

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestServeDaemonRefusesInlineAndFileNodeTokensTogether pins the daemon's own
// node-token configuration rule (token parsing itself is internal/nodeauth's):
// tokens come inline or from a file, never both, and the refusal happens
// before anything listens. A file-configured registry is the one the served
// daemon authenticates callers against.
func TestServeDaemonRefusesInlineAndFileNodeTokensTogether(t *testing.T) {
	database, _ := setupTestEnv(t)
	dbPath := database.Path()
	_ = database.Close()

	tokensFile := filepath.Join(t.TempDir(), "nodes.tokens")
	if err := os.WriteFile(tokensFile, []byte("max3=tok-max3\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Run("inline and file together is refused before listening", func(t *testing.T) {
		addr := freeAddr(t)
		err := ServeDaemon(DaemonOptions{Addr: addr, DBPath: dbPath, NodeTokens: "max3=tok-max3", NodeTokensFile: tokensFile})
		if err == nil || !strings.Contains(err.Error(), "not both") {
			t.Fatalf("ServeDaemon err = %v; want the inline-or-file refusal", err)
		}
		if conn, dialErr := net.DialTimeout("tcp", addr, 250*time.Millisecond); dialErr == nil {
			_ = conn.Close()
			t.Fatalf("daemon listener is accepting connections on %s despite the config refusal", addr)
		}
	})

	t.Run("file-configured token resolves the caller node", func(t *testing.T) {
		addr := freeAddr(t)
		stop := serveDaemon(t, DaemonOptions{Addr: addr, DBPath: dbPath, NodeTokensFile: tokensFile})
		defer stop()

		get := func(token string) (int, string) {
			req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/health", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("GET /v1/health: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			var payload struct {
				Node string `json:"node"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&payload)
			return resp.StatusCode, payload.Node
		}
		if status, node := get("tok-max3"); status != http.StatusOK || node != "max3" {
			t.Fatalf("file token: status=%d node=%q; want 200 max3", status, node)
		}
		if status, _ := get("tok-unknown"); status != http.StatusUnauthorized {
			t.Fatalf("unknown token: status=%d; want 401", status)
		}
	})
}
