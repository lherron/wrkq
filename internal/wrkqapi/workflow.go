package wrkqapi

import (
	"context"
	"encoding/json"

	"github.com/lherron/wrkq/internal/workflow"
)

const nsWorkflowAttach = "wrkq.workflow.attach"

// WorkflowAttach binds a workflow template to a task (a wrkq verb) and returns a
// WrkqWorkflowAttachResult. Idempotency is mandatory when an idempotencyKey is
// supplied: a replay returns the same instance with attached=false (§8.2).
func (a *API) WorkflowAttach(ctx context.Context, p WorkflowAttachParams) (*WrkqWorkflowAttachResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.wf == nil {
		return nil, NewInternalError(errWorkflowUnavailable)
	}
	taskUUID, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}

	var requestHash string
	if p.IdempotencyKey != "" {
		hp := p
		hp.IdempotencyKey = ""
		requestHash = canonicalRequestHash(hp)
		if raw, ok, rerr := a.idempotentReplay(nsWorkflowAttach, p.IdempotencyKey, requestHash); rerr != nil {
			return nil, rerr
		} else if ok {
			var replay WrkqWorkflowAttachResult
			if uerr := json.Unmarshal(raw, &replay); uerr != nil {
				return nil, NewInternalError(uerr)
			}
			replay.Attached = false
			return &replay, nil
		}
	}

	inst, err := a.wf.TaskAttach(ctx, p.Task, p.Workflow, defaultString(p.Actor, a.defaultPrincipalRef), workflow.AttachTaskOptions{
		Supersede:             p.Supersede,
		PredecessorInstanceID: p.PredecessorInstanceID,
		PredecessorRevision:   p.PredecessorRevision,
		AttachDiscontinued:    p.AttachDiscontinued,
	})
	if err != nil {
		return nil, err
	}
	dto, err := a.loadTask(taskUUID)
	if err != nil {
		return nil, err
	}
	result := &WrkqWorkflowAttachResult{Task: dto, Instance: inst, Attached: true}
	if p.IdempotencyKey != "" {
		if serr := a.idempotentStore(nsWorkflowAttach, p.IdempotencyKey, requestHash, result); serr != nil {
			return nil, serr
		}
	}
	return result, nil
}

// WorkflowInspect returns the current workflow instance for a task, wrapped in a
// typed result envelope.
func (a *API) WorkflowInspect(ctx context.Context, p WorkflowTaskParams) (*WrkqWorkflowInspectResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.wf == nil {
		return nil, NewInternalError(errWorkflowUnavailable)
	}
	if _, err := a.resolveTaskUUID(p.Task); err != nil {
		return nil, err
	}
	inst, err := a.wf.TaskInspect(ctx, p.Task)
	if err != nil {
		return nil, err
	}
	return &WrkqWorkflowInspectResult{Instance: inst}, nil
}

// WorkflowTimeline returns the workflow event timeline for a task, wrapped in a
// typed result envelope with a non-nil events array.
func (a *API) WorkflowTimeline(ctx context.Context, p WorkflowTaskParams) (*WrkqWorkflowTimelineResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.wf == nil {
		return nil, NewInternalError(errWorkflowUnavailable)
	}
	if _, err := a.resolveTaskUUID(p.Task); err != nil {
		return nil, err
	}
	events, err := a.wf.TaskTimeline(ctx, p.Task)
	if err != nil {
		return nil, err
	}
	if events == nil {
		events = []workflow.Event{}
	}
	return &WrkqWorkflowTimelineResult{Events: events}, nil
}

// WorkflowRefresh updates the workflow instance context from the current task
// document and returns the same wrapped shape as inspect.
func (a *API) WorkflowRefresh(ctx context.Context, taskSelector, actor string) (*WrkqWorkflowInspectResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.wf == nil {
		return nil, NewInternalError(errWorkflowUnavailable)
	}
	if _, err := a.resolveTaskUUID(taskSelector); err != nil {
		return nil, err
	}
	inst, err := a.wf.TaskRefresh(ctx, taskSelector, defaultString(actor, a.defaultPrincipalRef))
	if err != nil {
		return nil, err
	}
	return &WrkqWorkflowInspectResult{Instance: inst}, nil
}

func defaultString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

var errWorkflowUnavailable = &simpleError{"workflow API is unavailable"}

type simpleError struct{ msg string }

func (e *simpleError) Error() string { return e.msg }
