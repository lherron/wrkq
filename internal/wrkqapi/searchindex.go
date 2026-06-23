package wrkqapi

import (
	"context"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/search"
	"github.com/lherron/wrkq/internal/search/embed"
	"github.com/lherron/wrkq/internal/search/indexdb"
	"github.com/lherron/wrkq/internal/search/indexer"
)

// SearchConfig carries the SERVER's search/index host configuration. The server
// owns the derived <db>.search.sqlite sidecar and the dense embedder: it opens +
// migrates the sidecar, runs FTS5/vec queries, computes freshness/status, mutates
// the index lifecycle, and (for index update/rebuild) kickstarts ONLY the server
// host's configured embedder via EnsureLlamaReady. The CLI mirror NEVER opens the
// sidecar or calls EnsureLlamaReady — it owns project-root path scoping +
// presentation only (daedalus hrcchat#10211, T-05114).
type SearchConfig struct {
	Enabled          bool
	CanonicalDBPath  string // canonical db path; sidecar defaults to <path>.search.sqlite
	DBPath           string // explicit sidecar override ("" → default from CanonicalDBPath)
	DenseProvider    string
	DenseBaseURL     string
	DenseModel       string
	DenseDimension   int
	QueryInstruction string
	IndexBatchSize   int
	CandidateLimit   int
}

// ─── DTOs ─────────────────────────────────────────────────────────────────────

// WrkqSearchResult is the server-owned COMPATIBILITY projection of one search hit.
// Its exported-field/json-tag shape EXACTLY reproduces the legacy search.Result
// (internal/search/search.go) in LEGACY STRUCT ORDER so the mirror renders
// byte-identical `search --json` / `--ndjson` output by marshaling straight
// through. Not a canonical resource.
type WrkqSearchResult struct {
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	ResourceUUID string         `json:"resource_uuid"`
	TaskID       string         `json:"task_id,omitempty"`
	TaskUUID     string         `json:"task_uuid,omitempty"`
	CommentID    *string        `json:"comment_id,omitempty"`
	ScopeRef     string         `json:"scope_ref,omitempty"`
	Status       string         `json:"status,omitempty"`
	Path         string         `json:"path"`
	Title        string         `json:"title"`
	State        string         `json:"state,omitempty"`
	Kind         string         `json:"kind,omitempty"`
	Snippet      string         `json:"snippet"`
	Score        float64        `json:"score"`
	CreatedAt    string         `json:"created_at"`
	UpdatedAt    string         `json:"updated_at"`
	Stale        bool           `json:"stale"`
	Explain      map[string]any `json:"explain,omitempty"`
}

// WrkqIndexStatus is the server-owned COMPATIBILITY projection of the search index
// status. Its exported-field/json-tag shape EXACTLY reproduces the legacy
// indexdb.Status (internal/search/indexdb/db.go) in LEGACY STRUCT ORDER, so the
// mirror renders byte-identical `index status --json` and the nested `status`
// block in rebuild/update lifecycle output. Not a canonical resource.
type WrkqIndexStatus struct {
	Path                 string  `json:"path"`
	Enabled              bool    `json:"enabled"`
	Status               string  `json:"status"`
	LastIndexedEventID   int64   `json:"last_indexed_event_id"`
	CanonicalMaxEventID  int64   `json:"canonical_max_event_id"`
	StaleEventCount      int64   `json:"stale_event_count"`
	DenseModelID         string  `json:"dense_model_id,omitempty"`
	DenseDimension       int     `json:"dense_dimension,omitempty"`
	DenseVectorCount     int64   `json:"dense_vector_count,omitempty"`
	LastError            *string `json:"last_error,omitempty"`
	SearchableChunkCount int64   `json:"searchable_chunk_count"`
}

// WrkqSearchListView is the server-owned COMPATIBILITY search read model for
// `wrkq search`. Its exported-field/json-tag shape EXACTLY reproduces the legacy
// search.Response (internal/search/search.go) in LEGACY STRUCT ORDER, so the
// mirror renders byte-identical `search --json` by marshaling straight through.
// The SERVER owns sidecar open/migrate, FTS5/vec/lexical candidate retrieval,
// RRF fusion, canonical filtering, freshness, sort + paging. The CLI owns ONLY
// project-root path scoping (before the call) + byte rendering. Not canonical.
type WrkqSearchListView struct {
	Query        string             `json:"query"`
	Stale        bool               `json:"stale"`
	Status       *WrkqIndexStatus   `json:"status"`
	Results      []WrkqSearchResult `json:"results"`
	TotalMatches int                `json:"total_matches"`
	Offset       int                `json:"offset"`
}

// ─── params ───────────────────────────────────────────────────────────────────

// SearchListViewParams mirrors the legacy `wrkq search` surface. Project-root
// scoping is the CALLER's responsibility: Paths are already scoped before they
// reach this method.
type SearchListViewParams struct {
	Query                string   `json:"query"`
	Paths                []string `json:"paths,omitempty"`
	State                string   `json:"state,omitempty"`
	Kind                 string   `json:"kind,omitempty"`
	AssigneePrincipalRef string   `json:"assigneePrincipalRef,omitempty"`
	Limit                int      `json:"limit,omitempty"`
	CandidateLimit       int      `json:"candidateLimit,omitempty"`
	Sort                 string   `json:"sort,omitempty"`
	Reverse              bool     `json:"reverse,omitempty"`
	Fresh                bool     `json:"fresh,omitempty"`
	Explain              bool     `json:"explain,omitempty"`
}

// IndexStatusParams targets the index status read. No fields today; the server
// resolves the sidecar from its own configuration.
type IndexStatusParams struct{}

// IndexLifecycleParams targets an index lifecycle mutation
// (update/rebuild/vacuum/pause/resume). Foreground is accepted for rebuild
// surface parity; the server always runs synchronously.
type IndexLifecycleParams struct {
	Foreground bool `json:"foreground,omitempty"`
}

// ─── search host plumbing (server owns the sidecar + embedder) ────────────────

// openSidecar opens + migrates the derived search sidecar. SERVER-OWNED: this is
// the only place the sidecar is opened across the RPC boundary. Returns a
// WRKQ_VALIDATION when search is disabled (legacy "search is disabled").
func (a *API) openSidecar() (*indexdb.DB, error) {
	if !a.searchCfg.Enabled {
		return nil, NewValidationError("search is disabled", nil)
	}
	path := a.searchCfg.DBPath
	if path == "" {
		path = indexdb.DefaultPath(a.searchCfg.CanonicalDBPath)
	}
	idx, err := indexdb.Open(path)
	if err != nil {
		return nil, NewInternalError(err)
	}
	return idx, nil
}

// denseEmbedder builds the SERVER host's configured dense embedder (mirrors
// legacy denseEmbedderFromConfig). The embedder is the server's, never the
// caller's: EnsureLlamaReady kickstarts ONLY this host's configured embedder.
func (a *API) denseEmbedder() embed.DenseEmbedder {
	switch strings.ToLower(a.searchCfg.DenseProvider) {
	case "", "llama-cpp":
		return &embed.LlamaCPP{
			BaseURL:          a.searchCfg.DenseBaseURL,
			Model:            a.searchCfg.DenseModel,
			DimensionValue:   a.searchCfg.DenseDimension,
			QueryInstruction: a.searchCfg.QueryInstruction,
		}
	case "hash":
		return embed.HashEmbedder{Model: "wrkq-hash-test", Dims: a.searchCfg.DenseDimension}
	case "none", "off", "disabled":
		return nil
	default:
		return nil
	}
}

func projectStatus(s *indexdb.Status) *WrkqIndexStatus {
	if s == nil {
		return nil
	}
	return &WrkqIndexStatus{
		Path:                 s.Path,
		Enabled:              s.Enabled,
		Status:               s.Status,
		LastIndexedEventID:   s.LastIndexedEventID,
		CanonicalMaxEventID:  s.CanonicalMaxEventID,
		StaleEventCount:      s.StaleEventCount,
		DenseModelID:         s.DenseModelID,
		DenseDimension:       s.DenseDimension,
		DenseVectorCount:     s.DenseVectorCount,
		LastError:            s.LastError,
		SearchableChunkCount: s.SearchableChunkCount,
	}
}

// ─── methods ──────────────────────────────────────────────────────────────────

// SearchListView backs wrkq.search.listView. SERVER-OWNED: it opens the sidecar,
// builds the host embedder, runs the hybrid search.Service, and projects the
// legacy search.Response shape. The mirror sends already-scoped Paths and renders
// the result; it never opens the sidecar.
func (a *API) SearchListView(ctx context.Context, p SearchListViewParams) (*WrkqSearchListView, error) {
	if strings.TrimSpace(p.Query) == "" {
		return nil, NewValidationError("search query is required", map[string]any{"field": "query"})
	}
	// Compat normalization (server-owned, mirrors FindListView): the caller passes
	// the raw --assignee token (bare agent slug OR canonical ref); the server
	// canonicalizes it to a principal ref so the effective filter matches legacy
	// BEFORE search.Service compares rows. NormalizeCompat is idempotent on
	// already-canonical refs, so a canonical --assignee agent:clod also works.
	assigneePrincipalRef := strings.TrimSpace(p.AssigneePrincipalRef)
	if assigneePrincipalRef != "" {
		ref, nerr := attribution.NormalizeCompat(assigneePrincipalRef)
		if nerr != nil {
			return nil, NewValidationError("failed to resolve assignee: "+nerr.Error(), map[string]any{"field": "assignee"})
		}
		assigneePrincipalRef = ref
	}

	idx, err := a.openSidecar()
	if err != nil {
		return nil, err
	}
	defer func() { _ = idx.Close() }()

	svc := search.NewService(a.db, idx, a.denseEmbedder())
	sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	resp, err := svc.Search(sctx, search.Options{
		Query:                p.Query,
		Paths:                p.Paths,
		State:                p.State,
		Kind:                 p.Kind,
		AssigneePrincipalRef: assigneePrincipalRef,
		Limit:                p.Limit,
		CandidateLimit:       p.CandidateLimit,
		Fresh:                p.Fresh,
		Explain:              p.Explain,
		Sort:                 p.Sort,
		Reverse:              p.Reverse,
	})
	if err != nil {
		return nil, classifySearchError(err)
	}

	out := &WrkqSearchListView{
		Query:        resp.Query,
		Stale:        resp.Stale,
		Status:       projectStatus(resp.Status),
		Results:      make([]WrkqSearchResult, 0, len(resp.Results)),
		TotalMatches: resp.TotalMatches,
		Offset:       resp.Offset,
	}
	for _, r := range resp.Results {
		out.Results = append(out.Results, projectResult(r))
	}
	return out, nil
}

func projectResult(r search.Result) WrkqSearchResult {
	return WrkqSearchResult{
		ResourceType: r.ResourceType,
		ResourceID:   r.ResourceID,
		ResourceUUID: r.ResourceUUID,
		TaskID:       r.TaskID,
		TaskUUID:     r.TaskUUID,
		CommentID:    r.CommentID,
		ScopeRef:     r.ScopeRef,
		Status:       r.Status,
		Path:         r.Path,
		Title:        r.Title,
		State:        r.State,
		Kind:         r.Kind,
		Snippet:      r.Snippet,
		Score:        r.Score,
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
		Stale:        r.Stale,
		Explain:      r.Explain,
	}
}

// classifySearchError maps the search.Service's invalid-input sentinels to
// WRKQ_VALIDATION (invalid sort / stale-when-fresh) so the mirror surfaces the
// legacy message without a domain-code prefix; everything else is internal.
func classifySearchError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "invalid search sort") ||
		strings.Contains(msg, "search query is required") ||
		strings.Contains(msg, "search index is stale") {
		return NewValidationError(msg, nil)
	}
	return NewInternalError(err)
}

// IndexStatus backs wrkq.index.status. SERVER-OWNED sidecar open + status compute.
func (a *API) IndexStatus(ctx context.Context, _ IndexStatusParams) (*WrkqIndexStatus, error) {
	idx, err := a.openSidecar()
	if err != nil {
		return nil, err
	}
	defer func() { _ = idx.Close() }()

	ix := indexer.New(a.db, idx, a.denseEmbedder())
	ix.BatchSize = a.searchCfg.IndexBatchSize
	status, err := ix.Status()
	if err != nil {
		return nil, NewInternalError(err)
	}
	return projectStatus(status), nil
}

// IndexUpdate backs wrkq.index.update. SERVER-OWNED: it opens the sidecar, builds
// the host embedder, calls EnsureLlamaReady on the SERVER host's embedder (never
// the caller's), and indexes pending canonical changes. The map-shaped result
// preserves legacy map-alphabetical JSON (status, updated). Returns the raw
// indexdb.Status (legacy struct order) nested under "status".
func (a *API) IndexUpdate(ctx context.Context, _ IndexLifecycleParams) (map[string]any, error) {
	idx, err := a.openSidecar()
	if err != nil {
		return nil, err
	}
	defer func() { _ = idx.Close() }()

	embedder := a.denseEmbedder()
	ix := indexer.New(a.db, idx, embedder)
	ix.BatchSize = a.searchCfg.IndexBatchSize
	uctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	if err := embed.EnsureLlamaReady(uctx, embedder, 60*time.Second); err != nil {
		return nil, NewInternalError(err)
	}
	if err := ix.IndexPending(uctx); err != nil {
		return nil, NewInternalError(err)
	}
	status, _ := ix.Status()
	return map[string]any{
		"updated": true,
		"status":  projectStatus(status),
	}, nil
}

// IndexRebuild backs wrkq.index.rebuild. SERVER-OWNED sidecar + EnsureLlamaReady +
// full rebuild. Map-shaped result is map-alphabetical (rebuilt, status).
func (a *API) IndexRebuild(ctx context.Context, _ IndexLifecycleParams) (map[string]any, error) {
	idx, err := a.openSidecar()
	if err != nil {
		return nil, err
	}
	defer func() { _ = idx.Close() }()

	embedder := a.denseEmbedder()
	ix := indexer.New(a.db, idx, embedder)
	ix.BatchSize = a.searchCfg.IndexBatchSize
	rctx, cancel := context.WithTimeout(ctx, 60*time.Minute)
	defer cancel()

	if err := embed.EnsureLlamaReady(rctx, embedder, 60*time.Second); err != nil {
		return nil, NewInternalError(err)
	}
	if err := ix.Rebuild(rctx); err != nil {
		return nil, NewInternalError(err)
	}
	status, _ := ix.Status()
	return map[string]any{
		"rebuilt": true,
		"status":  projectStatus(status),
	}, nil
}

// IndexVacuum backs wrkq.index.vacuum. SERVER-OWNED sidecar VACUUM. Map-shaped
// result is map-alphabetical (vacuumed).
func (a *API) IndexVacuum(ctx context.Context, _ IndexLifecycleParams) (map[string]any, error) {
	idx, err := a.openSidecar()
	if err != nil {
		return nil, err
	}
	defer func() { _ = idx.Close() }()
	if _, err := idx.Exec(`VACUUM`); err != nil {
		return nil, NewInternalError(err)
	}
	return map[string]any{"vacuumed": true}, nil
}

// IndexPause backs wrkq.index.pause. SERVER-OWNED sidecar state write. Map-shaped
// result is map-alphabetical (status).
func (a *API) IndexPause(ctx context.Context, _ IndexLifecycleParams) (map[string]any, error) {
	idx, err := a.openSidecar()
	if err != nil {
		return nil, err
	}
	defer func() { _ = idx.Close() }()
	if err := idx.SetState("status", "paused"); err != nil {
		return nil, NewInternalError(err)
	}
	return map[string]any{"status": "paused"}, nil
}

// IndexResume backs wrkq.index.resume. SERVER-OWNED sidecar state write. Map-shaped
// result is map-alphabetical (status).
func (a *API) IndexResume(ctx context.Context, _ IndexLifecycleParams) (map[string]any, error) {
	idx, err := a.openSidecar()
	if err != nil {
		return nil, err
	}
	defer func() { _ = idx.Close() }()
	if err := idx.SetState("status", "ready"); err != nil {
		return nil, NewInternalError(err)
	}
	return map[string]any{"status": "ready"}, nil
}
