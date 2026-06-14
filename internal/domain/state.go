package domain

import "fmt"

// State is a validated task lifecycle state wire value.
type State string

const (
	StateIdea       State = "idea"
	StateDraft      State = "draft"
	StateOpen       State = "open"
	StateInProgress State = "in_progress"
	StateCompleted  State = "completed"
	StateBlocked    State = "blocked"
	StateCancelled  State = "cancelled"
	StateArchived   State = "archived"
	StateDeleted    State = "deleted"
)

// ParseState validates and wraps a task lifecycle state wire value.
func ParseState(state string) (State, error) {
	switch State(state) {
	case StateIdea, StateDraft, StateOpen, StateInProgress, StateCompleted, StateBlocked, StateCancelled, StateArchived, StateDeleted:
		return State(state), nil
	default:
		return "", fmt.Errorf("invalid state: must be one of: idea, draft, open, in_progress, completed, blocked, cancelled, archived, deleted")
	}
}
