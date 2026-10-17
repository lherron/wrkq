//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"testing"
)

// T-11305: a note is addressed mail below fyi. It routes like any addressed
// say, is created terminal (acked, reason note_recorded), and so never enters
// the wake set, the birth domain, presentation, or the 24h undeliverable rule.
// Reply is the ack, exactly as for fyi.

func TestNoteIsCreatedTerminalAndNeverPresentable(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	codySeat := "cody@proj:" + f.loneTaskID

	note := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "nightly queue empty", To: []string{"cody"}, Note: true, PrincipalRef: "agent:clod",
	})
	if len(note.Envelopes) != 1 {
		t.Fatalf("note wrote %d envelopes, want 1", len(note.Envelopes))
	}
	envelope := note.Envelopes[0]
	if envelope.Obligation != "note" || envelope.State != "acked" || !envelope.Terminal {
		t.Fatalf("note = %s/%s terminal=%v, want note/acked terminal", envelope.Obligation, envelope.State, envelope.Terminal)
	}
	if envelope.To == nil || envelope.To.ScopeRef == nil || *envelope.To.ScopeRef != codySeat {
		t.Fatalf("note addressee = %+v, want %s", envelope.To, codySeat)
	}
	var acked map[string]any
	if err := json.Unmarshal([]byte(f.lastEventPayload(t, envelope.UUID, "envelope.acked")), &acked); err != nil {
		t.Fatal(err)
	}
	if acked["reason"] != "note_recorded" || acked["state"] != "acked" {
		t.Fatalf("envelope.acked payload = %v, want reason note_recorded", acked)
	}

	// Routed like any addressed say: the room log shows it with its obligation.
	log, err := f.api.RoomLogView(ctx, RoomLogViewParams{Room: note.Room.Key})
	if err != nil {
		t.Fatalf("room log: %v", err)
	}
	if len(log.Items) != 1 || log.Items[0].ID != envelope.ID || log.Items[0].Obligation != "note" {
		t.Fatalf("room log = %+v, want the note", log.Items)
	}

	// Never in the wake set, even with the fyi opt-in, and never a birth.
	if view := f.pendingView(t, true, codySeat); len(view.Items) != 0 || len(view.Blocking) != 0 {
		t.Fatalf("pendingView includeFyi = %+v, want empty", view)
	}
	birth, err := f.api.EnvelopeBirthEnvelope(ctx, EnvelopeBirthEnvelopeParams{ScopeRef: codySeat})
	if err != nil || birth != nil {
		t.Fatalf("birthEnvelope = %+v, %v; a note never births a seat", birth, err)
	}

	// Presentation refuses a terminal row and records no receipt.
	_, err = f.api.EnvelopePresent(ctx, EnvelopePresentParams{Envelope: envelope.ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-1"})
	if err == nil {
		t.Fatal("presenting a note succeeded; a note is never presented")
	}
	shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: envelope.ID})
	if err != nil {
		t.Fatalf("show note: %v", err)
	}
	if shown.State != "acked" || len(shown.PresentedTo) != 0 {
		t.Fatalf("note after a present attempt = %s with %d receipts", shown.State, len(shown.PresentedTo))
	}

	// Never part of the 24h undeliverable rule.
	f.ageEnvelope(t, envelope.ID, "-90000 seconds")
	f.observe(t)
	if state, reason, _ := f.envelopeRow(t, envelope.ID); state != "acked" || reason != "" {
		t.Fatalf("aged note = %s/%s, want acked with no failure", state, reason)
	}
	if n := f.envelopeEventCount(t, envelope.ID, "envelope.failed"); n != 0 {
		t.Fatalf("aged note emitted %d envelope.failed events", n)
	}
}

func TestNoteFlagRulesRefuseConflictsAndAMissingAddressee(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	for name, params := range map[string]RoomSayParams{
		"without --to": {Ref: f.loneTaskID, Body: "x", Note: true},
		"with --fyi":   {Ref: f.loneTaskID, Body: "x", To: []string{"cody"}, Note: true, FYI: true},
		"with hold":    {Ref: f.loneTaskID, Body: "x", To: []string{"cody"}, Note: true, Hold: true},
		"with ttl":     {Ref: f.loneTaskID, Body: "x", To: []string{"cody"}, Note: true, TTL: "30s"},
	} {
		params.PrincipalRef = "agent:clod"
		if _, err := f.api.RoomSay(ctx, params); err == nil {
			t.Fatalf("note %s was accepted", name)
		} else {
			_ = assertDomainCode(t, CodeValidation, err)
		}
	}
	var count int
	if err := f.s.DB().QueryRow("SELECT COUNT(*) FROM envelopes").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("refused notes wrote %d envelopes", count)
	}
}

func TestNoteReplyDischargesTheSendersObligations(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	codySeat := "cody@proj:" + f.loneTaskID
	clodSeat := "clod@proj:" + f.loneTaskID

	// Reply is the ack: a note back to the asker discharges its pending and
	// presented obligations from that counterparty.
	pending := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "one", To: []string{"cody"}, PrincipalRef: "agent:clod", ScopeRef: clodSeat})
	presented := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "two", To: []string{"cody"}, PrincipalRef: "agent:clod", ScopeRef: clodSeat})
	f.present(t, presented.Envelopes[0].ID)
	reply := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "done", To: []string{clodSeat}, Note: true, PrincipalRef: "agent:cody", ScopeRef: codySeat})
	got := ackedSet(reply.Acked)
	if len(reply.Acked) != 2 || !got[pending.Envelopes[0].ID] || !got[presented.Envelopes[0].ID] {
		t.Fatalf("note reply acked %v, want both obligations", reply.Acked)
	}
	if reply.Envelopes[0].State != "acked" || reply.Envelopes[0].Obligation != "note" {
		t.Fatalf("reply = %s/%s", reply.Envelopes[0].Obligation, reply.Envelopes[0].State)
	}
	// The sender gets nothing to wake on from the reply.
	if view := f.pendingView(t, true, clodSeat); len(view.Items) != 0 {
		t.Fatalf("the note reply entered the original sender's wake set: %+v", view.Items)
	}

	// --discharges works with --note, and validates exactly as for any reply.
	ask := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "three", To: []string{"cody"}, PrincipalRef: "agent:clod", ScopeRef: clodSeat})
	f.present(t, ask.Envelopes[0].ID)
	if _, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{
		Envelope: ask.Envelopes[0].ID, Reason: "later", PrincipalRef: "agent:cody", ScopeRef: codySeat,
	}); err != nil {
		t.Fatalf("defer: %v", err)
	}
	scoped := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "finished", To: []string{clodSeat}, Note: true,
		DischargeEnvelopeIDs: []string{ask.Envelopes[0].ID}, PrincipalRef: "agent:cody", ScopeRef: codySeat,
	})
	if len(scoped.Acked) != 1 || scoped.Acked[0] != ask.Envelopes[0].ID {
		t.Fatalf("note --discharges acked %v", scoped.Acked)
	}
	f.assertEnvelopeStates(t, map[string]string{ask.Envelopes[0].ID: "acked", scoped.Envelopes[0].ID: "acked"})
	_, err := f.api.RoomSay(ctx, RoomSayParams{
		Ref: f.loneTaskID, Body: "again", To: []string{clodSeat}, Note: true,
		DischargeEnvelopeIDs: []string{ask.Envelopes[0].ID}, PrincipalRef: "agent:cody", ScopeRef: codySeat,
	})
	_ = assertDomainCode(t, CodeValidation, err)
}

func TestInboxCountsRecentNotesAndListsThemNewestFirst(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	codySeat := "cody@proj:" + f.loneTaskID

	old := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "old", To: []string{"cody"}, Note: true, PrincipalRef: "agent:clod"}).Envelopes[0].ID
	first := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "first", To: []string{"cody"}, Note: true, PrincipalRef: "agent:clod"}).Envelopes[0].ID
	second := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "second", To: []string{"cody"}, Note: true, PrincipalRef: "agent:mable"}).Envelopes[0].ID
	f.ageEnvelope(t, old, "-90000 seconds")
	f.ageEnvelope(t, first, "-60 seconds")
	// Neither another seat's note, an fyi, nor an obligation is a note of cody's.
	f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "for mable", To: []string{"mable"}, Note: true, PrincipalRef: "agent:clod"})
	f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "fyi", To: []string{"cody"}, FYI: true, PrincipalRef: "agent:clod"})
	ask := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "ask", To: []string{"cody"}, PrincipalRef: "agent:clod"}).Envelopes[0].ID

	inbox, err := f.api.EnvelopeInboxView(ctx, EnvelopeInboxViewParams{ScopeRef: codySeat, PrincipalRef: "agent:cody"})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if inbox.RecentNotes != 2 {
		t.Fatalf("recentNotes = %d, want 2 (24h window)", inbox.RecentNotes)
	}
	if inbox.Notes != nil {
		t.Fatalf("notes listed without includeNotes: %+v", inbox.Notes)
	}
	if len(inbox.Groups) != 1 || len(inbox.Groups[0].Items) != 1 || inbox.Groups[0].Items[0].ID != ask {
		t.Fatalf("inbox obligations = %+v, want only the ask", inbox.Groups)
	}

	listed, err := f.api.EnvelopeInboxView(ctx, EnvelopeInboxViewParams{ScopeRef: codySeat, IncludeNotes: true, PrincipalRef: "agent:cody"})
	if err != nil {
		t.Fatalf("inbox includeNotes: %v", err)
	}
	if ids := envelopeIDs(listed.Notes); len(ids) != 2 || ids[0] != second || ids[1] != first {
		t.Fatalf("notes = %v, want [%s %s] newest first", ids, second, first)
	}

	wider, err := f.api.EnvelopeInboxView(ctx, EnvelopeInboxViewParams{ScopeRef: codySeat, IncludeNotes: true, NotesSince: "2d", PrincipalRef: "agent:cody"})
	if err != nil {
		t.Fatalf("inbox notesSince 2d: %v", err)
	}
	if ids := envelopeIDs(wider.Notes); len(ids) != 3 || ids[2] != old {
		t.Fatalf("notes since 2d = %v, want three ending with %s", ids, old)
	}
	if wider.RecentNotes != 2 {
		t.Fatalf("recentNotes moved with notesSince: %d", wider.RecentNotes)
	}

	if _, err := f.api.EnvelopeInboxView(ctx, EnvelopeInboxViewParams{ScopeRef: codySeat, IncludeNotes: true, NotesSince: "yesterday-ish"}); err == nil {
		t.Fatal("a malformed notesSince was accepted")
	} else {
		_ = assertDomainCode(t, CodeValidation, err)
	}
}

func envelopeIDs(envelopes []WrkqEnvelope) []string {
	ids := make([]string, 0, len(envelopes))
	for _, envelope := range envelopes {
		ids = append(ids, envelope.ID)
	}
	return ids
}
