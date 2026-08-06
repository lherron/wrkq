//go:build wrkq_local

package wrkfapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/workflow"
)

func (api *API) ActionNext(ctx context.Context, params ActionNextParams) (*ActionNextResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := api.service.ActionNext(params)
	if err != nil {
		return nil, normalizeError(err)
	}
	return result, nil
}

func (api *API) ActionClaim(ctx context.Context, params ActionClaimParams) (*ActionClaimResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := api.service.ClaimAction(workflow.ClaimActionParams(params))
	if err != nil {
		return nil, normalizeError(err)
	}
	return result, nil
}

func (api *API) ActionSettle(ctx context.Context, params ActionSettleParams) (*ActionSettleResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mode, transitionID, err := parseTransitionDirective(params.Transition)
	if err != nil {
		return nil, err
	}
	out, err := api.service.SettleAction(workflow.SettleActionParams{
		ActionRunID:     params.ActionRunID,
		RunID:           params.RunID,
		OwnerToken:      params.OwnerToken,
		OwnerGeneration: params.OwnerGeneration,
		Result:          params.Result,
		Evidence:        actionEvidenceInput(params.Evidence),
		TransitionMode:  mode,
		TransitionID:    transitionID,
		TerminalSummary: params.TerminalSummary,
	})
	if err != nil {
		return nil, normalizeError(err)
	}
	result := &ActionSettleResult{Run: out.Run, Evidence: out.Evidence, Effects: out.Effects, Obligations: out.Obligations}
	if out.Transition != nil {
		tr := transitionResultFromAny(out.Transition)
		result.Transition = &tr
	}
	return result, nil
}

func (api *API) ActionStart(ctx context.Context, params ActionStartParams) (*ActionRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deliveryRef, err := canonicalDeliveryRef(params.DeliveryRef)
	if err != nil {
		return nil, err
	}
	run, err := api.service.StartAction(workflow.StartActionParams{
		Task:           params.Task,
		InstanceID:     params.InstanceID,
		Workflow:       params.Workflow,
		Action:         params.Action,
		Role:           params.Role,
		PrincipalRef:   params.PrincipalRef,
		Lane:           params.Lane,
		DeliveryRef:    deliveryRef,
		ExternalRunRef: params.ExternalRunRef,
		IdempotencyKey: params.IdempotencyKey,
		LeaseOwner:     params.LeaseOwner,
		LeaseMs:        params.LeaseMs,
	})
	if err != nil {
		return nil, normalizeError(err)
	}
	return run, nil
}

func (api *API) ActionBindExternal(ctx context.Context, params ActionBindExternalParams) (*ActionRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	deliveryRef, err := canonicalDeliveryRef(params.DeliveryRef)
	if err != nil {
		return nil, err
	}
	run, err := api.service.BindActionExternal(workflow.BindActionExternalParams{
		ActionRunID:    params.ActionRunID,
		ExternalRunRef: params.ExternalRunRef,
		DeliveryRef:    deliveryRef,
		Lane:           params.Lane,
		IdempotencyKey: params.IdempotencyKey,
	})
	if err != nil {
		return nil, normalizeError(err)
	}
	return run, nil
}

func (api *API) ActionComplete(ctx context.Context, params ActionCompleteParams) (*ActionCompleteResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	mode, transitionID, err := parseTransitionDirective(params.Transition)
	if err != nil {
		return nil, err
	}
	out, err := api.service.CompleteAction(workflow.CompleteActionParams{
		ActionRunID:              params.ActionRunID,
		LeaseToken:               params.LeaseToken,
		Evidence:                 actionEvidenceInput(params.Evidence),
		TransitionMode:           mode,
		TransitionID:             transitionID,
		TransitionIdempotencyKey: params.TransitionIdempotencyKey,
		RunSummary:               params.RunSummary,
	})
	if err != nil {
		return nil, normalizeError(err)
	}
	result := &ActionCompleteResult{Run: out.Run, Evidence: out.Evidence}
	if out.Transition != nil {
		tr := transitionResultFromAny(out.Transition)
		result.Transition = &tr
	}
	return result, nil
}

func (api *API) ActionFail(ctx context.Context, params ActionFailParams) (*ActionRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run, err := api.service.FailAction(workflow.FailActionParams{
		ActionRunID: params.ActionRunID,
		LeaseToken:  params.LeaseToken,
		Summary:     params.Summary,
		Evidence:    actionEvidenceInput(params.Evidence),
	})
	if err != nil {
		return nil, normalizeError(err)
	}
	return run, nil
}

func (api *API) ActionHeartbeat(ctx context.Context, params ActionHeartbeatParams) (*ActionRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run, err := api.service.HeartbeatAction(workflow.HeartbeatActionParams{
		ActionRunID: params.ActionRunID,
		LeaseToken:  params.LeaseToken,
		LeaseMs:     params.LeaseMs,
	})
	if err != nil {
		return nil, normalizeError(err)
	}
	return run, nil
}

func (api *API) ActionShow(ctx context.Context, params ActionShowParams) (*ActionRun, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	run, err := api.service.ShowAction(params.ActionRunID)
	if err != nil {
		return nil, normalizeError(err)
	}
	return run, nil
}

func (api *API) ActionList(ctx context.Context, params ActionListParams) (ActionListResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionListResult{}, err
	}
	runs, err := api.service.ListActions(workflow.ListActionsParams{
		Task:                   params.Task,
		InstanceID:             params.InstanceID,
		IncludeClosedInstances: params.IncludeClosedInstances,
		Status:                 params.Status,
		Action:                 params.Action,
		Limit:                  params.Limit,
	})
	if err != nil {
		return ActionListResult{}, normalizeError(err)
	}
	if runs == nil {
		runs = []workflow.ActionRun{}
	}
	return ActionListResult{Items: runs}, nil
}

func actionEvidenceInput(in *ActionEvidenceParams) *workflow.ActionEvidenceInput {
	if in == nil {
		return nil
	}
	return &workflow.ActionEvidenceInput{
		Kind:           in.Kind,
		Ref:            in.Ref,
		Summary:        in.Summary,
		Facts:          rawString(in.Facts),
		Data:           rawString(in.Data),
		ContentHash:    in.ContentHash,
		IdempotencyKey: in.IdempotencyKey,
	}
}

// parseTransitionDirective interprets the `transition` field, which is
// `string | false | undefined`:
//   - omitted/null → default transition resolution
//   - false        → skip the transition
//   - a string     → apply that explicit transition id
func parseTransitionDirective(raw json.RawMessage) (workflow.TransitionMode, string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if len(raw) == 0 || trimmed == "" || trimmed == "null" {
		return workflow.TransitionDefault, "", nil
	}
	if trimmed == "false" {
		return workflow.TransitionSkip, "", nil
	}
	if trimmed == "true" {
		return workflow.TransitionDefault, "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, "", NewValidationError("transition must be a transition id string or false", nil)
	}
	if strings.TrimSpace(s) == "" {
		return workflow.TransitionDefault, "", nil
	}
	return workflow.TransitionExplicit, s, nil
}

// canonicalDeliveryRef accepts deliveryRef as a JSON string or object and
// returns a stable string for storage. Objects are re-marshalled so map keys are
// emitted in a deterministic order (Go sorts map keys), making replay
// comparisons stable.
func canonicalDeliveryRef(raw json.RawMessage) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if len(raw) == 0 || trimmed == "" || trimmed == "null" {
		return "", nil
	}
	if strings.HasPrefix(trimmed, "\"") {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", NewValidationError("deliveryRef string is not valid JSON", nil)
		}
		return s, nil
	}

	if !strings.HasPrefix(trimmed, "{") {
		return "", NewValidationError("deliveryRef must be a string or JSON object", nil)
	}
	var v map[string]interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return "", NewValidationError("deliveryRef must be a string or JSON object", nil)
	}
	canonical, err := json.Marshal(v)
	if err != nil {
		return "", NewValidationError(fmt.Sprintf("deliveryRef canonicalization failed: %v", err), nil)
	}
	return string(canonical), nil
}
