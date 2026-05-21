package cli

import (
	"context"
	"fmt"
	"os"
	"time"

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
