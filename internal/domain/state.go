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

// AllStates lists every valid task state in lifecycle order. It exists so
// callers can render a complete, ordered validation message without
// re-listing the set and drifting from ParseState.
func AllStates() []string {
	return []string{
		string(StateIdea), string(StateDraft), string(StateOpen), string(StateInProgress),
		string(StateCompleted), string(StateBlocked), string(StateCancelled),
		string(StateArchived), string(StateDeleted),
	}
}

// IsValidState reports whether state is a valid task lifecycle state.
func IsValidState(state string) bool {
	_, err := ParseState(state)
	return err == nil
}

// DefaultViewStates is the producer-owned actionable discovery set: the states
// a view shows when the caller names none. tree/ls send it explicitly from the
// CLI (their server refuses a request without `states`); find's producer
// applies it when `state` is empty (T-06964). One list, so the views agree.
//
// Lance's ruling, 2026-09-07: exactly draft, open and in_progress. Narrower
// than the full non-terminal set because these are working views.
//
// Amended by Lance, 2026-09-17: blocked joins the set. Blocked work is working
// view work — it is waiting on something, not finished with something, and
// hiding it by default made live members of an active campaign (T-08567,
// T-08594) invisible in the view their owners read. idea stays out: it is the
// only remaining non-terminal state that is genuinely not yet work.
func DefaultViewStates() []string {
	return []string{string(StateDraft), string(StateOpen), string(StateInProgress), string(StateBlocked)}
}
