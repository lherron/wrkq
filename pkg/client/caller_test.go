package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCallerTransport(t *testing.T) {
	t.Setenv("PRAESIDIUM_CALLER", "component-name")
	t.Setenv("ASP_HANDLE", "seat@wrkq:primary")
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("X-Praesidium-Caller"))
		var req struct {
			ID any `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": json.RawMessage(healthyInitializeResult(t))})
	}))
	defer srv.Close()
	tr, err := NewRemote(strings.TrimPrefix(srv.URL, "http://"), "", WrkqProfile)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PRAESIDIUM_CALLER", "changed")
	_, err = tr.Call(context.Background(), "probe", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range got {
		if v != "component-name" {
			t.Errorf("HTTP caller=%q", v)
		}
	}
	var out bytes.Buffer
	c := &conn{w: &out, br: bufio.NewReader(strings.NewReader("")), caller: "seat@wrkq:primary"}
	if err = c.writeFrame(json.RawMessage(`1`), "probe", nil); err != nil {
		t.Fatal(err)
	}
	var frame map[string]any
	_ = json.Unmarshal(out.Bytes(), &frame)
	if frame["caller"] != "seat@wrkq:primary" {
		t.Errorf("stdio caller=%v", frame["caller"])
	}
}
