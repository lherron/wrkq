package workflow

import "strings"

const (
	WatchTargetTask     = "task"
	WatchTargetInstance = "instance"
	WatchTargetRun      = "run"

	WatchUntilTerminal  = "terminal"
	WatchUntilClosed    = "closed"
	WatchUntilWaiting   = "waiting"
	WatchUntilSuspended = "suspended"

	WatchClassPending   = "pending"
	WatchClassSuccess   = "success"
	WatchClassWaiting   = "waiting"
	WatchClassSuspended = "suspended"
	WatchClassFailure   = "failure"
	WatchClassCancelled = "cancelled"
)

func NormalizeWatchUntil(until string) (string, error) {
	until = strings.ToLower(strings.TrimSpace(until))
	if until == "" {
		return WatchUntilTerminal, nil
	}
	switch until {
	case WatchUntilTerminal, WatchUntilClosed, WatchUntilWaiting, WatchUntilSuspended:
		return until, nil
	default:
		return "", validationError("until", "invalid watch predicate", "closed|suspended|terminal|waiting", []string{WatchUntilClosed, WatchUntilSuspended, WatchUntilTerminal, WatchUntilWaiting}, "set --until to closed, suspended, terminal, or waiting")
	}
}

func InferWatchTargetKind(selector string) string {
	selector = strings.TrimSpace(selector)
	switch {
	case strings.HasPrefix(selector, "run_"):
		return WatchTargetRun
	case strings.HasPrefix(selector, "wfi_"):
		return WatchTargetInstance
	default:
		return WatchTargetTask
	}
}

type WatchTarget struct {
	Kind       string `json:"kind"`
	Selector   string `json:"selector"`
	InstanceID string `json:"instanceId,omitempty"`
	RunID      string `json:"runId,omitempty"`
	TaskRef    string `json:"taskRef,omitempty"`
}

type WatchSnapshot struct {
	Target   WatchTarget `json:"target"`
	Until    string      `json:"until"`
	Met      bool        `json:"met"`
	Class    string      `json:"class"`
	ExitCode int         `json:"exitCode"`
	Status   string      `json:"status,omitempty"`
	Phase    string      `json:"phase,omitempty"`
	Outcome  string      `json:"outcome,omitempty"`
	Instance *Instance   `json:"instance,omitempty"`
	Run      *Run        `json:"run,omitempty"`
}
