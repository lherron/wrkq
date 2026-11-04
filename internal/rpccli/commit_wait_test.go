package rpccli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"
	"time"
)

type commitLoopTransport struct {
	calls []string
	waits int
	reads int
}

func (tr *commitLoopTransport) Close() error { return nil }
func (tr *commitLoopTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	tr.calls = append(tr.calls, method)
	switch method {
	case "wrkq.commit.wait":
		tr.waits++
		if tr.waits > 1 {
			b, _ := json.Marshal(params)
			var p struct {
				Since     string
				TimeoutMs int
			}
			_ = json.Unmarshal(b, &p)
			if p.Since != "opaque" || p.TimeoutMs > 25000 || p.TimeoutMs <= 0 {
				return nil, fmt.Errorf("bad wait params: %s", b)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(30 * time.Millisecond):
			}
		}
		return json.RawMessage(`{"token":"opaque","changed":true}`), nil
	case "wrkq.monitor.stateView":
		tr.reads++
		if tr.waits == 0 {
			return nil, fmt.Errorf("read before token acquisition")
		}
		return json.RawMessage(fmt.Sprintf(`{"met":%t,"unmet":["T-1"]}`, tr.reads == 2)), nil
	}
	return nil, fmt.Errorf("unexpected %s", method)
}
func TestMonitorWaitTakesTokenBeforeReadAndWaitsForCommit(t *testing.T) {
	tr := &commitLoopTransport{}
	result, _, code, err := monitorWaitLoop(t.Context(), tr, monitorStreamOpts{condition: "terminal", timeout: time.Second})
	if err != nil || code != 0 || result != monitorResultMet || tr.reads != 2 || tr.waits != 2 {
		t.Fatalf("result=%s code=%d err=%v calls=%v", result, code, err, tr.calls)
	}
}

type commitPageTransport struct {
	calls  []string
	pages  int
	waited bool
}

func (tr *commitPageTransport) Close() error { return nil }
func (tr *commitPageTransport) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	tr.calls = append(tr.calls, method)
	switch method {
	case "wrkq.commit.wait":
		if len(tr.calls) > 1 && tr.pages < 3 {
			return nil, fmt.Errorf("waited with undrained backlog")
		}
		tr.waited = true
		return json.RawMessage(`{"token":"opaque","changed":true}`), nil
	case "wrkq.history.tailView", "wrkq.monitor.eventsView":
		if !tr.waited {
			return nil, fmt.Errorf("read before token")
		}
		tr.pages++
		if tr.pages < 3 {
			return json.RawMessage(fmt.Sprintf(`{"items":[],"high_water":%d}`, tr.pages)), nil
		}
		return nil, fmt.Errorf("drained")
	}
	return nil, fmt.Errorf("unexpected %s", method)
}
func TestCommitFollowDrainsPagesBeforeParking(t *testing.T) {
	for _, raw := range []bool{true, false} {
		t.Run(fmt.Sprint(raw), func(t *testing.T) {
			tr := &commitPageTransport{}
			if raw {
				_, _ = watchTailLoop(t.Context(), tr, io.Discard, 0, time.Second, 0)
			} else {
				_, _, _, _ = monitorFollowLoop(t.Context(), tr, json.NewEncoder(io.Discard), monitorStreamOpts{timeout: time.Second})
			}
			if tr.pages != 3 {
				t.Fatalf("pages=%d calls=%v", tr.pages, tr.calls)
			}
		})
	}
}
