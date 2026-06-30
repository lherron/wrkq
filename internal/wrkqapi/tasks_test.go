package wrkqapi

import (
	"context"
	"testing"

	"github.com/lherron/wrkq/internal/store"
)

func TestTaskUpdatePatchLabelsPersistsReturnShape(t *testing.T) {
	api, s := newMonitorAPI(t)
	projectUUID := seedMonitorProject(t, s)

	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug:        "rpc-labels",
		Title:       "RPC labels",
		ProjectUUID: projectUUID,
		State:       "open",
		Priority:    2,
		Labels:      `["ui"]`,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	labels := []string{"ui", "needs_smoketest"}
	updated, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task:  task.ID,
		Patch: TaskPatch{Labels: &labels},
	})
	if err != nil {
		t.Fatalf("TaskUpdate labels: %v", err)
	}

	if len(updated.Labels) != len(labels) {
		t.Fatalf("labels length = %d, want %d: %+v", len(updated.Labels), len(labels), updated.Labels)
	}
	for i, want := range labels {
		if updated.Labels[i] != want {
			t.Fatalf("labels[%d] = %q, want %q; labels=%+v", i, updated.Labels[i], want, updated.Labels)
		}
	}
}
