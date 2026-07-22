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

func (api *API) EffectAck(ctx context.Context, params EffectAckParams) (*workflow.Effect, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var effect *workflow.Effect
	var err error
	if params.Force {
		effect, err = api.service.ForceAckEffect(params.EffectID)
	} else {
		effect, err = api.service.AckEffectWithReceipt(params.EffectID, params.LeaseToken, params.Receipt)
	}
	if err != nil {
		return nil, normalizeError(err)
	}
	return effect, nil
}

func (api *API) EffectFail(ctx context.Context, params EffectFailParams) (*workflow.Effect, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var effect *workflow.Effect
	var err error
	if params.Force {
		effect, err = api.service.ForceFailEffect(params.EffectID, params.Reason)
	} else {
		effect, err = api.service.FailEffect(params.EffectID, params.LeaseToken, params.Reason, params.Retryable)
	}
	if err != nil {
		return nil, normalizeError(err)
	}
	return effect, nil
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

func (api *API) EffectDeliver(ctx context.Context, params EffectDeliverParams) (*workflow.EffectDelivery, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	delivery, err := api.service.DeliverEffectWithOptions(params.EffectID, params.Adapter, api.hookCatalog, api.templateDir, workflow.HookExecutionOptions{
		Context: ctx, TimeoutCeiling: api.hookTimeoutCeiling,
	})
	if err != nil {
		return delivery, normalizeError(err)
	}
	return delivery, nil
}
