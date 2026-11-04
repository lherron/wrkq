package wrkfcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/lherron/wrkq/internal/workflow"
	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/spf13/cobra"
)

type commitWatchTransport struct {
	calls []string
	pages int
}

func (tr *commitWatchTransport) Close() error { return nil }
func (tr *commitWatchTransport) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	tr.calls = append(tr.calls, method)
	switch method {
	case "wrkq.commit.wait":
		return json.RawMessage(`{"token":"opaque","changed":true}`), nil
	case "wrkf.watch.snapshot":
		if tr.calls[0] != "wrkq.commit.wait" {
			return nil, fmt.Errorf("read before token")
		}
		return json.Marshal(workflow.WatchSnapshot{Met: true, Class: "closed", Until: "terminal"})
	case "wrkf.watch.events":
		tr.pages++
		if tr.pages == 1 {
			return json.Marshal(wrkfapi.WatchEventsResult{Events: make([]workflow.Event, 100), NextCursor: "opaque-page"})
		}
		return json.Marshal(wrkfapi.WatchEventsResult{Events: []workflow.Event{}, NextCursor: "opaque-page"})
	}
	return nil, fmt.Errorf("unexpected %s", method)
}
func TestWatchAcquiresTokenAndDrainsEventsBeforeParking(t *testing.T) {
	tr := &commitWatchTransport{}
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(io.Discard)
	err := runWatch(&app{transport: tr}, cmd, "T-1", watchFlags{until: "terminal", follow: true, pollInterval: "1s"})
	if err != nil || tr.pages != 2 {
		t.Fatalf("err=%v pages=%d calls=%v", err, tr.pages, tr.calls)
	}
}
