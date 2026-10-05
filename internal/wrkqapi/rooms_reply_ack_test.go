//go:build wrkq_local

package wrkqapi

import (
	"context"
	"strings"
	"testing"
)

// Fan-out isolation and reply-is-ack: which obligations a say creates and which
// a reply discharges.

// TestFanoutWritesOneEnvelopePerAddresseeSharingAGroup proves the §3.2 fan-out
// contract: N addressees produce N envelopes with ONE group id, and every
// lifecycle field is per envelope so one recipient's disposition never touches
// its siblings.
func TestFanoutWritesOneEnvelopePerAddresseeSharingAGroup(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	result := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "three of you", To: []string{"cody,mable", "fowler"},
		PrincipalRef: "agent:clod", IdempotencyKey: "acp:hrc-message:m-1",
	})
	if len(result.Envelopes) != 3 {
		t.Fatalf("fan-out wrote %d envelopes, want 3", len(result.Envelopes))
	}
	if result.GroupID != result.Envelopes[0].ID {
		t.Fatalf("group id %q is not the first envelope's own id %q", result.GroupID, result.Envelopes[0].ID)
	}
	for _, envelope := range result.Envelopes {
		if envelope.GroupID == nil || *envelope.GroupID != result.GroupID {
			t.Fatalf("envelope %s group = %v, want %s", envelope.ID, envelope.GroupID, result.GroupID)
		}
		if envelope.State != "pending" || envelope.Obligation != "reply_required" {
			t.Fatalf("envelope %s = %s/%s", envelope.ID, envelope.Obligation, envelope.State)
		}
		// The say's idempotency key rides EVERY row so a consumer dual-writing
		// elsewhere can correlate on any addressee's envelope.
		if envelope.IdempotencyKey == nil || *envelope.IdempotencyKey != "acp:hrc-message:m-1" {
			t.Fatalf("envelope %s idempotency key = %v", envelope.ID, envelope.IdempotencyKey)
		}
		// A bare name in a task room resolves to the task-scoped seat.
		if envelope.To == nil || envelope.To.ScopeRef == nil ||
			!strings.HasSuffix(*envelope.To.ScopeRef, "@proj:"+f.loneTaskID) {
			t.Fatalf("envelope %s addressee = %+v, want a task-scoped seat", envelope.ID, envelope.To)
		}
	}

	// Present all three, then dispose exactly one of them three different ways
	// and prove the siblings are untouched each time.
	f.present(t, result.Envelopes[0].ID, result.Envelopes[1].ID, result.Envelopes[2].ID)

	// 1. defer one
	if _, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{
		Envelope: result.Envelopes[0].ID, Reason: "busy",
		PrincipalRef: "agent:cody", ScopeRef: *result.Envelopes[0].To.ScopeRef,
	}); err != nil {
		t.Fatalf("defer sibling 0: %v", err)
	}
	// 2. fail another
	if _, err := f.api.EnvelopeFail(ctx, EnvelopeFailParams{
		Envelope: result.Envelopes[1].ID, Reason: "ignored", Runtime: "rt-1", PrincipalRef: "agent:hrc",
	}); err != nil {
		t.Fatalf("fail sibling 1: %v", err)
	}
	// 3. operator-ack the third
	if _, err := f.api.EnvelopeAck(ctx, EnvelopeAckParams{
		Envelopes: []string{result.Envelopes[2].ID}, PrincipalRef: "agent:lance",
	}); err != nil {
		t.Fatalf("operator ack sibling 2: %v", err)
	}

	// Any other state means a sibling's disposition leaked.
	f.assertEnvelopeStates(t, map[string]string{
		result.Envelopes[0].ID: "deferred",
		result.Envelopes[1].ID: "failed",
		result.Envelopes[2].ID: "acked",
	})
}

// TestFYIPresentationAcksOnlyItsOwnEnvelope proves a fyi presented to ONE
// recipient stays pending for the others: fyi auto-acks at its own
// presentation, not at the group's.
func TestFYIPresentationAcksOnlyItsOwnEnvelope(t *testing.T) {
	f := newRoomFixture(t)

	result := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "heads up", To: []string{"cody", "mable"}, FYI: true,
		PrincipalRef: "agent:clod",
	})
	if len(result.Envelopes) != 2 {
		t.Fatalf("fyi fan-out wrote %d envelopes", len(result.Envelopes))
	}
	f.present(t, result.Envelopes[0].ID)
	f.assertEnvelopeStates(t, map[string]string{
		result.Envelopes[0].ID: "acked",
		result.Envelopes[1].ID: "pending",
	})
}

// TestReplyAcksOwnObligationsOnlyAndDeferExcludesOne is the §6 core: a reply acks
// every presented obligation addressed to the REPLIER'S OWN scope from that
// counterparty, leaves obligations addressed to other scopes alone, and skips
// one that was deferred first.
func TestReplyAcksOwnObligationsOnlyAndDeferExcludesOne(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	codySeat := "cody@proj:" + f.loneTaskID
	mableSeat := "mable@proj:" + f.loneTaskID

	// clod asks cody twice and mable once, in the same room.
	askA := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "question A", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	askB := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "question B", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	askC := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "question C", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	askMable := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "unrelated", To: []string{"mable"}, PrincipalRef: "agent:clod",
	})
	// mable also asks cody something: a DIFFERENT counterparty's obligation.
	askFromMable := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "mable asks cody", To: []string{"cody"},
		PrincipalRef: "agent:mable", ScopeRef: mableSeat,
	})

	f.present(t, askA.Envelopes[0].ID, askB.Envelopes[0].ID, askC.Envelopes[0].ID,
		askMable.Envelopes[0].ID, askFromMable.Envelopes[0].ID)

	// cody defers C explicitly: this is how you exclude ONE from a reply.
	if _, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{
		Envelope: askC.Envelopes[0].ID, Reason: "needs the build first",
		PrincipalRef: "agent:cody", ScopeRef: codySeat,
	}); err != nil {
		t.Fatalf("defer C: %v", err)
	}

	// cody replies to clod. This acks A and B and nothing else.
	reply := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "answers", To: []string{"clod"},
		PrincipalRef: "agent:cody", ScopeRef: codySeat,
	})
	acked := ackedSet(reply.Acked)
	if !acked[askA.Envelopes[0].ID] || !acked[askB.Envelopes[0].ID] {
		t.Fatalf("reply did not ack both of clod's standing questions: %v", reply.Acked)
	}
	if acked[askC.Envelopes[0].ID] {
		t.Fatal("reply acked the envelope cody deliberately deferred")
	}
	if acked[askMable.Envelopes[0].ID] {
		t.Fatal("reply acked an obligation addressed to ANOTHER scope")
	}
	if acked[askFromMable.Envelopes[0].ID] {
		t.Fatal("reply to clod acked an obligation from a different counterparty")
	}

	f.assertEnvelopeStates(t, map[string]string{
		askA.Envelopes[0].ID:         "acked",
		askB.Envelopes[0].ID:         "acked",
		askC.Envelopes[0].ID:         "deferred",
		askMable.Envelopes[0].ID:     "presented",
		askFromMable.Envelopes[0].ID: "presented",
	})
}

// TestReplyAcksPendingObligationsAndPreservesIsolation covers the mid-turn
// mail-hint path: a reply is stronger evidence of reading than a presentation
// receipt, so it acks both pending and presented obligations while preserving
// every existing room, obligation, counterparty-scope, and addressee boundary.
func TestReplyAcksPendingObligationsAndPreservesIsolation(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	clodSeat := "clod@proj:" + f.loneTaskID
	otherClodSeat := "clod@proj:primary"
	codySeat := "cody@proj:" + f.loneTaskID
	mableSeat := "mable@proj:" + f.loneTaskID

	pending := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "pending", To: []string{codySeat},
		PrincipalRef: "agent:clod", ScopeRef: clodSeat,
	})
	presented := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "presented", To: []string{codySeat},
		PrincipalRef: "agent:clod", ScopeRef: clodSeat,
	})
	deferred := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "deferred", To: []string{codySeat},
		PrincipalRef: "agent:clod", ScopeRef: clodSeat,
	})
	f.present(t, presented.Envelopes[0].ID, deferred.Envelopes[0].ID)
	if _, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{
		Envelope: deferred.Envelopes[0].ID, Reason: "not this reply",
		PrincipalRef: "agent:cody", ScopeRef: codySeat,
	}); err != nil {
		t.Fatalf("defer: %v", err)
	}
	fyi := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "fyi", To: []string{codySeat}, FYI: true,
		PrincipalRef: "agent:clod", ScopeRef: clodSeat,
	})
	otherCounterpartyScope := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "same principal, other seat", To: []string{codySeat},
		PrincipalRef: "agent:clod", ScopeRef: otherClodSeat,
	})
	fanout := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "fanout", To: []string{codySeat, mableSeat},
		PrincipalRef: "agent:clod", ScopeRef: clodSeat,
	})
	siblings := envelopeByScope(t, fanout.Envelopes)
	codySibling, mableSibling := siblings[codySeat], siblings[mableSeat]
	if codySibling == "" || mableSibling == "" {
		t.Fatalf("fan-out did not address both seats: %+v", fanout.Envelopes)
	}
	otherRoom := f.say(t, RoomSayParams{
		Ref: f.memberTaskID, Body: "other room", To: []string{codySeat},
		PrincipalRef: "agent:clod", ScopeRef: clodSeat,
	})
	human := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "scope-less human", To: []string{codySeat},
		PrincipalRef: "agent:lance",
	})

	reply := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "read and answered", To: []string{clodSeat},
		PrincipalRef: "agent:cody", ScopeRef: codySeat,
	})
	acked := ackedSet(reply.Acked)
	for _, id := range []string{pending.Envelopes[0].ID, presented.Envelopes[0].ID, codySibling} {
		if !acked[id] {
			t.Fatalf("reply did not ack %s: %v", id, reply.Acked)
		}
	}
	if len(acked) != 3 {
		t.Fatalf("reply acked extra envelopes: %v", reply.Acked)
	}
	f.assertEnvelopeStates(t, map[string]string{
		deferred.Envelopes[0].ID:               "deferred",
		fyi.Envelopes[0].ID:                    "pending",
		otherCounterpartyScope.Envelopes[0].ID: "pending",
		mableSibling:                           "pending",
		otherRoom.Envelopes[0].ID:              "pending",
		human.Envelopes[0].ID:                  "pending",
	})
	payload := f.lastEventPayload(t, pending.Envelopes[0].UUID, "envelope.acked")
	if !strings.Contains(payload, `"reason":"reply"`) || !strings.Contains(payload, `"previous_state":"pending"`) {
		t.Fatalf("pending ack payload = %s", payload)
	}

	humanReply := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "answering Lance", To: []string{"lance"},
		PrincipalRef: "agent:cody", ScopeRef: codySeat,
	})
	if len(humanReply.Acked) != 1 || humanReply.Acked[0] != human.Envelopes[0].ID {
		t.Fatalf("scope-less pending reply acked %v, want %s", humanReply.Acked, human.Envelopes[0].ID)
	}
}

// TestReplyAcksBySeatEvenWhenTheMemberPrincipalDisagrees is the T-07628 rule: a
// member IS a scope and its principal is attribution only, so ack matching keys
// on SCOPES on both sides. A seat whose first say carried an `--as` disagreeing
// with it still discharges every later obligation it sent, the member row's
// principal follows the latest say, and a scope-less human keeps matching by
// principal because it has no scope to match on.
func TestReplyAcksBySeatEvenWhenTheMemberPrincipalDisagrees(t *testing.T) {
	f := newRoomFixture(t)
	clodSeat := "clod@proj:" + f.loneTaskID
	codySeat := "cody@proj:" + f.loneTaskID
	mableSeat := "mable@proj:" + f.loneTaskID

	// clod's seat speaks first under the WRONG hat: the member row is minted
	// (clodSeat -> agent:mable). This is the shape that failed EN-00027.
	wrongHat := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "asked under the wrong --as", To: []string{"cody"},
		PrincipalRef: "agent:mable", ScopeRef: clodSeat,
	})
	if principal := roomMemberPrincipal(t, f, clodSeat); principal != "agent:mable" {
		t.Fatalf("member principal after the first say = %q, want agent:mable", principal)
	}

	// The same seat then speaks correctly. Attendance follows the latest say...
	rightHat := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "asked under the right --as", To: []string{"cody"},
		PrincipalRef: "agent:clod", ScopeRef: clodSeat,
	})
	if principal := roomMemberPrincipal(t, f, clodSeat); principal != "agent:clod" {
		t.Fatalf("member principal after a later say = %q, want agent:clod", principal)
	}

	// ...and a fan-out from that seat gives mable a sibling obligation that
	// cody's reply must not touch.
	fanout := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "both of you", To: []string{"cody", "mable"},
		PrincipalRef: "agent:clod", ScopeRef: clodSeat,
	})
	siblings := envelopeByScope(t, fanout.Envelopes)
	codySibling, mableSibling := siblings[codySeat], siblings[mableSeat]
	if codySibling == "" || mableSibling == "" {
		t.Fatalf("fan-out did not address both seats: %+v", fanout.Envelopes)
	}

	// A scope-less human asks cody too: it has no scope, so it keeps matching
	// on its principal exactly as before.
	human := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "Lance asks", To: []string{"cody"}, PrincipalRef: "agent:lance",
	})

	f.present(t, wrongHat.Envelopes[0].ID, rightHat.Envelopes[0].ID,
		codySibling, mableSibling, human.Envelopes[0].ID)

	// One correct reply to the seat discharges everything that seat sent,
	// whatever principal each say was attributed to.
	reply := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "answers", To: []string{"clod"},
		PrincipalRef: "agent:cody", ScopeRef: codySeat,
	})
	acked := ackedSet(reply.Acked)
	if !acked[wrongHat.Envelopes[0].ID] {
		t.Fatalf("reply left the obligation sent under a disagreeing --as standing: %v", reply.Acked)
	}
	if !acked[rightHat.Envelopes[0].ID] || !acked[codySibling] {
		t.Fatalf("reply did not ack the seat's other obligations: %v", reply.Acked)
	}
	if acked[mableSibling] {
		t.Fatal("reply acked a fan-out sibling addressed to another seat")
	}
	if acked[human.Envelopes[0].ID] {
		t.Fatal("a reply to clod's seat acked the human's obligation")
	}

	// The human path is unchanged: a reply addressed to the scope-less
	// principal still discharges what that principal sent.
	humanReply := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "answering Lance", To: []string{"lance"},
		PrincipalRef: "agent:cody", ScopeRef: codySeat,
	})
	if len(humanReply.Acked) != 1 || humanReply.Acked[0] != human.Envelopes[0].ID {
		t.Fatalf("reply to a scope-less principal acked %v, want %s",
			humanReply.Acked, human.Envelopes[0].ID)
	}

	// The seat is addressed as itself: the envelope carries the seat's own
	// agent as attribution, never the principal frozen on the member row.
	if to := reply.Envelopes[0].To; to == nil || to.ScopeRef == nil ||
		*to.ScopeRef != clodSeat || to.PrincipalRef != "agent:clod" {
		t.Fatalf("addressing the seat resolved to %+v, want %s/agent:clod", to, clodSeat)
	}
}

// TestSayWithoutToIsALogEntry proves §5's only-`--to`-fires rule: a say with no
// addressee is disposed at write and appears in nobody's inbox.
func TestSayWithoutToIsALogEntry(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	logged := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "thinking out loud", PrincipalRef: "agent:clod"})
	if len(logged.Envelopes) != 1 {
		t.Fatalf("log entry wrote %d envelopes", len(logged.Envelopes))
	}
	envelope := logged.Envelopes[0]
	if envelope.Obligation != "none" || envelope.To != nil {
		t.Fatalf("log entry = %s to %+v, want none/nil", envelope.Obligation, envelope.To)
	}
	if envelope.State != "acked" {
		t.Fatalf("log entry state = %s, want acked at write (nothing will ever present it)", envelope.State)
	}
	inbox, err := f.api.EnvelopeInboxView(ctx, EnvelopeInboxViewParams{
		PrincipalRef: "agent:cody", ScopeRef: "cody@proj:" + f.loneTaskID,
	})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if len(inbox.Groups) != 0 {
		t.Fatalf("a say without --to reached an inbox: %+v", inbox.Groups)
	}
}

// TestHumanPrincipalSaysAndAcksUnderUnchangedAttribution proves §3.3/§11: a
// human is an ordinary scope-less principal. agent:lance can say, be addressed,
// and operator-ack, all under the SAME attribution contract as any agent — this
// spec adds no principal kind and no auth machinery.
func TestHumanPrincipalSaysAndAcksUnderUnchangedAttribution(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	// A human says with no scope at all.
	said := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "Lance here", To: []string{"cody"}, PrincipalRef: "agent:lance",
	})
	if said.Envelopes[0].From.PrincipalRef != "agent:lance" {
		t.Fatalf("human sender principal = %q", said.Envelopes[0].From.PrincipalRef)
	}
	if said.Envelopes[0].From.ScopeRef != nil {
		t.Fatalf("human sender carried a scope: %v", said.Envelopes[0].From.ScopeRef)
	}

	// A human is addressable by an explicit principal, and gets no derived scope.
	toHuman := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "for Lance", To: []string{"agent:lance"}, PrincipalRef: "agent:cody",
	})
	if toHuman.Envelopes[0].To.PrincipalRef != "agent:lance" {
		t.Fatalf("human addressee = %+v", toHuman.Envelopes[0].To)
	}
	if toHuman.Envelopes[0].To.ScopeRef != nil {
		t.Fatalf("human addressee was given a scope it does not have: %v", toHuman.Envelopes[0].To.ScopeRef)
	}

	// Once the ledger has seen that principal scope-less, a BARE name addresses
	// it directly instead of deriving a seat it will never occupy.
	bare := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "still for Lance", To: []string{"lance"}, PrincipalRef: "agent:cody",
	})
	if bare.Envelopes[0].To.ScopeRef != nil {
		t.Fatalf("bare human name derived a scope: %v", bare.Envelopes[0].To.ScopeRef)
	}

	// The operator ack is reachable by that same ordinary principal.
	acked, err := f.api.EnvelopeAck(ctx, EnvelopeAckParams{
		Envelopes: []string{said.Envelopes[0].ID}, PrincipalRef: "agent:lance", Note: "handled offline",
	})
	if err != nil {
		t.Fatalf("operator ack as a human: %v", err)
	}
	if acked.Items[0].State != "acked" || acked.Items[0].TerminalActor == nil ||
		*acked.Items[0].TerminalActor != "agent:lance" {
		t.Fatalf("operator ack = %+v", acked.Items[0])
	}
}

func TestConsumedByWaitAckRequiresExactAddresseeAndLiveReply(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	aScope := "alice@proj:" + f.loneTaskID
	otherAliceScope := "alice@proj:primary"
	bScope := "bob@proj:" + f.loneTaskID

	fanout := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "fan-out reply", To: []string{aScope, otherAliceScope},
		PrincipalRef: "agent:bob", ScopeRef: bScope,
	})
	toAlice := fanout.Envelopes[0]
	toOtherAlice := fanout.Envelopes[1]
	if toAlice.To == nil || toAlice.To.ScopeRef == nil || *toAlice.To.ScopeRef != aScope {
		toAlice, toOtherAlice = toOtherAlice, toAlice
	}

	_, err := f.api.EnvelopeAck(ctx, EnvelopeAckParams{
		Envelopes: []string{toAlice.ID}, Reason: envelopeAckReasonConsumedByWait,
		PrincipalRef: "agent:bob", ScopeRef: bScope,
	})
	_ = assertDomainCode(t, CodeForbidden, err)
	_, err = f.api.EnvelopeAck(ctx, EnvelopeAckParams{
		Envelopes: []string{toAlice.ID}, Reason: envelopeAckReasonConsumedByWait,
		PrincipalRef: "agent:alice", ScopeRef: otherAliceScope,
	})
	_ = assertDomainCode(t, CodeForbidden, err)

	acked, err := f.api.EnvelopeAck(ctx, EnvelopeAckParams{
		Envelopes: []string{toAlice.ID}, Reason: envelopeAckReasonConsumedByWait,
		PrincipalRef: "agent:alice", ScopeRef: aScope,
	})
	if err != nil {
		t.Fatalf("consume pending reply: %v", err)
	}
	if len(acked.Items) != 1 || acked.Items[0].State != "acked" || acked.Items[0].Reason == nil ||
		*acked.Items[0].Reason != envelopeAckReasonConsumedByWait {
		t.Fatalf("consumed pending reply = %+v", acked.Items)
	}
	if eventPayload := f.lastEventPayload(t, toAlice.UUID, "envelope.acked"); !strings.Contains(eventPayload, `"reason":"consumed_by_wait"`) {
		t.Fatalf("consumed ack payload = %s", eventPayload)
	}

	other, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: toOtherAlice.ID, PrincipalRef: "agent:alice"})
	if err != nil {
		t.Fatalf("show untouched sibling: %v", err)
	}
	if other.State != "pending" {
		t.Fatalf("same-principal other-scope sibling = %s, want pending", other.State)
	}

	presented := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "presented reply", To: []string{aScope},
		PrincipalRef: "agent:bob", ScopeRef: bScope,
	})
	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: presented.Envelopes[0].ID, PrincipalRef: "agent:hrc", ScopeRef: aScope,
	}); err != nil {
		t.Fatalf("present reply: %v", err)
	}
	if _, err := f.api.EnvelopeAck(ctx, EnvelopeAckParams{
		Envelopes: []string{presented.Envelopes[0].ID}, Reason: envelopeAckReasonConsumedByWait,
		PrincipalRef: "agent:alice", ScopeRef: aScope,
	}); err != nil {
		t.Fatalf("consume presented reply: %v", err)
	}

	deferred := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "deferred reply", To: []string{aScope},
		PrincipalRef: "agent:bob", ScopeRef: bScope,
	})
	if _, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{
		Envelope: deferred.Envelopes[0].ID, Reason: "later",
		PrincipalRef: "agent:alice", ScopeRef: aScope,
	}); err != nil {
		t.Fatalf("defer reply: %v", err)
	}
	_, err = f.api.EnvelopeAck(ctx, EnvelopeAckParams{
		Envelopes: []string{deferred.Envelopes[0].ID}, Reason: envelopeAckReasonConsumedByWait,
		PrincipalRef: "agent:alice", ScopeRef: aScope,
	})
	_ = assertDomainCode(t, CodeWrongState, err)
	stillDeferred, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{
		Envelope: deferred.Envelopes[0].ID, PrincipalRef: "agent:alice",
	})
	if err != nil {
		t.Fatalf("show deferred reply: %v", err)
	}
	if stillDeferred.State != "deferred" {
		t.Fatalf("consumed_by_wait touched deferred reply: %s", stillDeferred.State)
	}
}

// TestSayIdempotencyKeyRefusesARetriedSay proves the guard the key buys: a
// retried say with the same key collides and rolls back rather than
// double-writing the group.
func TestSayIdempotencyKeyRefusesARetriedSay(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	params := RoomSayParams{
		Ref: f.loneTaskID, Body: "exactly once", To: []string{"cody", "mable"},
		PrincipalRef: "agent:clod", IdempotencyKey: "acp:hrc-message:m-9",
	}
	f.say(t, params)
	if _, err := f.api.RoomSay(ctx, params); err == nil {
		t.Fatal("a retried say with the same idempotency key wrote a second group")
	}

	var count int
	if err := f.s.DB().QueryRow(
		"SELECT COUNT(*) FROM envelopes WHERE idempotency_key = ?", "acp:hrc-message:m-9").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("idempotency key covers %d envelopes, want exactly the 2 of the first say", count)
	}
}
