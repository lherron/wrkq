package domain

import (
	"encoding/json"
	"time"
)

// ContainerKind represents the type of container
type ContainerKind string

const (
	ContainerKindProject ContainerKind = "project"
	ContainerKindFeature ContainerKind = "feature"
	ContainerKindArea    ContainerKind = "area"
	ContainerKindMisc    ContainerKind = "misc"
)

// TaskKind represents the type of task
type TaskKind string

const (
	TaskKindTask    TaskKind = "task"
	TaskKindSubtask TaskKind = "subtask"
	TaskKindSpike   TaskKind = "spike"
	TaskKindBug     TaskKind = "bug"
	TaskKindChore   TaskKind = "chore"
)

// TaskResolution represents the resolution of a completed task
type TaskResolution string

const (
	TaskResolutionDone      TaskResolution = "done"
	TaskResolutionWontDo    TaskResolution = "wont_do"
	TaskResolutionDuplicate TaskResolution = "duplicate"
	TaskResolutionNeedsInfo TaskResolution = "needs_info"
)

// SectionRole represents the semantic role of a section in the kanban workflow
type SectionRole string

const (
	SectionRoleBacklog SectionRole = "backlog"
	SectionRoleReady   SectionRole = "ready"
	SectionRoleActive  SectionRole = "active"
	SectionRoleReview  SectionRole = "review"
	SectionRoleDone    SectionRole = "done"
)

// TaskRelationKind represents the type of relationship between tasks
type TaskRelationKind string

const (
	TaskRelationBlocks     TaskRelationKind = "blocks"
	TaskRelationRelatesTo  TaskRelationKind = "relates_to"
	TaskRelationDuplicates TaskRelationKind = "duplicates"
)

// Actor represents an actor in the system
type Actor struct {
	UUID        string    `json:"uuid" db:"uuid"`
	ID          string    `json:"id" db:"id"`
	Slug        string    `json:"slug" db:"slug"`
	DisplayName *string   `json:"display_name,omitempty" db:"display_name"`
	Role        string    `json:"role" db:"role"`           // human, agent, system
	Meta        *string   `json:"meta,omitempty" db:"meta"` // JSON
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Container represents a project or subproject
type Container struct {
	UUID               string        `json:"uuid" db:"uuid"`
	ID                 string        `json:"id" db:"id"`
	Slug               string        `json:"slug" db:"slug"`
	Title              *string       `json:"title,omitempty" db:"title"`
	ParentUUID         *string       `json:"parent_uuid,omitempty" db:"parent_uuid"`
	Kind               ContainerKind `json:"kind" db:"kind"`
	SectionUUID        *string       `json:"section_uuid,omitempty" db:"section_uuid"`
	SortIndex          int           `json:"sort_index" db:"sort_index"`
	WebhookURLs        *string       `json:"webhook_urls,omitempty" db:"webhook_urls"`
	ETag               int64         `json:"etag" db:"etag"`
	CreatedAt          time.Time     `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at" db:"updated_at"`
	ArchivedAt         *time.Time    `json:"archived_at,omitempty" db:"archived_at"`
	CreatedByActorUUID string        `json:"created_by_actor_uuid" db:"created_by_actor_uuid"`
	UpdatedByActorUUID string        `json:"updated_by_actor_uuid" db:"updated_by_actor_uuid"`
}

// Task represents a task
type Task struct {
	UUID                 string     `json:"uuid" db:"uuid"`
	ID                   string     `json:"id" db:"id"`
	Slug                 string     `json:"slug" db:"slug"`
	Title                string     `json:"title" db:"title"`
	ProjectUUID          string     `json:"project_uuid" db:"project_uuid"`
	RequestedByProjectID *string    `json:"requested_by_project_id,omitempty" db:"requested_by_project_id"`
	AssignedProjectID    *string    `json:"assigned_project_id,omitempty" db:"assigned_project_id"`
	State                string     `json:"state" db:"state"`       // idea, draft, open, in_progress, completed, blocked, cancelled, archived, deleted
	Priority             int        `json:"priority" db:"priority"` // 1-4, 1 is highest
	Kind                 TaskKind   `json:"kind" db:"kind"`         // task, subtask, spike, bug, chore
	ParentTaskUUID       *string    `json:"parent_task_uuid,omitempty" db:"parent_task_uuid"`
	AssigneeActorUUID    *string    `json:"assignee_actor_uuid,omitempty" db:"assignee_actor_uuid"`
	AcknowledgedAt       *time.Time `json:"acknowledged_at,omitempty" db:"acknowledged_at"`
	Resolution           *string    `json:"resolution,omitempty" db:"resolution"`
	CPProjectID          *string    `json:"cp_project_id,omitempty" db:"cp_project_id"`
	CPWorkItemID         *string    `json:"cp_work_item_id,omitempty" db:"cp_work_item_id"`
	CPRunID              *string    `json:"cp_run_id,omitempty" db:"cp_run_id"`
	CPSessionID          *string    `json:"cp_session_id,omitempty" db:"cp_session_id"`
	SDKSessionID         *string    `json:"sdk_session_id,omitempty" db:"sdk_session_id"`
	RunStatus            *string    `json:"run_status,omitempty" db:"run_status"`
	StartAt              *time.Time `json:"start_at,omitempty" db:"start_at"`
	DueAt                *time.Time `json:"due_at,omitempty" db:"due_at"`
	Labels               *string    `json:"labels,omitempty" db:"labels"` // JSON array
	Meta                 *string    `json:"meta,omitempty" db:"meta"`     // JSON object
	Description          string     `json:"description" db:"description"`
	ETag                 int64      `json:"etag" db:"etag"`
	CreatedAt            time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at" db:"updated_at"`
	CompletedAt          *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	ArchivedAt           *time.Time `json:"archived_at,omitempty" db:"archived_at"`
	DeletedAt            *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`
	CreatedByActorUUID   string     `json:"created_by_actor_uuid" db:"created_by_actor_uuid"`
	UpdatedByActorUUID   string     `json:"updated_by_actor_uuid" db:"updated_by_actor_uuid"`
}

// Section represents a kanban column/lane in a project
type Section struct {
	UUID        string      `json:"uuid" db:"uuid"`
	ID          string      `json:"id" db:"id"`
	ProjectUUID string      `json:"project_uuid" db:"project_uuid"`
	Slug        string      `json:"slug" db:"slug"`
	Title       string      `json:"title" db:"title"`
	OrderIndex  int         `json:"order_index" db:"order_index"`
	Role        SectionRole `json:"role" db:"role"`
	IsDefault   bool        `json:"is_default" db:"is_default"`
	WIPLimit    *int        `json:"wip_limit,omitempty" db:"wip_limit"`
	Meta        *string     `json:"meta,omitempty" db:"meta"` // JSON
	CreatedAt   time.Time   `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at" db:"updated_at"`
	ArchivedAt  *time.Time  `json:"archived_at,omitempty" db:"archived_at"`
}

// TaskRelation represents a dependency or relationship between tasks
type TaskRelation struct {
	FromTaskUUID       string           `json:"from_task_uuid" db:"from_task_uuid"`
	ToTaskUUID         string           `json:"to_task_uuid" db:"to_task_uuid"`
	Kind               TaskRelationKind `json:"kind" db:"kind"`
	Meta               *string          `json:"meta,omitempty" db:"meta"` // JSON
	CreatedAt          time.Time        `json:"created_at" db:"created_at"`
	CreatedByActorUUID string           `json:"created_by_actor_uuid" db:"created_by_actor_uuid"`
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
	UpdatedAt          *time.Time `json:"updated_at,omitempty" db:"updated_at"`                       // nullable; reserved for future editable comments
	DeletedAt          *time.Time `json:"deleted_at,omitempty" db:"deleted_at"`                       // nullable; soft delete timestamp
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
