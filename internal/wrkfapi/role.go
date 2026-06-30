package wrkfapi

import (
	"context"

	"github.com/lherron/wrkq/internal/workflow"
)

func (api *API) RoleList(ctx context.Context, params RoleListParams) ([]workflow.RoleBinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bindings, err := api.service.ListRoleBindings(params.TaskSelector, params.InstanceID)
	if err != nil {
		return nil, normalizeError(err)
	}
	return bindings, nil
}

func (api *API) RoleBind(ctx context.Context, params RoleBindParams) (*workflow.RoleBinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	binding, err := api.service.BindRole(workflow.RoleBindOptions{
		TaskSelector: params.TaskSelector,
		InstanceID:   params.InstanceID,
		Role:         params.Role,
		PrincipalRef: params.PrincipalRef,
		DeliveryRef:  params.DeliveryRef,
		Lane:         params.Lane,
		BindingMode:  params.BindingMode,
	})
	if err != nil {
		return nil, normalizeError(err)
	}
	return binding, nil
}

func (api *API) RoleUnbind(ctx context.Context, params RoleUnbindParams) ([]workflow.RoleBinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bindings, err := api.service.UnbindRole(params.TaskSelector, params.InstanceID, params.Role, params.PrincipalRef)
	if err != nil {
		return nil, normalizeError(err)
	}
	return bindings, nil
}

func (api *API) RoleSet(ctx context.Context, params RoleSetParams) ([]workflow.RoleBinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bindings, err := api.service.SetRoleBindings(params.TaskSelector, params.InstanceID, params.RoleMap)
	if err != nil {
		return nil, normalizeError(err)
	}
	return bindings, nil
}
