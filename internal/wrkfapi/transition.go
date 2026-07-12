package wrkfapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/lherron/wrkq/internal/workflow"
)

type TransitionApplyParams struct {
	TaskSelector   string   `json:"task"`
	InstanceID     string   `json:"instanceId,omitempty"`
	Transition     string   `json:"transition"`
	Role           string   `json:"role,omitempty"`
	PrincipalRef   string   `json:"principal_ref,omitempty"`
	ExpectRevision *int64   `json:"expectRevision,omitempty"`
	IdempotencyKey string   `json:"idempotencyKey,omitempty"`
	CheckIDs       []string `json:"checkIds,omitempty"`
	RunChecks      bool     `json:"runChecks,omitempty"`
	DryRun         bool     `json:"dryRun,omitempty"`
}

func (api *API) TransitionApply(ctx context.Context, params TransitionApplyParams) (TransitionResult, error) {
	if err := ctx.Err(); err != nil {
		return TransitionResult{}, err
	}
	out, err := api.service.TransitionForSelectors(params.TaskSelector, params.InstanceID, params.Transition, workflow.TransitionOptions{
		PrincipalRef:   params.PrincipalRef,
		Role:           params.Role,
		ExpectRevision: params.ExpectRevision,
		IdempotencyKey: params.IdempotencyKey,
		CheckIDs:       params.CheckIDs,
		RunChecks:      params.RunChecks,
		DryRun:         params.DryRun,
		HookCatalog:    api.hookCatalog,
		TemplateDir:    api.templateDir,
	})
	if err != nil {
		return TransitionResult{}, normalizeTransitionError(err)
	}
	return transitionResultFromAny(out), nil
}

type codedError interface {
	Code() string
}

func normalizeTransitionError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr Error
	if errors.As(err, &apiErr) {
		return err
	}
	var coded codedError
	if errors.As(err, &coded) {
		switch coded.Code() {
		case CodeStaleRevision:
			return NewStaleRevisionError("", 0, 0)
		case CodeTransitionBlocked:
			return NewTransitionBlockedError("", "", nil)
		case CodeRoleDenied:
			return NewRoleDeniedError("", "", "")
		case CodeIdempotencyMismatch:
			return NewIdempotencyMismatchError("")
		}
	}
	return normalizeError(err)
}

func transitionResultFromAny(out map[string]interface{}) TransitionResult {
	return TransitionResult{
		Task:        stringFromAny(out["task"]),
		InstanceID:  stringFromAny(out["instanceId"]),
		State:       stateFromAny(out["state"]),
		Revision:    int64FromAny(out["revision"]),
		EventID:     stringFromAny(out["eventId"]),
		Effects:     effectsFromAny(out["effects"]),
		Obligations: obligationsFromAny(out["obligations"]),
	}
}

func stateFromAny(v any) workflow.State {
	switch x := v.(type) {
	case workflow.State:
		return x
	case map[string]any:
		return workflow.State{
			Status:  stringFromAny(x["status"]),
			Phase:   stringFromAny(x["phase"]),
			Outcome: stringFromAny(x["outcome"]),
		}
	default:
		var state workflow.State
		_ = remarshal(v, &state)
		return state
	}
}

func effectsFromAny(v any) []workflow.Effect {
	switch x := v.(type) {
	case []workflow.Effect:
		return x
	case []any:
		out := make([]workflow.Effect, 0, len(x))
		for _, item := range x {
			var effect workflow.Effect
			if remarshal(item, &effect) == nil && effect.ID != "" {
				out = append(out, effect)
			}
		}
		return out
	default:
		return nil
	}
}

func obligationsFromAny(v any) []workflow.Obligation {
	switch x := v.(type) {
	case []workflow.Obligation:
		return x
	case []any:
		out := make([]workflow.Obligation, 0, len(x))
		for _, item := range x {
			var obligation workflow.Obligation
			if remarshal(item, &obligation) == nil && obligation.ID != "" {
				out = append(out, obligation)
			}
		}
		return out
	default:
		return nil
	}
}

func int64FromAny(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	default:
		n, _ := strconv.ParseInt(fmt.Sprint(x), 10, 64)
		return n
	}
}

func remarshal(in any, out any) error {
	if in == nil {
		return nil
	}
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
