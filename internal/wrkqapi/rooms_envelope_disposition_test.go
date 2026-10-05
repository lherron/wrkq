//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Envelope disposition by the addressee or its runtime: defer (paused, never
// terminal), explicit discharge, fail, and the sender's failure listing.

// TestDeferWithRetryIsPromiseBackedAndRepends proves §6's defer contract: the
// retry time is carried by a real wrkq promise, and when it comes due the
// envelope returns to PENDING so the kicker's next sweep re-drives it. Deferred
// is paused, never terminal.
func TestDeferWithRetryIsPromiseBackedAndRepends(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	codySeat := "cody@proj:" + f.loneTaskID

	ask := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "when you can", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	f.present(t, ask.Envelopes[0].ID)

	deferred, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{
		Envelope: ask.Envelopes[0].ID, Reason: "after the build", RetryAfter: "2h",
		PrincipalRef: "agent:cody", ScopeRef: codySeat,
	})
	if err != nil {
		t.Fatalf("defer: %v", err)
	}
	if deferred.State != "deferred" || deferred.Terminal {
		t.Fatalf("deferred envelope = %s (terminal=%v); defer is PAUSED, never terminal", deferred.State, deferred.Terminal)
	}
	if deferred.RetryPromiseID == nil {
		t.Fatal("--retry-after did not arm a wrkq promise")
	}
	promise, err := f.api.PromiseShow(ctx, PromiseShowParams{Promise: *deferred.RetryPromiseID})
	if err != nil {
		t.Fatalf("show retry promise: %v", err)
	}
	if promise.OwnerPrincipalRef != "agent:cody" {
		t.Fatalf("retry promise owner = %q, want the deferring principal", promise.OwnerPrincipalRef)
	}

	// It is not due yet, so a sweep leaves it alone.
	pending := f.pendingView(t, false, codySeat)
	if pending.Repended != 0 || len(pending.Items) != 0 {
		t.Fatalf("an undue deferral was re-pended: %+v", pending)
	}

	// Bring the retry time forward and sweep again.
	if _, err := f.s.DB().Exec(
		"UPDATE envelopes SET retry_at = '2000-01-01T00:00:00Z' WHERE id = ?", ask.Envelopes[0].ID,
	); err != nil {
		t.Fatalf("age the retry: %v", err)
	}
	swept := f.pendingView(t, false, codySeat)
	if swept.Repended != 1 {
		t.Fatalf("due deferral was not re-pended: %+v", swept)
	}
	if len(swept.Items) != 1 || swept.Items[0].State != "pending" {
		t.Fatalf("re-pended envelope = %+v, want one pending item", swept.Items)
	}
	if len(swept.Items[0].PresentedTo) != 1 || swept.Items[0].PresentedTo[0].RuntimeID == nil ||
		*swept.Items[0].PresentedTo[0].RuntimeID != "rt-1" {
		t.Fatalf("defer retry cleared presented_to: %+v", swept.Items[0].PresentedTo)
	}
	// A re-pended envelope is not yet presented, so it does not block a turn end.
	if len(swept.Blocking) != 0 {
		t.Fatalf("a re-pended (unpresented) envelope blocks the stop hook: %v", swept.Blocking)
	}
	// The promise that carried the deferral is discharged.
	reloaded, err := f.api.PromiseShow(ctx, PromiseShowParams{Promise: *deferred.RetryPromiseID})
	if err != nil {
		t.Fatalf("reload promise: %v", err)
	}
	if reloaded.State != "resolved" {
		t.Fatalf("retry promise state = %s, want resolved", reloaded.State)
	}
}

// TestDeferredEnvelopeStillAckableByALaterReply proves the other half of "paused,
// never terminal": deferred → acked by a later reply is legal.
func TestDeferredEnvelopeStillAckableByALaterReply(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	codySeat := "cody@proj:" + f.loneTaskID

	ask := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "eventually", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	f.present(t, ask.Envelopes[0].ID)
	if _, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{
		Envelope: ask.Envelopes[0].ID, Reason: "later", PrincipalRef: "agent:cody", ScopeRef: codySeat,
	}); err != nil {
		t.Fatalf("defer: %v", err)
	}
	// Re-present (the kicker's sweep would) and reply.
	if _, err := f.s.DB().Exec("UPDATE envelopes SET state = 'presented' WHERE id = ?", ask.Envelopes[0].ID); err != nil {
		t.Fatalf("re-present: %v", err)
	}
	reply := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "here you go", To: []string{"clod"},
		PrincipalRef: "agent:cody", ScopeRef: codySeat,
	})
	if len(reply.Acked) != 1 || reply.Acked[0] != ask.Envelopes[0].ID {
		t.Fatalf("a previously deferred envelope was not acked by the later reply: %v", reply.Acked)
	}
}

// TestExplicitDischargeAcksADeferredEnvelopeAndResolvesItsRetry proves the
// addressee can always finish a deferred obligation themselves: naming it in an
// explicit discharge set acks it with no re-presentation, and the retry promise
// that carried the deferral closes with it. A plain reply still leaves it
// deferred, so `defer` keeps excluding one obligation from a reply.
func TestExplicitDischargeAcksADeferredEnvelopeAndResolvesItsRetry(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	codySeat := "cody@proj:" + f.loneTaskID

	ask := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "eventually", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	envelopeID := ask.Envelopes[0].ID
	f.present(t, envelopeID)
	deferred, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{
		Envelope: envelopeID, Reason: "after the build", RetryAfter: "2h",
		PrincipalRef: "agent:cody", ScopeRef: codySeat,
	})
	if err != nil || deferred.RetryPromiseID == nil {
		t.Fatalf("defer: %+v, %v", deferred, err)
	}

	plain := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "unrelated", To: []string{"clod"}, FYI: true,
		PrincipalRef: "agent:cody", ScopeRef: codySeat,
	})
	if len(plain.Acked) != 0 {
		t.Fatalf("a plain reply acked a deferred envelope: %v", plain.Acked)
	}

	reply := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "done after all", To: []string{"clod"}, FYI: true,
		DischargeEnvelopeIDs: []string{envelopeID}, PrincipalRef: "agent:cody", ScopeRef: codySeat,
	})
	if len(reply.Acked) != 1 || reply.Acked[0] != envelopeID {
		t.Fatalf("explicit discharge of a deferred envelope acked %v", reply.Acked)
	}
	shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: envelopeID, PrincipalRef: "agent:cody"})
	if err != nil || shown.State != "acked" {
		t.Fatalf("discharged envelope = %+v, %v", shown, err)
	}
	promise, err := f.api.PromiseShow(ctx, PromiseShowParams{Promise: *deferred.RetryPromiseID})
	if err != nil {
		t.Fatalf("show retry promise: %v", err)
	}
	if promise.State != "resolved" {
		t.Fatalf("retry promise state = %s after its envelope was acked, want resolved", promise.State)
	}
}

// TestEnvelopeDispositionRequiresTheAddressee carries T-06810's hygiene rule:
// the envelope's target must equal the claimed scope, so a typo cannot dispose
// somebody else's obligation.
func TestEnvelopeDispositionRequiresTheAddressee(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	ask := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "for cody", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	_, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{
		Envelope: ask.Envelopes[0].ID, Reason: "not mine to defer",
		PrincipalRef: "agent:mable", ScopeRef: "mable@proj:" + f.loneTaskID,
	})
	_ = assertDomainCode(t, CodeForbidden, err)
}

// A presented envelope whose queued copy the broker expired before injection is
// failed `undeliverable` by the runtime holding its newest receipt (T-07891
// amendment 6). The body never reached a reader, so the D7 reason is truthful;
// an acked envelope is still refused.
func TestEnvelopeFailUndeliverableAdmitsPresented(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	ask := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "queued then expired", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	envelopeID := ask.Envelopes[0].ID
	f.present(t, envelopeID)
	failed, err := f.api.EnvelopeFail(ctx, EnvelopeFailParams{
		Envelope: envelopeID, Reason: "undeliverable", Runtime: "rt-1", PrincipalRef: "agent:hrc",
	})
	if err != nil {
		t.Fatalf("fail presented as undeliverable: %v", err)
	}
	if failed.State != "failed" || !failed.Terminal || failed.FailureReason == nil || *failed.FailureReason != "undeliverable" {
		t.Fatalf("failed envelope = %+v", failed)
	}

	acked := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "read and answered", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	ackedID := acked.Envelopes[0].ID
	f.present(t, ackedID)
	f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "reply", To: []string{"clod"},
		PrincipalRef: "agent:cody", ScopeRef: *acked.Envelopes[0].To.ScopeRef,
	})
	if _, err := f.api.EnvelopeFail(ctx, EnvelopeFailParams{
		Envelope: ackedID, Reason: "undeliverable", Runtime: "rt-1", PrincipalRef: "agent:hrc",
	}); err == nil {
		t.Fatal("undeliverable on an acked envelope succeeded")
	}
}

// envelope.fail's optional detail rides the envelope.failed event, bounded by
// truncation; omitting it leaves the payload as before (T-09657).
func TestEnvelopeFailCarriesOptionalBoundedDetail(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	failedPayload := func(detail string) string {
		t.Helper()
		ask := f.say(t, RoomSayParams{
			Ref: f.loneTaskID, Body: "never born", To: []string{"cody"}, PrincipalRef: "agent:clod",
		})
		failed, err := f.api.EnvelopeFail(ctx, EnvelopeFailParams{
			Envelope: ask.Envelopes[0].ID, Reason: "undeliverable", Detail: detail, PrincipalRef: "agent:hrc",
		})
		if err != nil {
			t.Fatalf("fail: %v", err)
		}
		return f.lastEventPayload(t, failed.UUID, "envelope.failed")
	}

	if p := failedPayload(""); strings.Contains(p, `"detail"`) {
		t.Fatalf("detail-less fail wrote detail: %s", p)
	}
	if p := failedPayload("HRC refused: worktree branch does not carry T-1"); !strings.Contains(p, `"detail":"HRC refused: worktree branch does not carry T-1"`) {
		t.Fatalf("detail missing from payload: %s", p)
	}
	long := failedPayload(strings.Repeat("é", 3000))
	var decoded struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal([]byte(long), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Detail) == 0 || len(decoded.Detail) > 2048 || !utf8.ValidString(decoded.Detail) {
		t.Fatalf("long detail not truncated safely: %d bytes valid=%v", len(decoded.Detail), utf8.ValidString(decoded.Detail))
	}
}

// TestEnvelopeFailIsTerminalVisibleAndIdempotentPerRuntime pins rev 5.1's
// unsuccessful terminal transition, including its rejection rules.
func TestEnvelopeFailIsTerminalVisibleAndIdempotentPerRuntime(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	ask := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "answer me", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	envelopeID := ask.Envelopes[0].ID

	f.present(t, envelopeID)
	if _, err := f.api.EnvelopeFail(ctx, EnvelopeFailParams{
		Envelope: envelopeID, Reason: "runtime_terminated", Runtime: "rt-other", PrincipalRef: "agent:hrc",
	}); err == nil {
		t.Fatal("fail from a runtime that does not own the newest receipt succeeded")
	}
	failed, err := f.api.EnvelopeFail(ctx, EnvelopeFailParams{
		Envelope: envelopeID, Reason: "runtime_terminated", Runtime: "rt-1", PrincipalRef: "agent:hrc",
	})
	if err != nil {
		t.Fatalf("fail: %v", err)
	}
	if failed.State != "failed" || !failed.Terminal || failed.FailureReason == nil || *failed.FailureReason != "runtime_terminated" {
		t.Fatalf("failed envelope = %+v", failed)
	}
	etag := failed.ETag
	repeat, err := f.api.EnvelopeFail(ctx, EnvelopeFailParams{
		Envelope: envelopeID, Reason: "runtime_terminated", Runtime: "rt-1", PrincipalRef: "agent:hrc",
	})
	if err != nil {
		t.Fatalf("repeat fail: %v", err)
	}
	if repeat.ETag != etag {
		t.Fatalf("idempotent fail advanced etag: %d -> %d", etag, repeat.ETag)
	}
	var failureEvents int
	var failurePayload string
	if err := f.s.DB().QueryRow(`SELECT COUNT(*), COALESCE(MAX(payload), '') FROM event_log
		WHERE resource_uuid = ? AND event_type = 'envelope.failed'`, failed.UUID).Scan(&failureEvents, &failurePayload); err != nil {
		t.Fatalf("read failure event: %v", err)
	}
	if failureEvents != 1 || !strings.Contains(failurePayload, `"reason":"runtime_terminated"`) ||
		!strings.Contains(failurePayload, `"runtime_id":"rt-1"`) {
		t.Fatalf("failure events = %d payload %s", failureEvents, failurePayload)
	}
	if _, err := f.api.EnvelopeAck(ctx, EnvelopeAckParams{Envelopes: []string{envelopeID}, PrincipalRef: "agent:lance"}); err == nil {
		t.Fatal("ack overwrote a failed envelope")
	}

	// Failed is terminal and visible on both recipient and sender sides.
	inbox, err := f.api.EnvelopeInboxView(ctx, EnvelopeInboxViewParams{
		PrincipalRef: "agent:cody", ScopeRef: "cody@proj:" + f.loneTaskID, IncludeFailed: true,
	})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if len(inbox.Failed) != 1 || inbox.Failed[0].ID != envelopeID {
		t.Fatalf("failed envelope is not visible: %+v", inbox.Failed)
	}
	if len(inbox.Groups) != 0 {
		t.Fatalf("a failed envelope still stands as an obligation: %+v", inbox.Groups)
	}
	sender, err := f.api.EnvelopeInboxView(ctx, EnvelopeInboxViewParams{
		PrincipalRef: "agent:clod",
	})
	if err != nil {
		t.Fatalf("sender inbox: %v", err)
	}
	if len(sender.SentFailed) != 1 || sender.SentFailed[0].ID != envelopeID {
		t.Fatalf("sent failure is not visible: %+v", sender.SentFailed)
	}
}

// TestInboxSentFailedListsOnlyLiveFailures is T-09880's visibility rule for the
// sender's failure queue. A failed fyi is never listed: it carries no
// obligation, and undeliverable-to-an-ended-seat is its designed outcome. A
// failed reply_required stays listed while its task is live and drops out once
// the task is terminal, even when the room it was said in (a campaign room)
// is still open. Records stay in the ledger; only the listing changes.
func TestInboxSentFailedListsOnlyLiveFailures(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	fail := func(envelopeID string) {
		t.Helper()
		f.present(t, envelopeID)
		if _, err := f.api.EnvelopeFail(ctx, EnvelopeFailParams{
			Envelope: envelopeID, Reason: "runtime_terminated", Runtime: "rt-1", PrincipalRef: "agent:hrc",
		}); err != nil {
			t.Fatalf("fail %s: %v", envelopeID, err)
		}
	}
	sentFailed := func() []string {
		t.Helper()
		view, err := f.api.EnvelopeInboxView(ctx, EnvelopeInboxViewParams{PrincipalRef: "agent:clod"})
		if err != nil {
			t.Fatalf("sender inbox: %v", err)
		}
		ids := []string{}
		for _, envelope := range view.SentFailed {
			ids = append(ids, envelope.ID)
		}
		return ids
	}

	// A reply_required on the enrolled member task, said into the campaign room.
	ask := f.say(t, RoomSayParams{
		Ref: f.memberTaskID, Body: "answer me", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	askID := ask.Envelopes[0].ID
	fail(askID)

	// A fyi that failed the same way.
	note := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "heads up", To: []string{"cody"}, FYI: true, PrincipalRef: "agent:clod",
	})
	noteID := note.Envelopes[0].ID
	if _, err := f.s.DB().Exec(`UPDATE envelopes SET state = 'failed', failure_reason = 'undeliverable' WHERE id = ?`, noteID); err != nil {
		t.Fatalf("fail fyi: %v", err)
	}

	if got := sentFailed(); len(got) != 1 || got[0] != askID {
		t.Fatalf("sentFailed = %v, want only the live reply_required %s (never the fyi %s)", got, askID, noteID)
	}

	f.completeTask(t, f.memberTaskID)
	if got := roomActivity(t, f, f.campaignPath); got.Work == "terminal" {
		t.Fatalf("fixture: completing one member made the campaign room terminal")
	}
	if got := sentFailed(); len(got) != 0 {
		t.Fatalf("sentFailed = %v after its task completed, want none", got)
	}

	// A taskless reply_required (a pair room) is listed inside the window and
	// drops out once it failed longer ago than the window.
	dm := f.say(t, RoomSayParams{
		Ref: "cody@proj:primary", Body: "dm ask", PrincipalRef: "agent:clod",
	})
	dmID := dm.Envelopes[0].ID
	if dm.Envelopes[0].TaskID != nil {
		t.Fatalf("fixture: the pair-room envelope carries task %s", *dm.Envelopes[0].TaskID)
	}
	fail(dmID)
	if got := sentFailed(); len(got) != 1 || got[0] != dmID {
		t.Fatalf("sentFailed = %v, want the fresh taskless failure %s", got, dmID)
	}
	saved := sentFailureWindow
	sentFailureWindow = time.Hour
	t.Cleanup(func() { sentFailureWindow = saved })
	stamp := time.Now().UTC().Add(-2 * time.Hour).Format("2006-01-02T15:04:05Z")
	if _, err := f.s.DB().Exec(`UPDATE envelopes SET updated_at = ? WHERE id = ?`, stamp, dmID); err != nil {
		t.Fatalf("age failure: %v", err)
	}
	if got := sentFailed(); len(got) != 0 {
		t.Fatalf("sentFailed = %v after the window, want none", got)
	}

	// Still in the ledger: show reads every one.
	for _, id := range []string{askID, noteID, dmID} {
		shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: id, PrincipalRef: "agent:clod"})
		if err != nil || shown.State != "failed" {
			t.Fatalf("show %s after the listing dropped it: %+v %v", id, shown, err)
		}
	}
}
