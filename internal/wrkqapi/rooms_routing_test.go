//go:build wrkq_local

package wrkqapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/store"
)

// §4 routing table: which room a selector or seat pair resolves to.

// TestRoutingRule1_RoomAndEnvelopeIDsResolveToTheirRoom covers rule 1: an R- id
// is the room and an EN- id resolves to the room it lives in.
func TestRoutingRule1_RoomAndEnvelopeIDsResolveToTheirRoom(t *testing.T) {
	f := newRoomFixture(t)

	first := f.say(t, RoomSayParams{
		Ref: "cody@proj:primary", Body: "pair opener",
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})
	roomID := first.Room.ID
	if roomID == nil {
		t.Fatal("ad-hoc room minted no R- id")
	}
	if !strings.HasPrefix(*roomID, "R-") {
		t.Fatalf("ad-hoc room id = %q, want R- prefix", *roomID)
	}

	byRoomID := f.say(t, RoomSayParams{
		Ref: *roomID, Body: "by room id",
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})
	if byRoomID.Room.UUID != first.Room.UUID {
		t.Fatalf("R- selector routed to %s, want %s", byRoomID.Room.UUID, first.Room.UUID)
	}

	envelopeID := first.Envelopes[0].ID
	if !strings.HasPrefix(envelopeID, "EN-") {
		t.Fatalf("envelope id = %q, want EN- prefix (EV- belongs to evidence_items)", envelopeID)
	}
	byEnvelopeID := f.say(t, RoomSayParams{
		Ref: envelopeID, Body: "by envelope id",
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})
	if byEnvelopeID.Room.UUID != first.Room.UUID {
		t.Fatalf("EN- selector routed to %s, want %s", byEnvelopeID.Room.UUID, first.Room.UUID)
	}
}

// TestRoutingRule2_StrictCampaignCoalesce covers rule 2 and the strict-coalesce
// invariant: a task NOT in a campaign gets its own room; a task IN a campaign
// talks in the CAMPAIGN room with no override, and the envelope is tagged with
// the task it came through either way.
func TestRoutingRule2_StrictCampaignCoalesce(t *testing.T) {
	f := newRoomFixture(t)

	lone := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "on the lone task", PrincipalRef: "agent:clod"})
	if lone.Room.Kind != "task" || lone.Room.Key != f.loneTaskID {
		t.Fatalf("lone task room = %s/%s, want task/%s", lone.Room.Kind, lone.Room.Key, f.loneTaskID)
	}
	if lone.Envelopes[0].TaskID == nil || *lone.Envelopes[0].TaskID != f.loneTaskID {
		t.Fatalf("lone envelope task tag = %v, want %s", lone.Envelopes[0].TaskID, f.loneTaskID)
	}

	member := f.say(t, RoomSayParams{Ref: f.memberTaskID, Body: "on the enrolled task", PrincipalRef: "agent:clod"})
	if member.Room.Kind != "campaign" {
		t.Fatalf("enrolled task routed to a %s room, want campaign (strict coalesce)", member.Room.Kind)
	}
	if member.Room.Key != f.campaignPath {
		t.Fatalf("campaign room key = %q, want %q", member.Room.Key, f.campaignPath)
	}
	// The tag survives the coalesce: this is what makes `wrkc log <campaign>
	// --task T-x` able to narrow back down.
	if member.Envelopes[0].TaskID == nil || *member.Envelopes[0].TaskID != f.memberTaskID {
		t.Fatalf("coalesced envelope lost its task tag: %v", member.Envelopes[0].TaskID)
	}
	// No task room was created for the enrolled task — the coalesce is strict.
	if room, err := f.s.Rooms.GetByTask(f.memberTaskUUID); err != nil || room != nil {
		t.Fatalf("enrolled task got its own room (%v, err=%v); coalesce is not strict", room, err)
	}

	// RESIDENCY is the common membership form: a task inside the campaign
	// container is a member without any campaign_uuid. A campaign_uuid-only
	// coalesce gives it its own room and splits the campaign's conversation —
	// which is exactly what the live smoke against the canonical ledger caught.
	resident := f.say(t, RoomSayParams{Ref: f.residentTaskID, Body: "on the resident task", PrincipalRef: "agent:clod"})
	if resident.Room.Kind != "campaign" {
		t.Fatalf("RESIDENT campaign task routed to a %s room, want campaign", resident.Room.Kind)
	}
	if resident.Room.UUID != member.Room.UUID {
		t.Fatalf("resident and enrolled members landed in different rooms (%s vs %s); one campaign has one room",
			resident.Room.UUID, member.Room.UUID)
	}
	if resident.Envelopes[0].TaskID == nil || *resident.Envelopes[0].TaskID != f.residentTaskID {
		t.Fatalf("resident envelope lost its task tag: %v", resident.Envelopes[0].TaskID)
	}
	if room, err := f.s.Rooms.GetByTask(f.residentTaskUUID); err != nil || room != nil {
		t.Fatalf("resident task got its own room (%v, err=%v); coalesce is not strict", room, err)
	}
}

// TestCoalesceIgnoresCampaignState proves rule 2 and rule 3 cannot disagree
// about the same campaign. A campaign container routes to its campaign room
// whatever its state, so gating the task-side coalesce on `active` would make
// a completed campaign mint fresh task rooms for work whose conversation already
// lives in the campaign room.
func TestCoalesceIgnoresCampaignState(t *testing.T) {
	f := newRoomFixture(t)

	for _, state := range []string{"draft", "active", "completed", "cancelled"} {
		if _, err := f.s.DB().Exec("UPDATE containers SET campaign_state = ? WHERE uuid = ?", state, f.campaignUUID); err != nil {
			t.Fatalf("set campaign_state=%s: %v", state, err)
		}
		routed, err := f.api.routeToTaskUUID(context.Background(),
			attribution.Attribution{PrincipalRef: "agent:clod"}, f.residentTaskUUID)
		if err != nil {
			t.Fatalf("route with campaign_state=%s: %v", state, err)
		}
		if routed.room.Kind != domain.RoomKindCampaign {
			t.Fatalf("campaign_state=%s routed to a %s room, want campaign", state, routed.room.Kind)
		}
	}
}

// TestRoutingRule3_ContainerKinds covers rule 3: a campaign-adorned container
// routes to the campaign room, a project container to the project room, and any
// other container is a typed refusal.
func TestRoutingRule3_ContainerKinds(t *testing.T) {
	f := newRoomFixture(t)

	campaign := f.say(t, RoomSayParams{Ref: f.campaignPath, Body: "campaign talk", PrincipalRef: "agent:clod"})
	if campaign.Room.Kind != "campaign" {
		t.Fatalf("campaign container routed to %s", campaign.Room.Kind)
	}

	project := f.say(t, RoomSayParams{Ref: "proj", Body: "project talk", PrincipalRef: "agent:clod"})
	if project.Room.Kind != "project" {
		t.Fatalf("project container routed to %s, want project", project.Room.Kind)
	}

	_, err := f.api.RoomSay(context.Background(), RoomSayParams{
		Ref: f.plainContainerPath, Body: "nope", PrincipalRef: "agent:clod",
	})
	de := assertDomainCode(t, CodeValidation, err)
	if !strings.Contains(de.Error(), "room_kind_unsupported") {
		t.Fatalf("plain container refusal is not typed room_kind_unsupported: %v", de)
	}
}

// TestRoutingRule4_TargetWins covers rule 4's three derived cases. The target's
// work context wins; when only the SENDER is task-scoped the say still lands on
// the work, which is what keeps an escalation attached to its task instead of
// disappearing into a side channel.
func TestRoutingRule4_TargetWins(t *testing.T) {
	f := newRoomFixture(t)

	// Target task-scoped → the target's task room, and --to is implied.
	targetScoped := f.say(t, RoomSayParams{
		Ref: "cody@proj:" + f.loneTaskID, Body: "you own this",
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})
	if targetScoped.Room.Key != f.loneTaskID {
		t.Fatalf("target-task-scoped routed to %q, want %s", targetScoped.Room.Key, f.loneTaskID)
	}
	if len(targetScoped.Envelopes) != 1 || targetScoped.Envelopes[0].To == nil {
		t.Fatalf("target handle did not imply --to: %+v", targetScoped.Envelopes)
	}
	if got := *targetScoped.Envelopes[0].To.ScopeRef; got != "cody@proj:"+f.loneTaskID {
		t.Fatalf("implied --to = %q", got)
	}

	// Sender task-scoped, target NOT → the SENDER's task room.
	senderScoped := f.say(t, RoomSayParams{
		Ref: "mable@proj:primary", Body: "escalating from my task",
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:" + f.loneTaskID,
	})
	if senderScoped.Room.Key != f.loneTaskID {
		t.Fatalf("sender-task-scoped escalation routed to %q, want the sender's task room %s",
			senderScoped.Room.Key, f.loneTaskID)
	}

	// Task A → task B: the TARGET's room wins.
	other, err := f.s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "task-b", Title: "B", ProjectUUID: f.proj, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task B: %v", err)
	}
	crossTask := f.say(t, RoomSayParams{
		Ref: "cody@proj:" + other.ID, Body: "about your work, not mine",
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:" + f.loneTaskID,
	})
	if crossTask.Room.Key != other.ID {
		t.Fatalf("task A → task B routed to %q, want the TARGET's room %s", crossTask.Room.Key, other.ID)
	}

	// Neither task-scoped → an ad-hoc pair room.
	adhoc := f.say(t, RoomSayParams{
		Ref: "cody@proj:primary", Body: "just us",
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})
	if adhoc.Room.Kind != "adhoc" {
		t.Fatalf("neither-task-scoped routed to %s, want adhoc", adhoc.Room.Kind)
	}
}

// TestAdhocPairRoomReuseNewAndThirdMember covers §4's pair-room rules: reuse the
// open pair room, --new forces a fresh one, and a third member makes it a group
// room so the next unsolicited pair say opens a NEW pair room rather than
// joining a conversation somebody deliberately widened.
func TestAdhocPairRoomReuseNewAndThirdMember(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	sender := RoomSayParams{
		Ref: "cody@proj:primary", Body: "one", PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	}

	first := f.say(t, sender)
	second := f.say(t, RoomSayParams{
		Ref: sender.Ref, Body: "two", PrincipalRef: sender.PrincipalRef, ScopeRef: sender.ScopeRef,
	})
	if second.Room.UUID != first.Room.UUID {
		t.Fatalf("pair room not reused: %s then %s", first.Room.UUID, second.Room.UUID)
	}

	forced := f.say(t, RoomSayParams{
		Ref: sender.Ref, Body: "three", New: true,
		PrincipalRef: sender.PrincipalRef, ScopeRef: sender.ScopeRef,
	})
	if forced.Room.UUID == first.Room.UUID {
		t.Fatal("--new reused the existing pair room")
	}

	// Widen the FIRST room to three members; it stops being a pair room.
	if _, err := f.api.RoomJoin(ctx, RoomMemberParams{
		Room: *first.Room.ID, Member: "mable@proj:primary", PrincipalRef: "agent:clod",
	}); err != nil {
		t.Fatalf("invite third member: %v", err)
	}
	// The most recent pair room is `forced`; age it past the 24h `active` window
	// so the only remaining pair candidate is quiet, and prove neither the
	// widened group room nor the quiet pair room is reused. Reuse keys on
	// ACTIVITY now: there is no closed state left to key on.
	backdateRoom(t, f, forced.Room.UUID, 30*time.Hour)
	fresh := f.say(t, RoomSayParams{
		Ref: sender.Ref, Body: "four", PrincipalRef: sender.PrincipalRef, ScopeRef: sender.ScopeRef,
	})
	if fresh.Room.UUID == first.Room.UUID {
		t.Fatal("a widened group room was reused as a pair room")
	}
	if fresh.Room.UUID == forced.Room.UUID {
		t.Fatal("a pair room whose activity had gone quiet was reused")
	}
}

// TestAdhocPairRoomScopelessSenderKeysOnPrincipal: a caller with no HRC scope
// (a human via Discord ingress, or a seat that forgot HRC_SESSION_REF) still
// opens a pair room with a bare seat — the 2026-08-30 regression was a hard
// refusal here that broke Lance → vesta@hcs:primary. The room is keyed on the
// principal as an unscoped member, reused on the next say, and the say carries
// an advisory notice instead of an error (a say is never refused for who the
// caller is).
func TestAdhocPairRoomScopelessSenderKeysOnPrincipal(t *testing.T) {
	f := newRoomFixture(t)
	human := RoomSayParams{Ref: "vesta@proj:primary", Body: "Hi", PrincipalRef: "agent:lance"}

	first := f.say(t, human)
	if first.Room.Kind != string(domain.RoomKindAdhoc) {
		t.Fatalf("expected adhoc pair room, got %s", first.Room.Kind)
	}
	var noticed bool
	for _, n := range first.Notices {
		if strings.Contains(n, "no caller scope") {
			noticed = true
		}
	}
	if !noticed {
		t.Fatalf("expected a no-caller-scope notice, got %v", first.Notices)
	}
	second := f.say(t, RoomSayParams{Ref: human.Ref, Body: "again", PrincipalRef: human.PrincipalRef})
	if second.Room.UUID != first.Room.UUID {
		t.Fatalf("scope-less pair room not reused: %s then %s", first.Room.UUID, second.Room.UUID)
	}
	members, err := f.api.RoomMembersView(context.Background(), RoomMembersViewParams{Room: *first.Room.ID, PrincipalRef: "agent:lance"})
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	var sawPrincipal bool
	for _, m := range members.Items {
		if m.MemberRef == "agent:lance" {
			sawPrincipal = true
			if m.Scoped {
				t.Fatal("scope-less sender recorded as a scoped member")
			}
		}
	}
	if !sawPrincipal {
		t.Fatalf("principal not a member: %+v", members.Items)
	}
}

// TestScopeRefToleratesTheRuntimeLaneSuffix is the case the isolated smoke
// caught: every live agent's HRC_SESSION_REF carries a runtime lane
// ("agent:clod:project:wrkq:task:T-07613/lane:main"), which the scope grammar
// does not accept. A lane is execution vocabulary — which pane is speaking — so
// wrkq drops it and keeps the scope. Without this, wrkc fails for EVERY agent
// under its real environment.
func TestScopeRefToleratesTheRuntimeLaneSuffix(t *testing.T) {
	f := newRoomFixture(t)

	said := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "from a real session", To: []string{"cody"},
		PrincipalRef: "agent:clod",
		ScopeRef:     "agent:clod:project:proj:task:" + f.loneTaskID + "/lane:main",
	})
	from := said.Envelopes[0].From
	if from.ScopeRef == nil || *from.ScopeRef != "clod@proj:"+f.loneTaskID {
		t.Fatalf("sender scope = %v, want the lane stripped to clod@proj:%s", from.ScopeRef, f.loneTaskID)
	}

	// A role suffix is part of the scope grammar and must SURVIVE: only a
	// suffix carrying its own key:value shape is a runtime lane.
	withRole := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "as a reviewer", To: []string{"cody"},
		PrincipalRef: "agent:mable", ScopeRef: "mable@proj:" + f.loneTaskID + "/reviewer",
	})
	roleScope := withRole.Envelopes[0].From.ScopeRef
	if roleScope == nil || !strings.HasSuffix(*roleScope, "/reviewer") {
		t.Fatalf("role suffix was stripped as a lane: %v", roleScope)
	}
}

// TestTaskRoomAndCampaignRoomAreLinkedNeverMerged proves the §3.1 rule for a
// task that later joins a campaign: new says route to the campaign room, the
// task room stays readable, and the two are linked.
func TestTaskRoomAndCampaignRoomAreLinkedNeverMerged(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	// Talk on the task BEFORE it joins the campaign.
	before := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "before enrolment", PrincipalRef: "agent:clod"})
	if before.Room.Kind != "task" {
		t.Fatalf("pre-enrolment room kind = %s", before.Room.Kind)
	}

	if _, err := f.s.DB().Exec(
		"UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", f.campaignUUID, f.loneTaskUUID); err != nil {
		t.Fatalf("enrol task: %v", err)
	}

	after := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "after enrolment", PrincipalRef: "agent:clod"})
	if after.Room.Kind != "campaign" {
		t.Fatalf("post-enrolment say routed to %s, want campaign", after.Room.Kind)
	}

	// The old room is still readable and still holds its history.
	log, err := f.api.RoomLogView(ctx, RoomLogViewParams{Room: f.loneTaskID})
	if err != nil {
		t.Fatalf("logView on the task room: %v", err)
	}
	if len(log.Items) != 1 || log.Items[0].Body != "before enrolment" {
		t.Fatalf("task room lost its history: %+v", log.Items)
	}
	if len(log.Room.Links) != 1 || log.Room.Links[0].Relation != "coalesced_into" {
		t.Fatalf("task room is not linked to the campaign room: %+v", log.Room.Links)
	}
	if log.Room.Links[0].Key != f.campaignPath {
		t.Fatalf("link points at %q, want %q", log.Room.Links[0].Key, f.campaignPath)
	}
}

// TestCampaignRoomLogNarrowsByTask proves the tag survives coalesce well enough
// to read back: --task narrows a campaign room to one task's traffic.
func TestCampaignRoomLogNarrowsByTask(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	f.say(t, RoomSayParams{Ref: f.memberTaskID, Body: "through the member task", PrincipalRef: "agent:clod"})
	f.say(t, RoomSayParams{Ref: f.campaignPath, Body: "campaign-level", PrincipalRef: "agent:clod"})

	all, err := f.api.RoomLogView(ctx, RoomLogViewParams{Room: f.campaignPath})
	if err != nil {
		t.Fatalf("logView: %v", err)
	}
	if len(all.Items) != 2 {
		t.Fatalf("campaign room holds %d messages, want 2", len(all.Items))
	}
	narrowed, err := f.api.RoomLogView(ctx, RoomLogViewParams{Room: f.campaignPath, Task: f.memberTaskID})
	if err != nil {
		t.Fatalf("logView --task: %v", err)
	}
	if len(narrowed.Items) != 1 || narrowed.Items[0].Body != "through the member task" {
		t.Fatalf("--task narrowing = %+v", narrowed.Items)
	}
}
