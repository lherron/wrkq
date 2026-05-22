package search

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/search/embed"
	"github.com/lherron/wrkq/internal/search/indexdb"
	"github.com/lherron/wrkq/internal/search/indexer"
	"github.com/lherron/wrkq/internal/store"
)

func TestSearchRebuildFTSDenseAndStateFiltering(t *testing.T) {
	canonical, actorUUID, containerUUID := setupSearchDB(t)
	s := store.New(canonical)

	openTask, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:          "open-vector-task",
		Title:         "Open vector search task",
		Description:   "Implement semantic vector lookup for active work.",
		Specification: "Dense embeddings should use qwen.",
		ProjectUUID:   containerUUID,
		State:         "open",
		Priority:      2,
		Kind:          "task",
	})
	if err != nil {
		t.Fatalf("create open task: %v", err)
	}

	archivedTask, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:        "archived-vector-task",
		Title:       "Archived vector search task",
		Description: "Old vector design notes.",
		ProjectUUID: containerUUID,
		State:       "archived",
		Priority:    3,
		Kind:        "task",
	})
	if err != nil {
		t.Fatalf("create archived task: %v", err)
	}

	idx := setupSearchIndex(t)
	emb := embed.HashEmbedder{Dims: 16}
	ix := indexer.New(canonical, idx, emb)
	if err := ix.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	var denseCount int
	if err := idx.QueryRow(`SELECT COUNT(*) FROM search_dense_vec`).Scan(&denseCount); err != nil {
		t.Fatalf("dense table query failed: %v", err)
	}
	if denseCount == 0 {
		t.Fatal("expected dense vectors to be indexed")
	}

	svc := NewService(canonical, idx, emb)
	denseCandidates, err := svc.denseCandidates(context.Background(), "semantic vector lookup", 10)
	if err != nil {
		t.Fatalf("dense candidates failed: %v", err)
	}
	if len(denseCandidates) == 0 {
		t.Fatal("expected dense candidates")
	}

	resp, err := svc.Search(context.Background(), Options{Query: "vector", Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected 1 default open result, got %d: %#v", len(resp.Results), resp.Results)
	}
	if resp.Results[0].TaskUUID != openTask.UUID {
		t.Fatalf("expected open task %s, got %s", openTask.UUID, resp.Results[0].TaskUUID)
	}

	resp, err = svc.Search(context.Background(), Options{Query: "vector", State: "all", Limit: 10})
	if err != nil {
		t.Fatalf("search all: %v", err)
	}
	seen := map[string]bool{}
	for _, result := range resp.Results {
		seen[result.TaskUUID] = true
	}
	if !seen[openTask.UUID] || !seen[archivedTask.UUID] {
		t.Fatalf("expected open and archived tasks in --state all results, got %#v", resp.Results)
	}
}

func TestSearchFreshFailsWhenIndexIsStale(t *testing.T) {
	canonical, actorUUID, containerUUID := setupSearchDB(t)
	s := store.New(canonical)
	if _, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:        "first-task",
		Title:       "First task",
		Description: "searchable text",
		ProjectUUID: containerUUID,
		State:       "open",
		Priority:    2,
		Kind:        "task",
	}); err != nil {
		t.Fatalf("create first task: %v", err)
	}

	idx := setupSearchIndex(t)
	emb := embed.HashEmbedder{Dims: 16}
	ix := indexer.New(canonical, idx, emb)
	if err := ix.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	if _, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:        "second-task",
		Title:       "Second task",
		Description: "advances event log",
		ProjectUUID: containerUUID,
		State:       "open",
		Priority:    2,
		Kind:        "task",
	}); err != nil {
		t.Fatalf("create second task: %v", err)
	}

	svc := NewService(canonical, idx, emb)
	resp, err := svc.Search(context.Background(), Options{Query: "searchable"})
	if err != nil {
		t.Fatalf("stale search should still work by default: %v", err)
	}
	if !resp.Stale {
		t.Fatal("expected stale response after canonical event log advanced")
	}
	if _, err := svc.Search(context.Background(), Options{Query: "searchable", Fresh: true}); err == nil {
		t.Fatal("expected --fresh search to fail when index is stale")
	}
}

func setupSearchDB(t *testing.T) (*db.DB, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wrkq.db")
	canonical, err := db.Open(path)
	if err != nil {
		t.Fatalf("open canonical db: %v", err)
	}
	if err := canonical.Migrate(); err != nil {
		t.Fatalf("migrate canonical db: %v", err)
	}
	t.Cleanup(func() { _ = canonical.Close() })

	res, err := canonical.Exec(`INSERT INTO actors (id, slug, role) VALUES ('', 'search-tester', 'human')`)
	if err != nil {
		t.Fatalf("insert actor: %v", err)
	}
	rowID, _ := res.LastInsertId()
	var actorUUID string
	if err := canonical.QueryRow(`SELECT uuid FROM actors WHERE rowid = ?`, rowID).Scan(&actorUUID); err != nil {
		t.Fatalf("actor uuid: %v", err)
	}

	s := store.New(canonical)
	container, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "inbox"})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	return canonical, actorUUID, container.UUID
}

func setupSearchIndex(t *testing.T) *indexdb.DB {
	t.Helper()
	idx, err := indexdb.Open(filepath.Join(t.TempDir(), "search.sqlite"))
	if err != nil {
		t.Fatalf("open search index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	return idx
}
