package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/wrkqapi"
)

func TestCommitWaitForwardsOpaqueTokenAndTimeout(t *testing.T) {
	tr := &fakeTransport{result: func(method string, params any) json.RawMessage {
		if method != "wrkq.commit.wait" {
			t.Fatalf("method=%s", method)
		}
		b, _ := json.Marshal(params)
		if string(b) != `{"since":"opaque","timeoutMs":1234}` {
			t.Fatalf("params=%s", b)
		}
		return json.RawMessage(`{"token":"next","changed":false}`)
	}}
	c, err := New(t.Context(), WithTransport(tr))
	if err != nil {
		t.Fatal(err)
	}
	ms := 1234
	got, err := c.CommitWait(CommitWaitParams{Since: "opaque", TimeoutMs: &ms})
	if err != nil || got.Token != "next" || got.Changed {
		t.Fatalf("result=%+v err=%v", got, err)
	}
}

func TestWaitForCommitBoundsOwnDeadlineAndCancellation(t *testing.T) {
	for _, parentCancel := range []bool{false, true} {
		t.Run(fmt.Sprint(parentCancel), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			tr := commitBlockingTransport{onCall: func(p CommitWaitParams) {
				if p.Since != "opaque" || p.TimeoutMs == nil || *p.TimeoutMs > 40 {
					t.Errorf("params=%+v", p)
				}
				if parentCancel {
					cancel()
				}
			}}
			start := time.Now()
			token, err := WaitForCommit(ctx, tr, "opaque", 40*time.Millisecond)
			if token != "opaque" || (parentCancel && !errors.Is(err, context.Canceled)) || (!parentCancel && err != nil) {
				t.Fatalf("token=%s err=%v", token, err)
			}
			if time.Since(start) > 250*time.Millisecond {
				t.Fatal("caller deadline overshot")
			}
		})
	}
}

type commitBlockingTransport struct{ onCall func(CommitWaitParams) }

func (tr commitBlockingTransport) Close() error { return nil }
func (tr commitBlockingTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	tr.onCall(params.(CommitWaitParams))
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestRoomWaitTakesTokenBeforeSnapshot(t *testing.T) {
	var calls []string
	tr := &fakeTransport{result: func(method string, params any) json.RawMessage {
		calls = append(calls, method)
		switch method {
		case "wrkq.commit.wait":
			return json.RawMessage(`{"token":"opaque","changed":true}`)
		case "wrkq.monitor.stateView":
			return json.RawMessage(`{"met":true,"unmet":[]}`)
		case "wrkq.room.logView":
			return json.RawMessage(`{"items":[]}`)
		}
		return json.RawMessage(`{}`)
	}}
	c, err := New(t.Context(), WithTransport(tr))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Room.waitForGroup(&RoomSayResult{WrkqRoomSayResult: wrkqapi.WrkqRoomSayResult{GroupID: "EN-1"}}, time.Second); err != nil {
		t.Fatal(err)
	}
	if calls[0] != "wrkq.commit.wait" || calls[1] != "wrkq.monitor.stateView" {
		t.Fatalf("calls=%v", calls)
	}
}

// The server answers a timed-out wait with changed:false at timeoutMs, so the
// request deadline must sit after it; a deadline at the same instant cancels
// the call first, wrkqd logs it as -32603 and the connection is torn down
// (T-12724 census: 224 errored timeout-length waits / 10 min from 12 followers).
func TestWaitForCommitLetsTheServerAnswerItsOwnTimeout(t *testing.T) {
	tr := &commitDeadlineTransport{}
	start := time.Now()
	token, err := WaitForCommit(t.Context(), tr, "same", 25*time.Second)
	if err != nil || token != "same" {
		t.Fatalf("token=%s err=%v", token, err)
	}
	if margin := tr.deadline.Sub(start) - time.Duration(tr.timeoutMs)*time.Millisecond; margin < time.Second {
		t.Fatalf("request deadline is %s after the server's own %d ms timeout, want >= 1s so the server answers first", margin, tr.timeoutMs)
	}
}

// commitDeadlineTransport records the request deadline and answers unchanged.
type commitDeadlineTransport struct {
	deadline  time.Time
	timeoutMs int
}

func (c *commitDeadlineTransport) Call(ctx context.Context, _ string, params any) (json.RawMessage, error) {
	c.deadline, _ = ctx.Deadline()
	c.timeoutMs = *params.(CommitWaitParams).TimeoutMs
	return json.RawMessage(`{"token":"same","changed":false}`), nil
}

func (*commitDeadlineTransport) Close() error { return nil }
