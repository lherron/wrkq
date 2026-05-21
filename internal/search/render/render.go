package render

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

type Chunk struct {
	ChunkID         string
	ResourceType    string
	TaskUUID        string
	CommentUUID     *string
	Path            string
	TaskID          string
	CommentID       *string
	State           string
	Kind            string
	Title           string
	Body            string
	Labels          string
	ContentHash     string
	SourceETag      int64
	SourceUpdatedAt string
}

func AllSearchableTaskUUIDs(database *sql.DB) ([]string, error) {
	rows, err := database.Query(`SELECT uuid FROM tasks WHERE state != 'deleted' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("failed to query searchable tasks: %w", err)
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

func TaskChunks(database *sql.DB, taskUUID string) ([]Chunk, error) {
	var task taskRow
	err := database.QueryRow(`
		SELECT t.uuid, t.id, t.title, t.state, t.priority, t.kind,
		       COALESCE(t.labels, ''), t.description, t.specification,
		       t.etag, t.updated_at, tp.path
		FROM tasks t
		JOIN v_task_paths tp ON tp.uuid = t.uuid
		WHERE t.uuid = ?
	`, taskUUID).Scan(
		&task.UUID, &task.ID, &task.Title, &task.State, &task.Priority, &task.Kind,
		&task.Labels, &task.Description, &task.Specification,
		&task.ETag, &task.UpdatedAt, &task.Path,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load task for search rendering: %w", err)
	}
	if task.State == "deleted" {
		return nil, nil
	}

	chunks := []Chunk{renderTaskChunk(task)}

	rows, err := database.Query(`
		SELECT c.uuid, c.id, c.body, c.etag, c.created_at, COALESCE(c.updated_at, c.created_at),
		       COALESCE(a.slug, '')
		FROM comments c
		LEFT JOIN actors a ON a.uuid = c.actor_uuid
		WHERE c.task_uuid = ? AND c.deleted_at IS NULL
		ORDER BY c.created_at ASC, c.id ASC
	`, taskUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query comments for search rendering: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var comment commentRow
		if err := rows.Scan(&comment.UUID, &comment.ID, &comment.Body, &comment.ETag, &comment.CreatedAt, &comment.UpdatedAt, &comment.ActorSlug); err != nil {
			return nil, err
		}
		chunks = append(chunks, renderCommentChunk(task, comment))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return chunks, nil
}

type taskRow struct {
	UUID          string
	ID            string
	Title         string
	State         string
	Priority      int
	Kind          string
	Labels        string
	Description   string
	Specification string
	ETag          int64
	UpdatedAt     string
	Path          string
}

type commentRow struct {
	UUID      string
	ID        string
	Body      string
	ETag      int64
	CreatedAt string
	UpdatedAt string
	ActorSlug string
}

func renderTaskChunk(task taskRow) Chunk {
	body := strings.Join(nonEmptyLines([]string{
		"Path: " + task.Path,
		"Task: " + task.ID,
		"Title: " + task.Title,
		fmt.Sprintf("State: %s", task.State),
		fmt.Sprintf("Kind: %s", task.Kind),
		fmt.Sprintf("Priority: %d", task.Priority),
		"Labels: " + task.Labels,
		"Description:",
		task.Description,
		"Specification:",
		task.Specification,
	}), "\n")

	chunk := Chunk{
		ChunkID:         "task:" + task.UUID,
		ResourceType:    "task",
		TaskUUID:        task.UUID,
		Path:            task.Path,
		TaskID:          task.ID,
		State:           task.State,
		Kind:            task.Kind,
		Title:           task.Title,
		Body:            body,
		Labels:          task.Labels,
		SourceETag:      task.ETag,
		SourceUpdatedAt: task.UpdatedAt,
	}
	chunk.ContentHash = hashChunk(chunk)
	return chunk
}

func renderCommentChunk(task taskRow, comment commentRow) Chunk {
	commentID := comment.ID
	commentUUID := comment.UUID
	body := strings.Join(nonEmptyLines([]string{
		"Path: " + task.Path,
		"Task: " + task.ID,
		"Comment: " + comment.ID,
		"Actor: " + comment.ActorSlug,
		"Body:",
		comment.Body,
	}), "\n")

	chunk := Chunk{
		ChunkID:         "comment:" + comment.UUID,
		ResourceType:    "comment",
		TaskUUID:        task.UUID,
		CommentUUID:     &commentUUID,
		Path:            task.Path,
		TaskID:          task.ID,
		CommentID:       &commentID,
		State:           task.State,
		Kind:            task.Kind,
		Title:           task.Title,
		Body:            body,
		Labels:          task.Labels,
		SourceETag:      comment.ETag,
		SourceUpdatedAt: comment.UpdatedAt,
	}
	chunk.ContentHash = hashChunk(chunk)
	return chunk
}

func hashChunk(chunk Chunk) string {
	h := sha256.New()
	_, _ = h.Write([]byte(chunk.ResourceType))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(chunk.Path))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(chunk.Title))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(chunk.Body))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(chunk.Labels))
	return hex.EncodeToString(h.Sum(nil))
}

func nonEmptyLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}
