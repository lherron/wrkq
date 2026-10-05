//go:build wrkq_local

package wrkqapi

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/store"
)

// Reachability: a room's work/activity projection and hidden label change what
// it reports and lists, never whether a say writes or an obligation gates.

// TestSayIntoATerminalTaskRoomAlwaysWrites is the headline of the rev-3
// amendment and the live regression it fixes: a supervisor messaging the seat on
// a task it just completed. There is no room state that can refuse; the room
// only reports what it has become.
func TestSayIntoATerminalTaskRoomAlwaysWrites(t *testing.T) {
	f := newRoomFixture(t)

	opened := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "open for business", PrincipalRef: "agent:clod"})
	if opened.Room.Work != "open" || opened.Room.Activity != "active" {
		t.Fatalf("new task room = work %s / activity %s, want open/active", opened.Room.Work, opened.Room.Activity)
	}
	if opened.Notice != nil {
		t.Fatalf("an open room carried a stale notice: %q", *opened.Notice)
	}

	f.completeTask(t, f.loneTaskID)

	// Terminal but still fresh: no refusal AND no notice. The notice is about
	// staleness, not about terminality.
	fresh := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "grading follow-up", PrincipalRef: "agent:clod"})
	if fresh.Room.Work != "terminal" || fresh.Room.Activity != "active" {
		t.Fatalf("terminal-but-fresh room = work %s / activity %s", fresh.Room.Work, fresh.Room.Activity)
	}
	if fresh.Notice != nil {
		t.Fatalf("a terminal room under 4h carried a notice: %q", *fresh.Notice)
	}

	// Past the 4h stale clock the say STILL writes and gains an advisory.
	backdateRoom(t, f, fresh.Room.UUID, 6*time.Hour)
	if got := roomActivity(t, f, f.loneTaskID).Activity; got != "stale" {
		t.Fatalf("terminal room quiet 6h = activity %s, want stale", got)
	}
	stale := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "still here?", PrincipalRef: "agent:clod"})
	if len(stale.Envelopes) != 1 {
		t.Fatalf("say into a stale room wrote %d envelopes, want 1", len(stale.Envelopes))
	}
	if stale.Notice == nil {
		t.Fatal("say into a stale room carried no notice")
	}
	for _, want := range []string{f.loneTaskID, "task completed", "last activity"} {
		if !strings.Contains(*stale.Notice, want) {
			t.Fatalf("stale notice %q does not name %q", *stale.Notice, want)
		}
	}
	// The say itself refreshes the clock, so the room is active again.
	if got := roomActivity(t, f, f.loneTaskID).Activity; got != "active" {
		t.Fatalf("after a say the room = activity %s, want active", got)
	}
}

// TestPairRoomSayNotices pins T-07700's two advisory-only triggers. Every case
// exercises RoomSay itself so a notice can never become a content refusal or a
// persisted ledger field by accident.
func TestPairRoomSayNotices(t *testing.T) {
	type observed struct {
		result  *WrkqRoomSayResult
		firstID string
	}
	type resultCheck func(*testing.T, *roomFixture, string, string, string) observed
	tests := []struct {
		name string
		run  resultCheck
		want func(*roomFixture, string, string) []string
	}{
		{
			name: "resolvable task mention fires",
			run: func(t *testing.T, f *roomFixture, roomID, senderSeat, targetSeat string) observed {
				return observed{result: f.say(t, RoomSayParams{Ref: roomID, Body: "see " + f.loneTaskID,
					To: []string{targetSeat}, PrincipalRef: "agent:clod", ScopeRef: senderSeat})}
			},
			want: func(f *roomFixture, _, _ string) []string {
				return []string{"this looks like " + f.loneTaskID + " work — say into " + f.loneTaskID + " so a reply here does not cross-discharge it"}
			},
		},
		{
			name: "unresolvable subtask mention never falls back to existing owner",
			run: func(t *testing.T, f *roomFixture, roomID, senderSeat, targetSeat string) observed {
				return observed{result: f.say(t, RoomSayParams{Ref: roomID, Body: "see " + f.loneTaskID + ".render-preview",
					To: []string{targetSeat}, PrincipalRef: "agent:clod", ScopeRef: senderSeat})}
			},
			want: func(_ *roomFixture, _, _ string) []string { return nil },
		},
		{
			name: "unresolvable task mention is silent",
			run: func(t *testing.T, f *roomFixture, roomID, senderSeat, targetSeat string) observed {
				return observed{result: f.say(t, RoomSayParams{Ref: roomID, Body: "see T-99999",
					To: []string{targetSeat}, PrincipalRef: "agent:clod", ScopeRef: senderSeat})}
			},
			want: func(_ *roomFixture, _, _ string) []string { return nil },
		},
		{
			name: "stacked obligation fires",
			run: func(t *testing.T, f *roomFixture, roomID, senderSeat, targetSeat string) observed {
				first := f.say(t, RoomSayParams{Ref: roomID, Body: "first topic",
					To: []string{targetSeat}, PrincipalRef: "agent:clod", ScopeRef: senderSeat})
				second := f.say(t, RoomSayParams{Ref: roomID, Body: "second topic",
					To: []string{targetSeat}, PrincipalRef: "agent:clod", ScopeRef: senderSeat})
				return observed{result: second, firstID: first.Envelopes[0].ID}
			},
			want: func(_ *roomFixture, firstID, targetSeat string) []string {
				return []string{targetSeat + " still owes you a reply here (" + firstID + "); one reply acks both — put a second topic on its task"}
			},
		},
		{
			name: "stacked obligation is silent after ack",
			run: func(t *testing.T, f *roomFixture, roomID, senderSeat, targetSeat string) observed {
				ctx := context.Background()
				first := f.say(t, RoomSayParams{Ref: roomID, Body: "first topic",
					To: []string{targetSeat}, PrincipalRef: "agent:clod", ScopeRef: senderSeat})
				if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
					Envelope: first.Envelopes[0].ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-pair-notice",
				}); err != nil {
					t.Fatalf("present first obligation: %v", err)
				}
				reply := f.say(t, RoomSayParams{Ref: roomID, Body: "answer",
					To: []string{senderSeat}, PrincipalRef: "agent:cody", ScopeRef: targetSeat})
				if len(reply.Acked) != 1 || reply.Acked[0] != first.Envelopes[0].ID {
					t.Fatalf("reply acked %v, want [%s]", reply.Acked, first.Envelopes[0].ID)
				}
				return observed{result: f.say(t, RoomSayParams{Ref: roomID, Body: "next topic",
					To: []string{targetSeat}, PrincipalRef: "agent:clod", ScopeRef: senderSeat})}
			},
			want: func(_ *roomFixture, _, _ string) []string { return nil },
		},
		{
			name: "fyi never fires stacked obligation",
			run: func(t *testing.T, f *roomFixture, roomID, senderSeat, targetSeat string) observed {
				f.say(t, RoomSayParams{Ref: roomID, Body: "first topic",
					To: []string{targetSeat}, PrincipalRef: "agent:clod", ScopeRef: senderSeat})
				return observed{result: f.say(t, RoomSayParams{Ref: roomID, Body: "just fyi", To: []string{targetSeat}, FYI: true,
					PrincipalRef: "agent:clod", ScopeRef: senderSeat})}
			},
			want: func(_ *roomFixture, _, _ string) []string { return nil },
		},
		{
			name: "non adhoc room is silent",
			run: func(t *testing.T, f *roomFixture, _, senderSeat, targetSeat string) observed {
				f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "first topic", To: []string{targetSeat},
					PrincipalRef: "agent:clod", ScopeRef: senderSeat})
				return observed{result: f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "see " + f.loneTaskID, To: []string{targetSeat},
					PrincipalRef: "agent:clod", ScopeRef: senderSeat})}
			},
			want: func(_ *roomFixture, _, _ string) []string { return nil },
		},
		{
			name: "task mention and stacked obligation both fire",
			run: func(t *testing.T, f *roomFixture, roomID, senderSeat, targetSeat string) observed {
				first := f.say(t, RoomSayParams{Ref: roomID, Body: "first topic",
					To: []string{targetSeat}, PrincipalRef: "agent:clod", ScopeRef: senderSeat})
				second := f.say(t, RoomSayParams{Ref: roomID, Body: "see " + f.loneTaskID,
					To: []string{targetSeat}, PrincipalRef: "agent:clod", ScopeRef: senderSeat})
				return observed{result: second, firstID: first.Envelopes[0].ID}
			},
			want: func(f *roomFixture, firstID, targetSeat string) []string {
				return []string{
					"this looks like " + f.loneTaskID + " work — say into " + f.loneTaskID + " so a reply here does not cross-discharge it",
					targetSeat + " still owes you a reply here (" + firstID + "); one reply acks both — put a second topic on its task",
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newRoomFixture(t)
			targetSeat := "cody@proj:primary"
			senderSeat := "clod@proj:primary"
			opened := f.say(t, RoomSayParams{Ref: senderSeat, Body: "pair opener",
				PrincipalRef: "agent:cody", ScopeRef: targetSeat})
			if opened.Room.ID == nil {
				t.Fatal("pair opener did not mint an ad-hoc room id")
			}
			got := tc.run(t, f, *opened.Room.ID, senderSeat, targetSeat)
			want := tc.want(f, got.firstID, targetSeat)
			if fmt.Sprint(got.result.Notices) != fmt.Sprint(want) {
				t.Fatalf("notices = %q, want %q", got.result.Notices, want)
			}
			if len(got.result.Notices) == 0 {
				if got.result.Notice != nil {
					t.Fatalf("legacy notice = %q with no notices", *got.result.Notice)
				}
			} else if got.result.Notice == nil || *got.result.Notice != got.result.Notices[0] {
				t.Fatalf("legacy notice = %v, want first notice %q", got.result.Notice, got.result.Notices[0])
			}
		})
	}
}

// TestHideAffectsTheDefaultListingAndNothingElse pins the label's whole reach:
// it changes what `RoomList` shows by default and never touches say, delivery,
// or obligations.
func TestHideAffectsTheDefaultListingAndNothingElse(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "hello", PrincipalRef: "agent:clod"})

	listed := func(t *testing.T, all bool) bool {
		t.Helper()
		result, err := f.api.RoomList(ctx, RoomListParams{All: all, PrincipalRef: "agent:clod"})
		if err != nil {
			t.Fatalf("RoomList: %v", err)
		}
		for _, room := range result.Items {
			if room.Key == f.loneTaskID {
				return true
			}
		}
		return false
	}

	if !listed(t, false) {
		t.Fatal("an active room is missing from the default listing")
	}
	hidden, err := f.api.RoomHide(ctx, RoomLabelParams{Room: f.loneTaskID, PrincipalRef: "agent:mable"})
	if err != nil {
		t.Fatalf("RoomHide: %v", err)
	}
	if !domain.RoomHasLabel(hidden.Labels, domain.RoomLabelHidden) {
		t.Fatalf("hide did not set the label: %+v", hidden.Labels)
	}
	if listed(t, false) {
		t.Fatal("a hidden room is still in the default listing")
	}
	if !listed(t, true) {
		t.Fatal("--all did not show a hidden room")
	}

	// Hidden changes nothing about reachability: the say writes and the
	// obligation gates.
	said := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "still reachable", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	seat := "cody@proj:" + f.loneTaskID
	f.present(t, said.Envelopes[0].ID)
	view := f.pendingView(t, false, seat)
	if len(view.Blocking) != 1 || view.Blocking[0] != said.Envelopes[0].ID {
		t.Fatalf("a hidden room's obligation stopped gating: %v", view.Blocking)
	}

	unhidden, err := f.api.RoomUnhide(ctx, RoomLabelParams{Room: f.loneTaskID, PrincipalRef: "agent:mable"})
	if err != nil {
		t.Fatalf("RoomUnhide: %v", err)
	}
	if domain.RoomHasLabel(unhidden.Labels, domain.RoomLabelHidden) {
		t.Fatalf("unhide left the label: %+v", unhidden.Labels)
	}
	if !listed(t, false) {
		t.Fatal("unhide did not restore the room to the default listing")
	}
}

// TestDefaultListingOmitsStaleRoomsButKeepsThemAddressable separates DISCOVERY
// from reachability: a stale room leaves the default listing and stays fully
// addressable by key.
func TestDefaultListingOmitsStaleRoomsButKeepsThemAddressable(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	said := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "hello", PrincipalRef: "agent:clod"})
	f.completeTask(t, f.loneTaskID)
	backdateRoom(t, f, said.Room.UUID, 6*time.Hour)

	def, err := f.api.RoomList(ctx, RoomListParams{PrincipalRef: "agent:clod"})
	if err != nil {
		t.Fatalf("RoomList: %v", err)
	}
	for _, room := range def.Items {
		if room.Key == f.loneTaskID {
			t.Fatalf("a stale room is in the default listing: %+v", room)
		}
	}
	all, err := f.api.RoomList(ctx, RoomListParams{All: true, PrincipalRef: "agent:clod"})
	if err != nil {
		t.Fatalf("RoomList --all: %v", err)
	}
	found := false
	for _, room := range all.Items {
		if room.Key == f.loneTaskID {
			found = true
			if room.Activity != "stale" || room.Work != "terminal" {
				t.Fatalf("--all room = work %s / activity %s", room.Work, room.Activity)
			}
		}
	}
	if !found {
		t.Fatal("--all did not show the stale room")
	}
	// Addressable is the point: the listing omitted it, the ledger did not.
	f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "reachable anyway", PrincipalRef: "agent:clod"})
}

// TestActivityIsTotalForAMessagelessRoom is the totality half of the projection:
// a store-created room with no envelope still classifies, because the clock
// folds opened_at. The public surface creates pair rooms lazily through say.
func TestActivityIsTotalForAMessagelessRoom(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	created, err := f.s.Rooms.CreateWithAttribution(attribution.Attribution{
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	}, store.RoomCreateParams{
		Kind: domain.RoomKindAdhoc,
		Members: []store.RoomMemberSeed{
			{MemberRef: "clod@proj:primary", MemberPrincipalRef: "agent:clod", Scoped: true, Source: domain.RoomMemberSourceSpoke},
			{MemberRef: "cody@proj:primary", MemberPrincipalRef: "agent:cody", Scoped: true, Source: domain.RoomMemberSourceAddressed},
		},
	})
	if err != nil {
		t.Fatalf("create message-less room: %v", err)
	}
	room, err := f.api.RoomShow(ctx, RoomShowParams{Room: *created.ID})
	if err != nil {
		t.Fatalf("RoomShow: %v", err)
	}
	if room.MessageCount != 0 {
		t.Fatalf("a fresh store-created room already has %d messages", room.MessageCount)
	}
	if room.Work != "open" || room.Activity != "active" || room.LastActivityAt == "" {
		t.Fatalf("message-less room = work %s / activity %s / last %q",
			room.Work, room.Activity, room.LastActivityAt)
	}

	// And it is REUSABLE as the pair room while active, which is exactly the
	// case a `MAX(envelopes.created_at)`-only clock leaves undefined.
	reused := f.say(t, RoomSayParams{
		Ref: "cody@proj:primary", Body: "first word here",
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})
	if reused.Room.UUID != room.UUID {
		t.Fatalf("an active message-less pair room was not reused: %s then %s", room.UUID, reused.Room.UUID)
	}

	// Aged past the active window it stops being reusable, with no auto-archive
	// and no state change anywhere.
	backdateRoom(t, f, room.UUID, 30*time.Hour)
	if got := roomActivity(t, f, *room.ID).Activity; got != "quiet" {
		t.Fatalf("a 30h-idle ad-hoc room = activity %s, want quiet", got)
	}
	fresh := f.say(t, RoomSayParams{
		Ref: "cody@proj:primary", Body: "much later",
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})
	if fresh.Room.UUID == room.UUID {
		t.Fatal("a quiet pair room was reused")
	}
	// The old room is untouched and still says-able by key.
	f.say(t, RoomSayParams{Ref: *room.ID, Body: "back to the old one", PrincipalRef: "agent:clod"})
}

// TestObligationsGateUniformlyWhateverTheRoomProjection is T-07633 INVERTED
// (T-07642). T-07633 dropped a closed room's mail from pendingView because a
// closed room refused a say and the seat had no reply path. The gate is gone, so
// the reason is gone: every standing obligation wakes and gates, whatever its
// room's work, activity, or hidden label reads.
func TestObligationsGateUniformlyWhateverTheRoomProjection(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	taskSeat := "cody@proj:" + f.loneTaskID
	pairSeat := "cody@proj:primary"

	inTask := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "please reply", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	inPair := f.say(t, RoomSayParams{
		Ref: pairSeat, Body: "still live", PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})
	f.present(t, inTask.Envelopes[0].ID)

	has := func(view *WrkqEnvelopePendingView, id string) bool { return pendingItems(view)[id] != "" }
	blocks := func(view *WrkqEnvelopePendingView, id string) bool { return slices.Contains(view.Blocking, id) }

	live := f.pendingView(t, false, taskSeat, pairSeat)
	if !has(live, inTask.Envelopes[0].ID) || !blocks(live, inTask.Envelopes[0].ID) {
		t.Fatalf("an open room's presented obligation must gate: %+v", live)
	}

	// Terminal work, gone stale, and hidden — every projection that used to
	// excuse an obligation, all at once.
	f.completeTask(t, f.loneTaskID)
	backdateRoom(t, f, inTask.Room.UUID, 6*time.Hour)
	if _, err := f.api.RoomHide(ctx, RoomLabelParams{Room: f.loneTaskID, PrincipalRef: "agent:mable"}); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if got := roomActivity(t, f, f.loneTaskID); got.Work != "terminal" || got.Activity != "stale" {
		t.Fatalf("fixture room = work %s / activity %s, want terminal/stale", got.Work, got.Activity)
	}

	after := f.pendingView(t, false, taskSeat, pairSeat)
	if !has(after, inTask.Envelopes[0].ID) {
		t.Fatalf("a terminal/stale/hidden room's obligation left the kicker wake set: %+v", after.Items)
	}
	if !blocks(after, inTask.Envelopes[0].ID) {
		t.Fatalf("a terminal/stale/hidden room's obligation stopped refusing a turn end: %v", after.Blocking)
	}
	if !has(after, inPair.Envelopes[0].ID) {
		t.Fatalf("an unrelated room's obligation was dropped: %+v", after.Items)
	}

	// The inbox lists it as an ordinary obligation and reports the projection as
	// information, not as a heading with a way out.
	inbox, err := f.api.EnvelopeInboxView(ctx, EnvelopeInboxViewParams{
		ScopeRef: taskSeat, PrincipalRef: "agent:cody",
	})
	if err != nil {
		t.Fatalf("inboxView: %v", err)
	}
	if len(inbox.Groups) != 1 || len(inbox.Groups[0].Items) != 1 ||
		inbox.Groups[0].Items[0].ID != inTask.Envelopes[0].ID {
		t.Fatalf("the obligation vanished from the inbox: %+v", inbox.Groups)
	}
	if inbox.Groups[0].Room.Work != "terminal" {
		t.Fatalf("inbox group room work = %q, want terminal", inbox.Groups[0].Room.Work)
	}
	if inbox.Groups[0].Items[0].State != string(domain.EnvelopeStatePresented) {
		t.Fatalf("the projection retired the obligation: %q", inbox.Groups[0].Items[0].State)
	}

	// And the reply path the carve-out assumed was missing is right there.
	replied := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "on it", To: []string{"clod"},
		PrincipalRef: "agent:cody", ScopeRef: taskSeat,
	})
	if len(replied.Acked) != 1 || replied.Acked[0] != inTask.Envelopes[0].ID {
		t.Fatalf("reply-is-ack did not discharge the obligation: %v", replied.Acked)
	}
}

// TestTerminalWorkKeepsItsFanoutGating is the case T-07633 was found in, with
// the ruling reversed: a supervisor completes a task, and BOTH siblings of the
// fan-out stay standing against their seats. Reachability into finished work is
// intended, not a leak.
func TestTerminalWorkKeepsItsFanoutGating(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	seat := "cody@proj:" + f.loneTaskID

	group := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "review this", To: []string{"cody", "mable"}, PrincipalRef: "agent:clod",
	})
	if len(group.Envelopes) != 2 {
		t.Fatalf("fan-out = %d envelopes, want 2", len(group.Envelopes))
	}
	f.completeTask(t, f.loneTaskID)

	view := f.pendingView(t, false, seat, "mable@proj:"+f.loneTaskID)
	if len(view.Items) != 2 {
		t.Fatalf("terminal work dropped its obligations: %+v", view.Items)
	}

	// The includeFyi read is uniform too.
	fyi := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "heads up", To: []string{"cody"}, FYI: true, PrincipalRef: "agent:clod",
	})
	fyiView := f.pendingView(t, true, seat)
	if pendingItems(fyiView)[fyi.Envelopes[0].ID] == "" {
		t.Fatalf("includeFyi dropped a terminal room's fyi: %+v", fyiView.Items)
	}

	inbox, err := f.api.EnvelopeInboxView(ctx, EnvelopeInboxViewParams{
		ScopeRef: seat, PrincipalRef: "agent:cody",
	})
	if err != nil {
		t.Fatalf("inboxView: %v", err)
	}
	if len(inbox.Groups) != 1 || inbox.Groups[0].Room.Work != "terminal" {
		t.Fatalf("inbox did not report work: terminal as information: %+v", inbox.Groups)
	}
}
