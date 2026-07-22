package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/store"
)

func TestTypedCommentsAttachToTasksAndContainersWithStampedEvents(t *testing.T) {
	api, s, database := newWebhookAPI(t)
	sink := newWebhookSink(t)
	project, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{Slug: "comments-project", Kind: "project"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	campaign, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{
		Slug: "comments-campaign", Kind: "directory", ParentUUID: &project.UUID,
	})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if _, err := database.Exec("UPDATE containers SET campaign_state = 'active' WHERE uuid = ?", campaign.UUID); err != nil {
		t.Fatalf("activate campaign: %v", err)
	}
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "resident-task", Title: "Resident task", ProjectUUID: campaign.UUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create resident task: %v", err)
	}
	urls, err := json.Marshal([]string{sink.server.URL + "/hook/{ticket_id}"})
	if err != nil {
		t.Fatalf("marshal webhook URLs: %v", err)
	}
	if _, err := s.Containers.UpdateFields(monitorSystemActor, project.UUID, map[string]any{"webhook_urls": string(urls)}, 0); err != nil {
		t.Fatalf("set webhook URLs: %v", err)
	}

	taskComment, err := api.CommentAdd(context.Background(), decodeCommentAddParams(t, map[string]any{
		"task": task.ID, "body": "task decision", "kind": "decision", "actor": "agent:comment-author",
	}))
	if err != nil {
		t.Fatalf("add typed task comment: %v", err)
	}
	assertStoredCommentParent(t, database, taskComment.UUID, task.UUID, "", "decision", nil)
	assertCommentCreatedStamps(t, database, taskComment.UUID, campaign.UUID, campaign.UUID)

	containerComment, err := api.CommentAdd(context.Background(), decodeCommentAddParams(t, map[string]any{
		"container": campaign.ID,
		"body":      "campaign digest",
		"kind":      "digest",
		"meta":      map[string]any{"event_log_id": float64(77)},
		"actor":     "agent:comment-author",
	}))
	if err != nil {
		t.Fatalf("add typed container comment: %v", err)
	}
	assertStoredCommentParent(t, database, containerComment.UUID, "", campaign.UUID, "digest", map[string]any{"event_log_id": float64(77)})
	assertCommentCreatedStamps(t, database, containerComment.UUID, campaign.UUID, campaign.UUID)

	listed, err := api.CommentList(context.Background(), decodeCommentListParams(t, map[string]any{"container": campaign.ID}))
	if err != nil {
		t.Fatalf("list container comments: %v", err)
	}
	if len(listed.Items) != 1 || listed.Items[0].UUID != containerComment.UUID {
		t.Fatalf("container comment list = %+v, want only %s", listed.Items, containerComment.UUID)
	}
	encoded, err := json.Marshal(listed.Items[0])
	if err != nil {
		t.Fatalf("marshal listed container comment: %v", err)
	}
	for _, want := range []string{`"kind":"digest"`, `"event_log_id":77`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("container comment DTO %s missing %s", encoded, want)
		}
	}

	for _, parent := range []struct {
		column string
		uuid   string
	}{
		{column: "tasks", uuid: task.UUID},
		{column: "containers", uuid: campaign.UUID},
	} {
		var principal string
		if err := database.QueryRow("SELECT updated_by_principal_ref FROM "+parent.column+" WHERE uuid = ?", parent.uuid).Scan(&principal); err != nil {
			t.Fatalf("load %s attribution: %v", parent.column, err)
		}
		if principal != "agent:comment-author" {
			t.Errorf("%s parent attribution = %q, want agent:comment-author", parent.column, principal)
		}
	}
	if _, ok := sink.get(campaign.ID); ok {
		t.Fatal("container comment emitted task-shaped comment_added webhook")
	}
}

func TestCommentAddRPCRejectsUnknownKindWithoutWriting(t *testing.T) {
	api, s := newMonitorAPI(t)
	projectUUID := seedMonitorProject(t, s)
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "invalid-kind", Title: "Invalid kind", ProjectUUID: projectUUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	_, err = api.CommentAdd(context.Background(), decodeCommentAddParams(t, map[string]any{
		"task": task.ID, "body": "mechanical", "kind": "heartbeat",
	}))
	if err == nil {
		t.Fatal("RPC accepted invalid comment kind heartbeat")
	}
	for _, want := range []string{"invalid comment kind", "blocker", "decision", "postmortem", "digest"} {
		if !strings.Contains(strings.ToLower(err.Error()), want) {
			t.Errorf("RPC invalid-kind error %q does not contain %q", err, want)
		}
	}
	var count int
	if err := api.db.QueryRow("SELECT COUNT(*) FROM comments WHERE body = 'mechanical'").Scan(&count); err != nil {
		t.Fatalf("count rejected comments: %v", err)
	}
	if count != 0 {
		t.Fatalf("invalid RPC kind wrote %d comments", count)
	}
}

func TestMechanicalTaskMutationDoesNotWriteComments(t *testing.T) {
	api, s := newMonitorAPI(t)
	projectUUID := seedMonitorProject(t, s)
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "mechanical-state", Title: "Mechanical state", ProjectUUID: projectUUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: task.ID, Patch: TaskPatch{State: strp("completed")}}); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	var count int
	if err := api.db.QueryRow("SELECT COUNT(*) FROM comments").Scan(&count); err != nil {
		t.Fatalf("count comments: %v", err)
	}
	if count != 0 {
		t.Fatalf("mechanical task mutation wrote %d comment rows", count)
	}
}

func decodeCommentAddParams(t *testing.T, input map[string]any) CommentAddParams {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal comment add params: %v", err)
	}
	var params CommentAddParams
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("decode comment add params: %v", err)
	}
	return params
}

func decodeCommentListParams(t *testing.T, input map[string]any) CommentListParams {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal comment list params: %v", err)
	}
	var params CommentListParams
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatalf("decode comment list params: %v", err)
	}
	return params
}

func assertStoredCommentParent(t *testing.T, database interface {
	QueryRow(string, ...any) *sql.Row
}, commentUUID, taskUUID, containerUUID, kind string, wantMeta map[string]any) {
	t.Helper()
	var gotTask, gotContainer, gotKind, gotMeta sql.NullString
	if err := database.QueryRow(
		"SELECT task_uuid, container_uuid, kind, meta FROM comments WHERE uuid = ?", commentUUID,
	).Scan(&gotTask, &gotContainer, &gotKind, &gotMeta); err != nil {
		t.Fatalf("load stored comment %s: %v", commentUUID, err)
	}
	if gotTask.String != taskUUID || gotTask.Valid != (taskUUID != "") {
		t.Errorf("task parent = %#v, want %q", gotTask, taskUUID)
	}
	if gotContainer.String != containerUUID || gotContainer.Valid != (containerUUID != "") {
		t.Errorf("container parent = %#v, want %q", gotContainer, containerUUID)
	}
	if !gotKind.Valid || gotKind.String != kind {
		t.Errorf("kind = %#v, want %q", gotKind, kind)
	}
	if wantMeta != nil {
		var gotMetaMap map[string]any
		if err := json.Unmarshal([]byte(gotMeta.String), &gotMetaMap); err != nil {
			t.Fatalf("decode stored meta %q: %v", gotMeta.String, err)
		}
		if gotMetaMap["event_log_id"] != wantMeta["event_log_id"] {
			t.Errorf("meta = %#v, want %#v", gotMetaMap, wantMeta)
		}
	}
}

func assertCommentCreatedStamps(t *testing.T, database interface {
	QueryRow(string, ...any) *sql.Row
}, commentUUID, campaignUUID, containerUUID string) {
	t.Helper()
	var payloadRaw string
	if err := database.QueryRow(
		"SELECT payload FROM event_log WHERE resource_uuid = ? AND event_type = 'comment.created'", commentUUID,
	).Scan(&payloadRaw); err != nil {
		t.Fatalf("load comment.created payload: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadRaw), &payload); err != nil {
		t.Fatalf("decode comment.created payload %q: %v", payloadRaw, err)
	}
	if payload["campaign_uuid"] != campaignUUID || payload["container_uuid"] != containerUUID {
		t.Errorf("comment.created stamps = campaign %#v container %#v, want %s/%s; payload=%s",
			payload["campaign_uuid"], payload["container_uuid"], campaignUUID, containerUUID, payloadRaw)
	}
}
