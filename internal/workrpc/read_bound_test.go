//go:build wrkq_local

package workrpc

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// newBoundTestServer is an initialized server with stub handlers: a bounded
// read that blocks until released (slowRead), a bounded read that answers at
// once (fastRead), and a write that is not bounded (slowWrite).
func newBoundTestServer(t *testing.T, deadline time.Duration, maxAbandoned int64) (*Server, chan struct{}) {
	t.Helper()
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	s := NewServer(nil)
	s.reads.deadline = deadline
	s.reads.maxAbandoned = maxAbandoned
	s.Register("rpc.initialize", HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{}`), nil
	}))
	// slowRead ignores its context, like a handler stuck in a query that was
	// not issued with *Context: only the bound can answer for it.
	s.Register("wrkq.task.lsView", HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		<-release
		return json.RawMessage(`{"slow":true}`), nil
	}))
	s.Register("wrkq.task.show", HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"fast":true}`), nil
	}))
	s.Register("wrkq.task.list", HandlerFunc(func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}))
	s.Register("wrkq.task.update", HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		time.Sleep(3 * deadline)
		return json.RawMessage(`{"written":true}`), nil
	}))
	s.Register("wrkq.room.show", HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		panic("boom")
	}))
	if resp, _ := s.HandleRequest(t.Context(), Request{JSONRPC: "2.0", ID: json.RawMessage(`0`), Method: "rpc.initialize"}); resp.Error != nil {
		t.Fatalf("initialize: %+v", resp.Error)
	}
	return s, release
}

func call(s *Server, method string) Response {
	resp, _ := s.HandleRequest(context.Background(), Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method, Params: json.RawMessage(`{}`)})
	return resp
}

func domainCode(t *testing.T, resp Response) string {
	t.Helper()
	if resp.Error == nil {
		return ""
	}
	var data struct {
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal(resp.Error.Data, &data); err != nil {
		t.Fatalf("decode error data: %v", err)
	}
	if data.Code == CodeWorkRPCTimeout && !data.Retryable {
		t.Fatalf("%s must be retryable: %s", CodeWorkRPCTimeout, resp.Error.Data)
	}
	return data.Code
}

// One read stuck in the store must not hold up another read: the global
// handler mutex that did so is gone (T-09997).
func TestSlowReadDoesNotBlockConcurrentRead(t *testing.T) {
	s, release := newBoundTestServer(t, time.Minute, 16)

	slow := make(chan Response, 1)
	go func() { slow <- call(s, "wrkq.task.lsView") }()

	fast := make(chan Response, 20)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); fast <- call(s, "wrkq.task.show") }()
	}
	waited := make(chan struct{})
	go func() { wg.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent reads queued behind a slow read")
	}
	close(fast)
	for resp := range fast {
		if resp.Error != nil || string(resp.Result) != `{"fast":true}` {
			t.Fatalf("fast read = %+v", resp)
		}
	}
	select {
	case resp := <-slow:
		t.Fatalf("slow read answered before release: %+v", resp)
	default:
	}
	close(release)
	if resp := <-slow; resp.Error != nil || string(resp.Result) != `{"slow":true}` {
		t.Fatalf("slow read after release = %+v", resp)
	}
}

func TestReadPastDeadlineAnswersTypedTimeout(t *testing.T) {
	s, release := newBoundTestServer(t, 50*time.Millisecond, 16)

	start := time.Now()
	resp := call(s, "wrkq.task.lsView")
	if got := domainCode(t, resp); got != CodeWorkRPCTimeout {
		t.Fatalf("code = %q (%+v), want %s", got, resp.Error, CodeWorkRPCTimeout)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("timeout answered after %s", elapsed)
	}
	if n := s.reads.abandoned.Load(); n != 1 {
		t.Fatalf("abandoned = %d, want 1", n)
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for s.reads.abandoned.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("abandoned read never accounted as finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A handler that honours its context returns ctx.Err(); the caller still sees
// the typed timeout, not an internal error.
func TestContextAwareReadPastDeadlineAnswersTypedTimeout(t *testing.T) {
	s, _ := newBoundTestServer(t, 50*time.Millisecond, 16)
	if got := domainCode(t, call(s, "wrkq.task.list")); got != CodeWorkRPCTimeout {
		t.Fatalf("code = %q, want %s", got, CodeWorkRPCTimeout)
	}
}

func TestAbandonedReadsPastLimitAreRefusedAtOnce(t *testing.T) {
	s, _ := newBoundTestServer(t, 30*time.Millisecond, 1)
	if got := domainCode(t, call(s, "wrkq.task.lsView")); got != CodeWorkRPCTimeout {
		t.Fatalf("first slow read code = %q", got)
	}
	start := time.Now()
	resp := call(s, "wrkq.task.show")
	if got := domainCode(t, resp); got != CodeWorkRPCTimeout {
		t.Fatalf("read at the abandoned limit = %+v, want refusal", resp)
	}
	if !json.Valid(resp.Error.Data) || time.Since(start) > 25*time.Millisecond {
		t.Fatalf("refusal was not immediate (%s)", time.Since(start))
	}
}

func TestWriteIsNotBoundedByReadDeadline(t *testing.T) {
	s, _ := newBoundTestServer(t, 20*time.Millisecond, 16)
	resp := call(s, "wrkq.task.update")
	if resp.Error != nil || string(resp.Result) != `{"written":true}` {
		t.Fatalf("write = %+v, want it to run to completion", resp)
	}
}

func TestBoundedReadPanicAnswersInternal(t *testing.T) {
	s, _ := newBoundTestServer(t, time.Second, 16)
	if got := domainCode(t, call(s, "wrkq.room.show")); got != CodeWorkRPCInternal {
		t.Fatalf("panicking read code = %q, want %s", got, CodeWorkRPCInternal)
	}
}

func TestBoundedReadMethodsAreCatalogMethods(t *testing.T) {
	catalog := map[string]bool{}
	for _, method := range MethodCatalog() {
		catalog[method] = true
	}
	for method := range boundedReadMethods {
		if !catalog[method] {
			t.Errorf("bounded read %q is not a catalog method", method)
		}
	}
}

// The timeout code is an infrastructure signal like WRKQ_DB_BUSY: adding it
// must not move the protocol schema hash every client pins.
func TestTimeoutCodeStaysOutOfSchemaCatalog(t *testing.T) {
	for _, code := range ErrorCodeCatalog() {
		if code == CodeWorkRPCTimeout {
			t.Fatalf("%s is in ErrorCodeCatalog; it would move ProtocolSchemaHash", CodeWorkRPCTimeout)
		}
	}
}
