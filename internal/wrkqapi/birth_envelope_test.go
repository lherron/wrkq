//go:build wrkq_local

package wrkqapi

import (
	"context"
	"testing"
)

// T-07655 acceptance 1(a)/5: the BIRTH ENVELOPE of a target scope is the
// envelope with the lowest ledger sequence addressed to that scope whose
// obligation is `reply_required`, REGARDLESS of state. fyi rows never summon
// and are outside the domain; a log entry has no addressee at all.
//
// The rule matters because HRC's registry host reads this to decide, exactly
// once, which node a virgin scope is born on. If the read drifted to "lowest
// pending" the answer would change as mail was disposed, and a scope could be
// designated one home today and another tomorrow.

const birthTarget = "cody@proj:primary"

// birthEnvelopeOf is the read under test, called the way the registry host
// calls it: target scope only.
func birthEnvelopeOf(t *testing.T, f *roomFixture, scopeRef string) *WrkqEnvelopeBirth {
	t.Helper()
	got, err := f.api.EnvelopeBirthEnvelope(context.Background(), EnvelopeBirthEnvelopeParams{ScopeRef: scopeRef})
	if err != nil {
		t.Fatalf("EnvelopeBirthEnvelope(%s): %v", scopeRef, err)
	}
	return got
}

func TestBirthEnvelopeIsTheLowestSeqReplyRequiredWhateverItsState(t *testing.T) {
	f := newRoomFixture(t)

	// An fyi lands FIRST and must never be the birth envelope: it does not fire.
	f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "heads up", To: []string{birthTarget}, FYI: true,
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})
	first := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "please do the thing", To: []string{birthTarget},
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})
	second := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "and this too", To: []string{birthTarget},
		PrincipalRef: "agent:mable", ScopeRef: "mable@proj:primary",
	})
	if first.Envelopes[0].ID >= second.Envelopes[0].ID {
		t.Fatalf("ledger order broken: %s then %s", first.Envelopes[0].ID, second.Envelopes[0].ID)
	}

	got := birthEnvelopeOf(t, f, birthTarget)
	if got == nil {
		t.Fatal("birth envelope is nil for a scope with two standing obligations")
	}
	if got.EnvelopeID != first.Envelopes[0].ID {
		t.Fatalf("birth envelope = %s, want %s (the lowest-seq reply_required)", got.EnvelopeID, first.Envelopes[0].ID)
	}
	if got.Seq <= 0 {
		t.Fatalf("birth envelope seq = %d, want the id's ordinal", got.Seq)
	}
	// `from` is what the designation reads to find the sender's home. It is the
	// LEDGER's record of the sender, never anything a caller supplied.
	if got.From.PrincipalRef != "agent:clod" {
		t.Fatalf("from.principalRef = %q, want agent:clod", got.From.PrincipalRef)
	}
	if got.From.ScopeRef == nil || *got.From.ScopeRef != "clod@proj:primary" {
		t.Fatalf("from.scopeRef = %v, want clod@proj:primary", got.From.ScopeRef)
	}

	// State-independence: dispose the birth envelope every terminal way and the
	// answer must not move. A designation taken today must be re-derivable.
	for _, state := range []string{"presented", "acked", "deferred", "dead"} {
		if _, err := f.s.DB().Exec(
			"UPDATE envelopes SET state = ?, defer_reason = 'test' WHERE id = ?", state, first.Envelopes[0].ID,
		); err != nil {
			t.Fatalf("force state %s: %v", state, err)
		}
		again := birthEnvelopeOf(t, f, birthTarget)
		if again == nil || again.EnvelopeID != first.Envelopes[0].ID {
			t.Fatalf("state %s moved the birth envelope to %v", state, again)
		}
	}
}

func TestBirthEnvelopeIsNilWhenNothingEverFiredAtTheScope(t *testing.T) {
	f := newRoomFixture(t)

	// A log entry (no addressee) and an fyi are the two ways a scope can appear
	// in the ledger without ever having been summoned.
	f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "thinking out loud", PrincipalRef: "agent:clod"})
	f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "fyi only", To: []string{birthTarget}, FYI: true,
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})

	if got := birthEnvelopeOf(t, f, birthTarget); got != nil {
		t.Fatalf("birth envelope = %+v, want nil: only fyi and log entries exist", got)
	}
	if got := birthEnvelopeOf(t, f, "fowler@proj:primary"); got != nil {
		t.Fatalf("birth envelope = %+v for a scope the ledger has never seen", got)
	}
}

func TestBirthEnvelopeRefusesATargetThatIsNotAScope(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	for _, target := range []string{"", "   ", "agent:lance"} {
		_, err := f.api.EnvelopeBirthEnvelope(ctx, EnvelopeBirthEnvelopeParams{ScopeRef: target})
		refusal := assertDomainCode(t, CodeValidation, err)
		data, _ := refusal.Data().(map[string]any)
		if data["field"] != "scopeRef" {
			t.Fatalf("refusal for %q names field %v, want scopeRef", target, data["field"])
		}
	}
}
