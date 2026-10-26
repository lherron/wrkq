//go:build wrkq_local

package wrkqapi

import (
	"context"
	"slices"
	"testing"
)

// Presentation: receipts HRC records when it drives an envelope, and the
// pendingView read (kicker wake set + stop-hook predicate) built on them.

// TestPresentationReceiptAndHistoryHintKeyedToRuntime proves §7's cue rule: the
// hint is keyed to the RUNTIME, not the generation. /quit clears continuation
// without rotating the generation, so a second runtime inside the SAME
// generation is cold and gets the cue; a warm runtime's second message does not.
func TestPresentationReceiptAndHistoryHintKeyedToRuntime(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	first := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "one", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	second := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "two", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	third := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "three", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})

	// Runtime A's first presentation in this room: the room already has prior
	// messages, so the cue fires.
	firstPresent, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: first.Envelopes[0].ID, PrincipalRef: "agent:hrc",
		Node: "mini", RuntimeID: "runtime-A", Generation: "49", RunID: "run-1",
	})
	if err != nil {
		t.Fatalf("present 1: %v", err)
	}
	if !firstPresent.HistoryHint {
		t.Fatal("a cold runtime's first presentation did not get the history cue")
	}

	// Same runtime, second message: warm, no cue.
	warm, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: second.Envelopes[0].ID, PrincipalRef: "agent:hrc",
		Node: "mini", RuntimeID: "runtime-A", Generation: "49", RunID: "run-2",
	})
	if err != nil {
		t.Fatalf("present 2: %v", err)
	}
	if warm.HistoryHint {
		t.Fatal("a warm runtime got the history cue on its second message")
	}

	// A NEW runtime inside the SAME generation (the post-/quit case) is cold.
	postQuit, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: third.Envelopes[0].ID, PrincipalRef: "agent:hrc",
		Node: "mini", RuntimeID: "runtime-B", Generation: "49", RunID: "run-3",
	})
	if err != nil {
		t.Fatalf("present 3: %v", err)
	}
	if !postQuit.HistoryHint {
		t.Fatal("a post-/quit runtime sharing its generation did not get the history cue")
	}

	// The receipt is durable and carries the HRC identifiers verbatim.
	shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: first.Envelopes[0].ID})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if len(shown.PresentedTo) != 1 {
		t.Fatalf("presented_to = %+v, want one receipt", shown.PresentedTo)
	}
	receipt := shown.PresentedTo[0]
	if receipt.Node == nil || *receipt.Node != "mini" ||
		receipt.RuntimeID == nil || *receipt.RuntimeID != "runtime-A" ||
		receipt.Generation == nil || *receipt.Generation != "49" {
		t.Fatalf("receipt lost HRC identifiers: %+v", receipt)
	}
}

// TestPresentationPreviewIsSideEffectFreeAndMatchesCommit proves the split
// presentation contract: preview computes the complete projection without
// touching the envelope row, presentation rows, event ledger, or fyi state.
func TestPresentationPreviewIsSideEffectFreeAndMatchesCommit(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	// Ensure the room has history worth cueing before the target fyi arrives.
	f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "earlier context", PrincipalRef: "agent:clod",
	})
	fyi := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "preview me", To: []string{"cody"}, FYI: true,
		PrincipalRef: "agent:clod",
	})
	envelopeUUID := fyi.Envelopes[0].UUID
	rowBefore := databaseRowsSnapshot(t, f.s.DB(), "SELECT * FROM envelopes WHERE uuid = ?", envelopeUUID)
	eventsBefore := databaseRowsSnapshot(t, f.s.DB(), "SELECT * FROM event_log WHERE resource_uuid = ? ORDER BY id", envelopeUUID)

	params := EnvelopePresentParams{
		Envelope: fyi.Envelopes[0].ID, Preview: true, PrincipalRef: "agent:hrc",
		RuntimeID: "runtime-preview", DriveAttemptID: "drive-preview", InputID: "ignored-preview-input",
	}
	preview, err := f.api.EnvelopePresent(ctx, params)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Recorded {
		t.Fatal("preview reported a durable presentation")
	}
	if preview.Envelope.State != "pending" || preview.Envelope.Terminal || len(preview.Envelope.PresentedTo) != 0 {
		t.Fatalf("preview mutated its result envelope: %+v", preview.Envelope)
	}
	if got := databaseRowsSnapshot(t, f.s.DB(), "SELECT * FROM envelopes WHERE uuid = ?", envelopeUUID); got != rowBefore {
		t.Fatalf("preview changed the envelope row:\nbefore %s\nafter  %s", rowBefore, got)
	}
	if got := databaseRowsSnapshot(t, f.s.DB(), "SELECT * FROM event_log WHERE resource_uuid = ? ORDER BY id", envelopeUUID); got != eventsBefore {
		t.Fatalf("preview changed the envelope event ledger:\nbefore %s\nafter  %s", eventsBefore, got)
	}
	var receiptCount int
	if err := f.s.DB().QueryRow("SELECT COUNT(*) FROM envelope_presentations WHERE envelope_uuid = ?", envelopeUUID).Scan(&receiptCount); err != nil {
		t.Fatalf("count preview receipts: %v", err)
	}
	if receiptCount != 0 {
		t.Fatalf("preview wrote %d presentation receipts", receiptCount)
	}

	params.Preview = false
	params.InputID = "input-accepted-1"
	committed, err := f.api.EnvelopePresent(ctx, params)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if !committed.Recorded || committed.Envelope.State != "acked" || !committed.Envelope.Terminal {
		t.Fatalf("committed fyi was not recorded and auto-acked: %+v", committed)
	}
	if preview.HistoryHint != committed.HistoryHint || preview.MessageCount != committed.MessageCount ||
		(preview.LastMessage == nil) != (committed.LastMessage == nil) ||
		(preview.LastMessage != nil && *preview.LastMessage != *committed.LastMessage) {
		t.Fatalf("preview projection differs from commit: preview=%+v commit=%+v", preview, committed)
	}
	if len(committed.Envelope.PresentedTo) != 1 || committed.Envelope.PresentedTo[0].InputID == nil ||
		*committed.Envelope.PresentedTo[0].InputID != "input-accepted-1" {
		t.Fatalf("commit receipt lost inputId: %+v", committed.Envelope.PresentedTo)
	}

	repeated, err := f.api.EnvelopePresent(ctx, params)
	if err != nil {
		t.Fatalf("repeat commit: %v", err)
	}
	if repeated.Recorded || len(repeated.Envelope.PresentedTo) != 1 {
		t.Fatalf("drive-attempt retry duplicated the receipt: %+v", repeated)
	}
}

func TestPresentationPreviewStillRequiresMemberRefWithoutAddressee(t *testing.T) {
	f := newRoomFixture(t)
	logEntry := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "unaddressed", PrincipalRef: "agent:clod",
	})
	_, err := f.api.EnvelopePresent(context.Background(), EnvelopePresentParams{
		Envelope: logEntry.Envelopes[0].ID, Preview: true, PrincipalRef: "agent:hrc",
	})
	_ = assertDomainCode(t, CodeValidation, err)
}

// TestPresentationIsExactlyOncePerDriveAttempt proves the at-least-once
// presentation residual is bounded: one driveAttemptId presents an envelope
// exactly once, however many times the kicker retries the call.
func TestPresentationIsExactlyOncePerDriveAttempt(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	ask := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "once", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	for attempt := 0; attempt < 3; attempt++ {
		result, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
			Envelope: ask.Envelopes[0].ID, PrincipalRef: "agent:hrc",
			RuntimeID: "rt-1", DriveAttemptID: "drive-1",
		})
		if err != nil {
			t.Fatalf("present attempt %d: %v", attempt, err)
		}
		if attempt > 0 && result.Recorded {
			t.Fatalf("attempt %d recorded a duplicate presentation for one drive attempt", attempt)
		}
	}
	shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: ask.Envelopes[0].ID})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if len(shown.PresentedTo) != 1 {
		t.Fatalf("one drive attempt wrote %d receipts", len(shown.PresentedTo))
	}
}

// TestPresentationReceiptKeepsInputIDAndDeliveryOutcomeVerbatimOrNull covers the
// receipt's opaque HRC fields: an inputId and a delivery class HRC supplies
// (even one wrkq has never heard of) ride through to show and to the inbox view
// unchanged, and a field it omits stays null rather than inventing a default.
// wrkq validates no vocabulary here, so HRC can add a class without a wrkq
// change.
func TestPresentationReceiptKeepsInputIDAndDeliveryOutcomeVerbatimOrNull(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	rows := []struct {
		name            string
		inputID         string
		deliveryOutcome string
	}{
		{name: "inputId and an unknown class kept verbatim", inputID: "in-1", deliveryOutcome: "future-harness-class"},
		{name: "known class without inputId", deliveryOutcome: "admitted_into_active_turn"},
		{name: "neither field supplied", inputID: "", deliveryOutcome: ""},
	}
	sameOptional := func(got *string, want string) bool {
		if want == "" {
			return got == nil
		}
		return got != nil && *got == want
	}

	envelopeIDs := make([]string, len(rows))
	for i, row := range rows {
		said := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: row.name,
			To: []string{"mable@proj:primary"}, PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary"})
		envelopeIDs[i] = said.Envelopes[0].ID
		if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
			Envelope: envelopeIDs[i], PrincipalRef: "agent:hrc", RuntimeID: "rt-1",
			InputID: row.inputID, DeliveryOutcome: row.deliveryOutcome,
		}); err != nil {
			t.Fatalf("%s: present: %v", row.name, err)
		}
	}

	inbox, err := f.api.EnvelopeInboxView(ctx, EnvelopeInboxViewParams{
		ScopeRef: "mable@proj:primary", PrincipalRef: "agent:mable",
	})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	inboxReceipts := map[string]WrkqEnvelopePresentation{}
	for _, group := range inbox.Groups {
		for _, envelope := range group.Items {
			if len(envelope.PresentedTo) == 1 {
				inboxReceipts[envelope.ID] = envelope.PresentedTo[0]
			}
		}
	}

	for i, row := range rows {
		shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: envelopeIDs[i]})
		if err != nil {
			t.Fatalf("%s: show: %v", row.name, err)
		}
		if len(shown.PresentedTo) != 1 {
			t.Fatalf("%s: show receipts = %+v, want one", row.name, shown.PresentedTo)
		}
		listed, ok := inboxReceipts[envelopeIDs[i]]
		if !ok {
			t.Fatalf("%s: envelope %s missing from the inbox view", row.name, envelopeIDs[i])
		}
		for surface, receipt := range map[string]WrkqEnvelopePresentation{"show": shown.PresentedTo[0], "inbox": listed} {
			if !sameOptional(receipt.InputID, row.inputID) {
				t.Fatalf("%s: %s inputId = %v (%q), want %q (empty = null)", row.name, surface, receipt.InputID, deref(receipt.InputID), row.inputID)
			}
			if !sameOptional(receipt.DeliveryOutcome, row.deliveryOutcome) {
				t.Fatalf("%s: %s deliveryOutcome = %v (%q), want %q (empty = null)", row.name, surface, receipt.DeliveryOutcome, deref(receipt.DeliveryOutcome), row.deliveryOutcome)
			}
		}
	}
}

// TestPendingViewIncludeFyiIsOptInAndNeverBlocks proves T-07627: the default
// read stays the obligation-only wake set, includeFyi additionally surfaces
// pending fyi envelopes as ITEMS, and only a PRESENTED obligation enters the
// stop-hook predicate (a pending one or a fyi never does). The presentation half is pinned too: presenting a fyi auto-acks
// it, so it leaves the includeFyi read on its own.
func TestPendingViewIncludeFyiIsOptInAndNeverBlocks(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	codySeat := "cody@proj:" + f.loneTaskID

	ask := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "please reply", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	fyi := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "heads up", To: []string{"cody"}, FYI: true, PrincipalRef: "agent:clod",
	})

	// Default: fyi is invisible, exactly as the wake set requires.
	base := f.pendingView(t, false, codySeat)
	if len(base.Items) != 1 || base.Items[0].ID != ask.Envelopes[0].ID {
		t.Fatalf("default pendingView = %+v, want only the obligation", base.Items)
	}
	// The stop-hook predicate is narrower than the wake set: an obligation that
	// was never presented wakes the kicker but does not refuse a turn end.
	if len(base.Blocking) != 0 {
		t.Fatalf("a pending, never-presented obligation blocked a turn end: %v", base.Blocking)
	}

	// Opt-in: the fyi joins items and stays out of blocking.
	withFyi := f.pendingView(t, true, codySeat)
	ids := pendingItems(withFyi)
	if len(withFyi.Items) != 2 || ids[ask.Envelopes[0].ID] != "reply_required" || ids[fyi.Envelopes[0].ID] != "fyi" {
		t.Fatalf("includeFyi items = %+v, want the obligation and the fyi", withFyi.Items)
	}
	if slices.Contains(withFyi.Blocking, fyi.Envelopes[0].ID) {
		t.Fatal("a fyi entered the stop-hook predicate; fyi never blocks a turn end")
	}

	// Presenting the obligation blocks; presenting the fyi auto-acks it, which
	// is what retires it from the includeFyi read without any ack call.
	f.present(t, ask.Envelopes[0].ID)
	presentedFyi, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: fyi.Envelopes[0].ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-1",
	})
	if err != nil {
		t.Fatalf("present fyi: %v", err)
	}
	if presentedFyi.Envelope.State != "acked" || !presentedFyi.Envelope.Terminal {
		t.Fatalf("presented fyi = %s (terminal=%v), want an auto-ack", presentedFyi.Envelope.State, presentedFyi.Envelope.Terminal)
	}

	after := f.pendingView(t, true, codySeat)
	if len(after.Items) != 1 || after.Items[0].ID != ask.Envelopes[0].ID {
		t.Fatalf("an auto-acked fyi survived the includeFyi read: %+v", after.Items)
	}
	if len(after.Blocking) != 1 || after.Blocking[0] != ask.Envelopes[0].ID {
		t.Fatalf("blocking = %v, want only the presented obligation", after.Blocking)
	}
}
