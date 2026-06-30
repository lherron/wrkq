package wrkfapi

import (
	"context"

	"github.com/lherron/wrkq/internal/workflow"
)

type RunStartParams struct {
	TaskSelector   string `json:"task"`
	InstanceID     string `json:"instanceId,omitempty"`
	Role           string `json:"role"`
	PrincipalRef   string `json:"principal_ref"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
	DeliveryRef    string `json:"deliveryRef,omitempty"`
	Lane           string `json:"lane,omitempty"`
	ExternalRunRef string `json:"externalRunRef,omitempty"`
}

type RunBindExternalParams struct {
	RunID          string `json:"runId"`
	ExternalRunRef string `json:"externalRunRef"`
	DeliveryRef    string `json:"deliveryRef,omitempty"`
	Lane           string `json:"lane,omitempty"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

type RunFinishParams struct {
	RunID   string `json:"runId"`
	Status  string `json:"status,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type RunFailParams struct {
	RunID   string `json:"runId"`
	Summary string `json:"summary,omitempty"`
}

func (api *API) RunStart(ctx context.Context, params RunStartParams) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	run, err := api.service.StartRunForSelectors(params.TaskSelector, params.InstanceID, params.Role, params.PrincipalRef, workflow.StartRunOptions{
		IdempotencyKey: params.IdempotencyKey,
		DeliveryRef:    params.DeliveryRef,
		Lane:           params.Lane,
		ExternalRunRef: params.ExternalRunRef,
	})
	if err != nil {
		return Run{}, normalizeError(err)
	}
	return *run, nil
}

func (api *API) RunBindExternal(ctx context.Context, params RunBindExternalParams) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	run, err := api.service.BindExternal(params.RunID, params.ExternalRunRef, workflow.BindExternalOptions{
		DeliveryRef:    params.DeliveryRef,
		Lane:           params.Lane,
		IdempotencyKey: params.IdempotencyKey,
	})
	if err != nil {
		return Run{}, normalizeError(err)
	}
	return *run, nil
}

func (api *API) RunFinish(ctx context.Context, params RunFinishParams) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	run, err := api.service.FinishRun(params.RunID, params.Status, params.Summary)
	if err != nil {
		return Run{}, normalizeError(err)
	}
	return *run, nil
}

func (api *API) RunFail(ctx context.Context, params RunFailParams) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	run, err := api.service.FailRun(params.RunID, params.Summary)
	if err != nil {
		return Run{}, normalizeError(err)
	}
	return *run, nil
}

func (api *API) RunShow(ctx context.Context, id string) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	run, err := api.service.ShowRun(id)
	if err != nil {
		return Run{}, normalizeError(err)
	}
	return *run, nil
}

func (api *API) ShowRun(ctx context.Context, id string) (Run, error) {
	return api.RunShow(ctx, id)
}

func (api *API) RunList(ctx context.Context, taskSelector string) ([]Run, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	runs, err := api.service.ListRuns(taskSelector)
	if err != nil {
		return nil, normalizeError(err)
	}
	return runs, nil
}

func (api *API) ListRuns(ctx context.Context, taskSelector string) ([]Run, error) {
	return api.RunList(ctx, taskSelector)
}
