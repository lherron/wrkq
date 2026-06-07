package render

import (
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
)

func TestTaskChunksDeterministicHash(t *testing.T) {
	database, actorUUID, containerUUID := setupRenderDB(t)
	s := store.New(database)
	task, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:          "deterministic-task",
		Title:         "Deterministic task",
		Description:   "The rendered text should be stable.",
		Specification: "Search chunk hashes should not drift.",
		ProjectUUID:   containerUUID,
		State:         "open",
		Priority:      2,
		Kind:          "task",
		Labels:        `["search","test"]`,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	first, err := TaskChunks(database.DB, task.UUID)
	if err != nil {
		t.Fatalf("first render: %v", err)
	}
	second, err := TaskChunks(database.DB, task.UUID)
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("chunk count changed: %d != %d", len(first), len(second))
	}
	for i := range first {
		if first[i].ContentHash != second[i].ContentHash {
			t.Fatalf("hash drift for chunk %d: %s != %s", i, first[i].ContentHash, second[i].ContentHash)
		}
		if first[i].Body != second[i].Body {
			t.Fatalf("body drift for chunk %d", i)
		}
	}
}

func setupRenderDB(t *testing.T) (*db.DB, string, string) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "wrkq.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	res, err := database.Exec(`INSERT INTO actors (id, slug, role) VALUES ('', 'render-tester', 'human')`)
	if err != nil {
		t.Fatalf("insert actor: %v", err)
	}
	rowID, _ := res.LastInsertId()
	var actorUUID string
	if err := database.QueryRow(`SELECT uuid FROM actors WHERE rowid = ?`, rowID).Scan(&actorUUID); err != nil {
		t.Fatalf("actor uuid: %v", err)
	}

	s := store.New(database)
	container, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "inbox", Kind: "project"})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	return database, actorUUID, container.UUID
}
