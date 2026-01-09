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

	meta := `{"triage_status":"queued"}`
	result, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "task",
		Title:       "Task",
		Description: "Test",
		ProjectUUID: container.UUID,
		State:       "open",
		Priority:    2,
		Meta:        &meta,
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
		if string(got.payload.Meta) == "" || string(got.payload.Meta) == "null" {
			t.Fatalf("unexpected meta payload: %s", string(got.payload.Meta))
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

func TestUnblockWebhookSingleBlocker(t *testing.T) {
	database := setupWebhookTestDB(t)
	actorUUID := setupWebhookTestActor(t, database)
	s := New(database)

	container, err := s.Containers.Create(actorUUID, ContainerCreateParams{Slug: "project"})
	if err != nil {
		t.Fatalf("failed to create container: %v", err)
	}

	// Create task A (the blocker)
	taskA, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "blocker-task",
		Title:       "Blocker Task",
		Description: "This task blocks another",
		ProjectUUID: container.UUID,
		State:       "open",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("failed to create task A: %v", err)
	}

	// Create task B (blocked by A)
	taskB, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "blocked-task",
		Title:       "Blocked Task",
		Description: "This task is blocked by task A",
		ProjectUUID: container.UUID,
		State:       "blocked",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("failed to create task B: %v", err)
	}

	// Create blocking relation: A blocks B
	_, err = database.Exec(`
		INSERT INTO task_relations (from_task_uuid, to_task_uuid, kind, created_by_actor_uuid)
		VALUES (?, ?, 'blocks', ?)
	`, taskA.UUID, taskB.UUID, actorUUID)
	if err != nil {
		t.Fatalf("failed to create blocking relation: %v", err)
	}

	// Set up webhook server to capture calls
	calls := make(chan struct {
		path    string
		payload webhooks.Payload
	}, 10) // Buffer for multiple webhooks

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

	// Configure webhooks on the container
	webhookURLs, _ := json.Marshal([]string{server.URL + "/hook/{ticket_id}"})
	_, err = s.Containers.UpdateFields(actorUUID, container.UUID, map[string]interface{}{"webhook_urls": string(webhookURLs)}, 0)
	if err != nil {
		t.Fatalf("failed to set webhook urls: %v", err)
	}

	// Complete task A - this should trigger webhooks for both A and B
	if _, err := s.Tasks.UpdateFields(actorUUID, taskA.UUID, map[string]interface{}{"state": "completed"}, 0); err != nil {
		t.Fatalf("failed to complete task A: %v", err)
	}

	// Collect all webhook calls (expect 2: one for A, one for B)
	receivedWebhooks := make(map[string]webhooks.Payload)
	timeout := time.After(3 * time.Second)

	for i := 0; i < 2; i++ {
		select {
		case got := <-calls:
			receivedWebhooks[got.payload.TicketUUID] = got.payload
		case <-timeout:
			t.Fatalf("timed out waiting for webhook %d, received %d so far", i+1, len(receivedWebhooks))
		}
	}

	// Verify we got webhook for task A (the completed task)
	if payload, ok := receivedWebhooks[taskA.UUID]; !ok {
		t.Fatalf("did not receive webhook for completed task A")
	} else if payload.State != "completed" {
		t.Fatalf("task A webhook has wrong state: %s (expected completed)", payload.State)
	}

	// Verify we got webhook for task B (the unblocked task)
	if _, ok := receivedWebhooks[taskB.UUID]; !ok {
		t.Fatalf("did not receive webhook for unblocked task B")
	}
}

func TestUnblockWebhookMultipleBlockers(t *testing.T) {
	database := setupWebhookTestDB(t)
	actorUUID := setupWebhookTestActor(t, database)
	s := New(database)

	container, err := s.Containers.Create(actorUUID, ContainerCreateParams{Slug: "project"})
	if err != nil {
		t.Fatalf("failed to create container: %v", err)
	}

	// Create task A1 (first blocker)
	taskA1, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "blocker-1",
		Title:       "Blocker 1",
		Description: "First blocker",
		ProjectUUID: container.UUID,
		State:       "open",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("failed to create task A1: %v", err)
	}

	// Create task A2 (second blocker)
	taskA2, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "blocker-2",
		Title:       "Blocker 2",
		Description: "Second blocker",
		ProjectUUID: container.UUID,
		State:       "open",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("failed to create task A2: %v", err)
	}

	// Create task B (blocked by both A1 and A2)
	taskB, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "blocked-task",
		Title:       "Blocked Task",
		Description: "Blocked by two tasks",
		ProjectUUID: container.UUID,
		State:       "blocked",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("failed to create task B: %v", err)
	}

	// Create blocking relations: A1 blocks B, A2 blocks B
	_, err = database.Exec(`
		INSERT INTO task_relations (from_task_uuid, to_task_uuid, kind, created_by_actor_uuid)
		VALUES (?, ?, 'blocks', ?), (?, ?, 'blocks', ?)
	`, taskA1.UUID, taskB.UUID, actorUUID, taskA2.UUID, taskB.UUID, actorUUID)
	if err != nil {
		t.Fatalf("failed to create blocking relations: %v", err)
	}

	// Set up webhook server
	calls := make(chan struct {
		path    string
		payload webhooks.Payload
	}, 10)

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

	webhookURLs, _ := json.Marshal([]string{server.URL + "/hook/{ticket_id}"})
	_, err = s.Containers.UpdateFields(actorUUID, container.UUID, map[string]interface{}{"webhook_urls": string(webhookURLs)}, 0)
	if err != nil {
		t.Fatalf("failed to set webhook urls: %v", err)
	}

	// Complete task A1 - task B should NOT be unblocked yet (A2 still blocks it)
	if _, err := s.Tasks.UpdateFields(actorUUID, taskA1.UUID, map[string]interface{}{"state": "completed"}, 0); err != nil {
		t.Fatalf("failed to complete task A1: %v", err)
	}

	// Should only get 1 webhook (for A1)
	select {
	case got := <-calls:
		if got.payload.TicketUUID != taskA1.UUID {
			t.Fatalf("expected webhook for A1, got %s", got.payload.TicketUUID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for A1 webhook")
	}

	// Make sure no additional webhook came (B should still be blocked)
	select {
	case got := <-calls:
		t.Fatalf("unexpected webhook received for %s (B should still be blocked)", got.payload.TicketUUID)
	case <-time.After(500 * time.Millisecond):
		// Good - no extra webhook
	}

	// Complete task A2 - now B should be unblocked
	if _, err := s.Tasks.UpdateFields(actorUUID, taskA2.UUID, map[string]interface{}{"state": "completed"}, 0); err != nil {
		t.Fatalf("failed to complete task A2: %v", err)
	}

	// Should get 2 webhooks (for A2 and for B)
	receivedWebhooks := make(map[string]bool)
	for i := 0; i < 2; i++ {
		select {
		case got := <-calls:
			receivedWebhooks[got.payload.TicketUUID] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for webhook %d", i+1)
		}
	}

	if !receivedWebhooks[taskA2.UUID] {
		t.Fatalf("did not receive webhook for completed task A2")
	}
	if !receivedWebhooks[taskB.UUID] {
		t.Fatalf("did not receive webhook for unblocked task B")
	}
}

func TestUnblockWebhookCancelledState(t *testing.T) {
	database := setupWebhookTestDB(t)
	actorUUID := setupWebhookTestActor(t, database)
	s := New(database)

	container, err := s.Containers.Create(actorUUID, ContainerCreateParams{Slug: "project"})
	if err != nil {
		t.Fatalf("failed to create container: %v", err)
	}

	// Create blocker task
	blocker, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "blocker",
		Title:       "Blocker",
		ProjectUUID: container.UUID,
		State:       "open",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("failed to create blocker: %v", err)
	}

	// Create blocked task
	blocked, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "blocked",
		Title:       "Blocked",
		ProjectUUID: container.UUID,
		State:       "blocked",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("failed to create blocked task: %v", err)
	}

	// Create blocking relation
	_, err = database.Exec(`
		INSERT INTO task_relations (from_task_uuid, to_task_uuid, kind, created_by_actor_uuid)
		VALUES (?, ?, 'blocks', ?)
	`, blocker.UUID, blocked.UUID, actorUUID)
	if err != nil {
		t.Fatalf("failed to create blocking relation: %v", err)
	}

	// Set up webhook server
	calls := make(chan webhooks.Payload, 10)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		var payload webhooks.Payload
		_ = json.Unmarshal(body, &payload)
		calls <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	webhookURLs, _ := json.Marshal([]string{server.URL + "/hook"})
	_, err = s.Containers.UpdateFields(actorUUID, container.UUID, map[string]interface{}{"webhook_urls": string(webhookURLs)}, 0)
	if err != nil {
		t.Fatalf("failed to set webhook urls: %v", err)
	}

	// Cancel the blocker (should also unblock the blocked task)
	if _, err := s.Tasks.UpdateFields(actorUUID, blocker.UUID, map[string]interface{}{"state": "cancelled"}, 0); err != nil {
		t.Fatalf("failed to cancel blocker: %v", err)
	}

	// Should get 2 webhooks
	receivedUUIDs := make(map[string]bool)
	for i := 0; i < 2; i++ {
		select {
		case got := <-calls:
			receivedUUIDs[got.TicketUUID] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for webhook %d", i+1)
		}
	}

	if !receivedUUIDs[blocker.UUID] {
		t.Fatalf("did not receive webhook for cancelled blocker")
	}
	if !receivedUUIDs[blocked.UUID] {
		t.Fatalf("did not receive webhook for unblocked task")
	}
}

func TestNoUnblockWebhookWhenAlreadyCompleted(t *testing.T) {
	database := setupWebhookTestDB(t)
	actorUUID := setupWebhookTestActor(t, database)
	s := New(database)

	container, err := s.Containers.Create(actorUUID, ContainerCreateParams{Slug: "project"})
	if err != nil {
		t.Fatalf("failed to create container: %v", err)
	}

	// Create blocker task that's already completed
	blocker, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "blocker",
		Title:       "Blocker",
		ProjectUUID: container.UUID,
		State:       "completed",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("failed to create blocker: %v", err)
	}

	// Create blocked task
	blocked, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "blocked",
		Title:       "Blocked",
		ProjectUUID: container.UUID,
		State:       "blocked",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("failed to create blocked task: %v", err)
	}

	// Create blocking relation
	_, err = database.Exec(`
		INSERT INTO task_relations (from_task_uuid, to_task_uuid, kind, created_by_actor_uuid)
		VALUES (?, ?, 'blocks', ?)
	`, blocker.UUID, blocked.UUID, actorUUID)
	if err != nil {
		t.Fatalf("failed to create blocking relation: %v", err)
	}

	// Set up webhook server
	calls := make(chan webhooks.Payload, 10)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		var payload webhooks.Payload
		_ = json.Unmarshal(body, &payload)
		calls <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	webhookURLs, _ := json.Marshal([]string{server.URL + "/hook"})
	_, err = s.Containers.UpdateFields(actorUUID, container.UUID, map[string]interface{}{"webhook_urls": string(webhookURLs)}, 0)
	if err != nil {
		t.Fatalf("failed to set webhook urls: %v", err)
	}

	// Update the already-completed blocker (not a state transition to completion)
	if _, err := s.Tasks.UpdateFields(actorUUID, blocker.UUID, map[string]interface{}{"title": "Updated Title"}, 0); err != nil {
		t.Fatalf("failed to update blocker: %v", err)
	}

	// Should only get 1 webhook (for the blocker's title update, not for unblocking)
	select {
	case got := <-calls:
		if got.TicketUUID != blocker.UUID {
			t.Fatalf("expected webhook for blocker, got %s", got.TicketUUID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for blocker webhook")
	}

	// Verify no additional webhook came for the blocked task
	select {
	case got := <-calls:
		t.Fatalf("unexpected webhook for %s", got.TicketUUID)
	case <-time.After(500 * time.Millisecond):
		// Good - no unblock webhook since blocker was already completed
	}
}

func TestWebhookPayloadIncludesBlockedBy(t *testing.T) {
	database := setupWebhookTestDB(t)
	actorUUID := setupWebhookTestActor(t, database)
	s := New(database)

	container, err := s.Containers.Create(actorUUID, ContainerCreateParams{Slug: "project"})
	if err != nil {
		t.Fatalf("failed to create container: %v", err)
	}

	// Create blocker task A
	taskA, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "blocker-a",
		Title:       "Blocker A",
		ProjectUUID: container.UUID,
		State:       "open",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("failed to create task A: %v", err)
	}

	// Create blocker task B
	taskB, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "blocker-b",
		Title:       "Blocker B",
		ProjectUUID: container.UUID,
		State:       "in_progress",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("failed to create task B: %v", err)
	}

	// Create blocked task C
	taskC, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "blocked-task",
		Title:       "Blocked Task",
		ProjectUUID: container.UUID,
		State:       "blocked",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("failed to create task C: %v", err)
	}

	// Create blocking relations: A blocks C, B blocks C
	_, err = database.Exec(`
		INSERT INTO task_relations (from_task_uuid, to_task_uuid, kind, created_by_actor_uuid)
		VALUES (?, ?, 'blocks', ?), (?, ?, 'blocks', ?)
	`, taskA.UUID, taskC.UUID, actorUUID, taskB.UUID, taskC.UUID, actorUUID)
	if err != nil {
		t.Fatalf("failed to create blocking relations: %v", err)
	}

	// Set up webhook server
	calls := make(chan webhooks.Payload, 10)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		var payload webhooks.Payload
		_ = json.Unmarshal(body, &payload)
		calls <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	webhookURLs, _ := json.Marshal([]string{server.URL + "/hook"})
	_, err = s.Containers.UpdateFields(actorUUID, container.UUID, map[string]interface{}{"webhook_urls": string(webhookURLs)}, 0)
	if err != nil {
		t.Fatalf("failed to set webhook urls: %v", err)
	}

	// Update task C to trigger a webhook
	if _, err := s.Tasks.UpdateFields(actorUUID, taskC.UUID, map[string]interface{}{"priority": 1}, 0); err != nil {
		t.Fatalf("failed to update task C: %v", err)
	}

	// Receive the webhook for task C
	var taskCPayload webhooks.Payload
	select {
	case taskCPayload = <-calls:
		if taskCPayload.TicketUUID != taskC.UUID {
			t.Fatalf("expected webhook for task C, got %s", taskCPayload.TicketUUID)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for task C webhook")
	}

	// Verify blocked_by contains both blockers
	if len(taskCPayload.BlockedBy) != 2 {
		t.Fatalf("expected 2 blockers, got %d: %+v", len(taskCPayload.BlockedBy), taskCPayload.BlockedBy)
	}

	// Create a map of blocker IDs for easier verification
	blockerIDs := make(map[string]string) // id -> state
	for _, blocker := range taskCPayload.BlockedBy {
		blockerIDs[blocker.ID] = blocker.State
	}

	// Verify task A is in blocked_by with correct state
	if state, ok := blockerIDs[taskA.ID]; !ok {
		t.Fatalf("task A (%s) not found in blocked_by", taskA.ID)
	} else if state != "open" {
		t.Fatalf("task A has wrong state in blocked_by: %s (expected open)", state)
	}

	// Verify task B is in blocked_by with correct state
	if state, ok := blockerIDs[taskB.ID]; !ok {
		t.Fatalf("task B (%s) not found in blocked_by", taskB.ID)
	} else if state != "in_progress" {
		t.Fatalf("task B has wrong state in blocked_by: %s (expected in_progress)", state)
	}

	// Now complete task A and verify blocked_by is updated
	if _, err := s.Tasks.UpdateFields(actorUUID, taskA.UUID, map[string]interface{}{"state": "completed"}, 0); err != nil {
		t.Fatalf("failed to complete task A: %v", err)
	}

	// Collect webhooks (expect A's completion webhook, then possibly C's)
	receivedPayloads := make(map[string]webhooks.Payload)
	for i := 0; i < 2; i++ {
		select {
		case got := <-calls:
			receivedPayloads[got.TicketUUID] = got
		case <-time.After(2 * time.Second):
			if i == 0 {
				t.Fatalf("timed out waiting for webhook")
			}
			// Second webhook might not come if C isn't fully unblocked
		}
	}

	// If we got a webhook for C, verify it only shows B as blocker now
	if payload, ok := receivedPayloads[taskC.UUID]; ok {
		if len(payload.BlockedBy) != 1 {
			t.Fatalf("after A completed, expected 1 blocker, got %d: %+v", len(payload.BlockedBy), payload.BlockedBy)
		}
		if payload.BlockedBy[0].ID != taskB.ID {
			t.Fatalf("expected remaining blocker to be B (%s), got %s", taskB.ID, payload.BlockedBy[0].ID)
		}
	}

	// Complete task B so C becomes fully unblocked
	if _, err := s.Tasks.UpdateFields(actorUUID, taskB.UUID, map[string]interface{}{"state": "completed"}, 0); err != nil {
		t.Fatalf("failed to complete task B: %v", err)
	}

	// Receive webhooks for B and C
	receivedPayloads = make(map[string]webhooks.Payload)
	for i := 0; i < 2; i++ {
		select {
		case got := <-calls:
			receivedPayloads[got.TicketUUID] = got
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for webhook %d", i+1)
		}
	}

	// Verify C's webhook now has empty blocked_by
	if payload, ok := receivedPayloads[taskC.UUID]; !ok {
		t.Fatalf("did not receive webhook for fully unblocked task C")
	} else if len(payload.BlockedBy) != 0 {
		t.Fatalf("expected empty blocked_by for fully unblocked task, got: %+v", payload.BlockedBy)
	}
}

func TestWebhookPayloadBlockedByOmittedWhenEmpty(t *testing.T) {
	database := setupWebhookTestDB(t)
	actorUUID := setupWebhookTestActor(t, database)
	s := New(database)

	container, err := s.Containers.Create(actorUUID, ContainerCreateParams{Slug: "project"})
	if err != nil {
		t.Fatalf("failed to create container: %v", err)
	}

	// Create a task with no blockers
	task, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "unblocked-task",
		Title:       "Unblocked Task",
		ProjectUUID: container.UUID,
		State:       "open",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("failed to create task: %v", err)
	}

	// Set up webhook server to capture raw JSON
	rawPayloads := make(chan []byte, 10)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		body, _ := io.ReadAll(r.Body)
		rawPayloads <- body
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	webhookURLs, _ := json.Marshal([]string{server.URL + "/hook"})
	_, err = s.Containers.UpdateFields(actorUUID, container.UUID, map[string]interface{}{"webhook_urls": string(webhookURLs)}, 0)
	if err != nil {
		t.Fatalf("failed to set webhook urls: %v", err)
	}

	// Update the task to trigger a webhook
	if _, err := s.Tasks.UpdateFields(actorUUID, task.UUID, map[string]interface{}{"state": "in_progress"}, 0); err != nil {
		t.Fatalf("failed to update task: %v", err)
	}

	// Receive the raw payload
	var rawPayload []byte
	select {
	case rawPayload = <-rawPayloads:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for webhook")
	}

	// Verify blocked_by is not present in the JSON (omitempty behavior)
	var payloadMap map[string]interface{}
	if err := json.Unmarshal(rawPayload, &payloadMap); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	if _, exists := payloadMap["blocked_by"]; exists {
		t.Fatalf("blocked_by should be omitted when empty, but found in payload: %s", string(rawPayload))
	}
}
