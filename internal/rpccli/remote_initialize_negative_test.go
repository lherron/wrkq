//go:build wrkq_local

package rpccli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// skewedRemote stands up a REAL workrpc registry over HTTP, then lets a test
// corrupt just the rpc.initialize result before it reaches the client. That
// keeps the negative cases honest: everything except the one skewed field is
// exactly what a healthy daemon serves.
//
// It records every method the client asks for, so a test can prove refusal
// happened BEFORE any business request went out.
type skewedRemote struct {
	url string

	mu      sync.Mutex
	methods []string
}

func (s *skewedRemote) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.methods...)
}

// businessCalls returns the methods that are not part of the handshake.
func (s *skewedRemote) businessCalls() []string {
	var out []string
	for _, m := range s.seen() {
		switch m {
		case "rpc.initialize", "rpc.shutdown", "rpc.exit":
			continue
		}
		out = append(out, m)
	}
	return out
}

// newSkewedRemote serves a healthy registry; skew, when non-nil, rewrites the
// decoded rpc.initialize result in place.
func newSkewedRemote(t *testing.T, skew func(map[string]any)) *skewedRemote {
	t.Helper()

	dbPath, _ := migratedDBWithTask(t)
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	api, opts, err := bootstrap.Server(database, &config.Config{DBPath: dbPath, AttachmentsMaxMB: 50})
	if err != nil {
		t.Fatalf("bootstrap.Server: %v", err)
	}
	opts.ServerVersion = "v-test-dirty"
	opts.ServerRevision = "server-revision-test"
	rpcServer := workrpc.NewServer(nil)
	workrpc.RegisterAPI(rpcServer, api, opts)

	remote := &skewedRemote{}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req workrpc.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		remote.mu.Lock()
		remote.methods = append(remote.methods, req.Method)
		remote.mu.Unlock()

		resp, ok := rpcServer.HandleRequest(r.Context(), req)
		if !ok {
			return
		}
		if req.Method == "rpc.initialize" && skew != nil && resp.Result != nil {
			var result map[string]any
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				t.Errorf("unmarshal initialize result: %v", err)
				return
			}
			skew(result)
			patched, err := json.Marshal(result)
			if err != nil {
				t.Errorf("marshal skewed initialize result: %v", err)
				return
			}
			resp.Result = patched
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(httpServer.Close)

	remote.url = strings.TrimPrefix(httpServer.URL, "http://")
	return remote
}

// TestRemoteInitializeRefusesIncompatibleDaemons is the negative bundle for
// wrkq.rpc.remote-transport-locator: a same-protocolVersion daemon that is
// nonetheless incompatible must be refused during the handshake, never reach
// business dispatch, and never be reported as SQLite contention.
func TestRemoteInitializeRefusesIncompatibleDaemons(t *testing.T) {
	cases := []struct {
		name      string
		skew      func(map[string]any)
		wantErr   string
		rationale string
	}{
		{
			name: "schema hash mismatch",
			skew: func(result map[string]any) {
				result["protocolSchemaHash"] = "sha256:" + strings.Repeat("f", 64)
			},
			wantErr:   "rpc protocol schema mismatch",
			rationale: "matching protocolVersion must not excuse a diverged method/DTO/error contract",
		},
		{
			name: "schema hash absent",
			skew: func(result map[string]any) {
				delete(result, "protocolSchemaHash")
			},
			wantErr:   "reported no protocolSchemaHash",
			rationale: "a daemon too old to advertise the hash cannot be verified, so it is refused",
		},
		{
			name: "required method absent",
			skew: func(result map[string]any) {
				methods, _ := result["methods"].([]any)
				kept := make([]any, 0, len(methods))
				for _, m := range methods {
					if m != "wrkq.task.show" {
						kept = append(kept, m)
					}
				}
				result["methods"] = kept
			},
			wantErr:   `does not expose required method "wrkq.task.show"`,
			rationale: "a server wired to a different surface must not reach dispatch",
		},
		{
			name: "required capability absent",
			skew: func(result map[string]any) {
				caps, _ := result["capabilities"].(map[string]any)
				if caps == nil {
					caps = map[string]any{}
				}
				caps["wrkq"] = false
				result["capabilities"] = caps
			},
			wantErr:   "does not advertise the wrkq capability",
			rationale: "the client drives the wrkq surface; a daemon disclaiming it is incompatible",
		},
		{
			name: "protocol version mismatch",
			skew: func(result map[string]any) {
				result["protocolVersion"] = "1999-01-01"
			},
			wantErr:   "rpc protocol mismatch",
			rationale: "the pre-existing version gate must survive the added checks",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			remote := newSkewedRemote(t, tc.skew)

			tr, err := NewRemote(remote.url, "")
			if err == nil {
				_ = tr.Close()
				t.Fatalf("NewRemote accepted an incompatible daemon (%s)", tc.rationale)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.wantErr)
			}
			if tc.name == "schema hash mismatch" {
				for _, want := range []string{
					workrpc.ProtocolSchemaHash(),
					"sha256:" + strings.Repeat("f", 64),
					"server-revision-test",
				} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("schema mismatch error %q missing %q", err, want)
					}
				}
			}
			// The refusal is a transport error. Reporting it as SQLite
			// contention would send the operator hunting a lock that does
			// not exist on the canonical host.
			if strings.Contains(err.Error(), "WRKQ_DB_BUSY") {
				t.Fatalf("handshake refusal surfaced as SQLite contention: %v", err)
			}
			if calls := remote.businessCalls(); len(calls) != 0 {
				t.Fatalf("business requests dispatched despite refusal: %v", calls)
			}
		})
	}
}

// TestRemoteInitializeAcceptsHealthyDaemon is the positive control: the added
// checks must not refuse a real, healthy registry. This is what keeps the
// live max3->mini path working.
func TestRemoteInitializeAcceptsHealthyDaemon(t *testing.T) {
	remote := newSkewedRemote(t, nil)

	tr, err := NewRemote(remote.url, "")
	if err != nil {
		t.Fatalf("NewRemote refused a healthy daemon: %v", err)
	}
	defer func() { _ = tr.Close() }()

	if _, err := tr.Call(t.Context(), "wrkq.task.show", map[string]string{"task": "T-00001"}); err != nil {
		t.Fatalf("business call after healthy handshake: %v", err)
	}
	if len(remote.businessCalls()) == 0 {
		t.Fatal("expected a business call to reach the server after a healthy handshake")
	}
}