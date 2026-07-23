package store

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCampaignConversionUsesEffectiveMembershipValidatorAtomically(t *testing.T) {
	f := newCampaignMembershipFixture(t)
	taskUUID := f.createTask(t, "conversion-conflict", f.nonCampaignContainer, nil)
	if _, err := f.db.Exec(
		"UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", f.campaignB, taskUUID,
	); err != nil {
		t.Fatalf("seed foreign enrollment: %v", err)
	}
	description := "must roll back"
	_, err := f.store.Containers.ConvertCampaignWithAttribution(
		testAttribution(f.actorUUID), f.nonCampaignContainer, CampaignStateActive,
		&description, nil, nil, 0,
	)
	requireCampaignRejection(t, err, "unenroll")

	var state sql.NullString
	var gotDescription string
	if err := f.db.QueryRow(
		"SELECT campaign_state, description FROM containers WHERE uuid = ?",
		f.nonCampaignContainer,
	).Scan(&state, &gotDescription); err != nil {
		t.Fatalf("read rejected conversion: %v", err)
	}
	if state.Valid || gotDescription != "" {
		t.Fatalf("rejected conversion mutated state/content: state=%v description=%q", state, gotDescription)
	}
	var events int
	if err := f.db.QueryRow(`
		SELECT COUNT(*) FROM event_log
		 WHERE resource_uuid = ?
		   AND event_type IN ('container.updated','container.campaign_state_changed')
	`, f.nonCampaignContainer).Scan(&events); err != nil {
		t.Fatalf("count conversion events: %v", err)
	}
	if events != 0 {
		t.Fatalf("rejected conversion wrote %d event(s), want 0", events)
	}
}

func TestCampaignCloseGuardSingleTransactionSeesConcurrentReopen(t *testing.T) {
	f := newCampaignMembershipFixture(t)
	taskUUID := f.createTask(t, "race-member", f.campaignA, nil)
	if _, err := f.store.Tasks.UpdateFields(
		f.actorUUID, taskUUID, map[string]any{"state": "completed"}, 0,
	); err != nil {
		t.Fatalf("complete member: %v", err)
	}

	// Hold the production BEGIN IMMEDIATE writer lock, stage the reopen, and
	// start close on another pooled connection. Close cannot read a stale
	// terminal snapshot and later write: it blocks at transaction start, then
	// evaluates the guard after this reopen commits.
	reopenTx, err := f.db.Begin()
	if err != nil {
		t.Fatalf("begin reopen tx: %v", err)
	}
	if _, err := reopenTx.Exec(
		"UPDATE tasks SET state = 'open', etag = etag + 1 WHERE uuid = ?", taskUUID,
	); err != nil {
		_ = reopenTx.Rollback()
		t.Fatalf("stage reopen: %v", err)
	}

	closeResult := make(chan error, 1)
	go func() {
		_, closeErr := f.store.Containers.TransitionCampaignWithAttribution(
			testAttribution(f.actorUUID), f.campaignA, CampaignStateCompleted, 0,
		)
		closeResult <- closeErr
	}()
	time.Sleep(75 * time.Millisecond)
	if err := reopenTx.Commit(); err != nil {
		t.Fatalf("commit reopen: %v", err)
	}

	select {
	case closeErr := <-closeResult:
		var blocked *CampaignCloseBlockedError
		if !errors.As(closeErr, &blocked) || len(blocked.Stragglers) != 1 ||
			blocked.Stragglers[0].UUID != taskUUID {
			t.Fatalf("close after concurrent reopen = %v (%#v), want one-member guard block", closeErr, blocked)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("campaign close did not resume after concurrent reopen committed")
	}

	var campaignState, taskState string
	if err := f.db.QueryRow(
		"SELECT campaign_state FROM containers WHERE uuid = ?", f.campaignA,
	).Scan(&campaignState); err != nil {
		t.Fatalf("read campaign state: %v", err)
	}
	if err := f.db.QueryRow("SELECT state FROM tasks WHERE uuid = ?", taskUUID).Scan(&taskState); err != nil {
		t.Fatalf("read task state: %v", err)
	}
	if campaignState != CampaignStateActive || taskState != "open" {
		t.Fatalf("race result campaign/task = %s/%s, want active/open", campaignState, taskState)
	}
}

func TestCampaignCompletedCloseMissingOutcomeIsNonBlocking(t *testing.T) {
	f := newCampaignMembershipFixture(t)
	taskUUID := f.createTask(t, "missing-outcome", f.campaignA, nil)
	if _, err := f.store.Tasks.UpdateFields(
		f.actorUUID, taskUUID, map[string]any{"state": "completed"}, 0,
	); err != nil {
		t.Fatalf("complete member: %v", err)
	}
	result, err := f.store.Containers.TransitionCampaignWithAttribution(
		testAttribution(f.actorUUID), f.campaignA, CampaignStateCompleted, 0,
	)
	if err != nil {
		t.Fatalf("completed close with missing outcome: %v", err)
	}
	if len(result.MissingOutcomes) != 1 || result.MissingOutcomes[0].UUID != taskUUID {
		t.Fatalf("missing outcomes = %#v, want completed member", result.MissingOutcomes)
	}
}

func TestCampaignCancelledCloseBypassesOpenMembers(t *testing.T) {
	f := newCampaignMembershipFixture(t)
	taskUUID := f.createTask(t, "abandoned-member", f.campaignA, nil)
	completedUUID := f.createTask(t, "completed-without-outcome", f.campaignA, nil)
	if _, err := f.db.Exec(
		"UPDATE tasks SET state = 'completed' WHERE uuid = ?", completedUUID,
	); err != nil {
		t.Fatalf("seed completed member: %v", err)
	}
	result, err := f.store.Containers.TransitionCampaignWithAttribution(
		testAttribution(f.actorUUID), f.campaignA, CampaignStateCancelled, 0,
	)
	if err != nil {
		t.Fatalf("cancel campaign with open member: %v", err)
	}
	if result.CampaignState != CampaignStateCancelled {
		t.Fatalf("campaign state = %s, want cancelled", result.CampaignState)
	}
	if len(result.MissingOutcomes) != 0 {
		t.Fatalf("cancelled close returned missing-outcome diagnostics: %#v", result.MissingOutcomes)
	}
	var taskState string
	if err := f.db.QueryRow("SELECT state FROM tasks WHERE uuid = ?", taskUUID).Scan(&taskState); err != nil {
		t.Fatalf("read abandoned task: %v", err)
	}
	if taskState != "open" {
		t.Fatalf("cancelled close changed member to %s, want open", taskState)
	}
}

func TestCampaignCompletedCloseUsesCanonicalTerminalSet(t *testing.T) {
	for _, tc := range []struct {
		state   string
		blocked bool
	}{
		{state: "completed"},
		{state: "cancelled"},
		{state: "archived"},
		{state: "deleted"},
		{state: "idea", blocked: true},
		{state: "draft", blocked: true},
	} {
		t.Run(tc.state, func(t *testing.T) {
			f := newCampaignMembershipFixture(t)
			taskUUID := f.createTask(t, "member-"+tc.state, f.campaignA, nil)
			if _, err := f.db.Exec(
				"UPDATE tasks SET state = ? WHERE uuid = ?", tc.state, taskUUID,
			); err != nil {
				t.Fatalf("seed member state %s: %v", tc.state, err)
			}

			_, err := f.store.Containers.TransitionCampaignWithAttribution(
				testAttribution(f.actorUUID), f.campaignA, CampaignStateCompleted, 0,
			)
			var blocked *CampaignCloseBlockedError
			if tc.blocked {
				if !errors.As(err, &blocked) || len(blocked.Stragglers) != 1 ||
					blocked.Stragglers[0].State != tc.state {
					t.Fatalf("close with %s member = %v (%#v), want blocked", tc.state, err, blocked)
				}
				return
			}
			if err != nil {
				t.Fatalf("close with terminal %s member: %v", tc.state, err)
			}
		})
	}
}

func TestCampaignContainerDeleteAndPurgeRestrictEnrolledMembers(t *testing.T) {
	for _, recursive := range []bool{false, true} {
		name := "delete"
		if recursive {
			name = "recursive purge"
		}
		t.Run(name, func(t *testing.T) {
			f := newCampaignMembershipFixture(t)
			taskUUID := f.createTask(t, "external-enrollment", f.projectB, nil)
			if _, err := f.db.Exec(
				"UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", f.campaignA, taskUUID,
			); err != nil {
				t.Fatalf("seed enrollment: %v", err)
			}

			var err error
			if recursive {
				impact, impactErr := f.store.Containers.DeleteRecursiveImpact(f.campaignA)
				if impactErr != nil {
					t.Fatalf("recursive impact: %v", impactErr)
				}
				_, err = f.store.Containers.DeleteRecursiveWithAttribution(
					testAttribution(f.actorUUID), f.campaignA, 0, *impact,
				)
			} else {
				err = f.store.Containers.DeleteWithAttribution(
					testAttribution(f.actorUUID), f.campaignA, 0,
				)
			}
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "foreign key") {
				t.Fatalf("%s error = %v, want FK RESTRICT", name, err)
			}
			var containerCount, enrollmentCount int
			if err := f.db.QueryRow(
				"SELECT COUNT(*) FROM containers WHERE uuid = ?", f.campaignA,
			).Scan(&containerCount); err != nil {
				t.Fatalf("count retained campaign: %v", err)
			}
			if err := f.db.QueryRow(
				"SELECT COUNT(*) FROM tasks WHERE uuid = ? AND campaign_uuid = ?",
				taskUUID, f.campaignA,
			).Scan(&enrollmentCount); err != nil {
				t.Fatalf("count retained enrollment: %v", err)
			}
			if containerCount != 1 || enrollmentCount != 1 {
				t.Fatalf("%s partially mutated campaign/enrollment = %d/%d", name, containerCount, enrollmentCount)
			}
		})
	}
}
