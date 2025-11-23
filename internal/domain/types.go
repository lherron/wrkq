package domain

import (
	"encoding/json"
	"time"
)

// Actor represents an actor in the system
type Actor struct {
	UUID        string    `json:"uuid" db:"uuid"`
	ID          string    `json:"id" db:"id"`
	Slug        string    `json:"slug" db:"slug"`
	DisplayName *string   `json:"display_name,omitempty" db:"display_name"`
	Role        string    `json:"role" db:"role"` // human, agent, system
	Meta        *string   `json:"meta,omitempty" db:"meta"` // JSON
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Container represents a project or subproject
type Container struct {
	UUID               string     `json:"uuid" db:"uuid"`
	ID                 string     `json:"id" db:"id"`
	Slug               string     `json:"slug" db:"slug"`
	Title              *string    `json:"title,omitempty" db:"title"`
	ParentUUID         *string    `json:"parent_uuid,omitempty" db:"parent_uuid"`
	ETag               int64      `json:"etag" db:"etag"`
	CreatedAt          time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at" db:"updated_at"`
	ArchivedAt         *time.Time `json:"archived_at,omitempty" db:"archived_at"`
	CreatedByActorUUID string     `json:"created_by_actor_uuid" db:"created_by_actor_uuid"`
	UpdatedByActorUUID string     `json:"updated_by_actor_uuid" db:"updated_by_actor_uuid"`
}

// Task represents a task
type Task struct {
	UUID               string     `json:"uuid" db:"uuid"`
	ID                 string     `json:"id" db:"id"`
	Slug               string     `json:"slug" db:"slug"`
	Title              string     `json:"title" db:"title"`
	ProjectUUID        string     `json:"project_uuid" db:"project_uuid"`
	State              string     `json:"state" db:"state"` // open, completed, archived
	Priority           int        `json:"priority" db:"priority"` // 1-4, 1 is highest
	StartAt            *time.Time `json:"start_at,omitempty" db:"start_at"`
	DueAt              *time.Time `json:"due_at,omitempty" db:"due_at"`
	Labels             *string    `json:"labels,omitempty" db:"labels"` // JSON array
	Description        string     `json:"description" db:"description"`
	ETag               int64      `json:"etag" db:"etag"`
	CreatedAt          time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at" db:"updated_at"`
	CompletedAt        *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	ArchivedAt         *time.Time `json:"archived_at,omitempty" db:"archived_at"`
	CreatedByActorUUID string     `json:"created_by_actor_uuid" db:"created_by_actor_uuid"`
	UpdatedByActorUUID string     `json:"updated_by_actor_uuid" db:"updated_by_actor_uuid"`
}

// Comment represents a comment on a task
type Comment struct {
	UUID               string     `json:"uuid" db:"uuid"`
	ID                 string     `json:"id" db:"id"`
	TaskUUID           string     `json:"task_uuid" db:"task_uuid"`
	ActorUUID          string     `json:"actor_uuid" db:"actor_uuid"`
	Body               string     `json:"body" db:"body"`
	Meta               *string    `json:"meta,omitempty" db:"meta"` // JSON optional metadata for agents/tools
	ETag               int64      `json:"etag" db:"etag"`
	CreatedAt          time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt          *time.Time `json:"updated_at,omitempty" db:"updated_at"` // nullable; reserved for future editable comments
	DeletedAt          *time.Time `json:"deleted_at,omitempty" db:"deleted_at"` // nullable; soft delete timestamp
	DeletedByActorUUID *string    `json:"deleted_by_actor_uuid,omitempty" db:"deleted_by_actor_uuid"` // nullable; actor who soft-deleted
}

// Attachment represents a file attachment
type Attachment struct {
	UUID               string    `json:"uuid" db:"uuid"`
	ID                 string    `json:"id" db:"id"`
	TaskUUID           string    `json:"task_uuid" db:"task_uuid"`
	Filename           string    `json:"filename" db:"filename"`
	RelativePath       string    `json:"relative_path" db:"relative_path"`
	MimeType           *string   `json:"mime_type,omitempty" db:"mime_type"`
	SizeBytes          int64     `json:"size_bytes" db:"size_bytes"`
	Checksum           *string   `json:"checksum,omitempty" db:"checksum"`
	CreatedAt          time.Time `json:"created_at" db:"created_at"`
	CreatedByActorUUID string    `json:"created_by_actor_uuid" db:"created_by_actor_uuid"`
}

// Event represents an event in the event log
type Event struct {
	ID           int64     `json:"id" db:"id"`
	Timestamp    time.Time `json:"timestamp" db:"timestamp"`
	ActorUUID    *string   `json:"actor_uuid,omitempty" db:"actor_uuid"`
	ResourceType string    `json:"resource_type" db:"resource_type"`
	ResourceUUID *string   `json:"resource_uuid,omitempty" db:"resource_uuid"`
	EventType    string    `json:"event_type" db:"event_type"`
	ETag         *int64    `json:"etag,omitempty" db:"etag"`
	Payload      *string   `json:"payload,omitempty" db:"payload"` // JSON
}

// GetLabels parses the labels JSON into a string slice
func (t *Task) GetLabels() ([]string, error) {
	if t.Labels == nil || *t.Labels == "" {
		return []string{}, nil
	}
	var labels []string
	if err := json.Unmarshal([]byte(*t.Labels), &labels); err != nil {
		return nil, err
	}
	return labels, nil
}

// SetLabels sets the labels from a string slice
func (t *Task) SetLabels(labels []string) error {
	if labels == nil {
		labels = []string{}
	}
	data, err := json.Marshal(labels)
	if err != nil {
		return err
	}
	s := string(data)
	t.Labels = &s
	return nil
}

// GetMeta parses the meta JSON into a map
func (c *Comment) GetMeta() (map[string]interface{}, error) {
	if c.Meta == nil || *c.Meta == "" {
		return map[string]interface{}{}, nil
	}
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(*c.Meta), &meta); err != nil {
		return nil, err
	}
	return meta, nil
}

// SetMeta sets the meta from a map
func (c *Comment) SetMeta(meta map[string]interface{}) error {
	if meta == nil {
		meta = map[string]interface{}{}
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	s := string(data)
	c.Meta = &s
	return nil
}
