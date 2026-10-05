package store

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/webhooks"
)

// webhookFixture is a migrated store with one actor, ready to create
// projects that deliver task webhooks to a local capture server.
type webhookFixture struct {
	t     *testing.T
	s     *Store
	actor string
}

func newWebhookFixture(t *testing.T) *webhookFixture {
	t.Helper()
	database := setupTestDB(t)
	return &webhookFixture{t: t, s: New(database), actor: setupTestActor(t, database)}
}

// project creates a top-level project container.
func (f *webhookFixture) project(slug string) *ContainerCreateResult {
	f.t.Helper()
	container, err := f.s.Containers.Create(f.actor, ContainerCreateParams{Slug: slug, Kind: "project"})
	if err != nil {
		f.t.Fatalf("failed to create container %s: %v", slug, err)
	}
	return container
}

// task creates a priority-2 task in project with the given slug and state;
// edit adjusts any other create parameter.
func (f *webhookFixture) task(project *ContainerCreateResult, slug, state string, edit ...func(*CreateParams)) *CreateResult {
	f.t.Helper()
	params := CreateParams{Slug: slug, Title: slug, ProjectUUID: project.UUID, State: domain.State(state), Priority: 2}
	for _, e := range edit {
		e(&params)
	}
	result, err := f.s.Tasks.Create(f.actor, params)
	if err != nil {
		f.t.Fatalf("failed to create task %s: %v", slug, err)
	}
	return result
}

// blocks records a 'blocks' relation from each blocker to blocked.
func (f *webhookFixture) blocks(blocked *CreateResult, blockers ...*CreateResult) {
	f.t.Helper()
	for _, blocker := range blockers {
		if _, err := f.s.db.Exec(`
			INSERT INTO task_relations (from_task_uuid, to_task_uuid, kind, created_by_actor_uuid)
			VALUES (?, ?, 'blocks', ?)
		`, blocker.UUID, blocked.UUID, f.actor); err != nil {
			f.t.Fatalf("failed to create blocking relation: %v", err)
		}
	}
}

// update applies fields to a task as the fixture actor.
func (f *webhookFixture) update(task *CreateResult, fields map[string]interface{}) {
	f.t.Helper()
	if _, err := f.s.Tasks.UpdateFields(f.actor, task.UUID, fields, 0); err != nil {
		f.t.Fatalf("failed to update task %s with %v: %v", task.ID, fields, err)
	}
}

// capturedWebhook is one delivery received by the capture server.
type capturedWebhook struct {
	path    string
	raw     []byte
	payload webhooks.Payload
}

// captureWebhooks points project's webhook_urls at a fresh capture server,
// one URL per path (a path may carry {ticket_id}), plus any extra raw URLs,
// and returns the delivery stream.
func (f *webhookFixture) captureWebhooks(project *ContainerCreateResult, path string, extraURLs ...string) <-chan capturedWebhook {
	f.t.Helper()
	calls := make(chan capturedWebhook, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		body, _ := io.ReadAll(r.Body)
		var payload webhooks.Payload
		_ = json.Unmarshal(body, &payload)
		calls <- capturedWebhook{path: r.URL.Path, raw: body, payload: payload}
		w.WriteHeader(http.StatusNoContent)
	}))
	f.t.Cleanup(server.Close)

	webhookURLs, _ := json.Marshal(append([]string{server.URL + path}, extraURLs...))
	if _, err := f.s.Containers.UpdateFields(f.actor, project.UUID, map[string]interface{}{"webhook_urls": string(webhookURLs)}, 0); err != nil {
		f.t.Fatalf("failed to set webhook urls: %v", err)
	}
	return calls
}

// receiveWebhook waits for the next delivery.
func receiveWebhook(t *testing.T, calls <-chan capturedWebhook, within time.Duration) capturedWebhook {
	t.Helper()
	select {
	case got := <-calls:
		return got
	case <-time.After(within):
		t.Fatalf("timed out waiting for webhook")
		return capturedWebhook{}
	}
}

// receiveWebhooksByTicket waits for n deliveries and keys them by ticket UUID.
func receiveWebhooksByTicket(t *testing.T, calls <-chan capturedWebhook, n int, within time.Duration) map[string]webhooks.Payload {
	t.Helper()
	received := make(map[string]webhooks.Payload)
	timeout := time.After(within)
	for i := 0; i < n; i++ {
		select {
		case got := <-calls:
			received[got.payload.TicketUUID] = got.payload
		case <-timeout:
			t.Fatalf("timed out waiting for webhook %d, received %d so far", i+1, len(received))
		}
	}
	return received
}

// expectNoWebhook fails if another delivery arrives within quiet.
func expectNoWebhook(t *testing.T, calls <-chan capturedWebhook, quiet time.Duration, why string) {
	t.Helper()
	select {
	case got := <-calls:
		t.Fatalf("unexpected webhook received for %s (%s)", got.payload.TicketUUID, why)
	case <-time.After(quiet):
	}
}
