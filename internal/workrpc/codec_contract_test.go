//go:build wrkq_local

package workrpc_test

import (
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/workrpc"
)

// TestCodec_MaxFrameSizeRejected drives the real `wrkq rpc --stdio` binary: a
// request line larger than the frame cap is refused with a WRKQ_VALIDATION
// "frame too large" error frame carrying data.maxBytes, and the stream recovers —
// the next request in the same session is still served.
func TestCodec_MaxFrameSizeRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	// Overshoot the cap by more than one read buffer, so the reader has to
	// discard the rest of the line to resynchronize on the next frame.
	oversized := mkRPC("big", "wrkq.task.create", map[string]any{
		"title":       "oversized",
		"description": strings.Repeat("X", workrpc.DefaultMaxFrameBytes+64<<10),
	})
	frames := p2Run(t, dbPath,
		oversized,
		mkRPC("next", "wrkq.task.create", map[string]any{"title": "after oversized frame", "kind": "task"}),
	)

	refused := frames[1]
	if code := p2ErrCode(refused); code != "WRKQ_VALIDATION" {
		t.Fatalf("oversized frame: want WRKQ_VALIDATION, got %q (frame=%#v)", code, refused)
	}
	if msg := g1ErrMessage(refused); msg != "frame too large" {
		t.Errorf("oversized frame: error.message = %q, want \"frame too large\"", msg)
	}
	if got := p2ErrDataField(refused, "maxBytes"); got != float64(workrpc.DefaultMaxFrameBytes) {
		t.Errorf("oversized frame: error.data.maxBytes = %#v, want %d", got, workrpc.DefaultMaxFrameBytes)
	}
	if id, present := refused["id"]; !present || id != nil {
		t.Errorf("oversized frame: refusal id = %#v, want null (the request was never parsed)", id)
	}

	next := p2ResultOrFail(t, frames[2], "wrkq.task.create after oversized frame")
	p2AssertFieldEq(t, next, "title", "after oversized frame")
}
