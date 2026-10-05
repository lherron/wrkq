//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// §3.4: room activity on the event ledger, and the monitor reads over it.

// TestEventsEmittedForEachTransition proves §3.4: room, member, and envelope
// activity rides the EXISTING wrkq event ledger with the documented kinds.
func TestEventsEmittedForEachTransition(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	codySeat := "cody@proj:" + f.loneTaskID

	ask := f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "trace me", To: []string{"cody"}, PrincipalRef: "agent:clod",
	})
	f.present(t, ask.Envelopes[0].ID)
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
	if _, err := f.api.RoomHide(ctx, RoomLabelParams{Room: f.loneTaskID, PrincipalRef: "agent:clod"}); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if _, err := f.api.RoomUnhide(ctx, RoomLabelParams{Room: f.loneTaskID, PrincipalRef: "agent:clod"}); err != nil {
		t.Fatalf("unhide: %v", err)
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
		"room.opened", "room.hidden", "room.unhidden",
		"envelope.created", "envelope.presented", "envelope.deferred", "envelope.acked",
		"member.joined", "member.left",
	} {
		if !seen[want] {
			t.Errorf("no %s event was emitted", want)
		}
	}
	// The lifecycle events are GONE, not merely unused: a consumer keying on one
	// would be waiting forever, so nothing may emit them.
	for _, gone := range []string{"room.closed", "room.reopened", "room.archived"} {
		if seen[gone] {
			t.Errorf("%s was emitted; room lifecycle events are removed", gone)
		}
	}
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
// terminal counts failed so a failed obligation releases the waiter rather
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

	// Dispose one by reply-is-ack and one by failure; terminal covers both.
	f.present(t, group.Envelopes[0].ID)
	f.say(t, RoomSayParams{
		Ref: f.loneTaskID, Body: "done", To: []string{"clod"},
		PrincipalRef: "agent:cody", ScopeRef: *group.Envelopes[0].To.ScopeRef,
	})
	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{
		Envelope: group.Envelopes[1].ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-2",
	}); err != nil {
		t.Fatalf("present 2: %v", err)
	}
	if _, err := f.api.EnvelopeFail(ctx, EnvelopeFailParams{
		Envelope: group.Envelopes[1].ID, Reason: "ignored", Runtime: "rt-2", PrincipalRef: "agent:hrc",
	}); err != nil {
		t.Fatalf("fail: %v", err)
	}

	met, err := f.api.MonitorStateView(ctx, MonitorStateViewParams{Tasks: []string{head}, Condition: "terminal"})
	if err != nil {
		t.Fatalf("stateView after disposal: %v", err)
	}
	if !met.Met {
		t.Fatalf("group not terminal after ack+failed: %+v", met)
	}
	// acked is STRICTER than terminal: the failed sibling keeps it unmet.
	ackedOnly, err := f.api.MonitorStateView(ctx, MonitorStateViewParams{Tasks: []string{head}, Condition: "acked"})
	if err != nil {
		t.Fatalf("stateView acked: %v", err)
	}
	if ackedOnly.Met {
		t.Fatal("--until acked was met with a failed member")
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
