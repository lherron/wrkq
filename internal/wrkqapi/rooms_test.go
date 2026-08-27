//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/store"
)

// Test bundle 1 of T-07612 §15: the ledger and its routing table. Every case
// here is a behavioral claim about the collaboration ledger ALONE — no HRC
// daemon exists in this process, which is itself the point (§2: wrkc and the
// ledger must work with every HRC daemon down).

// roomFixture seeds a project, a campaign container, and tasks in each so the
// §4 routing table has real work to route against.
type roomFixture struct {
	api  *API
	s    *store.Store
	proj string // project container uuid, slug "proj"

	loneTaskID   string // a task NOT in a campaign
	loneTaskUUID string

	campaignUUID string // a campaign-adorned container under the project
	campaignPath string

	memberTaskID   string // a task inside the campaign
	memberTaskUUID string

	plainContainerPath string // a directory: has no room and must be refused
}

func newRoomFixture(t *testing.T) *roomFixture {
	t.Helper()
	api, s := newMonitorAPI(t)
	f := &roomFixture{api: api, s: s, proj: seedMonitorProject(t, s)}

	lone, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "lone", Title: "Lone", ProjectUUID: f.proj, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create lone task: %v", err)
	}
	f.loneTaskID, f.loneTaskUUID = lone.ID, lone.UUID

	campaign, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{
		Slug: "wave", Kind: "feature", ParentUUID: &f.proj,
	})
	if err != nil {
		t.Fatalf("create campaign container: %v", err)
	}
	f.campaignUUID, f.campaignPath = campaign.UUID, "proj/wave"
	if _, err := s.DB().Exec("UPDATE containers SET campaign_state = 'active' WHERE uuid = ?", f.campaignUUID); err != nil {
		t.Fatalf("adorn campaign: %v", err)
	}

	member, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "member", Title: "Member", ProjectUUID: f.proj, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create member task: %v", err)
	}
	f.memberTaskID, f.memberTaskUUID = member.ID, member.UUID
	if _, err := s.DB().Exec("UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", f.campaignUUID, f.memberTaskUUID); err != nil {
		t.Fatalf("enroll member task: %v", err)
	}

	plain, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{
		Slug: "notes", Kind: "directory", ParentUUID: &f.proj,
	})
	if err != nil {
		t.Fatalf("create plain container: %v", err)
	}
	_ = plain
	f.plainContainerPath = "proj/notes"
	return f
}

func (f *roomFixture) say(t *testing.T, p RoomSayParams) *WrkqRoomSayResult {
	t.Helper()
	result, err := f.api.RoomSay(context.Background(), p)
	if err != nil {
		t.Fatalf("RoomSay(%+v): %v", p, err)
	}
	return result
}

func assertDomainCode(t *testing.T, want string, err error) *DomainError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s, got nil error", want)
	}
	de, ok := err.(*DomainError)
	if !ok {
		t.Fatalf("expected *DomainError %s, got %T: %v", want, err, err)
	}
	if de.Code() != want {
		t.Fatalf("code = %s, want %s (%v)", de.Code(), want, err)
	}
	return de
}

// ─── §4 routing table ─────────────────────────────────────────────────────────

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
	// The most recent pair room is `forced`; close it so the only candidate left
	// is the widened group room, and prove the group room is not reused.
	if _, err := f.api.RoomClose(ctx, RoomLifecycleParams{Room: *forced.Room.ID, PrincipalRef: "agent:clod"}); err != nil {
		t.Fatalf("close forced room: %v", err)
	}
	fresh := f.say(t, RoomSayParams{
		Ref: sender.Ref, Body: "four", PrincipalRef: sender.PrincipalRef, ScopeRef: sender.ScopeRef,
	})
	if fresh.Room.UUID == first.Room.UUID {
		t.Fatal("a widened group room was reused as a pair room")
	}
	if fresh.Room.UUID == forced.Room.UUID {
		t.Fatal("a closed room was reused")
	}
}

// ─── closure ──────────────────────────────────────────────────────────────────

// TestClosedRoomRefusesSay proves both closure paths refuse a say with a typed
// error naming the state: an explicit close, and the DERIVED closure a task room
// inherits when its task goes terminal. Reopen clears the derived one.
func TestClosedRoomRefusesSay(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	opened := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "open for business", PrincipalRef: "agent:clod"})
	if opened.Room.State != "open" {
		t.Fatalf("new task room state = %s", opened.Room.State)
	}

	if _, err := f.api.RoomClose(ctx, RoomLifecycleParams{Room: f.loneTaskID, PrincipalRef: "agent:clod"}); err != nil {
		t.Fatalf("RoomClose: %v", err)
	}
	_, err := f.api.RoomSay(ctx, RoomSayParams{Ref: f.loneTaskID, Body: "after close", PrincipalRef: "agent:clod"})
	de := assertDomainCode(t, CodeWrongState, err)
	if !strings.Contains(fmt.Sprint(de.Data()), "closed") {
		t.Fatalf("closed-room refusal does not name the state: %v", de.Data())
	}

	if _, err := f.api.RoomReopen(ctx, RoomLifecycleParams{Room: f.loneTaskID, PrincipalRef: "agent:clod"}); err != nil {
		t.Fatalf("RoomReopen: %v", err)
	}
	f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "after reopen", PrincipalRef: "agent:clod"})

	// Derived closure: complete the task and the room reads closed without a
	// stored transition. The stored state stays open, which is how a caller can
	// tell the two apart.
	if _, err := f.api.TaskUpdate(ctx, TaskUpdateParams{Task: f.loneTaskID, Patch: TaskPatch{State: strp("completed")}}); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	// An explicit reopen is still in force from above, so clear it first.
	if _, err := f.api.RoomClose(ctx, RoomLifecycleParams{Room: f.loneTaskID, PrincipalRef: "agent:clod"}); err != nil {
		t.Fatalf("close before derived check: %v", err)
	}
	shown, err := f.api.RoomShow(ctx, RoomShowParams{Room: f.loneTaskID})
	if err != nil {
		t.Fatalf("RoomShow: %v", err)
	}
	if shown.State != "closed" {
		t.Fatalf("terminal task's room state = %s, want closed", shown.State)
	}
}

// TestDerivedClosureFromTerminalTaskNeedsNoStoredTransition isolates the derived
// half: a task room whose task completed reads closed while its STORED state is
// still open, and reopen overrides it.
func TestDerivedClosureFromTerminalTaskNeedsNoStoredTransition(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "hello", PrincipalRef: "agent:clod"})

	if _, err := f.api.TaskUpdate(ctx, TaskUpdateParams{Task: f.loneTaskID, Patch: TaskPatch{State: strp("cancelled")}}); err != nil {
		t.Fatalf("cancel task: %v", err)
	}
	shown, err := f.api.RoomShow(ctx, RoomShowParams{Room: f.loneTaskID})
	if err != nil {
		t.Fatalf("RoomShow: %v", err)
	}
	if shown.State != "closed" || shown.StoredState != "open" {
		t.Fatalf("derived closure = state %s / stored %s, want closed/open", shown.State, shown.StoredState)
	}
	if _, err := f.api.RoomSay(ctx, RoomSayParams{Ref: f.loneTaskID, Body: "after terminal", PrincipalRef: "agent:clod"}); err == nil {
		t.Fatal("say into a derived-closed room succeeded")
	}
	if _, err := f.api.RoomReopen(ctx, RoomLifecycleParams{Room: f.loneTaskID, PrincipalRef: "agent:clod"}); err != nil {
		t.Fatalf("RoomReopen: %v", err)
	}
	f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "reopened deliberately", PrincipalRef: "agent:clod"})
}

// ─── fan-out isolation ────────────────────────────────────────────────────────

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
	for _, envelope := range result.Envelopes {
		if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
			Envelope: envelope.ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-1",
		}); err != nil {
			t.Fatalf("present %s: %v", envelope.ID, err)
		}
	}

	// 1. defer one
	if _, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{
		Envelope: result.Envelopes[0].ID, Reason: "busy",
		PrincipalRef: "agent:cody", ScopeRef: *result.Envelopes[0].To.ScopeRef,
	}); err != nil {
		t.Fatalf("defer sibling 0: %v", err)
	}
	// 2. dead-letter another by exhausting its rounds
	for round := 0; round < 5; round++ {
		if _, err := f.api.EnvelopeRoundEnded(ctx, EnvelopeRoundParams{
			Envelope: result.Envelopes[1].ID, MaxRounds: 5, PrincipalRef: "agent:hrc",
		}); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
	}
	// 3. operator-ack the third
	if _, err := f.api.EnvelopeAck(ctx, EnvelopeAckParams{
		Envelopes: []string{result.Envelopes[2].ID}, PrincipalRef: "agent:lance",
	}); err != nil {
		t.Fatalf("operator ack sibling 2: %v", err)
	}

	want := map[string]string{
		result.Envelopes[0].ID: "deferred",
		result.Envelopes[1].ID: "dead",
		result.Envelopes[2].ID: "acked",
	}
	for envelopeID, wantState := range want {
		shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: envelopeID})
		if err != nil {
			t.Fatalf("show %s: %v", envelopeID, err)
		}
		if shown.State != wantState {
			t.Fatalf("envelope %s = %s, want %s (a sibling's disposition leaked)", envelopeID, shown.State, wantState)
		}
	}
}

// TestFYIPresentationAcksOnlyItsOwnEnvelope proves a fyi presented to ONE
// recipient stays pending for the others: fyi auto-acks at its own
// presentation, not at the group's.
func TestFYIPresentationAcksOnlyItsOwnEnvelope(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	result := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "heads up", To: []string{"cody", "mable"}, FYI: true,
		PrincipalRef: "agent:clod",
	})
	if len(result.Envelopes) != 2 {
		t.Fatalf("fyi fan-out wrote %d envelopes", len(result.Envelopes))
	}
	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: result.Envelopes[0].ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-1",
	}); err != nil {
		t.Fatalf("present fyi: %v", err)
	}
	first, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: result.Envelopes[0].ID})
	if err != nil {
		t.Fatalf("show fyi 0: %v", err)
	}
	if first.State != "acked" {
		t.Fatalf("presented fyi state = %s, want acked", first.State)
	}
	second, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: result.Envelopes[1].ID})
	if err != nil {
		t.Fatalf("show fyi 1: %v", err)
	}
	if second.State != "pending" {
		t.Fatalf("unpresented fyi sibling state = %s, want pending", second.State)
	}
}

// ─── obligations ──────────────────────────────────────────────────────────────

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

	for _, envelopeID := range []string{
		askA.Envelopes[0].ID, askB.Envelopes[0].ID, askC.Envelopes[0].ID,
		askMable.Envelopes[0].ID, askFromMable.Envelopes[0].ID,
	} {
		if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
			Envelope: envelopeID, PrincipalRef: "agent:hrc", RuntimeID: "rt-1",
		}); err != nil {
			t.Fatalf("present %s: %v", envelopeID, err)
		}
	}

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
	acked := map[string]bool{}
	for _, id := range reply.Acked {
		acked[id] = true
	}
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

	for envelopeID, want := range map[string]string{
		askA.Envelopes[0].ID:         "acked",
		askB.Envelopes[0].ID:         "acked",
		askC.Envelopes[0].ID:         "deferred",
		askMable.Envelopes[0].ID:     "presented",
		askFromMable.Envelopes[0].ID: "presented",
	} {
		shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: envelopeID})
		if err != nil {
			t.Fatalf("show %s: %v", envelopeID, err)
		}
		if shown.State != want {
			t.Fatalf("envelope %s = %s, want %s", envelopeID, shown.State, want)
		}
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

// TestDeferWithRetryIsPromiseBackedAndRepends proves §6's defer contract: the
// retry time is carried by a real wrkq promise, and when it comes due the
// envelope returns to PENDING so the kicker's next sweep re-drives it. Deferred
// is paused, never terminal.
func TestDeferWithRetryIsPromiseBackedAndRepends(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	codySeat := "cody@proj:" + f.loneTaskID

	ask := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "when you can", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: ask.Envelopes[0].ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-1",
	}); err != nil {
		t.Fatalf("present: %v", err)
	}

	deferred, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{
		Envelope: ask.Envelopes[0].ID, Reason: "after the build", RetryAfter: "2h",
		PrincipalRef: "agent:cody", ScopeRef: codySeat,
	})
	if err != nil {
		t.Fatalf("defer: %v", err)
	}
	if deferred.State != "deferred" || deferred.Terminal {
		t.Fatalf("deferred envelope = %s (terminal=%v); defer is PAUSED, never terminal", deferred.State, deferred.Terminal)
	}
	if deferred.RetryPromiseID == nil {
		t.Fatal("--retry-after did not arm a wrkq promise")
	}
	promise, err := f.api.PromiseShow(ctx, PromiseShowParams{Promise: *deferred.RetryPromiseID})
	if err != nil {
		t.Fatalf("show retry promise: %v", err)
	}
	if promise.OwnerPrincipalRef != "agent:cody" {
		t.Fatalf("retry promise owner = %q, want the deferring principal", promise.OwnerPrincipalRef)
	}

	// It is not due yet, so a sweep leaves it alone.
	pending, err := f.api.EnvelopePendingView(ctx, EnvelopePendingViewParams{
		Scopes: []string{codySeat}, PrincipalRef: "agent:hrc",
	})
	if err != nil {
		t.Fatalf("pendingView: %v", err)
	}
	if pending.Repended != 0 || len(pending.Items) != 0 {
		t.Fatalf("an undue deferral was re-pended: %+v", pending)
	}

	// Bring the retry time forward and sweep again.
	if _, err := f.s.DB().Exec(
		"UPDATE envelopes SET retry_at = '2000-01-01T00:00:00Z' WHERE id = ?", ask.Envelopes[0].ID,
	); err != nil {
		t.Fatalf("age the retry: %v", err)
	}
	swept, err := f.api.EnvelopePendingView(ctx, EnvelopePendingViewParams{
		Scopes: []string{codySeat}, PrincipalRef: "agent:hrc",
	})
	if err != nil {
		t.Fatalf("pendingView after aging: %v", err)
	}
	if swept.Repended != 1 {
		t.Fatalf("due deferral was not re-pended: %+v", swept)
	}
	if len(swept.Items) != 1 || swept.Items[0].State != "pending" {
		t.Fatalf("re-pended envelope = %+v, want one pending item", swept.Items)
	}
	// A re-pended envelope is not yet presented, so it does not block a turn end.
	if len(swept.Blocking) != 0 {
		t.Fatalf("a re-pended (unpresented) envelope blocks the stop hook: %v", swept.Blocking)
	}
	// The promise that carried the deferral is discharged.
	reloaded, err := f.api.PromiseShow(ctx, PromiseShowParams{Promise: *deferred.RetryPromiseID})
	if err != nil {
		t.Fatalf("reload promise: %v", err)
	}
	if reloaded.State != "resolved" {
		t.Fatalf("retry promise state = %s, want resolved", reloaded.State)
	}
}

// TestDeferredEnvelopeStillAckableByALaterReply proves the other half of "paused,
// never terminal": deferred → acked by a later reply is legal.
func TestDeferredEnvelopeStillAckableByALaterReply(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	codySeat := "cody@proj:" + f.loneTaskID

	ask := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "eventually", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: ask.Envelopes[0].ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-1",
	}); err != nil {
		t.Fatalf("present: %v", err)
	}
	if _, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{
		Envelope: ask.Envelopes[0].ID, Reason: "later", PrincipalRef: "agent:cody", ScopeRef: codySeat,
	}); err != nil {
		t.Fatalf("defer: %v", err)
	}
	// Re-present (the kicker's sweep would) and reply.
	if _, err := f.s.DB().Exec("UPDATE envelopes SET state = 'presented' WHERE id = ?", ask.Envelopes[0].ID); err != nil {
		t.Fatalf("re-present: %v", err)
	}
	reply := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "here you go", To: []string{"clod"},
		PrincipalRef: "agent:cody", ScopeRef: codySeat,
	})
	if len(reply.Acked) != 1 || reply.Acked[0] != ask.Envelopes[0].ID {
		t.Fatalf("a previously deferred envelope was not acked by the later reply: %v", reply.Acked)
	}
}

// TestRoundsDeadLetterVisiblyAndNoOpTurnsDoNotAdvance carries T-06810's backstop
// unchanged: only a still-PRESENTED envelope advances on a completed kicker
// turn, exhaustion lands in a visible dead state, and a clear-inbox no-op turn
// never burns a round.
func TestRoundsDeadLetterVisiblyAndNoOpTurnsDoNotAdvance(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	ask := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "answer me", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	envelopeID := ask.Envelopes[0].ID

	// A pending (never presented) envelope does not advance: that is the
	// clear-inbox no-op turn.
	noop, err := f.api.EnvelopeRoundEnded(ctx, EnvelopeRoundParams{Envelope: envelopeID, PrincipalRef: "agent:hrc"})
	if err != nil {
		t.Fatalf("no-op round: %v", err)
	}
	if noop.RoundCount != 0 || noop.State != "pending" {
		t.Fatalf("a no-op turn advanced rounds: %+v", noop)
	}

	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: envelopeID, PrincipalRef: "agent:hrc", RuntimeID: "rt-1",
	}); err != nil {
		t.Fatalf("present: %v", err)
	}
	for round := 1; round <= 5; round++ {
		got, rerr := f.api.EnvelopeRoundEnded(ctx, EnvelopeRoundParams{
			Envelope: envelopeID, MaxRounds: 5, PrincipalRef: "agent:hrc",
		})
		if rerr != nil {
			t.Fatalf("round %d: %v", round, rerr)
		}
		if got.RoundCount != int64(round) {
			t.Fatalf("round %d recorded count %d", round, got.RoundCount)
		}
		wantState := "presented"
		if round == 5 {
			wantState = "dead"
		}
		if got.State != wantState {
			t.Fatalf("after round %d state = %s, want %s", round, got.State, wantState)
		}
	}
	// Dead is terminal and visible: it shows up under the dead heading.
	inbox, err := f.api.EnvelopeInboxView(ctx, EnvelopeInboxViewParams{
		PrincipalRef: "agent:cody", ScopeRef: "cody@proj:" + f.loneTaskID, IncludeDead: true,
	})
	if err != nil {
		t.Fatalf("inbox: %v", err)
	}
	if len(inbox.Dead) != 1 || inbox.Dead[0].ID != envelopeID {
		t.Fatalf("dead-lettered envelope is not visible: %+v", inbox.Dead)
	}
	if len(inbox.Groups) != 0 {
		t.Fatalf("a dead envelope still stands as an obligation: %+v", inbox.Groups)
	}
}

// TestPresentationReceiptAndHistoryHintKeyedToRuntime proves §7's cue rule: the
// hint is keyed to the RUNTIME, not the generation. /quit clears continuation
// without rotating the generation, so a second runtime inside the SAME
// generation is cold and gets the cue; a warm runtime's second message does not.
func TestPresentationReceiptAndHistoryHintKeyedToRuntime(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	first := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "one", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	second := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "two", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	third := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "three", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})

	// Runtime A's first presentation in this room: the room already has prior
	// messages, so the cue fires.
	firstPresent, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: first.Envelopes[0].ID, PrincipalRef: "agent:hrc",
		Node: "mini", RuntimeID: "runtime-A", Generation: "49", RunID: "run-1",
	})
	if err != nil {
		t.Fatalf("present 1: %v", err)
	}
	if !firstPresent.HistoryHint {
		t.Fatal("a cold runtime's first presentation did not get the history cue")
	}

	// Same runtime, second message: warm, no cue.
	warm, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: second.Envelopes[0].ID, PrincipalRef: "agent:hrc",
		Node: "mini", RuntimeID: "runtime-A", Generation: "49", RunID: "run-2",
	})
	if err != nil {
		t.Fatalf("present 2: %v", err)
	}
	if warm.HistoryHint {
		t.Fatal("a warm runtime got the history cue on its second message")
	}

	// A NEW runtime inside the SAME generation (the post-/quit case) is cold.
	postQuit, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: third.Envelopes[0].ID, PrincipalRef: "agent:hrc",
		Node: "mini", RuntimeID: "runtime-B", Generation: "49", RunID: "run-3",
	})
	if err != nil {
		t.Fatalf("present 3: %v", err)
	}
	if !postQuit.HistoryHint {
		t.Fatal("a post-/quit runtime sharing its generation did not get the history cue")
	}

	// The receipt is durable and carries the HRC identifiers verbatim.
	shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: first.Envelopes[0].ID})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if len(shown.PresentedTo) != 1 {
		t.Fatalf("presented_to = %+v, want one receipt", shown.PresentedTo)
	}
	receipt := shown.PresentedTo[0]
	if receipt.Node == nil || *receipt.Node != "mini" ||
		receipt.RuntimeID == nil || *receipt.RuntimeID != "runtime-A" ||
		receipt.Generation == nil || *receipt.Generation != "49" {
		t.Fatalf("receipt lost HRC identifiers: %+v", receipt)
	}
}

// TestPresentationIsExactlyOncePerDriveAttempt proves the at-least-once
// presentation residual is bounded: one driveAttemptId presents an envelope
// exactly once, however many times the kicker retries the call.
func TestPresentationIsExactlyOncePerDriveAttempt(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	ask := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "once", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	for attempt := 0; attempt < 3; attempt++ {
		result, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
			Envelope: ask.Envelopes[0].ID, PrincipalRef: "agent:hrc",
			RuntimeID: "rt-1", DriveAttemptID: "drive-1",
		})
		if err != nil {
			t.Fatalf("present attempt %d: %v", attempt, err)
		}
		if attempt > 0 && result.Recorded {
			t.Fatalf("attempt %d recorded a duplicate presentation for one drive attempt", attempt)
		}
	}
	shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: ask.Envelopes[0].ID})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if len(shown.PresentedTo) != 1 {
		t.Fatalf("one drive attempt wrote %d receipts", len(shown.PresentedTo))
	}
}

// TestStopHookPredicateCountsOnlyPresentedObligations proves §8's predicate
// shape: a turn end is refused only for what was actually PRESENTED and left
// neither replied nor deferred.
func TestStopHookPredicateCountsOnlyPresentedObligations(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	codySeat := "cody@proj:" + f.loneTaskID

	presented := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "blocking", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	pendingOnly := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "not yet presented", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	fyi := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "no obligation", To: []string{"cody"}, FYI: true, PrincipalRef: "agent:clod",
	})
	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: presented.Envelopes[0].ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-1",
	}); err != nil {
		t.Fatalf("present: %v", err)
	}
	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: fyi.Envelopes[0].ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-1",
	}); err != nil {
		t.Fatalf("present fyi: %v", err)
	}

	view, err := f.api.EnvelopePendingView(ctx, EnvelopePendingViewParams{
		Scopes: []string{codySeat}, PrincipalRef: "agent:hrc",
	})
	if err != nil {
		t.Fatalf("pendingView: %v", err)
	}
	if len(view.Blocking) != 1 || view.Blocking[0] != presented.Envelopes[0].ID {
		t.Fatalf("stop-hook predicate = %v, want only the presented obligation", view.Blocking)
	}
	// The wake set is broader than the predicate: the kicker still has work.
	ids := map[string]bool{}
	for _, item := range view.Items {
		ids[item.ID] = true
	}
	if !ids[pendingOnly.Envelopes[0].ID] {
		t.Fatal("the kicker wake set omitted a pending obligation")
	}
	if ids[fyi.Envelopes[0].ID] {
		t.Fatal("a fyi envelope entered the kicker wake set; fyi never summons")
	}
}

// ─── events ───────────────────────────────────────────────────────────────────

// TestEventsEmittedForEachTransition proves §3.4: room, member, and envelope
// activity rides the EXISTING wrkq event ledger with the documented kinds.
func TestEventsEmittedForEachTransition(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	codySeat := "cody@proj:" + f.loneTaskID

	ask := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "trace me", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: ask.Envelopes[0].ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-1",
	}); err != nil {
		t.Fatalf("present: %v", err)
	}
	if _, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{
		Envelope: ask.Envelopes[0].ID, Reason: "later", PrincipalRef: "agent:cody", ScopeRef: codySeat,
	}); err != nil {
		t.Fatalf("defer: %v", err)
	}
	if _, err := f.api.EnvelopeAck(ctx, EnvelopeAckParams{
		Envelopes: []string{ask.Envelopes[0].ID}, PrincipalRef: "agent:lance",
	}); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if _, err := f.api.RoomClose(ctx, RoomLifecycleParams{Room: f.loneTaskID, PrincipalRef: "agent:clod"}); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := f.api.RoomReopen(ctx, RoomLifecycleParams{Room: f.loneTaskID, PrincipalRef: "agent:clod"}); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := f.api.RoomLeave(ctx, RoomMemberParams{
		Room: f.loneTaskID, Member: codySeat, PrincipalRef: "agent:cody",
	}); err != nil {
		t.Fatalf("leave: %v", err)
	}

	seen := map[string]bool{}
	rows, err := f.s.DB().Query(
		"SELECT event_type, resource_type FROM event_log WHERE resource_type IN ('room','envelope')")
	if err != nil {
		t.Fatalf("read events: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var eventType, resourceType string
		if err := rows.Scan(&eventType, &resourceType); err != nil {
			t.Fatalf("scan event: %v", err)
		}
		seen[eventType] = true
		// member.* rides the ROOM: membership has no addressable identity of its
		// own, and watching a room must show joins.
		if strings.HasPrefix(eventType, "member.") && resourceType != "room" {
			t.Fatalf("%s logged against resource_type %q, want room", eventType, resourceType)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate events: %v", err)
	}
	for _, want := range []string{
		"room.opened", "room.closed", "room.reopened",
		"envelope.created", "envelope.presented", "envelope.deferred", "envelope.acked",
		"member.joined", "member.left",
	} {
		if !seen[want] {
			t.Errorf("no %s event was emitted", want)
		}
	}
}

// TestMonitorWatchTaskSelectorCarriesItsConversation proves §3.4's headline
// claim: because a task room's key IS the task id, one selector shows the task's
// state changes AND its conversation — and --state-only still excludes the talk.
func TestMonitorWatchTaskSelectorCarriesItsConversation(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "conversation", To: []string{"cody"}, PrincipalRef: "agent:clod"})
	if _, err := f.api.TaskUpdate(ctx, TaskUpdateParams{
		Task: f.loneTaskID, Patch: TaskPatch{State: strp("in_progress")},
	}); err != nil {
		t.Fatalf("update task: %v", err)
	}

	view, err := f.api.MonitorEventsView(ctx, MonitorEventsViewParams{Tasks: []string{f.loneTaskID}})
	if err != nil {
		t.Fatalf("eventsView: %v", err)
	}
	kinds := map[string]bool{}
	for _, event := range view.Items {
		kinds[event.ResourceType] = true
	}
	if !kinds["task"] || !kinds["envelope"] {
		t.Fatalf("one task selector did not carry both state and conversation: %v", kinds)
	}

	stateOnly, err := f.api.MonitorEventsView(ctx, MonitorEventsViewParams{
		Tasks: []string{f.loneTaskID}, StateOnly: true,
	})
	if err != nil {
		t.Fatalf("eventsView --state-only: %v", err)
	}
	for _, event := range stateOnly.Items {
		if event.ResourceType != "task" {
			t.Fatalf("--state-only emitted a %s event: %+v", event.ResourceType, event)
		}
	}
}

// TestMonitorWaitUntilTerminalOverAGroup proves the §5 `--wait` mechanism: an
// EN- selector that is a fan-out group head evaluates over the WHOLE group, and
// terminal counts dead so a dead-lettered obligation releases the waiter rather
// than hanging it.
func TestMonitorWaitUntilTerminalOverAGroup(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	group := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "both of you", To: []string{"cody", "mable"}, PrincipalRef: "agent:clod",
	})
	head := group.GroupID

	snapshot, err := f.api.MonitorStateView(ctx, MonitorStateViewParams{
		Tasks: []string{head}, Condition: "terminal",
	})
	if err != nil {
		t.Fatalf("stateView: %v", err)
	}
	if snapshot.Met || len(snapshot.Unmet) != 2 {
		t.Fatalf("group terminal snapshot = %+v, want unmet for both members", snapshot)
	}

	// Dispose one by reply-is-ack and one by dead-letter; terminal covers both.
	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: group.Envelopes[0].ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-1",
	}); err != nil {
		t.Fatalf("present: %v", err)
	}
	f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "done", To: []string{"clod"},
		PrincipalRef: "agent:cody", ScopeRef: *group.Envelopes[0].To.ScopeRef,
	})
	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: group.Envelopes[1].ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-2",
	}); err != nil {
		t.Fatalf("present 2: %v", err)
	}
	for round := 0; round < 5; round++ {
		if _, err := f.api.EnvelopeRoundEnded(ctx, EnvelopeRoundParams{
			Envelope: group.Envelopes[1].ID, MaxRounds: 5, PrincipalRef: "agent:hrc",
		}); err != nil {
			t.Fatalf("round: %v", err)
		}
	}

	met, err := f.api.MonitorStateView(ctx, MonitorStateViewParams{Tasks: []string{head}, Condition: "terminal"})
	if err != nil {
		t.Fatalf("stateView after disposal: %v", err)
	}
	if !met.Met {
		t.Fatalf("group not terminal after ack+dead: %+v", met)
	}
	// acked is STRICTER than terminal: the dead sibling keeps it unmet.
	ackedOnly, err := f.api.MonitorStateView(ctx, MonitorStateViewParams{Tasks: []string{head}, Condition: "acked"})
	if err != nil {
		t.Fatalf("stateView acked: %v", err)
	}
	if ackedOnly.Met {
		t.Fatal("--until acked was met with a dead-lettered member")
	}

	// A sibling's own id is nobody's group id, so it selects only itself.
	sibling, err := f.api.MonitorStateView(ctx, MonitorStateViewParams{
		Tasks: []string{group.Envelopes[1].ID}, Condition: "acked",
	})
	if err != nil {
		t.Fatalf("stateView sibling: %v", err)
	}
	if len(sibling.Unmet) != 1 || sibling.Unmet[0] != group.Envelopes[1].ID {
		t.Fatalf("sibling selector widened to the group: %+v", sibling)
	}
}

// TestMonitorConditionAndSelectorsMustAgree proves the guard that keeps `unmet`
// meaningful: task conditions take task selectors, envelope conditions take
// envelope selectors, and mixing them is a validation error.
func TestMonitorConditionAndSelectorsMustAgree(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	group := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "x", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})

	_, err := f.api.MonitorStateView(ctx, MonitorStateViewParams{
		Tasks: []string{f.loneTaskID}, Condition: "terminal",
	})
	_ = assertDomainCode(t, CodeValidation, err)

	_, err = f.api.MonitorStateView(ctx, MonitorStateViewParams{
		Tasks: []string{group.Envelopes[0].ID}, Condition: "state=completed",
	})
	_ = assertDomainCode(t, CodeValidation, err)
}

// ─── membership & record ──────────────────────────────────────────────────────

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

// TestEnvelopeDispositionRequiresTheAddressee carries T-06810's hygiene rule:
// the envelope's target must equal the claimed scope, so a typo cannot dispose
// somebody else's obligation.
func TestEnvelopeDispositionRequiresTheAddressee(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	ask := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "for cody", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	_, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{
		Envelope: ask.Envelopes[0].ID, Reason: "not mine to defer",
		PrincipalRef: "agent:mable", ScopeRef: "mable@proj:" + f.loneTaskID,
	})
	_ = assertDomainCode(t, CodeForbidden, err)
}

// TestWebhookPayloadShapeForEnvelopeCreated pins the wake signal wave 3's kicker
// subscribes to, without asserting delivery (which is best-effort and async).
func TestWebhookPayloadShapeForEnvelopeCreated(t *testing.T) {
	f := newRoomFixture(t)

	result := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "wake up", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	var payload string
	if err := f.s.DB().QueryRow(`SELECT payload FROM event_log
		 WHERE resource_type = 'envelope' AND event_type = 'envelope.created'
		 ORDER BY id DESC LIMIT 1`).Scan(&payload); err != nil {
		t.Fatalf("read envelope.created: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	for _, key := range []string{"id", "room_uuid", "obligation", "state", "to_scope_ref", "from_principal_ref"} {
		if _, ok := event[key]; !ok {
			t.Errorf("envelope.created payload is missing %q: %v", key, event)
		}
	}
	if event["id"] != result.Envelopes[0].ID {
		t.Fatalf("payload id = %v, want %s", event["id"], result.Envelopes[0].ID)
	}
}

// TestMonitorHydratesRoomKeyForDerivedRooms proves the monitor feed is readable:
// a derived room has no friendly id, so its resource_id hydrates to the work
// identity that IS its key rather than to a blank column.
func TestMonitorHydratesRoomKeyForDerivedRooms(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "task room", PrincipalRef: "agent:clod"})
	f.say(t, RoomSayParams{Ref: f.campaignPath, Body: "campaign room", PrincipalRef: "agent:clod"})
	f.say(t, RoomSayParams{
		Ref: "cody@proj:primary", Body: "ad-hoc room",
		PrincipalRef: "agent:clod", ScopeRef: "clod@proj:primary",
	})

	view, err := f.api.MonitorEventsView(ctx, MonitorEventsViewParams{})
	if err != nil {
		t.Fatalf("eventsView: %v", err)
	}
	keys := map[string]bool{}
	for _, event := range view.Items {
		if event.ResourceType != "room" || event.EventType != "room.opened" {
			continue
		}
		if event.ResourceID == nil || *event.ResourceID == "" {
			t.Fatalf("room.opened has no hydrated key: %+v", event)
		}
		keys[*event.ResourceID] = true
	}
	if !keys[f.loneTaskID] {
		t.Fatalf("task room did not hydrate to its task id; got %v", keys)
	}
	if !keys[f.campaignPath] {
		t.Fatalf("campaign room did not hydrate to its container path; got %v", keys)
	}
	hasAdhoc := false
	for key := range keys {
		if strings.HasPrefix(key, "R-") {
			hasAdhoc = true
		}
	}
	if !hasAdhoc {
		t.Fatalf("ad-hoc room did not hydrate to its R- id; got %v", keys)
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

// TestClosedRoomRefusalNamesTheStateInItsMessage proves the refusal is legible
// without opening the data bag: "wrong_state" alone tells a caller nothing.
func TestClosedRoomRefusalNamesTheStateInItsMessage(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "open", PrincipalRef: "agent:clod"})
	if _, err := f.api.RoomClose(ctx, RoomLifecycleParams{Room: f.loneTaskID, PrincipalRef: "agent:clod"}); err != nil {
		t.Fatalf("close: %v", err)
	}
	_, err := f.api.RoomSay(ctx, RoomSayParams{Ref: f.loneTaskID, Body: "after", PrincipalRef: "agent:clod"})
	de := assertDomainCode(t, CodeWrongState, err)
	if !strings.Contains(de.Error(), f.loneTaskID) || !strings.Contains(de.Error(), "closed") {
		t.Fatalf("refusal message does not name the room and its state: %q", de.Error())
	}
}
