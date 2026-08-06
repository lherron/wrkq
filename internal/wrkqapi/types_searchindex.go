package wrkqapi

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

// SearchListViewParams mirrors the legacy `wrkq search` surface. Project-root
// scoping is the CALLER's responsibility: Paths are already scoped before they
// reach this method.
type SearchListViewParams struct {
	Query                string   `json:"query"`
	Paths                []string `json:"paths,omitempty"`
	State                string   `json:"state,omitempty"`
	Kind                 string   `json:"kind,omitempty"`
	Labels               []string `json:"labels,omitempty"`
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

type handoffSearchCursorPayload struct {
	Offset int `json:"offset"`
}
