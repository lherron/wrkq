package wrkqd

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/search/indexdb"
	"github.com/lherron/wrkq/internal/store"
)

// A real store commit must reach the background index before the old two-second tick.
func TestSearchIndexerCommitFreshness(t *testing.T) {
	database, path := setupTestEnv(t)
	cfg := &config.Config{DBPath: path}
	cfg.Search.Enabled = true
	cfg.Search.DBPath = filepath.Join(t.TempDir(), "search.sqlite")
	cfg.Search.DenseProvider = "none"
	s := &daemonServer{db: database, cfg: cfg}
	stop := s.startSearchIndexer()
	defer stop()
	idx, err := indexdb.Open(cfg.Search.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	task, err := store.New(database).Tasks.Create("00000000-0000-0000-0000-000000000001", store.CreateParams{
		ProjectUUID: "00000000-0000-0000-0000-000000000002", Slug: "commit-freshness", Title: "Commit freshness needle", State: "open", Priority: 2, Kind: "task",
	})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := idx.QueryRow("SELECT COUNT(*) FROM search_chunks WHERE task_uuid = ?", task.UUID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("committed task not indexed within 1 second without ticker")
}
