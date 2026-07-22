package wrkqd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/search/embed"
	"github.com/lherron/wrkq/internal/search/indexdb"
	"github.com/lherron/wrkq/internal/search/indexer"
)

func (s *daemonServer) startSearchIndexer() {
	if s.cfg == nil || !s.cfg.Search.Enabled {
		return
	}
	idx, err := openSearchIndex(s.cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "search indexer disabled: %v\n", err)
		return
	}

	ix := indexer.New(s.db, idx, denseEmbedderFromConfig(s.cfg))
	ix.BatchSize = s.cfg.Search.IndexBatchSize
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			<-ticker.C
			status, _, _ := idx.State("status")
			if status == "paused" {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			err := ix.IndexPending(ctx)
			cancel()
			if err != nil {
				idx.RecordFailure(0, "", "background_index", err)
			}
		}
	}()
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
