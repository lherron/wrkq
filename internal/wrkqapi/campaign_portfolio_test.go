//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/store"
)

func TestCampaignDraftLifecycleLabelsAndMembership(t *testing.T) {
	api, s := newMonitorAPI(t)
	projectUUID := seedMonitorProject(t, s)
	campaign := createTimelineContainer(t, s, projectUUID, "draft-campaign")
	resident := createTimelineTask(t, s, campaign.UUID, "resident-before-convert", "open", "")

	labels := []string{"domain:platform", " domain:platform ", "domain:platform"}
	converted, err := api.ContainerCampaignConvert(context.Background(), ContainerCampaignConvertParams{
		Container: campaign.UUID,
		State:     store.CampaignStateDraft,
		Labels:    &labels,
	})
	if err != nil {
		t.Fatalf("convert populated container to draft: %v", err)
	}
	if converted.CampaignState != store.CampaignStateDraft ||
		converted.PreviousState != nil ||
		converted.Container.CampaignState == nil ||
		*converted.Container.CampaignState != store.CampaignStateDraft {
		t.Fatalf("draft conversion = %#v", converted)
	}
	if len(converted.Container.Labels) != len(labels) {
		t.Fatalf("draft labels = %#v, want exact %#v", converted.Container.Labels, labels)
	}
	for index := range labels {
		if converted.Container.Labels[index] != labels[index] {
			t.Fatalf("draft labels[%d] = %q, want %q", index, converted.Container.Labels[index], labels[index])
		}
	}
	var eventPayload string
	if err := s.DB().QueryRow(`
		SELECT payload FROM event_log
		 WHERE resource_uuid = ? AND event_type = 'container.updated'
		 ORDER BY id DESC LIMIT 1
	`, campaign.UUID).Scan(&eventPayload); err != nil {
		t.Fatalf("read campaign label event: %v", err)
	}
	var eventSnapshot struct {
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(eventPayload), &eventSnapshot); err != nil {
		t.Fatalf("decode campaign label event %q: %v", eventPayload, err)
	}
	if len(eventSnapshot.Labels) != len(labels) ||
		eventSnapshot.Labels[1] != labels[1] {
		t.Fatalf("campaign label event = %#v, want exact %#v", eventSnapshot.Labels, labels)
	}

	external, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{
		Slug: "draft-external", Kind: "project",
	})
	if err != nil {
		t.Fatalf("create external project: %v", err)
	}
	enrolled := createTimelineTask(t, s, external.UUID, "draft-enrolled", "in_progress", "")
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: enrolled.ID, Patch: TaskPatch{Campaign: &campaign.UUID},
	}); err != nil {
		t.Fatalf("enroll task in draft: %v", err)
	}

	view, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: campaign.UUID,
	})
	if err != nil {
		t.Fatalf("draft detail: %v", err)
	}
	if view.Rollup.Total != 2 || len(view.Footprint) != 2 || view.LastActivityAt == "" {
		t.Fatalf("draft detail aggregate = rollup %#v footprint %#v activity %q", view.Rollup, view.Footprint, view.LastActivityAt)
	}
	gotMembership := map[string]string{}
	for _, member := range view.Members {
		gotMembership[member.UUID] = member.Membership
		if member.Project.UUID == "" || member.Project.ID == "" || member.Project.Slug == "" {
			t.Fatalf("member missing stable project identity: %#v", member)
		}
	}
	if gotMembership[resident.UUID] != "resident" || gotMembership[enrolled.UUID] != "enrolled" {
		t.Fatalf("draft membership = %#v", gotMembership)
	}

	_, err = api.ContainerCampaignActivate(context.Background(), ContainerCampaignActivateParams{
		Container: campaign.UUID, ExpectETag: converted.Container.ETag - 1,
	})
	var apiErr Error
	if !errors.As(err, &apiErr) || apiErr.Code() != CodeConflict {
		t.Fatalf("stale draft activation error = %v, want WRKQ_CONFLICT", err)
	}
	activated, err := api.ContainerCampaignActivate(context.Background(), ContainerCampaignActivateParams{
		Container: campaign.UUID, ExpectETag: converted.Container.ETag,
	})
	if err != nil {
		t.Fatalf("activate draft: %v", err)
	}
	if activated.PreviousState == nil || *activated.PreviousState != store.CampaignStateDraft ||
		activated.CampaignState != store.CampaignStateActive {
		t.Fatalf("activation = %#v", activated)
	}

	cleared := []string{}
	updated, err := api.ContainerCampaignUpdate(context.Background(), ContainerCampaignUpdateParams{
		Container: campaign.UUID, Labels: &cleared, ExpectETag: activated.Container.ETag,
	})
	if err != nil {
		t.Fatalf("clear campaign labels: %v", err)
	}
	if updated.Labels == nil || len(updated.Labels) != 0 {
		t.Fatalf("cleared labels = %#v, want []", updated.Labels)
	}
	if err := s.DB().QueryRow(`
		SELECT payload FROM event_log
		 WHERE resource_uuid = ? AND event_type = 'container.updated'
		 ORDER BY id DESC LIMIT 1
	`, campaign.UUID).Scan(&eventPayload); err != nil {
		t.Fatalf("read campaign label-clear event: %v", err)
	}
	eventSnapshot.Labels = nil
	if err := json.Unmarshal([]byte(eventPayload), &eventSnapshot); err != nil {
		t.Fatalf("decode campaign label-clear event %q: %v", eventPayload, err)
	}
	if eventSnapshot.Labels == nil || len(eventSnapshot.Labels) != 0 {
		t.Fatalf("campaign label-clear event = %#v, want []", eventSnapshot.Labels)
	}
}

func TestCampaignDraftCancellationAndTerminalAdmissionSeal(t *testing.T) {
	api, s := newMonitorAPI(t)
	projectUUID := seedMonitorProject(t, s)
	campaign := createTimelineContainer(t, s, projectUUID, "cancelled-draft")
	if _, err := api.ContainerCampaignConvert(context.Background(), ContainerCampaignConvertParams{
		Container: campaign.UUID, State: store.CampaignStateDraft,
	}); err != nil {
		t.Fatalf("convert draft: %v", err)
	}
	existing := createTimelineTask(t, s, projectUUID, "existing-enrolled", "open", "")
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: existing.ID, Patch: TaskPatch{Campaign: &campaign.UUID},
	}); err != nil {
		t.Fatalf("enroll existing draft member: %v", err)
	}
	cancelled, err := api.ContainerCampaignClose(context.Background(), ContainerCampaignCloseParams{
		Container: campaign.UUID, State: store.CampaignStateCancelled,
	})
	if err != nil {
		t.Fatalf("cancel draft: %v", err)
	}
	if cancelled.PreviousState == nil || *cancelled.PreviousState != store.CampaignStateDraft {
		t.Fatalf("draft cancellation = %#v", cancelled)
	}

	newMember := createTimelineTask(t, s, projectUUID, "terminal-enrollment", "open", "")
	_, err = api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: newMember.ID, Patch: TaskPatch{Campaign: &campaign.UUID},
	})
	var apiErr Error
	if !errors.As(err, &apiErr) || apiErr.Code() != CodeValidation {
		t.Fatalf("terminal enrollment error = %v, want WRKQ_VALIDATION", err)
	}

	if _, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "terminal-resident", Title: "terminal-resident", ProjectUUID: campaign.UUID,
		State: domain.StateOpen, Priority: 2,
	}); err == nil {
		t.Fatal("created resident task in terminal campaign")
	}
	if _, err := s.Tasks.Move(monitorSystemActor, newMember.UUID, campaign.UUID, 0); err == nil {
		t.Fatal("moved task into terminal campaign")
	}
	if _, err := api.TaskCopy(context.Background(), TaskCopyParams{
		Source: newMember.ID, Destination: campaign.UUID,
	}); err == nil {
		t.Fatal("copied task into terminal campaign")
	} else if !errors.As(err, &apiErr) || apiErr.Code() != CodeValidation {
		t.Fatalf("terminal copy error = %v, want WRKQ_VALIDATION", err)
	}

	empty := ""
	unenrolled, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: existing.ID, Patch: TaskPatch{Campaign: &empty},
	})
	if err != nil {
		t.Fatalf("leave terminal campaign: %v", err)
	}
	if unenrolled.CampaignUUID != "" {
		t.Fatalf("unenrolled task campaign = %q", unenrolled.CampaignUUID)
	}

	_, err = api.ContainerCampaignActivate(context.Background(), ContainerCampaignActivateParams{
		Container: campaign.UUID,
	})
	if !errors.As(err, &apiErr) || apiErr.Code() != CodeValidation {
		t.Fatalf("terminal activation error = %v, want WRKQ_VALIDATION", err)
	}
}

func TestCampaignPortfolioCompleteAggregateSnapshot(t *testing.T) {
	api, s := newMonitorAPI(t)
	projectUUID := seedMonitorProject(t, s)
	campaign := createTimelineContainer(t, s, projectUUID, "portfolio-campaign")
	labels := []string{"domain:platform"}
	if _, err := api.ContainerCampaignConvert(context.Background(), ContainerCampaignConvertParams{
		Container: campaign.UUID, State: store.CampaignStateDraft, Labels: &labels,
	}); err != nil {
		t.Fatalf("convert portfolio campaign: %v", err)
	}
	completed := createTimelineTask(t, s, campaign.UUID, "portfolio-completed", "completed", "")

	external, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{
		Slug: "portfolio-external", Title: "Portfolio External", Kind: "project",
	})
	if err != nil {
		t.Fatalf("create external project: %v", err)
	}
	inProgress := createTimelineTask(t, s, external.UUID, "portfolio-moving", "in_progress", "")
	if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: inProgress.ID, Patch: TaskPatch{Campaign: &campaign.UUID},
	}); err != nil {
		t.Fatalf("enroll portfolio member: %v", err)
	}
	for _, state := range []string{
		"idea", "draft", "open", "blocked", "cancelled", "archived", "deleted",
	} {
		createTimelineTask(t, s, campaign.UUID, "portfolio-"+state, state, "")
	}
	var campaignActivity, memberActivity string
	if err := s.DB().QueryRow(
		"SELECT updated_at FROM containers WHERE uuid = ?", campaign.UUID,
	).Scan(&campaignActivity); err != nil {
		t.Fatalf("read campaign activity fixture: %v", err)
	}
	if err := s.DB().QueryRow(`
		SELECT MAX(updated_at) FROM tasks
		 WHERE project_uuid = ? OR campaign_uuid = ?
	`, campaign.UUID, campaign.UUID).Scan(&memberActivity); err != nil {
		t.Fatalf("read member activity fixture: %v", err)
	}
	expectedActivity := maxTimestamp(toRFC3339(campaignActivity), toRFC3339(memberActivity))

	portfolio, err := api.ContainerCampaignPortfolio(
		context.Background(), ContainerCampaignPortfolioParams{},
	)
	if err != nil {
		t.Fatalf("portfolio: %v", err)
	}
	if len(portfolio.Items) != 1 {
		t.Fatalf("portfolio items = %d, want 1: %#v", len(portfolio.Items), portfolio.Items)
	}
	row := portfolio.Items[0]
	if row.Container.UUID != campaign.UUID || row.Container.CampaignState == nil ||
		*row.Container.CampaignState != store.CampaignStateDraft {
		t.Fatalf("portfolio container = %#v", row.Container)
	}
	if row.TotalMembers != 9 || row.ResidentCount != 8 || row.EnrolledCount != 1 ||
		row.InProgressCount != 1 || row.MissingOutcomeCount != 1 ||
		row.StateCounts["completed"] != 1 || row.StateCounts["in_progress"] != 1 {
		t.Fatalf("portfolio counts = %#v", row)
	}
	for _, state := range []string{
		"idea", "draft", "open", "in_progress", "completed",
		"blocked", "cancelled", "archived", "deleted",
	} {
		if row.StateCounts[state] != 1 {
			t.Fatalf("portfolio state count %q = %d, want 1: %#v", state, row.StateCounts[state], row.StateCounts)
		}
	}
	if len(row.Footprint) != 2 || row.LastActivityAt != expectedActivity {
		t.Fatalf("portfolio footprint/activity = %#v/%q", row.Footprint, row.LastActivityAt)
	}
	projectCounts := map[string]int{}
	for _, entry := range row.Footprint {
		projectCounts[entry.Project.UUID] = entry.MemberCount
	}
	if projectCounts[projectUUID] != 8 || projectCounts[external.UUID] != 1 {
		t.Fatalf("portfolio project counts = %#v", projectCounts)
	}
	if row.Container.Labels[0] != labels[0] {
		t.Fatalf("portfolio labels = %#v", row.Container.Labels)
	}

	detail, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: campaign.UUID,
	})
	if err != nil {
		t.Fatalf("portfolio detail: %v", err)
	}
	if len(detail.Members) != 9 || len(detail.Footprint) != 2 ||
		detail.LastActivityAt != row.LastActivityAt ||
		len(detail.MissingOutcomes) != 1 ||
		detail.MissingOutcomes[0].UUID != completed.UUID {
		t.Fatalf("detail aggregate = %#v", detail)
	}

	terminalOnly, err := api.ContainerCampaignPortfolio(context.Background(), ContainerCampaignPortfolioParams{
		States: []string{store.CampaignStateCompleted, store.CampaignStateCancelled},
	})
	if err != nil {
		t.Fatalf("terminal portfolio filter: %v", err)
	}
	if len(terminalOnly.Items) != 0 {
		t.Fatalf("terminal portfolio = %#v, want empty", terminalOnly.Items)
	}
}

func TestCampaignPortfolioFiltersArchiveAndOrdersCompleteSet(t *testing.T) {
	api, s := newMonitorAPI(t)
	projectUUID := seedMonitorProject(t, s)
	createCampaign := func(slug, state, createdAt string, archived bool) string {
		t.Helper()
		campaign := createTimelineContainer(t, s, projectUUID, slug)
		if _, err := api.ContainerCampaignConvert(context.Background(), ContainerCampaignConvertParams{
			Container: campaign.UUID, State: state,
		}); err != nil {
			t.Fatalf("convert %s campaign: %v", slug, err)
		}
		var archivedAt any
		if archived {
			archivedAt = "2026-07-23 00:00:00"
		}
		if _, err := s.DB().Exec(
			"UPDATE containers SET created_at = ?, updated_at = ?, archived_at = ? WHERE uuid = ?",
			createdAt, createdAt, archivedAt, campaign.UUID,
		); err != nil {
			t.Fatalf("set %s ordering fixture: %v", slug, err)
		}
		return campaign.UUID
	}
	oldDraft := createCampaign("old-draft", store.CampaignStateDraft, "2026-07-20 00:00:00", false)
	newActive := createCampaign("new-active", store.CampaignStateActive, "2026-07-22 00:00:00", false)
	archivedDraft := createCampaign("archived-draft", store.CampaignStateDraft, "2026-07-21 00:00:00", true)

	defaultView, err := api.ContainerCampaignPortfolio(context.Background(), ContainerCampaignPortfolioParams{})
	if err != nil {
		t.Fatalf("default portfolio: %v", err)
	}
	if len(defaultView.Items) != 2 ||
		defaultView.Items[0].Container.UUID != newActive ||
		defaultView.Items[1].Container.UUID != oldDraft {
		t.Fatalf("default portfolio order/filter = %#v", defaultView.Items)
	}
	for _, row := range defaultView.Items {
		if row.TotalMembers != 0 || len(row.StateCounts) != 0 ||
			len(row.Footprint) != 0 || row.LastActivityAt != row.Container.UpdatedAt {
			t.Fatalf("memberless campaign aggregate = %#v", row)
		}
	}

	drafts, err := api.ContainerCampaignPortfolio(context.Background(), ContainerCampaignPortfolioParams{
		States: []string{store.CampaignStateDraft}, IncludeArchived: true,
	})
	if err != nil {
		t.Fatalf("draft portfolio including archived: %v", err)
	}
	if len(drafts.Items) != 2 ||
		drafts.Items[0].Container.UUID != archivedDraft ||
		drafts.Items[1].Container.UUID != oldDraft {
		t.Fatalf("draft archived portfolio order/filter = %#v", drafts.Items)
	}
}