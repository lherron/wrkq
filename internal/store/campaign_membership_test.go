package store

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

type campaignMembershipFixture struct {
	db                   *db.DB
	store                *Store
	actorUUID            string
	projectA, projectB   string
	campaignA, campaignB string
	nonCampaignContainer string
}

func newCampaignMembershipFixture(t *testing.T) campaignMembershipFixture {
	t.Helper()
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := New(database)
	projectA := createCrossProjectTestContainer(t, s, actorUUID, "campaign-project-a")
	projectB := createCrossProjectTestContainer(t, s, actorUUID, "campaign-project-b")
	createDirectory := func(slug, parent string) string {
		t.Helper()
		result, err := s.Containers.Create(actorUUID, ContainerCreateParams{
			Slug: slug, Kind: "directory", ParentUUID: &parent,
		})
		if err != nil {
			t.Fatalf("create directory %s: %v", slug, err)
		}
		return result.UUID
	}
	campaignA := createDirectory("campaign-a", projectA)
	campaignB := createDirectory("campaign-b", projectB)
	nonCampaign := createDirectory("plain-bucket", projectA)
	if _, err := database.Exec(
		"UPDATE containers SET campaign_state = 'active' WHERE uuid IN (?, ?)",
		campaignA, campaignB,
	); err != nil {
		t.Fatalf("activate campaigns: %v", err)
	}
	return campaignMembershipFixture{
		db: database, store: s, actorUUID: actorUUID,
		projectA: projectA, projectB: projectB,
		campaignA: campaignA, campaignB: campaignB,
		nonCampaignContainer: nonCampaign,
	}
}

func (f campaignMembershipFixture) createTask(t *testing.T, slug, resident string, parent *string) string {
	t.Helper()
	result, err := f.store.Tasks.Create(f.actorUUID, CreateParams{
		Slug: slug, Title: slug, ProjectUUID: resident, ParentTaskUUID: parent,
		State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task %s: %v", slug, err)
	}
	return result.UUID
}

func requireCampaignRejection(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("mutation succeeded; want campaign-membership rejection containing %q", want)
	}
	message := strings.ToLower(err.Error())
	if !strings.Contains(message, "campaign") || !strings.Contains(message, strings.ToLower(want)) {
		t.Fatalf("error = %q; want campaign-membership rejection containing %q", err, want)
	}
}

func TestEffectiveCampaignMembershipValidatorMutationMatrix(t *testing.T) {
	t.Run("enrollment requires draft or active campaign target", func(t *testing.T) {
		f := newCampaignMembershipFixture(t)
		taskUUID := f.createTask(t, "inactive-target", f.projectB, nil)
		_, err := f.store.Tasks.UpdateFields(f.actorUUID, taskUUID, map[string]any{
			"campaign_uuid": f.nonCampaignContainer,
		}, 0)
		requireCampaignRejection(t, err, "draft or active")
	})

	t.Run("resident task rejects foreign enrollment", func(t *testing.T) {
		f := newCampaignMembershipFixture(t)
		taskUUID := f.createTask(t, "foreign-enrollment", f.campaignA, nil)
		_, err := f.store.Tasks.UpdateFields(f.actorUUID, taskUUID, map[string]any{
			"campaign_uuid": f.campaignB,
		}, 0)
		requireCampaignRejection(t, err, "resident")
	})

	t.Run("move to different campaign rejects atomically", func(t *testing.T) {
		f := newCampaignMembershipFixture(t)
		taskUUID := f.createTask(t, "different-campaign", f.projectB, nil)
		if _, err := f.db.Exec("UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", f.campaignA, taskUUID); err != nil {
			t.Fatalf("seed enrollment: %v", err)
		}
		_, err := f.store.Tasks.Move(f.actorUUID, taskUUID, f.campaignB, 0)
		requireCampaignRejection(t, err, "unenroll")
		var resident string
		var enrolled sql.NullString
		if err := f.db.QueryRow("SELECT project_uuid, campaign_uuid FROM tasks WHERE uuid = ?", taskUUID).Scan(&resident, &enrolled); err != nil {
			t.Fatalf("read rejected move: %v", err)
		}
		if resident != f.projectB || !enrolled.Valid || enrolled.String != f.campaignA {
			t.Fatalf("rejected move mutated task: resident=%s enrolled=%v", resident, enrolled)
		}
	})

	t.Run("move to same campaign clears redundant enrollment", func(t *testing.T) {
		f := newCampaignMembershipFixture(t)
		taskUUID := f.createTask(t, "same-campaign", f.projectB, nil)
		if _, err := f.db.Exec("UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", f.campaignA, taskUUID); err != nil {
			t.Fatalf("seed enrollment: %v", err)
		}
		if _, err := f.store.Tasks.Move(f.actorUUID, taskUUID, f.campaignA, 0); err != nil {
			t.Fatalf("move into enrollment target: %v", err)
		}
		var resident string
		var enrolled sql.NullString
		if err := f.db.QueryRow("SELECT project_uuid, campaign_uuid FROM tasks WHERE uuid = ?", taskUUID).Scan(&resident, &enrolled); err != nil {
			t.Fatalf("read same-campaign move: %v", err)
		}
		if resident != f.campaignA || enrolled.Valid {
			t.Fatalf("same-campaign move = resident %s enrolled %v; want resident campaign and NULL enrollment", resident, enrolled)
		}
	})

	t.Run("subtree validator sees full moved set", func(t *testing.T) {
		f := newCampaignMembershipFixture(t)
		parent := f.createTask(t, "move-parent", f.projectB, nil)
		child := f.createTask(t, "move-child", f.projectB, &parent)
		if _, err := f.db.Exec("UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", f.campaignA, child); err != nil {
			t.Fatalf("seed child enrollment: %v", err)
		}
		_, err := f.store.Tasks.Move(f.actorUUID, parent, f.campaignB, 0)
		requireCampaignRejection(t, err, "unenroll")
		for _, taskUUID := range []string{parent, child} {
			var resident string
			if err := f.db.QueryRow("SELECT project_uuid FROM tasks WHERE uuid = ?", taskUUID).Scan(&resident); err != nil {
				t.Fatalf("read subtree member %s: %v", taskUUID, err)
			}
			if resident != f.projectB {
				t.Fatalf("rejected subtree move changed %s resident to %s", taskUUID, resident)
			}
		}
	})

	t.Run("generic project uuid update cannot bypass validator", func(t *testing.T) {
		f := newCampaignMembershipFixture(t)
		taskUUID := f.createTask(t, "generic-bypass", f.projectB, nil)
		if _, err := f.db.Exec("UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", f.campaignA, taskUUID); err != nil {
			t.Fatalf("seed enrollment: %v", err)
		}
		_, err := f.store.Tasks.UpdateFieldsWithViaAttribution(
			testAttribution(f.actorUUID), taskUUID,
			map[string]any{"project_uuid": f.campaignB}, 0, "rpc",
		)
		requireCampaignRejection(t, err, "unenroll")
	})

	t.Run("campaign container cannot move under campaign", func(t *testing.T) {
		f := newCampaignMembershipFixture(t)
		_, err := f.store.Containers.Move(f.actorUUID, f.campaignA, &f.campaignB, 0)
		requireCampaignRejection(t, err, "nested")
		var parent string
		if err := f.db.QueryRow("SELECT parent_uuid FROM containers WHERE uuid = ?", f.campaignA).Scan(&parent); err != nil {
			t.Fatalf("read rejected container move: %v", err)
		}
		if parent != f.projectA {
			t.Fatalf("rejected nested campaign move changed parent to %s", parent)
		}
	})
}
