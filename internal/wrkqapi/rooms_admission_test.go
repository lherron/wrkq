//go:build wrkq_local

package wrkqapi

import (
	"context"
	"testing"
)

func TestEnvelopeTTLExpiryMaterializesFromPendingAndDeferredWithExactObserver(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	codySeat := "cody@proj:" + f.loneTaskID

	pending := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "pending ttl", To: []string{"cody"}, TTL: "1h", PrincipalRef: "agent:clod"})
	deferred := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "deferred ttl", To: []string{"cody"}, TTL: "1h", PrincipalRef: "agent:clod"})
	if _, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{Envelope: deferred.Envelopes[0].ID, Reason: "later", PrincipalRef: "agent:cody", ScopeRef: codySeat}); err != nil {
		t.Fatalf("defer: %v", err)
	}
	if _, err := f.s.DB().Exec("UPDATE envelopes SET expires_at = '2000-01-01T00:00:00Z' WHERE id IN (?, ?)", pending.Envelopes[0].ID, deferred.Envelopes[0].ID); err != nil {
		t.Fatalf("age ttl: %v", err)
	}
	view, err := f.api.MonitorStateView(ctx, MonitorStateViewParams{Tasks: []string{pending.GroupID}, Condition: "terminal", PrincipalRef: "agent:cody", ScopeRef: codySeat})
	if err != nil || !view.Met {
		t.Fatalf("monitor materialization = %+v, %v", view, err)
	}
	for _, id := range []string{pending.Envelopes[0].ID, deferred.Envelopes[0].ID} {
		var state, actor, updatedBy string
		if err := f.s.DB().QueryRow("SELECT state, terminal_actor, updated_by_principal_ref FROM envelopes WHERE id = ?", id).Scan(&state, &actor, &updatedBy); err != nil {
			t.Fatal(err)
		}
		if state != "expired" || actor != "wrkq" || updatedBy != "agent:cody" {
			t.Fatalf("%s = %s actor=%s updatedBy=%s", id, state, actor, updatedBy)
		}
		var events int
		if err := f.s.DB().QueryRow("SELECT COUNT(*) FROM event_log WHERE resource_uuid = (SELECT uuid FROM envelopes WHERE id = ?) AND event_type = 'envelope.expired'", id).Scan(&events); err != nil {
			t.Fatal(err)
		}
		if events != 1 {
			t.Fatalf("%s expiry events = %d", id, events)
		}
	}
	if _, err := f.api.EnvelopeInboxView(ctx, EnvelopeInboxViewParams{PrincipalRef: "agent:cody", ScopeRef: codySeat}); err != nil {
		t.Fatal(err)
	}
	var expiryEvents int
	if err := f.s.DB().QueryRow("SELECT COUNT(*) FROM event_log WHERE event_type = 'envelope.expired' AND resource_uuid IN (?, ?)", pending.Envelopes[0].UUID, deferred.Envelopes[0].UUID).Scan(&expiryEvents); err != nil {
		t.Fatal(err)
	}
	if expiryEvents != 2 {
		t.Fatalf("repeat observation duplicated expiry events: %d", expiryEvents)
	}
}

func TestEnvelopeTTLDoesNotExpireAfterPresentationAndPresentGuardsTerminal(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	said := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "present first", To: []string{"cody"}, TTL: "1h", PrincipalRef: "agent:clod"})
	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{Envelope: said.Envelopes[0].ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-1", DriveAttemptID: "drive-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DB().Exec("UPDATE envelopes SET expires_at = '2000-01-01T00:00:00Z' WHERE id = ?", said.Envelopes[0].ID); err != nil {
		t.Fatal(err)
	}
	shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: said.Envelopes[0].ID, PrincipalRef: "agent:cody"})
	if err != nil || shown.State != "presented" {
		t.Fatalf("shown = %+v, %v", shown, err)
	}

	withdrawn := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "withdraw me", To: []string{"cody"}, PrincipalRef: "agent:clod"})
	if _, err := f.api.EnvelopeWithdraw(ctx, EnvelopeWithdrawParams{Envelope: withdrawn.Envelopes[0].ID, PrincipalRef: "agent:clod"}); err != nil {
		t.Fatal(err)
	}
	_, err = f.api.EnvelopePresent(ctx, EnvelopePresentParams{Envelope: withdrawn.Envelopes[0].ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-2", DriveAttemptID: "drive-2"})
	_ = assertDomainCode(t, CodeWrongState, err)
	var receipts int
	if err := f.s.DB().QueryRow("SELECT COUNT(*) FROM envelope_presentations WHERE envelope_uuid = ?", withdrawn.Envelopes[0].UUID).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 0 {
		t.Fatalf("withdrawn envelope receipts = %d", receipts)
	}

	due := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "already due", To: []string{"cody"}, TTL: "1h", PrincipalRef: "agent:clod"})
	if _, err := f.s.DB().Exec("UPDATE envelopes SET expires_at = '2000-01-01T00:00:00Z' WHERE id = ?", due.Envelopes[0].ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.api.EnvelopePresent(ctx, EnvelopePresentParams{Envelope: due.Envelopes[0].ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-3", DriveAttemptID: "drive-3"})
	_ = assertDomainCode(t, CodeWrongState, err)
	shownDue, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: due.Envelopes[0].ID, PrincipalRef: "agent:cody"})
	if err != nil || shownDue.State != "expired" || shownDue.TerminalActor == nil || *shownDue.TerminalActor != "wrkq" {
		t.Fatalf("due presentation guard = %+v, %v", shownDue, err)
	}
}

func TestEnvelopeWithdrawBeforeAndAfterPresentationAndGroupPartialReport(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	group := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "fanout", To: []string{"cody", "mable"}, PrincipalRef: "agent:clod"})
	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{Envelope: group.Envelopes[1].ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-1", DriveAttemptID: "drive-1"}); err != nil {
		t.Fatal(err)
	}
	result, err := f.api.EnvelopeWithdraw(ctx, EnvelopeWithdrawParams{Envelope: group.Envelopes[0].ID, Group: true, Reason: "superseded", PrincipalRef: "agent:clod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Withdrawn) != 1 || result.Withdrawn[0].State != "withdrawn" || len(result.Refused) != 1 || result.Refused[0].Reason != "already_presented" || result.Refused[0].Presentation == nil {
		t.Fatalf("group withdraw = %+v", result)
	}
	var events int
	if err := f.s.DB().QueryRow("SELECT COUNT(*) FROM event_log WHERE resource_uuid = ? AND event_type = 'envelope.withdrawn'", result.Withdrawn[0].UUID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("withdrawal events = %d", events)
	}
	_, err = f.api.EnvelopeWithdraw(ctx, EnvelopeWithdrawParams{Envelope: group.Envelopes[1].ID, PrincipalRef: "agent:clod"})
	_ = assertDomainCode(t, CodeAlreadyPresented, err)
}

func TestManifestScopedReplyDischargesExactSetAtomically(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	codySeat := "cody@proj:" + f.loneTaskID
	a := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "a", To: []string{"cody"}, PrincipalRef: "agent:clod"})
	b := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "b", To: []string{"cody"}, PrincipalRef: "agent:clod"})
	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{Envelope: b.Envelopes[0].ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-1", DriveAttemptID: "drive-" + b.Envelopes[0].ID}); err != nil {
		t.Fatal(err)
	}
	reply := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "only a", To: []string{"clod"}, DischargeEnvelopeIDs: []string{a.Envelopes[0].ID}, PrincipalRef: "agent:cody", ScopeRef: codySeat})
	if len(reply.Acked) != 1 || reply.Acked[0] != a.Envelopes[0].ID {
		t.Fatalf("acked = %v", reply.Acked)
	}
	shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: b.Envelopes[0].ID, PrincipalRef: "agent:cody"})
	if err != nil || shown.State != "presented" {
		t.Fatalf("b = %+v, %v", shown, err)
	}

	var before int
	if err := f.s.DB().QueryRow("SELECT COUNT(*) FROM envelopes").Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, err = f.api.RoomSay(ctx, RoomSayParams{Ref: f.loneTaskID, Body: "invalid", To: []string{"clod"}, DischargeEnvelopeIDs: []string{reply.Envelopes[0].ID}, PrincipalRef: "agent:cody", ScopeRef: codySeat})
	assertValidationReason(t, "addressed to another scope", err)
	var after int
	if err := f.s.DB().QueryRow("SELECT COUNT(*) FROM envelopes").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("invalid scoped reply wrote %d envelopes", after-before)
	}

	foreign := f.say(t, RoomSayParams{Ref: f.memberTaskID, Body: "foreign", To: []string{"cody"}, PrincipalRef: "agent:clod"})
	if err := f.s.DB().QueryRow("SELECT COUNT(*) FROM envelopes").Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, err = f.api.RoomSay(ctx, RoomSayParams{Ref: f.loneTaskID, Body: "foreign refusal", To: []string{"clod"}, DischargeEnvelopeIDs: []string{foreign.Envelopes[0].ID}, PrincipalRef: "agent:cody", ScopeRef: codySeat})
	assertValidationReason(t, "foreign room", err)
	if err := f.s.DB().QueryRow("SELECT COUNT(*) FROM envelopes").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("foreign scoped reply wrote %d envelopes", after-before)
	}

	fyi := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "fyi", To: []string{"cody"}, FYI: true, PrincipalRef: "agent:clod"})
	_, err = f.api.RoomSay(ctx, RoomSayParams{Ref: f.loneTaskID, Body: "not an obligation", To: []string{"clod"}, DischargeEnvelopeIDs: []string{fyi.Envelopes[0].ID}, PrincipalRef: "agent:cody", ScopeRef: codySeat})
	assertValidationReason(t, "must be pending or presented reply_required", err)
}

func TestSayStoresTTLHoldAndIdempotencyAcrossFanout(t *testing.T) {
	f := newRoomFixture(t)
	said := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "hold", To: []string{"cody", "mable"}, TTL: "30s", Hold: true, IdempotencyKey: "admission-1", PrincipalRef: "agent:clod"})
	if len(said.Envelopes) != 2 {
		t.Fatalf("fanout = %d", len(said.Envelopes))
	}
	for _, envelope := range said.Envelopes {
		if envelope.ExpiresAt == nil || envelope.Delivery != "hold" || envelope.IdempotencyKey == nil || *envelope.IdempotencyKey != "admission-1" {
			t.Fatalf("envelope = %+v", envelope)
		}
	}
}
