//go:build wrkq_local

package rpccli

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
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

	archiveOut := mustContainerLifecycleCLI(t, f.dbPath, "--project", "archive-roundtrip", "archive", "archive-roundtrip", "--if-match", fmt.Sprint(f.project.ETag))
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
