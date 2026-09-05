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
