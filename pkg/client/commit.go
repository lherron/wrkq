package client

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/lherron/wrkq/internal/wrkqapi"
)

type CommitWaitParams = wrkqapi.CommitWaitParams
type CommitWaitResult = wrkqapi.WrkqCommitWait

// CommitWait returns an ephemeral wake hint, never a durable view cursor.
// Acquire a token before reading a bounded view, then pass it back to wait.
func (c *Client) CommitWait(params CommitWaitParams) (*CommitWaitResult, error) {
	var out CommitWaitResult
	if err := c.call("wrkq.commit.wait", params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// WaitForCommit adapts the bounded wait to a caller-owned loop. slack is the
// time to its nearest clock (at most 25s). The request context enforces short
// clocks even though the server clamps its own wait to at least one second.
// Empty since acquires the initial token immediately, before any view read.
func WaitForCommit(ctx context.Context, tr Transport, since string, slack time.Duration) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	slack = min(25*time.Second, slack)
	if slack <= 0 {
		return since, nil
	}
	waitCtx, cancel := context.WithTimeout(ctx, slack)
	defer cancel()
	ms := max(1, int((slack+time.Millisecond-1)/time.Millisecond))
	started := time.Now()
	raw, err := tr.Call(waitCtx, "wrkq.commit.wait", CommitWaitParams{Since: since, TimeoutMs: &ms})
	if err != nil {
		if ctx.Err() == nil && errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			return since, nil
		}
		return since, err
	}
	var out CommitWaitResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return since, err
	}
	// Capacity refusal returns unchanged immediately. Back off one former
	// condition-poll interval rather than spinning, still respecting the clock.
	if !out.Changed && time.Since(started) < slack {
		timer := time.NewTimer(min(100*time.Millisecond, slack-time.Since(started)))
		defer timer.Stop()
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return since, ctx.Err()
			}
		case <-timer.C:
		}
	}
	return out.Token, nil
}
