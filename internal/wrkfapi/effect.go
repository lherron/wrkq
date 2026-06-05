package wrkfapi

import (
	"context"

	"github.com/lherron/wrkq/internal/workflow"
)

func (api *API) EffectClaim(ctx context.Context, params EffectClaimParams) (*workflow.EffectClaim, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	claim, err := api.service.ClaimEffects(params.Adapter, params.Limit, params.LeaseMs, params.TaskSelector, params.Kind)
	if err != nil {
		return nil, normalizeError(err)
	}
	return claim, nil
}

func (api *API) ClaimEffects(ctx context.Context, params EffectClaimParams) (*workflow.EffectClaim, error) {
	return api.EffectClaim(ctx, params)
}

func (api *API) EffectAck(ctx context.Context, params EffectAckParams) (*workflow.Effect, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	effect, err := api.service.AckEffect(params.EffectID, params.LeaseToken)
	if err != nil {
		return nil, normalizeError(err)
	}
	return effect, nil
}

func (api *API) AckEffect(ctx context.Context, params EffectAckParams) (*workflow.Effect, error) {
	return api.EffectAck(ctx, params)
}

func (api *API) EffectFail(ctx context.Context, params EffectFailParams) (*workflow.Effect, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	effect, err := api.service.FailEffect(params.EffectID, params.LeaseToken, params.Reason, params.Retryable)
	if err != nil {
		return nil, normalizeError(err)
	}
	return effect, nil
}

func (api *API) FailEffect(ctx context.Context, params EffectFailParams) (*workflow.Effect, error) {
	return api.EffectFail(ctx, params)
}

func (api *API) EffectRetry(ctx context.Context, id string) (*workflow.Effect, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	effect, err := api.service.RetryEffect(id)
	if err != nil {
		return nil, normalizeError(err)
	}
	return effect, nil
}

func (api *API) RetryEffect(ctx context.Context, id string) (*workflow.Effect, error) {
	return api.EffectRetry(ctx, id)
}

func (api *API) EffectDeliver(ctx context.Context, params EffectDeliverParams) (*workflow.EffectDelivery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	delivery, err := api.service.DeliverEffect(params.EffectID, params.Adapter, api.hookCatalog, api.templateDir)
	if err != nil {
		return delivery, normalizeError(err)
	}
	return delivery, nil
}

func (api *API) DeliverEffect(ctx context.Context, params EffectDeliverParams) (*workflow.EffectDelivery, error) {
	return api.EffectDeliver(ctx, params)
}
