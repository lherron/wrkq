package search

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

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
	Limit          int
	CandidateLimit int
	Fresh          bool
	Explain        bool
}

type Result struct {
	TaskID       string         `json:"task_id"`
	TaskUUID     string         `json:"task_uuid"`
	Path         string         `json:"path"`
	Title        string         `json:"title"`
	State        string         `json:"state"`
	Kind         string         `json:"kind"`
	ResourceType string         `json:"resource_type"`
	CommentID    *string        `json:"comment_id,omitempty"`
	Snippet      string         `json:"snippet"`
	Score        float64        `json:"score"`
	Stale        bool           `json:"stale"`
	Explain      map[string]any `json:"explain,omitempty"`
}

type Response struct {
	Query   string          `json:"query"`
	Stale   bool            `json:"stale"`
	Status  *indexdb.Status `json:"status"`
	Results []Result        `json:"results"`
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
		return &Response{Query: opts.Query, Stale: stale, Status: status, Results: []Result{}}, nil
	}

	results, err := s.materializeResults(scoreMap, opts, stale)
	if err != nil {
		return nil, err
	}
	if len(results) > opts.Limit {
		results = results[:opts.Limit]
	}
	return &Response{Query: opts.Query, Stale: stale, Status: status, Results: results}, nil
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
			SELECT resource_type, task_uuid, comment_id, path, task_id, state, kind, title, body
			FROM search_chunks
			WHERE chunk_id = ?
		`, chunkID)
		var resourceType, taskUUID, path, taskID, state, kind, title, body string
		var commentID sql.NullString
		if err := row.Scan(&resourceType, &taskUUID, &commentID, &path, &taskID, &state, &kind, &title, &body); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return nil, err
		}
		if !s.canonicalTaskMatches(taskUUID, opts) {
			continue
		}
		if !pathMatches(path, opts.Paths) {
			continue
		}
		var commentIDPtr *string
		if commentID.Valid {
			commentIDPtr = &commentID.String
		}
		result := Result{
			TaskID:       taskID,
			TaskUUID:     taskUUID,
			Path:         path,
			Title:        title,
			State:        state,
			Kind:         kind,
			ResourceType: resourceType,
			CommentID:    commentIDPtr,
			Snippet:      snippet(body, opts.Query),
			Score:        scoreMap[chunkID],
			Stale:        stale,
		}
		if opts.Explain {
			result.Explain = map[string]any{"chunk_id": chunkID, "rrf_score": scoreMap[chunkID]}
		}
		results = append(results, result)
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].TaskID < results[j].TaskID
		}
		return results[i].Score > results[j].Score
	})

	seen := map[string]int{}
	aggregated := make([]Result, 0, len(results))
	for _, result := range results {
		key := result.TaskUUID
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

func snippet(body, query string) string {
	const max = 240
	compact := strings.Join(strings.Fields(body), " ")
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
	return compact[start:end]
}
