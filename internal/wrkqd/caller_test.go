package wrkqd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/workrpc"
)

// Exercise the real HTTP envelope decoder, dispatch, request logger and sampler.
func TestCallerRequestLog(t *testing.T) {
	var logs bytes.Buffer
	old := requestLogOut
	requestLogOut = &logs
	defer func() { requestLogOut = old }()
	quietPolls = quietPollState{window: time.Minute, last: map[quietPollKey]time.Time{}, skip: map[quietPollKey]int{}}
	defer func() {
		quietPolls = quietPollState{window: DefaultPollSampleWindow, last: map[quietPollKey]time.Time{}, skip: map[quietPollKey]int{}}
	}()
	rpc := workrpc.NewServer(nil)
	s := &daemonServer{workrpc: rpc}
	h := withRequestLog(http.HandlerFunc(s.handleWorkRPC), listenerMain)
	cases := []struct{ header, caller, want string }{
		{"seat@wrkq:primary", `"envelope"`, "seat@wrkq:primary"},
		{"", `"cody@wrkq:T-12517"`, "cody@wrkq:T-12517"},
		{"bad value", `"valid"`, "unknown"},
		{"", `"bad value"`, "unknown"},
		{"", `123`, "unknown"},
		{"", `null`, "unknown"},
		{"", `{}`, "unknown"},
		{"", `"` + strings.Repeat("a", 129) + `"`, "unknown"},
	}
	for _, tc := range cases {
		logs.Reset()
		req := httptest.NewRequest("POST", "/v1/rpc", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"probe","caller":`+tc.caller+`}`))
		if tc.header != "" {
			req.Header.Set("X-Praesidium-Caller", tc.header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("caller %s refused: %d %s", tc.caller, rec.Code, rec.Body.String())
		}
		var line e2eLine
		if err := json.Unmarshal(logs.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if line.Data["caller"] != tc.want {
			t.Errorf("header=%q envelope=%s caller=%v want %q", tc.header, tc.caller, line.Data["caller"], tc.want)
		}
	}
	// A successful sampled op logs once for EACH caller, sharing client/remote/op.
	logs.Reset()
	h = withRequestLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { noteRPC(r.Context(), "rpc.initialize", nil) }), listenerMain)
	for _, caller := range []string{"one", "one", "two", "two"} {
		req := httptest.NewRequest("POST", "/v1/rpc", nil)
		req.Header.Set("X-Praesidium-Caller", caller)
		req.Header.Set("User-Agent", "probe/1")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	if got := bytes.Count(logs.Bytes(), []byte("\n")); got != 2 {
		t.Fatalf("sampled lines=%d want 2: %s", got, logs.String())
	}
}
