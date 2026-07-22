package wrkqapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lherron/wrkq/internal/store"
)

func TestCommentAddRollsBackWhenCreatedEventCannotBeWritten(t *testing.T) {
	api, s, database := newWebhookAPI(t)
	actorUUID := "00000000-0000-4000-8000-0000000000a0"

	container, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "proj", Kind: "project"})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	task, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug: "atomic-comment", Title: "Atomic comment", ProjectUUID: container.UUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if _, err := database.Exec(`
		CREATE TRIGGER reject_comment_created
		BEFORE INSERT ON event_log
		WHEN NEW.event_type = 'comment.created'
		BEGIN
			SELECT RAISE(ABORT, 'forced comment event failure');
		END
	`); err != nil {
		t.Fatalf("create event failure trigger: %v", err)
	}

	if _, err := api.CommentAdd(context.Background(), CommentAddParams{Task: task.ID, Body: "must roll back"}); err == nil {
		t.Error("CommentAdd succeeded when comment.created could not be written")
	}

	var commentCount, eventCount int
	if err := database.QueryRow("SELECT COUNT(*) FROM comments WHERE task_uuid = ? AND body = ?", task.UUID, "must roll back").Scan(&commentCount); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM event_log WHERE event_type = 'comment.created' AND resource_type = 'comment'").Scan(&eventCount); err != nil {
		t.Fatalf("count comment events: %v", err)
	}
	if commentCount != 0 || eventCount != 0 {
		t.Fatalf("comment/event transaction was not rolled back: comments=%d events=%d", commentCount, eventCount)
	}
}

func TestCommentAddProducesCanonicalCreatedEvent(t *testing.T) {
	api, s := newMonitorAPI(t)
	projectUUID := seedMonitorProject(t, s)
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "comment-event", Title: "Comment event", ProjectUUID: projectUUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	comment, err := api.CommentAdd(context.Background(), CommentAddParams{Task: task.ID, Body: "event-backed comment"})
	if err != nil {
		t.Fatalf("CommentAdd: %v", err)
	}
	view, err := api.MonitorEventsView(context.Background(), MonitorEventsViewParams{
		Tasks: []string{task.ID}, EventTypes: []string{"comment.created"}, Cursor: 0,
	})
	if err != nil {
		t.Fatalf("MonitorEventsView: %v", err)
	}
	if len(view.Items) != 1 {
		t.Fatalf("comment.created event count = %d, want 1: %+v", len(view.Items), view.Items)
	}
	got := view.Items[0]
	if got.EventType != "comment.created" || got.ResourceType != "comment" || got.ResourceUUID == nil || *got.ResourceUUID != comment.UUID {
		t.Fatalf("unexpected comment event: %+v", got)
	}

	var legacyCount int
	if err := api.db.QueryRow("SELECT COUNT(*) FROM event_log WHERE event_type = 'comment_added'").Scan(&legacyCount); err != nil {
		t.Fatalf("count legacy events: %v", err)
	}
	if legacyCount != 0 {
		t.Fatalf("event_log contains %d legacy comment_added rows", legacyCount)
	}
}

func TestCommentAddDispatchesLegacyWebhookProjection(t *testing.T) {
	api, s, _ := newWebhookAPI(t)
	sink := newWebhookSink(t)
	actorUUID := "00000000-0000-4000-8000-0000000000a0"

	container, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "proj", Kind: "project"})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	task, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug: "comment-hook", Title: "Comment hook", ProjectUUID: container.UUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	urls, err := json.Marshal([]string{sink.server.URL + "/hook/{ticket_id}"})
	if err != nil {
		t.Fatalf("marshal webhook URLs: %v", err)
	}
	if _, err := s.Containers.UpdateFields(actorUUID, container.UUID, map[string]any{"webhook_urls": string(urls)}, 0); err != nil {
		t.Fatalf("set webhook_urls: %v", err)
	}

	if _, err := api.CommentAdd(context.Background(), CommentAddParams{Task: task.ID, Body: "notify compatibility consumer"}); err != nil {
		t.Fatalf("CommentAdd: %v", err)
	}
	payload, ok := sink.get(task.ID)
	if !ok {
		t.Fatalf("comment add did not dispatch a task webhook for %s", task.ID)
	}
	if payload.Event != "comment_added" {
		t.Fatalf("webhook event = %q, want comment_added", payload.Event)
	}
}
