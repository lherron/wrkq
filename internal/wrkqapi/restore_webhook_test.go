package wrkqapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/webhooks"
)

// webhookSink is an httptest server that records every webhook POST payload,
// keyed by ticket_id. DispatchTaskEvent blocks on delivery (dispatchURLs waits
// on its worker group), so every dispatch a restore makes has arrived by the
// time API.TaskRestore returns — the collection below is race-free.
type webhookSink struct {
	server   *httptest.Server
	mu       chan struct{}
	payloads map[string]webhooks.Payload
}

func newWebhookSink(t *testing.T) *webhookSink {
	t.Helper()
	s := &webhookSink{
		mu:       make(chan struct{}, 1),
		payloads: map[string]webhooks.Payload{},
	}
	s.mu <- struct{}{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		body, _ := io.ReadAll(r.Body)
		var p webhooks.Payload
		_ = json.Unmarshal(body, &p)
		<-s.mu
		s.payloads[p.TicketID] = p
		s.mu <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *webhookSink) get(ticketID string) (webhooks.Payload, bool) {
	<-s.mu
	defer func() { s.mu <- struct{}{} }()
	p, ok := s.payloads[ticketID]
	return p, ok
}

func newWebhookAPI(t *testing.T) (*API, *store.Store, *db.DB) {
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
	return New(database, nil, "", "", 0), store.New(database), database
}

// TestTaskRestore_DispatchesWebhook proves the RPC restore path dispatches the
// restore webhook for the ROOT task (daedalus hrcchat#10201 gap 1): an `updated`
// event with the archived→open transition, the per-field Changed/Changes, and
// Origin.Via="rpc" (the RPC-mutation convention, distinct from legacy "cli").
func TestTaskRestore_DispatchesWebhook(t *testing.T) {
	api, s, _ := newWebhookAPI(t)
	sink := newWebhookSink(t)

	actorUUID := "00000000-0000-4000-8000-0000000000a0" // wrkq-system (seeded)
	container, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "proj", Kind: "project"})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	urls, _ := json.Marshal([]string{sink.server.URL + "/hook/{ticket_id}"})
	if _, err := s.Containers.UpdateFields(actorUUID, container.UUID, map[string]any{"webhook_urls": string(urls)}, 0); err != nil {
		t.Fatalf("set webhook_urls: %v", err)
	}

	task, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug: "gone", Title: "Gone", ProjectUUID: container.UUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	// Archive it through the RPC delete path (mode=archive) so the precondition is
	// real.
	if _, err := api.TaskDelete(context.Background(), TaskDeleteParams{Task: task.ID, Mode: "archive"}); err != nil {
		t.Fatalf("archive: %v", err)
	}

	if _, err := api.TaskRestore(context.Background(), TaskRestoreParams{
		Task: task.ID, Comment: "back online", Priority: 1,
	}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	p, ok := sink.get(task.ID)
	if !ok {
		t.Fatalf("root restore did not dispatch a webhook for %s", task.ID)
	}
	if p.Event != "updated" {
		t.Errorf("event: want updated, got %q", p.Event)
	}
	if p.Origin.Via != "rpc" {
		t.Errorf("origin.via: want rpc, got %q", p.Origin.Via)
	}
	if p.Transition == nil || p.Transition.From == nil || *p.Transition.From != "archived" || p.Transition.To == nil || *p.Transition.To != "open" {
		t.Errorf("transition: want archived->open, got %+v", p.Transition)
	}
	if !containsStr(p.Changed, "state") || !containsStr(p.Changed, "priority") {
		t.Errorf("changed must include state+priority, got %v", p.Changed)
	}
	if _, ok := p.Changes["priority"]; !ok {
		t.Errorf("changes must include priority, got %v", p.Changes)
	}
	if p.State != "open" {
		t.Errorf("post-restore state: want open, got %q", p.State)
	}
}

// TestTaskRestore_CascadeDispatchesSubtaskWebhook proves cascade-restored subtasks
// each dispatch their own restore webhook (daedalus gap 1, cascade arm).
func TestTaskRestore_CascadeDispatchesSubtaskWebhook(t *testing.T) {
	api, s, database := newWebhookAPI(t)
	sink := newWebhookSink(t)

	actorUUID := "00000000-0000-4000-8000-0000000000a0"
	container, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "proj", Kind: "project"})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	urls, _ := json.Marshal([]string{sink.server.URL + "/hook/{ticket_id}"})
	if _, err := s.Containers.UpdateFields(actorUUID, container.UUID, map[string]any{"webhook_urls": string(urls)}, 0); err != nil {
		t.Fatalf("set webhook_urls: %v", err)
	}

	parent, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug: "parent", Title: "Parent", ProjectUUID: container.UUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}
	child, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug: "child", Title: "Child", ProjectUUID: container.UUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	// Link child→parent and archive both (independent archives — restore cascades).
	if _, err := database.Exec("UPDATE tasks SET parent_task_uuid = ? WHERE uuid = ?", parent.UUID, child.UUID); err != nil {
		t.Fatalf("link child: %v", err)
	}
	if _, err := api.TaskDelete(context.Background(), TaskDeleteParams{Task: child.ID, Mode: "archive"}); err != nil {
		t.Fatalf("archive child: %v", err)
	}
	if _, err := api.TaskDelete(context.Background(), TaskDeleteParams{Task: parent.ID, Mode: "archive"}); err != nil {
		t.Fatalf("archive parent: %v", err)
	}

	if _, err := api.TaskRestore(context.Background(), TaskRestoreParams{Task: parent.ID}); err != nil {
		t.Fatalf("restore parent: %v", err)
	}

	if _, ok := sink.get(parent.ID); !ok {
		t.Errorf("parent restore did not dispatch a webhook")
	}
	cp, ok := sink.get(child.ID)
	if !ok {
		t.Fatalf("cascade-restored child did not dispatch a webhook")
	}
	if cp.Origin.Via != "rpc" {
		t.Errorf("child origin.via: want rpc, got %q", cp.Origin.Via)
	}
	if cp.Transition == nil || cp.Transition.To == nil || *cp.Transition.To != "open" {
		t.Errorf("child transition: want ->open, got %+v", cp.Transition)
	}
	if cp.State != "open" {
		t.Errorf("child post-restore state: want open, got %q", cp.State)
	}
}

// TestTaskRestore_MoveWebhookChangedPayload proves --to (move-on-restore) carries
// the move into the webhook changed payload (project_uuid + slug in Changed), so a
// consumer sees the relocation — the changed-payload drift daedalus called out.
func TestTaskRestore_MoveWebhookChangedPayload(t *testing.T) {
	api, s, _ := newWebhookAPI(t)
	sink := newWebhookSink(t)

	actorUUID := "00000000-0000-4000-8000-0000000000a0"
	src, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "src", Kind: "project"})
	if err != nil {
		t.Fatalf("create src: %v", err)
	}
	dest, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "dest", Kind: "project"})
	if err != nil {
		t.Fatalf("create dest: %v", err)
	}
	urls, _ := json.Marshal([]string{sink.server.URL + "/hook/{ticket_id}"})
	// Webhooks resolve against the task's project at dispatch time; set on both.
	for _, c := range []string{src.UUID, dest.UUID} {
		if _, err := s.Containers.UpdateFields(actorUUID, c, map[string]any{"webhook_urls": string(urls)}, 0); err != nil {
			t.Fatalf("set webhook_urls: %v", err)
		}
	}

	task, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug: "mover", Title: "Mover", ProjectUUID: src.UUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := api.TaskDelete(context.Background(), TaskDeleteParams{Task: task.ID, Mode: "archive"}); err != nil {
		t.Fatalf("archive: %v", err)
	}

	if _, err := api.TaskRestore(context.Background(), TaskRestoreParams{Task: task.ID, ToPath: "dest/mover"}); err != nil {
		t.Fatalf("restore --to: %v", err)
	}

	p, ok := sink.get(task.ID)
	if !ok {
		t.Fatalf("move-on-restore did not dispatch a webhook")
	}
	if !containsStr(p.Changed, "project_uuid") || !containsStr(p.Changed, "slug") {
		t.Errorf("changed must include project_uuid+slug for a move, got %v", p.Changed)
	}
	if ch, ok := p.Changes["project_uuid"]; !ok || ch.To != dest.UUID {
		t.Errorf("changes.project_uuid.to: want %s, got %+v (present=%v)", dest.UUID, ch, ok)
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
