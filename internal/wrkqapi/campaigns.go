package wrkqapi

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
)

// ContainerCampaignConvert adorns a plain container as an active campaign.
func (a *API) ContainerCampaignConvert(
	ctx context.Context,
	p ContainerCampaignConvertParams,
) (*WrkqCampaignTransitionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	containerUUID, selector, err := a.resolveCampaignContainer(p.Container)
	if err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.Actor)
	if err != nil {
		return nil, err
	}
	result, err := a.store.Containers.ConvertCampaignWithAttribution(
		attr, containerUUID, p.Description, p.Specification, p.ExpectETag,
	)
	if err != nil {
		return nil, mapCampaignStoreError(err, selector)
	}
	return a.campaignTransitionDTO(containerUUID, result)
}

// ContainerCampaignUpdate edits campaign brief/specification content through
// the generic container.updated store producer. The store snapshots both full
// bodies into the event payload.
func (a *API) ContainerCampaignUpdate(
	ctx context.Context,
	p ContainerCampaignUpdateParams,
) (*WrkqContainer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.Description == nil && p.Specification == nil {
		return nil, NewValidationError(
			"description or specification is required",
			map[string]any{"field": "description|specification"},
		)
	}
	containerUUID, selector, err := a.resolveCampaignContainer(p.Container)
	if err != nil {
		return nil, err
	}
	var campaignState sql.NullString
	if err := a.db.QueryRowContext(
		ctx, "SELECT campaign_state FROM containers WHERE uuid = ?", containerUUID,
	).Scan(&campaignState); err != nil {
		return nil, NewInternalError(err)
	}
	if !campaignState.Valid {
		return nil, NewValidationError("container is not a campaign; convert it first", nil)
	}

	fields := map[string]interface{}{}
	if p.Description != nil {
		fields["description"] = *p.Description
	}
	if p.Specification != nil {
		if strings.TrimSpace(*p.Specification) == "" {
			fields["specification"] = nil
		} else {
			fields["specification"] = *p.Specification
		}
	}
	attr, err := a.attributionFor(p.Actor)
	if err != nil {
		return nil, err
	}
	if _, err := a.store.Containers.UpdateFieldsWithAttribution(
		attr, containerUUID, fields, p.ExpectETag,
	); err != nil {
		return nil, mapCampaignStoreError(err, selector)
	}
	return a.loadContainer(containerUUID)
}

// ContainerCampaignClose declares an active campaign completed or cancelled.
func (a *API) ContainerCampaignClose(
	ctx context.Context,
	p ContainerCampaignCloseParams,
) (*WrkqCampaignTransitionResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	targetState := strings.TrimSpace(p.State)
	if targetState != store.CampaignStateCompleted && targetState != store.CampaignStateCancelled {
		return nil, NewValidationError(
			"campaign state must be completed or cancelled",
			map[string]any{"field": "state"},
		)
	}
	containerUUID, selector, err := a.resolveCampaignContainer(p.Container)
	if err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.Actor)
	if err != nil {
		return nil, err
	}
	result, err := a.store.Containers.TransitionCampaignWithAttribution(
		attr, containerUUID, targetState, p.ExpectETag,
	)
	if err != nil {
		return nil, mapCampaignStoreError(err, selector)
	}
	return a.campaignTransitionDTO(containerUUID, result)
}

func (a *API) resolveCampaignContainer(raw string) (string, string, error) {
	selector := strings.TrimSpace(raw)
	if selector == "" {
		return "", "", NewValidationError("container is required", map[string]any{"field": "container"})
	}
	containerUUID, _, err := selectors.ResolveContainer(a.db, selector)
	if err != nil {
		return "", selector, NewNotFoundError(selector, "container")
	}
	return containerUUID, selector, nil
}

func (a *API) campaignTransitionDTO(
	containerUUID string,
	result *store.CampaignTransitionResult,
) (*WrkqCampaignTransitionResult, error) {
	container, err := a.loadContainer(containerUUID)
	if err != nil {
		return nil, err
	}
	return &WrkqCampaignTransitionResult{
		Container:       container,
		PreviousState:   result.PreviousState,
		CampaignState:   result.CampaignState,
		MissingOutcomes: campaignDiagnosticsDTO(result.MissingOutcomes),
		EventID:         result.EventID,
		EventTimestamp:  result.EventTimestamp,
	}, nil
}

func campaignDiagnosticsDTO(in []store.CampaignMemberDiagnostic) []WrkqCampaignMemberDiagnostic {
	out := make([]WrkqCampaignMemberDiagnostic, 0, len(in))
	for _, item := range in {
		out = append(out, WrkqCampaignMemberDiagnostic{
			UUID: item.UUID, ID: item.ID, Path: item.Path,
			State: item.State, Membership: item.Membership,
		})
	}
	return out
}

func mapCampaignStoreError(err error, selector string) error {
	if err == nil {
		return nil
	}
	var blocked *store.CampaignCloseBlockedError
	if errors.As(err, &blocked) {
		return NewWrongStateError(map[string]any{
			"campaign":        selector,
			"stragglers":      campaignDiagnosticsDTO(blocked.Stragglers),
			"missingOutcomes": campaignDiagnosticsDTO(blocked.MissingOutcomes),
		})
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "already a campaign"),
		strings.Contains(msg, "not a campaign"),
		strings.Contains(msg, "only active campaigns"),
		strings.Contains(msg, "target state"),
		strings.Contains(msg, "cannot be converted"),
		strings.Contains(msg, "campaign containers cannot be nested"),
		strings.Contains(msg, "cannot remain enrolled"):
		return NewValidationError(err.Error(), nil)
	}
	return mapContainerStoreError(err, selector)
}
