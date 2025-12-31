package domain

import (
	"fmt"
	"regexp"
	"time"
)

// UUIDv4Regex validates lowercase UUIDv4 format
var UUIDv4Regex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// ValidateUUID validates a UUID v4 format (lowercase with hyphens)
func ValidateUUID(uuid string) error {
	if !UUIDv4Regex.MatchString(uuid) {
		return fmt.Errorf("invalid UUID: must be lowercase UUIDv4 format (e.g., 550e8400-e29b-41d4-a716-446655440000)")
	}
	return nil
}

// ValidateState validates a task state
func ValidateState(state string) error {
	switch state {
	case "draft", "open", "in_progress", "completed", "blocked", "cancelled", "archived", "deleted":
		return nil
	default:
		return fmt.Errorf("invalid state: must be one of: draft, open, in_progress, completed, blocked, cancelled, archived, deleted")
	}
}

// ValidatePriority validates a task priority
func ValidatePriority(priority int) error {
	if priority < 1 || priority > 4 {
		return fmt.Errorf("invalid priority: must be between 1 and 4")
	}
	return nil
}

// ValidateActorRole validates an actor role
func ValidateActorRole(role string) error {
	switch role {
	case "human", "agent", "system":
		return nil
	default:
		return fmt.Errorf("invalid actor role: must be one of: human, agent, system")
	}
}

// ValidateResourceType validates an event resource type
func ValidateResourceType(resourceType string) error {
	switch resourceType {
	case "task", "container", "attachment", "actor", "config", "system", "section", "task_relation":
		return nil
	default:
		return fmt.Errorf("invalid resource type: must be one of: task, container, attachment, actor, config, system, section, task_relation")
	}
}

// ValidateContainerKind validates a container kind
func ValidateContainerKind(kind string) error {
	switch kind {
	case "project", "feature", "area", "misc":
		return nil
	default:
		return fmt.Errorf("invalid container kind: must be one of: project, feature, area, misc")
	}
}

// ValidateTaskKind validates a task kind
func ValidateTaskKind(kind string) error {
	switch kind {
	case "task", "subtask", "spike", "bug", "chore":
		return nil
	default:
		return fmt.Errorf("invalid task kind: must be one of: task, subtask, spike, bug, chore")
	}
}

// ValidateSectionRole validates a section role
func ValidateSectionRole(role string) error {
	switch role {
	case "backlog", "ready", "active", "review", "done":
		return nil
	default:
		return fmt.Errorf("invalid section role: must be one of: backlog, ready, active, review, done")
	}
}

// ValidateTaskRelationKind validates a task relation kind
func ValidateTaskRelationKind(kind string) error {
	switch kind {
	case "blocks", "relates_to", "duplicates":
		return nil
	default:
		return fmt.Errorf("invalid task relation kind: must be one of: blocks, relates_to, duplicates")
	}
}

// ValidateTimestamp validates and parses an ISO8601 timestamp
func ValidateTimestamp(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid timestamp format: expected ISO8601/RFC3339")
	}
	return t, nil
}

// ETagMismatchError is returned when an etag doesn't match
type ETagMismatchError struct {
	Expected int64
	Actual   int64
}

func (e *ETagMismatchError) Error() string {
	return fmt.Sprintf("etag mismatch: expected %d, got %d", e.Expected, e.Actual)
}

// CheckETag validates an etag against the current value
func CheckETag(expected, actual int64) error {
	if expected != actual {
		return &ETagMismatchError{Expected: expected, Actual: actual}
	}
	return nil
}
