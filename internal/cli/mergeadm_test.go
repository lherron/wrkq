package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/attach"
	"github.com/lherron/wrkq/internal/db"
)

const (
	testActorUUID = "00000000-0000-0000-0000-000000000001"
	testActorID   = "A-00001"
)

func setupMergeDB(t *testing.T) (*db.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}
	_, err = database.Exec(`
		INSERT INTO actors (uuid, id, slug, display_name, role, created_at, updated_at)
		VALUES (?, ?, 'test-user', 'Test User', 'human', '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')
	`, testActorUUID, testActorID)
	if err != nil {
		t.Fatalf("failed to seed actor: %v", err)
	}
	t.Cleanup(func() {
		database.Close()
	})
	return database, path
}

func insertContainer(t *testing.T, database *db.DB, uuid, id, slug, title, parentUUID, updatedAt string) {
	t.Helper()
	_, err := database.Exec(`
		INSERT INTO containers (uuid, id, slug, title, description, parent_uuid, kind, sort_index, etag,
			created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, ?, ?, ?, '', ?, 'project', 0, 1, '2024-01-01T00:00:00Z', ?, ?, ?)
	`, uuid, id, slug, title, nullString(parentUUID), updatedAt, testActorUUID, testActorUUID)
	if err != nil {
		t.Fatalf("failed to insert container: %v", err)
	}
}

func insertTask(t *testing.T, database *db.DB, uuid, id, slug, title, projectUUID string) {
	t.Helper()
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, kind, description, etag,
			created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, ?, ?, ?, ?, 'open', 3, 'task', '', 1, '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z', ?, ?)
	`, uuid, id, slug, title, projectUUID, testActorUUID, testActorUUID)
	if err != nil {
		t.Fatalf("failed to insert task: %v", err)
	}
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func TestMergePathPrefixing(t *testing.T) {
	srcDB, _ := setupMergeDB(t)
	destDB, _ := setupMergeDB(t)

	projectUUID := "00000000-0000-0000-0000-000000000010"
	childUUID := "00000000-0000-0000-0000-000000000011"
	taskUUID := "00000000-0000-0000-0000-000000000012"

	insertContainer(t, srcDB, projectUUID, "P-00010", "proj", "Project", "", "2024-01-02T00:00:00Z")
	insertContainer(t, srcDB, childUUID, "P-00011", "child", "Child", projectUUID, "2024-01-02T00:00:00Z")
	insertTask(t, srcDB, taskUUID, "T-00010", "task-one", "Task One", childUUID)

	srcAttach := filepath.Join(t.TempDir(), "src-attach")
	destAttach := filepath.Join(t.TempDir(), "dest-attach")
	if err := os.MkdirAll(srcAttach, 0755); err != nil {
		t.Fatalf("failed to create attach dir: %v", err)
	}
	if err := os.MkdirAll(destAttach, 0755); err != nil {
		t.Fatalf("failed to create attach dir: %v", err)
	}

	report, err := mergeProjectIntoCanonical(mergeOptions{
		SourceDB:        srcDB,
		DestDB:          destDB,
		SourceAttachDir: srcAttach,
		DestAttachDir:   destAttach,
		ProjectSelector: "proj",
		PathPrefix:      "canonical",
		DryRun:          false,
		ActorUUID:       testActorUUID,
	})
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if report.DestPrefix != "canonical" {
		t.Fatalf("expected prefix canonical, got %s", report.DestPrefix)
	}

	var projectPath string
	if err := destDB.QueryRow("SELECT path FROM v_container_paths WHERE uuid = ?", projectUUID).Scan(&projectPath); err != nil {
		t.Fatalf("failed to load project path: %v", err)
	}
	if projectPath != "canonical" {
		t.Fatalf("expected project path canonical, got %s", projectPath)
	}

	var childPath string
	if err := destDB.QueryRow("SELECT path FROM v_container_paths WHERE uuid = ?", childUUID).Scan(&childPath); err != nil {
		t.Fatalf("failed to load child path: %v", err)
	}
	if childPath != "canonical/child" {
		t.Fatalf("expected child path canonical/child, got %s", childPath)
	}

	var taskPath string
	if err := destDB.QueryRow("SELECT path FROM v_task_paths WHERE uuid = ?", taskUUID).Scan(&taskPath); err != nil {
		t.Fatalf("failed to load task path: %v", err)
	}
	if taskPath != "canonical/child/task-one" {
		t.Fatalf("expected task path canonical/child/task-one, got %s", taskPath)
	}
}

func TestMergeUUIDConflictPrefersNewest(t *testing.T) {
	srcDB, _ := setupMergeDB(t)
	destDB, _ := setupMergeDB(t)

	projectUUID := "00000000-0000-0000-0000-000000000020"
	insertContainer(t, srcDB, projectUUID, "P-00020", "proj", "New Title", "", "2024-02-01T00:00:00Z")
	insertContainer(t, destDB, projectUUID, "P-99999", "proj", "Old Title", "", "2024-01-01T00:00:00Z")

	report, err := mergeProjectIntoCanonical(mergeOptions{
		SourceDB:        srcDB,
		DestDB:          destDB,
		SourceAttachDir: t.TempDir(),
		DestAttachDir:   t.TempDir(),
		ProjectSelector: "proj",
		PathPrefix:      "proj",
		DryRun:          false,
		ActorUUID:       testActorUUID,
	})
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if report.Stats.Containers.Updated == 0 {
		t.Fatalf("expected container update")
	}

	var title string
	if err := destDB.QueryRow("SELECT title FROM containers WHERE uuid = ?", projectUUID).Scan(&title); err != nil {
		t.Fatalf("failed to load container: %v", err)
	}
	if title != "New Title" {
		t.Fatalf("expected updated title, got %s", title)
	}
}

func TestMergeSlugCollisionRenames(t *testing.T) {
	srcDB, _ := setupMergeDB(t)
	destDB, _ := setupMergeDB(t)

	projectUUID := "00000000-0000-0000-0000-000000000030"
	childUUID := "00000000-0000-0000-0000-000000000031"
	destChildUUID := "00000000-0000-0000-0000-000000000032"

	insertContainer(t, srcDB, projectUUID, "P-00030", "proj", "Project", "", "2024-02-01T00:00:00Z")
	insertContainer(t, srcDB, childUUID, "P-00031", "child", "Child", projectUUID, "2024-02-01T00:00:00Z")

	insertContainer(t, destDB, projectUUID, "P-99990", "canonical", "Project", "", "2024-02-02T00:00:00Z")
	insertContainer(t, destDB, destChildUUID, "P-99991", "child", "Existing", projectUUID, "2024-02-02T00:00:00Z")

	report, err := mergeProjectIntoCanonical(mergeOptions{
		SourceDB:        srcDB,
		DestDB:          destDB,
		SourceAttachDir: t.TempDir(),
		DestAttachDir:   t.TempDir(),
		ProjectSelector: "proj",
		PathPrefix:      "canonical",
		DryRun:          false,
		ActorUUID:       testActorUUID,
	})
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if report.Stats.Containers.Renamed == 0 {
		t.Fatalf("expected container rename")
	}

	var slug string
	if err := destDB.QueryRow("SELECT slug FROM containers WHERE uuid = ?", childUUID).Scan(&slug); err != nil {
		t.Fatalf("failed to load renamed container: %v", err)
	}
	if slug != "child--dup-2" {
		t.Fatalf("expected renamed slug child--dup-2, got %s", slug)
	}
}

func TestMergeAttachmentCopy(t *testing.T) {
	srcDB, _ := setupMergeDB(t)
	destDB, _ := setupMergeDB(t)

	projectUUID := "00000000-0000-0000-0000-000000000040"
	taskUUID := "00000000-0000-0000-0000-000000000041"

	insertContainer(t, srcDB, projectUUID, "P-00040", "proj", "Project", "", "2024-02-01T00:00:00Z")
	insertTask(t, srcDB, taskUUID, "T-00040", "task-attach", "Task Attach", projectUUID)

	srcAttach := filepath.Join(t.TempDir(), "src-attach")
	destAttach := filepath.Join(t.TempDir(), "dest-attach")
	if err := os.MkdirAll(srcAttach, 0755); err != nil {
		t.Fatalf("failed to create src attach dir: %v", err)
	}
	if err := os.MkdirAll(destAttach, 0755); err != nil {
		t.Fatalf("failed to create dest attach dir: %v", err)
	}

	if err := attach.EnsureTaskDir(srcAttach, taskUUID); err != nil {
		t.Fatalf("failed to ensure task dir: %v", err)
	}
	filePath := filepath.Join(srcAttach, "tasks", taskUUID, "note.txt")
	if err := os.WriteFile(filePath, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write attachment: %v", err)
	}

	relPath := filepath.Join("tasks", taskUUID, "note.txt")
	_, err := srcDB.Exec(`
		INSERT INTO attachments (uuid, id, task_uuid, filename, relative_path, mime_type, size_bytes, checksum, created_at, created_by_actor_uuid)
		VALUES ('00000000-0000-0000-0000-000000000050', 'ATT-00001', ?, 'note.txt', ?, 'text/plain', 5, NULL, '2024-02-01T00:00:00Z', ?)
	`, taskUUID, relPath, testActorUUID)
	if err != nil {
		t.Fatalf("failed to insert attachment: %v", err)
	}

	report, err := mergeProjectIntoCanonical(mergeOptions{
		SourceDB:        srcDB,
		DestDB:          destDB,
		SourceAttachDir: srcAttach,
		DestAttachDir:   destAttach,
		ProjectSelector: "proj",
		PathPrefix:      "proj",
		DryRun:          false,
		ActorUUID:       testActorUUID,
	})
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if report.Stats.Attachments.Created == 0 {
		t.Fatalf("expected attachment created")
	}

	destFile := filepath.Join(destAttach, relPath)
	if _, err := os.Stat(destFile); err != nil {
		t.Fatalf("expected attachment file copied: %v", err)
	}
}

func TestMergeDryRunNoWrite(t *testing.T) {
	srcDB, _ := setupMergeDB(t)
	destDB, _ := setupMergeDB(t)

	projectUUID := "00000000-0000-0000-0000-000000000060"
	insertContainer(t, srcDB, projectUUID, "P-00060", "proj", "Project", "", "2024-02-01T00:00:00Z")

	_, err := mergeProjectIntoCanonical(mergeOptions{
		SourceDB:        srcDB,
		DestDB:          destDB,
		SourceAttachDir: t.TempDir(),
		DestAttachDir:   t.TempDir(),
		ProjectSelector: "proj",
		PathPrefix:      "proj",
		DryRun:          true,
		ActorUUID:       testActorUUID,
	})
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}

	var count int
	if err := destDB.QueryRow("SELECT COUNT(*) FROM containers WHERE uuid = ?", projectUUID).Scan(&count); err != nil {
		t.Fatalf("failed to query dest container: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no containers written in dry-run")
	}
}
