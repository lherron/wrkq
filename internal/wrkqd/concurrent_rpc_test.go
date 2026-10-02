package wrkqd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// TestDaemonRealHandlersRunConcurrentlyBehindASlowRead drives the real
// registry over the daemon's HTTP route (T-09997). One read is held stuck; with
// it held, real reads (task show, envelope inbox) and real writes (task
// create, room say) from many goroutines must all complete. Under -race this
// is also the audit that no handler state relied on the old global mutex.
func TestDaemonRealHandlersRunConcurrentlyBehindASlowRead(t *testing.T) {
	database, _ := setupTestEnv(t)
	cfg := &config.Config{DBPath: database.Path(), AttachmentsMaxMB: 50}
	api, opts, err := bootstrap.Server(database, cfg)
	if err != nil {
		t.Fatalf("bootstrap.Server: %v", err)
	}
	rpcServer := workrpc.NewServer(nil)
	workrpc.RegisterAPI(rpcServer, api, opts)

	// Stand in a stuck read for one bounded method; everything else is real.
	release := make(chan struct{})
	stuck := make(chan struct{})
	rpcServer.Register("wrkq.task.lsView", workrpc.HandlerFunc(func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		close(stuck)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return json.RawMessage(`{}`), nil
	}))
	defer close(release)

	s := &daemonServer{db: database, cfg: cfg, token: "secret", workrpc: rpcServer}
	handler := s.withAuth(s.handleWorkRPC)
	rpc := func(id int, method string, params any) workrpc.Response {
		raw, _ := json.Marshal(params)
		body, _ := json.Marshal(workrpc.Request{JSONRPC: "2.0", ID: json.RawMessage(fmt.Sprint(id)), Method: method, Params: raw})
		req := httptest.NewRequest(http.MethodPost, "/v1/rpc", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		handler(rec, req)
		var resp workrpc.Response
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Errorf("%s: decode %q: %v", method, rec.Body.String(), err)
		}
		return resp
	}
	must := func(resp workrpc.Response, label string) json.RawMessage {
		t.Helper()
		if resp.Error != nil {
			t.Fatalf("%s: %+v %s", label, resp.Error, resp.Error.Data)
		}
		return resp.Result
	}

	must(rpc(0, "rpc.initialize", map[string]any{"protocolVersion": workrpc.ProtocolVersion}), "initialize")
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(must(rpc(1, "wrkq.task.create", map[string]any{
		"path": "inbox/concurrency-seed", "title": "seed", "principalRef": "agent:tester",
	}), "seed create"), &created)

	go rpc(2, "wrkq.task.lsView", map[string]any{})
	select {
	case <-stuck:
	case <-time.After(5 * time.Second):
		t.Fatal("stuck read never started")
	}

	const workers = 24
	var wg sync.WaitGroup
	errs := make(chan string, workers*4)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := 100 + i*10
			calls := []struct {
				method string
				params any
			}{
				{"wrkq.task.show", map[string]any{"task": created.ID}},
				{"wrkq.envelope.inboxView", map[string]any{"scopeRef": "tester@inbox:primary", "principalRef": "agent:tester"}},
				{"wrkq.task.create", map[string]any{"path": fmt.Sprintf("inbox/concurrent-%d", i), "title": "concurrent", "principalRef": "agent:tester"}},
				{"wrkq.room.say", map[string]any{"ref": created.ID, "body": fmt.Sprintf("hello %d", i), "to": []string{"tester@inbox:primary"}, "principalRef": "agent:sender", "scopeRef": "sender@inbox:primary"}},
			}
			for n, c := range calls {
				if resp := rpc(id+n, c.method, c.params); resp.Error != nil {
					errs <- fmt.Sprintf("%s: %s %s", c.method, resp.Error.Message, resp.Error.Data)
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("real reads and writes stalled behind a stuck read")
	}
	close(errs)
	for e := range errs {
		t.Error(e)
	}

	var inbox struct {
		Groups []struct {
			Items []json.RawMessage `json:"items"`
		} `json:"groups"`
	}
	_ = json.Unmarshal(must(rpc(9000, "wrkq.envelope.inboxView", map[string]any{"scopeRef": "tester@inbox:primary", "principalRef": "agent:tester"}), "final inbox"), &inbox)
	got := 0
	for _, g := range inbox.Groups {
		got += len(g.Items)
	}
	if got != workers {
		t.Fatalf("inbox after concurrent says holds %d envelopes, want %d", got, workers)
	}
}
