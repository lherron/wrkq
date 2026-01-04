// Package snapshot provides canonical JSON state snapshots for wrkq.
//
// Snapshots are deterministic JSON representations of the entire wrkq database
// state, designed for use in patch-first Git workflows. They follow the
// PATCH-MODE.md specification with JCS-like canonicalization.
package snapshot

import (
	"time"
)

// Snapshot represents the complete canonical state of a wrkq database.
// The JSON shape follows PATCH-MODE.md §2.2.
type Snapshot struct {
	Meta       Meta                      `json:"meta"`
	Actors     map[string]ActorEntry     `json:"actors,omitempty"`
	Containers map[string]ContainerEntry `json:"containers,omitempty"`
	Tasks      map[string]TaskEntry      `json:"tasks,omitempty"`
	Comments   map[string]CommentEntry   `json:"comments,omitempty"`
	Links      map[string]LinkEntry      `json:"links,omitempty"`
	Events     map[string]EventEntry     `json:"events,omitempty"`
}

// Meta contains snapshot metadata.
type Meta struct {
	SchemaVersion           int    `json:"schema_version"`
	SnapshotRev             string `json:"snapshot_rev,omitempty"`
	GeneratedAt             string `json:"generated_at,omitempty"`
	MachineInterfaceVersion int    `json:"machine_interface_version"`
}

// ActorEntry represents an actor in the snapshot.
// Keys under "actors" are UUIDs.
type ActorEntry struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name,omitempty"`
	Role        string `json:"role"`
	Meta        string `json:"meta,omitempty"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// ContainerEntry represents a container (project/subproject) in the snapshot.
// Keys under "containers" are UUIDs.
type ContainerEntry struct {
	ID         string `json:"id"`
	Slug       string `json:"slug"`
	Title      string `json:"title,omitempty"`
	ParentUUID string `json:"parent_uuid,omitempty"`
	ETag       int64  `json:"etag"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
	ArchivedAt string `json:"archived_at,omitempty"`
	CreatedBy  string `json:"created_by"`
	UpdatedBy  string `json:"updated_by"`
}

// TaskEntry represents a task in the snapshot.
// Keys under "tasks" are UUIDs.
type TaskEntry struct {
	ID                   string   `json:"id"`
	Slug                 string   `json:"slug"`
	Title                string   `json:"title"`
	ProjectUUID          string   `json:"project_uuid"`
	RequestedByProjectID string   `json:"requested_by_project_id,omitempty"`
	AssignedProjectID    string   `json:"assigned_project_id,omitempty"`
	AcknowledgedAt       string   `json:"acknowledged_at,omitempty"`
	Resolution           string   `json:"resolution,omitempty"`
	State                string   `json:"state"`
	Priority             int      `json:"priority"`
	StartAt              string   `json:"start_at,omitempty"`
	DueAt                string   `json:"due_at,omitempty"`
	Labels               []string `json:"labels,omitempty"`
	Description          string   `json:"description,omitempty"`
	ETag                 int64    `json:"etag"`
	CreatedAt            string   `json:"created_at"`
	UpdatedAt            string   `json:"updated_at"`
	CompletedAt          string   `json:"completed_at,omitempty"`
	ArchivedAt           string   `json:"archived_at,omitempty"`
	CreatedBy            string   `json:"created_by"`
	UpdatedBy            string   `json:"updated_by"`
}

// CommentEntry represents a comment in the snapshot.
// Keys under "comments" are UUIDs.
type CommentEntry struct {
	ID        string `json:"id"`
	TaskUUID  string `json:"task_uuid"`
	ActorUUID string `json:"actor_uuid"`
	Body      string `json:"body"`
	Meta      string `json:"meta,omitempty"`
	ETag      int64  `json:"etag"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at,omitempty"`
	DeletedAt string `json:"deleted_at,omitempty"`
	DeletedBy string `json:"deleted_by,omitempty"`
}

// LinkEntry represents a link/dependency in the snapshot.
// Keys under "links" are UUIDs.
type LinkEntry struct {
	ID         string `json:"id,omitempty"`
	SourceUUID string `json:"source_uuid"`
	TargetUUID string `json:"target_uuid"`
	LinkType   string `json:"link_type"`
	CreatedAt  string `json:"created_at"`
	CreatedBy  string `json:"created_by"`
}

// EventEntry represents minimal event metadata in the snapshot.
// Full event payloads are only included with --include-events.
type EventEntry struct {
	ID           int64  `json:"id"`
	Timestamp    string `json:"timestamp"`
	ActorUUID    string `json:"actor_uuid,omitempty"`
	ResourceType string `json:"resource_type"`
	ResourceUUID string `json:"resource_uuid,omitempty"`
	EventType    string `json:"event_type"`
	ETag         int64  `json:"etag,omitempty"`
	Payload      string `json:"payload,omitempty"`
}

// ExportOptions configures snapshot export behavior.
type ExportOptions struct {
	// OutputPath is the file to write to (default: .wrkq/state.json)
	OutputPath string
	// Canonical enables JCS canonicalization (default: true)
	Canonical bool
	// IncludeEvents includes full event log in snapshot
	IncludeEvents bool
}

// ImportOptions configures snapshot import behavior.
type ImportOptions struct {
	// InputPath is the file to read from (default: .wrkq/state.json)
	InputPath string
	// DryRun validates without writing
	DryRun bool
	// IfEmpty requires DB to be empty
	IfEmpty bool
	// Force allows importing into non-empty DB by truncating
	Force bool
}

// ExportResult contains the result of an export operation.
type ExportResult struct {
	OutputPath     string `json:"out"`
	SnapshotRev    string `json:"snapshot_rev"`
	ActorCount     int    `json:"actors"`
	ContainerCount int    `json:"containers"`
	TaskCount      int    `json:"tasks"`
	CommentCount   int    `json:"comments"`
	LinkCount      int    `json:"links,omitempty"`
	EventCount     int    `json:"events,omitempty"`
}

// ImportResult contains the result of an import operation.
type ImportResult struct {
	InputPath      string `json:"from"`
	SnapshotRev    string `json:"snapshot_rev"`
	ActorCount     int    `json:"actors"`
	ContainerCount int    `json:"containers"`
	TaskCount      int    `json:"tasks"`
	CommentCount   int    `json:"comments"`
	DryRun         bool   `json:"dry_run,omitempty"`
}

// VerifyResult contains the result of a verify operation.
type VerifyResult struct {
	InputPath   string `json:"input"`
	Valid       bool   `json:"valid"`
	SnapshotRev string `json:"snapshot_rev"`
	Message     string `json:"message,omitempty"`
}

// DefaultOutputPath is the default snapshot file location.
const DefaultOutputPath = ".wrkq/state.json"

// FormatTimestamp formats a time.Time as ISO-8601 with Z suffix.
func FormatTimestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05Z")
}

// ParseTimestamp parses an ISO-8601 timestamp.
func ParseTimestamp(s string) (time.Time, error) {
	return time.Parse("2006-01-02T15:04:05Z", s)
}
