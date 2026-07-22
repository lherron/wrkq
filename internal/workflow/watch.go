package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
)

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

func (s *Service) WatchSnapshot(selector, until string) (*WatchSnapshot, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, validationError("selector", "watch selector is required", "task|instance|run", nil, "supply a task selector, workflow instance id, or run id")
	}
	until, err := NormalizeWatchUntil(until)
	if err != nil {
		return nil, err
	}

	switch InferWatchTargetKind(selector) {
	case WatchTargetRun:
		if until == WatchUntilWaiting || until == WatchUntilSuspended {
			return nil, validationError("until", "run watches do not support instance conditions", "terminal|closed", []string{WatchUntilTerminal, WatchUntilClosed}, "watch the workflow instance for --until waiting or --until suspended")
		}
		run, err := s.ShowRun(selector)
		if err != nil {
			return nil, err
		}
		inst, err := s.instanceByID(run.InstanceID)
		if err != nil {
			return nil, err
		}
		return runWatchSnapshot(selector, until, inst, run), nil
	case WatchTargetInstance:
		inst, err := s.instanceByID(selector)
		if err != nil {
			return nil, err
		}
		return instanceWatchSnapshot(WatchTargetInstance, selector, until, inst), nil
	default:
		inst, err := s.LatestInstance(selector)
		if err != nil {
			return nil, err
		}
		return instanceWatchSnapshot(WatchTargetTask, selector, until, inst), nil
	}
}

func (s *Service) WatchEvents(selector string, afterSeq int64, limit int) ([]Event, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, validationError("selector", "watch selector is required", "task|instance|run", nil, "supply a task selector, workflow instance id, or run id")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	var target WatchTarget
	switch InferWatchTargetKind(selector) {
	case WatchTargetRun:
		run, err := s.ShowRun(selector)
		if err != nil {
			return nil, err
		}
		target = WatchTarget{Kind: WatchTargetRun, Selector: selector, InstanceID: run.InstanceID, RunID: run.ID}
	case WatchTargetInstance:
		inst, err := s.instanceByID(selector)
		if err != nil {
			return nil, err
		}
		target = WatchTarget{Kind: WatchTargetInstance, Selector: selector, InstanceID: inst.ID, TaskRef: inst.TaskRef}
	default:
		inst, err := s.LatestInstance(selector)
		if err != nil {
			return nil, err
		}
		target = WatchTarget{Kind: WatchTargetTask, Selector: selector, InstanceID: inst.ID, TaskRef: inst.TaskRef}
	}
	return s.WatchEventsForTarget(target, afterSeq, limit)
}

// WatchEventsForTarget reads one bounded page for an already-resolved target.
// Keeping resolution outside this query lets the RPC watch cursor bind the page
// to the exact instance/run identity it names, even when a task selector rolls
// over to a successor instance between client polls.
func (s *Service) WatchEventsForTarget(target WatchTarget, afterSeq int64, limit int) ([]Event, error) {
	if strings.TrimSpace(target.InstanceID) == "" {
		return nil, validationError("target.instanceId", "watch target instance id is required", "resolved workflow instance id", nil, "resolve the watch selector before reading events")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	where := "instance_id = ? AND seq > ?"
	args := []interface{}{target.InstanceID, afterSeq}
	if target.RunID != "" {
		where += " AND run_id = ?"
		args = append(args, target.RunID)
	}
	args = append(args, limit)
	rows, err := s.db.Query(`
		SELECT id, instance_id, seq, schema_version, type, COALESCE(principal_ref, actor, ''), COALESCE(role,''), COALESCE(run_id,''),
		       COALESCE(observed_revision,0), next_revision, COALESCE(task_doc_etag,''), COALESCE(task_doc_hash,''),
		       COALESCE(idempotency_key,''), COALESCE(result,''), COALESCE(rejection_code,''), payload_json, created_at
		FROM workflow_events
		WHERE `+where+`
		ORDER BY seq
		LIMIT ?
	`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []Event
	for rows.Next() {
		var e Event
		var payload string
		if err := rows.Scan(&e.ID, &e.InstanceID, &e.Seq, &e.SchemaVersion, &e.Type, &e.PrincipalRef, &e.Role, &e.RunID, &e.ObservedRevision, &e.NextRevision, &e.TaskDocEtag, &e.TaskDocHash, &e.IdempotencyKey, &e.Result, &e.RejectionCode, &payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Payload = json.RawMessage(payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

func instanceWatchSnapshot(kind, selector, until string, inst *Instance) *WatchSnapshot {
	out := &WatchSnapshot{
		Target: WatchTarget{Kind: kind, Selector: selector, InstanceID: inst.ID, TaskRef: inst.TaskRef},
		Until:  until, Class: WatchClassPending, Status: inst.Status, Phase: inst.Phase, Outcome: inst.Outcome, Instance: inst,
	}
	switch until {
	case WatchUntilSuspended:
		out.Met = inst.Suspension != nil
		if out.Met {
			out.Class = WatchClassSuspended
			out.ExitCode = 0
		}
	case WatchUntilWaiting:
		out.Met = inst.Status == "waiting"
		if out.Met {
			out.Class = WatchClassWaiting
			out.ExitCode = 0
		}
	case WatchUntilTerminal, WatchUntilClosed:
		out.Met = inst.Status == "closed"
		if out.Met {
			out.Class, out.ExitCode = classifyInstanceTerminal(inst)
		}
	}
	return out
}

func runWatchSnapshot(selector, until string, inst *Instance, run *Run) *WatchSnapshot {
	out := &WatchSnapshot{
		Target: WatchTarget{Kind: WatchTargetRun, Selector: selector, InstanceID: run.InstanceID, RunID: run.ID, TaskRef: inst.TaskRef},
		Until:  until, Class: WatchClassPending, Status: run.Status, Instance: inst, Run: run,
	}
	out.Met = isTerminalRunStatus(run.Status)
	if out.Met {
		out.Class, out.ExitCode = classifyRunTerminal(run.Status)
	}
	return out
}

func classifyRunTerminal(status string) (string, int) {
	status = strings.ToLower(strings.TrimSpace(status))
	switch {
	case status == "completed" || status == "succeeded" || status == "success":
		return WatchClassSuccess, 0
	case isCancelledToken(status):
		return WatchClassCancelled, 11
	default:
		return WatchClassFailure, 10
	}
}

func classifyInstanceTerminal(inst *Instance) (string, int) {
	values := []string{inst.Status, inst.Phase, inst.Outcome}
	for _, v := range values {
		if strings.ToLower(strings.TrimSpace(v)) == "superseded" {
			return WatchClassFailure, 10
		}
		if isCancelledToken(v) {
			return WatchClassCancelled, 11
		}
	}
	for _, v := range values {
		if isFailureToken(v) {
			return WatchClassFailure, 10
		}
	}
	return WatchClassSuccess, 0
}

func isCancelledToken(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "cancelled", "canceled", "aborted":
		return true
	default:
		return false
	}
}

func isFailureToken(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "failed", "failure", "error", "errored", "semantic_blocked", "operational_failed", "operator_required", "blocked":
		return true
	default:
		return false
	}
}

func runLifecyclePayload(run *Run) map[string]interface{} {
	return map[string]interface{}{
		"runId":          run.ID,
		"instanceId":     run.InstanceID,
		"role":           run.Role,
		"principal_ref":  run.PrincipalRef,
		"deliveryRef":    run.DeliveryRef,
		"lane":           run.Lane,
		"externalRunRef": run.ExternalRunRef,
		"action":         run.Action,
		"status":         run.Status,
		"startedAt":      run.StartedAt,
		"completedAt":    run.CompletedAt,
		"terminalResult": run.TerminalResult,
	}
}

func taskDocEtagInt(inst *Instance) int64 {
	var out int64
	_, _ = fmt.Sscan(inst.TaskDocEtag, &out)
	return out
}
