package wrkqd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/search/embed"
	"github.com/lherron/wrkq/internal/search/indexdb"
	"github.com/lherron/wrkq/internal/search/indexer"
)

// startSearchIndexer catches up on startup, then sleeps until a local commit,
// resume, or the safety tick for writers outside this process. The returned
// stop function joins the worker before the daemon closes its canonical DB.
func (s *daemonServer) startSearchIndexer() func() {
	if s.cfg == nil || !s.cfg.Search.Enabled {
		return func() {}
	}
	idx, err := openSearchIndex(s.cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "search indexer disabled: %v\n", err)
		return func() {}
	}
	ix := indexer.New(s.db, idx, denseEmbedderFromConfig(s.cfg))
	ix.BatchSize = s.cfg.Search.IndexBatchSize
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = idx.Close() }()
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		runSearchIndexer(ctx, s.db, ticker.C, func(parent context.Context) bool {
			status, _, _ := idx.State("status")
			if status == "paused" {
				return true
			}
			batchCtx, batchCancel := context.WithTimeout(parent, 2*time.Minute)
			err := ix.IndexPending(batchCtx)
			batchCancel()
			if err != nil && parent.Err() == nil {
				idx.RecordFailure(0, "", "background_index", err)
			}
			return false
		})
	}()
	return func() { cancel(); <-done }
}

// runSearchIndexer snapshots before each pass so commits during indexing stay
// pending. No per-event queue is retained. Paused workers ignore commits and
// only reread status on resume or a safety tick.
func runSearchIndexer(ctx context.Context, database *db.DB, safety <-chan time.Time, run func(context.Context) bool) {
	for {
		if ctx.Err() != nil {
			return
		}
		_, commits := database.EventCommits.Snapshot()
		_, resumes := database.SearchResumes.Snapshot()
		if run(ctx) {
			commits = nil
		}
		select {
		case <-ctx.Done():
			return
		case <-commits:
		case <-resumes:
		case <-safety:
		}
	}
}

func openSearchIndex(cfg *config.Config) (*indexdb.DB, error) {
	if !cfg.Search.Enabled {
		return nil, fmt.Errorf("search is disabled")
	}
	path := cfg.Search.DBPath
	if path == "" {
		path = indexdb.DefaultPath(cfg.DBPath)
	}
	return indexdb.Open(path)
}

func denseEmbedderFromConfig(cfg *config.Config) embed.DenseEmbedder {
	switch strings.ToLower(cfg.Search.DenseProvider) {
	case "", "llama-cpp":
		return &embed.LlamaCPP{
			BaseURL:          cfg.Search.DenseBaseURL,
			Model:            cfg.Search.DenseModel,
			DimensionValue:   cfg.Search.DenseDimension,
			QueryInstruction: cfg.Search.QueryInstruction,
		}
	case "hash":
		return embed.HashEmbedder{Model: "wrkq-hash-test", Dims: cfg.Search.DenseDimension}
	case "none", "off", "disabled":
		return nil
	default:
		return nil
	}
}
