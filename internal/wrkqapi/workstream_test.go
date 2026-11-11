//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"github.com/lherron/wrkq/internal/nodeauth"
	"testing"
)

func TestWorkstreamCoversContract(t *testing.T) {
	a := newAttributionAPI(t, "agent:seed")
	ctx := context.Background()
	target := claimTestTask(t, a, "target")
	var container string
	if err := a.db.QueryRow("SELECT id FROM containers WHERE uuid=?", target.ProjectUUID).Scan(&container); err != nil {
		t.Fatal(err)
	}
	covers := &TaskCovers{Container: container, Tasks: []string{target.ID}}
	task, err := a.TaskCreate(ctx, TaskCreateParams{Title: "stream", Kind: "workstream", Covers: covers})
	if err != nil {
		t.Fatal(err)
	}
	if task.Covers == nil || task.Covers.Container != container {
		t.Fatalf("covers: %+v", task)
	}
	shown, err := a.TaskShow(ctx, TaskShowParams{Task: task.ID})
	if err != nil || shown.Covers == nil {
		t.Fatalf("show: %+v %v", shown, err)
	}
	cat, err := a.TaskCatView(ctx, TaskCatViewParams{Task: task.ID})
	if err != nil || cat.Covers == nil {
		t.Fatalf("cat: %+v %v", cat, err)
	}
	var listParams TaskListParams
	if err := json.Unmarshal([]byte(`{"kind":"workstream"}`), &listParams); err != nil {
		t.Fatal(err)
	}
	list, err := a.TaskList(ctx, listParams)
	if err != nil || len(list.Items) != 1 || list.Items[0].ID != task.ID || list.Items[0].Covers == nil {
		t.Fatalf("list: %+v %v", list, err)
	}
	found, err := a.FindListView(ctx, FindListViewParams{Kind: "workstream"})
	if err != nil {
		t.Fatal(err)
	}
	taskHits := 0
	for _, item := range found.Items {
		if item.Type == "task" {
			taskHits++
			if item.ID != task.ID {
				t.Fatalf("kind filter leaked task %+v", item)
			}
		}
	}
	if taskHits != 1 {
		t.Fatalf("find: %+v %v", found, err)
	}
	for name, p := range map[string]TaskCreateParams{
		"wrong kind":        {Title: "wrong", Covers: covers},
		"missing container": {Title: "bad container", Kind: "workstream", Covers: &TaskCovers{Container: "P-999999"}},
		"missing task":      {Title: "bad task", Kind: "workstream", Covers: &TaskCovers{Tasks: []string{"T-999999"}}},
	} {
		_, err := a.TaskCreate(ctx, p)
		if err == nil {
			t.Fatalf("%s admitted", name)
		}
		want := CodeNotFound
		if name == "wrong kind" {
			want = "WRKQ_COVERS_REQUIRES_WORKSTREAM"
		}
		if claimErrorCode(t, err) != want {
			t.Fatalf("%s: %v", name, err)
		}
	}
	_, err = a.TaskUpdate(ctx, TaskUpdateParams{Task: task.ID, Patch: TaskPatch{Kind: strp("task")}})
	if err == nil || claimErrorCode(t, err) != "WRKQ_WORKSTREAM_KIND_FIXED" {
		t.Fatalf("kind change: %v", err)
	}
	ctx = nodeauth.WithNode(ctx, "test-node")
	_, err = a.TaskClaim(ctx, TaskClaimParams{Task: task.ID, PrincipalRef: "agent:seed", Scope: "agent:seed:project:wrkq:task:" + task.ID})
	if err == nil || claimErrorCode(t, err) != "WRKQ_WORKSTREAM_NOT_CLAIMABLE" {
		t.Fatalf("claim: %v", err)
	}
	var patch TaskPatch
	if err := json.Unmarshal([]byte(`{"covers":{"tasks":["`+target.ID+`"]}}`), &patch); err != nil {
		t.Fatal(err)
	}
	updated, err := a.TaskUpdate(ctx, TaskUpdateParams{Task: task.ID, ExpectEtag: &task.ETag, Patch: patch})
	if err != nil || updated.Covers == nil || updated.Covers.Container != "" {
		t.Fatalf("update: %+v %v", updated, err)
	}
	var payload string
	if err := a.db.QueryRow("SELECT payload FROM event_log WHERE resource_uuid=? AND event_type='task.updated' ORDER BY id DESC LIMIT 1", task.UUID).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatal(err)
	}
	if _, ok := event["covers"].(map[string]any); !ok {
		t.Fatalf("covers payload not object: %s", payload)
	}
	_, err = a.TaskUpdate(ctx, TaskUpdateParams{Task: task.ID, ExpectEtag: &task.ETag, Patch: TaskPatch{Covers: json.RawMessage(`null`)}})
	if err == nil || claimErrorCode(t, err) != CodeConflict {
		t.Fatalf("stale fence: %v", err)
	}
	cleared, err := a.TaskUpdate(ctx, TaskUpdateParams{Task: task.ID, Patch: TaskPatch{Covers: json.RawMessage(`null`)}})
	if err != nil || cleared.Covers != nil || cleared.Kind != "workstream" {
		t.Fatalf("clear: %+v %v", cleared, err)
	}
}
