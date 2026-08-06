//go:build wrkq_local

package wrkfapi

import (
	"context"

	"github.com/lherron/wrkq/internal/workflow"
)

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
		Task:             stringFromAny(out["task"]),
		InstanceID:       stringFromAny(out["instanceId"]),
		SuspensionID:     stringFromAny(out["suspensionId"]),
		Disposition:      stringFromAny(out["disposition"]),
		State:            stateFromAny(out["state"]),
		Revision:         int64FromAny(out["revision"]),
		EventID:          stringFromAny(out["eventId"]),
		Effects:          effectsFromAny(out["effects"]),
		TerminalizedRuns: terminalizedRunsFromAny(out["terminalizedRuns"]),
	}
	if inst, ok := out["instance"].(workflow.Instance); ok {
		res.Instance = &inst
	}
	return res
}
