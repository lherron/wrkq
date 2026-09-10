//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/store"
)

func createProjectEventContainer(t *testing.T, s *store.Store, slug, kind string, parent *string) *store.ContainerCreateResult {
	t.Helper()
	created, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{Slug: slug, Kind: kind, ParentUUID: parent})
	if err != nil {
		t.Fatalf("create %s container %s: %v", kind, slug, err)
	}
	return created
}

func postProjectEvent(t *testing.T, api *API, p ProjectEventPostParams) *WrkqProjectEventPostResult {
	t.Helper()
	if p.Type == "" {
		p.Type = "smoke.posted"
	}
	if p.Source == "" {
		p.Source = "test"
	}
	if p.Summary == "" {
		p.Summary = "fact"
	}
	result, err := api.ProjectEventPost(context.Background(), p)
	if err != nil {
		t.Fatalf("post project event: %v", err)
	}
	return result
}

func tableCount(t *testing.T, database *sql.DB, table string) int64 {
	t.Helper()
	var count int64
	if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func requireValidationReason(t *testing.T, err error, field, reason string) {
	t.Helper()
	domainErr, ok := err.(*DomainError)
	if !ok || domainErr.Code() != CodeValidation {
		t.Fatalf("want WRKQ_VALIDATION, got %T %v", err, err)
	}
	data, ok := domainErr.Data().(map[string]any)
	if !ok || data["field"] != field || (reason != "" && data["reason"] != reason) {
		t.Fatalf("validation data = %#v, want field=%s reason=%s", domainErr.Data(), field, reason)
	}
}

func TestProjectEventsI1OneFactOneHome(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "i1", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "task", "open", "")
	eventsBefore := tableCount(t, api.db.DB, "event_log")
	postProjectEvent(t, api, ProjectEventPostParams{Task: task.ID})
	if got := tableCount(t, api.db.DB, "event_log"); got != eventsBefore {
		t.Fatalf("post added event_log row: %d -> %d", eventsBefore, got)
	}
	projectBefore := tableCount(t, api.db.DB, "project_events")
	state := "in_progress"
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{State: &state}}); err != nil {
		t.Fatal(err)
	}
	if got := tableCount(t, api.db.DB, "project_events"); got != projectBefore {
		t.Fatalf("task update added project_events row: %d -> %d", projectBefore, got)
	}
}

func TestProjectEventsI2ReservedNamespacesAndOpenVocabulary(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "i2", "project", nil)
	for namespace := range reservedProjectEventNamespaces {
		_, err := api.ProjectEventPost(context.Background(), ProjectEventPostParams{Project: project.UUID, Type: namespace + ".forged", Source: "test", Summary: "no"})
		requireValidationReason(t, err, "type", "reserved_namespace")
	}
	postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID, Type: "unregistered_namespace.new_fact"})
}

func TestProjectEventsI3ExactAttributionAndNoWakeEffects(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "i3", "project", nil)
	before := map[string]int64{}
	for _, table := range []string{"event_log", "envelopes", "workflow_events"} {
		before[table] = tableCount(t, api.db.DB, table)
	}
	created := postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID, PrincipalRef: "agent:exact", ScopeRef: "exact@wrkq:primary"})
	event, err := api.ProjectEventGet(context.Background(), ProjectEventGetParams{ProjectEvent: created.FID})
	if err != nil {
		t.Fatal(err)
	}
	if event.PrincipalRef != "agent:exact" || event.ScopeRef == nil || *event.ScopeRef != "exact@wrkq:primary" {
		t.Fatalf("attribution = %#v", event)
	}
	for table, want := range before {
		if got := tableCount(t, api.db.DB, table); got != want {
			t.Fatalf("post changed %s: %d -> %d", table, want, got)
		}
	}

	noDefault := New(api.db, nil, "", "", 0)
	for _, principal := range []string{"", "human:lance"} {
		_, err := noDefault.ProjectEventPost(context.Background(), ProjectEventPostParams{Project: project.UUID, Type: "smoke.posted", Source: "test", Summary: "no", PrincipalRef: principal, IdempotencyKey: "attribution-key"})
		if err == nil {
			t.Fatalf("principal %q unexpectedly succeeded", principal)
		}
		if de, ok := err.(*DomainError); !ok || de.Code() != CodeValidation {
			t.Fatalf("principal %q error = %T %v", principal, err, err)
		}
	}
}

func TestProjectEventsI4ProjectScopedIdempotentReplay(t *testing.T) {
	api, s := newMonitorAPI(t)
	a := createProjectEventContainer(t, s, "i4a", "project", nil)
	b := createProjectEventContainer(t, s, "i4b", "project", nil)
	first := postProjectEvent(t, api, ProjectEventPostParams{Project: a.UUID, IdempotencyKey: "same"})
	second := postProjectEvent(t, api, ProjectEventPostParams{Project: a.UUID, IdempotencyKey: "same", Summary: "retry"})
	if first.FID != second.FID || second.Created {
		t.Fatalf("replay = %#v then %#v", first, second)
	}
	other := postProjectEvent(t, api, ProjectEventPostParams{Project: b.UUID, IdempotencyKey: "same"})
	if other.FID == first.FID || !other.Created {
		t.Fatalf("cross-project key reused row: %#v", other)
	}
}

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
	state := "in_progress"
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{State: &state}}); err != nil {
		t.Fatal(err)
	}
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

	scanned, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: b.UUID, Scope: "subtree", EntriesOnly: true, Tail: true, Cursor: cold.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
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
	held, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: b.UUID, Scope: "subtree", EntriesOnly: true, Tail: true, Cursor: scanned.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(held.Entries) != 0 {
		t.Fatalf("held cursor replayed moved-in history: %#v", held.Entries)
	}
	fresh, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: b.UUID, Scope: "subtree", EntriesOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if !timelineContainsIDs(fresh.Entries, eventID, foreign.ID) {
		t.Fatalf("fresh page misses moved history: %#v", fresh.Entries)
	}

	state = "completed"
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{State: &state}}); err != nil {
		t.Fatal(err)
	}
	afterMove := postProjectEvent(t, api, ProjectEventPostParams{Task: task.ID, Type: "move.after"})
	arrived, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: b.UUID, Scope: "subtree", EntriesOnly: true, Tail: true, Cursor: held.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	eventArrivals := 0
	for _, entry := range arrived.Entries {
		if entry.EventID != 0 {
			eventArrivals++
		}
	}
	if len(arrived.Entries) != 2 || eventArrivals != 1 || !timelineContainsIDs(arrived.Entries, 0, afterMove.ID) {
		t.Fatalf("post-move arrivals = %#v", arrived.Entries)
	}
	again, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: b.UUID, Scope: "subtree", EntriesOnly: true, Tail: true, Cursor: arrived.NextCursor})
	if err != nil || len(again.Entries) != 0 {
		t.Fatalf("duplicates after drain: %#v %v", again, err)
	}

	capStart := again.NextCursor
	for index := 0; index < monitorMaxPageLimit+2; index++ {
		if _, err := api.db.Exec(`INSERT INTO event_log(resource_type,event_type) VALUES ('system','ignored.raw')`); err != nil {
			t.Fatal(err)
		}
	}
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

func timelineContainsIDs(entries []WrkqTimelineEntry, eventID, projectEventID int64) bool {
	sawEvent, sawProject := false, false
	for _, entry := range entries {
		if eventID != 0 && entry.EventID == eventID {
			sawEvent = true
		}
		if projectEventID != 0 && entry.ProjectEventID == projectEventID {
			sawProject = true
		}
	}
	return (eventID == 0 || sawEvent) && (projectEventID == 0 || sawProject)
}

func TestProjectEventsI6V1ContinuationAndScopeMismatch(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "i6", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "task", "open", "")
	for _, state := range []string{"in_progress", "completed"} {
		if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{State: &state}}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: project.UUID, Limit: 1})
	if err != nil || page.NextCursor == "" || timelineCursorVersion(page.NextCursor) != 1 {
		t.Fatalf("v1 page = %#v %v", page, err)
	}
	postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID})
	continued, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: project.UUID, Limit: 1, Cursor: page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range continued.Entries {
		if entry.ProjectEventID != 0 {
			t.Fatalf("v1 continuation widened: %#v", entry)
		}
	}
	v2, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: project.UUID, Scope: "subtree", EntriesOnly: true, Tail: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: project.UUID, Scope: "container", EntriesOnly: true, Tail: true, Cursor: v2.NextCursor})
	requireValidationReason(t, err, "cursor", "")
}

func TestProjectEventsI7LegacyAdditiveOnlyAndStateFrom(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "i7", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "task", "open", "")
	state := "in_progress"
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{State: &state}}); err != nil {
		t.Fatal(err)
	}
	legacy, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: project.UUID})
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: project.UUID, Scope: "container"})
	if err != nil {
		t.Fatal(err)
	}
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

func TestProjectEventsI8UnprunedNoDeletionOwner(t *testing.T) {
	api, s := newMonitorAPI(t)
	a := createProjectEventContainer(t, s, "i8a", "project", nil)
	b := createProjectEventContainer(t, s, "i8b", "project", nil)
	d := createProjectEventContainer(t, s, "inbox", "directory", &a.UUID)
	task := createTimelineTask(t, s, d.UUID, "task", "open", "")
	posted := postProjectEvent(t, api, ProjectEventPostParams{Task: task.ID, Type: "retention.fact"})
	state := "in_progress"
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{State: &state}}); err != nil {
		t.Fatal(err)
	}
	before := tableCount(t, api.db.DB, "project_events")
	if _, err := s.Containers.MoveWithAttribution(attribution.Attribution{PrincipalRef: "agent:test"}, d.UUID, &b.UUID, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := api.ContainerDelete(context.Background(), ContainerDeleteParams{Container: a.UUID}); err != nil {
		t.Fatalf("delete old project: %v", err)
	}
	if _, err := api.TaskDelete(context.Background(), TaskDeleteParams{Task: task.ID, Mode: "purge"}); err != nil {
		t.Fatalf("purge task: %v", err)
	}
	if got := tableCount(t, api.db.DB, "project_events"); got != before {
		t.Fatalf("project event count changed %d -> %d", before, got)
	}
	shown, err := api.ProjectEventGet(context.Background(), ProjectEventGetParams{ProjectEvent: posted.FID})
	if err != nil || shown.TaskUUID != nil {
		t.Fatalf("show after deletion = %#v %v", shown, err)
	}
	view, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: b.UUID, Scope: "subtree", EntriesOnly: true})
	if err != nil || !timelineContainsIDs(view.Entries, 0, posted.ID) {
		t.Fatalf("moved retained row absent: %#v %v", view, err)
	}
	eventRows := 0
	for _, entry := range view.Entries {
		if entry.EventID != 0 {
			eventRows++
		}
	}
	if eventRows == 0 {
		t.Fatalf("moved retained event_log source absent: %#v", view.Entries)
	}

	fks, err := api.db.Query(`PRAGMA foreign_key_list(project_events)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fks.Close() }()
	count := 0
	for fks.Next() {
		var id, seq int
		var table, from, to, onUpdate, onDelete, match string
		if err := fks.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			t.Fatal(err)
		}
		count++
		if table != "tasks" || from != "task_uuid" || strings.ToUpper(onDelete) != "SET NULL" {
			t.Fatalf("unexpected FK: %s %s %s", table, from, onDelete)
		}
	}
	if count != 1 {
		t.Fatalf("foreign keys = %d, want 1", count)
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
	view, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: campaign.UUID, Scope: "container", EntriesOnly: true, Types: []string{"affiliation.*"}})
	if err != nil {
		t.Fatal(err)
	}
	if timelineContainsIDs(view.Entries, 0, pre.ID) || !timelineContainsIDs(view.Entries, 0, during.ID) {
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
	state := "in_progress"
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{State: &state}}); err != nil {
		t.Fatal(err)
	}
	posted := postProjectEvent(t, api, ProjectEventPostParams{Task: task.ID, Type: "subtree.fact"})
	subtree, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: project.UUID, Scope: "subtree", EntriesOnly: true})
	if err != nil || !timelineContainsIDs(subtree.Entries, 0, posted.ID) {
		t.Fatalf("subtree view = %#v %v", subtree, err)
	}
	for _, entry := range subtree.Entries {
		if (entry.EventID != 0 || entry.ProjectEventID == posted.ID) && entry.Membership != "subtree" {
			t.Fatalf("entry membership = %#v", entry)
		}
	}
	containerOnly, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: project.UUID, Scope: "container", EntriesOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(containerOnly.Entries) != 0 {
		t.Fatalf("container scope leaked subtree: %#v", containerOnly.Entries)
	}
	_, err = api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: dir.UUID, Scope: "subtree"})
	requireValidationReason(t, err, "scope", "subtree_requires_unadorned_project")
	campaign := createProjectEventContainer(t, s, "campaign", "directory", &project.UUID)
	convertTimelineCampaign(t, api, campaign.UUID, "brief", "spec")
	legacy, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: campaign.UUID})
	if err != nil {
		t.Fatal(err)
	}
	explicit, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: campaign.UUID, Scope: "container"})
	if err != nil {
		t.Fatal(err)
	}
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
	state := "in_progress"
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{State: &state}}); err != nil {
		t.Fatal(err)
	}
	posted := postProjectEvent(t, api, ProjectEventPostParams{Task: task.ID, Type: "move.fact"})
	if _, err := s.Containers.MoveWithAttribution(attribution.Attribution{PrincipalRef: "agent:test"}, d.UUID, &b.UUID, 0); err != nil {
		t.Fatal(err)
	}
	av, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: a.UUID, Scope: "subtree", EntriesOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	bv, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: b.UUID, Scope: "subtree", EntriesOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(av.Entries) != 0 || !timelineContainsIDs(bv.Entries, 0, posted.ID) {
		t.Fatalf("fresh move A=%#v B=%#v", av.Entries, bv.Entries)
	}
	eventRows := 0
	for _, entry := range bv.Entries {
		if entry.EventID != 0 {
			eventRows++
		}
	}
	if eventRows == 0 {
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
	shown, err := api.ProjectEventGet(context.Background(), ProjectEventGetParams{ProjectEvent: posted.FID})
	if err != nil || shown.ProjectUUID != a.UUID {
		t.Fatalf("idempotency stamp moved: %#v %v", shown, err)
	}
	if strings.Contains(strings.ToLower(timelineProjectEventsRawQuery), "project_uuid") {
		t.Fatal("timeline reader predicates on project_uuid")
	}
}

// TestTimelineMergeHorizonAcrossPages is T-08328's regression. The two sources
// are scanned at a fixed ROW cap, so a small project_events table drains in one
// page while event_log is still deep in its own past. Without a common
// timestamp horizon the page-one merge emits every project event before the
// older event rows that arrive pages later, and the delivered stream jumps
// backwards at the boundary.
//
// The fixture is that shape in miniature: a scan-cap's worth of old filler
// events, then one deliverable event that is NEWER than the filler but OLDER
// than the only project event, positioned beyond the first page's reach.
func TestTimelineMergeHorizonAcrossPages(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "horizon", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "task", "open", "")

	for index := 0; index < monitorMaxPageLimit; index++ {
		if _, err := api.db.Exec(`INSERT INTO event_log(resource_type,event_type) VALUES ('system','ignored.raw')`); err != nil {
			t.Fatal(err)
		}
	}
	// Every row written so far is filler as far as the merge is concerned, and
	// backdating the whole prefix keeps id order equal to timestamp order --
	// the invariant the per-source scan depends on.
	if _, err := api.db.Exec(`UPDATE event_log SET timestamp = '2020-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}

	state := "completed"
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{State: &state}}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Exec(`UPDATE event_log SET timestamp = '2026-01-01 00:00:00' WHERE timestamp <> '2020-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}
	event := postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID, Type: "git.commit"})
	if _, err := api.db.Exec(`UPDATE project_events SET created_at = '2026-01-02 00:00:00'`); err != nil {
		t.Fatal(err)
	}

	delivered := []WrkqTimelineEntry{}
	cursor := ""
	for page := 0; page < 8; page++ {
		view, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
			Container: project.UUID, Scope: "subtree", EntriesOnly: true, Limit: 100, Cursor: cursor,
		})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		delivered = append(delivered, view.Entries...)
		if cursor = view.NextCursor; cursor == "" {
			break
		}
	}
	if cursor != "" {
		t.Fatal("fence did not drain in 8 pages")
	}

	for index := 1; index < len(delivered); index++ {
		if delivered[index].Timestamp < delivered[index-1].Timestamp {
			t.Fatalf("delivered stream jumps backwards at %d: %s -> %s (%#v)",
				index, delivered[index-1].Timestamp, delivered[index].Timestamp, delivered)
		}
	}
	if len(delivered) < 2 {
		t.Fatalf("expected the state change and the project event, got %#v", delivered)
	}
	last := delivered[len(delivered)-1]
	if last.ProjectEventID != event.ID {
		t.Fatalf("newest entry is not the project event: %#v", delivered)
	}
	if !timelineContainsIDs(delivered, 0, event.ID) {
		t.Fatalf("project event never delivered: %#v", delivered)
	}
}

// TestTimelineMergeHorizonBound pins the horizon rule itself: only a truncated
// source bounds the page, and the earliest such bound wins.
func TestTimelineMergeHorizonBound(t *testing.T) {
	if bound := timelineScanBound(false, true, "2026-01-01T00:00:00Z"); bound != "" {
		t.Fatalf("a drained source must impose no bound, got %q", bound)
	}
	if bound := timelineScanBound(true, false, ""); bound != "" {
		t.Fatalf("an empty source must impose no bound, got %q", bound)
	}
	if got := timelineMergeHorizon(false, "2026-02-01T00:00:00Z", "2026-01-01T00:00:00Z"); got != "2026-01-01T00:00:00Z" {
		t.Fatalf("horizon = %q, want the earliest bound", got)
	}
	if got := timelineMergeHorizon(false, "", ""); got != "" {
		t.Fatalf("no bound must leave the page unbounded, got %q", got)
	}
	rows := []timelineRawProjectEvent{
		{entry: WrkqTimelineEntry{Timestamp: "2026-01-01T00:00:00Z"}},
		{entry: WrkqTimelineEntry{Timestamp: "2026-01-05T00:00:00Z"}},
	}
	if kept := trimTimelineProjectRows(rows, "2026-01-01T00:00:00Z", false); len(kept) != 1 {
		t.Fatalf("trim kept %d rows, want the horizon prefix", len(kept))
	}
	if kept := trimTimelineProjectRows(rows, "", false); len(kept) != 2 {
		t.Fatalf("an unbounded page must keep every row, got %d", len(kept))
	}
	// Descending delivery inverts the rule: the LATEST bound wins, and rows
	// EARLIER than the horizon are the ones withheld.
	if got := timelineMergeHorizon(true, "2026-02-01T00:00:00Z", "2026-01-01T00:00:00Z"); got != "2026-02-01T00:00:00Z" {
		t.Fatalf("descending horizon = %q, want the latest bound", got)
	}
	descRows := []timelineRawProjectEvent{
		{entry: WrkqTimelineEntry{Timestamp: "2026-01-05T00:00:00Z"}},
		{entry: WrkqTimelineEntry{Timestamp: "2026-01-01T00:00:00Z"}},
	}
	if kept := trimTimelineProjectRows(descRows, "2026-01-05T00:00:00Z", true); len(kept) != 1 {
		t.Fatalf("descending trim kept %d rows, want the horizon prefix", len(kept))
	}
}

// TestTimelineDescendingIsTheExactReverse is F2's contract. A `log` verb reads
// newest-first, so --limit N means "the N most recent". The descending page
// must be the exact reverse of the ascending one over the same rows -- ties
// included -- or the two directions disagree about what the timeline IS.
func TestTimelineDescendingIsTheExactReverse(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "desc", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "task", "open", "")
	for _, state := range []string{"in_progress", "completed"} {
		if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{State: &state}}); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 3; index++ {
		postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID, Type: "git.commit"})
	}

	drain := func(order string) []WrkqTimelineEntry {
		t.Helper()
		out, cursor := []WrkqTimelineEntry{}, ""
		for page := 0; page < 8; page++ {
			view, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
				Container: project.UUID, Scope: "subtree", EntriesOnly: true, Limit: 2,
				Order: order, Cursor: cursor,
			})
			if err != nil {
				t.Fatalf("%s page %d: %v", order, page, err)
			}
			out = append(out, view.Entries...)
			if cursor = view.NextCursor; cursor == "" {
				return out
			}
		}
		t.Fatalf("%s did not drain in 8 pages", order)
		return nil
	}

	ascending := drain("asc")
	descending := drain("desc")
	if len(ascending) != len(descending) || len(ascending) < 5 {
		t.Fatalf("asc delivered %d, desc delivered %d", len(ascending), len(descending))
	}
	for index, entry := range descending {
		mirror := ascending[len(ascending)-1-index]
		if entry.Timestamp != mirror.Timestamp || entry.EventID != mirror.EventID ||
			entry.ProjectEventID != mirror.ProjectEventID {
			t.Fatalf("desc[%d] is not the mirror of asc[%d]: %#v vs %#v",
				index, len(ascending)-1-index, entry, mirror)
		}
	}
	for index := 1; index < len(descending); index++ {
		if descending[index].Timestamp > descending[index-1].Timestamp {
			t.Fatalf("descending stream jumps forwards at %d: %s -> %s",
				index, descending[index-1].Timestamp, descending[index].Timestamp)
		}
	}

	// A cursor is a position in ONE direction and must never be reinterpreted
	// in the other, and a tail follows appends so it cannot run backwards.
	first, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true, Limit: 2, Order: "desc",
	})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("descending first page = %#v %v", first, err)
	}
	if timelineCursorVersion(first.NextCursor) != 3 {
		t.Fatalf("descending cursor version = %d, want 3", timelineCursorVersion(first.NextCursor))
	}
	if _, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true, Order: "asc", Cursor: first.NextCursor,
	}); err == nil {
		t.Fatal("asc over a descending cursor must be refused")
	}
	if _, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true, Order: "desc", Tail: true,
	}); err == nil {
		t.Fatal("a descending tail must be refused")
	}
	if _, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true, Order: "sideways",
	}); err == nil {
		t.Fatal("an unknown order must be refused")
	}
}

// TestTimelineDescendingHonoursTheHorizon is the T-08328 fixture read backwards:
// the newest end is reached on page one instead of after the whole history, and
// the horizon still keeps the two sources from crossing.
func TestTimelineDescendingHonoursTheHorizon(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "deschorizon", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "task", "open", "")
	state := "completed"
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{State: &state}}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Exec(`UPDATE event_log SET timestamp = '2026-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < monitorMaxPageLimit; index++ {
		if _, err := api.db.Exec(`INSERT INTO event_log(resource_type,event_type,timestamp) VALUES ('system','ignored.raw','2026-03-01 00:00:00')`); err != nil {
			t.Fatal(err)
		}
	}
	event := postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID, Type: "git.commit"})
	if _, err := api.db.Exec(`UPDATE project_events SET created_at = '2026-02-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}

	// A page may legitimately deliver nothing -- here the newest 1000 rows are
	// all unmatched filler -- so the contract is about the DELIVERED stream,
	// not about page one. The first entry delivered must be the newest match,
	// and the stream must never run forwards.
	delivered, cursor := []WrkqTimelineEntry{}, ""
	for page := 0; page < 8; page++ {
		view, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
			Container: project.UUID, Scope: "subtree", EntriesOnly: true, Limit: 100,
			Order: "desc", Cursor: cursor,
		})
		if err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		delivered = append(delivered, view.Entries...)
		if cursor = view.NextCursor; cursor == "" {
			break
		}
	}
	if cursor != "" {
		t.Fatal("descending fence did not drain in 8 pages")
	}
	if len(delivered) < 2 {
		t.Fatalf("expected the project event and the state change, got %#v", delivered)
	}
	if delivered[0].ProjectEventID != event.ID {
		t.Fatalf("descending stream must open on the newest match, got %#v", delivered)
	}
	for index := 1; index < len(delivered); index++ {
		if delivered[index].Timestamp > delivered[index-1].Timestamp {
			t.Fatalf("descending stream jumps forwards at %d: %s -> %s",
				index, delivered[index-1].Timestamp, delivered[index].Timestamp)
		}
	}
	// The horizon still separates the sources: the project event is NEWER than
	// the state change and must be delivered before it, never beside it.
	if delivered[len(delivered)-1].ProjectEventID == event.ID {
		t.Fatalf("project event landed at the oldest end: %#v", delivered)
	}
}

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

	view, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true, Types: []string{"message"}, Limit: 100,
	})
	if err != nil {
		t.Fatalf("ContainerTimelineView: %v", err)
	}
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
	state := "completed"
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: task.ID, Patch: TaskPatch{State: &state},
	}); err != nil {
		t.Fatalf("TaskUpdate: %v", err)
	}

	read := func(types []string) []WrkqTimelineEntry {
		t.Helper()
		view, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
			Container: project.UUID, Scope: "subtree", EntriesOnly: true, Types: types, Limit: 100,
		})
		if err != nil {
			t.Fatalf("ContainerTimelineView(%v): %v", types, err)
		}
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

// TestTimelineExcludesAdHocRooms pins D4's membership property: an ad-hoc room
// (an agent DM) is anchored to neither a task nor a container, so it resolves to
// no container and never appears in any project's log.
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
		Ref: "cody@dmproj:primary", Body: "a direct message", PrincipalRef: "agent:clod",
	}); err != nil {
		t.Fatalf("RoomSay(dm): %v", err)
	}

	view, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true, Types: []string{"message"}, Limit: 100,
	})
	if err != nil {
		t.Fatalf("ContainerTimelineView: %v", err)
	}
	for _, entry := range view.Entries {
		if entry.Message != nil && entry.Message.Body == "a direct message" {
			t.Fatalf("an ad-hoc DM leaked into the project log: %#v", entry)
		}
	}
	if len(view.Entries) != 1 {
		t.Fatalf("want exactly the task-room message, got %d: %#v", len(view.Entries), view.Entries)
	}
}

// TestAscendingSinceSeeksPastHistory pins the fix for the `--follow --since`
// stall. A tail is ascending by construction, and an ascending scan starts at
// the OLDEST row, so before the seek it walked every event ever written before
// reaching the window: on a real ledger that was ~78 pages at the poll interval
// (~16s) to print a backlog that `--since` alone returned in 0.15s.
//
// The assertion is on the CURSOR POSITION, not on wall time, so it states the
// mechanism and cannot flake: the opening tail page must start just below the
// first in-window row rather than at zero. Removing the seek leaves
// AfterEventID at 0 and fails this.
func TestAscendingSinceSeeksPastHistory(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "seek", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "target", "open", "")

	// A long prefix of history, all far older than the floor.
	for index := 0; index < 2500; index++ {
		if _, err := api.db.Exec(`INSERT INTO event_log(resource_type,event_type) VALUES ('system','ignored.raw')`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := api.db.Exec(`UPDATE event_log SET timestamp = '2020-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}

	// One recent, deliverable entry inside the window.
	state := "completed"
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: task.ID, Patch: TaskPatch{State: &state},
	}); err != nil {
		t.Fatal(err)
	}
	var firstRecent int64
	if err := api.db.QueryRow(
		`SELECT MIN(id) FROM event_log WHERE timestamp > '2020-01-01T00:00:00Z'`,
	).Scan(&firstRecent); err != nil {
		t.Fatal(err)
	}

	view, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true,
		Since: "2h", Tail: true, Limit: 100,
	})
	if err != nil {
		t.Fatalf("ContainerTimelineView: %v", err)
	}

	cur, err := decodeTimelineCursorAny(view.NextCursor)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	// The scan must have skipped the 2500-row prefix outright.
	if cur.AfterEventID < firstRecent-1 {
		t.Fatalf("ascending tail did not seek past history: AfterEventID=%d, want >= %d "+
			"(the first in-window row is %d; starting below that walks the whole ledger)",
			cur.AfterEventID, firstRecent-1, firstRecent)
	}
	// And the in-window entry is still delivered -- a seek that overshot would
	// be fast and WRONG.
	found := false
	for _, entry := range view.Entries {
		if entry.TaskID == task.ID && entry.Type == "task.state" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the in-window entry was skipped by the seek: %#v", view.Entries)
	}
}

// TestAscendingSinceSeekKeepsAnEmptyWindowUsable pins the no-match arm: when
// every row predates the floor there is nothing to deliver, and the tail must
// start at the END rather than replaying history.
func TestAscendingSinceSeekKeepsAnEmptyWindowUsable(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "seekempty", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "target", "open", "")

	state := "completed"
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: task.ID, Patch: TaskPatch{State: &state},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.db.Exec(`UPDATE event_log SET timestamp = '2020-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	var maxEvent int64
	if err := api.db.QueryRow(`SELECT MAX(id) FROM event_log`).Scan(&maxEvent); err != nil {
		t.Fatal(err)
	}

	view, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true,
		Since: "2h", Tail: true, Limit: 100,
	})
	if err != nil {
		t.Fatalf("ContainerTimelineView: %v", err)
	}
	if len(view.Entries) != 0 {
		t.Fatalf("every row predates the floor, so nothing may be delivered: %#v", view.Entries)
	}
	cur, err := decodeTimelineCursorAny(view.NextCursor)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if cur.AfterEventID != maxEvent {
		t.Fatalf("an all-behind-the-floor source must start at the end: AfterEventID=%d, want %d",
			cur.AfterEventID, maxEvent)
	}
}
