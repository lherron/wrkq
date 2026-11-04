package wrkqd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

type commitWaitReply struct {
	Token   string `json:"token"`
	Changed bool   `json:"changed"`
}

type commitWaitHarness struct {
	url    string
	client *http.Client
}

func newCommitWaitHarness(t *testing.T, database *db.DB) commitWaitHarness {
	t.Helper()
	cfg := &config.Config{DBPath: database.Path(), AttachmentsMaxMB: 50}
	api, opts, err := bootstrap.Server(database, cfg)
	if err != nil {
		t.Fatal(err)
	}
	rpc := workrpc.NewServer(nil)
	workrpc.RegisterAPI(rpc, api, opts)
	daemon := &daemonServer{db: database, cfg: cfg, token: "secret", workrpc: rpc}
	server := httptest.NewServer(daemon.withAuth(daemon.handleWorkRPC))
	t.Cleanup(server.Close)
	h := commitWaitHarness{server.URL, server.Client()}
	h.must(t, "rpc.initialize", map[string]any{"protocolVersion": workrpc.ProtocolVersion})
	return h
}
func (h commitWaitHarness) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url+"/v1/rpc", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Wrkq-Principal-Ref", "agent:cody")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	var frame workrpc.Response
	if err := json.NewDecoder(resp.Body).Decode(&frame); err != nil {
		return nil, err
	}
	if frame.Error != nil {
		return nil, fmt.Errorf("%s: %+v", method, frame.Error)
	}
	return frame.Result, nil
}
func (h commitWaitHarness) must(t *testing.T, method string, params any) json.RawMessage {
	t.Helper()
	result, err := h.call(context.Background(), method, params)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func (h commitWaitHarness) wait(t *testing.T, since string, timeout int) commitWaitReply {
	t.Helper()
	raw := h.must(t, "wrkq.commit.wait", map[string]any{"since": since, "timeoutMs": timeout})
	var result commitWaitReply
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Token == "" {
		t.Fatal("empty token")
	}
	return result
}
func TestCommitWaitWakeAndTokenBeforeRead(t *testing.T) {
	database, _ := setupTestEnv(t)
	h := newCommitWaitHarness(t, database)
	for _, mutation := range []struct {
		method string
		params any
	}{
		{"wrkq.task.create", map[string]any{"path": "inbox/wake", "title": "wake"}},
		{"wrkq.projectEvent.post", map[string]any{"project": "inbox", "type": "test.fact", "summary": "wake", "attributes": map[string]any{}}},
	} {
		t.Run(mutation.method, func(t *testing.T) {
			token := h.wait(t, "", 1000).Token
			done := make(chan error, 1)
			go func() {
				raw, err := h.call(context.Background(), "wrkq.commit.wait", map[string]any{"since": token, "timeoutMs": 1000})
				if err == nil {
					var r commitWaitReply
					err = json.Unmarshal(raw, &r)
					if err == nil && (!r.Changed || r.Token == token) {
						err = fmt.Errorf("did not wake: %s", raw)
					}
				}
				done <- err
			}()
			time.Sleep(30 * time.Millisecond)
			start := time.Now()
			h.must(t, mutation.method, mutation.params)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(200 * time.Millisecond):
				t.Fatal("commit did not wake within 200ms")
			}
			t.Logf("commit + wake: %s", time.Since(start))
			// Read durable state after taking a token and committing. The next wait
			// must return immediately even if the broadcast has not flushed yet.
			h.must(t, "wrkq.task.show", map[string]any{"task": "inbox/wake"})
			start = time.Now()
			next := h.wait(t, token, 1000)
			if !next.Changed || time.Since(start) > 200*time.Millisecond {
				t.Fatalf("token-before-read lost commit: %+v", next)
			}
		})
	}
}
func TestCommitWaitIdleNoConnectionsAndIncarnation(t *testing.T) {
	database, _ := setupTestEnv(t)
	h := newCommitWaitHarness(t, database)
	token := h.wait(t, "", 1000).Token
	before := database.Stats().InUse
	done := make(chan error, 20)
	start := time.Now()
	for i := 0; i < 20; i++ {
		go func() {
			raw, err := h.call(context.Background(), "wrkq.commit.wait", map[string]any{"since": token, "timeoutMs": 1})
			if err == nil {
				var r commitWaitReply
				err = json.Unmarshal(raw, &r)
				if err == nil && (r.Changed || r.Token != token) {
					err = fmt.Errorf("idle result: %s", raw)
				}
			}
			done <- err
		}()
	}
	time.Sleep(100 * time.Millisecond)
	if got := database.Stats().InUse; got != before {
		t.Fatalf("parked waits use DB: before %d after %d", before, got)
	}
	for i := 0; i < 20; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < time.Second || elapsed > 2*time.Second {
		t.Fatalf("clamped idle timeout: %s", elapsed)
	}
	// A new registry represents a new process incarnation, sharing the same
	// durable store. Old tokens must force cursor replay without waiting.
	restarted := newCommitWaitHarness(t, database)
	start = time.Now()
	result := restarted.wait(t, token, 30000)
	if !result.Changed || result.Token == token || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("incarnation did not return immediately: %+v", result)
	}
}
