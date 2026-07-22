package wrkfapi

import "github.com/lherron/wrkq/internal/workflow"

// CLI-facing aliases keep command adapters on the wrkf API boundary while the
// frozen wire DTOs continue to be implemented by workflow domain types.
type (
	ActionClaimPrefer  = workflow.ActionClaimPrefer
	ActionNextFilters  = workflow.ActionNextFilters
	EffectDelivery     = workflow.EffectDelivery
	ErrorDetail        = workflow.ErrorDetail
	NextActionResponse = workflow.NextActionResponse
	ValidateResult     = workflow.ValidateResult
)

func AsErrorDetail(err error) (ErrorDetail, bool) {
	return workflow.AsErrorDetail(err)
}
