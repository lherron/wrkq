package wrkfapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/workflow"
)

// action.go — JSON/RPC surface for the low-ceremony wrkf.action.* API. These
// are thin wrappers over the workflow.Service composition methods; the service
// layer owns the actual composition over run/evidence/transition primitives.

type ActionRun = workflow.ActionRun
type ActionNextParams = workflow.ActionNextParams
type ActionNextResult = workflow.ActionNextResult
type ActionClaimParams = workflow.ClaimActionParams
type ActionClaimResult = workflow.ClaimActionResult
type ActionEvidenceParams struct {
	Kind           string          `json:"kind,omitempty"`
	Ref            string          `json:"ref,omitempty"`
	Summary        string          `json:"summary,omitempty"`
	Facts          json.RawMessage `json:"facts,omitempty"`
	Data           json.RawMessage `json:"data,omitempty"`
	ContentHash    string          `json:"contentHash,omitempty"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
}

type ActionStartParams struct {
	Task           string          `json:"task,omitempty"`
	InstanceID     string          `json:"instanceId,omitempty"`
	Workflow       string          `json:"workflow,omitempty"`
	Action         string          `json:"action"`
	Role           string          `json:"role,omitempty"`
	PrincipalRef   string          `json:"principal_ref,omitempty"`
	Lane           string          `json:"lane,omitempty"`
	DeliveryRef    json.RawMessage `json:"deliveryRef,omitempty"`
	ExternalRunRef string          `json:"externalRunRef,omitempty"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
	LeaseOwner     string          `json:"leaseOwner,omitempty"`
	LeaseMs        int64           `json:"leaseMs,omitempty"`
}

type ActionBindExternalParams struct {
	ActionRunID    string          `json:"actionRunId"`
	ExternalRunRef string          `json:"externalRunRef"`
	DeliveryRef    json.RawMessage `json:"deliveryRef,omitempty"`
	Lane           string          `json:"lane,omitempty"`
	IdempotencyKey string          `json:"idempotencyKey,omitempty"`
}

type ActionCompleteParams struct {
	ActionRunID              string                `json:"actionRunId"`
	LeaseToken               string                `json:"leaseToken,omitempty"`
	Evidence                 *ActionEvidenceParams `json:"evidence,omitempty"`
	Transition               json.RawMessage       `json:"transition,omitempty"`
	TransitionIdempotencyKey string                `json:"transitionIdempotencyKey,omitempty"`
	RunSummary               string                `json:"runSummary,omitempty"`
}

type ActionSettleParams struct {
	ActionRunID     string                `json:"actionRunId,omitempty"`
	RunID           string                `json:"runId,omitempty"`
	OwnerToken      string                `json:"ownerToken,omitempty"`
	OwnerGeneration int64                 `json:"ownerGeneration,omitempty"`
	Result          string                `json:"result"`
	Evidence        *ActionEvidenceParams `json:"evidence,omitempty"`
	Transition      json.RawMessage       `json:"transition,omitempty"`
	TerminalSummary string                `json:"terminalSummary,omitempty"`
}

type ActionFailParams struct {
	ActionRunID string                `json:"actionRunId"`
	LeaseToken  string                `json:"leaseToken,omitempty"`
	Summary     string                `json:"summary"`
	Evidence    *ActionEvidenceParams `json:"evidence,omitempty"`
}

type ActionHeartbeatParams struct {
	ActionRunID string `json:"actionRunId"`
	LeaseToken  string `json:"leaseToken"`
	LeaseMs     int64  `json:"leaseMs,omitempty"`
}

type ActionShowParams struct {
	ActionRunID string `json:"actionRunId"`
}

type ActionListParams struct {
	Task                   string `json:"task,omitempty"`
	InstanceID             string `json:"instanceId,omitempty"`
	IncludeClosedInstances bool   `json:"includeClosedInstances,omitempty"`
	Status                 string `json:"status,omitempty"`
	Action                 string `json:"action,omitempty"`
	Limit                  int    `json:"limit,omitempty"`
	Cursor                 string `json:"cursor,omitempty"`
}

type ActionListResult struct {
	Items []workflow.ActionRun `json:"items"`
}

type ActionCompleteResult struct {
	Run        *workflow.ActionRun `json:"run"`
	Evidence   *workflow.Evidence  `json:"evidence,omitempty"`
	Transition *TransitionResult   `json:"transition,omitempty"`
}

type ActionSettleResult struct {
	Run         workflow.WorkflowRunAttempt `json:"run"`
	Evidence    *workflow.Evidence          `json:"evidence,omitempty"`
	Transition  *TransitionResult           `json:"transition,omitempty"`
	Effects     []workflow.Effect           `json:"effects,omitempty"`
	Obligations []workflow.Obligation       `json:"obligations,omitempty"`
}

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
	// The advertised contract is `string | object`. Reject arrays, numbers,
	// booleans, and other JSON values rather than silently storing them.
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
