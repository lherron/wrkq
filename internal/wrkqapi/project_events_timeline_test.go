//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/attribution"
)

// Project events in the subtree timeline (T-08048 I5-I12): raw-scan cursors,
// version continuation, legacy additivity, affiliation, tails, scope, moves.

func TestProjectEventsI5RawScanCursorExactnessCycle5(t *testing.T) {
	api, s := newMonitorAPI(t)
	a := createProjectEventContainer(t, s, "i5a", "project", nil)
	b := createProjectEventContainer(t, s, "i5b", "project", nil)
	d := createProjectEventContainer(t, s, "dir", "directory", &a.UUID)
	task := createTimelineTask(t, s, d.UUID, "task", "open", "")

	cold, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: b.UUID, Scope: "subtree", EntriesOnly: true, Tail: true})
	if err != nil || cold.NextCursor == "" {
		t.Fatalf("cold cursor: %#v %v", cold, err)
	}
	p, err := decodeTimelineCursorAny(cold.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	setTaskState(t, api, task.ID, "in_progress")
	foreign := postProjectEvent(t, api, ProjectEventPostParams{Task: task.ID, Type: "move.before"})
	if _, err := api.db.Exec(`UPDATE event_log SET timestamp = '2020-01-01T00:00:00Z' WHERE id = (SELECT MAX(id) FROM event_log)`); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Exec(`UPDATE project_events SET created_at = '2019-01-01T00:00:00Z' WHERE id = ?`, foreign.ID); err != nil {
		t.Fatal(err)
	}
	var eventID int64
	if err := api.db.QueryRow(`SELECT MAX(id) FROM event_log`).Scan(&eventID); err != nil {
		t.Fatal(err)
	}

	scanned := timelineView(t, api, ContainerTimelineViewParams{Container: b.UUID, Scope: "subtree", EntriesOnly: true, Tail: true, Cursor: cold.NextCursor})
	if len(scanned.Entries) != 0 || scanned.NextCursor == "" {
		t.Fatalf("excluded scan = %#v", scanned)
	}
	position, err := decodeTimelineCursorAny(scanned.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	if position.AfterEventID != eventID || position.AfterProjectEventID != foreign.ID || position.AfterEventID <= p.AfterEventID {
		t.Fatalf("raw position = %#v, rows=%d/%d", position, eventID, foreign.ID)
	}

	if _, err := s.Containers.MoveWithAttribution(attribution.Attribution{PrincipalRef: "agent:test"}, d.UUID, &b.UUID, 0); err != nil {
		t.Fatal(err)
	}
	held := timelineView(t, api, ContainerTimelineViewParams{Container: b.UUID, Scope: "subtree", EntriesOnly: true, Tail: true, Cursor: scanned.NextCursor})
	if len(held.Entries) != 0 {
		t.Fatalf("held cursor replayed moved-in history: %#v", held.Entries)
	}
	fresh := timelineView(t, api, ContainerTimelineViewParams{Container: b.UUID, Scope: "subtree", EntriesOnly: true})
	if !timelineContainsIDs(fresh.Entries, eventID, foreign.ID) {
		t.Fatalf("fresh page misses moved history: %#v", fresh.Entries)
	}

	setTaskState(t, api, task.ID, "completed")
	afterMove := postProjectEvent(t, api, ProjectEventPostParams{Task: task.ID, Type: "move.after"})
	arrived := timelineView(t, api, ContainerTimelineViewParams{Container: b.UUID, Scope: "subtree", EntriesOnly: true, Tail: true, Cursor: held.NextCursor})
	if len(arrived.Entries) != 2 || countEventEntries(arrived.Entries) != 1 || !timelineContainsIDs(arrived.Entries, 0, afterMove.ID) {
		t.Fatalf("post-move arrivals = %#v", arrived.Entries)
	}
	again, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: b.UUID, Scope: "subtree", EntriesOnly: true, Tail: true, Cursor: arrived.NextCursor})
	if err != nil || len(again.Entries) != 0 {
		t.Fatalf("duplicates after drain: %#v %v", again, err)
	}

	capStart := again.NextCursor
	insertRawEvents(t, api, monitorMaxPageLimit+2, "ignored.raw", "")
	zero, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: b.UUID, Scope: "subtree", EntriesOnly: true, Tail: true, Cursor: capStart})
	if err != nil || len(zero.Entries) != 0 {
		t.Fatalf("zero-delivery raw page: %#v %v", zero, err)
	}
	zeroCur, _ := decodeTimelineCursorAny(zero.NextCursor)
	startCur, _ := decodeTimelineCursorAny(capStart)
	if zeroCur.AfterEventID-startCur.AfterEventID != monitorMaxPageLimit {
		t.Fatalf("raw cap advanced %d, want %d", zeroCur.AfterEventID-startCur.AfterEventID, monitorMaxPageLimit)
	}
}

func TestProjectEventsI6V1ContinuationAndScopeMismatch(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "i6", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "task", "open", "")
	setTaskState(t, api, task.ID, "in_progress", "completed")
	page, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: project.UUID, Limit: 1})
	if err != nil || page.NextCursor == "" || timelineCursorVersion(page.NextCursor) != 1 {
		t.Fatalf("v1 page = %#v %v", page, err)
	}
	postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID})
	continued := timelineView(t, api, ContainerTimelineViewParams{Container: project.UUID, Limit: 1, Cursor: page.NextCursor})
	for _, entry := range continued.Entries {
		if entry.ProjectEvent != nil {
			t.Fatalf("v1 continuation widened: %#v", entry)
		}
	}
	v2 := timelineView(t, api, ContainerTimelineViewParams{Container: project.UUID, Scope: "subtree", EntriesOnly: true, Tail: true})
	_, err = api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: project.UUID, Scope: "container", EntriesOnly: true, Tail: true, Cursor: v2.NextCursor})
	requireValidationReason(t, err, "cursor", "")
}

func TestProjectEventsI7LegacyAdditiveOnlyAndStateFrom(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "i7", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "task", "open", "")
	setTaskState(t, api, task.ID, "in_progress")
	legacy := timelineView(t, api, ContainerTimelineViewParams{Container: project.UUID})
	explicit := timelineView(t, api, ContainerTimelineViewParams{Container: project.UUID, Scope: "container"})
	if len(legacy.Entries) != len(explicit.Entries) {
		t.Fatalf("container scope changed delivery: %d/%d", len(legacy.Entries), len(explicit.Entries))
	}
	found := false
	for _, entry := range legacy.Entries {
		if entry.Type == "task.state" && entry.TaskUUID == task.UUID {
			found = entry.TaskState.From != nil && *entry.TaskState.From == "open" && entry.TaskState.State == "in_progress"
		}
	}
	if !found || timelineRequestUsesV2(ContainerTimelineViewParams{Container: project.UUID}) {
		t.Fatal("legacy path or additive state_from contract failed")
	}
}

func TestProjectEventsI9ProductionTimeAffiliation(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "i9", "project", nil)
	campaign := createProjectEventContainer(t, s, "campaign", "directory", &project.UUID)
	convertTimelineCampaign(t, api, campaign.UUID, "brief", "spec")
	external := createProjectEventContainer(t, s, "i9external", "project", nil)
	task := createTimelineTask(t, s, external.UUID, "task", "open", "")
	pre := postProjectEvent(t, api, ProjectEventPostParams{Task: task.ID, Type: "affiliation.pre"})
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{Campaign: &campaign.UUID}}); err != nil {
		t.Fatal(err)
	}
	during := postProjectEvent(t, api, ProjectEventPostParams{Task: task.ID, Type: "affiliation.during"})
	empty := ""
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{Campaign: &empty}}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.TaskDelete(context.Background(), TaskDeleteParams{Task: task.ID, Mode: "purge"}); err != nil {
		t.Fatal(err)
	}
	view := timelineView(t, api, ContainerTimelineViewParams{Container: campaign.UUID, Scope: "container", EntriesOnly: true, Types: []string{"affiliation.*"}})
	if timelineContainsProjectUUID(view.Entries, pre.UUID) || !timelineContainsProjectUUID(view.Entries, during.UUID) {
		t.Fatalf("production affiliation = %#v", view.Entries)
	}
	if view.Entries[0].Membership != "enrolled" || view.Entries[0].TaskUUID != "" {
		t.Fatalf("post-purge affiliated entry = %#v", view.Entries[0])
	}
}

func TestProjectEventsI10TailLivenessFromColdStart(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "i10", "project", nil)
	cold, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: project.UUID, Scope: "subtree", EntriesOnly: true, Tail: true, Types: []string{"tail.*"}})
	if err != nil || len(cold.Entries) != 0 || cold.NextCursor == "" {
		t.Fatalf("cold tail = %#v %v", cold, err)
	}
	posted := postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID, Type: "tail.first"})
	next, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: project.UUID, Scope: "subtree", EntriesOnly: true, Tail: true, Types: []string{"tail.*"}, Cursor: cold.NextCursor})
	if err != nil || !timelineContainsIDs(next.Entries, 0, posted.ID) {
		t.Fatalf("next tail = %#v %v", next, err)
	}
	history, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: project.UUID, Scope: "subtree", EntriesOnly: true, Tail: true, Types: []string{"tail.*"}, Since: "24h", Limit: 10})
	if err != nil || len(history.Entries) != 1 || history.NextCursor == "" {
		t.Fatalf("since tail = %#v %v", history, err)
	}
	second := postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID, Type: "tail.second"})
	afterHistory, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: project.UUID, Scope: "subtree", EntriesOnly: true, Tail: true, Types: []string{"tail.*"}, Since: "24h", Limit: 10, Cursor: history.NextCursor})
	if err != nil || !timelineContainsIDs(afterHistory.Entries, 0, second.ID) || afterHistory.NextCursor == "" {
		t.Fatalf("tail after short drain = %#v %v", afterHistory, err)
	}
}

func TestProjectEventsI11SubtreeCoherenceAndConfinement(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "i11", "project", nil)
	dir := createProjectEventContainer(t, s, "inbox", "directory", &project.UUID)
	task := createTimelineTask(t, s, dir.UUID, "task", "open", "")
	setTaskState(t, api, task.ID, "in_progress")
	posted := postProjectEvent(t, api, ProjectEventPostParams{Task: task.ID, Type: "subtree.fact"})
	subtree, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: project.UUID, Scope: "subtree", EntriesOnly: true})
	if err != nil || !timelineContainsIDs(subtree.Entries, 0, posted.ID) {
		t.Fatalf("subtree view = %#v %v", subtree, err)
	}
	for _, entry := range subtree.Entries {
		if (entry.EventID != 0 || entry.ProjectEvent != nil) && entry.Membership != "subtree" {
			t.Fatalf("entry membership = %#v", entry)
		}
	}
	containerOnly := timelineView(t, api, ContainerTimelineViewParams{Container: project.UUID, Scope: "container", EntriesOnly: true})
	if len(containerOnly.Entries) != 0 {
		t.Fatalf("container scope leaked subtree: %#v", containerOnly.Entries)
	}
	_, err = api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: dir.UUID, Scope: "subtree"})
	requireValidationReason(t, err, "scope", "subtree_requires_unadorned_project")
	campaign := createProjectEventContainer(t, s, "campaign", "directory", &project.UUID)
	convertTimelineCampaign(t, api, campaign.UUID, "brief", "spec")
	legacy := timelineView(t, api, ContainerTimelineViewParams{Container: campaign.UUID})
	explicit := timelineView(t, api, ContainerTimelineViewParams{Container: campaign.UUID, Scope: "container"})
	legacyProjection, _ := json.Marshal(struct {
		Members   []WrkqTimelineMember
		Rollup    WrkqTimelineRollup
		Footprint []WrkqCampaignFootprint
	}{legacy.Members, legacy.Rollup, legacy.Footprint})
	explicitProjection, _ := json.Marshal(struct {
		Members   []WrkqTimelineMember
		Rollup    WrkqTimelineRollup
		Footprint []WrkqCampaignFootprint
	}{explicit.Members, explicit.Rollup, explicit.Footprint})
	if string(legacyProjection) != string(explicitProjection) {
		t.Fatalf("campaign portfolio widened: %s != %s", legacyProjection, explicitProjection)
	}
	_, err = api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: campaign.UUID, Scope: "subtree"})
	requireValidationReason(t, err, "scope", "subtree_requires_unadorned_project")
}

func TestProjectEventsI12MoveCoherenceFreshReads(t *testing.T) {
	api, s := newMonitorAPI(t)
	a := createProjectEventContainer(t, s, "i12a", "project", nil)
	b := createProjectEventContainer(t, s, "i12b", "project", nil)
	d := createProjectEventContainer(t, s, "inbox", "directory", &a.UUID)
	task := createTimelineTask(t, s, d.UUID, "task", "open", "")
	setTaskState(t, api, task.ID, "in_progress")
	posted := postProjectEvent(t, api, ProjectEventPostParams{Task: task.ID, Type: "move.fact"})
	if _, err := s.Containers.MoveWithAttribution(attribution.Attribution{PrincipalRef: "agent:test"}, d.UUID, &b.UUID, 0); err != nil {
		t.Fatal(err)
	}
	av := timelineView(t, api, ContainerTimelineViewParams{Container: a.UUID, Scope: "subtree", EntriesOnly: true})
	bv := timelineView(t, api, ContainerTimelineViewParams{Container: b.UUID, Scope: "subtree", EntriesOnly: true})
	if len(av.Entries) != 0 || !timelineContainsIDs(bv.Entries, 0, posted.ID) {
		t.Fatalf("fresh move A=%#v B=%#v", av.Entries, bv.Entries)
	}
	if countEventEntries(bv.Entries) == 0 {
		t.Fatalf("event_log source did not move with tree: %#v", bv.Entries)
	}
	at, err := api.ProjectEventTypesView(context.Background(), ProjectEventTypesViewParams{Project: a.UUID})
	if err != nil {
		t.Fatal(err)
	}
	bt, err := api.ProjectEventTypesView(context.Background(), ProjectEventTypesViewParams{Project: b.UUID})
	if err != nil {
		t.Fatal(err)
	}
	if len(at.Items) != 0 || len(bt.Items) != 1 || bt.Items[0].Type != "move.fact" {
		t.Fatalf("types move A=%#v B=%#v", at.Items, bt.Items)
	}
	shown, err := api.ProjectEventGet(context.Background(), ProjectEventGetParams{ProjectEvent: posted.UUID})
	if err != nil || shown.ProjectUUID != a.UUID {
		t.Fatalf("idempotency stamp moved: %#v %v", shown, err)
	}
	if strings.Contains(strings.ToLower(timelineProjectEventsRawQuery), "project_uuid") {
		t.Fatal("timeline reader predicates on project_uuid")
	}
}
