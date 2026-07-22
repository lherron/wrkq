package wrkfapi

import (
	"context"

	"github.com/lherron/wrkq/internal/workflow"
)

type InstanceCancelParams struct {
	TaskSelector   string `json:"task,omitempty"`
	InstanceID     string `json:"instanceId,omitempty"`
	ExpectRevision *int64 `json:"expectRevision,omitempty"`
	Explanation    string `json:"explanation,omitempty"`
	PrincipalRef   string `json:"principal_ref,omitempty"`
	Role           string `json:"role,omitempty"`
}

type InstanceCancelResult struct {
	Task             string                            `json:"task"`
	InstanceID       string                            `json:"instanceId"`
	State            workflow.State                    `json:"state"`
	Revision         int64                             `json:"revision"`
	EventID          string                            `json:"eventId"`
	Effects          []workflow.Effect                 `json:"effects"`
	TerminalizedRuns []workflow.TerminalizedRunSummary `json:"terminalizedRuns"`
	Instance         *workflow.Instance                `json:"instance,omitempty"`
}

func (api *API) InstanceCancel(ctx context.Context, params InstanceCancelParams) (*InstanceCancelResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out, err := api.service.CancelInstance(workflow.CancelInstanceParams{
		Task: params.TaskSelector, InstanceID: params.InstanceID, ExpectRevision: params.ExpectRevision,
		Explanation: params.Explanation, PrincipalRef: params.PrincipalRef, Role: params.Role,
	})
	if err != nil {
		return nil, normalizeError(err)
	}
	res := &InstanceCancelResult{
		Task: stringFromAny(out["task"]), InstanceID: stringFromAny(out["instanceId"]), State: stateFromAny(out["state"]),
		Revision: int64FromAny(out["revision"]), EventID: stringFromAny(out["eventId"]), Effects: effectsFromAny(out["effects"]),
		TerminalizedRuns: terminalizedRunsFromAny(out["terminalizedRuns"]),
	}
	if inst, ok := out["instance"].(workflow.Instance); ok {
		res.Instance = &inst
	}
	return res, nil
}

func terminalizedRunsFromAny(value interface{}) []workflow.TerminalizedRunSummary {
	switch runs := value.(type) {
	case []workflow.TerminalizedRunSummary:
		return runs
	case nil:
		return []workflow.TerminalizedRunSummary{}
	default:
		return []workflow.TerminalizedRunSummary{}
	}
}
