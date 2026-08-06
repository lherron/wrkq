//go:build wrkq_local

package wrkfapi

import (
	"context"

	"github.com/lherron/wrkq/internal/workflow"
)

func (api *API) EventQuery(ctx context.Context, params EventQueryParams) (workflow.EventQueryResult, error) {
	if err := ctx.Err(); err != nil {
		return workflow.EventQueryResult{}, err
	}
	result, err := api.service.QueryEvents(params)
	if err != nil {
		return workflow.EventQueryResult{}, normalizeError(err)
	}
	return result, nil
}