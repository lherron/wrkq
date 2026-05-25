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

func TestSearchSortUpdatedAtReverseAndTimestamps(t *testing.T) {
	canonical, actorUUID, containerUUID := setupSearchDB(t)

	tasks := []struct {
		uuid      string
		id        string
		slug      string
		title     string
		createdAt string
		updatedAt string
	}{
		{
			uuid:      "old-search-task-uuid",
			id:        "T-10001",
			slug:      "old-search-task",
			title:     "Old needle search task",
			createdAt: "2026-05-20T10:00:00Z",
			updatedAt: "2026-05-20T10:30:00Z",
		},
		{
			uuid:      "new-search-task-uuid",
			id:        "T-10002",
			slug:      "new-search-task",
			title:     "New needle search task",
			createdAt: "2026-05-21T10:00:00Z",
			updatedAt: "2026-05-25T12:00:00Z",
		},
	}

	for _, task := range tasks {
		if _, err := canonical.Exec(`
			INSERT INTO tasks (
				uuid, id, slug, title, project_uuid, state, priority, kind,
				description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag
			)
			VALUES (?, ?, ?, ?, ?, 'open', 2, 'task', 'needle body', ?, ?, ?, ?, 1)
		`, task.uuid, task.id, task.slug, task.title, containerUUID, task.createdAt, task.updatedAt, actorUUID, actorUUID); err != nil {
			t.Fatalf("insert task %s: %v", task.id, err)
		}
	}

	idx := setupSearchIndex(t)
	ix := indexer.New(canonical, idx, nil)
	if err := ix.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	svc := NewService(canonical, idx, nil)
	resp, err := svc.Search(context.Background(), Options{
		Query:   "needle",
		Limit:   10,
		Sort:    "updated_at",
		Reverse: true,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("expected 2 results, got %d: %#v", len(resp.Results), resp.Results)
	}
	if resp.Results[0].TaskUUID != "new-search-task-uuid" {
		t.Fatalf("expected newest updated task first, got %#v", resp.Results)
	}
	if resp.Results[0].CreatedAt != "2026-05-21T10:00:00Z" || resp.Results[0].UpdatedAt != "2026-05-25T12:00:00Z" {
		t.Fatalf("expected timestamps on result, got %#v", resp.Results[0])
	}

	relevanceResp, err := svc.Search(context.Background(), Options{Query: "needle", Limit: 10})
	if err != nil {
		t.Fatalf("relevance search: %v", err)
	}
	if len(relevanceResp.Results) != 2 {
		t.Fatalf("expected 2 relevance results, got %d", len(relevanceResp.Results))
	}
	if relevanceResp.Results[0].Score < relevanceResp.Results[1].Score {
		t.Fatalf("default relevance sort should remain descending, got %#v", relevanceResp.Results)
	}
}

func TestSearchNormalizesCommentTimestamps(t *testing.T) {
	canonical, actorUUID, containerUUID := setupSearchDB(t)

	if _, err := canonical.Exec(`
		INSERT INTO tasks (
			uuid, id, slug, title, project_uuid, state, priority, kind,
			description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag
		)
		VALUES (
			'comment-timestamp-task-uuid', 'T-10003', 'comment-timestamp-task',
			'Comment timestamp task', ?, 'open', 2, 'task',
			'needle parent', '2026-05-20T10:00:00Z', '2026-05-20T10:00:00Z', ?, ?, 1
		)
	`, containerUUID, actorUUID, actorUUID); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	if _, err := canonical.Exec(`
		INSERT INTO comments (
			uuid, id, task_uuid, actor_uuid, body, etag, created_at, updated_at
		)
		VALUES (
			'comment-timestamp-comment-uuid', 'C-10001', 'comment-timestamp-task-uuid',
			?, 'needle comment', 1, '2026-05-21 14:21:49', '2026-05-22 15:22:50'
		)
	`, actorUUID); err != nil {
		t.Fatalf("insert comment: %v", err)
	}

	idx := setupSearchIndex(t)
	ix := indexer.New(canonical, idx, nil)
	if err := ix.Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	svc := NewService(canonical, idx, nil)
	resp, err := svc.Search(context.Background(), Options{
		Query: "comment",
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	for _, result := range resp.Results {
		if result.CommentID == nil || *result.CommentID != "C-10001" {
			continue
		}
		if result.CreatedAt != "2026-05-21T14:21:49Z" || result.UpdatedAt != "2026-05-22T15:22:50Z" {
			t.Fatalf("expected normalized comment timestamps, got %#v", result)
		}
		return
	}
	t.Fatalf("expected comment result in %#v", resp.Results)
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
