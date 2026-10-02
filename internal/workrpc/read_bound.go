//go:build wrkq_local

package workrpc

import (
	"context"
	"encoding/json"
	"log"
	"runtime/debug"
	"sync/atomic"
	"time"
)

// readBound cuts a bounded read off at its deadline (T-09997). The handler
// runs under a context that expires at the deadline, so *Context queries are
// interrupted and hand their pool connection back. A handler that does not
// notice keeps running in the background ("abandoned") and holds only a pool
// connection, never a lock. Abandoned reads are counted; past maxAbandoned new
// bounded reads are refused at once, so a pile of stuck reads cannot take
// every connection writes need.
type readBound struct {
	deadline     time.Duration
	maxAbandoned int64
	abandoned    atomic.Int64
}

type readOutcome struct {
	result json.RawMessage
	err    error
}

func (b *readBound) run(ctx context.Context, method string, handler Handler, params json.RawMessage, invoke invokeFunc) (json.RawMessage, error) {
	if n := b.abandoned.Load(); n >= b.maxAbandoned {
		log.Printf("workrpc: refused %s: %d abandoned reads still running (limit %d)", method, n, b.maxAbandoned)
		return nil, b.timeoutError(method, "abandoned_reads_saturated")
	}
	ctx, cancel := context.WithTimeout(ctx, b.deadline)
	done := make(chan readOutcome, 1)
	start := time.Now()
	go func() {
		// A panic on this goroutine is outside net/http's per-request recovery,
		// so it would take the whole daemon down; answer internal instead.
		defer func() {
			if r := recover(); r != nil {
				log.Printf("workrpc: %s panicked: %v\n%s", method, r, debug.Stack())
				done <- readOutcome{err: NewDomainError(CodeWorkRPCInternal, "internal error", false, nil)}
			}
		}()
		result, err := invoke(ctx, method, handler, params)
		done <- readOutcome{result: result, err: err}
	}()

	select {
	case out := <-done:
		// An interrupted query surfaces as the handler's own error; report
		// the deadline, not an internal failure. Read ctx.Err before cancel.
		expired := ctx.Err() != nil
		cancel()
		if out.err != nil && expired {
			return nil, b.timeoutError(method, "read_deadline")
		}
		return out.result, out.err
	case <-ctx.Done():
	}

	n := b.abandoned.Add(1)
	log.Printf("workrpc: %s exceeded the %s read deadline; abandoned (%d abandoned reads running)", method, b.deadline, n)
	go func() {
		<-done
		cancel()
		left := b.abandoned.Add(-1)
		log.Printf("workrpc: abandoned %s finished after %s (%d abandoned reads running)", method, time.Since(start).Round(time.Millisecond), left)
	}()
	return nil, b.timeoutError(method, "read_deadline")
}

func (b *readBound) timeoutError(method, reason string) error {
	return NewDomainError(CodeWorkRPCTimeout, "read exceeded the server deadline; retry", true, map[string]any{
		"method":    method,
		"reason":    reason,
		"timeoutMs": b.deadline.Milliseconds(),
	})
}

// isBoundedRead reports whether method is a pure read the server may cut off
// at the read deadline. Each entry was checked to write nothing, except the
// envelope views' ExpireDueEnvelopes sweep, which is idempotent maintenance
// that re-selects inside its own transaction. Anything not listed (writes,
// hook/external-process runs, next/suggest/preflight) runs unbounded as before.
func isBoundedRead(method string) bool {
	_, ok := boundedReadMethods[method]
	return ok
}

var boundedReadMethods = map[string]struct{}{
	"wrkq.task.show":                   {},
	"wrkq.task.catView":                {},
	"wrkq.task.list":                   {},
	"wrkq.task.lsView":                 {},
	"wrkq.task.findListView":           {},
	"wrkq.history.listView":            {},
	"wrkq.history.tailView":            {},
	"wrkq.monitor.eventsView":          {},
	"wrkq.monitor.stateView":           {},
	"wrkq.task.treeView":               {},
	"wrkq.task.blockedView":            {},
	"wrkq.task.inboxView":              {},
	"wrkq.promise.show":                {},
	"wrkq.promise.list":                {},
	"wrkq.promise.ready":               {},
	"wrkq.room.show":                   {},
	"wrkq.room.list":                   {},
	"wrkq.room.logView":                {},
	"wrkq.room.membersView":            {},
	"wrkq.envelope.show":               {},
	"wrkq.envelope.memberPage":         {},
	"wrkq.envelope.inboxView":          {},
	"wrkq.envelope.pendingView":        {},
	"wrkq.comment.list":                {},
	"wrkq.comment.show":                {},
	"wrkq.comment.catView":             {},
	"wrkq.comment.listView":            {},
	"wrkq.attachment.getBytes":         {},
	"wrkq.attachment.list":             {},
	"wrkq.attachment.listView":         {},
	"wrkq.attachment.show":             {},
	"wrkq.relation.list":               {},
	"wrkq.relation.listView":           {},
	"wrkq.container.campaignPortfolio": {},
	"wrkq.container.timelineView":      {},
	"wrkq.container.show":              {},
	"wrkq.container.catView":           {},
	"wrkq.container.list":              {},
	"wrkq.container.taskCounts":        {},
	"wrkq.project.listView":            {},
	"wrkq.projectEvent.get":            {},
	"wrkq.projectEvent.typesView":      {},
	"wrkq.webhook.listView":            {},
	"wrkq.workflow.inspect":            {},
	"wrkq.workflow.instances":          {},
	"wrkq.workflow.timeline":           {},
	"wrkq.handoff.get":                 {},
	"wrkq.handoff.listView":            {},
	"wrkq.handoff.searchView":          {},
	"wrkq.search.listView":             {},
	"wrkq.index.status":                {},
	"wrkf.workflow.show":               {},
	"wrkf.workflow.list":               {},
	"wrkf.instance.show":               {},
	"wrkf.evidence.list":               {},
	"wrkf.evidence.show":               {},
	"wrkf.ledger.list":                 {},
	"wrkf.event.query":                 {},
	"wrkf.role.list":                   {},
	"wrkf.obligation.list":             {},
	"wrkf.obligation.show":             {},
	"wrkf.check.show":                  {},
	"wrkf.check.list":                  {},
	"wrkf.hook.list":                   {},
	"wrkf.hook.show":                   {},
	"wrkf.run.show":                    {},
	"wrkf.run.list":                    {},
	"wrkf.action.show":                 {},
	"wrkf.action.list":                 {},
	"wrkf.effect.list":                 {},
	"wrkf.effect.show":                 {},
	"wrkf.watch.snapshot":              {},
}
