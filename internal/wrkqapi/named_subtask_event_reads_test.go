//go:build wrkq_local

package wrkqapi

import (
	"context"
	"github.com/lherron/wrkq/internal/store"
	"testing"
)

func TestNamedSubtaskEventFamilyAndExactState(t *testing.T) {
	a, s := newMonitorAPI(t)
	p := seedMonitorProject(t, s)
	ctx := context.Background()
	owner, e := s.Tasks.Create(monitorSystemActor, store.CreateParams{Slug: "owner", Title: "Owner", ProjectUUID: p, State: "open", Priority: 3})
	if e != nil {
		t.Fatal(e)
	}
	sub, e := s.Tasks.Create(monitorSystemActor, store.CreateParams{Slug: "diagram", Title: "Diagram", SubtaskOwnerUUID: &owner.UUID, State: "open", Priority: 3})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.TaskUpdate(ctx, TaskUpdateParams{Task: sub.ID, Patch: TaskPatch{State: strp("completed")}}); e != nil {
		t.Fatal(e)
	}
	if _, e = a.CommentAdd(ctx, CommentAddParams{Task: sub.ID, Body: "subtask comment"}); e != nil {
		t.Fatal(e)
	}
	if _, e = a.RoomSay(ctx, RoomSayParams{Ref: sub.ID, Body: "subtask message", PrincipalRef: "agent:wrkq-system"}); e != nil {
		t.Fatal(e)
	}
	family, e := a.MonitorEventsView(ctx, MonitorEventsViewParams{Tasks: []string{owner.ID}})
	if e != nil {
		t.Fatal(e)
	}
	childFound := false
	commentFound := false
	envelopeFound := false
	for _, row := range family.Items {
		if row.ResourceType == "envelope" {
			envelopeFound = true
			if row.TaskID != sub.ID {
				t.Fatal("monitor envelope lost composite task ID")
			}
		}
		if row.ResourceType == "comment" {
			commentFound = true
			if row.TaskID != sub.ID {
				t.Fatalf("comment task ID=%s", row.TaskID)
			}
		}
		if row.ResourceUUID != nil && *row.ResourceUUID == sub.UUID {
			childFound = true
			if row.ResourceID == nil || *row.ResourceID != sub.ID {
				t.Fatal("lost composite event ID")
			}
		}
	}
	if !envelopeFound {
		t.Fatal("owner monitor missed subtask envelope")
	}
	if !commentFound {
		t.Fatal("owner monitor missed subtask comment")
	}
	if !childFound {
		t.Fatal("owner event selector missed subtask")
	}
	exact, e := a.MonitorEventsView(ctx, MonitorEventsViewParams{Tasks: []string{sub.ID}})
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range exact.Items {
		if row.ResourceType == "task" && row.ResourceUUID != nil && *row.ResourceUUID != sub.UUID {
			t.Fatal("subtask selector widened")
		}
	}
	state, e := a.MonitorStateView(ctx, MonitorStateViewParams{Tasks: []string{owner.ID}, Condition: "state=completed"})
	if e != nil {
		t.Fatal(e)
	}
	if state.Met {
		t.Fatal("subtask completion satisfied owner state")
	}
	only, e := a.MonitorEventsView(ctx, MonitorEventsViewParams{Tasks: []string{owner.ID}, StateOnly: true})
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range only.Items {
		if row.ResourceUUID != nil && *row.ResourceUUID == sub.UUID {
			t.Fatal("stateOnly widened")
		}
	}
	log, e := a.HistoryListView(ctx, HistoryListViewParams{Target: owner.ID})
	if e != nil {
		t.Fatal(e)
	}
	childFound = false
	commentFound = false
	envelopeFound = false
	for _, row := range log.Items {
		if row.ResourceType == "envelope" {
			envelopeFound = true
			if row.TaskID != sub.ID {
				t.Fatal("history envelope lost composite task ID")
			}
		}
		if row.ResourceType == "comment" {
			commentFound = true
			if row.TaskID != sub.ID {
				t.Fatal("history lost subtask ID")
			}
		}
		if row.ResourceUUID == sub.UUID {
			childFound = true
		}
	}
	if !childFound {
		t.Fatal("owner history missed child")
	}
	if !commentFound {
		t.Fatal("owner history missed subtask comment")
	}
	if !envelopeFound {
		t.Fatal("owner history missed subtask envelope")
	}
	tail, e := a.HistoryTailView(ctx, HistoryTailViewParams{Tasks: []string{sub.ID}})
	if e != nil {
		t.Fatal(e)
	}
	for _, row := range tail.Items {
		if row.TaskID != sub.ID {
			t.Fatal("tail selector widened")
		}
	}
	if len(tail.Items) == 0 {
		t.Fatal("tail missed subtask")
	}
	if _, e = a.HistoryListView(ctx, HistoryListViewParams{Target: sub.ID}); e != nil {
		t.Fatal("subtask history selector refused", e)
	}
}
