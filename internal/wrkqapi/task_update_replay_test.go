//go:build wrkq_local

package wrkqapi

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/lherron/wrkq/internal/attribution"
)

// Compare the wire result; private timestamp cache fields are not receipt data.
func sameReplayTask(a, b *WrkqTask) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && bytes.Equal(left, right)
}

func replayUpdateParams(task *WrkqTask, key, title string) TaskUpdateParams {
	etag := task.ETag
	return TaskUpdateParams{Task: task.ID, ExpectEtag: &etag, IdempotencyKey: key, Actor: "agent:writer", ScopeRef: "agent:writer:project:wrkq", Patch: TaskPatch{Title: &title}}
}

func updateReplayCount(t *testing.T, a *API, query string, args ...any) int {
	t.Helper()
	var n int
	if err := a.db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func updateReplayEvents(t *testing.T, a *API, task *WrkqTask) int {
	t.Helper()
	return updateReplayCount(t, a, "SELECT count(*) FROM event_log WHERE resource_uuid = ? AND event_type = 'task.updated'", task.UUID)
}

func updateReplayReceipt(t *testing.T, a *API, key string) string {
	t.Helper()
	var result string
	if err := a.db.QueryRow("SELECT result_json FROM wrkq_rpc_idempotency WHERE idempotency_key = ?", key).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestTaskUpdateReplayOriginalResultAndCollision(t *testing.T) {
	a := newAttributionAPI(t, "agent:seed")
	task := claimTestTask(t, a, "replay-original")
	p := replayUpdateParams(task, "update-original", "first write")
	first, err := a.TaskUpdate(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := a.TaskUpdate(context.Background(), p)
	if err != nil {
		t.Fatalf("same key and original fence must replay: %v", err)
	}
	if !sameReplayTask(first, replayed) {
		t.Fatalf("replay differs from original: first=%+v replay=%+v", first, replayed)
	}
	originalReceipt := updateReplayReceipt(t, a, p.IdempotencyKey)
	var persisted WrkqTask
	if err := json.Unmarshal([]byte(originalReceipt), &persisted); err != nil {
		t.Fatal(err)
	}
	if !sameReplayTask(first, &persisted) {
		t.Fatal("receipt does not contain exact returned task")
	}
	fresh := p
	fresh.IdempotencyKey = "fresh-stale-fence"
	if _, err := a.TaskUpdate(context.Background(), fresh); err == nil || claimErrorCode(t, err) != CodeConflict {
		t.Fatalf("fresh key stale fence = %v", err)
	}
	changed := p
	title := "changed body"
	changed.Patch.Title = &title
	// Removing the stale fence proves that the collision is checked against
	// the request, rather than accidentally refused only by the current etag.
	changed.ExpectEtag = nil
	if _, err := a.TaskUpdate(context.Background(), changed); err == nil || claimErrorCode(t, err) != CodeConflict {
		t.Fatalf("changed body collision = %v", err)
	}
	if updateReplayReceipt(t, a, p.IdempotencyKey) != originalReceipt {
		t.Fatal("collision rewrote original receipt")
	}
	later, err := a.TaskUpdate(context.Background(), replayUpdateParams(first, "later-write", "later title"))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err = a.TaskUpdate(context.Background(), p)
	if err != nil || !sameReplayTask(first, replayed) {
		t.Fatalf("replay after later write = %+v, %v", replayed, err)
	}
	current, err := a.TaskShow(context.Background(), TaskShowParams{Task: task.ID})
	if err != nil || !sameReplayTask(later, current) {
		t.Fatalf("replay mutated current task: %+v %v", current, err)
	}
	if got := updateReplayEvents(t, a, task); got != 2 {
		t.Fatalf("task.updated count = %d, want 2", got)
	}
	if updateReplayReceipt(t, a, p.IdempotencyKey) != originalReceipt {
		t.Fatal("later write rewrote original receipt")
	}
	var principal, scope string
	if err := a.db.QueryRow("SELECT principal_ref, scope_ref FROM event_log WHERE resource_uuid = ? AND event_type = 'task.updated' ORDER BY rowid LIMIT 1", task.UUID).Scan(&principal, &scope); err != nil {
		t.Fatal(err)
	}
	if principal != p.Actor || scope != p.ScopeRef {
		t.Fatalf("attribution = %q %q", principal, scope)
	}
}

func TestTaskUpdateReplayConcurrentOneMutation(t *testing.T) {
	a := newAttributionAPI(t, "agent:seed")
	task := claimTestTask(t, a, "replay-race")
	p := replayUpdateParams(task, "update-race", "winner")
	const callers = 8
	start := make(chan struct{})
	type result struct {
		task *WrkqTask
		err  error
	}
	results := make(chan result, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			task, err := a.TaskUpdate(context.Background(), p)
			results <- result{task, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	var first *WrkqTask
	for got := range results {
		if got.err != nil {
			t.Errorf("identical concurrent update: %v", got.err)
			continue
		}
		if first == nil {
			first = got.task
		} else if !sameReplayTask(first, got.task) {
			t.Errorf("concurrent result differs: %+v vs %+v", first, got.task)
		}
	}
	if got := updateReplayEvents(t, a, task); got != 1 {
		t.Errorf("task.updated count = %d, want 1", got)
	}
	if got := updateReplayCount(t, a, "SELECT count(*) FROM wrkq_rpc_idempotency WHERE idempotency_key = ?", p.IdempotencyKey); got != 1 {
		t.Errorf("receipt count = %d, want 1", got)
	}
	current, err := a.TaskShow(context.Background(), TaskShowParams{Task: task.ID})
	if err != nil || current.ETag != task.ETag+1 {
		t.Fatalf("current = %+v, %v", current, err)
	}
}

func TestTaskUpdateReplayRollbackAtomicity(t *testing.T) {
	for _, stage := range []string{"receipt", "event"} {
		t.Run(stage, func(t *testing.T) {
			a := newAttributionAPI(t, "agent:seed")
			task := claimTestTask(t, a, "replay-rollback")
			p := replayUpdateParams(task, "update-rollback", "must rollback")
			trigger := "CREATE TRIGGER fail_replay_write BEFORE INSERT ON wrkq_rpc_idempotency BEGIN SELECT RAISE(ABORT, 'injected receipt failure'); END"
			if stage == "event" {
				trigger = "CREATE TRIGGER fail_replay_write BEFORE INSERT ON event_log WHEN NEW.event_type = 'task.updated' BEGIN SELECT RAISE(ABORT, 'injected event failure'); END"
			}
			if _, err := a.db.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			if _, err := a.TaskUpdate(context.Background(), p); err == nil {
				t.Error("injected failure admitted update")
			}
			current, err := a.TaskShow(context.Background(), TaskShowParams{Task: task.ID})
			if err != nil || !sameReplayTask(task, current) {
				t.Errorf("failed write mutated task: %+v %v", current, err)
			}
			if got := updateReplayEvents(t, a, task); got != 0 {
				t.Errorf("failed write events = %d", got)
			}
			if got := updateReplayCount(t, a, "SELECT count(*) FROM wrkq_rpc_idempotency WHERE idempotency_key = ?", p.IdempotencyKey); got != 0 {
				t.Errorf("failed write receipts = %d", got)
			}
			if _, err := a.db.Exec("DROP TRIGGER fail_replay_write"); err != nil {
				t.Fatal(err)
			}
			admitted, err := a.TaskUpdate(context.Background(), p)
			if err != nil || admitted.ETag != task.ETag+1 {
				t.Fatalf("retry after rollback = %+v, %v", admitted, err)
			}
			replayed, err := a.TaskUpdate(context.Background(), p)
			if err != nil || !sameReplayTask(admitted, replayed) {
				t.Fatalf("retry replay = %+v, %v", replayed, err)
			}
		})
	}
}

func TestTaskUpdateReplayClaimAndCallerAttribution(t *testing.T) {
	a := newAttributionAPI(t, "agent:seed")
	task := claimTestTask(t, a, "replay-claim")
	scope := "agent:writer:project:wrkq:task:" + task.ID
	claim, err := a.TaskClaim(nodeContext("lab"), TaskClaimParams{Task: task.ID, PrincipalRef: "agent:writer", Scope: scope})
	if err != nil {
		t.Fatal(err)
	}
	current, err := a.TaskShow(context.Background(), TaskShowParams{Task: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	completed := "completed"
	p := TaskUpdateParams{Task: task.ID, IdempotencyKey: "claimed-completion", ExpectEtag: &current.ETag, Patch: TaskPatch{State: &completed}, ClaimScope: scope, ClaimGeneration: claim.ClaimGeneration, ClaimToken: claim.ClaimToken}
	ctx := attribution.WithCallerPrincipal(nodeContext("lab"), "agent:writer")
	callerScope := "writer@wrkq:" + task.ID
	ctx = attribution.WithCallerScope(ctx, callerScope)
	bad := p
	bad.ClaimToken = "incorrect-token"
	if _, err := a.TaskUpdate(ctx, bad); err == nil || claimErrorCode(t, err) != CodeClaimSuperseded {
		t.Fatalf("invalid completion = %v", err)
	}
	if got := updateReplayCount(t, a, "SELECT count(*) FROM wrkq_rpc_idempotency WHERE idempotency_key = ?", p.IdempotencyKey); got != 0 {
		t.Fatal("invalid claim acquired a replay receipt")
	}
	before := updateReplayEvents(t, a, task)
	first, err := a.TaskUpdate(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := a.TaskUpdate(ctx, p)
	if err != nil || !sameReplayTask(first, replayed) {
		t.Fatalf("claimed completion replay = %+v %v", replayed, err)
	}
	if got := updateReplayEvents(t, a, task); got != before+1 {
		t.Fatalf("completion added %d events, want 1", got-before)
	}
	var principal, storedScope string
	if err := a.db.QueryRow("SELECT principal_ref, scope_ref FROM event_log WHERE resource_uuid = ? AND event_type = 'task.updated' ORDER BY rowid DESC LIMIT 1", task.UUID).Scan(&principal, &storedScope); err != nil {
		t.Fatal(err)
	}
	if principal != "agent:writer" || storedScope != callerScope {
		t.Fatalf("caller attribution = %q %q", principal, storedScope)
	}
}

func TestTaskUpdateReplayBeforeSelectorResolution(t *testing.T) {
	a := newAttributionAPI(t, "agent:seed")
	task := claimTestTask(t, a, "old-selector")
	p := replayUpdateParams(task, "path-replay", "original title")
	p.Task = task.Path
	first, err := a.TaskUpdate(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	next := replayUpdateParams(first, "rename-path", "later title")
	slug := "new-selector"
	next.Patch.Slug = &slug
	renamed, err := a.TaskUpdate(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.TaskShow(context.Background(), TaskShowParams{Task: p.Task}); err == nil {
		t.Fatal("old path unexpectedly resolves")
	}
	replayed, err := a.TaskUpdate(context.Background(), p)
	if err != nil || !sameReplayTask(first, replayed) {
		t.Fatalf("replay must precede old-path resolution: %+v %v", replayed, err)
	}
	shown, err := a.TaskShow(context.Background(), TaskShowParams{Task: task.ID})
	if err != nil || !sameReplayTask(renamed, shown) {
		t.Fatalf("replay reverted rename: %+v %v", shown, err)
	}
}

func TestTaskUpdateReplayEmptyPatchSnapshotsOriginal(t *testing.T) {
	a := newAttributionAPI(t, "agent:seed")
	task := claimTestTask(t, a, "noop-snapshot")
	p := replayUpdateParams(task, "noop-key", "unused")
	p.Patch = TaskPatch{}
	first, err := a.TaskUpdate(context.Background(), p)
	if err != nil || !sameReplayTask(task, first) {
		t.Fatalf("empty patch changed task: %+v %v", first, err)
	}
	if n := updateReplayEvents(t, a, task); n != 0 {
		t.Fatalf("empty patch emitted %d events", n)
	}
	if _, err := a.TaskUpdate(context.Background(), replayUpdateParams(task, "after-noop", "later title")); err != nil {
		t.Fatal(err)
	}
	replayed, err := a.TaskUpdate(context.Background(), p)
	if err != nil || !sameReplayTask(first, replayed) {
		t.Fatalf("empty-patch replay did not retain snapshot: %+v %v", replayed, err)
	}
	if n := updateReplayEvents(t, a, task); n != 1 {
		t.Fatalf("empty replay emitted another event: %d", n)
	}
}

func TestTaskUpdateReplayEffectiveCallerCollision(t *testing.T) {
	a := newAttributionAPI(t, "agent:seed")
	task := claimTestTask(t, a, "caller-replay")
	p := replayUpdateParams(task, "caller-key", "first title")
	p.Actor, p.ScopeRef = "", ""
	ctx := attribution.WithCallerPrincipal(nodeContext("lab"), "agent:writer")
	ctx = attribution.WithCallerScope(ctx, "writer@wrkq")
	first, err := a.TaskUpdate(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	receipt := updateReplayReceipt(t, a, p.IdempotencyKey)
	for name, other := range map[string]context.Context{
		"principal": attribution.WithCallerScope(attribution.WithCallerPrincipal(nodeContext("lab"), "agent:other"), "other@wrkq"),
		"scope":     attribution.WithCallerScope(ctx, "writer@wrkq:"+task.ID),
		"node":      attribution.WithCallerScope(attribution.WithCallerPrincipal(nodeContext("other-node"), "agent:writer"), "writer@wrkq"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := a.TaskUpdate(other, p); err == nil || claimErrorCode(t, err) != CodeConflict {
				t.Fatalf("caller collision = %v", err)
			}
		})
	}
	replayed, err := a.TaskUpdate(ctx, p)
	if err != nil || !sameReplayTask(first, replayed) {
		t.Fatalf("original caller replay = %+v %v", replayed, err)
	}
	if receipt != updateReplayReceipt(t, a, p.IdempotencyKey) || updateReplayEvents(t, a, task) != 1 {
		t.Fatal("caller collisions changed receipt or task")
	}
}
