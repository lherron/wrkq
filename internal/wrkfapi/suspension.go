package wrkfapi

import (
	"context"

	"github.com/lherron/wrkq/internal/workflow"
)

// SuspensionResolveParams is the RPC/CLI input to the atomic resolution command.
// The matching suspension id is the only gate; disposition is one of resume,
// close, or cancel; explanation is recorded free text; expectRevision is the
// ordinary CAS precondition.
type SuspensionResolveParams struct {
	SuspensionID   string `json:"suspensionId"`
	Disposition    string `json:"disposition"`
	Explanation    string `json:"explanation,omitempty"`
	ExpectRevision *int64 `json:"expectRevision,omitempty"`
	Role           string `json:"role,omitempty"`
	PrincipalRef   string `json:"principal_ref,omitempty"`
}

// SuspensionResolveResult is the resolution outcome: the cleared suspension id,
// the disposition applied, the instance's new state/revision, the resolution
// event id, and any disposition effects created.
type SuspensionResolveResult struct {
	Task         string             `json:"task,omitempty"`
	InstanceID   string             `json:"instanceId"`
	SuspensionID string             `json:"suspensionId"`
	Disposition  string             `json:"disposition"`
	State        workflow.State     `json:"state"`
	Revision     int64              `json:"revision"`
	EventID      string             `json:"eventId"`
	Effects      []workflow.Effect  `json:"effects"`
	Instance     *workflow.Instance `json:"instance,omitempty"`
}

// SuspensionResolve resolves the active suspension named by params.SuspensionID
// with the given disposition, atomically — the single exception to the
// suspended-write gate.
func (api *API) SuspensionResolve(ctx context.Context, params SuspensionResolveParams) (*SuspensionResolveResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out, err := api.service.ResolveSuspension(workflow.ResolveSuspensionParams{
		SuspensionID:   params.SuspensionID,
		Disposition:    params.Disposition,
		Explanation:    params.Explanation,
		ExpectRevision: params.ExpectRevision,
		PrincipalRef:   params.PrincipalRef,
		Role:           params.Role,
	})
	if err != nil {
		return nil, normalizeError(err)
	}
	return suspensionResolveResultFromAny(out), nil
}

func suspensionResolveResultFromAny(out map[string]interface{}) *SuspensionResolveResult {
	res := &SuspensionResolveResult{
		Task:         stringFromAny(out["task"]),
		InstanceID:   stringFromAny(out["instanceId"]),
		SuspensionID: stringFromAny(out["suspensionId"]),
		Disposition:  stringFromAny(out["disposition"]),
		State:        stateFromAny(out["state"]),
		Revision:     int64FromAny(out["revision"]),
		EventID:      stringFromAny(out["eventId"]),
		Effects:      effectsFromAny(out["effects"]),
	}
	if inst, ok := out["instance"].(workflow.Instance); ok {
		res.Instance = &inst
	}
	return res
}
