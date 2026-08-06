//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
)

type campaignStampFixture struct {
	api                       *API
	store                     *store.Store
	projectUUID, campaignUUID string
}

func newCampaignStampFixture(t *testing.T) campaignStampFixture {
	t.Helper()
	api, s := newMonitorAPI(t)
	project := seedMonitorProject(t, s)
	campaign, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{
		Slug: "stamp-campaign", Kind: "directory", ParentUUID: &project,
	})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	if _, err := api.db.Exec("UPDATE containers SET campaign_state = 'active' WHERE uuid = ?", campaign.UUID); err != nil {
		t.Fatalf("activate campaign: %v", err)
	}
	return campaignStampFixture{api: api, store: s, projectUUID: project, campaignUUID: campaign.UUID}
}

func (f campaignStampFixture) createTask(t *testing.T, slug, resident string, parent *string) *store.CreateResult {
	t.Helper()
	result, err := f.store.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: slug, Title: slug, ProjectUUID: resident, ParentTaskUUID: parent,
		State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task %s: %v", slug, err)
	}
	return result
}

func (f campaignStampFixture) enroll(t *testing.T, taskUUID string) {
	t.Helper()
	if _, err := f.api.db.Exec("UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", f.campaignUUID, taskUUID); err != nil {
		t.Fatalf("enroll task: %v", err)
	}
}

func assertCampaignStamp(t *testing.T, database *db.DB, taskUUID, eventType string, campaignUUID *string, containerUUID string) {
	t.Helper()
	var count int
	if err := database.QueryRow(
		"SELECT COUNT(*) FROM event_log WHERE resource_uuid = ? AND event_type = ?",
		taskUUID, eventType,
	).Scan(&count); err != nil {
		t.Fatalf("count %s for %s: %v", eventType, taskUUID, err)
	}
	if count != 1 {
		t.Fatalf("%s rows for %s = %d, want exactly 1", eventType, taskUUID, count)
	}
	var payloadRaw string
	if err := database.QueryRow(
		"SELECT payload FROM event_log WHERE resource_uuid = ? AND event_type = ?",
		taskUUID, eventType,
	).Scan(&payloadRaw); err != nil {
		t.Fatalf("load %s payload for %s: %v", eventType, taskUUID, err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(payloadRaw), &payload); err != nil {
		t.Fatalf("decode %s payload %q: %v", eventType, payloadRaw, err)
	}
	if got, ok := payload["container_uuid"].(string); !ok || got != containerUUID {
		t.Errorf("%s container_uuid = %#v, want %s; payload=%s", eventType, payload["container_uuid"], containerUUID, payloadRaw)
	}
	gotCampaign, exists := payload["campaign_uuid"]
	if campaignUUID == nil {
		if !exists || gotCampaign != nil {
			t.Errorf("%s campaign_uuid = %#v (exists=%v), want explicit null; payload=%s", eventType, gotCampaign, exists, payloadRaw)
		}
		return
	}
	if got, ok := gotCampaign.(string); !ok || got != *campaignUUID {
		t.Errorf("%s campaign_uuid = %#v, want %s; payload=%s", eventType, gotCampaign, *campaignUUID, payloadRaw)
	}
}

func stampTestAttribution() attribution.Attribution {
	actor := monitorSystemActor
	return attribution.Attribution{PrincipalRef: "agent:wrkq-system", LegacyActorUUID: &actor}
}

func TestTaskStateChangingProducersStampEffectiveCampaignAtWriteTime(t *testing.T) {
	t.Run("task updated after move", func(t *testing.T) {
		f := newCampaignStampFixture(t)
		task := f.createTask(t, "updated-after-move", f.campaignUUID, nil)
		if _, err := f.store.Tasks.Move(monitorSystemActor, task.UUID, f.projectUUID, 0); err != nil {
			t.Fatalf("move task out of campaign: %v", err)
		}
		if _, err := f.api.TaskUpdate(context.Background(), TaskUpdateParams{
			Task: task.ID, Patch: TaskPatch{State: strp("completed")},
		}); err != nil {
			t.Fatalf("state update: %v", err)
		}
		assertCampaignStamp(t, f.api.db, task.UUID, "task.updated", nil, f.projectUUID)
	})

	t.Run("task archived while enrolled", func(t *testing.T) {
		f := newCampaignStampFixture(t)
		task := f.createTask(t, "archived-enrolled", f.projectUUID, nil)
		f.enroll(t, task.UUID)
		if _, err := f.api.TaskDelete(context.Background(), TaskDeleteParams{Task: task.ID, Mode: "archive"}); err != nil {
			t.Fatalf("archive task: %v", err)
		}
		assertCampaignStamp(t, f.api.db, task.UUID, "task.archived", &f.campaignUUID, f.projectUUID)
	})

	t.Run("task deleted cascade", func(t *testing.T) {
		f := newCampaignStampFixture(t)
		parent := f.createTask(t, "deleted-parent", f.campaignUUID, nil)
		child := f.createTask(t, "deleted-child", f.campaignUUID, &parent.UUID)
		if _, err := f.api.TaskDelete(context.Background(), TaskDeleteParams{Task: parent.ID}); err != nil {
			t.Fatalf("delete parent: %v", err)
		}
		assertCampaignStamp(t, f.api.db, child.UUID, "task.deleted", &f.campaignUUID, f.campaignUUID)
	})

	t.Run("task restored after unenrollment", func(t *testing.T) {
		f := newCampaignStampFixture(t)
		task := f.createTask(t, "restored-unenrolled", f.projectUUID, nil)
		f.enroll(t, task.UUID)
		if _, err := f.api.TaskDelete(context.Background(), TaskDeleteParams{Task: task.ID, Mode: "archive"}); err != nil {
			t.Fatalf("archive task: %v", err)
		}
		if _, err := f.api.db.Exec("UPDATE tasks SET campaign_uuid = NULL WHERE uuid = ?", task.UUID); err != nil {
			t.Fatalf("unenroll archived task: %v", err)
		}
		if _, err := f.api.TaskRestore(context.Background(), TaskRestoreParams{Task: task.ID}); err != nil {
			t.Fatalf("restore task: %v", err)
		}
		assertCampaignStamp(t, f.api.db, task.UUID, "task.restored", nil, f.projectUUID)
	})

	t.Run("task purged direct", func(t *testing.T) {
		f := newCampaignStampFixture(t)
		task := f.createTask(t, "purged-direct", f.projectUUID, nil)
		f.enroll(t, task.UUID)
		if _, err := f.store.Tasks.Purge(monitorSystemActor, task.UUID, 0); err != nil {
			t.Fatalf("purge task: %v", err)
		}
		assertCampaignStamp(t, f.api.db, task.UUID, "task.purged", &f.campaignUUID, f.projectUUID)
	})

	t.Run("task purged resident subtask cascade", func(t *testing.T) {
		f := newCampaignStampFixture(t)
		parent := f.createTask(t, "purged-parent", f.campaignUUID, nil)
		child := f.createTask(t, "purged-child", f.campaignUUID, &parent.UUID)
		if _, err := f.store.Tasks.Purge(monitorSystemActor, parent.UUID, 0); err != nil {
			t.Fatalf("purge parent: %v", err)
		}
		assertCampaignStamp(t, f.api.db, child.UUID, "task.purged", &f.campaignUUID, f.campaignUUID)
	})

	t.Run("task purged container cascade", func(t *testing.T) {
		f := newCampaignStampFixture(t)
		task := f.createTask(t, "purged-with-container", f.campaignUUID, nil)
		impact, err := f.store.Containers.DeleteRecursiveImpact(f.campaignUUID)
		if err != nil {
			t.Fatalf("container delete impact: %v", err)
		}
		if _, err := f.store.Containers.DeleteRecursiveWithAttribution(
			stampTestAttribution(), f.campaignUUID, 0, *impact,
		); err != nil {
			t.Fatalf("delete campaign recursively: %v", err)
		}
		assertCampaignStamp(t, f.api.db, task.UUID, "task.purged", &f.campaignUUID, f.campaignUUID)
	})
}