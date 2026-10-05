//go:build wrkq_local

package wrkqapi

import (
	"context"
	"testing"
)

// §3.3 membership and the --record comment bridge.

// TestMembershipSourcesAndAttendance proves §3.3: membership comes from spoke,
// addressed, and joined only — never derived from wrkq fields — and attendance
// is the latest presentation receipt, absent for scope-less members.
func TestMembershipSourcesAndAttendance(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	said := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "hello", To: []string{"cody", "agent:lance"},
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:" + f.loneTaskID,
	})
	if _, err := f.api.RoomJoin(ctx, RoomMemberParams{
		Room: f.loneTaskID, Member: "mable@proj:primary", PrincipalRef: "agent:mable",
	}); err != nil {
		t.Fatalf("join: %v", err)
	}
	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: said.Envelopes[0].ID, PrincipalRef: "agent:hrc", Node: "mini", RuntimeID: "rt-1", Generation: "7",
	}); err != nil {
		t.Fatalf("present: %v", err)
	}

	members, err := f.api.RoomMembersView(ctx, RoomMembersViewParams{Room: f.loneTaskID})
	if err != nil {
		t.Fatalf("membersView: %v", err)
	}
	bySource := map[string]string{}
	attendance := map[string]bool{}
	for _, member := range members.Items {
		bySource[member.MemberRef] = member.Source
		attendance[member.MemberRef] = member.Attendance != nil
	}
	if bySource["clod@proj:"+f.loneTaskID] != "spoke" {
		t.Fatalf("sender source = %q, want spoke", bySource["clod@proj:"+f.loneTaskID])
	}
	if bySource["cody@proj:"+f.loneTaskID] != "addressed" {
		t.Fatalf("addressee source = %q, want addressed", bySource["cody@proj:"+f.loneTaskID])
	}
	if bySource["mable@proj:primary"] != "joined" {
		t.Fatalf("joiner source = %q, want joined", bySource["mable@proj:primary"])
	}
	if !attendance["cody@proj:"+f.loneTaskID] {
		t.Fatal("a presented member has no attendance")
	}
	if attendance["agent:lance"] {
		t.Fatal("a scope-less member has attendance; it is never presented through a runtime")
	}
	// The task's assignee is NOT a member: derived membership does not exist.
	if _, ok := bySource["agent:wrkq-system"]; ok {
		t.Fatal("membership was derived from a wrkq field")
	}
}

// TestRecordBridgesToACommentAndNothingElse proves the one bridge: --record
// writes the body as a wrkq comment on the room's task, and no say mirrors
// itself into comments without it.
func TestRecordBridgesToACommentAndNothingElse(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	plain := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "just talk", PrincipalRef: "agent:clod"})
	if plain.RecordedCommentID != nil {
		t.Fatal("a plain say mirrored itself into comments")
	}
	var count int
	if err := f.s.DB().QueryRow("SELECT COUNT(*) FROM comments WHERE task_uuid = ?", f.loneTaskUUID).Scan(&count); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if count != 0 {
		t.Fatalf("plain say wrote %d comments", count)
	}

	recorded := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "this is the record", Record: true, PrincipalRef: "agent:clod",
	})
	if recorded.RecordedCommentID == nil {
		t.Fatal("--record wrote no comment")
	}
	var body string
	if err := f.s.DB().QueryRow(
		"SELECT body FROM comments WHERE id = ?", *recorded.RecordedCommentID).Scan(&body); err != nil {
		t.Fatalf("read comment: %v", err)
	}
	if body != "this is the record" {
		t.Fatalf("recorded comment body = %q", body)
	}
	_ = ctx
}
