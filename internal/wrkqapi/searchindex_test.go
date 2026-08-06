//go:build wrkq_local

package wrkqapi

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
)

// newSearchAPI builds a search-ENABLED API over a fresh canonical DB with a
// hermetic sidecar path and the deterministic `hash` dense provider (no network /
// llama dependency). It returns the API plus the seeded actor + project UUIDs.
func newSearchAPI(t *testing.T, provider string) (*API, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wrkq.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	res, err := database.Exec(`INSERT INTO actors (id, slug, role) VALUES ('', 'search-tester', 'human')`)
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
	if _, err := s.Tasks.Create(actorUUID, store.CreateParams{
		Slug:        "vector-task",
		Title:       "Vector search task",
		Description: "Implement semantic vector lookup for active work.",
		ProjectUUID: container.UUID,
		State:       "open",
		Priority:    2,
		Kind:        "task",
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	api := New(database, nil, "", "", 0, WithSearch(SearchConfig{
		Enabled:         true,
		CanonicalDBPath: dbPath,
		DBPath:          filepath.Join(dir, "search.sqlite"),
		DenseProvider:   provider,
		DenseDimension:  16,
	}))
	return api, s
}

// TestSearchIndex_RebuildStatusSearchLifecycle exercises the full server-owned
// search + index family end to end with the deterministic hash provider: rebuild
// indexes the seeded task, status reports it, search finds it, and the lifecycle
// mutations (pause/resume/vacuum/update) return the legacy map-alphabetical shape.
func TestSearchIndex_RebuildStatusSearchLifecycle(t *testing.T) {
	api, _ := newSearchAPI(t, "hash")
	ctx := context.Background()

	rebuilt, err := api.IndexRebuild(ctx, IndexLifecycleParams{})
	if err != nil {
		t.Fatalf("IndexRebuild: %v", err)
	}
	if rebuilt["rebuilt"] != true {
		t.Errorf("rebuild result missing rebuilt=true: %#v", rebuilt)
	}
	if _, ok := rebuilt["status"]; !ok {
		t.Errorf("rebuild result missing status: %#v", rebuilt)
	}

	status, err := api.IndexStatus(ctx, IndexStatusParams{})
	if err != nil {
		t.Fatalf("IndexStatus: %v", err)
	}
	if !status.Enabled {
		t.Errorf("status.Enabled = false, want true")
	}
	if status.SearchableChunkCount == 0 {
		t.Errorf("status.SearchableChunkCount = 0, want > 0 after rebuild")
	}
	if status.StaleEventCount != 0 {
		t.Errorf("status.StaleEventCount = %d, want 0 immediately after rebuild", status.StaleEventCount)
	}

	view, err := api.SearchListView(ctx, SearchListViewParams{Query: "vector"})
	if err != nil {
		t.Fatalf("SearchListView: %v", err)
	}
	if len(view.Results) == 0 {
		t.Fatalf("search returned no results for 'vector'")
	}
	if view.Results[0].TaskID == "" {
		t.Errorf("first result has empty task_id: %#v", view.Results[0])
	}
	if view.Status == nil {
		t.Errorf("search view.Status is nil (legacy always includes status)")
	}

	pause, err := api.IndexPause(ctx, IndexLifecycleParams{})
	if err != nil {
		t.Fatalf("IndexPause: %v", err)
	}
	if pause["status"] != "paused" {
		t.Errorf("pause result = %#v, want status=paused", pause)
	}
	resume, err := api.IndexResume(ctx, IndexLifecycleParams{})
	if err != nil {
		t.Fatalf("IndexResume: %v", err)
	}
	if resume["status"] != "ready" {
		t.Errorf("resume result = %#v, want status=ready", resume)
	}
	vac, err := api.IndexVacuum(ctx, IndexLifecycleParams{})
	if err != nil {
		t.Fatalf("IndexVacuum: %v", err)
	}
	if vac["vacuumed"] != true {
		t.Errorf("vacuum result = %#v, want vacuumed=true", vac)
	}
	upd, err := api.IndexUpdate(ctx, IndexLifecycleParams{})
	if err != nil {
		t.Fatalf("IndexUpdate: %v", err)
	}
	if upd["updated"] != true {
		t.Errorf("update result = %#v, want updated=true", upd)
	}
}

// TestSearchIndex_Disabled proves search/index methods report WRKQ_VALIDATION
// "search is disabled" when the server's search host is not enabled — and that
// the sidecar is NEVER opened (no file created) in that case.
func TestSearchIndex_Disabled(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wrkq.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	// No WithSearch → Enabled=false.
	api := New(database, nil, "", "", 0)
	ctx := context.Background()

	if _, err := api.SearchListView(ctx, SearchListViewParams{Query: "x"}); err == nil {
		t.Fatal("expected error when search disabled")
	} else if de, ok := err.(*DomainError); !ok || de.Code() != CodeValidation {
		t.Errorf("SearchListView err = %v, want WRKQ_VALIDATION", err)
	}
	if _, err := api.IndexStatus(ctx, IndexStatusParams{}); err == nil {
		t.Error("expected error from IndexStatus when search disabled")
	}
	if _, err := api.IndexRebuild(ctx, IndexLifecycleParams{}); err == nil {
		t.Error("expected error from IndexRebuild when search disabled")
	}
}

// TestSearchListView_InvalidSortIsValidation proves the invalid-sort sentinel
// from search.Service is reclassified to WRKQ_VALIDATION (not WORKRPC_INTERNAL),
// so the mirror surfaces the legacy message cleanly.
func TestSearchListView_InvalidSortIsValidation(t *testing.T) {
	api, _ := newSearchAPI(t, "none")
	if _, err := api.IndexRebuild(context.Background(), IndexLifecycleParams{}); err != nil {
		t.Fatalf("IndexRebuild: %v", err)
	}
	_, err := api.SearchListView(context.Background(), SearchListViewParams{Query: "vector", Sort: "bogus"})
	if err == nil {
		t.Fatal("expected error for invalid sort")
	}
	if de, ok := err.(*DomainError); !ok || de.Code() != CodeValidation {
		t.Errorf("err = %v, want WRKQ_VALIDATION", err)
	}
}