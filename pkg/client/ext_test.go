package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/workrpc"
)

func TestExtFacadePreservesIndependentFencesAndPublicSubjects(t *testing.T) {
	transport := &fakeTransport{result: func(method string, _ any) json.RawMessage {
		switch method {
		case "wrkq.ext.put":
			return json.RawMessage(`{"ext":{"subject":"T-12364","ns":"acp","kind":"request","id":"main","body":{"rev":1},"etag":1},"created":true}`)
		case "wrkq.ext.show":
			return json.RawMessage(`{"ext":{"subject":"T-12364","etag":1}}`)
		case "wrkq.ext.listView":
			return json.RawMessage(`{"items":[{"subject":{"type":"task","id":"T-12364","state":"open"},"etag":1}],"nextCursor":"opaque-next"}`)
		default:
			return json.RawMessage(`{"deleted":true}`)
		}
	}}
	c, err := New(t.Context(), WithTransport(transport), WithPrincipalRef("agent:faber"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	subject := ExtSubject{Task: "T-12364"}
	zero, one := int64(0), int64(1)
	put, err := c.Ext.Put(ExtPutParams{Subject: subject, NS: "acp", Kind: "request", ID: "main", Body: map[string]any{"rev": 1}, IfMatch: &zero})
	if err != nil || !put.Created || put.Ext.Subject != subject.Task || put.Ext.ETag != 1 {
		t.Fatalf("put=%+v, %v", put, err)
	}
	if _, err := c.Ext.Show(ExtShowParams{Subject: subject, NS: "acp", Kind: "request", ID: "main"}); err != nil {
		t.Fatal(err)
	}
	list, err := c.Ext.ListView(ExtListViewParams{Subject: &subject, NS: "acp", Kind: "request", SubjectStates: []string{"open"}, Limit: 2, Cursor: "opaque-in"})
	if err != nil || list.NextCursor != "opaque-next" || list.Items[0].Subject.ID != subject.Task {
		t.Fatalf("list=%+v, %v", list, err)
	}
	if _, err := c.Ext.Delete(ExtDeleteParams{Subject: subject, NS: "acp", Kind: "request", ID: "main", IfMatch: &one}); err != nil {
		t.Fatal(err)
	}
	for i, call := range transport.calls {
		data, _ := json.Marshal(call.params)
		var p map[string]any
		if err := json.Unmarshal(data, &p); err != nil {
			t.Fatal(err)
		}
		if i == 0 || i == 3 {
			if p["principalRef"] != "agent:faber" {
				t.Fatalf("principal missing: %s", data)
			}
			if p["ifMatch"] != float64(map[int]int{0: 0, 3: 1}[i]) {
				t.Fatalf("fence lost: %s", data)
			}
		}
		if i == 2 && (p["cursor"] != "opaque-in" || p["subjectStates"].([]any)[0] != "open") {
			t.Fatalf("filter lost: %s", data)
		}
	}
}

func TestExtRemoteForwardsConfiguredPrincipalAndScopeHeaders(t *testing.T) {
	scopeRef := "agent:faber:project:wrkq:task:T-12364"
	var seen int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req workrpc.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		result := json.RawMessage(`{}`)
		switch req.Method {
		case "rpc.initialize":
			result = healthyInitializeResult(t)
		case "wrkq.ext.put", "wrkq.ext.delete":
			seen++
			if got := r.Header.Get("X-Wrkq-Scope-Ref"); got != scopeRef {
				t.Errorf("scope=%q", got)
			}
			if got := r.Header.Get("X-Wrkq-Principal-Ref"); got != "agent:faber" {
				t.Errorf("principal=%q", got)
			}
		}
		_ = json.NewEncoder(w).Encode(workrpc.Response{JSONRPC: "2.0", ID: req.ID, Result: result})
	}))
	defer server.Close()
	transport, err := NewRemote(strings.TrimPrefix(server.URL, "http://"), "test-token", WrkqProfile)
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(t.Context(), WithTransport(transport), WithPrincipalRef("agent:faber"), WithScopeRef(scopeRef))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	subject := ExtSubject{Task: "T-12364"}
	if _, err := c.Ext.Put(ExtPutParams{Subject: subject, NS: "acp", Kind: "request", ID: "main", Body: map[string]any{"rev": 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Ext.Delete(ExtDeleteParams{Subject: subject, NS: "acp", Kind: "request", ID: "main"}); err != nil {
		t.Fatal(err)
	}
	if seen != 2 {
		t.Fatalf("extension writes seen=%d", seen)
	}
}
