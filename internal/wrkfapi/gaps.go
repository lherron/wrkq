package wrkfapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/lherron/wrkq/internal/workflow"
)

const maxWatchEventsPage = 500

type watchCursor struct {
	Kind       string `json:"kind"`
	InstanceID string `json:"instanceId"`
	RunID      string `json:"runId,omitempty"`
	Seq        int64  `json:"seq"`
}

func (api *API) EvidenceSchema(ctx context.Context, params EvidenceSchemaParams) (*EvidenceSchema, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	schema, err := api.service.EvidenceSchema(params.TaskSelector, params.Kind)
	if err != nil {
		return nil, normalizeError(err)
	}
	return schema, nil
}

func (api *API) SupervisorCall(ctx context.Context, params SupervisorParams) (*Effect, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	effect, err := api.service.SupervisorCall(params.TaskSelector, params.Reason)
	if err != nil {
		return nil, normalizeError(err)
	}
	return effect, nil
}

func (api *API) SupervisorEscalate(ctx context.Context, params SupervisorParams) (*Effect, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	effect, err := api.service.SupervisorEscalate(params.TaskSelector, params.Reason)
	if err != nil {
		return nil, normalizeError(err)
	}
	return effect, nil
}

func (api *API) ObligationCreate(ctx context.Context, params ObligationCreateParams) (*Obligation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	obligation, err := api.service.CreateObligation(
		params.TaskSelector,
		params.Kind,
		params.OwnerRole,
		params.OwnerActor,
		params.Blocking,
		params.Reason,
	)
	if err != nil {
		return nil, normalizeError(err)
	}
	return obligation, nil
}

func (api *API) WatchSnapshot(ctx context.Context, params WatchSnapshotParams) (*WatchSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot, err := api.service.WatchSnapshot(params.Selector, params.Until)
	if err != nil {
		return nil, normalizeError(err)
	}
	return snapshot, nil
}

func (api *API) WatchEvents(ctx context.Context, params WatchEventsParams) (*WatchEventsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Resolve once, then query the explicit identity. This avoids a task selector
	// changing instances between cursor validation and event selection.
	snapshot, err := api.service.WatchSnapshot(params.Selector, workflow.WatchUntilTerminal)
	if err != nil {
		return nil, normalizeError(err)
	}
	target := snapshot.Target
	afterSeq := int64(0)
	if strings.TrimSpace(params.AfterCursor) != "" {
		cursor, err := decodeWatchCursor(params.AfterCursor)
		if err != nil {
			return nil, NewValidationError("invalid watch events cursor", map[string]any{"field": "afterCursor"})
		}
		if watchCursorMatches(cursor, target) {
			afterSeq = cursor.Seq
		}
		// A task selector may now resolve to a successor instance. An identity
		// mismatch deliberately resets seq to zero so its early events are not
		// suppressed by the predecessor's high-water mark.
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 100
	} else if limit > maxWatchEventsPage {
		limit = maxWatchEventsPage
	}
	events, err := api.service.WatchEventsForTarget(target, afterSeq, limit)
	if err != nil {
		return nil, normalizeError(err)
	}
	if events == nil {
		events = []workflow.Event{}
	}
	nextSeq := afterSeq
	if len(events) > 0 {
		nextSeq = events[len(events)-1].Seq
	}
	nextCursor, err := encodeWatchCursor(watchCursor{
		Kind: target.Kind, InstanceID: target.InstanceID, RunID: target.RunID, Seq: nextSeq,
	})
	if err != nil {
		return nil, NewInternalError(err)
	}
	return &WatchEventsResult{Events: events, NextCursor: nextCursor}, nil
}

func watchCursorMatches(cursor watchCursor, target workflow.WatchTarget) bool {
	return cursor.Kind == target.Kind && cursor.InstanceID == target.InstanceID && cursor.RunID == target.RunID
}

func encodeWatchCursor(cursor watchCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeWatchCursor(encoded string) (watchCursor, error) {
	var cursor watchCursor
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return cursor, err
	}
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return cursor, err
	}
	if cursor.Kind == "" || cursor.InstanceID == "" || cursor.Seq < 0 {
		return watchCursor{}, NewValidationError("invalid watch events cursor", nil)
	}
	return cursor, nil
}
