package indexer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/search/embed"
	"github.com/lherron/wrkq/internal/search/indexdb"
	searchrender "github.com/lherron/wrkq/internal/search/render"
)

type Indexer struct {
	Canonical *db.DB
	Index     *indexdb.DB
	Embedder  embed.DenseEmbedder
	BatchSize int
}

func New(canonical *db.DB, index *indexdb.DB, embedder embed.DenseEmbedder) *Indexer {
	return &Indexer{Canonical: canonical, Index: index, Embedder: embedder, BatchSize: 8}
}

func (ix *Indexer) Rebuild(ctx context.Context) error {
	maxEventID, err := ix.CanonicalMaxEventID()
	if err != nil {
		return err
	}
	_ = ix.Index.SetState("last_error", "")
	_ = ix.Index.SetState("status", "indexing")

	if _, err := ix.Index.Exec(`DELETE FROM search_fts`); err != nil {
		if ix.Index.FTSAvailable() {
			return fmt.Errorf("failed to clear search fts: %w", err)
		}
	}
	if _, err := ix.Index.Exec(`DELETE FROM search_fts_plain`); err != nil {
		return fmt.Errorf("failed to clear search lexical table: %w", err)
	}
	if _, err := ix.Index.Exec(`DELETE FROM search_chunks`); err != nil {
		return fmt.Errorf("failed to clear search chunks: %w", err)
	}
	if ix.Index.HasDenseTable() {
		if _, err := ix.Index.Exec(`DELETE FROM search_dense_vec`); err != nil {
			return fmt.Errorf("failed to clear dense vectors: %w", err)
		}
	}

	if err := ix.prepareDenseTable(); err != nil {
		ix.Index.RecordFailure(maxEventID, "", "dense_schema", err)
	}

	taskUUIDs, err := searchrender.AllSearchableTaskUUIDs(ix.Canonical.DB)
	if err != nil {
		return err
	}
	var denseChunks []searchrender.Chunk
	for _, taskUUID := range taskUUIDs {
		chunks, err := ix.indexTaskMetadata(ctx, taskUUID, maxEventID)
		if err != nil {
			ix.Index.RecordFailure(maxEventID, taskUUID, "index_task", err)
			return err
		}
		denseChunks = append(denseChunks, chunks...)
	}
	if len(denseChunks) > 0 && ix.Embedder != nil && ix.Embedder.Dimension() > 0 && ix.Index.HasDenseTable() {
		if err := ix.indexDenseBatches(ctx, denseChunks, maxEventID); err != nil {
			ix.Index.RecordFailure(maxEventID, "", "dense_embed", err)
		}
	}

	if err := ix.Index.SetState("last_indexed_event_id", strconv.FormatInt(maxEventID, 10)); err != nil {
		return err
	}
	if err := ix.Index.SetState("last_successful_indexed_event_id", strconv.FormatInt(maxEventID, 10)); err != nil {
		return err
	}
	return ix.finishStatus()
}

func (ix *Indexer) IndexPending(ctx context.Context) error {
	last, err := ix.Index.Int64State("last_indexed_event_id")
	if err != nil {
		return err
	}
	maxEventID, err := ix.CanonicalMaxEventID()
	if err != nil {
		return err
	}
	if maxEventID <= last {
		return nil
	}
	_ = ix.Index.SetState("last_error", "")
	_ = ix.Index.SetState("status", "indexing")

	dirty, err := ix.DirtyTasksSince(last)
	if err != nil {
		return err
	}
	if err := ix.prepareDenseTable(); err != nil {
		ix.Index.RecordFailure(maxEventID, "", "dense_schema", err)
	}
	for taskUUID := range dirty {
		if err := ix.IndexTask(ctx, taskUUID, maxEventID); err != nil {
			ix.Index.RecordFailure(maxEventID, taskUUID, "index_task", err)
			return err
		}
	}
	if err := ix.Index.SetState("last_indexed_event_id", strconv.FormatInt(maxEventID, 10)); err != nil {
		return err
	}
	if err := ix.Index.SetState("last_successful_indexed_event_id", strconv.FormatInt(maxEventID, 10)); err != nil {
		return err
	}
	return ix.finishStatus()
}

func (ix *Indexer) IndexTask(ctx context.Context, taskUUID string, indexedEventID int64) error {
	denseChunks, err := ix.indexTaskMetadata(ctx, taskUUID, indexedEventID)
	if err != nil {
		return err
	}
	if len(denseChunks) > 0 && ix.Embedder != nil && ix.Embedder.Dimension() > 0 && ix.Index.HasDenseTable() {
		if err := ix.indexDense(ctx, denseChunks, indexedEventID); err != nil {
			ix.Index.RecordFailure(indexedEventID, taskUUID, "dense_embed", err)
		}
	}

	return nil
}

func (ix *Indexer) indexTaskMetadata(ctx context.Context, taskUUID string, indexedEventID int64) ([]searchrender.Chunk, error) {
	chunks, err := searchrender.TaskChunks(ix.Canonical.DB, taskUUID)
	if err != nil {
		return nil, err
	}

	tx, err := ix.Index.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, err := existingTaskChunks(tx, taskUUID)
	if err != nil {
		return nil, err
	}
	next := map[string]searchrender.Chunk{}
	for _, chunk := range chunks {
		next[chunk.ChunkID] = chunk
	}

	for chunkID, ordinal := range existing {
		if _, ok := next[chunkID]; ok {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM search_fts WHERE chunk_id = ?`, chunkID); err != nil {
			if ix.Index.FTSAvailable() {
				return nil, err
			}
		}
		if _, err := tx.Exec(`DELETE FROM search_fts_plain WHERE chunk_id = ?`, chunkID); err != nil {
			return nil, err
		}
		if ix.Index.HasDenseTable() {
			if _, err := tx.Exec(`DELETE FROM search_dense_vec WHERE rowid = ?`, ordinal); err != nil {
				return nil, err
			}
		}
		if _, err := tx.Exec(`DELETE FROM search_chunks WHERE chunk_id = ?`, chunkID); err != nil {
			return nil, err
		}
	}

	var denseChunks []searchrender.Chunk
	for _, chunk := range chunks {
		changed, err := upsertChunk(tx, chunk, indexedEventID)
		if err != nil {
			return nil, err
		}
		if changed {
			if _, err := tx.Exec(`DELETE FROM search_fts WHERE chunk_id = ?`, chunk.ChunkID); err != nil {
				if ix.Index.FTSAvailable() {
					return nil, err
				}
			}
			if ix.Index.FTSAvailable() {
				if _, err := tx.Exec(`INSERT INTO search_fts (chunk_id, title, body, path) VALUES (?, ?, ?, ?)`, chunk.ChunkID, chunk.Title, chunk.Body, chunk.Path); err != nil {
					return nil, err
				}
			}
			if _, err := tx.Exec(`
				INSERT INTO search_fts_plain (chunk_id, title, body, path)
				VALUES (?, ?, ?, ?)
				ON CONFLICT(chunk_id) DO UPDATE SET
				  title = excluded.title,
				  body = excluded.body,
				  path = excluded.path
			`, chunk.ChunkID, chunk.Title, chunk.Body, chunk.Path); err != nil {
				return nil, err
			}
			denseChunks = append(denseChunks, chunk)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return denseChunks, nil
}

func (ix *Indexer) indexDenseBatches(ctx context.Context, chunks []searchrender.Chunk, eventID int64) error {
	batchSize := ix.BatchSize
	if batchSize <= 0 {
		batchSize = 8
	}
	const maxBatchChars = 24000
	for start := 0; start < len(chunks); {
		end := start
		batchChars := 0
		for end < len(chunks) && end-start < batchSize {
			textLen := len(denseText(chunks[end]))
			if end > start && batchChars+textLen > maxBatchChars {
				break
			}
			batchChars += textLen
			end++
		}
		if end == start {
			end = start + 1
		}
		if err := ix.indexDense(ctx, chunks[start:end], eventID); err != nil {
			return err
		}
		start = end
	}
	return nil
}

func (ix *Indexer) indexDense(ctx context.Context, chunks []searchrender.Chunk, eventID int64) error {
	texts := make([]string, len(chunks))
	for i, chunk := range chunks {
		texts[i] = denseText(chunk)
	}
	vectors, err := ix.Embedder.EmbedDocuments(ctx, texts)
	if err != nil {
		return err
	}
	if len(vectors) != len(chunks) {
		return fmt.Errorf("dense embedder returned %d vectors for %d chunks", len(vectors), len(chunks))
	}

	tx, err := ix.Index.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for i, chunk := range chunks {
		if len(vectors[i]) != ix.Embedder.Dimension() {
			return fmt.Errorf("dense vector for %s has dimension %d, expected %d", chunk.ChunkID, len(vectors[i]), ix.Embedder.Dimension())
		}
		var ordinal int64
		if err := tx.QueryRow(`SELECT ordinal FROM search_chunks WHERE chunk_id = ?`, chunk.ChunkID).Scan(&ordinal); err != nil {
			return err
		}
		blob, err := sqlitevec.SerializeFloat32(vectors[i])
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM search_dense_vec WHERE rowid = ?`, ordinal); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO search_dense_vec(rowid, embedding) VALUES (?, ?)`, ordinal, blob); err != nil {
			return fmt.Errorf("failed to insert dense vector for %s: %w", chunk.ChunkID, err)
		}
	}
	return tx.Commit()
}

func denseText(chunk searchrender.Chunk) string {
	const maxTextChars = 4000
	text := chunk.Title + "\n" + chunk.Body
	if len(text) <= maxTextChars {
		return text
	}
	end := maxTextChars
	for end > 0 && !utf8.ValidString(text[:end]) {
		end--
	}
	if end == 0 {
		return ""
	}
	return text[:end] + "\n[truncated]"
}

func (ix *Indexer) prepareDenseTable() error {
	if ix.Embedder == nil || ix.Embedder.Dimension() <= 0 {
		return nil
	}
	return ix.Index.EnsureDenseTable(ix.Embedder.Dimension(), ix.Embedder.ModelID())
}

func existingTaskChunks(tx *sql.Tx, taskUUID string) (map[string]int64, error) {
	rows, err := tx.Query(`SELECT chunk_id, ordinal FROM search_chunks WHERE task_uuid = ?`, taskUUID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]int64{}
	for rows.Next() {
		var chunkID string
		var ordinal int64
		if err := rows.Scan(&chunkID, &ordinal); err != nil {
			return nil, err
		}
		out[chunkID] = ordinal
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func upsertChunk(tx *sql.Tx, chunk searchrender.Chunk, eventID int64) (bool, error) {
	var oldHash string
	err := tx.QueryRow(`SELECT content_hash FROM search_chunks WHERE chunk_id = ?`, chunk.ChunkID).Scan(&oldHash)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if err == nil && oldHash == chunk.ContentHash {
		_, err = tx.Exec(`UPDATE search_chunks SET indexed_event_id = ?, indexed_at = ? WHERE chunk_id = ?`, eventID, now(), chunk.ChunkID)
		return false, err
	}

	_, err = tx.Exec(`
		INSERT INTO search_chunks (
		  chunk_id, resource_type, task_uuid, comment_uuid, path, task_id, comment_id,
		  state, kind, title, body, labels, content_hash, source_etag, source_updated_at,
		  indexed_event_id, indexed_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(chunk_id) DO UPDATE SET
		  resource_type = excluded.resource_type,
		  task_uuid = excluded.task_uuid,
		  comment_uuid = excluded.comment_uuid,
		  path = excluded.path,
		  task_id = excluded.task_id,
		  comment_id = excluded.comment_id,
		  state = excluded.state,
		  kind = excluded.kind,
		  title = excluded.title,
		  body = excluded.body,
		  labels = excluded.labels,
		  content_hash = excluded.content_hash,
		  source_etag = excluded.source_etag,
		  source_updated_at = excluded.source_updated_at,
		  indexed_event_id = excluded.indexed_event_id,
		  indexed_at = excluded.indexed_at
	`, chunk.ChunkID, chunk.ResourceType, chunk.TaskUUID, chunk.CommentUUID, chunk.Path, chunk.TaskID, chunk.CommentID,
		chunk.State, chunk.Kind, chunk.Title, chunk.Body, chunk.Labels, chunk.ContentHash, chunk.SourceETag, chunk.SourceUpdatedAt,
		eventID, now())
	if err != nil {
		return false, err
	}
	return true, nil
}

func (ix *Indexer) CanonicalMaxEventID() (int64, error) {
	var maxID int64
	err := ix.Canonical.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM event_log`).Scan(&maxID)
	if err != nil {
		return 0, fmt.Errorf("failed to read canonical max event id: %w", err)
	}
	return maxID, nil
}

func (ix *Indexer) DirtyTasksSince(lastEventID int64) (map[string]struct{}, error) {
	rows, err := ix.Canonical.Query(`
		SELECT id, resource_type, resource_uuid, event_type, COALESCE(payload, '')
		FROM event_log
		WHERE id > ?
		ORDER BY id ASC
	`, lastEventID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	dirty := map[string]struct{}{}
	for rows.Next() {
		var id int64
		var resourceType, eventType, payload string
		var resourceUUID sql.NullString
		if err := rows.Scan(&id, &resourceType, &resourceUUID, &eventType, &payload); err != nil {
			return nil, err
		}
		switch resourceType {
		case "task":
			if resourceUUID.Valid {
				dirty[resourceUUID.String] = struct{}{}
			}
		case "comment":
			taskUUID := ix.taskUUIDForCommentEvent(resourceUUID, payload)
			if taskUUID != "" {
				dirty[taskUUID] = struct{}{}
			}
		case "container":
			if resourceUUID.Valid {
				uuids, err := ix.descendantTaskUUIDs(resourceUUID.String)
				if err != nil {
					return nil, err
				}
				for _, uuid := range uuids {
					dirty[uuid] = struct{}{}
				}
			}
		}
		_ = id
		_ = eventType
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return dirty, nil
}

func (ix *Indexer) taskUUIDForCommentEvent(resourceUUID sql.NullString, payload string) string {
	if resourceUUID.Valid {
		var taskUUID string
		err := ix.Canonical.QueryRow(`SELECT task_uuid FROM comments WHERE uuid = ?`, resourceUUID.String).Scan(&taskUUID)
		if err == nil {
			return taskUUID
		}
	}
	var decoded map[string]interface{}
	if json.Unmarshal([]byte(payload), &decoded) == nil {
		if v, ok := decoded["task_id"].(string); ok && looksLikeUUID(v) {
			return v
		}
		if v, ok := decoded["task_uuid"].(string); ok && looksLikeUUID(v) {
			return v
		}
	}
	return ""
}

func (ix *Indexer) descendantTaskUUIDs(containerUUID string) ([]string, error) {
	rows, err := ix.Canonical.Query(`
		WITH RECURSIVE descendants(uuid) AS (
		  SELECT uuid FROM containers WHERE uuid = ?
		  UNION ALL
		  SELECT c.uuid FROM containers c JOIN descendants d ON c.parent_uuid = d.uuid
		)
		SELECT t.uuid FROM tasks t
		WHERE t.project_uuid IN (SELECT uuid FROM descendants)
	`, containerUUID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var uuids []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return nil, err
		}
		uuids = append(uuids, uuid)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return uuids, nil
}

func (ix *Indexer) Status() (*indexdb.Status, error) {
	maxEventID, err := ix.CanonicalMaxEventID()
	if err != nil {
		return nil, err
	}
	last, err := ix.Index.Int64State("last_indexed_event_id")
	if err != nil {
		return nil, err
	}
	statusValue, _, _ := ix.Index.State("status")
	lastError, _, _ := ix.Index.State("last_error")
	denseModel, _, _ := ix.Index.State("dense_model_id")
	denseDim, _ := ix.Index.IntState("dense_dimension")
	var chunks int64
	_ = ix.Index.QueryRow(`SELECT COUNT(*) FROM search_chunks`).Scan(&chunks)
	var denseVectors int64
	if ix.Index.HasDenseTable() {
		_ = ix.Index.QueryRow(`SELECT COUNT(*) FROM search_dense_vec`).Scan(&denseVectors)
	}

	var lastErrorPtr *string
	if strings.TrimSpace(lastError) != "" {
		lastErrorPtr = &lastError
	}
	return &indexdb.Status{
		Path:                 ix.Index.Path(),
		Enabled:              true,
		Status:               statusValue,
		LastIndexedEventID:   last,
		CanonicalMaxEventID:  maxEventID,
		StaleEventCount:      maxEventID - last,
		DenseModelID:         denseModel,
		DenseDimension:       denseDim,
		LastError:            lastErrorPtr,
		SearchableChunkCount: chunks,
		DenseVectorCount:     denseVectors,
	}, nil
}

func (ix *Indexer) finishStatus() error {
	lastError, _, _ := ix.Index.State("last_error")
	if strings.TrimSpace(lastError) != "" {
		return ix.Index.SetState("status", "degraded")
	}
	return ix.Index.SetState("status", "ready")
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func looksLikeUUID(s string) bool {
	return len(s) == 36 && strings.Count(s, "-") == 4
}
