package workrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/attribution"
)

func TestRemoteStdioForwardsCallerScopeHeader(t *testing.T) {
	const caller = "agent:faber:project:wrkq:task:T-12364"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Wrkq-Scope-Ref"); got != caller {
			t.Errorf("scope = %q, want %q", got, caller)
		}
		if got := r.Header.Get("X-Wrkq-Principal-Ref"); got != "agent:faber" {
			t.Errorf("principal = %q", got)
		}
		var req Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	input := strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"rpc.initialize\",\"params\":{}}\n")
	var out bytes.Buffer
	ctx := attribution.WithCallerScope(context.Background(), caller)
	if err := ServeRemoteStdio(ctx, input, &out, strings.TrimPrefix(server.URL, "http://"), "", "agent:faber"); err != nil {
		t.Fatal(err)
	}
}
