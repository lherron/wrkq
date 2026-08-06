//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/lherron/wrkq/internal/store"
)

func TestTaskOutcomeEventsSnapshotProductionCampaignContext(t *testing.T) {
	f := newCampaignStampFixture(t)
	task := f.createTask(t, "outcome-history", f.projectUUID, nil)
	f.enroll(t, task.UUID)

	values := []string{"Initial result\nwith full detail\n", "Amended result", " \n\t"}
	for i := range values {
		updated, err := f.api.TaskUpdate(context.Background(), TaskUpdateParams{
			Task: task.ID, Patch: TaskPatch{Outcome: &values[i]}, Actor: "agent:outcome-author",
		})
		if err != nil {
			t.Fatalf("outcome update %d: %v", i+1, err)
		}
		if i < 2 && (updated.Outcome == nil || *updated.Outcome != values[i]) {
			t.Fatalf("outcome update %d DTO = %#v, want %q", i+1, updated.Outcome, values[i])
		}
		if i == 2 && updated.Outcome != nil {
			t.Fatalf("clear DTO outcome = %#v, want nil", updated.Outcome)
		}

		switch i {
		case 0:
			if _, err := f.api.db.Exec("UPDATE tasks SET campaign_uuid = NULL WHERE uuid = ?", task.UUID); err != nil {
				t.Fatalf("unenroll between outcome writes: %v", err)
			}
		case 1:
			if _, err := f.api.db.Exec("UPDATE tasks SET project_uuid = ? WHERE uuid = ?", f.campaignUUID, task.UUID); err != nil {
				t.Fatalf("move into campaign between outcome writes: %v", err)
			}
		}
	}

	rows, err := f.api.db.Query(`
		SELECT principal_ref, payload
		  FROM event_log
		 WHERE resource_uuid = ? AND event_type = 'task.outcome_set'
		 ORDER BY id`, task.UUID)
	if err != nil {
		t.Fatalf("query task.outcome_set history: %v", err)
	}
	defer func() { _ = rows.Close() }()

	type expectedEvent struct {
		outcome       any
		containerUUID string
		campaignUUID  any
	}
	expected := []expectedEvent{
		{outcome: values[0], containerUUID: f.projectUUID, campaignUUID: f.campaignUUID},
		{outcome: values[1], containerUUID: f.projectUUID, campaignUUID: nil},
		{outcome: nil, containerUUID: f.campaignUUID, campaignUUID: f.campaignUUID},
	}
	var gotCount int
	for rows.Next() {
		if gotCount >= len(expected) {
			t.Fatal("more than three task.outcome_set events")
		}
		var principal, raw string
		if err := rows.Scan(&principal, &raw); err != nil {
			t.Fatalf("scan outcome event: %v", err)
		}
		if principal != "agent:outcome-author" {
			t.Errorf("event %d principal = %q", gotCount+1, principal)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("decode event %d payload %q: %v", gotCount+1, raw, err)
		}
		want := expected[gotCount]
		if payload["task_uuid"] != task.UUID ||
			payload["outcome"] != want.outcome ||
			payload["container_uuid"] != want.containerUUID ||
			payload["campaign_uuid"] != want.campaignUUID {
			t.Errorf("event %d payload = %#v, want outcome=%#v task=%s container=%s campaign=%#v",
				gotCount+1, payload, want.outcome, task.UUID, want.containerUUID, want.campaignUUID)
		}
		gotCount++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate outcome history: %v", err)
	}
	if gotCount != len(expected) {
		t.Fatalf("task.outcome_set event count = %d, want %d", gotCount, len(expected))
	}

	var current sql.NullString
	if err := f.api.db.QueryRow("SELECT outcome FROM tasks WHERE uuid = ?", task.UUID).Scan(&current); err != nil {
		t.Fatalf("load current outcome: %v", err)
	}
	if current.Valid {
		t.Fatalf("current outcome = %q, want NULL after clear", current.String)
	}
}

func TestTaskCompletionDoesNotRequireOutcome(t *testing.T) {
	api, s := newMonitorAPI(t)
	projectUUID := seedMonitorProject(t, s)
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "completion-without-outcome", Title: "Completion without outcome",
		ProjectUUID: projectUUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	updated, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: task.ID, Patch: TaskPatch{State: strp("completed")},
	})
	if err != nil {
		t.Fatalf("complete without outcome: %v", err)
	}
	if updated.State != "completed" || updated.Outcome != nil {
		t.Fatalf("completed task = state %q outcome %#v, want completed/nil", updated.State, updated.Outcome)
	}
}