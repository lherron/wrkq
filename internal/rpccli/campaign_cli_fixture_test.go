//go:build wrkq_local

package rpccli

// campaign_cli_fixture_test.go — the shared world for the campaign CLI
// acceptance tests: two projects, an active campaign in each (wave-a under
// campaign-cli-a, wave-b under campaign-cli-b), a task resident in wave-a and a
// task in campaign-cli-b that tests enroll into wave-a. Helpers run the real
// wrkq root command in-process and read or seed the fixture DB directly.

import (
	"bytes"
	"database/sql"
	"io"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/store"
)

// campaignFixtureActor is the wrkq-system actor seeded by migrations.
const campaignFixtureActor = "00000000-0000-4000-8000-0000000000a0"

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
	projectA, err := s.Containers.Create(campaignFixtureActor, store.ContainerCreateParams{Slug: "campaign-cli-a", Kind: "project"})
	if err != nil {
		t.Fatalf("create project A: %v", err)
	}
	projectB, err := s.Containers.Create(campaignFixtureActor, store.ContainerCreateParams{Slug: "campaign-cli-b", Kind: "project"})
	if err != nil {
		t.Fatalf("create project B: %v", err)
	}
	campaignA := createActiveCampaign(t, database, "wave-a", projectA.UUID)
	campaignB := createActiveCampaign(t, database, "wave-b", projectB.UUID)
	resident := createCampaignFixtureTask(t, database, "resident-member", "Resident member", campaignA, "open")
	enrolled := createCampaignFixtureTask(t, database, "enrolled-member", "Enrolled member", projectB.UUID, "open")
	return campaignCLIFixture{
		dbPath:        dbPath,
		projectAUUID:  projectA.UUID,
		campaignAUUID: campaignA, campaignBUUID: campaignB,
		residentID: resident.ID, enrolledID: enrolled.ID,
		residentUUID: resident.UUID, enrolledUUID: enrolled.UUID,
	}
}

// createActiveCampaign creates a directory under parentUUID and makes it an
// active campaign, returning its UUID.
func createActiveCampaign(t *testing.T, database *db.DB, slug, parentUUID string) string {
	t.Helper()
	uuid := createDirectory(t, database, slug, parentUUID)
	if _, err := database.Exec("UPDATE containers SET campaign_state = 'active' WHERE uuid = ?", uuid); err != nil {
		t.Fatalf("activate campaign %s: %v", slug, err)
	}
	return uuid
}

// createDirectory creates a plain directory under parentUUID, returning its UUID.
func createDirectory(t *testing.T, database *db.DB, slug, parentUUID string) string {
	t.Helper()
	container, err := store.New(database).Containers.Create(campaignFixtureActor,
		store.ContainerCreateParams{Slug: slug, Kind: "directory", ParentUUID: &parentUUID})
	if err != nil {
		t.Fatalf("create directory %s: %v", slug, err)
	}
	return container.UUID
}

// createCampaignFixtureTask creates a priority-2 task in containerUUID.
func createCampaignFixtureTask(t *testing.T, database *db.DB, slug, title, containerUUID string, state domain.State) *store.CreateResult {
	t.Helper()
	task, err := store.New(database).Tasks.Create(campaignFixtureActor, store.CreateParams{
		Slug: slug, Title: title, ProjectUUID: containerUUID, State: state, Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task %s: %v", slug, err)
	}
	return task
}

// openDB opens the fixture DB for direct seeding or readback; the caller closes it.
func (f campaignCLIFixture) openDB(t *testing.T) *db.DB {
	t.Helper()
	database, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatalf("open fixture DB: %v", err)
	}
	return database
}

// exec runs one statement against the fixture DB.
func (f campaignCLIFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	database := f.openDB(t)
	defer func() { _ = database.Close() }()
	if _, err := database.Exec(query, args...); err != nil {
		t.Fatalf("fixture exec %q: %v", query, err)
	}
}

// count runs a single-integer query against the fixture DB.
func (f campaignCLIFixture) count(t *testing.T, query string, args ...any) int64 {
	t.Helper()
	database := f.openDB(t)
	defer func() { _ = database.Close() }()
	var n int64
	if err := database.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("fixture query %q: %v", query, err)
	}
	return n
}

// enroll writes taskUUID's campaign enrollment directly, bypassing the CLI.
func (f campaignCLIFixture) enroll(t *testing.T, campaignUUID, taskUUID string) {
	t.Helper()
	f.exec(t, "UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", campaignUUID, taskUUID)
}

// campaignOf reads taskUUID's stored enrollment.
func (f campaignCLIFixture) campaignOf(t *testing.T, taskUUID string) sql.NullString {
	t.Helper()
	database := f.openDB(t)
	defer func() { _ = database.Close() }()
	var got sql.NullString
	if err := database.QueryRow("SELECT campaign_uuid FROM tasks WHERE uuid = ?", taskUUID).Scan(&got); err != nil {
		t.Fatalf("query task campaign: %v", err)
	}
	return got
}

// friendlyID reads a container's P- id.
func (f campaignCLIFixture) friendlyID(t *testing.T, containerUUID string) string {
	t.Helper()
	database := f.openDB(t)
	defer func() { _ = database.Close() }()
	var id string
	if err := database.QueryRow("SELECT id FROM containers WHERE uuid = ?", containerUUID).Scan(&id); err != nil {
		t.Fatalf("read campaign id: %v", err)
	}
	return id
}

// runCampaignCLI executes `wrkq <args>` against dbPath as agent:campaign-test
// and returns combined stdout+stderr.
func runCampaignCLI(t *testing.T, dbPath string, args ...string) (string, error) {
	t.Helper()
	return runCampaignCLIWith(dbPath, nil, args...)
}

// runCampaignCLIInput is runCampaignCLI with stdin supplied.
func runCampaignCLIInput(t *testing.T, dbPath, input string, args ...string) (string, error) {
	t.Helper()
	return runCampaignCLIWith(dbPath, strings.NewReader(input), args...)
}

// mustRun runs `wrkq <args>` against the fixture, failing the test on error.
func (f campaignCLIFixture) mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runCampaignCLI(t, f.dbPath, args...)
	if err != nil {
		t.Fatalf("wrkq %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func runCampaignCLIWith(dbPath string, stdin io.Reader, args ...string) (string, error) {
	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs(append([]string{"--db", dbPath, "--principal-ref", "agent:campaign-test"}, args...))
	if stdin != nil {
		cmd.SetIn(stdin)
	}
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	err := cmd.Execute()
	return output.String(), err
}
