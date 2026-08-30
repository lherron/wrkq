//go:build wrkq_local

package wrkqapi

import (
	"context"
	"testing"
)

const initialEnvelopeBeforeSeq int64 = 1 << 62

func TestEnvelopeMemberPageReverseAndForwardAreExclusiveGapFreeAndExact(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	memberRef := "cody@proj:primary"

	first := f.say(t, RoomSayParams{
		Ref: "cody@proj:primary", To: []string{memberRef}, Body: "member one",
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})
	roomID := *first.Room.ID
	second := f.say(t, RoomSayParams{
		Ref: roomID, To: []string{memberRef}, Body: "member two",
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})
	third := f.say(t, RoomSayParams{
		Ref: roomID, To: []string{memberRef}, Body: "member three",
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})

	// Same principal, different scope: exact member filtering must exclude it.
	f.say(t, RoomSayParams{
		Ref: "cody@proj:" + f.loneTaskID, To: []string{"cody@proj:" + f.loneTaskID},
		Body: "different cody seat", PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})

	reverse, err := f.api.EnvelopeMemberPage(ctx, EnvelopeMemberPageParams{
		MemberRef: memberRef, BeforeMessageSeq: int64ptr(initialEnvelopeBeforeSeq), Limit: 2,
	})
	if err != nil {
		t.Fatalf("first reverse page: %v", err)
	}
	if got, want := envelopePageBodies(reverse.Items), []string{"member two", "member three"}; !equalStrings(got, want) {
		t.Fatalf("first reverse page bodies = %v, want %v", got, want)
	}
	if reverse.Items[0].MessageSeq != second.Envelopes[0].MessageSeq ||
		reverse.Items[1].MessageSeq != third.Envelopes[0].MessageSeq {
		t.Fatalf("first reverse page sequences = [%d %d], want [%d %d]",
			reverse.Items[0].MessageSeq, reverse.Items[1].MessageSeq,
			second.Envelopes[0].MessageSeq, third.Envelopes[0].MessageSeq)
	}
	if !reverse.HasMoreBefore || reverse.HasMoreAfter {
		t.Fatalf("first reverse availability = before:%v after:%v, want true/false",
			reverse.HasMoreBefore, reverse.HasMoreAfter)
	}
	if reverse.HeadMessageSeq < third.Envelopes[0].MessageSeq || reverse.LedgerIncarnation == "" {
		t.Fatalf("missing head/incarnation metadata: %+v", reverse)
	}

	nextReverse, err := f.api.EnvelopeMemberPage(ctx, EnvelopeMemberPageParams{
		MemberRef: memberRef, BeforeMessageSeq: int64ptr(reverse.Items[0].MessageSeq), Limit: 1,
		ExpectedLedgerIncarnation: reverse.LedgerIncarnation,
	})
	if err != nil {
		t.Fatalf("next reverse page: %v", err)
	}
	if len(nextReverse.Items) != 1 || nextReverse.Items[0].MessageSeq != first.Envelopes[0].MessageSeq {
		t.Fatalf("next reverse page = %+v, want first envelope only", nextReverse.Items)
	}
	if nextReverse.HasMoreBefore || !nextReverse.HasMoreAfter {
		t.Fatalf("next reverse availability = before:%v after:%v, want false/true",
			nextReverse.HasMoreBefore, nextReverse.HasMoreAfter)
	}

	forward, err := f.api.EnvelopeMemberPage(ctx, EnvelopeMemberPageParams{
		MemberRef: memberRef, AfterMessageSeq: int64ptr(first.Envelopes[0].MessageSeq), Limit: 1,
		ExpectedLedgerIncarnation: reverse.LedgerIncarnation,
	})
	if err != nil {
		t.Fatalf("first forward page: %v", err)
	}
	if len(forward.Items) != 1 || forward.Items[0].MessageSeq != second.Envelopes[0].MessageSeq {
		t.Fatalf("first forward page = %+v, want second envelope only", forward.Items)
	}
	if !forward.HasMoreBefore || !forward.HasMoreAfter {
		t.Fatalf("first forward availability = before:%v after:%v, want true/true",
			forward.HasMoreBefore, forward.HasMoreAfter)
	}

	lastForward, err := f.api.EnvelopeMemberPage(ctx, EnvelopeMemberPageParams{
		MemberRef: memberRef, AfterMessageSeq: int64ptr(forward.Items[0].MessageSeq), Limit: 2,
		ExpectedLedgerIncarnation: reverse.LedgerIncarnation,
	})
	if err != nil {
		t.Fatalf("last forward page: %v", err)
	}
	if len(lastForward.Items) != 1 || lastForward.Items[0].MessageSeq != third.Envelopes[0].MessageSeq {
		t.Fatalf("last forward page = %+v, want third envelope only", lastForward.Items)
	}
	if !lastForward.HasMoreBefore || lastForward.HasMoreAfter {
		t.Fatalf("last forward availability = before:%v after:%v, want true/false",
			lastForward.HasMoreBefore, lastForward.HasMoreAfter)
	}
}

func TestEnvelopeMemberPageRejectsInvalidCursorAndShapeWithoutEnvelopes(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	memberRef := "cody@proj:primary"
	f.say(t, RoomSayParams{
		Ref: "cody@proj:primary", To: []string{memberRef}, Body: "fenced",
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})

	_, err := f.api.EnvelopeMemberPage(ctx, EnvelopeMemberPageParams{
		MemberRef: memberRef, BeforeMessageSeq: int64ptr(initialEnvelopeBeforeSeq),
		AfterMessageSeq: int64ptr(0), Limit: 1,
	})
	_ = assertDomainCode(t, CodeValidation, err)

	_, err = f.api.EnvelopeMemberPage(ctx, EnvelopeMemberPageParams{
		MemberRef: memberRef, BeforeMessageSeq: int64ptr(initialEnvelopeBeforeSeq), Limit: 501,
	})
	_ = assertDomainCode(t, CodeValidation, err)

	_, err = f.api.EnvelopeMemberPage(ctx, EnvelopeMemberPageParams{
		MemberRef: memberRef, BeforeMessageSeq: int64ptr(initialEnvelopeBeforeSeq), Limit: 1,
		ExpectedLedgerIncarnation: "replacement-ledger",
	})
	_ = assertDomainCode(t, CodeCursorInvalid, err)
}

func int64ptr(value int64) *int64 { return &value }

func envelopePageBodies(items []WrkqEnvelope) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.Body)
	}
	return result
}

func equalStrings(lhs, rhs []string) bool {
	if len(lhs) != len(rhs) {
		return false
	}
	for index := range lhs {
		if lhs[index] != rhs[index] {
			return false
		}
	}
	return true
}
