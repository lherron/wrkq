//go:build wrkq_local

package workrpc_test

// rpc_seed_test.go — fixtures written straight to disk ahead of an RPC session:
// task rows inserted by SQL (for states the RPC surface cannot create directly)
// and scratch files.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// seedProjectUUID is the project container every seeded task lives in
// (slug "p2-test-proj").
const seedProjectUUID = "00000000-1111-4000-8000-000000000001"

// seedActorUUID is the wrkq-system actor created by the migrations.
const seedActorUUID = "00000000-0000-4000-8000-0000000000a0"

// taskSeed describes one task row to insert.
type taskSeed struct {
	uuid, slug, title string
	state             string // defaults to "open"
	stampColumn       string // optional timestamp column set to now (deleted_at, archived_at, acknowledged_at)
	parentUUID        string // optional parent_task_uuid
}

// seedTaskRow inserts the p2-test-proj project (once) and the described task,
// returning the task's wrkq ID (e.g. "T-00001").
func seedTaskRow(t *testing.T, dbPath string, s taskSeed) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("seedTaskRow: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	_, _ = database.Exec(
		`INSERT OR IGNORE INTO containers (uuid, slug, title, parent_uuid, kind,
		                                   created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'p2-test-proj', 'P2 Test Project',
		         (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		seedProjectUUID, seedActorUUID, seedActorUUID,
	)

	state := s.state
	if state == "" {
		state = "open"
	}
	columns := "uuid, slug, title, description, project_uuid, state, priority, kind, created_by_actor_uuid, updated_by_actor_uuid"
	values := "?, ?, ?, '', ?, ?, 2, 'task', ?, ?"
	args := []any{s.uuid, s.slug, s.title, seedProjectUUID, state, seedActorUUID, seedActorUUID}
	if s.stampColumn != "" {
		columns += ", " + s.stampColumn
		values += ", datetime('now')"
	}
	if s.parentUUID != "" {
		columns += ", parent_task_uuid"
		values += ", ?"
		args = append(args, s.parentUUID)
	}
	if _, err := database.Exec("INSERT OR IGNORE INTO tasks ("+columns+") VALUES ("+values+")", args...); err != nil {
		t.Fatalf("seedTaskRow: INSERT task %s: %v", s.slug, err)
	}

	var taskID string
	if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", s.uuid).Scan(&taskID); err != nil {
		t.Fatalf("seedTaskRow: fetch task id: %v", err)
	}
	return taskID
}

// p2SeedTask inserts an open task (and its project) directly into dbPath, for
// tests that need a persisted task without going through wrkq.task.create.
func p2SeedTask(t *testing.T, dbPath, taskUUID, slug, title string) string {
	t.Helper()
	return seedTaskRow(t, dbPath, taskSeed{uuid: taskUUID, slug: slug, title: title})
}

// p4WriteTempFile creates a file with the given name and content in t.TempDir().
func p4WriteTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("p4WriteTempFile: write %s: %v", path, err)
	}
	return path
}
