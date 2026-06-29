package t04891smoketestlabeledge

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/webhooks"
)

// T-04891 red bar: exercise the public store API and observe the emitted
// webhook payload. The failure must be the payload edge shape, not source text.
func TestNeedsSmoketestWebhookLabelEdges(t *testing.T) {
	database := setupDB(t)
	actorUUID := setupActor(t, database)
	s := store.New(database)

	project, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "project", Kind: "project"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	calls := make(chan webhooks.Payload, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		body, _ := io.ReadAll(r.Body)
		var payload webhooks.Payload
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode webhook payload: %v", err)
		}
		calls <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	webhookURLs, _ := json.Marshal([]string{server.URL + "/hook"})
	if _, err := s.Containers.UpdateFields(actorUUID, project.UUID, map[string]interface{}{"webhook_urls": string(webhookURLs)}, 0); err != nil {
		t.Fatalf("set webhook urls: %v", err)
	}

	t.Run("created event exposes a decoded needs_smoketest label edge", func(t *testing.T) {
		task, err := s.Tasks.Create(actorUUID, store.CreateParams{
			Slug:        "created-needs-smoke",
			Title:       "Created needs smoke",
			ProjectUUID: project.UUID,
			State:       "open",
			Priority:    2,
			Labels:      `["needs_smoketest","ui"]`,
		})
		if err != nil {
			t.Fatalf("create task: %v", err)
		}

		payload := waitForWebhook(t, calls, task.UUID)
		if payload.Event != "created" {
			t.Fatalf("event = %q, want created", payload.Event)
		}
		if !containsString(payload.Labels, "needs_smoketest") {
			t.Fatalf("top-level labels = %#v, want needs_smoketest", payload.Labels)
		}
		change, ok := payload.Changes["labels"]
		if !ok {
			t.Fatalf("missing labels change: %#v", payload.Changes)
		}
		if change.From != nil {
			t.Fatalf("created labels from = %#v, want nil", change.From)
		}
		toLabels, ok := change.To.([]interface{})
		if !ok {
			t.Fatalf("created labels to = %#v (%T), want JSON label array", change.To, change.To)
		}
		if !containsAnyString(toLabels, "needs_smoketest") {
			t.Fatalf("created labels to = %#v, want needs_smoketest", toLabels)
		}
	})

	t.Run("updated event exposes decoded from and to arrays for label-addition edge", func(t *testing.T) {
		task, err := s.Tasks.Create(actorUUID, store.CreateParams{
			Slug:        "updated-needs-smoke",
			Title:       "Updated needs smoke",
			ProjectUUID: project.UUID,
			State:       "open",
			Priority:    2,
			Labels:      `["ui"]`,
		})
		if err != nil {
			t.Fatalf("create task: %v", err)
		}
		_ = waitForWebhook(t, calls, task.UUID)

		if _, err := s.Tasks.UpdateFields(actorUUID, task.UUID, map[string]interface{}{
			"labels": `["ui","needs_smoketest"]`,
		}, 0); err != nil {
			t.Fatalf("add needs_smoketest label: %v", err)
		}

		payload := waitForWebhook(t, calls, task.UUID)
		if payload.Event != "updated" {
			t.Fatalf("event = %q, want updated", payload.Event)
		}
		if !reflect.DeepEqual(payload.Changed, []string{"labels"}) {
			t.Fatalf("changed = %#v, want [labels]", payload.Changed)
		}
		if !containsString(payload.Labels, "needs_smoketest") {
			t.Fatalf("top-level labels = %#v, want needs_smoketest", payload.Labels)
		}
		change, ok := payload.Changes["labels"]
		if !ok {
			t.Fatalf("missing labels change: %#v", payload.Changes)
		}
		fromLabels, ok := change.From.([]interface{})
		if !ok {
			t.Fatalf("updated labels from = %#v (%T), want JSON label array instead of storage string", change.From, change.From)
		}
		toLabels, ok := change.To.([]interface{})
		if !ok {
			t.Fatalf("updated labels to = %#v (%T), want JSON label array", change.To, change.To)
		}
		if containsAnyString(fromLabels, "needs_smoketest") {
			t.Fatalf("updated labels from = %#v, must not already include needs_smoketest", fromLabels)
		}
		if !containsAnyString(toLabels, "needs_smoketest") {
			t.Fatalf("updated labels to = %#v, want needs_smoketest", toLabels)
		}
	})
}

func setupDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func setupActor(t *testing.T, database *db.DB) string {
	t.Helper()
	result, err := database.Exec(`
		INSERT INTO actors (id, slug, role) VALUES ('', 't04891-actor', 'human')
	`)
	if err != nil {
		t.Fatalf("create actor: %v", err)
	}
	rowID, _ := result.LastInsertId()
	var uuid string
	if err := database.QueryRow("SELECT uuid FROM actors WHERE rowid = ?", rowID).Scan(&uuid); err != nil {
		t.Fatalf("load actor uuid: %v", err)
	}
	return uuid
}

func waitForWebhook(t *testing.T, calls <-chan webhooks.Payload, taskUUID string) webhooks.Payload {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case payload := <-calls:
			if payload.TicketUUID == taskUUID {
				return payload
			}
		case <-timeout:
			t.Fatalf("timed out waiting for webhook for %s", taskUUID)
		}
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func containsAnyString(values []interface{}, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
