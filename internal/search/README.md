# Search Index Operations

## Search index (FTS5 + dense vectors)

- Sidecar SQLite at `<canonical>.search.sqlite`. Built and queried independently from the canonical DB. Drop and rebuild is non-destructive.
- Indexes three resource types: `task`, `comment`, `handoff`. `wrkq search` queries tasks+comments; `wrkq handoff search` queries handoffs through the same engine. Both go through `internal/search/search.go`.
- `search_chunks` schema is at version `2` (`internal/search/indexdb/db.go`). On version mismatch the chunk/FTS tables are auto-dropped and the indexer resets `last_indexed_event_id` to `0` so the next `wrkq index update` or `wrkq handoff search` lazily rebuilds.
- **FTS5 requires the `sqlite_fts5` build tag.** `just build` already passes it. Without the tag, queries silently degrade to a LIKE-based fallback on `search_fts_plain` — works, but no bm25 ranking.
- Lifecycle commands: `wrkq index status`, `wrkq index rebuild [--foreground]`, `wrkq index update`, `wrkq index vacuum`, `wrkq index pause`, `wrkq index resume`.
- `wrkq handoff search` calls `IndexPending` automatically before each query so freshly created/acked handoffs show up without a manual rebuild.

### Dense embeddings — llama-server (`internal/search/embed/embed.go`)

The dense provider defaults to `llama-cpp` at `http://127.0.0.1:8080` (see `cfg.Search.Dense*`). The configured model is `Qwen/Qwen3-Embedding-8B-GGUF:Q4_K_M`, dim 4096. Operating notes from this codebase's experience:

- **`--pooling last` reliably crashes llama.cpp 9260 with this model** (`EXC_BREAKPOINT` in `llama_context::decode`, malloc heap-protection trap). Use **`--pooling mean`** instead. Crash reports land in `~/Library/Logs/DiagnosticReports/llama-server-*.ips`.
- **`--parallel N>1` also crashes** under the same model+quant. Single slot is stable.
- **`--ubatch-size 512` (the default) is too small.** wrkq chunks truncate at 4000 chars (~1000 tokens). llama-server returns 500 `input (N tokens) is too large to process. increase the physical batch size` if input exceeds the physical batch. Set `--batch-size 4096 --ubatch-size 4096` to cover the largest realistic chunk.
- Set **`WRKQ_SEARCH_INDEX_BATCH_SIZE=1`** when running a full rebuild against this model+quant to send one document per HTTP call. Multi-doc batches risk exceeding `--ctx-size`.
- **`--cache-ram 0`** disables prompt cache. Helpful for memory-constrained or batch-only workloads; the cache doesn't help indexing throughput.
- **`WRKQ_SEARCH_DENSE_PROVIDER=none`** disables dense indexing entirely (FTS-only). Use in tests and on hosts without a llama-server.
- **Full rebuild rate** on this model+quant (Apple Silicon, single slot, batch=1): ~20 dense vectors/min. ~4000 chunks ≈ 3.5 hours.
- Embeddings index lives at `<canonical>.search.sqlite` in the `search_dense_vec` table (sqlite-vec virtual table, dim=4096). The shell `sqlite3` binary cannot read this table (no `vec0` module); use `wrkq index status` to count vectors.

### llama-server canonical args (see `launchd/com.praesidium.llama-server.plist`)

```
llama-server \
  --hf-repo Qwen/Qwen3-Embedding-8B-GGUF:Q4_K_M \
  --host 127.0.0.1 \
  --port <PORT> \
  --embedding \
  --pooling mean \
  --ctx-size 4096 \
  --batch-size 4096 --ubatch-size 4096 \
  --parallel 1
```

`--port 8080` conflicts with other things on Lance's hosts — wrkq's default `DenseBaseURL` and the launchd plist both use the wrkq-block port assigned in `internal/config/config.go`. Don't co-locate llama-server on 8080.
