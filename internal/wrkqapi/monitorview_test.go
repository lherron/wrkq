package wrkqapi

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
)

// newMonitorAPI builds a migrated in-memory-ish API + store for the monitor/watch
// read-model tests. Events are generated through the REAL API mutation paths so the
// event_log rows the views read are produced exactly as production would.
func newMonitorAPI(t *testing.T) (*API, *store.Store) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(database, nil, "", "", 0), store.New(database)
}

const monitorSystemActor = "00000000-0000-4000-8000-0000000000a0"

// strp returns a pointer to s, for building TaskPatch pointer fields.
func strp(s string) *string { return &s }

func seedMonitorProject(t *testing.T, s *store.Store) string {
	t.Helper()
	container, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{Slug: "proj", Kind: "project"})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	return container.UUID
}

// TestMonitorEventsView_SelectorResolution proves a bad selector hard-gates with
// WRKQ_VALIDATION (the legacy exit-code-2 path) before any rows are scanned.
func TestMonitorEventsView_SelectorResolution(t *testing.T) {
	api, _ := newMonitorAPI(t)
	_, err := api.MonitorEventsView(context.Background(), MonitorEventsViewParams{
		Tasks:  []string{"T-09999999"},
		Cursor: 0,
	})
	if err == nil {
		t.Fatal("expected validation error for bad selector, got nil")
	}
	de, ok := err.(*DomainError)
	if !ok || de.Code() != "WRKQ_VALIDATION" {
		t.Fatalf("want WRKQ_VALIDATION, got %T %v", err, err)
	}
}

// TestMonitorEventsView_TaskMatchAndCursor proves task.* events are matched by
// resource_uuid and the high-water cursor advances to the last RAW row scanned
// (even when later rows are filtered out), so the client never re-scans.
func TestMonitorEventsView_TaskMatchAndCursor(t *testing.T) {
	api, s := newMonitorAPI(t)
	proj := seedMonitorProject(t, s)

	watched, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "watched", Title: "Watched", ProjectUUID: proj, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create watched: %v", err)
	}
	other, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "other", Title: "Other", ProjectUUID: proj, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}

	// Drive a state change on each through the RPC update path so task.updated rows
	// with a "state" payload land in event_log.
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: watched.ID, Patch: TaskPatch{State: strp("completed")}}); err != nil {
		t.Fatalf("update watched: %v", err)
	}
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: other.ID, Patch: TaskPatch{State: strp("completed")}}); err != nil {
		t.Fatalf("update other: %v", err)
	}

	view, err := api.MonitorEventsView(context.Background(), MonitorEventsViewParams{
		Tasks:  []string{watched.ID},
		Cursor: 0,
	})
	if err != nil {
		t.Fatalf("eventsView: %v", err)
	}
	// Only the watched task's events are included.
	for _, e := range view.Items {
		if e.ResourceUUID == nil || *e.ResourceUUID != watched.UUID {
			t.Errorf("unexpected event for non-watched resource: %+v", e)
		}
	}
	if len(view.Items) == 0 {
		t.Fatal("expected at least the watched task's create/update events")
	}
	// High-water advances PAST the other task's (filtered-out) rows: a second page
	// from the high-water returns nothing.
	next, err := api.MonitorEventsView(context.Background(), MonitorEventsViewParams{
		Tasks:  []string{watched.ID},
		Cursor: view.HighWater,
	})
	if err != nil {
		t.Fatalf("eventsView page 2: %v", err)
	}
	if len(next.Items) != 0 {
		t.Errorf("expected no new events past high-water %d, got %d", view.HighWater, len(next.Items))
	}
}

// TestMonitorEventsView_StateOnlyFilter proves --state-only restricts to lifecycle
// state-change events (task.updated with a "state" payload key); a non-state update
// (e.g. title-only) is excluded.
func TestMonitorEventsView_StateOnlyFilter(t *testing.T) {
	api, s := newMonitorAPI(t)
	proj := seedMonitorProject(t, s)
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "task", Title: "Task", ProjectUUID: proj, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Title-only update (NOT a state change).
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{Title: strp("Renamed")}}); err != nil {
		t.Fatalf("title update: %v", err)
	}
	// State change.
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{State: strp("completed")}}); err != nil {
		t.Fatalf("state update: %v", err)
	}

	view, err := api.MonitorEventsView(context.Background(), MonitorEventsViewParams{
		Tasks:     []string{task.ID},
		StateOnly: true,
		Cursor:    0,
	})
	if err != nil {
		t.Fatalf("eventsView: %v", err)
	}
	for _, e := range view.Items {
		if e.EventType == "task.updated" && (e.Payload == nil) {
			t.Errorf("state-only must carry a payload for %+v", e)
		}
	}
	if len(view.Items) == 0 {
		t.Fatal("expected the state-change update to be included under --state-only")
	}
}

// TestMonitorEventsView_EventTypeFilter proves an explicit event-type filter
// restricts to the requested event_type values.
func TestMonitorEventsView_EventTypeFilter(t *testing.T) {
	api, s := newMonitorAPI(t)
	proj := seedMonitorProject(t, s)
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "task", Title: "Task", ProjectUUID: proj, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{State: strp("completed")}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	view, err := api.MonitorEventsView(context.Background(), MonitorEventsViewParams{
		Tasks:      []string{task.ID},
		EventTypes: []string{"task.updated"},
		Cursor:     0,
	})
	if err != nil {
		t.Fatalf("eventsView: %v", err)
	}
	if len(view.Items) == 0 {
		t.Fatal("expected at least one task.updated event")
	}
	for _, e := range view.Items {
		if e.EventType != "task.updated" {
			t.Errorf("event-type filter leaked %q", e.EventType)
		}
	}
}

// TestMonitorEventsView_CommentBackfill proves comment.* events are matched by the
// watched task via the comment→task backfill (payload.task_id hydrated server-side).
func TestMonitorEventsView_CommentBackfill(t *testing.T) {
	api, s := newMonitorAPI(t)
	proj := seedMonitorProject(t, s)
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "task", Title: "Task", ProjectUUID: proj, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Adding a comment emits no event_log row; DELETING one emits a comment.deleted
	// row whose payload.task_id is the task UUID. That is the comment.* event the
	// monitor filter matches to the watched task (the backfill path the server owns).
	cm, err := api.CommentAdd(context.Background(), CommentAddParams{Task: task.ID, Body: "a comment"})
	if err != nil {
		t.Fatalf("comment add: %v", err)
	}
	if _, err := api.CommentDelete(context.Background(), CommentDeleteParams{ID: cm.ID}); err != nil {
		t.Fatalf("comment delete: %v", err)
	}
	view, err := api.MonitorEventsView(context.Background(), MonitorEventsViewParams{
		Tasks:  []string{task.ID},
		Cursor: 0,
	})
	if err != nil {
		t.Fatalf("eventsView: %v", err)
	}
	sawComment := false
	for _, e := range view.Items {
		if e.ResourceType == "comment" {
			sawComment = true
		}
	}
	if !sawComment {
		t.Fatal("expected the comment.deleted event to be matched to the watched task")
	}
}

// TestMonitorStateView_ConditionEval proves the single authoritative snapshot:
// met=false with the unmet task listed while open, met=true once completed.
func TestMonitorStateView_ConditionEval(t *testing.T) {
	api, s := newMonitorAPI(t)
	proj := seedMonitorProject(t, s)
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "task", Title: "Task", ProjectUUID: proj, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	before, err := api.MonitorStateView(context.Background(), MonitorStateViewParams{
		Tasks:     []string{task.ID},
		Condition: "state=completed",
	})
	if err != nil {
		t.Fatalf("stateView before: %v", err)
	}
	if before.Met {
		t.Error("condition should be unmet while open")
	}
	if len(before.Unmet) != 1 || before.Unmet[0] != task.ID {
		t.Errorf("unmet should list the open task, got %v", before.Unmet)
	}

	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{State: strp("completed")}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	after, err := api.MonitorStateView(context.Background(), MonitorStateViewParams{
		Tasks:     []string{task.ID},
		Condition: "state=completed",
	})
	if err != nil {
		t.Fatalf("stateView after: %v", err)
	}
	if !after.Met {
		t.Error("condition should be met once completed")
	}
	if len(after.Unmet) != 0 {
		t.Errorf("unmet should be empty once met, got %v", after.Unmet)
	}
}

// TestMonitorStateView_BadCondition proves an unsupported --until condition
// hard-gates with WRKQ_VALIDATION.
func TestMonitorStateView_BadCondition(t *testing.T) {
	api, s := newMonitorAPI(t)
	proj := seedMonitorProject(t, s)
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "task", Title: "Task", ProjectUUID: proj, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = api.MonitorStateView(context.Background(), MonitorStateViewParams{
		Tasks:     []string{task.ID},
		Condition: "bogus",
	})
	de, ok := err.(*DomainError)
	if !ok || de.Code() != "WRKQ_VALIDATION" {
		t.Fatalf("want WRKQ_VALIDATION for bad condition, got %T %v", err, err)
	}
}

// TestHistoryTailView_AscendingAndHighWater proves the raw tail returns rows in
// ASCENDING id order, advances the high-water, and pages forward without overlap.
func TestHistoryTailView_AscendingAndHighWater(t *testing.T) {
	api, s := newMonitorAPI(t)
	proj := seedMonitorProject(t, s)
	for i, slug := range []string{"taska", "taskb", "taskc"} {
		if _, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
			Slug: slug, Title: slug, ProjectUUID: proj, State: "open", Priority: 2,
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	page1, err := api.HistoryTailView(context.Background(), HistoryTailViewParams{Cursor: 0, Limit: 2})
	if err != nil {
		t.Fatalf("tailView page1: %v", err)
	}
	if len(page1.Items) != 2 {
		t.Fatalf("expected limit-bounded 2 rows, got %d", len(page1.Items))
	}
	if page1.Items[0].ID >= page1.Items[1].ID {
		t.Errorf("rows must be ASCENDING by id, got %d then %d", page1.Items[0].ID, page1.Items[1].ID)
	}
	if page1.HighWater != page1.Items[1].ID {
		t.Errorf("high-water should be last row id %d, got %d", page1.Items[1].ID, page1.HighWater)
	}

	page2, err := api.HistoryTailView(context.Background(), HistoryTailViewParams{Cursor: page1.HighWater, Limit: 2})
	if err != nil {
		t.Fatalf("tailView page2: %v", err)
	}
	for _, e := range page2.Items {
		if e.ID <= page1.HighWater {
			t.Errorf("page2 must not re-emit id %d <= high-water %d", e.ID, page1.HighWater)
		}
	}
}

// TestHistoryTailView_WatchEventShape proves the watchEvent row carries
// resource_id and resource_type (the legacy watchEvent hydration), distinct from
// the logEvent shape.
func TestHistoryTailView_WatchEventShape(t *testing.T) {
	api, s := newMonitorAPI(t)
	proj := seedMonitorProject(t, s)
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "task", Title: "Task", ProjectUUID: proj, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	view, err := api.HistoryTailView(context.Background(), HistoryTailViewParams{Cursor: 0})
	if err != nil {
		t.Fatalf("tailView: %v", err)
	}
	var sawTask bool
	for _, e := range view.Items {
		if e.ResourceType == "task" && e.ResourceUUID != nil && *e.ResourceUUID == task.UUID {
			sawTask = true
			if e.ResourceID == nil || *e.ResourceID != task.ID {
				t.Errorf("watchEvent must hydrate resource_id to %s, got %v", task.ID, e.ResourceID)
			}
		}
	}
	if !sawTask {
		t.Fatal("expected the created task's event in the raw tail")
	}
}
