//go:build wrkq_local

package rpccli

// campaign_lifecycle_acceptance_test.go — the campaign lifecycle verbs from the
// CLI: convert (with exact bodies, draft state and labels), edit and its
// content-snapshot event, the draft portfolio and activation, and close —
// completed (guarded by member dispositions) versus cancelled (unguarded) —
// plus the raw-monitor nudge when the last member goes terminal.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// resetToPlainContainer clears wave-a's campaign state so convert has a plain
// directory to work on.
func (f campaignCLIFixture) resetToPlainContainer(t *testing.T) {
	t.Helper()
	f.exec(t, "UPDATE containers SET campaign_state = NULL WHERE uuid = ?", f.campaignAUUID)
}

// decodeCLIJSON decodes a JSON command output into v.
func decodeCLIJSON(t *testing.T, out string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(out), v); err != nil {
		t.Fatalf("decode %T from %q: %v", v, out, err)
	}
}

func TestCampaignLifecycleCLIContentAndSnapshotHistory(t *testing.T) {
	f := newCampaignCLIFixture(t)
	f.resetToPlainContainer(t)

	const initialBrief = "Initial campaign brief.\n"
	const initialSpec = "Ratified campaign specification.\nSecond line.\n"
	convertOut, err := runCampaignCLIInput(t, f.dbPath, initialSpec,
		"campaign", "convert", f.campaignAUUID, "--description", initialBrief, "--specification", "-")
	if err != nil {
		t.Fatalf("campaign convert: %v\n%s", err, convertOut)
	}
	var converted campaignTransitionResult
	decodeCLIJSON(t, convertOut, &converted)
	if converted.CampaignState != "active" || converted.Container.CampaignState == nil ||
		*converted.Container.CampaignState != "active" ||
		converted.Container.Description != initialBrief ||
		converted.Container.Specification == nil ||
		*converted.Container.Specification != initialSpec {
		t.Fatalf("convert readback = %#v, want active + exact bodies", converted)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM containers WHERE uuid = ? AND kind = 'directory' AND archived_at IS NULL", f.campaignAUUID); n != 1 {
		t.Fatalf("conversion changed kind/archive; want directory/NULL")
	}

	const amendedBrief = "Amended brief — exact bytes.\n"
	const amendedSpec = "Amended specification.\n\nFull body retained.\n"
	var edited campaignContainer
	decodeCLIJSON(t, f.mustRun(t, "campaign", "edit", f.campaignAUUID,
		"--description", amendedBrief, "--specification", amendedSpec), &edited)
	if edited.Description != amendedBrief || edited.Specification == nil || *edited.Specification != amendedSpec {
		t.Fatalf("edit readback = %#v, want exact amended bodies", edited)
	}

	database := f.openDB(t)
	defer func() { _ = database.Close() }()
	var payload string
	if err := database.QueryRow(`
		SELECT payload FROM event_log
		 WHERE resource_uuid = ? AND event_type = 'container.updated'
		 ORDER BY id DESC LIMIT 1
	`, f.campaignAUUID).Scan(&payload); err != nil {
		t.Fatalf("read content snapshot event: %v", err)
	}
	wantPayloadBytes, _ := json.Marshal(map[string]any{
		"description":   amendedBrief,
		"specification": amendedSpec,
	})
	if payload != string(wantPayloadBytes) {
		t.Fatalf("container.updated payload bytes:\n got: %q\nwant: %q", payload, wantPayloadBytes)
	}
}

func TestCampaignDraftLabelsActivationAndPortfolioCLI(t *testing.T) {
	f := newCampaignCLIFixture(t)
	f.resetToPlainContainer(t)

	var converted campaignTransitionResult
	decodeCLIJSON(t, f.mustRun(t, "campaign", "convert", f.campaignAUUID,
		"--state", "draft", "--labels", `["domain:platform"," domain:platform ","domain:platform"]`), &converted)
	if converted.CampaignState != "draft" || len(converted.Container.Labels) != 3 ||
		converted.Container.Labels[1] != " domain:platform " {
		t.Fatalf("draft conversion = %#v", converted)
	}

	var portfolio campaignPortfolioResult
	decodeCLIJSON(t, f.mustRun(t, "campaign", "portfolio", "--state", "draft"), &portfolio)
	if len(portfolio.Items) != 1 ||
		portfolio.Items[0].Container.ID != converted.Container.ID ||
		portfolio.Items[0].TotalMembers != 1 {
		t.Fatalf("portfolio = %#v", portfolio)
	}

	var activated campaignTransitionResult
	decodeCLIJSON(t, f.mustRun(t, "campaign", "activate", f.campaignAUUID,
		"--if-match", fmt.Sprint(converted.Container.ETag)), &activated)
	if activated.PreviousState == nil || *activated.PreviousState != "draft" || activated.CampaignState != "active" {
		t.Fatalf("activation = %#v", activated)
	}

	var edited campaignContainer
	decodeCLIJSON(t, f.mustRun(t, "campaign", "edit", f.campaignAUUID, "--labels", "[]"), &edited)
	if edited.Labels == nil || len(edited.Labels) != 0 {
		t.Fatalf("cleared labels = %#v", edited.Labels)
	}
}

func TestCampaignLifecycleCLICompletedCloseDispositionMatrix(t *testing.T) {
	t.Run("complete and cancel", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		f.mustRun(t, "set", f.enrolledID, "--campaign", f.campaignAUUID)
		out, err := runCampaignCLI(t, f.dbPath, "campaign", "close", f.campaignAUUID, "--state", "completed")
		if err == nil {
			t.Fatalf("completed close with open members succeeded: %s", out)
		}
		for _, want := range []string{
			"resident-member", "enrolled-member", "resident", "enrolled",
			"complete/cancel", "move/unenroll",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("close guard error missing %q: %v", want, err)
			}
		}

		f.mustRun(t, "set", f.residentID, "--state", "completed")
		f.mustRun(t, "set", f.enrolledID, "--state", "cancelled")
		var result campaignTransitionResult
		decodeCLIJSON(t, f.mustRun(t, "campaign", "close", f.campaignAUUID, "--state", "completed"), &result)
		if result.CampaignState != "completed" || len(result.MissingOutcomes) != 1 ||
			result.MissingOutcomes[0].UUID != f.residentUUID {
			t.Fatalf("completed close result = %#v, want non-blocking missing outcome", result)
		}
	})

	t.Run("move and unenroll", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		f.mustRun(t, "set", f.enrolledID, "--campaign", f.campaignAUUID)
		f.mustRun(t, "mv", f.residentID, f.projectAUUID)
		f.mustRun(t, "set", f.enrolledID, "--campaign", "")
		f.mustRun(t, "campaign", "close", f.campaignAUUID, "--state", "completed")
	})
}

func TestCampaignLifecycleCLICancelAndNudgeAreEventsNotComments(t *testing.T) {
	t.Run("cancel bypasses disposition", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		f.mustRun(t, "set", f.enrolledID, "--campaign", f.campaignAUUID)
		f.mustRun(t, "campaign", "close", f.campaignAUUID, "--state", "cancelled")
		for _, taskUUID := range []string{f.residentUUID, f.enrolledUUID} {
			if n := f.count(t, "SELECT COUNT(*) FROM tasks WHERE uuid = ? AND state = 'open'", taskUUID); n != 1 {
				t.Fatalf("cancelled close changed task %s out of open", taskUUID)
			}
		}
	})

	t.Run("last terminal member emits raw-monitor nudge", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		f.mustRun(t, "set", f.enrolledID, "--campaign", f.campaignAUUID)
		before := f.count(t, "SELECT COALESCE(MAX(id),0) FROM event_log")
		const nudgesSince = `SELECT COUNT(*) FROM event_log
			 WHERE id > ? AND resource_uuid = ? AND event_type = 'container.campaign_close_nudged'`

		f.mustRun(t, "set", f.residentID, "--state", "completed")
		if early := f.count(t, nudgesSince, before, f.campaignAUUID); early != 0 {
			t.Fatalf("first terminal member emitted %d nudge(s), want 0", early)
		}

		f.mustRun(t, "set", f.enrolledID, "--state", "cancelled")
		watchOut := f.mustRun(t, "monitor", "watch", "--raw", "--since", fmt.Sprint(before), "--stall-after", "300ms")
		if !strings.Contains(watchOut, `"event_type":"container.campaign_close_nudged"`) ||
			!strings.Contains(watchOut, "all_members_terminal") {
			t.Fatalf("raw monitor missing campaign nudge: %s", watchOut)
		}

		nudges := f.count(t, nudgesSince, 0, f.campaignAUUID)
		transitions := f.count(t, `SELECT COUNT(*) FROM event_log
			 WHERE resource_uuid = ? AND event_type = 'container.campaign_state_changed'`, f.campaignAUUID)
		comments := f.count(t, "SELECT COUNT(*) FROM comments WHERE container_uuid = ?", f.campaignAUUID)
		if nudges != 1 || transitions != 0 || comments != 0 {
			t.Fatalf("nudge/state/comment counts = %d/%d/%d, want 1/0/0", nudges, transitions, comments)
		}
	})
}
