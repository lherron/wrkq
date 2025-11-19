package domain

import (
	"fmt"
	"time"
)

// ValidateState validates a task state
func ValidateState(state string) error {
	switch state {
	case "open", "in_progress", "completed", "archived":
		return nil
	default:
		return fmt.Errorf("invalid state: must be one of: open, in_progress, completed, archived")
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
	case "task", "container", "attachment", "actor", "config", "system":
		return nil
	default:
		return fmt.Errorf("invalid resource type: must be one of: task, container, attachment, actor, config, system")
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
