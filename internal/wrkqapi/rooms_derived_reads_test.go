//go:build wrkq_local

package wrkqapi

import (
	"context"
	"testing"

	"github.com/lherron/wrkq/internal/domain"
)

// T-08298: a room read never hard-fails on work entitled to a room; it derives
// the room a say would open, without writing it.

// countRooms is the "reads never write" instrument: it counts materialized room
// rows, which a read must never change.
func (f *roomFixture) countRooms(t *testing.T) int {
	t.Helper()
	var rooms int
	if err := f.s.DB().QueryRow("SELECT COUNT(*) FROM rooms").Scan(&rooms); err != nil {
		t.Fatalf("count rooms: %v", err)
	}
	return rooms
}

// TestRoomReadsDeriveAnUnspokenRoomInsteadOfFailing is the whole point: an agent
// told that campaigns have rooms calls log/show/members on one nobody has spoken
// into, and gets an empty room rather than WRKQ_NOT_FOUND.
func TestRoomReadsDeriveAnUnspokenRoomInsteadOfFailing(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	before := f.countRooms(t)

	log, err := f.api.RoomLogView(ctx, RoomLogViewParams{Room: f.campaignPath, PrincipalRef: "agent:clod"})
	if err != nil {
		t.Fatalf("log on an unspoken campaign room failed: %v", err)
	}
	if len(log.Items) != 0 {
		t.Fatalf("derived room carried %d messages, want 0", len(log.Items))
	}
	if log.Room.Kind != string(domain.RoomKindCampaign) {
		t.Fatalf("derived room kind = %q, want campaign", log.Room.Kind)
	}
	if log.Room.Key != f.campaignPath {
		t.Fatalf("derived room key = %q, want %q", log.Room.Key, f.campaignPath)
	}

	show, err := f.api.RoomShow(ctx, RoomShowParams{Room: f.campaignPath, PrincipalRef: "agent:clod"})
	if err != nil {
		t.Fatalf("show on an unspoken campaign room failed: %v", err)
	}
	if show.MessageCount != 0 || show.MemberCount != 0 {
		t.Fatalf("derived room counts = %d messages / %d members, want 0/0", show.MessageCount, show.MemberCount)
	}

	members, err := f.api.RoomMembersView(ctx, RoomMembersViewParams{Room: f.campaignPath, PrincipalRef: "agent:clod"})
	if err != nil {
		t.Fatalf("members on an unspoken campaign room failed: %v", err)
	}
	if len(members.Items) != 0 {
		t.Fatalf("derived room had %d members, want 0", len(members.Items))
	}

	// The instrument that makes this a DERIVED room and not a minted one.
	if after := f.countRooms(t); after != before {
		t.Fatalf("reads materialized %d room row(s); reads must never write", after-before)
	}
}

// TestDerivedRoomIsTheSameRoomASayWouldOpen pins the join between the read and
// the say: whatever a read shows you, a say must land in THAT room. A derived
// room that materialized into a different row would split the conversation.
func TestDerivedRoomIsTheSameRoomASayWouldOpen(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	read, err := f.api.RoomLogView(ctx, RoomLogViewParams{Room: f.campaignPath, PrincipalRef: "agent:clod"})
	if err != nil {
		t.Fatalf("log on an unspoken campaign room failed: %v", err)
	}
	f.say(t, RoomSayParams{Ref: f.campaignPath, Body: "first", PrincipalRef: "agent:clod"})

	after, err := f.api.RoomLogView(ctx, RoomLogViewParams{Room: f.campaignPath, PrincipalRef: "agent:clod"})
	if err != nil {
		t.Fatalf("log after say: %v", err)
	}
	if len(after.Items) != 1 {
		t.Fatalf("say did not land in the room the read showed: %d messages", len(after.Items))
	}
	if read.Room.Key != after.Room.Key || read.Room.Kind != after.Room.Kind {
		t.Fatalf("derived room %s/%s became %s/%s after a say",
			read.Room.Key, read.Room.Kind, after.Room.Key, after.Room.Kind)
	}
	if f.countRooms(t) != 1 {
		t.Fatalf("say opened %d rooms, want exactly 1", f.countRooms(t))
	}
}

// TestDerivedTaskRoomFollowsTheCampaignCoalesce is the split-conversation guard.
// A campaign-resident task's say coalesces into the campaign room, so a read of
// that task must derive the CAMPAIGN room — never a task room that a say would
// then bypass forever.
func TestDerivedTaskRoomFollowsTheCampaignCoalesce(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	for _, taskID := range []string{f.residentTaskID, f.memberTaskID} {
		view, err := f.api.RoomLogView(ctx, RoomLogViewParams{Room: taskID, PrincipalRef: "agent:clod"})
		if err != nil {
			t.Fatalf("log on campaign task %s failed: %v", taskID, err)
		}
		if view.Room.Kind != string(domain.RoomKindCampaign) {
			t.Fatalf("task %s derived a %s room; a say would coalesce to the campaign room and split the conversation",
				taskID, view.Room.Kind)
		}
		if view.Room.Key != f.campaignPath {
			t.Fatalf("task %s derived room key %q, want the campaign %q", taskID, view.Room.Key, f.campaignPath)
		}
	}

	// A task outside any campaign still derives its own task room.
	lone, err := f.api.RoomLogView(ctx, RoomLogViewParams{Room: f.loneTaskID, PrincipalRef: "agent:clod"})
	if err != nil {
		t.Fatalf("log on a lone task failed: %v", err)
	}
	if lone.Room.Kind != string(domain.RoomKindTask) {
		t.Fatalf("lone task derived a %s room, want task", lone.Room.Kind)
	}
}

// TestExistingTaskRoomWinsOverTheCoalesceOnRead guards the ordering the
// linked-never-merged law requires: a task room that predates enrolment stays
// readable at its own selector even though new says go to the campaign.
func TestExistingTaskRoomWinsOverTheCoalesceOnRead(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "before enrolment", PrincipalRef: "agent:clod"})
	if _, err := f.s.DB().Exec("UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", f.campaignUUID, f.loneTaskUUID); err != nil {
		t.Fatalf("enroll lone task: %v", err)
	}

	view, err := f.api.RoomLogView(ctx, RoomLogViewParams{Room: f.loneTaskID, PrincipalRef: "agent:clod"})
	if err != nil {
		t.Fatalf("log on a newly enrolled task with its own room failed: %v", err)
	}
	if view.Room.Kind != string(domain.RoomKindTask) {
		t.Fatalf("read followed the coalesce past an existing task room: kind %s", view.Room.Kind)
	}
	if len(view.Items) != 1 {
		t.Fatalf("task room lost its own history on read: %d messages", len(view.Items))
	}
}

// TestUnentitledContainerKeepsTheTypedRefusal: deriving must not paper over a
// true answer. A plain directory has no room and never will, and the read says
// so with the same typed reason the say path uses — not "room not found".
func TestUnentitledContainerKeepsTheTypedRefusal(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	_, err := f.api.RoomLogView(ctx, RoomLogViewParams{Room: f.plainContainerPath, PrincipalRef: "agent:clod"})
	assertValidationReason(t, "room_kind_unsupported", err)

	// And an R- id for a room that does not exist derives nothing: an ad-hoc
	// room's identity is its member set, so a selector naming no work has
	// nothing to derive from.
	_, err = f.api.RoomLogView(ctx, RoomLogViewParams{Room: "R-09999", PrincipalRef: "agent:clod"})
	_ = assertDomainCode(t, CodeNotFound, err)
}

// TestJoinMaterializesADerivedRoomButLeaveDoesNot: a write verb that ADDS state
// opens the row; declining to be in a room must not open one as a side effect.
func TestJoinMaterializesADerivedRoomButLeaveDoesNot(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	if _, err := f.api.RoomLeave(ctx, RoomMemberParams{
		Room: f.campaignPath, PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	}); err != nil {
		t.Fatalf("leave on an unspoken room failed: %v", err)
	}
	if got := f.countRooms(t); got != 0 {
		t.Fatalf("leave materialized %d room(s); it must open nothing", got)
	}

	view, err := f.api.RoomJoin(ctx, RoomMemberParams{
		Room: f.campaignPath, PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})
	if err != nil {
		t.Fatalf("join on an unspoken room failed: %v", err)
	}
	if got := f.countRooms(t); got != 1 {
		t.Fatalf("join materialized %d rooms, want exactly 1", got)
	}
	if len(view.Items) != 1 || view.Items[0].MemberRef != "clod@proj:primary" {
		t.Fatalf("join did not record the member: %+v", view.Items)
	}
}
