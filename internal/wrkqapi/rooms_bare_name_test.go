//go:build wrkq_local

package wrkqapi

import (
	"context"
	"strings"
	"testing"
)

// §4 bare-name resolution: which seat a bare --to name addresses.

// bareNameFixture stands up the EN-00078 shape: a task room whose members
// include the task-scoped seat AND another seat of the same agent, so the room's
// own shape can never disambiguate a bare name on its own.
func bareNameFixture(t *testing.T, coordinator string) (*roomFixture, string) {
	t.Helper()
	f := newRoomFixture(t)

	// The task-scoped worker seat speaks, so it is a member and the room-derived
	// default (clod@proj:T-xxx) is also a real member.
	f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "worker here",
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:" + f.loneTaskID})

	// The coordinator — a clod seat that is NOT task-scoped — asks mable.
	asked := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "please grade this",
		To: []string{"mable@proj:primary"}, PrincipalRef: "agent:clod", ScopeRef: coordinator})
	obligation := asked.Envelopes[0].ID
	f.present(t, obligation)
	return f, obligation
}

// TestBareNameResolvesToThePresentedObligationsSender is the EN-00078 defect
// itself: HRC's §7 reply line prints a bare name, reply-is-ack keys on scopes,
// and the room's shape points at a DIFFERENT clod seat. The obligation's sender
// wins, so the bare reply acks — whether the asker sat at a :codex-… runtime
// handle or at :primary, the two shapes the collective actually runs.
func TestBareNameResolvesToThePresentedObligationsSender(t *testing.T) {
	for _, coordinator := range []string{
		"clod@proj:codex-019efeb5-1234-7abc-8def-0123456789ab",
		"clod@proj:primary",
	} {
		t.Run(coordinator, func(t *testing.T) {
			f, obligation := bareNameFixture(t, coordinator)
			ctx := context.Background()

			reply, err := f.api.RoomSay(ctx, RoomSayParams{Ref: f.loneTaskID, Body: "graded",
				To: []string{"clod"}, PrincipalRef: "agent:mable", ScopeRef: "mable@proj:primary"})
			if err != nil {
				t.Fatalf("bare reply: %v", err)
			}
			addressee := reply.Envelopes[0].To
			if addressee == nil || addressee.ScopeRef == nil || *addressee.ScopeRef != coordinator {
				t.Fatalf("bare --to clod addressed %+v, want the asking seat %s", addressee, coordinator)
			}
			// The address IS the seat: attribution derives from it, never from
			// the principal the asking say was made under (T-07628).
			if addressee.PrincipalRef != "agent:clod" {
				t.Fatalf("addressee principal = %s, want agent:clod", addressee.PrincipalRef)
			}
			if len(reply.Acked) != 1 || reply.Acked[0] != obligation {
				t.Fatalf("reply acked %v, want [%s]", reply.Acked, obligation)
			}
			f.assertEnvelopeStates(t, map[string]string{obligation: "acked"})
		})
	}
}

// TestBareNameObligationBeatsTheRoomDefaultEvenAsTheOnlyClodMember covers the
// most common collective shape: a :primary supervisor says into a WORKER's task
// room. The room's shape would answer agent@project:T-xxx — the worker's own
// seat — so without the obligation rule the supervisor is never answered.
func TestBareNameObligationBeatsTheRoomDefaultEvenAsTheOnlyClodMember(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	asked := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "status?",
		To: []string{"cody@proj:" + f.loneTaskID}, PrincipalRef: "agent:mable",
		ScopeRef: "mable@proj:primary"})
	obligation := asked.Envelopes[0].ID
	f.present(t, obligation)

	reply, err := f.api.RoomSay(ctx, RoomSayParams{Ref: f.loneTaskID, Body: "still going",
		To: []string{"mable"}, PrincipalRef: "agent:cody", ScopeRef: "cody@proj:" + f.loneTaskID})
	if err != nil {
		t.Fatalf("bare reply: %v", err)
	}
	addressee := reply.Envelopes[0].To
	if addressee == nil || addressee.ScopeRef == nil || *addressee.ScopeRef != "mable@proj:primary" {
		t.Fatalf("worker's bare --to mable addressed %+v, want mable@proj:primary", addressee)
	}
	if len(reply.Acked) != 1 || reply.Acked[0] != obligation {
		t.Fatalf("reply acked %v, want [%s]", reply.Acked, obligation)
	}
}

// TestBareNameTakesTheMostRecentlyPresentedObligation settles the tie the ruling
// names: two seats of one agent both waiting on the replier resolve to the one
// most recently PRESENTED — the one the replier was last shown.
func TestBareNameTakesTheMostRecentlyPresentedObligation(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	const first = "clod@proj:codex-aaaa"
	const second = "clod@proj:codex-bbbb"

	older := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "older ask", To: []string{"mable@proj:primary"},
		PrincipalRef: "agent:clod", ScopeRef: first})
	newer := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "newer ask", To: []string{"mable@proj:primary"},
		PrincipalRef: "agent:clod", ScopeRef: second})
	f.present(t, older.Envelopes[0].ID, newer.Envelopes[0].ID)

	reply, err := f.api.RoomSay(ctx, RoomSayParams{Ref: f.loneTaskID, Body: "answering the latest",
		To: []string{"clod"}, PrincipalRef: "agent:mable", ScopeRef: "mable@proj:primary"})
	if err != nil {
		t.Fatalf("bare reply: %v", err)
	}
	addressee := reply.Envelopes[0].To
	if addressee == nil || addressee.ScopeRef == nil || *addressee.ScopeRef != second {
		t.Fatalf("bare --to clod addressed %+v, want the most recently presented asker %s", addressee, second)
	}
}

// TestBareNameAmbiguityAlwaysRefusesWithCandidatesForEveryCaller locks the
// second half of the ruling: with no obligation to resolve against, several
// same-name members ALWAYS refuse and name every candidate, identically for
// every caller. A refusal costs one retry; a silently chosen seat costs an
// obligation nobody notices is dead.
func TestBareNameAmbiguityAlwaysRefusesWithCandidatesForEveryCaller(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	seats := []string{
		"clod@proj:" + f.loneTaskID,
		"clod@proj:codex-019efeb5-1234-7abc-8def-0123456789ab",
		"clod@agent-loop:primary",
	}
	for _, seat := range seats {
		f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "seat " + seat,
			PrincipalRef: "agent:clod", ScopeRef: seat})
	}

	// Resolution is a function of the ROOM and the replier's SEAT, never of the
	// principal a say is attributed to: an --as that disagrees with the seat
	// cannot move it, and a scope-less human sees the same refusal.
	callers := []struct{ principal, scopeRef string }{
		{"agent:clod", "clod@proj:codex-019efeb5-1234-7abc-8def-0123456789ab"},
		{"agent:mable", "clod@proj:codex-019efeb5-1234-7abc-8def-0123456789ab"},
		{"agent:mable", "mable@proj:primary"},
		{"agent:lance", ""},
	}
	for _, caller := range callers {
		_, err := f.api.RoomSay(ctx, RoomSayParams{Ref: f.loneTaskID, Body: "bare",
			To: []string{"clod"}, PrincipalRef: caller.principal, ScopeRef: caller.scopeRef})
		domainErr := assertDomainCode(t, "WRKQ_VALIDATION", err)
		for _, seat := range seats {
			if !strings.Contains(domainErr.Error(), seat) {
				t.Fatalf("caller %s/%q: refusal %q omits candidate %s",
					caller.principal, caller.scopeRef, domainErr.Error(), seat)
			}
		}
		data, ok := domainErr.Data().(map[string]any)
		if !ok {
			t.Fatalf("refusal data = %T, want map", domainErr.Data())
		}
		candidates, ok := data["candidates"].([]string)
		if !ok || len(candidates) != len(seats) {
			t.Fatalf("refusal candidates = %v, want %d seats", data["candidates"], len(seats))
		}
	}

	// A --fyi carries no obligation and gets the same refusal: nothing about the
	// obligation rule quietly re-enables a silent pick.
	_, err := f.api.RoomSay(ctx, RoomSayParams{Ref: f.loneTaskID, Body: "heads up",
		To: []string{"clod"}, FYI: true, PrincipalRef: "agent:mable", ScopeRef: "mable@proj:primary"})
	_ = assertDomainCode(t, "WRKQ_VALIDATION", err)
}

// TestBareNameFallsBackToTheRoomDefaultWithNoMemberOfThatName keeps the last
// rung: with no obligation and no member of that name, the room's own shape
// still resolves, so addressing someone who has never spoken here still works.
func TestBareNameFallsBackToTheRoomDefaultWithNoMemberOfThatName(t *testing.T) {
	f := newRoomFixture(t)

	result := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "first contact",
		To: []string{"fowler"}, PrincipalRef: "agent:clod"})
	addressee := result.Envelopes[0].To
	if addressee == nil || addressee.ScopeRef == nil || *addressee.ScopeRef != "fowler@proj:"+f.loneTaskID {
		t.Fatalf("unknown bare name addressed %+v, want the task-scoped default", addressee)
	}

	campaign := f.say(t, RoomSayParams{Ref: f.campaignPath, Body: "first contact",
		To: []string{"fowler"}, PrincipalRef: "agent:clod"})
	addressee = campaign.Envelopes[0].To
	if addressee == nil || addressee.ScopeRef == nil || *addressee.ScopeRef != "fowler@proj:primary" {
		t.Fatalf("campaign-room bare name addressed %+v, want fowler@proj:primary", addressee)
	}
}

// TestBareNameObligationResolutionLeavesFanoutSiblingsUntouched proves the new
// rung cannot leak across a fan-out: answering the seat that asked acks THAT
// obligation and no sibling of the group addressed to another scope.
func TestBareNameObligationResolutionLeavesFanoutSiblingsUntouched(t *testing.T) {
	f, obligation := bareNameFixture(t, "clod@proj:codex-019efeb5-1234-7abc-8def-0123456789ab")
	ctx := context.Background()

	// A second asker fans out to mable AND cody in the same room.
	fanout := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "both of you",
		To:           []string{"mable@proj:primary", "cody@proj:primary"},
		PrincipalRef: "agent:fowler", ScopeRef: "fowler@proj:primary"})
	if len(fanout.Envelopes) != 2 {
		t.Fatalf("fan-out wrote %d envelopes, want 2", len(fanout.Envelopes))
	}
	f.present(t, fanout.Envelopes[0].ID, fanout.Envelopes[1].ID)

	reply, err := f.api.RoomSay(ctx, RoomSayParams{Ref: f.loneTaskID, Body: "graded",
		To: []string{"clod"}, PrincipalRef: "agent:mable", ScopeRef: "mable@proj:primary"})
	if err != nil {
		t.Fatalf("bare reply: %v", err)
	}
	if len(reply.Acked) != 1 || reply.Acked[0] != obligation {
		t.Fatalf("reply acked %v, want only %s", reply.Acked, obligation)
	}
	// Both fan-out siblings stay untouched.
	f.assertEnvelopeStates(t, map[string]string{
		fanout.Envelopes[0].ID: "presented",
		fanout.Envelopes[1].ID: "presented",
	})
}

// TestEnvelopeReplyToCarriesTheExactSenderScope is the consumer half: every
// envelope names the exact --to that answers it, so HRC's §7 reply line prints
// a handle it never had to guess.
func TestEnvelopeReplyToCarriesTheExactSenderScope(t *testing.T) {
	const coordinator = "clod@proj:codex-019efeb5-1234-7abc-8def-0123456789ab"
	f, obligation := bareNameFixture(t, coordinator)
	ctx := context.Background()

	shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: obligation})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if shown.ReplyTo != coordinator {
		t.Fatalf("envelope replyTo = %q, want the exact sender scope %s", shown.ReplyTo, coordinator)
	}

	inbox, err := f.api.EnvelopeInboxView(ctx, EnvelopeInboxViewParams{
		ScopeRef: "mable@proj:primary", PrincipalRef: "agent:mable",
	})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	found := false
	for _, group := range inbox.Groups {
		for _, envelope := range group.Items {
			if envelope.ID != obligation {
				continue
			}
			found = true
			if envelope.ReplyTo != coordinator {
				t.Fatalf("inbox replyTo = %q, want %s", envelope.ReplyTo, coordinator)
			}
		}
	}
	if !found {
		t.Fatalf("obligation %s missing from the inbox view", obligation)
	}

	// A scope-less sender has no seat, so its principal IS the reply token.
	human := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "from a human",
		To: []string{"mable@proj:primary"}, PrincipalRef: "agent:lance"})
	if human.Envelopes[0].ReplyTo != "agent:lance" {
		t.Fatalf("scope-less sender replyTo = %q, want agent:lance", human.Envelopes[0].ReplyTo)
	}
}
