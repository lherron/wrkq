//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/store"
)

// The project-event post contract (T-08048 A1-A5) and retention, plus the
// fixture helpers that seed containers and post facts.

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
	if len(p.Attributes) == 0 {
		p.Attributes = json.RawMessage(`{"alpha":"beta"}`)
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

func TestProjectEventsA1OneBody(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "i1", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "task", "open", "")
	eventsBefore := tableCount(t, api.db.DB, "event_log")
	postProjectEvent(t, api, ProjectEventPostParams{Task: task.ID})
	if got := tableCount(t, api.db.DB, "event_log"); got != eventsBefore {
		t.Fatalf("post added event_log row: %d -> %d", eventsBefore, got)
	}
	projectBefore := tableCount(t, api.db.DB, "project_events")
	setTaskState(t, api, task.ID, "in_progress")
	if got := tableCount(t, api.db.DB, "project_events"); got != projectBefore {
		t.Fatalf("task update added project_events row: %d -> %d", projectBefore, got)
	}
}

func TestProjectEventsA2ShapeAndBounds(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "a2", "project", nil)
	base := ProjectEventPostParams{Project: project.UUID, Type: "session.born", Summary: "fact"}
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`{"Bad-Key":"x"}`), json.RawMessage(`{"alpha":{"nested":"x"}}`), json.RawMessage(`{"alpha":1}`)} {
		p := base
		p.Attributes = raw
		if _, err := api.ProjectEventPost(context.Background(), p); err == nil {
			t.Fatalf("attributes %s unexpectedly accepted", raw)
		}
	}
	keys := make([]string, 33)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%d", i)
	}
	tooMany := map[string]string{}
	for _, key := range keys {
		tooMany[key] = "x"
	}
	p := base
	raw, _ := json.Marshal(tooMany)
	p.Attributes = raw
	if _, err := api.ProjectEventPost(context.Background(), p); err == nil {
		t.Fatal("33 keys unexpectedly accepted")
	}
	accepted := map[string]string{}
	for i := 0; i < 32; i++ {
		accepted[fmt.Sprintf("k%d", i)] = strings.Repeat("x", 1024)
	}
	raw, _ = json.Marshal(accepted)
	p.Attributes = raw
	if _, err := api.ProjectEventPost(context.Background(), p); err != nil {
		t.Fatalf("bounded attributes refused: %v", err)
	}
}

func TestProjectEventsA3SubjectNamespace(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "i2", "project", nil)
	for namespace := range reservedProjectEventNamespaces {
		_, err := api.ProjectEventPost(context.Background(), ProjectEventPostParams{Project: project.UUID, Type: namespace + ".forged", Summary: "no", Attributes: json.RawMessage(`{"alpha":"beta"}`)})
		requireValidationReason(t, err, "type", "reserved_namespace")
	}
	postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID, Type: "unregistered_namespace.new_fact"})
}

func TestProjectEventsA4OptionalAttributionAndNoWakeEffects(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "i3", "project", nil)
	before := map[string]int64{}
	for _, table := range []string{"event_log", "envelopes", "workflow_events"} {
		before[table] = tableCount(t, api.db.DB, table)
	}
	created := postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID, PrincipalRef: "agent:exact", ScopeRef: "exact@wrkq:primary"})
	event, err := api.ProjectEventGet(context.Background(), ProjectEventGetParams{ProjectEvent: created.UUID})
	if err != nil {
		t.Fatal(err)
	}
	if event.PrincipalRef == nil || *event.PrincipalRef != "agent:exact" || event.ScopeRef == nil || *event.ScopeRef != "exact@wrkq:primary" {
		t.Fatalf("attribution = %#v", event)
	}
	for table, want := range before {
		if got := tableCount(t, api.db.DB, table); got != want {
			t.Fatalf("post changed %s: %d -> %d", table, want, got)
		}
	}

	without := postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID, IdempotencyKey: "attribution-key"})
	if without.Created == false {
		t.Fatal("unattributed post was not created")
	}
	_, err = api.ProjectEventPost(context.Background(), ProjectEventPostParams{Project: project.UUID, Type: "smoke.posted", Summary: "no", Attributes: json.RawMessage(`{"alpha":"beta"}`), PrincipalRef: "human:lance"})
	requireValidationReason(t, err, "principalRef", "")
}

func TestProjectEventsA5UUIDIdentity(t *testing.T) {
	api, s := newMonitorAPI(t)
	a := createProjectEventContainer(t, s, "i4a", "project", nil)
	b := createProjectEventContainer(t, s, "i4b", "project", nil)
	first := postProjectEvent(t, api, ProjectEventPostParams{Project: a.UUID, IdempotencyKey: "same"})
	second := postProjectEvent(t, api, ProjectEventPostParams{Project: a.UUID, IdempotencyKey: "same", Summary: "retry"})
	if first.UUID != second.UUID || second.Created {
		t.Fatalf("replay = %#v then %#v", first, second)
	}
	other := postProjectEvent(t, api, ProjectEventPostParams{Project: b.UUID, IdempotencyKey: "same"})
	if other.UUID == first.UUID || !other.Created {
		t.Fatalf("cross-project key reused row: %#v", other)
	}
}

func TestProjectEventsI8UnprunedNoDeletionOwner(t *testing.T) {
	api, s := newMonitorAPI(t)
	a := createProjectEventContainer(t, s, "i8a", "project", nil)
	b := createProjectEventContainer(t, s, "i8b", "project", nil)
	d := createProjectEventContainer(t, s, "inbox", "directory", &a.UUID)
	task := createTimelineTask(t, s, d.UUID, "task", "open", "")
	posted := postProjectEvent(t, api, ProjectEventPostParams{Task: task.ID, Type: "retention.fact"})
	setTaskState(t, api, task.ID, "in_progress")
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
	shown, err := api.ProjectEventGet(context.Background(), ProjectEventGetParams{ProjectEvent: posted.UUID})
	if err != nil || shown.TaskUUID != nil {
		t.Fatalf("show after deletion = %#v %v", shown, err)
	}
	view, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{Container: b.UUID, Scope: "subtree", EntriesOnly: true})
	if err != nil || !timelineContainsIDs(view.Entries, 0, 1) {
		t.Fatalf("moved retained row absent: %#v %v", view, err)
	}
	if countEventEntries(view.Entries) == 0 {
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
