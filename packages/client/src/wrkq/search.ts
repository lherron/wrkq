/**
 * wrkq/search.ts — search and index DTOs (wrkq.search.listView, wrkq.index.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7.
 */

// The SERVER owns the derived <db>.search.sqlite sidecar + dense embedder behind
// wrkq.search.listView / wrkq.index.*. The client owns ONLY project-root path
// scoping (paths are pre-scoped before the call) + presentation. The result DTOs
// keep the LEGACY snake_case output keys (search.Response / search.Result /
// indexdb.Status field shapes) so machine consumers match `wrkq search --json`
// and `wrkq index status --json` byte-for-byte.

/** Params for wrkq.search.listView. Paths are already project-root scoped. */
export interface WrkqSearchListViewParams {
  subtasks?: boolean;
  query: string;
  /** Pre-scoped path prefixes to restrict results to. */
  paths?: string[];
  /** State filter ("" → open only; "all" → non-deleted). */
  state?: string;
  kind?: string;
  /** Exact, case-sensitive canonical task labels; every value must be present. */
  labels?: string[];
  /** Assignee principal ref (or bare agent slug the caller resolved). */
  assigneePrincipalRef?: string;
  limit?: number;
  candidateLimit?: number;
  /** "relevance" (default) | "updated_at" | "created_at". */
  sort?: string;
  reverse?: boolean;
  /** Fail (WRKQ_VALIDATION) if the index is stale rather than returning stale rows. */
  fresh?: boolean;
  /** Include ranking diagnostics under each result's `explain`. */
  explain?: boolean;
}

/** One search hit — LEGACY search.Result snake_case shape (struct order). */
export interface WrkqSearchResult {
  resource_type: string;
  resource_id: string;
  resource_uuid: string;
  task_id?: string;
  task_uuid?: string;
  comment_id?: string;
  scope_ref?: string;
  status?: string;
  path: string;
  title: string;
  state?: string;
  kind?: string;
  snippet: string;
  score: number;
  created_at: string;
  updated_at: string;
  stale: boolean;
  explain?: Record<string, unknown>;
}

/** Search index status — LEGACY indexdb.Status snake_case shape (struct order). */
export interface WrkqIndexStatus {
  path: string;
  enabled: boolean;
  status: string;
  last_indexed_event_id: number;
  canonical_max_event_id: number;
  stale_event_count: number;
  dense_model_id?: string;
  dense_dimension?: number;
  dense_vector_count?: number;
  last_error?: string;
  searchable_chunk_count: number;
}

/** Result of wrkq.search.listView — LEGACY search.Response snake_case shape. */
export interface WrkqSearchListView {
  query: string;
  stale: boolean;
  status: WrkqIndexStatus | null;
  results: WrkqSearchResult[];
  total_matches: number;
  offset: number;
}

/** Params for the index lifecycle methods (update/rebuild/vacuum/pause/resume). */
export interface WrkqIndexLifecycleParams {
  /** Accepted for `rebuild` surface parity; the server always runs synchronously. */
  foreground?: boolean;
}

/** Result of wrkq.index.update (legacy map-alphabetical ack). */
export interface WrkqIndexUpdateResult {
  status: WrkqIndexStatus;
  updated: boolean;
}

/** Result of wrkq.index.rebuild (legacy map-alphabetical ack). */
export interface WrkqIndexRebuildResult {
  rebuilt: boolean;
  status: WrkqIndexStatus;
}

/** Result of wrkq.index.vacuum (legacy map-alphabetical ack). */
export interface WrkqIndexVacuumResult {
  vacuumed: boolean;
}

/** Result of wrkq.index.pause / wrkq.index.resume (legacy map-alphabetical ack). */
export interface WrkqIndexStateResult {
  status: string;
}
