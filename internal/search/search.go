package search

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/search/embed"
	"github.com/lherron/wrkq/internal/search/indexdb"
	"github.com/lherron/wrkq/internal/search/indexer"
	"github.com/lherron/wrkq/internal/search/rank"
)

type Service struct {
	Canonical *db.DB
	Index     *indexdb.DB
	Embedder  embed.DenseEmbedder
}

type Options struct {
	Query          string
	Paths          []string
	State          string
	Kind           string
	AssigneeUUID   string
	ResourceType   string
	ScopeRef       string
	Status         string
	Offset         int
	Limit          int
	CandidateLimit int
	Fresh          bool
	Explain        bool
	Sort           string
	Reverse        bool
}

type Result struct {
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

type Response struct {
	Query        string          `json:"query"`
	Stale        bool            `json:"stale"`
	Status       *indexdb.Status `json:"status"`
	Results      []Result        `json:"results"`
	TotalMatches int             `json:"total_matches"`
	Offset       int             `json:"offset"`
}

func NewService(canonical *db.DB, index *indexdb.DB, embedder embed.DenseEmbedder) *Service {
	return &Service{Canonical: canonical, Index: index, Embedder: embedder}
}

func (s *Service) Search(ctx context.Context, opts Options) (*Response, error) {
	if opts.Query == "" {
		return nil, fmt.Errorf("search query is required")
	}
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.CandidateLimit <= 0 {
		opts.CandidateLimit = 300
	}
	if opts.Sort == "" {
		opts.Sort = "relevance"
	}
	if opts.Sort != "relevance" && opts.Sort != "updated_at" && opts.Sort != "created_at" {
		return nil, fmt.Errorf("invalid search sort %q: choose relevance, updated_at, or created_at", opts.Sort)
	}

	ix := indexer.New(s.Canonical, s.Index, s.Embedder)
	status, err := ix.Status()
	if err != nil {
		return nil, err
	}
	stale := status.StaleEventCount > 0
	if stale && opts.Fresh {
		return nil, fmt.Errorf("search index is stale: %d event(s) behind", status.StaleEventCount)
	}

	fts, err := s.ftsCandidates(opts.Query, opts.CandidateLimit)
	if err != nil {
		return nil, err
	}
	var dense []rank.Candidate
	if s.Embedder != nil && s.Index.HasDenseTable() {
		dense, err = s.denseCandidates(ctx, opts.Query, opts.CandidateLimit)
		if err != nil {
			s.Index.RecordFailure(status.CanonicalMaxEventID, "", "dense_query", err)
		}
	}

	scoreMap := rank.RRF([][]rank.Candidate{fts, dense}, 60)
	if len(scoreMap) == 0 {
		return &Response{
			Query:        opts.Query,
			Stale:        stale,
			Status:       status,
			Results:      []Result{},
			TotalMatches: 0,
			Offset:       opts.Offset,
		}, nil
	}

	results, err := s.materializeResults(scoreMap, opts, stale)
	if err != nil {
		return nil, err
	}
	sortResults(results, opts)
	total := len(results)
	start := opts.Offset
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	end := start + opts.Limit
	if end > total {
		end = total
	}
	page := results[start:end]
	return &Response{
		Query:        opts.Query,
		Stale:        stale,
		Status:       status,
		Results:      page,
		TotalMatches: total,
		Offset:       start,
	}, nil
}

func (s *Service) ftsCandidates(query string, limit int) ([]rank.Candidate, error) {
	if !s.Index.FTSAvailable() {
		return s.plainLexicalCandidates(query, limit)
	}
	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}
	rows, err := s.Index.Query(`
		SELECT chunk_id, bm25(search_fts) AS score
		FROM search_fts
		WHERE search_fts MATCH ?
		ORDER BY score ASC
		LIMIT ?
	`, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("fts search failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []rank.Candidate
	for rows.Next() {
		var chunkID string
		var score float64
		if err := rows.Scan(&chunkID, &score); err != nil {
			return nil, err
		}
		out = append(out, rank.Candidate{ChunkID: chunkID, Score: -score, Source: "fts"})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) plainLexicalCandidates(query string, limit int) ([]rank.Candidate, error) {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return nil, nil
	}
	clauses := make([]string, 0, len(terms)*3)
	args := make([]interface{}, 0, len(terms)*3+1)
	for _, term := range terms {
		like := "%" + term + "%"
		clauses = append(clauses, "lower(title) LIKE ?", "lower(body) LIKE ?", "lower(path) LIKE ?")
		args = append(args, like, like, like)
	}
	args = append(args, limit)
	rows, err := s.Index.Query(`
		SELECT chunk_id
		FROM search_fts_plain
		WHERE `+strings.Join(clauses, " OR ")+`
		ORDER BY chunk_id
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("lexical search failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []rank.Candidate
	for rows.Next() {
		var chunkID string
		if err := rows.Scan(&chunkID); err != nil {
			return nil, err
		}
		out = append(out, rank.Candidate{ChunkID: chunkID, Score: 1, Source: "lexical"})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) denseCandidates(ctx context.Context, query string, limit int) ([]rank.Candidate, error) {
	vector, err := s.Embedder.EmbedQuery(ctx, query)
	if err != nil {
		return nil, err
	}
	if len(vector) != s.Embedder.Dimension() {
		return nil, fmt.Errorf("query vector dimension %d does not match configured dimension %d", len(vector), s.Embedder.Dimension())
	}
	blob, err := sqlitevec.SerializeFloat32(vector)
	if err != nil {
		return nil, err
	}
	rows, err := s.Index.Query(`
		SELECT c.chunk_id, v.distance
		FROM search_dense_vec v
		JOIN search_chunks c ON c.ordinal = v.rowid
		WHERE v.embedding MATCH ? AND k = ?
		ORDER BY v.distance ASC
	`, blob, limit)
	if err != nil {
		return nil, fmt.Errorf("dense vector search failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []rank.Candidate
	for rows.Next() {
		var chunkID string
		var distance float64
		if err := rows.Scan(&chunkID, &distance); err != nil {
			return nil, err
		}
		out = append(out, rank.Candidate{ChunkID: chunkID, Score: -distance, Source: "dense"})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) materializeResults(scoreMap map[string]float64, opts Options, stale bool) ([]Result, error) {
	chunkIDs := make([]string, 0, len(scoreMap))
	for chunkID := range scoreMap {
		chunkIDs = append(chunkIDs, chunkID)
	}

	results := make([]Result, 0, len(chunkIDs))
	for _, chunkID := range chunkIDs {
		row := s.Index.QueryRow(`
			SELECT resource_type, resource_uuid, resource_id,
			       task_uuid, task_id, comment_id,
			       scope_ref, status, path, state, kind, title, body
			FROM search_chunks
			WHERE chunk_id = ?
		`, chunkID)
		var resourceType, resourceUUID, resourceID, path, title, body string
		var taskUUID, taskID, commentID, scopeRef, status, state, kind sql.NullString
		if err := row.Scan(&resourceType, &resourceUUID, &resourceID,
			&taskUUID, &taskID, &commentID,
			&scopeRef, &status, &path, &state, &kind, &title, &body); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return nil, err
		}
		if opts.ResourceType != "" && resourceType != opts.ResourceType {
			continue
		}
		switch resourceType {
		case "task", "comment":
			if !s.canonicalTaskMatches(taskUUID.String, opts) {
				continue
			}
		case "handoff":
			if !handoffMatches(scopeRef.String, status.String, opts) {
				continue
			}
		}
		if !pathMatches(path, opts.Paths) {
			continue
		}
		result := Result{
			ResourceType: resourceType,
			ResourceID:   resourceID,
			ResourceUUID: resourceUUID,
			TaskID:       taskID.String,
			TaskUUID:     taskUUID.String,
			ScopeRef:     scopeRef.String,
			Status:       status.String,
			Path:         path,
			Title:        title,
			State:        state.String,
			Kind:         kind.String,
			Snippet:      snippet(body, opts.Query),
			Score:        scoreMap[chunkID],
			Stale:        stale,
		}
		if commentID.Valid {
			cid := commentID.String
			result.CommentID = &cid
		}
		if opts.Explain {
			result.Explain = map[string]any{"chunk_id": chunkID, "rrf_score": scoreMap[chunkID]}
		}
		createdAt, updatedAt, err := s.resourceTimestamps(resourceType, resourceUUID)
		if err != nil {
			continue
		}
		result.CreatedAt = normalizeTimestamp(createdAt)
		result.UpdatedAt = normalizeTimestamp(updatedAt)
		results = append(results, result)
	}

	sortResults(results, Options{Sort: "relevance"})

	seen := map[string]int{}
	aggregated := make([]Result, 0, len(results))
	for _, result := range results {
		key := result.ResourceType + ":" + result.ResourceUUID
		if idx, ok := seen[key]; ok {
			if result.Score > aggregated[idx].Score {
				aggregated[idx] = result
			}
			continue
		}
		seen[key] = len(aggregated)
		aggregated = append(aggregated, result)
	}
	return aggregated, nil
}

func (s *Service) resourceTimestamps(resourceType, resourceUUID string) (string, string, error) {
	var createdAt, updatedAt string
	switch resourceType {
	case "task":
		err := s.Canonical.QueryRow(`
			SELECT created_at, updated_at
			FROM tasks
			WHERE uuid = ?
		`, resourceUUID).Scan(&createdAt, &updatedAt)
		return createdAt, updatedAt, err
	case "comment":
		err := s.Canonical.QueryRow(`
			SELECT created_at, COALESCE(updated_at, created_at)
			FROM comments
			WHERE uuid = ?
		`, resourceUUID).Scan(&createdAt, &updatedAt)
		return createdAt, updatedAt, err
	case "handoff":
		err := s.Canonical.QueryRow(`
			SELECT created_at, updated_at
			FROM handoffs
			WHERE uuid = ?
		`, resourceUUID).Scan(&createdAt, &updatedAt)
		return createdAt, updatedAt, err
	default:
		return "", "", fmt.Errorf("unsupported search resource type %q", resourceType)
	}
}

func sortResults(results []Result, opts Options) {
	sortField := opts.Sort
	if sortField == "" {
		sortField = "relevance"
	}
	sort.SliceStable(results, func(i, j int) bool {
		cmp := 0
		switch sortField {
		case "created_at":
			if results[i].CreatedAt < results[j].CreatedAt {
				cmp = -1
			} else if results[i].CreatedAt > results[j].CreatedAt {
				cmp = 1
			} else {
				cmp = compareString(results[i].ResourceID, results[j].ResourceID)
			}
		case "updated_at":
			if results[i].UpdatedAt < results[j].UpdatedAt {
				cmp = -1
			} else if results[i].UpdatedAt > results[j].UpdatedAt {
				cmp = 1
			} else {
				cmp = compareString(results[i].ResourceID, results[j].ResourceID)
			}
		default:
			if results[i].Score > results[j].Score {
				cmp = -1
			} else if results[i].Score < results[j].Score {
				cmp = 1
			} else {
				cmp = compareString(results[i].ResourceID, results[j].ResourceID)
			}
		}
		if opts.Reverse {
			return cmp > 0
		}
		return cmp < 0
	})
}

func compareString(left, right string) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func normalizeTimestamp(value string) string {
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
	}
	return value
}

func handoffMatches(scopeRef, status string, opts Options) bool {
	if opts.ScopeRef != "" && scopeRef != opts.ScopeRef {
		return false
	}
	switch opts.Status {
	case "", "pending":
		if status != "pending" {
			return false
		}
	case "acknowledged":
		if status != "acknowledged" {
			return false
		}
	case "all":
		// no filter
	default:
		if status != opts.Status {
			return false
		}
	}
	return true
}

func (s *Service) canonicalTaskMatches(taskUUID string, opts Options) bool {
	var state, kind string
	var assignee sql.NullString
	err := s.Canonical.QueryRow(`SELECT state, kind, assignee_actor_uuid FROM tasks WHERE uuid = ?`, taskUUID).Scan(&state, &kind, &assignee)
	if err != nil {
		return false
	}
	switch opts.State {
	case "all":
		if state == "deleted" {
			return false
		}
	case "":
		if state != "open" {
			return false
		}
	default:
		if state != opts.State {
			return false
		}
	}
	if opts.Kind != "" && kind != opts.Kind {
		return false
	}
	if opts.AssigneeUUID != "" && (!assignee.Valid || assignee.String != opts.AssigneeUUID) {
		return false
	}
	return true
}

func buildFTSQuery(query string) string {
	terms := strings.Fields(query)
	escaped := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.Trim(term, `"'()[]{}:;,.!?`)
		if term == "" {
			continue
		}
		term = strings.ReplaceAll(term, `"`, `""`)
		escaped = append(escaped, `"`+term+`"`)
	}
	return strings.Join(escaped, " OR ")
}

func pathMatches(path string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true
	}
	for _, prefix := range prefixes {
		prefix = strings.Trim(prefix, "/")
		if prefix == "" || path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// contentBody strips the structured chunk preamble (Path:/Task:/Title:/…)
// emitted by the renderer, returning just the description or comment body.
// The CLI surfaces id, title, state, and path as their own columns, so the
// snippet should focus on the matched prose rather than repeating metadata.
func contentBody(body string) string {
	lines := strings.Split(body, "\n")
	start := -1
	for i, ln := range lines {
		if ln == "Description:" || ln == "Body:" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return body
	}
	out := make([]string, 0, len(lines)-start)
	for _, ln := range lines[start:] {
		if ln == "Specification:" { // drop the bare section label, keep its prose
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

func snippet(body, query string) string {
	const max = 240
	compact := strings.Join(strings.Fields(contentBody(body)), " ")
	if len(compact) <= max {
		return compact
	}
	lower := strings.ToLower(compact)
	start := 0
	for _, term := range strings.Fields(strings.ToLower(query)) {
		if idx := strings.Index(lower, term); idx >= 0 {
			start = idx - 60
			if start < 0 {
				start = 0
			}
			break
		}
	}
	end := start + max
	if end > len(compact) {
		end = len(compact)
	}
	// Snap the window to word boundaries so it doesn't slice mid-word, and
	// mark each trimmed edge with an ellipsis.
	if start > 0 {
		if sp := strings.IndexByte(compact[start:end], ' '); sp >= 0 {
			start += sp + 1
		}
	}
	if end < len(compact) {
		if sp := strings.LastIndexByte(compact[start:end], ' '); sp > 0 {
			end = start + sp
		}
	}
	out := compact[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(compact) {
		out += "…"
	}
	return out
}
