package store

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/webhooks"
)

func setupWebhookTestDB(t *testing.T) *db.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func setupWebhookTestActor(t *testing.T, database *db.DB) string {
	t.Helper()
	result, err := database.Exec(`
		INSERT INTO actors (id, slug, role) VALUES ('', 'test-actor', 'human')
	`)
	if err != nil {
		t.Fatalf("failed to create test actor: %v", err)
	}
	rowID, _ := result.LastInsertId()
	var uuid string
	if err := database.QueryRow("SELECT uuid FROM actors WHERE rowid = ?", rowID).Scan(&uuid); err != nil {
		t.Fatalf("failed to get actor uuid: %v", err)
	}
	return uuid
}

func TestTaskStoreUpdateFieldsDispatchesWebhook(t *testing.T) {
	database := setupWebhookTestDB(t)
	actorUUID := setupWebhookTestActor(t, database)
	s := New(database)

	container, err := s.Containers.Create(actorUUID, ContainerCreateParams{Slug: "project"})
	if err != nil {
		t.Fatalf("failed to create container: %v", err)
	}

	result, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "task",
		Title:       "Task",
		Description: "Test",
		ProjectUUID: container.UUID,
		State:       "open",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	calls := make(chan struct {
		path    string
		payload webhooks.Payload
	}, 1)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		var payload webhooks.Payload
		_ = json.Unmarshal(body, &payload)
		calls <- struct {
			path    string
			payload webhooks.Payload
		}{path: r.URL.Path, payload: payload}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	webhookURLs, _ := json.Marshal([]string{server.URL + "/hook/{ticket_id}", "ftp://invalid"})
	_, err = s.Containers.UpdateFields(actorUUID, container.UUID, map[string]interface{}{"webhook_urls": string(webhookURLs)}, 0)
	if err != nil {
		t.Fatalf("failed to set webhook urls: %v", err)
	}

	if _, err := s.Tasks.UpdateFields(actorUUID, result.UUID, map[string]interface{}{"state": "in_progress"}, 0); err != nil {
		t.Fatalf("failed to update task: %v", err)
	}

	select {
	case got := <-calls:
		expectedPath := "/hook/" + result.ID
		if got.path != expectedPath {
			t.Fatalf("unexpected path: %s (expected %s)", got.path, expectedPath)
		}
		if got.payload.TicketID != result.ID {
			t.Fatalf("unexpected ticket_id: %s", got.payload.TicketID)
		}
		if got.payload.TicketUUID != result.UUID {
			t.Fatalf("unexpected ticket_uuid: %s", got.payload.TicketUUID)
		}
		if got.payload.ProjectID != container.ID {
			t.Fatalf("unexpected project_id: %s", got.payload.ProjectID)
		}
		if got.payload.ProjectUUID != container.UUID {
			t.Fatalf("unexpected project_uuid: %s", got.payload.ProjectUUID)
		}
		if got.payload.State != "in_progress" {
			t.Fatalf("unexpected state: %s", got.payload.State)
		}
		if got.payload.Priority != 2 {
			t.Fatalf("unexpected priority: %d", got.payload.Priority)
		}
		if got.payload.Kind != "task" {
			t.Fatalf("unexpected kind: %s", got.payload.Kind)
		}
		if got.payload.ETag != 3 {
			t.Fatalf("unexpected etag: %d", got.payload.ETag)
		}
		if got.payload.RunStatus != nil {
			t.Fatalf("unexpected run_status: %s", *got.payload.RunStatus)
		}
		if got.payload.Resolution != nil {
			t.Fatalf("unexpected resolution: %s", *got.payload.Resolution)
		}
		if got.payload.CPRunID != nil || got.payload.CPSessionID != nil || got.payload.CPProjectID != nil || got.payload.SDKSessionID != nil {
			t.Fatalf("unexpected run linkage payload: %+v", got.payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for webhook")
	}
}
