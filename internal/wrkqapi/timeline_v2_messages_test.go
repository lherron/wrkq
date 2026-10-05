//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lherron/wrkq/internal/domain"
)

// Room messages in the v2 timeline: fan-out collapse, the message type, and
// which projects a room's messages affiliate with.

// TestTimelineCollapsesSayFanOut is the fan-out collapse (T-08358 D2). One say
// to three addressees writes THREE envelope rows sharing one group id; the
// timeline reports the MESSAGE, so it must deliver exactly one entry naming all
// three. Removing the leader predicate from loadTimelineRawEvents' envelope join
// fails this with 3 entries — that is the defect this test exists to catch.
func TestTimelineCollapsesSayFanOut(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "fanout", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "target", "open", "")

	said, err := api.RoomSay(context.Background(), RoomSayParams{
		Ref:  task.ID,
		Body: "three addressees, one message",
		To: []string{
			"cody@fanout:" + task.ID,
			"astra@fanout:" + task.ID,
			"mable@fanout:" + task.ID,
		},
		PrincipalRef: "agent:clod",
	})
	if err != nil {
		t.Fatalf("RoomSay: %v", err)
	}
	if len(said.Envelopes) != 3 {
		t.Fatalf("precondition: fan-out wrote %d envelopes, want 3", len(said.Envelopes))
	}

	view := timelineView(t, api, ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true, Types: []string{"message"}, Limit: 100,
	})
	if len(view.Entries) != 1 {
		t.Fatalf("one say to three addressees delivered %d entries, want exactly 1: %#v",
			len(view.Entries), view.Entries)
	}
	message := view.Entries[0].Message
	if message == nil {
		t.Fatalf("message entry carries no message detail: %#v", view.Entries[0])
	}
	if len(message.To) != 3 {
		t.Fatalf("collapsed entry names %d addressees, want all 3: %#v", len(message.To), message.To)
	}
	for _, want := range []string{"astra@fanout:" + task.ID, "cody@fanout:" + task.ID, "mable@fanout:" + task.ID} {
		found := false
		for _, got := range message.To {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("addressee %s missing from the collapsed entry: %#v", want, message.To)
		}
	}
	if message.Body != "three addressees, one message" {
		t.Fatalf("message body = %q", message.Body)
	}
	if message.From != "agent:clod" && message.From != "clod" {
		t.Fatalf("message from = %q, want the sender", message.From)
	}
	if view.Entries[0].TaskID != task.ID {
		t.Fatalf("message is not tagged with its task: %#v", view.Entries[0])
	}
}

// TestTimelineTypeFilterSelectsMessages proves the `message` type filter BITES:
// an unfiltered read carries both kinds, --type message narrows to the message,
// and a bogus selector returns nothing. Without the last arm a filter that
// matched everything would pass the first two.
func TestTimelineTypeFilterSelectsMessages(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "filter", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "target", "open", "")

	if _, err := api.RoomSay(context.Background(), RoomSayParams{
		Ref: task.ID, Body: "a room message", To: []string{"cody@filter:" + task.ID},
		PrincipalRef: "agent:clod",
	}); err != nil {
		t.Fatalf("RoomSay: %v", err)
	}
	setTaskState(t, api, task.ID, "completed")

	read := func(types []string) []WrkqTimelineEntry {
		t.Helper()
		view := timelineView(t, api, ContainerTimelineViewParams{
			Container: project.UUID, Scope: "subtree", EntriesOnly: true, Types: types, Limit: 100,
		})
		return view.Entries
	}

	all := read(nil)
	var sawMessage, sawState bool
	for _, entry := range all {
		switch entry.Type {
		case "message":
			sawMessage = true
		case "task.state":
			sawState = true
		}
	}
	if !sawMessage || !sawState {
		t.Fatalf("unfiltered read must carry both kinds (message=%v state=%v): %#v", sawMessage, sawState, all)
	}

	messages := read([]string{"message"})
	if len(messages) != 1 || messages[0].Type != "message" {
		t.Fatalf("--type message returned %#v, want exactly the one message", messages)
	}

	if bogus := read([]string{"no.such.type"}); len(bogus) != 0 {
		t.Fatalf("a bogus type filter returned %d entries; the filter does not bite", len(bogus))
	}
}

// TestTimelineExcludesUnstampedAdHocRooms pins the fail-closed legacy rule: an
// ad-hoc room has no ownership affiliation, so an unstamped message remains
// outside every project timeline rather than being guessed from current slugs.
func TestTimelineExcludesAdHocRooms(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "dmproj", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "anchor", "open", "")

	if _, err := api.RoomSay(context.Background(), RoomSayParams{
		Ref: task.ID, Body: "anchored to the task", To: []string{"cody@dmproj:" + task.ID},
		PrincipalRef: "agent:clod",
	}); err != nil {
		t.Fatalf("RoomSay(task room): %v", err)
	}
	if _, err := api.RoomSay(context.Background(), RoomSayParams{
		Ref: "cody@unresolved-project:primary", Body: "a direct message", PrincipalRef: "agent:clod",
	}); err != nil {
		t.Fatalf("RoomSay(dm): %v", err)
	}

	view := timelineView(t, api, ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true, Types: []string{"message"}, Limit: 100,
	})
	for _, entry := range view.Entries {
		if entry.Message != nil && entry.Message.Body == "a direct message" {
			t.Fatalf("an ad-hoc DM leaked into the project log: %#v", entry)
		}
	}
	if len(view.Entries) != 1 {
		t.Fatalf("want exactly the task-room message, got %d: %#v", len(view.Entries), view.Entries)
	}
}

func TestTimelineAdHocEndpointProjectAffiliationIsStampedAndImmutable(t *testing.T) {
	api, s := newMonitorAPI(t)
	alpha := createProjectEventContainer(t, s, "alpha", "project", nil)
	beta := createProjectEventContainer(t, s, "beta", "project", nil)
	gamma := createProjectEventContainer(t, s, "gamma", "project", nil)

	message := "ad-hoc endpoint affiliation"
	result, err := api.RoomSay(context.Background(), RoomSayParams{
		Ref:          "cody@alpha:primary",
		ScopeRef:     "clod@alpha:primary",
		PrincipalRef: "agent:clod",
		To:           []string{"cody@alpha:primary", "astra@beta:primary", "mable@alpha:primary"},
		Body:         message,
	})
	if err != nil {
		t.Fatalf("RoomSay(ad-hoc): %v", err)
	}
	if len(result.Envelopes) != 3 {
		t.Fatalf("fan-out envelopes = %d, want 3", len(result.Envelopes))
	}
	if stamped := countEnvelopeStamps(t, api, result.GroupID); stamped != 3 {
		t.Fatalf("stamped envelopes = %d, want 3", stamped)
	}

	read := func(project string) []WrkqTimelineEntry {
		t.Helper()
		return messagesWithBody(t, api, project, message)
	}
	assertParticipant := func(project string) {
		t.Helper()
		entries := read(project)
		if len(entries) != 1 {
			t.Fatalf("project %s messages = %#v, want one", project, entries)
		}
		entry := entries[0]
		if entry.Membership != "participant" || entry.TaskUUID != "" || entry.TaskID != "" || entry.TaskPath != "" {
			t.Fatalf("participant entry = %#v", entry)
		}
		if len(entry.Message.To) != 3 {
			t.Fatalf("fan-out addressees = %#v, want all three", entry.Message.To)
		}
	}
	assertParticipant(alpha.UUID)
	assertParticipant(beta.UUID)
	if entries := read(gamma.UUID); len(entries) != 0 {
		t.Fatalf("unrelated project received ad-hoc message: %#v", entries)
	}

	// Membership added after the fact is intentionally not a timeline authority.
	if result.Room.ID == nil {
		t.Fatal("ad-hoc room has no ID")
	}
	if _, err := api.RoomJoin(context.Background(), RoomMemberParams{
		Room: *result.Room.ID, Member: "nova@gamma:primary", PrincipalRef: "agent:clod",
	}); err != nil {
		t.Fatalf("RoomJoin: %v", err)
	}
	if entries := read(gamma.UUID); len(entries) != 0 {
		t.Fatalf("post-send join re-affiliated message: %#v", entries)
	}

	// Rename the original project, reuse its old slug for a new project, and
	// prove lookup never happens at read time.
	if _, err := api.ContainerUpdate(context.Background(), ContainerUpdateParams{
		Container: alpha.UUID, Patch: json.RawMessage(`{"slug":"alpha-renamed"}`), Actor: "agent:wrkq-system",
	}); err != nil {
		t.Fatalf("rename alpha: %v", err)
	}
	reused := createProjectEventContainer(t, s, "alpha", "project", nil)
	assertParticipant(alpha.UUID)
	if entries := read(reused.UUID); len(entries) != 0 {
		t.Fatalf("reused slug received historical message: %#v", entries)
	}

	// Simulate an old pre-stamp envelope. NULL means unknown history, not a
	// request to re-resolve scope text through today's project names.
	legacy, err := api.RoomSay(context.Background(), RoomSayParams{
		Ref: "cody@alpha-renamed:primary", ScopeRef: "clod@alpha-renamed:primary", PrincipalRef: "agent:clod",
		To: []string{"cody@alpha-renamed:primary"}, Body: "legacy unstamped message",
	})
	if err != nil {
		t.Fatalf("RoomSay(legacy seed): %v", err)
	}
	if _, err := api.db.Exec(`UPDATE envelopes SET from_project_uuid = NULL, to_project_uuid = NULL WHERE group_id = ?`, legacy.GroupID); err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{alpha.UUID, beta.UUID, gamma.UUID, reused.UUID} {
		if entries := messagesWithBody(t, api, project, "legacy unstamped message"); len(entries) != 0 {
			t.Fatalf("unstamped legacy message appeared in %s: %#v", project, entries)
		}
	}

	// Owned task-room history remains owned by its task/container, regardless of
	// cross-project endpoints; it cannot duplicate into endpoint timelines.
	task := createTimelineTask(t, s, alpha.UUID, "owned", "open", "")
	if _, err := api.RoomSay(context.Background(), RoomSayParams{
		Ref: task.ID, ScopeRef: "clod@beta:primary", PrincipalRef: "agent:clod",
		To: []string{"cody@gamma:primary"}, Body: "owned task-room message",
	}); err != nil {
		t.Fatalf("RoomSay(task room): %v", err)
	}
	var ownedStamps int
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM envelopes WHERE body = 'owned task-room message'
		AND (from_project_uuid IS NOT NULL OR to_project_uuid IS NOT NULL)`).Scan(&ownedStamps); err != nil {
		t.Fatal(err)
	}
	if ownedStamps != 0 {
		t.Fatalf("task-room endpoint stamps = %d, want 0", ownedStamps)
	}
	owned := func(project string) int {
		return len(messagesWithBody(t, api, project, "owned task-room message"))
	}
	if owned(alpha.UUID) != 1 || owned(beta.UUID) != 0 || owned(gamma.UUID) != 0 {
		t.Fatalf("task-room ownership precedence failed: alpha=%d beta=%d gamma=%d", owned(alpha.UUID), owned(beta.UUID), owned(gamma.UUID))
	}
}

func TestTimelineProjectRoomEndpointProjectAffiliationKeepsOwnerResident(t *testing.T) {
	api, s := newMonitorAPI(t)
	alpha := createProjectEventContainer(t, s, "alpha", "project", nil)
	beta := createProjectEventContainer(t, s, "beta", "project", nil)
	gamma := createProjectEventContainer(t, s, "gamma", "project", nil)
	delta := createProjectEventContainer(t, s, "delta", "project", nil)

	message := "project-room endpoint affiliation"
	result, err := api.RoomSay(context.Background(), RoomSayParams{
		Ref:          "alpha",
		ScopeRef:     "clod@beta:primary",
		PrincipalRef: "agent:clod",
		To:           []string{"cody@alpha:primary", "astra@beta:primary", "mable@gamma:primary"},
		Body:         message,
	})
	if err != nil {
		t.Fatalf("RoomSay(project room): %v", err)
	}
	if result.Room.Kind != string(domain.RoomKindProject) {
		t.Fatalf("room kind = %q, want project", result.Room.Kind)
	}
	if len(result.Envelopes) != 3 {
		t.Fatalf("fan-out envelopes = %d, want 3", len(result.Envelopes))
	}
	var stamped int
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM envelopes
		WHERE group_id = ? AND from_project_uuid IS NOT NULL AND to_project_uuid IS NOT NULL`, result.GroupID).Scan(&stamped); err != nil {
		t.Fatal(err)
	}
	if stamped != 3 {
		t.Fatalf("fully stamped envelopes = %d, want 3", stamped)
	}

	read := func(project string) []WrkqTimelineEntry {
		t.Helper()
		return messagesWithBody(t, api, project, message)
	}
	assertMembership := func(project, membership string) {
		t.Helper()
		entries := read(project)
		if len(entries) != 1 {
			t.Fatalf("project %s messages = %#v, want one", project, entries)
		}
		entry := entries[0]
		if entry.Membership != membership || entry.TaskUUID != "" || entry.TaskID != "" || entry.TaskPath != "" {
			t.Fatalf("project %s entry = %#v, want membership %q without task", project, entry, membership)
		}
		if len(entry.Message.To) != 3 {
			t.Fatalf("fan-out addressees = %#v, want all three", entry.Message.To)
		}
	}
	assertMembership(alpha.UUID, "resident")
	assertMembership(beta.UUID, "participant")
	assertMembership(gamma.UUID, "participant")
	if entries := read(delta.UUID); len(entries) != 0 {
		t.Fatalf("unrelated project received project-room message: %#v", entries)
	}

	// The owning project keeps its stored identity after a rename; reused slugs
	// and endpoint scope text cannot re-home this historical message.
	if _, err := api.ContainerUpdate(context.Background(), ContainerUpdateParams{
		Container: alpha.UUID, Patch: json.RawMessage(`{"slug":"alpha-renamed"}`), Actor: "agent:wrkq-system",
	}); err != nil {
		t.Fatalf("rename alpha: %v", err)
	}
	reused := createProjectEventContainer(t, s, "alpha", "project", nil)
	assertMembership(alpha.UUID, "resident")
	assertMembership(beta.UUID, "participant")
	assertMembership(gamma.UUID, "participant")
	if entries := read(reused.UUID); len(entries) != 0 {
		t.Fatalf("reused slug received historical message: %#v", entries)
	}

	// A pre-stamp project-room row remains readable by its owner but cannot be
	// guessed into an endpoint timeline.
	if _, err := api.db.Exec(`UPDATE envelopes
		SET from_project_uuid = NULL, to_project_uuid = NULL
		WHERE group_id = ?`, result.GroupID); err != nil {
		t.Fatal(err)
	}
	assertMembership(alpha.UUID, "resident")
	for _, project := range []string{beta.UUID, gamma.UUID, delta.UUID, reused.UUID} {
		if entries := read(project); len(entries) != 0 {
			t.Fatalf("legacy unstamped project-room message appeared in %s: %#v", project, entries)
		}
	}

	// Campaign rooms remain ownership-only: cross-project endpoints are neither
	// stamped nor projected into recipient project timelines.
	campaign := createProjectEventContainer(t, s, "campaign", "project", nil)
	if _, err := api.db.Exec(`UPDATE containers SET campaign_state = 'active' WHERE uuid = ?`, campaign.UUID); err != nil {
		t.Fatal(err)
	}
	campaignMessage := "campaign ownership-only message"
	campaignResult, err := api.RoomSay(context.Background(), RoomSayParams{
		Ref:          "campaign",
		ScopeRef:     "clod@beta:primary",
		PrincipalRef: "agent:clod",
		To:           []string{"mable@gamma:primary"},
		Body:         campaignMessage,
	})
	if err != nil {
		t.Fatalf("RoomSay(campaign room): %v", err)
	}
	if campaignResult.Room.Kind != string(domain.RoomKindCampaign) {
		t.Fatalf("campaign room kind = %q, want campaign", campaignResult.Room.Kind)
	}
	if campaignStamps := countEnvelopeStamps(t, api, campaignResult.GroupID); campaignStamps != 0 {
		t.Fatalf("campaign endpoint stamps = %d, want 0", campaignStamps)
	}
	if entries := messagesWithBody(t, api, gamma.UUID, campaignMessage); len(entries) != 0 {
		t.Fatalf("campaign room message leaked into endpoint timeline: %#v", entries)
	}
}
