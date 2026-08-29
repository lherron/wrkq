//go:build wrkq_local

package rpccli

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
)

type campaignCLIFixture struct {
	dbPath                       string
	projectAUUID                 string
	campaignAUUID, campaignBUUID string
	residentID, enrolledID       string
	residentUUID, enrolledUUID   string
}

func newCampaignCLIFixture(t *testing.T) campaignCLIFixture {
	t.Helper()
	dbPath := t.TempDir() + "/wrkq.db"
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	s := store.New(database)
	actor := "00000000-0000-4000-8000-0000000000a0" // wrkq-system actor seeded by migrations
	projectA, err := s.Containers.Create(actor, store.ContainerCreateParams{Slug: "campaign-cli-a", Kind: "project"})
	if err != nil {
		t.Fatalf("create project A: %v", err)
	}
	projectB, err := s.Containers.Create(actor, store.ContainerCreateParams{Slug: "campaign-cli-b", Kind: "project"})
	if err != nil {
		t.Fatalf("create project B: %v", err)
	}
	campaignA, err := s.Containers.Create(actor, store.ContainerCreateParams{Slug: "wave-a", Kind: "directory", ParentUUID: &projectA.UUID})
	if err != nil {
		t.Fatalf("create campaign A: %v", err)
	}
	campaignB, err := s.Containers.Create(actor, store.ContainerCreateParams{Slug: "wave-b", Kind: "directory", ParentUUID: &projectB.UUID})
	if err != nil {
		t.Fatalf("create campaign B: %v", err)
	}
	if _, err := database.Exec("UPDATE containers SET campaign_state = 'active' WHERE uuid IN (?, ?)", campaignA.UUID, campaignB.UUID); err != nil {
		t.Fatalf("activate campaigns: %v", err)
	}
	resident, err := s.Tasks.Create(actor, store.CreateParams{
		Slug: "resident-member", Title: "Resident member", ProjectUUID: campaignA.UUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create resident: %v", err)
	}
	enrolled, err := s.Tasks.Create(actor, store.CreateParams{
		Slug: "enrolled-member", Title: "Enrolled member", ProjectUUID: projectB.UUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create enrolled candidate: %v", err)
	}
	return campaignCLIFixture{
		dbPath:        dbPath,
		projectAUUID:  projectA.UUID,
		campaignAUUID: campaignA.UUID, campaignBUUID: campaignB.UUID,
		residentID: resident.ID, enrolledID: enrolled.ID,
		residentUUID: resident.UUID, enrolledUUID: enrolled.UUID,
	}
}

func runCampaignCLI(t *testing.T, dbPath string, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs(append([]string{"--db", dbPath, "--principal-ref", "agent:campaign-test"}, args...))
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	err := cmd.Execute()
	return output.String(), err
}

func campaignUUIDForTask(t *testing.T, dbPath, taskUUID string) sql.NullString {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open fixture DB: %v", err)
	}
	defer func() { _ = database.Close() }()
	var got sql.NullString
	if err := database.QueryRow("SELECT campaign_uuid FROM tasks WHERE uuid = ?", taskUUID).Scan(&got); err != nil {
		t.Fatalf("query task campaign: %v", err)
	}
	return got
}

func TestCampaignRPCEnrollmentAndMoveMatrix(t *testing.T) {
	t.Run("cross project enroll and unenroll", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		if out, err := runCampaignCLI(t, f.dbPath, "set", f.enrolledID, "--campaign", f.campaignAUUID); err != nil {
			t.Fatalf("set --campaign failed: %v\n%s", err, out)
		}
		if got := campaignUUIDForTask(t, f.dbPath, f.enrolledUUID); !got.Valid || got.String != f.campaignAUUID {
			t.Fatalf("enrollment = %v, want %s", got, f.campaignAUUID)
		}
		if out, err := runCampaignCLI(t, f.dbPath, "set", f.enrolledID, "--campaign", ""); err != nil {
			t.Fatalf("set --campaign empty failed: %v\n%s", err, out)
		}
		if got := campaignUUIDForTask(t, f.dbPath, f.enrolledUUID); got.Valid {
			t.Fatalf("unenrollment retained campaign %s", got.String)
		}
	})

	t.Run("resident exclusivity rejection", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		out, err := runCampaignCLI(t, f.dbPath, "set", f.residentID, "--campaign", f.campaignBUUID)
		if err == nil || strings.Contains(strings.ToLower(err.Error()), "unknown flag") {
			t.Fatalf("foreign enrollment error = %v output=%q; want effective-membership rejection", err, out)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "campaign") {
			t.Fatalf("foreign enrollment error = %q; want campaign context", err)
		}
		if got := campaignUUIDForTask(t, f.dbPath, f.residentUUID); got.Valid {
			t.Fatalf("rejected foreign enrollment persisted %s", got.String)
		}
	})

	t.Run("different and same campaign move edges", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		database, err := db.Open(f.dbPath)
		if err != nil {
			t.Fatalf("open fixture DB: %v", err)
		}
		if _, err := database.Exec("UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", f.campaignAUUID, f.enrolledUUID); err != nil {
			t.Fatalf("seed enrollment: %v", err)
		}
		_ = database.Close()

		out, err := runCampaignCLI(t, f.dbPath, "mv", f.enrolledID, f.campaignBUUID)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "unenroll") {
			t.Fatalf("different-campaign move error = %v output=%q; want unenroll rejection", err, out)
		}
		if out, err := runCampaignCLI(t, f.dbPath, "mv", f.enrolledID, f.campaignAUUID); err != nil {
			t.Fatalf("same-campaign move failed: %v\n%s", err, out)
		}
		if got := campaignUUIDForTask(t, f.dbPath, f.enrolledUUID); got.Valid {
			t.Fatalf("same-campaign move retained redundant enrollment %s", got.String)
		}
	})
}

func TestCampaignFindAndTreeReadSemantics(t *testing.T) {
	f := newCampaignCLIFixture(t)
	database, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatalf("open fixture DB: %v", err)
	}
	if _, err := database.Exec("UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", f.campaignAUUID, f.enrolledUUID); err != nil {
		t.Fatalf("seed enrollment: %v", err)
	}
	_ = database.Close()

	unionOut, err := runCampaignCLI(t, f.dbPath, "find", "--campaign", f.campaignAUUID, "--type", "t", "--ndjson")
	if err != nil {
		t.Fatalf("find --campaign failed: %v\n%s", err, unionOut)
	}
	membership := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(unionOut))
	for scanner.Scan() {
		var row struct {
			Slug       string `json:"slug"`
			Membership string `json:"membership"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatalf("decode find campaign row %q: %v", scanner.Text(), err)
		}
		membership[row.Slug] = row.Membership
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan campaign union: %v", err)
	}
	if membership["resident-member"] != "resident" || membership["enrolled-member"] != "enrolled" {
		t.Fatalf("campaign union membership = %#v; want resident and enrolled markings", membership)
	}

	findOut, err := runCampaignCLI(t, f.dbPath, "--project", "campaign-cli-a", "find", "wave-a", "--type", "t", "--ndjson")
	if err != nil {
		t.Fatalf("residency find failed: %v\n%s", err, findOut)
	}
	if !strings.Contains(findOut, "resident-member") || strings.Contains(findOut, "enrolled-member") {
		t.Fatalf("machine find must stay residency-only, got %q", findOut)
	}

	treeOut, err := runCampaignCLI(t, f.dbPath, "--project", "campaign-cli-a", "tree", "wave-a", "--ndjson")
	if err != nil {
		t.Fatalf("machine tree failed: %v\n%s", err, treeOut)
	}
	if !strings.Contains(treeOut, "resident-member") || strings.Contains(treeOut, "enrolled-member") {
		t.Fatalf("machine tree must stay residency-only, got %q", treeOut)
	}

	humanOut, err := runCampaignCLI(t, f.dbPath, "--project", "campaign-cli-a", "tree", "wave-a", "--pretty")
	if err != nil {
		t.Fatalf("interactive tree rendering failed: %v\n%s", err, humanOut)
	}
	for _, want := range []string{"resident-member", "enrolled-member", "↗ campaign-cli-b"} {
		if !strings.Contains(humanOut, want) {
			t.Errorf("interactive campaign tree missing %q: %q", want, humanOut)
		}
	}
}

func TestTaskOutcomeCLISetEditClearHistoryAndFind(t *testing.T) {
	f := newCampaignCLIFixture(t)
	outcomeFile := t.TempDir() + "/outcome.md"
	const initial = "Shipped the first behavior.\nPreserved the full snapshot.\n"
	if err := os.WriteFile(outcomeFile, []byte(initial), 0o600); err != nil {
		t.Fatalf("write outcome fixture: %v", err)
	}

	if out, err := runCampaignCLIInput(t, f.dbPath, "", "set", f.residentID, "--outcome", "@"+outcomeFile); err != nil {
		t.Fatalf("set outcome from file: %v\n%s", err, out)
	}
	const amended = "Amended after verification.\n"
	if out, err := runCampaignCLIInput(t, f.dbPath, amended, "set", f.residentID, "--outcome", "-"); err != nil {
		t.Fatalf("edit outcome from stdin: %v\n%s", err, out)
	}

	findOut, err := runCampaignCLIInput(t, f.dbPath, "", "--project", "campaign-cli-a", "find", "--has-outcome", "--type", "t", "--ndjson")
	if err != nil {
		t.Fatalf("find --has-outcome after set: %v\n%s", err, findOut)
	}
	if !strings.Contains(findOut, "resident-member") || strings.Contains(findOut, "enrolled-member") {
		t.Fatalf("find --has-outcome after set = %q, want resident only", findOut)
	}

	if out, err := runCampaignCLIInput(t, f.dbPath, "", "set", f.residentID, "--outcome", " \n\t"); err != nil {
		t.Fatalf("clear whitespace outcome: %v\n%s", err, out)
	}
	findOut, err = runCampaignCLIInput(t, f.dbPath, "", "--project", "campaign-cli-a", "find", "--has-outcome", "--type", "t", "--ndjson")
	if err != nil {
		t.Fatalf("find --has-outcome after clear: %v\n%s", err, findOut)
	}
	if strings.Contains(findOut, "resident-member") {
		t.Fatalf("cleared task still returned by --has-outcome: %q", findOut)
	}

	if out, err := runCampaignCLIInput(t, f.dbPath, "", "set", f.residentID, "--state", "completed"); err != nil {
		t.Fatalf("completion without current outcome must succeed: %v\n%s", err, out)
	}

	database, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatalf("open outcome fixture DB: %v", err)
	}
	defer func() { _ = database.Close() }()

	rows, err := database.Query(`
		SELECT payload
		  FROM event_log
		 WHERE resource_uuid = ? AND event_type = 'task.outcome_set'
		 ORDER BY id`, f.residentUUID)
	if err != nil {
		t.Fatalf("query outcome history: %v", err)
	}
	defer func() { _ = rows.Close() }()
	expected := []any{initial, amended, nil}
	var index int
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan outcome history: %v", err)
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("decode outcome payload %q: %v", raw, err)
		}
		if index >= len(expected) {
			t.Fatalf("unexpected extra outcome event: %#v", payload)
		}
		if payload["task_uuid"] != f.residentUUID ||
			payload["outcome"] != expected[index] ||
			payload["container_uuid"] != f.campaignAUUID ||
			payload["campaign_uuid"] != f.campaignAUUID {
			t.Errorf("outcome event %d payload = %#v, want snapshot=%#v and resident campaign stamps",
				index+1, payload, expected[index])
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate outcome history: %v", err)
	}
	if index != 3 {
		t.Fatalf("task.outcome_set count = %d, want 3", index)
	}

	var outcome sql.NullString
	var state string
	if err := database.QueryRow("SELECT outcome, state FROM tasks WHERE uuid = ?", f.residentUUID).Scan(&outcome, &state); err != nil {
		t.Fatalf("load final task state: %v", err)
	}
	if outcome.Valid || state != "completed" {
		t.Fatalf("final task outcome/state = %#v/%q, want NULL/completed", outcome, state)
	}
}

func runCampaignCLIInput(t *testing.T, dbPath, input string, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs(append([]string{"--db", dbPath, "--principal-ref", "agent:campaign-test"}, args...))
	cmd.SetIn(strings.NewReader(input))
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	err := cmd.Execute()
	return output.String(), err
}

func TestCampaignLifecycleCLIContentAndSnapshotHistory(t *testing.T) {
	f := newCampaignCLIFixture(t)
	database, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatalf("open lifecycle fixture: %v", err)
	}
	if _, err := database.Exec(
		"UPDATE containers SET campaign_state = NULL WHERE uuid = ?", f.campaignAUUID,
	); err != nil {
		t.Fatalf("reset campaign to plain: %v", err)
	}
	_ = database.Close()

	const initialBrief = "Initial campaign brief.\n"
	const initialSpec = "Ratified campaign specification.\nSecond line.\n"
	convertOut, err := runCampaignCLIInput(
		t, f.dbPath, initialSpec,
		"campaign", "convert", f.campaignAUUID,
		"--description", initialBrief,
		"--specification", "-",
	)
	if err != nil {
		t.Fatalf("campaign convert: %v\n%s", err, convertOut)
	}
	var converted campaignTransitionResult
	if err := json.Unmarshal([]byte(convertOut), &converted); err != nil {
		t.Fatalf("decode convert output %q: %v", convertOut, err)
	}
	if converted.CampaignState != "active" || converted.Container.CampaignState == nil ||
		*converted.Container.CampaignState != "active" ||
		converted.Container.Description != initialBrief ||
		converted.Container.Specification == nil ||
		*converted.Container.Specification != initialSpec {
		t.Fatalf("convert readback = %#v, want active + exact bodies", converted)
	}

	database, err = db.Open(f.dbPath)
	if err != nil {
		t.Fatalf("reopen lifecycle fixture: %v", err)
	}
	defer func() { _ = database.Close() }()
	var kind string
	var archivedAt sql.NullString
	if err := database.QueryRow(
		"SELECT kind, archived_at FROM containers WHERE uuid = ?", f.campaignAUUID,
	).Scan(&kind, &archivedAt); err != nil {
		t.Fatalf("read conversion orthogonal fields: %v", err)
	}
	if kind != "directory" || archivedAt.Valid {
		t.Fatalf("conversion changed kind/archive = %q/%v, want directory/NULL", kind, archivedAt)
	}

	const amendedBrief = "Amended brief — exact bytes.\n"
	const amendedSpec = "Amended specification.\n\nFull body retained.\n"
	editOut, err := runCampaignCLIInput(
		t, f.dbPath, "",
		"campaign", "edit", f.campaignAUUID,
		"--description", amendedBrief,
		"--specification", amendedSpec,
	)
	if err != nil {
		t.Fatalf("campaign edit: %v\n%s", err, editOut)
	}
	var edited campaignContainer
	if err := json.Unmarshal([]byte(editOut), &edited); err != nil {
		t.Fatalf("decode edit output %q: %v", editOut, err)
	}
	if edited.Description != amendedBrief || edited.Specification == nil ||
		*edited.Specification != amendedSpec {
		t.Fatalf("edit readback = %#v, want exact amended bodies", edited)
	}

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
	database, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatalf("open draft fixture: %v", err)
	}
	if _, err := database.Exec(
		"UPDATE containers SET campaign_state = NULL WHERE uuid = ?", f.campaignAUUID,
	); err != nil {
		t.Fatalf("reset campaign to plain: %v", err)
	}
	_ = database.Close()

	labelsJSON := `["domain:platform"," domain:platform ","domain:platform"]`
	convertOut, err := runCampaignCLI(
		t, f.dbPath,
		"campaign", "convert", f.campaignAUUID,
		"--state", "draft",
		"--labels", labelsJSON,
	)
	if err != nil {
		t.Fatalf("draft campaign convert: %v\n%s", err, convertOut)
	}
	var converted campaignTransitionResult
	if err := json.Unmarshal([]byte(convertOut), &converted); err != nil {
		t.Fatalf("decode draft conversion: %v\n%s", err, convertOut)
	}
	if converted.CampaignState != "draft" || len(converted.Container.Labels) != 3 ||
		converted.Container.Labels[1] != " domain:platform " {
		t.Fatalf("draft conversion = %#v", converted)
	}

	portfolioOut, err := runCampaignCLI(
		t, f.dbPath, "campaign", "portfolio", "--state", "draft",
	)
	if err != nil {
		t.Fatalf("campaign portfolio: %v\n%s", err, portfolioOut)
	}
	var portfolio campaignPortfolioResult
	if err := json.Unmarshal([]byte(portfolioOut), &portfolio); err != nil {
		t.Fatalf("decode portfolio: %v\n%s", err, portfolioOut)
	}
	if len(portfolio.Items) != 1 ||
		portfolio.Items[0].Container.ID != converted.Container.ID ||
		portfolio.Items[0].TotalMembers != 1 {
		t.Fatalf("portfolio = %#v", portfolio)
	}

	activateOut, err := runCampaignCLI(
		t, f.dbPath, "campaign", "activate", f.campaignAUUID,
		"--if-match", fmt.Sprint(converted.Container.ETag),
	)
	if err != nil {
		t.Fatalf("activate campaign: %v\n%s", err, activateOut)
	}
	var activated campaignTransitionResult
	if err := json.Unmarshal([]byte(activateOut), &activated); err != nil {
		t.Fatalf("decode activation: %v\n%s", err, activateOut)
	}
	if activated.PreviousState == nil || *activated.PreviousState != "draft" ||
		activated.CampaignState != "active" {
		t.Fatalf("activation = %#v", activated)
	}

	editOut, err := runCampaignCLI(
		t, f.dbPath, "campaign", "edit", f.campaignAUUID, "--labels", "[]",
	)
	if err != nil {
		t.Fatalf("clear campaign labels: %v\n%s", err, editOut)
	}
	var edited campaignContainer
	if err := json.Unmarshal([]byte(editOut), &edited); err != nil {
		t.Fatalf("decode label clear: %v\n%s", err, editOut)
	}
	if edited.Labels == nil || len(edited.Labels) != 0 {
		t.Fatalf("cleared labels = %#v", edited.Labels)
	}
}

func TestCampaignLifecycleCLICompletedCloseDispositionMatrix(t *testing.T) {
	t.Run("complete and cancel", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		if out, err := runCampaignCLI(t, f.dbPath, "set", f.enrolledID, "--campaign", f.campaignAUUID); err != nil {
			t.Fatalf("enroll member: %v\n%s", err, out)
		}
		out, err := runCampaignCLI(
			t, f.dbPath, "campaign", "close", f.campaignAUUID, "--state", "completed",
		)
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

		if out, err := runCampaignCLI(t, f.dbPath, "set", f.residentID, "--state", "completed"); err != nil {
			t.Fatalf("complete resident: %v\n%s", err, out)
		}
		if out, err := runCampaignCLI(t, f.dbPath, "set", f.enrolledID, "--state", "cancelled"); err != nil {
			t.Fatalf("cancel enrolled: %v\n%s", err, out)
		}
		closeOut, err := runCampaignCLI(
			t, f.dbPath, "campaign", "close", f.campaignAUUID, "--state", "completed",
		)
		if err != nil {
			t.Fatalf("completed close after terminal dispositions: %v\n%s", err, closeOut)
		}
		var result campaignTransitionResult
		if err := json.Unmarshal([]byte(closeOut), &result); err != nil {
			t.Fatalf("decode close output %q: %v", closeOut, err)
		}
		if result.CampaignState != "completed" || len(result.MissingOutcomes) != 1 ||
			result.MissingOutcomes[0].UUID != f.residentUUID {
			t.Fatalf("completed close result = %#v, want non-blocking missing outcome", result)
		}
	})

	t.Run("move and unenroll", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		if out, err := runCampaignCLI(t, f.dbPath, "set", f.enrolledID, "--campaign", f.campaignAUUID); err != nil {
			t.Fatalf("enroll member: %v\n%s", err, out)
		}
		if out, err := runCampaignCLI(t, f.dbPath, "mv", f.residentID, f.projectAUUID); err != nil {
			t.Fatalf("move resident out: %v\n%s", err, out)
		}
		if out, err := runCampaignCLI(t, f.dbPath, "set", f.enrolledID, "--campaign", ""); err != nil {
			t.Fatalf("unenroll member: %v\n%s", err, out)
		}
		if out, err := runCampaignCLI(
			t, f.dbPath, "campaign", "close", f.campaignAUUID, "--state", "completed",
		); err != nil {
			t.Fatalf("completed close after move/unenroll: %v\n%s", err, out)
		}
	})
}

func TestCampaignLifecycleCLICancelAndNudgeAreEventsNotComments(t *testing.T) {
	t.Run("cancel bypasses disposition", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		if out, err := runCampaignCLI(t, f.dbPath, "set", f.enrolledID, "--campaign", f.campaignAUUID); err != nil {
			t.Fatalf("enroll member: %v\n%s", err, out)
		}
		if out, err := runCampaignCLI(
			t, f.dbPath, "campaign", "close", f.campaignAUUID, "--state", "cancelled",
		); err != nil {
			t.Fatalf("cancel campaign with open members: %v\n%s", err, out)
		}
		database, err := db.Open(f.dbPath)
		if err != nil {
			t.Fatalf("open cancelled fixture: %v", err)
		}
		defer func() { _ = database.Close() }()
		for _, taskUUID := range []string{f.residentUUID, f.enrolledUUID} {
			var state string
			if err := database.QueryRow("SELECT state FROM tasks WHERE uuid = ?", taskUUID).Scan(&state); err != nil {
				t.Fatalf("read cancelled-close member: %v", err)
			}
			if state != "open" {
				t.Fatalf("cancelled close changed task %s to %s", taskUUID, state)
			}
		}
	})

	t.Run("last terminal member emits raw-monitor nudge", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		if out, err := runCampaignCLI(t, f.dbPath, "set", f.enrolledID, "--campaign", f.campaignAUUID); err != nil {
			t.Fatalf("enroll member: %v\n%s", err, out)
		}
		database, err := db.Open(f.dbPath)
		if err != nil {
			t.Fatalf("open nudge fixture: %v", err)
		}
		var before int64
		if err := database.QueryRow("SELECT COALESCE(MAX(id),0) FROM event_log").Scan(&before); err != nil {
			t.Fatalf("read nudge cursor: %v", err)
		}
		_ = database.Close()

		if out, err := runCampaignCLI(t, f.dbPath, "set", f.residentID, "--state", "completed"); err != nil {
			t.Fatalf("complete first member: %v\n%s", err, out)
		}
		database, err = db.Open(f.dbPath)
		if err != nil {
			t.Fatalf("reopen nudge fixture: %v", err)
		}
		var earlyNudges int
		if err := database.QueryRow(`
			SELECT COUNT(*) FROM event_log
			 WHERE id > ? AND resource_uuid = ?
			   AND event_type = 'container.campaign_close_nudged'
		`, before, f.campaignAUUID).Scan(&earlyNudges); err != nil {
			t.Fatalf("count early nudges: %v", err)
		}
		_ = database.Close()
		if earlyNudges != 0 {
			t.Fatalf("first terminal member emitted %d nudge(s), want 0", earlyNudges)
		}

		if out, err := runCampaignCLI(t, f.dbPath, "set", f.enrolledID, "--state", "cancelled"); err != nil {
			t.Fatalf("terminalize last member: %v\n%s", err, out)
		}
		watchOut, err := runCampaignCLI(
			t, f.dbPath, "watch", "--since", fmt.Sprint(before), "--ndjson", "--follow=false",
		)
		if err != nil {
			t.Fatalf("raw monitor replay: %v\n%s", err, watchOut)
		}
		if !strings.Contains(watchOut, `"event_type":"container.campaign_close_nudged"`) ||
			!strings.Contains(watchOut, "all_members_terminal") {
			t.Fatalf("raw monitor missing campaign nudge: %s", watchOut)
		}

		database, err = db.Open(f.dbPath)
		if err != nil {
			t.Fatalf("reopen final nudge fixture: %v", err)
		}
		defer func() { _ = database.Close() }()
		var transitions, nudges, comments int
		if err := database.QueryRow(`
			SELECT COUNT(*) FROM event_log
			 WHERE resource_uuid = ? AND event_type = 'container.campaign_close_nudged'
		`, f.campaignAUUID).Scan(&nudges); err != nil {
			t.Fatalf("count nudges: %v", err)
		}
		if err := database.QueryRow(`
			SELECT COUNT(*) FROM event_log
			 WHERE resource_uuid = ? AND event_type = 'container.campaign_state_changed'
		`, f.campaignAUUID).Scan(&transitions); err != nil {
			t.Fatalf("count transitions: %v", err)
		}
		if err := database.QueryRow(
			"SELECT COUNT(*) FROM comments WHERE container_uuid = ?", f.campaignAUUID,
		).Scan(&comments); err != nil {
			t.Fatalf("count campaign comments: %v", err)
		}
		if nudges != 1 || transitions != 0 || comments != 0 {
			t.Fatalf("nudge/state/comment counts = %d/%d/%d, want 1/0/0", nudges, transitions, comments)
		}
	})
}

// TestCampaignSelectorGrammarIsUniform pins the finding from T-06823: a BARE
// campaign slug resolved for `campaign convert|close` (which scope the argument
// to the project root) but not for `set --campaign`, which shipped the raw token
// to the server and failed with "campaign not found". One selector grammar,
// every command.
func TestCampaignSelectorGrammarIsUniform(t *testing.T) {
	f := newCampaignCLIFixture(t)

	if out, err := runCampaignCLI(
		t, f.dbPath, "--project", "campaign-cli-a", "set", f.enrolledID, "--campaign", "wave-a",
	); err != nil {
		t.Fatalf("bare-slug --campaign failed: %v\n%s", err, out)
	}
	if got := campaignUUIDForTask(t, f.dbPath, f.enrolledUUID); !got.Valid || got.String != f.campaignAUUID {
		t.Fatalf("bare-slug enrollment = %v, want %s", got, f.campaignAUUID)
	}

	// The qualified forms must keep resolving identically.
	for _, selector := range []string{"campaign-cli-a/wave-a", f.campaignAUUID} {
		if out, err := runCampaignCLI(t, f.dbPath, "set", f.enrolledID, "--campaign", ""); err != nil {
			t.Fatalf("unenroll before %q failed: %v\n%s", selector, err, out)
		}
		if out, err := runCampaignCLI(
			t, f.dbPath, "--project", "campaign-cli-a", "set", f.enrolledID, "--campaign", selector,
		); err != nil {
			t.Fatalf("--campaign %q failed: %v\n%s", selector, err, out)
		}
		if got := campaignUUIDForTask(t, f.dbPath, f.enrolledUUID); !got.Valid || got.String != f.campaignAUUID {
			t.Fatalf("--campaign %q enrollment = %v, want %s", selector, got, f.campaignAUUID)
		}
	}
}

// TestCampaignSelectorAbsoluteFallback pins the scoped-first / absolute-fallback
// container-selector rule (wrkq.project-root.caller-semantics, T-07701). From a
// FOREIGN project root a campaign was previously reachable only by its P- id:
// every path form was prefixed with the caller's root and missed.
func TestCampaignSelectorAbsoluteFallback(t *testing.T) {
	withRootB := func(t *testing.T) {
		t.Helper()
		t.Setenv("ASP_PROJECT", "")
		t.Setenv("WRKQ_PROJECT_ROOT", "campaign-cli-b")
	}

	t.Run("absolute path resolves from a foreign root", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		withRootB(t)
		if out, err := runCampaignCLI(t, f.dbPath, "set", f.enrolledID, "--campaign", "campaign-cli-a/wave-a"); err != nil {
			t.Fatalf("absolute campaign path failed: %v\n%s", err, out)
		}
		if got := campaignUUIDForTask(t, f.dbPath, f.enrolledUUID); !got.Valid || got.String != f.campaignAUUID {
			t.Fatalf("enrollment = %v, want %s", got, f.campaignAUUID)
		}
	})

	t.Run("scoped first: a missing path errors with the SCOPED path", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		withRootB(t)
		_, err := runCampaignCLI(t, f.dbPath, "set", f.enrolledID, "--campaign", "campaign-cli-b/nope")
		if err == nil {
			t.Fatal("missing campaign path unexpectedly succeeded")
		}
		if !strings.Contains(err.Error(), "campaign-cli-b/nope") {
			t.Fatalf("error = %q; want it to name the scoped path campaign-cli-b/nope", err)
		}
	})

	t.Run("a bare slug is never re-pointed at another project", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		withRootB(t)
		_, err := runCampaignCLI(t, f.dbPath, "set", f.enrolledID, "--campaign", "wave-a")
		if err == nil {
			t.Fatal("bare foreign slug unexpectedly resolved across projects")
		}
		if !strings.Contains(err.Error(), "campaign-cli-b/wave-a") {
			t.Fatalf("error = %q; want the scoped path campaign-cli-b/wave-a", err)
		}
		if got := campaignUUIDForTask(t, f.dbPath, f.enrolledUUID); got.Valid {
			t.Fatalf("bare slug enrolled the task in %s", got.String)
		}
	})
}

// TestTouchCampaignEnrollsAtCreate pins `wrkq touch --campaign`: a cross-project
// slot is ONE command, and create is a full campaign admission path (T-07701).
func TestTouchCampaignEnrollsAtCreate(t *testing.T) {
	t.Run("enrolls a task resident in another project", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		t.Setenv("ASP_PROJECT", "")
		t.Setenv("WRKQ_PROJECT_ROOT", "campaign-cli-b")
		out, err := runCampaignCLI(t, f.dbPath, "touch", "slot", "--title", "Slot",
			"--campaign", "campaign-cli-a/wave-a", "--json")
		if err != nil {
			t.Fatalf("touch --campaign failed: %v\n%s", err, out)
		}
		var created []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(out), &created); err != nil || len(created) != 1 {
			t.Fatalf("decode touch output %q: %v", out, err)
		}
		if created[0].Path != "campaign-cli-b/slot" {
			t.Fatalf("created path = %q; the task must keep its OWN project", created[0].Path)
		}
		database, derr := db.Open(f.dbPath)
		if derr != nil {
			t.Fatalf("open fixture DB: %v", derr)
		}
		defer func() { _ = database.Close() }()
		var campaignUUID sql.NullString
		if err := database.QueryRow(
			"SELECT campaign_uuid FROM tasks WHERE id = ?", created[0].ID,
		).Scan(&campaignUUID); err != nil {
			t.Fatalf("read created task: %v", err)
		}
		if !campaignUUID.Valid || campaignUUID.String != f.campaignAUUID {
			t.Fatalf("created enrollment = %v, want %s", campaignUUID, f.campaignAUUID)
		}
	})

	t.Run("a terminal campaign rejects the create outright", func(t *testing.T) {
		f := newCampaignCLIFixture(t)
		t.Setenv("ASP_PROJECT", "")
		t.Setenv("WRKQ_PROJECT_ROOT", "campaign-cli-b")
		if out, err := runCampaignCLI(t, f.dbPath, "campaign", "close", f.campaignAUUID, "--state", "cancelled"); err != nil {
			t.Fatalf("close campaign A: %v\n%s", err, out)
		}
		out, err := runCampaignCLI(t, f.dbPath, "touch", "late-slot", "--title", "Late",
			"--campaign", f.campaignAUUID, "--json")
		if err == nil {
			t.Fatalf("create into a terminal campaign succeeded: %s", out)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "draft or active") {
			t.Fatalf("error = %q; want the shared admission rejection", err)
		}
		database, derr := db.Open(f.dbPath)
		if derr != nil {
			t.Fatalf("open fixture DB: %v", derr)
		}
		defer func() { _ = database.Close() }()
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM tasks WHERE slug = 'late-slot'").Scan(&count); err != nil {
			t.Fatalf("count late slots: %v", err)
		}
		if count != 0 {
			t.Fatalf("rejected create left %d task(s) behind; the insert must roll back", count)
		}
	})
}

// TestCatShowsEffectiveCampaignMembership pins the cat/show projection that made
// enrolment visible at all (T-07701): before it, a task could be enrolled and no
// reader of the task could tell.
func TestCatShowsEffectiveCampaignMembership(t *testing.T) {
	f := newCampaignCLIFixture(t)
	database, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatalf("open fixture DB: %v", err)
	}
	if _, err := database.Exec("UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", f.campaignAUUID, f.enrolledUUID); err != nil {
		t.Fatalf("seed enrollment: %v", err)
	}
	_ = database.Close()

	for _, tc := range []struct {
		name, taskID, wantPath, wantMembership string
	}{
		{"resident member", f.residentID, "campaign-cli-a/wave-a", "resident"},
		{"enrolled cross-project member", f.enrolledID, "campaign-cli-a/wave-a", "enrolled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runCampaignCLI(t, f.dbPath, "cat", tc.taskID, "--output", "raw")
			if err != nil {
				t.Fatalf("cat: %v\n%s", err, out)
			}
			want := "campaign: " + campaignFriendlyID(t, f.dbPath, f.campaignAUUID) +
				" " + tc.wantPath + " " + tc.wantMembership
			if !strings.Contains(out, want) {
				t.Fatalf("cat output missing %q:\n%s", want, out)
			}
		})
	}

	t.Run("a task in no campaign prints no campaign line", func(t *testing.T) {
		f2 := newCampaignCLIFixture(t)
		out, err := runCampaignCLI(t, f2.dbPath, "cat", f2.enrolledID, "--output", "raw")
		if err != nil {
			t.Fatalf("cat: %v\n%s", err, out)
		}
		if strings.Contains(out, "campaign:") {
			t.Fatalf("non-member cat output carries a campaign line:\n%s", out)
		}
	})
}

func campaignFriendlyID(t *testing.T, dbPath, containerUUID string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open fixture DB: %v", err)
	}
	defer func() { _ = database.Close() }()
	var id string
	if err := database.QueryRow("SELECT id FROM containers WHERE uuid = ?", containerUUID).Scan(&id); err != nil {
		t.Fatalf("read campaign id: %v", err)
	}
	return id
}
