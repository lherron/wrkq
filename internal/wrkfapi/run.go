//go:build wrkq_local

package wrkfapi

import (
	"context"

	"github.com/lherron/wrkq/internal/workflow"
)

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
