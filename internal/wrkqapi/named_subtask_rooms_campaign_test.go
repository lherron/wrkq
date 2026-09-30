//go:build wrkq_local

package wrkqapi

import (
	"context"
	"github.com/lherron/wrkq/internal/store"
	"testing"
)

// Insert directly so routing and campaign tests do not depend on creation RPC.
func seedRoomSubtask(t *testing.T, f *roomFixture, ownerUUID, slug string, updatedAt ...string) (string, string) {
	t.Helper()
	var ownerID string
	if err := f.api.db.QueryRow("SELECT id FROM tasks WHERE uuid = ?", ownerUUID).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	id := ownerID + "." + slug
	uuid := ownerUUID + "-" + slug
	timestamp := "2026-09-30T00:00:00Z"
	if len(updatedAt) > 0 {
		timestamp = updatedAt[0]
	}
	_, err := f.api.db.Exec(`INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, subtask_owner_uuid, updated_at)
 SELECT ?, ?, ?, ?, project_uuid, 'open', uuid, ? FROM tasks WHERE uuid = ?`, uuid, id, slug, slug, timestamp, ownerUUID)
	if err != nil {
		t.Fatal(err)
	}
	return id, uuid
}

func TestNamedSubtaskOwnerRoomRoutingAndHistory(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	firstID, firstUUID := seedRoomSubtask(t, f, f.loneTaskUUID, "render")
	secondID, _ := seedRoomSubtask(t, f, f.loneTaskUUID, "review")
	first := f.say(t, RoomSayParams{Ref: firstID, Body: "render before enrollment", PrincipalRef: "agent:clod"})
	second := f.say(t, RoomSayParams{Ref: secondID, Body: "review before enrollment", PrincipalRef: "agent:clod"})
	if first.Room.ID != second.Room.ID {
		t.Fatal("siblings did not share owner room")
	}
	if first.Envelopes[0].TaskID == nil || *first.Envelopes[0].TaskID != firstID {
		t.Fatal("subtask subject lost")
	}
	if room, err := f.s.Rooms.GetByTask(firstUUID); err != nil || room != nil {
		t.Fatalf("subtask acquired own room: %v %v", room, err)
	}
	if _, err := f.api.db.Exec("UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", f.campaignUUID, f.loneTaskUUID); err != nil {
		t.Fatal(err)
	}
	current := f.say(t, RoomSayParams{Ref: firstID, Body: "after enrollment", PrincipalRef: "agent:clod"})
	if current.Room.Kind != "campaign" {
		t.Fatalf("current route: %s", current.Room.Kind)
	}
	history, err := f.api.RoomLogView(ctx, RoomLogViewParams{Room: firstID})
	if err != nil {
		t.Fatal(err)
	}
	if history.Room.ID != first.Room.ID || len(history.Items) != 2 {
		t.Fatalf("owner old room not preserved: %+v", history)
	}
	for _, owner := range []string{f.memberTaskUUID, f.residentTaskUUID} {
		id, _ := seedRoomSubtask(t, f, owner, "render")
		result := f.say(t, RoomSayParams{Ref: id, Body: "campaign subtask", PrincipalRef: "agent:clod"})
		if result.Room.ID != current.Room.ID {
			t.Fatal("campaign context route differs from owner")
		}
	}
}

func TestNamedSubtaskCampaignCountsActivityContextAndAdmission(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	for _, owner := range []string{f.memberTaskUUID, f.residentTaskUUID} {
		_, uuid := seedRoomSubtask(t, f, owner, "render", "2099-01-01T00:00:00Z")
		tx, err := f.api.db.BeginTx()
		if err != nil {
			t.Fatal(err)
		}
		payload := map[string]any{}
		if err := store.StampTaskCampaignContext(tx, uuid, payload); err != nil {
			t.Fatal(err)
		}
		if payload["campaign_uuid"] != f.campaignUUID {
			t.Fatalf("subtask stamp: %#v", payload)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	view, err := f.api.ContainerTimelineView(ctx, ContainerTimelineViewParams{Container: f.campaignUUID})
	if err != nil {
		t.Fatal(err)
	}
	if view.Rollup.Total != 2 || len(view.Members) != 2 || view.LastActivityAt != "2099-01-01T00:00:00Z" {
		t.Fatalf("timeline counts/activity: %+v", view)
	}
	tx, err := f.api.db.BeginTx()
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := loadCampaignAggregateTx(ctx, tx, f.campaignUUID, "")
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.TotalMembers != 2 || aggregate.LastActivityAt != "2099-01-01T00:00:00Z" {
		t.Fatalf("portfolio counts/activity: %+v", aggregate)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.db.Exec("UPDATE containers SET campaign_state = 'completed' WHERE uuid = ?", f.campaignUUID); err != nil {
		t.Fatal(err)
	}
	_, uuid := seedRoomSubtask(t, f, f.residentTaskUUID, "late")
	tx, err = f.api.db.BeginTx()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ValidateTaskResidentAdmissionTx(tx, uuid); err != nil {
		t.Fatalf("subtask treated as new member: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Tasks.UpdateFields(monitorSystemActor, uuid, map[string]any{"campaign_uuid": f.campaignUUID}, 0); err == nil {
		t.Fatal("subtask campaign enrollment accepted")
	}
}

func TestNamedSubtaskSiblingScopesKeepIndependentObligations(t *testing.T) {
	f := newRoomFixture(t)
	firstID, _ := seedRoomSubtask(t, f, f.loneTaskUUID, "render")
	secondID, _ := seedRoomSubtask(t, f, f.loneTaskUUID, "review")
	sender := "clod@proj:primary"
	firstScope := "cody@proj:" + firstID
	secondScope := "cody@proj:" + secondID
	first := f.say(t, RoomSayParams{Ref: firstID, Body: "render", To: []string{firstScope}, PrincipalRef: "agent:clod", ScopeRef: sender})
	second := f.say(t, RoomSayParams{Ref: secondID, Body: "review", To: []string{secondScope}, PrincipalRef: "agent:clod", ScopeRef: sender})
	reply := f.say(t, RoomSayParams{Ref: first.Envelopes[0].ID, Body: "render answer", To: []string{sender}, PrincipalRef: "agent:cody", ScopeRef: firstScope, FYI: true})
	if len(reply.Acked) != 1 || reply.Acked[0] != first.Envelopes[0].ID {
		t.Fatalf("scope reply acknowledgments: %v", reply.Acked)
	}
	var secondState string
	if err := f.api.db.QueryRow("SELECT state FROM envelopes WHERE id = ?", second.Envelopes[0].ID).Scan(&secondState); err != nil {
		t.Fatal(err)
	}
	if secondState != "pending" {
		t.Fatalf("sibling obligation changed: %s", secondState)
	}
	if reply.Envelopes[0].TaskID == nil || *reply.Envelopes[0].TaskID != firstID {
		t.Fatal("envelope reply lost exact subject")
	}
	if _, err := f.api.db.Exec("UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", f.campaignUUID, f.loneTaskUUID); err != nil {
		t.Fatal(err)
	}
	oldReply := f.say(t, RoomSayParams{Ref: second.Envelopes[0].ID, Body: "old-room answer", To: []string{sender}, PrincipalRef: "agent:cody", ScopeRef: secondScope, FYI: true})
	if oldReply.Room.ID != second.Room.ID || oldReply.Envelopes[0].TaskID == nil || *oldReply.Envelopes[0].TaskID != secondID {
		t.Fatal("old-envelope reply moved room or subject")
	}
}

func TestNamedSubtaskDetailRoomLocatorDoesNotMaterializeRoom(t *testing.T) {
	f := newRoomFixture(t)
	sub, _ := seedRoomSubtask(t, f, f.loneTaskUUID, "render")
	task, err := f.api.TaskShow(context.Background(), TaskShowParams{Task: sub})
	if err != nil {
		t.Fatal(err)
	}
	if task.RoomLocator != f.loneTaskID {
		t.Fatalf("locator %q", task.RoomLocator)
	}
	cat, err := f.api.TaskCatView(context.Background(), TaskCatViewParams{Task: sub})
	if err != nil {
		t.Fatal(err)
	}
	if cat.RoomLocator != f.loneTaskID {
		t.Fatalf("cat locator %q", cat.RoomLocator)
	}
	var count int
	if err := f.api.db.QueryRow("SELECT COUNT(*) FROM rooms").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("detail read materialized room")
	}
}

func TestNamedSubtaskEventSelectorCoversOwnerAndSubtasks(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	first, _ := seedRoomSubtask(t, f, f.loneTaskUUID, "render")
	second, _ := seedRoomSubtask(t, f, f.loneTaskUUID, "review")
	for _, id := range []string{f.loneTaskID, first, second} {
		f.say(t, RoomSayParams{Ref: id, Body: id, PrincipalRef: "agent:clod"})
		if _, err := f.api.TaskUpdate(ctx, TaskUpdateParams{Task: id, Patch: TaskPatch{State: strp("in_progress")}}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.api.ProjectEventPost(ctx, ProjectEventPostParams{Task: id, Type: "acceptance.subtask", Summary: id, Attributes: []byte(`{"evidence":"yes"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	ownerLog, err := f.api.RoomLogView(ctx, RoomLogViewParams{Room: f.loneTaskID, Task: f.loneTaskID})
	if err != nil {
		t.Fatal(err)
	}
	if len(ownerLog.Items) != 3 {
		t.Fatalf("owner event selector dropped subtasks: %+v", ownerLog.Items)
	}
	exactLog, err := f.api.RoomLogView(ctx, RoomLogViewParams{Room: first, Task: first})
	if err != nil {
		t.Fatal(err)
	}
	if len(exactLog.Items) != 1 || exactLog.Items[0].TaskID == nil || *exactLog.Items[0].TaskID != first {
		t.Fatalf("composite event selector expanded: %+v", exactLog.Items)
	}
	for _, selector := range []string{f.loneTaskID, first} {
		view, err := f.api.ContainerTimelineView(ctx, ContainerTimelineViewParams{Container: f.proj, Task: selector, Scope: "subtree", Types: []string{"task.state", "acceptance.*"}, EntriesOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		events := map[string]bool{}
		for _, entry := range view.Entries {
			if entry.Type == "task.state" {
				ids[entry.TaskID] = true
			}
			if entry.ProjectEvent != nil {
				events[entry.TaskID] = true
			}
		}
		want := 3
		if selector == first {
			want = 1
		}
		if len(ids) != want || len(events) != want {
			t.Fatalf("selector %s state=%v project=%v", selector, ids, events)
		}
	}
}
