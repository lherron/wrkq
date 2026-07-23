package wrkqapi

import (
	"context"
	"testing"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/store"
)

func createTimelineContainer(
	t *testing.T,
	s *store.Store,
	parentUUID string,
	slug string,
) *store.ContainerCreateResult {
	t.Helper()
	container, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{
		Slug: slug, Kind: "directory", ParentUUID: &parentUUID,
	})
	if err != nil {
		t.Fatalf("create timeline container %s: %v", slug, err)
	}
	return container
}

func createTimelineTask(
	t *testing.T,
	s *store.Store,
	containerUUID, slug, state, labels string,
) *store.CreateResult {
	t.Helper()
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: slug, Title: slug, ProjectUUID: containerUUID,
		State: domain.State(state), Priority: 2, Labels: labels,
	})
	if err != nil {
		t.Fatalf("create timeline task %s: %v", slug, err)
	}
	return task
}

func convertTimelineCampaign(
	t *testing.T,
	api *API,
	containerUUID, description, specification string,
) {
	t.Helper()
	if _, err := api.ContainerCampaignConvert(context.Background(), ContainerCampaignConvertParams{
		Container: containerUUID, Description: &description, Specification: &specification,
	}); err != nil {
		t.Fatalf("convert timeline campaign: %v", err)
	}
}

func TestContainerTimelineViewBaseMembersRollupAndDecisions(t *testing.T) {
	api, s := newMonitorAPI(t)
	projectUUID := seedMonitorProject(t, s)

	plain := createTimelineContainer(t, s, projectUUID, "plain")
	if _, err := s.Containers.UpdateFields(
		monitorSystemActor, plain.UUID,
		map[string]any{"description": "plain brief", "specification": "plain spec"}, 0,
	); err != nil {
		t.Fatalf("seed plain content: %v", err)
	}
	createTimelineTask(t, s, plain.UUID, "plain-decision", "open", `["awaiting-lance"]`)

	plainView, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: "/PROJ/PLAIN/",
	})
	if err != nil {
		t.Fatalf("plain timeline: %v", err)
	}
	if plainView.Campaign != nil {
		t.Fatalf("plain campaign adornment = %#v, want null", plainView.Campaign)
	}
	if plainView.Container.Description != "plain brief" ||
		plainView.Container.Specification == nil ||
		*plainView.Container.Specification != "plain spec" {
		t.Fatalf("plain base content = %#v", plainView.Container)
	}
	if len(plainView.Members) != 1 || plainView.Members[0].Membership != "resident" ||
		len(plainView.DecisionTasks) != 1 {
		t.Fatalf("plain members/decisions = %#v/%#v", plainView.Members, plainView.DecisionTasks)
	}

	campaign := createTimelineContainer(t, s, projectUUID, "campaign")
	convertTimelineCampaign(t, api, campaign.UUID, "campaign brief", "campaign spec")
	resident := createTimelineTask(t, s, campaign.UUID, "resident", "completed", "")

	external, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{
		Slug: "external", Kind: "project",
	})
	if err != nil {
		t.Fatalf("create external project: %v", err)
	}
	enrolled := createTimelineTask(t, s, external.UUID, "enrolled-decision", "open", `["awaiting-lance"]`)
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: enrolled.ID, Patch: TaskPatch{Campaign: &campaign.UUID},
	}); err != nil {
		t.Fatalf("enroll external member: %v", err)
	}

	campaignView, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: campaign.UUID,
	})
	if err != nil {
		t.Fatalf("campaign timeline: %v", err)
	}
	if campaignView.Campaign == nil || campaignView.Campaign.State != "active" ||
		campaignView.Campaign.Archived {
		t.Fatalf("campaign adornment = %#v", campaignView.Campaign)
	}
	if campaignView.Container.Specification == nil ||
		*campaignView.Container.Specification != "campaign spec" {
		t.Fatalf("campaign base specification = %#v", campaignView.Container.Specification)
	}
	if campaignView.Rollup.Terminal != 1 || campaignView.Rollup.Total != 2 {
		t.Fatalf("rollup = %#v, want 1/2", campaignView.Rollup)
	}
	membership := map[string]string{}
	for _, member := range campaignView.Members {
		membership[member.UUID] = member.Membership
	}
	if membership[resident.UUID] != "resident" || membership[enrolled.UUID] != "enrolled" {
		t.Fatalf("membership = %#v", membership)
	}
	if len(campaignView.MissingOutcomes) != 1 ||
		campaignView.MissingOutcomes[0].UUID != resident.UUID {
		t.Fatalf("missing outcomes = %#v", campaignView.MissingOutcomes)
	}
	if len(campaignView.DecisionTasks) != 1 ||
		campaignView.DecisionTasks[0].UUID != enrolled.UUID {
		t.Fatalf("decision tasks = %#v", campaignView.DecisionTasks)
	}
}

func TestContainerTimelineViewNormalizesProducerVocabularyAndRetainsStampedHistory(t *testing.T) {
	api, s := newMonitorAPI(t)
	projectUUID := seedMonitorProject(t, s)
	campaign := createTimelineContainer(t, s, projectUUID, "history-campaign")
	convertTimelineCampaign(t, api, campaign.UUID, "history brief", "history spec")

	updated := createTimelineTask(t, s, campaign.UUID, "updated", "open", "")
	title := "title-only"
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: updated.ID, Patch: TaskPatch{Title: &title},
	}); err != nil {
		t.Fatalf("title-only update: %v", err)
	}
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: updated.ID, Patch: TaskPatch{State: strp("completed")},
	}); err != nil {
		t.Fatalf("state update: %v", err)
	}
	firstOutcome, secondOutcome := "first outcome", "amended outcome"
	for _, outcome := range []*string{&firstOutcome, &secondOutcome} {
		if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
			Task: updated.ID, Patch: TaskPatch{Outcome: outcome},
		}); err != nil {
			t.Fatalf("outcome update: %v", err)
		}
	}

	restoreTask := createTimelineTask(t, s, campaign.UUID, "restore", "open", "")
	if _, err := api.TaskDelete(context.Background(), TaskDeleteParams{
		Task: restoreTask.ID, Mode: "archive",
	}); err != nil {
		t.Fatalf("archive restore task: %v", err)
	}
	if _, err := api.TaskRestore(context.Background(), TaskRestoreParams{
		Task: restoreTask.ID,
	}); err != nil {
		t.Fatalf("restore task: %v", err)
	}

	parent := createTimelineTask(t, s, campaign.UUID, "delete-parent", "open", "")
	child, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "delete-child", Title: "delete-child", ProjectUUID: campaign.UUID,
		ParentTaskUUID: &parent.UUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create delete child: %v", err)
	}
	if _, err := api.TaskDelete(context.Background(), TaskDeleteParams{Task: parent.ID}); err != nil {
		t.Fatalf("delete parent: %v", err)
	}

	external, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{
		Slug: "history-external", Kind: "project",
	})
	if err != nil {
		t.Fatalf("create history external project: %v", err)
	}
	enrolled := createTimelineTask(t, s, external.UUID, "unenrolled", "open", "")
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: enrolled.ID, Patch: TaskPatch{Campaign: &campaign.UUID},
	}); err != nil {
		t.Fatalf("enroll history task: %v", err)
	}
	if _, err := api.TaskDelete(context.Background(), TaskDeleteParams{
		Task: enrolled.ID, Mode: "archive",
	}); err != nil {
		t.Fatalf("archive enrolled task: %v", err)
	}
	emptyCampaign := ""
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: enrolled.ID, Patch: TaskPatch{Campaign: &emptyCampaign},
	}); err != nil {
		t.Fatalf("unenroll archived task: %v", err)
	}
	if _, err := api.TaskRestore(context.Background(), TaskRestoreParams{Task: enrolled.ID}); err != nil {
		t.Fatalf("restore after unenroll: %v", err)
	}

	purged := createTimelineTask(t, s, external.UUID, "purged-enrolled", "open", "")
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: purged.ID, Patch: TaskPatch{Campaign: &campaign.UUID},
	}); err != nil {
		t.Fatalf("enroll purge task: %v", err)
	}
	if _, err := api.TaskDelete(context.Background(), TaskDeleteParams{
		Task: purged.ID, Mode: "purge",
	}); err != nil {
		t.Fatalf("purge enrolled task: %v", err)
	}

	decisionKind := "decision"
	if _, err := api.CommentAdd(context.Background(), CommentAddParams{
		Container: campaign.UUID, Kind: &decisionKind, Body: "campaign judgment",
	}); err != nil {
		t.Fatalf("add campaign decision: %v", err)
	}

	// Same-second timestamps make event_log.id the only valid ordering identity.
	if _, err := api.db.Exec(`
		UPDATE event_log
		   SET timestamp = '2026-07-23T12:00:00Z'
		 WHERE id > 0
	`); err != nil {
		t.Fatalf("force same-second timeline writes: %v", err)
	}

	view, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: campaign.UUID,
	})
	if err != nil {
		t.Fatalf("campaign history timeline: %v", err)
	}
	for index := 1; index < len(view.Entries); index++ {
		if view.Entries[index-1].EventID >= view.Entries[index].EventID {
			t.Fatalf("entries not event-id ordered: %#v", view.Entries)
		}
		if view.Entries[index].Timestamp != "2026-07-23T12:00:00Z" {
			t.Fatalf("ordering proof requires same timestamp, got %s", view.Entries[index].Timestamp)
		}
	}

	sourceCounts := map[string]int{}
	outcomeCount := 0
	commentCount := 0
	archiveRetained := false
	restoreAfterUnenrollLeaked := false
	for _, entry := range view.Entries {
		switch entry.Type {
		case "task.state":
			sourceCounts[entry.TaskState.SourceEventType]++
			if entry.TaskUUID == enrolled.UUID && entry.TaskState.SourceEventType == "task.archived" {
				archiveRetained = entry.Membership == "enrolled" &&
					entry.CampaignUUID != nil && *entry.CampaignUUID == campaign.UUID &&
					entry.ContainerUUID == external.UUID
			}
			if entry.TaskUUID == enrolled.UUID && entry.TaskState.SourceEventType == "task.restored" {
				restoreAfterUnenrollLeaked = true
			}
		case "task.outcome":
			if entry.TaskUUID == updated.UUID {
				outcomeCount++
			}
		case "comment":
			commentCount++
		}
	}
	if sourceCounts["task.updated"] != 2 { // completed update + parent reversible delete
		t.Fatalf("task.updated state count = %d, want 2; sources=%#v", sourceCounts["task.updated"], sourceCounts)
	}
	for _, source := range []string{"task.archived", "task.deleted", "task.restored", "task.purged"} {
		if sourceCounts[source] == 0 {
			t.Fatalf("missing normalized %s entry: %#v", source, sourceCounts)
		}
	}
	if outcomeCount != 2 {
		t.Fatalf("outcome entries = %d, want 2", outcomeCount)
	}
	if commentCount != 1 {
		t.Fatalf("comment entries = %d, want 1", commentCount)
	}
	if !archiveRetained {
		t.Fatal("pre-unenroll archived event did not retain enrolled campaign/container stamps")
	}
	if restoreAfterUnenrollLeaked {
		t.Fatal("post-unenroll restore was affiliated by current membership instead of production stamp")
	}

	externalView, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: external.UUID,
	})
	if err != nil {
		t.Fatalf("external plain timeline: %v", err)
	}
	foundRestore := false
	for _, entry := range externalView.Entries {
		if entry.TaskUUID == enrolled.UUID && entry.Type == "task.state" &&
			entry.TaskState.SourceEventType == "task.restored" {
			foundRestore = entry.CampaignUUID == nil && entry.ContainerUUID == external.UUID
		}
	}
	if !foundRestore {
		t.Fatal("post-unenroll restore missing from resident plain-container timeline")
	}
	if child.UUID == "" {
		t.Fatal("delete child fixture missing")
	}
}

func TestContainerTimelineViewPagingFencesConcurrentAppends(t *testing.T) {
	api, s := newMonitorAPI(t)
	projectUUID := seedMonitorProject(t, s)
	campaign := createTimelineContainer(t, s, projectUUID, "paging-campaign")
	convertTimelineCampaign(t, api, campaign.UUID, "paging brief", "paging spec")
	task := createTimelineTask(t, s, campaign.UUID, "paging-task", "open", "")

	for _, state := range []string{"in_progress", "completed"} {
		if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
			Task: task.ID, Patch: TaskPatch{State: &state},
		}); err != nil {
			t.Fatalf("seed paging state %s: %v", state, err)
		}
	}
	page1, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: campaign.UUID, Limit: 2,
	})
	if err != nil {
		t.Fatalf("timeline page 1: %v", err)
	}
	if len(page1.Entries) != 2 || page1.NextCursor == "" {
		t.Fatalf("page 1 = %d entries cursor=%q, want 2 + cursor", len(page1.Entries), page1.NextCursor)
	}

	concurrentOutcome := "concurrent append"
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: task.ID, Patch: TaskPatch{Outcome: &concurrentOutcome},
	}); err != nil {
		t.Fatalf("append after page 1: %v", err)
	}
	var concurrentEventID int64
	if err := api.db.QueryRow(`
		SELECT MAX(id) FROM event_log
		 WHERE resource_uuid = ? AND event_type = 'task.outcome_set'
	`, task.UUID).Scan(&concurrentEventID); err != nil {
		t.Fatalf("read concurrent event id: %v", err)
	}
	if concurrentEventID <= page1.SnapshotEventID {
		t.Fatalf("concurrent event %d not after snapshot %d", concurrentEventID, page1.SnapshotEventID)
	}

	seen := map[int64]bool{}
	for _, entry := range page1.Entries {
		seen[entry.EventID] = true
	}
	cursor := page1.NextCursor
	for cursor != "" {
		page, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
			Container: campaign.UUID, Cursor: cursor, Limit: 2,
		})
		if err != nil {
			t.Fatalf("timeline continuation: %v", err)
		}
		if page.SnapshotEventID != page1.SnapshotEventID {
			t.Fatalf("snapshot fence changed %d -> %d", page1.SnapshotEventID, page.SnapshotEventID)
		}
		for _, entry := range page.Entries {
			if entry.EventID > page1.SnapshotEventID || entry.EventID == concurrentEventID {
				t.Fatalf("concurrent append leaked into fenced page: %#v", entry)
			}
			if seen[entry.EventID] {
				t.Fatalf("duplicate event across pages: %d", entry.EventID)
			}
			seen[entry.EventID] = true
		}
		cursor = page.NextCursor
	}

	fresh, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: campaign.UUID,
	})
	if err != nil {
		t.Fatalf("fresh timeline after append: %v", err)
	}
	foundConcurrent := false
	for _, entry := range fresh.Entries {
		if entry.EventID == concurrentEventID {
			foundConcurrent = true
		}
	}
	if !foundConcurrent {
		t.Fatal("fresh timeline did not include post-snapshot append")
	}
}
