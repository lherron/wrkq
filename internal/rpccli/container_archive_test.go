//go:build wrkq_local

package rpccli

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/store"
)

const containerLifecycleActorUUID = "00000000-0000-4000-8000-0000000000a0"

type containerLifecycleFixture struct {
	db      *db.DB
	dbPath  string
	project store.ContainerCreateResult
	child   store.ContainerCreateResult
	taskID  string
}

func newContainerLifecycleFixture(t *testing.T, slug string, withContents bool) containerLifecycleFixture {
	t.Helper()
	hookCatalog := filepath.Join(t.TempDir(), "empty-hook-catalog.json")
	if err := os.WriteFile(hookCatalog, []byte(`{"schemaVersion":"wrkf.hook-catalog.v0","hooks":{}}`), 0o600); err != nil {
		t.Fatalf("write hook catalog: %v", err)
	}
	t.Setenv("WRKF_HOOK_CATALOG", hookCatalog)

	dbPath := filepath.Join(t.TempDir(), "wrkq.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		_ = database.Close()
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	s := store.New(database)
	project, err := s.Containers.Create(containerLifecycleActorUUID, store.ContainerCreateParams{Slug: slug, Kind: "project"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	f := containerLifecycleFixture{db: database, dbPath: dbPath, project: *project}
	if !withContents {
		return f
	}
	child, err := s.Containers.Create(containerLifecycleActorUUID, store.ContainerCreateParams{
		Slug: slug + "-inbox", Kind: "directory", ParentUUID: &project.UUID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	task, err := s.Tasks.Create(containerLifecycleActorUUID, store.CreateParams{
		Slug: "open-work", Title: "Open work", ProjectUUID: child.UUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	f.child = *child
	f.taskID = task.ID
	return f
}

func runContainerLifecycleCLI(t *testing.T, dbPath, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs(append([]string{"--db", dbPath, "--principal-ref", "agent:container-lifecycle-test"}, args...))
	cmd.SetIn(strings.NewReader(stdin))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func mustContainerLifecycleCLI(t *testing.T, dbPath string, args ...string) string {
	t.Helper()
	stdout, stderr, err := runContainerLifecycleCLI(t, dbPath, "", args...)
	if err != nil {
		t.Fatalf("wrkq %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout, stderr)
	}
	return stdout
}

func TestContainerArchiveNonEmptyProjectRoundTrip(t *testing.T) {
	f := newContainerLifecycleFixture(t, "archive-roundtrip", true)

	before := mustContainerLifecycleCLI(t, f.dbPath, "projects", "--one")
	if !strings.Contains(before, "archive-roundtrip") {
		t.Fatalf("project missing before archive: %q", before)
	}

	archiveOut := mustContainerLifecycleCLI(t, f.dbPath, "--project", "archive-roundtrip", "archive", "archive-roundtrip", "--if-match", fmt.Sprint(f.project.ETag), "--yes")
	if !strings.Contains(archiveOut, `"archived": true`) || !strings.Contains(archiveOut, `"path": "archive-roundtrip"`) {
		t.Fatalf("archive output does not identify mutation: %s", archiveOut)
	}

	var archivedAt sql.NullString
	if err := f.db.QueryRow("SELECT archived_at FROM containers WHERE uuid = ?", f.project.UUID).Scan(&archivedAt); err != nil {
		t.Fatalf("read archived project: %v", err)
	}
	if !archivedAt.Valid || archivedAt.String == "" {
		t.Fatalf("project archived_at = %#v, want timestamp", archivedAt)
	}
	for table, uuid := range map[string]string{"containers": f.child.UUID, "tasks": taskUUIDByID(t, f.db, f.taskID)} {
		var count int
		if err := f.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE uuid = ?", uuid).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("archive deleted %s row %s", table, uuid)
		}
	}

	defaultProjects := mustContainerLifecycleCLI(t, f.dbPath, "projects", "--one")
	if strings.Contains(defaultProjects, "archive-roundtrip") {
		t.Fatalf("archived project remained in default listing: %q", defaultProjects)
	}
	allProjects := mustContainerLifecycleCLI(t, f.dbPath, "projects", "--all", "--one")
	if !strings.Contains(allProjects, "archive-roundtrip") {
		t.Fatalf("archived project missing from --all listing: %q", allProjects)
	}

	var payload string
	if err := f.db.QueryRow(`SELECT payload FROM event_log WHERE resource_uuid = ? AND event_type = 'container.archived' ORDER BY id DESC LIMIT 1`, f.project.UUID).Scan(&payload); err != nil {
		t.Fatalf("read archive event: %v", err)
	}
	if !strings.Contains(payload, `"soft_delete":true`) {
		t.Fatalf("archive event payload = %q, want soft_delete=true", payload)
	}

	unarchiveOut := mustContainerLifecycleCLI(t, f.dbPath, "--project", "archive-roundtrip", "unarchive", "archive-roundtrip")
	if !strings.Contains(unarchiveOut, `"unarchived": true`) || !strings.Contains(unarchiveOut, f.project.UUID) {
		t.Fatalf("unarchive output does not identify mutation: %s", unarchiveOut)
	}
	if err := f.db.QueryRow("SELECT archived_at FROM containers WHERE uuid = ?", f.project.UUID).Scan(&archivedAt); err != nil {
		t.Fatalf("read unarchived project: %v", err)
	}
	if archivedAt.Valid {
		t.Fatalf("project archived_at after unarchive = %#v, want NULL", archivedAt)
	}
	if got := mustContainerLifecycleCLI(t, f.dbPath, "projects", "--one"); !strings.Contains(got, "archive-roundtrip") {
		t.Fatalf("unarchived project missing from default listing: %q", got)
	}
}

func TestContainerArchiveCascadeRoundTripAndIdempotentRepair(t *testing.T) {
	f := newContainerLifecycleFixture(t, "archive-cascade", false)
	s := store.New(f.db)
	levelOne, err := s.Containers.Create(containerLifecycleActorUUID, store.ContainerCreateParams{
		Slug: "level-one", Kind: "directory", ParentUUID: &f.project.UUID,
	})
	if err != nil {
		t.Fatalf("create level one: %v", err)
	}
	levelTwo, err := s.Containers.Create(containerLifecycleActorUUID, store.ContainerCreateParams{
		Slug: "level-two", Kind: "directory", ParentUUID: &levelOne.UUID,
	})
	if err != nil {
		t.Fatalf("create level two: %v", err)
	}

	live := map[string]string{}
	var residentParentUUID string
	for i, state := range []string{"idea", "draft", "open", "in_progress", "blocked"} {
		containerUUID := levelOne.UUID
		if i%2 == 0 {
			containerUUID = levelTwo.UUID
		}
		task := createContainerLifecycleTask(t, s, containerUUID, "live-"+state, state, nil)
		live[task.UUID] = state
		if state == "open" {
			residentParentUUID = task.UUID
		}
	}
	externalProject, err := s.Containers.Create(containerLifecycleActorUUID, store.ContainerCreateParams{Slug: "archive-cascade-external", Kind: "project"})
	if err != nil {
		t.Fatalf("create external project: %v", err)
	}
	externalChild, err := s.Tasks.Create(containerLifecycleActorUUID, store.CreateParams{
		Slug: "external-child", Title: "external-child", ProjectUUID: externalProject.UUID,
		State: "open", Priority: 2, Kind: "subtask", ParentTaskUUID: &residentParentUUID,
	})
	if err != nil {
		t.Fatalf("create cross-residency child: %v", err)
	}
	originalMeta := `{"owner":"human"}`
	preCancelled := createContainerLifecycleTask(t, s, levelTwo.UUID, "pre-cancelled", "cancelled", &originalMeta)
	terminal := map[string]string{}
	for _, state := range []string{"completed", "archived", "deleted"} {
		task := createContainerLifecycleTask(t, s, levelTwo.UUID, "terminal-"+state, state, nil)
		terminal[task.UUID] = state
	}

	stdout, stderr, err := runContainerLifecycleCLI(t, f.dbPath, "y\n", "--project", "archive-cascade", "archive", "archive-cascade")
	if err != nil {
		t.Fatalf("archive cascade: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	for _, want := range []string{"5 live task(s) will be cancelled", "Archive container(s) and cancel", `"tasks_cancelled": 5`} {
		if !strings.Contains(stdout+stderr, want) {
			t.Errorf("archive report missing %q:\nstdout:\n%s\nstderr:\n%s", want, stdout, stderr)
		}
	}
	for uuid, priorState := range live {
		state, meta := taskStateAndMeta(t, f.db, uuid)
		if state != "cancelled" {
			t.Errorf("live task %s state = %s, want cancelled", uuid, state)
		}
		assertArchiveMarker(t, meta, f.project.UUID, priorState)
	}
	assertTaskStateAndMeta(t, f.db, preCancelled.UUID, "cancelled", originalMeta)
	for uuid, state := range terminal {
		assertTaskStateAndMeta(t, f.db, uuid, state, "")
	}
	assertTaskStateAndMeta(t, f.db, externalChild.UUID, "open", "")

	straggler := createContainerLifecycleTask(t, s, levelTwo.UUID, "late-straggler", "blocked", nil)
	stdout, stderr, err = runContainerLifecycleCLI(t, f.dbPath, "", "--project", "archive-cascade", "archive", "archive-cascade", "--yes")
	if err != nil {
		t.Fatalf("re-archive with straggler: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stderr, "1 live task(s) will be cancelled") || !strings.Contains(stdout, `"tasks_cancelled": 1`) {
		t.Fatalf("re-archive count mismatch:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	stragglerState, stragglerMeta := taskStateAndMeta(t, f.db, straggler.UUID)
	if stragglerState != "cancelled" {
		t.Fatalf("re-archive straggler state = %s, want cancelled", stragglerState)
	}
	assertArchiveMarker(t, stragglerMeta, f.project.UUID, "blocked")

	mustContainerLifecycleCLI(t, f.dbPath, "--project", "archive-cascade", "unarchive", "archive-cascade")
	for uuid, priorState := range live {
		assertTaskStateAndMeta(t, f.db, uuid, priorState, "")
	}
	assertTaskStateAndMeta(t, f.db, straggler.UUID, "blocked", "")
	assertTaskStateAndMeta(t, f.db, preCancelled.UUID, "cancelled", originalMeta)
	for uuid, state := range terminal {
		assertTaskStateAndMeta(t, f.db, uuid, state, "")
	}
	assertTaskStateAndMeta(t, f.db, externalChild.UUID, "open", "")
}

func TestContainerArchiveCascadeIsAtomicOnInvalidTaskMeta(t *testing.T) {
	f := newContainerLifecycleFixture(t, "archive-atomic", false)
	s := store.New(f.db)
	first := createContainerLifecycleTask(t, s, f.project.UUID, "first", "open", nil)
	second := createContainerLifecycleTask(t, s, f.project.UUID, "second", "blocked", nil)
	if _, err := f.db.Exec("UPDATE tasks SET meta = '{broken' WHERE uuid = ?", second.UUID); err != nil {
		t.Fatalf("seed invalid meta: %v", err)
	}

	_, _, err := runContainerLifecycleCLI(t, f.dbPath, "", "--project", "archive-atomic", "archive", "archive-atomic", "--yes")
	if err == nil || !strings.Contains(err.Error(), "invalid meta") {
		t.Fatalf("archive with invalid task meta error = %v, want invalid meta", err)
	}
	assertTaskStateAndMeta(t, f.db, first.UUID, "open", "")
	state, meta := taskStateAndMeta(t, f.db, second.UUID)
	if state != "blocked" || meta != "{broken" {
		t.Fatalf("invalid-meta task mutated to state=%s meta=%q", state, meta)
	}
	var archivedAt sql.NullString
	if err := f.db.QueryRow("SELECT archived_at FROM containers WHERE uuid = ?", f.project.UUID).Scan(&archivedAt); err != nil {
		t.Fatalf("read project archived_at: %v", err)
	}
	if archivedAt.Valid {
		t.Fatalf("failed atomic archive left archived_at=%s", archivedAt.String)
	}
}

func TestContainerArchiveRequiresReportedConfirmation(t *testing.T) {
	f := newContainerLifecycleFixture(t, "archive-confirm", true)
	_, stderr, err := runContainerLifecycleCLI(t, f.dbPath, "", "--project", "archive-confirm", "archive", "archive-confirm")
	if err == nil || err.Error() != "aborted" {
		t.Fatalf("archive without confirmation error = %v, want aborted", err)
	}
	for _, want := range []string{"1 live task(s) will be cancelled", "Archive container(s) and cancel"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("archive confirmation missing %q: %s", want, stderr)
		}
	}
	assertTaskStateAndMeta(t, f.db, taskUUIDByID(t, f.db, f.taskID), "open", "")
	var archivedAt sql.NullString
	if err := f.db.QueryRow("SELECT archived_at FROM containers WHERE uuid = ?", f.project.UUID).Scan(&archivedAt); err != nil {
		t.Fatalf("read project archived_at: %v", err)
	}
	if archivedAt.Valid {
		t.Fatalf("aborted archive set archived_at=%s", archivedAt.String)
	}
}

func TestRmdirNonEmptyErrorNamesArchiveAndDestructiveForce(t *testing.T) {
	f := newContainerLifecycleFixture(t, "rmdir-nonempty", true)
	_, _, err := runContainerLifecycleCLI(t, f.dbPath, "", "--project", "rmdir-nonempty", "rmdir", "rmdir-nonempty")
	if err == nil {
		t.Fatal("rmdir of non-empty project unexpectedly succeeded")
	}
	for _, want := range []string{"container is not empty", "wrkq archive", "wrkq rmdir --force", "permanently cascade-delete"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("rmdir error missing %q: %v", want, err)
		}
	}
	assertContainerExists(t, f.db, f.project.UUID)
	assertContainerExists(t, f.db, f.child.UUID)
}

func TestRmdirProjectAlwaysRequiresTypedYes(t *testing.T) {
	t.Run("plain empty project ignores yes flag", func(t *testing.T) {
		f := newContainerLifecycleFixture(t, "rmdir-empty-project", false)
		_, stderr, err := runContainerLifecycleCLI(t, f.dbPath, "", "--project", "rmdir-empty-project", "rmdir", "rmdir-empty-project", "--yes")
		assertProjectConfirmationAbort(t, stderr, err)
		assertContainerExists(t, f.db, f.project.UUID)

		if _, _, err := runContainerLifecycleCLI(t, f.dbPath, "yes\n", "--project", "rmdir-empty-project", "rmdir", "rmdir-empty-project", "--yes"); err != nil {
			t.Fatalf("typed yes did not delete empty project: %v", err)
		}
		assertContainerMissing(t, f.db, f.project.UUID)
	})

	t.Run("forced non-empty project ignores yes flag", func(t *testing.T) {
		f := newContainerLifecycleFixture(t, "rmdir-force-project", true)
		_, stderr, err := runContainerLifecycleCLI(t, f.dbPath, "", "--project", "rmdir-force-project", "rmdir", "rmdir-force-project", "--force", "--yes")
		assertProjectConfirmationAbort(t, stderr, err)
		assertContainerExists(t, f.db, f.project.UUID)
		assertContainerExists(t, f.db, f.child.UUID)

		if _, _, err := runContainerLifecycleCLI(t, f.dbPath, "yes\n", "--project", "rmdir-force-project", "rmdir", "rmdir-force-project", "--force", "--yes"); err != nil {
			t.Fatalf("typed yes did not force-delete project: %v", err)
		}
		assertContainerMissing(t, f.db, f.project.UUID)
		assertContainerMissing(t, f.db, f.child.UUID)
	})
}

func taskUUIDByID(t *testing.T, database *db.DB, id string) string {
	t.Helper()
	var uuid string
	if err := database.QueryRow("SELECT uuid FROM tasks WHERE id = ?", id).Scan(&uuid); err != nil {
		t.Fatalf("resolve task %s: %v", id, err)
	}
	return uuid
}

func createContainerLifecycleTask(t *testing.T, s *store.Store, containerUUID, slug, state string, meta *string) store.CreateResult {
	t.Helper()
	task, err := s.Tasks.Create(containerLifecycleActorUUID, store.CreateParams{
		Slug: slug, Title: slug, ProjectUUID: containerUUID, State: domain.State(state), Priority: 2, Meta: meta,
	})
	if err != nil {
		t.Fatalf("create task %s: %v", slug, err)
	}
	return *task
}

func taskStateAndMeta(t *testing.T, database *db.DB, uuid string) (string, string) {
	t.Helper()
	var state string
	var meta sql.NullString
	if err := database.QueryRow("SELECT state, meta FROM tasks WHERE uuid = ?", uuid).Scan(&state, &meta); err != nil {
		t.Fatalf("read task %s: %v", uuid, err)
	}
	return state, meta.String
}

func assertTaskStateAndMeta(t *testing.T, database *db.DB, uuid, wantState, wantMeta string) {
	t.Helper()
	state, meta := taskStateAndMeta(t, database, uuid)
	if state != wantState || meta != wantMeta {
		t.Errorf("task %s = state %s meta %q, want state %s meta %q", uuid, state, meta, wantState, wantMeta)
	}
}

func assertArchiveMarker(t *testing.T, rawMeta, containerUUID, priorState string) {
	t.Helper()
	var meta map[string]json.RawMessage
	if err := json.Unmarshal([]byte(rawMeta), &meta); err != nil {
		t.Fatalf("decode marked meta %q: %v", rawMeta, err)
	}
	var marker struct {
		Version        int    `json:"version"`
		ContainerUUID  string `json:"container_uuid"`
		ArchiveEventID int64  `json:"archive_event_id"`
		PriorState     string `json:"prior_state"`
	}
	if err := json.Unmarshal(meta["_wrkq_archive_cascade"], &marker); err != nil {
		t.Fatalf("decode archive marker from %q: %v", rawMeta, err)
	}
	if marker.Version != 1 || marker.ContainerUUID != containerUUID || marker.ArchiveEventID <= 0 || marker.PriorState != priorState {
		t.Errorf("archive marker = %#v, want container=%s prior=%s with event", marker, containerUUID, priorState)
	}
}

func assertProjectConfirmationAbort(t *testing.T, stderr string, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("project rmdir unexpectedly succeeded without typed yes")
	}
	for _, want := range []string{"--yes does not bypass confirmation for projects", "wrkq archive", "reversible"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("project abort missing %q: %v", want, err)
		}
	}
	if !strings.Contains(stderr, "WARNING: This will permanently delete") {
		t.Errorf("project confirmation stderr missing warning: %q", stderr)
	}
}

func assertContainerExists(t *testing.T, database *db.DB, uuid string) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM containers WHERE uuid = ?", uuid).Scan(&count); err != nil {
		t.Fatalf("count container %s: %v", uuid, err)
	}
	if count != 1 {
		t.Fatalf("container %s missing", uuid)
	}
}

func assertContainerMissing(t *testing.T, database *db.DB, uuid string) {
	t.Helper()
	var count int
	if err := database.QueryRow("SELECT COUNT(*) FROM containers WHERE uuid = ?", uuid).Scan(&count); err != nil {
		t.Fatalf("count container %s: %v", uuid, err)
	}
	if count != 0 {
		t.Fatalf("container %s still exists", uuid)
	}
}
