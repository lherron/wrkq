//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/taskfamily"
)

// The v2 timeline reader merges two independently scanned sources, event_log
// and project_events, into one ordered page under a resumable cursor. This file
// is the read itself, step by step; timeline_v2_request.go validates the request
// and positions its cursor, timeline_v2_sources.go scans each source,
// timeline_v2_merge.go merges them under the horizon, and
// timeline_v2_delivery.go decides what each scanned row delivers.

func timelineRequestUsesV2(p ContainerTimelineViewParams) bool {
	if p.Scope != "" || p.Types != nil || p.AllTypes || p.Task != "" || p.Since != "" || p.Before != "" || p.EntriesOnly || p.Tail || p.Order != "" {
		return true
	}
	version := timelineCursorVersion(p.Cursor)
	return version == 2 || version == 3
}

func (a *API) containerTimelineViewV2(
	ctx context.Context,
	tx *sql.Tx,
	p ContainerTimelineViewParams,
	containerUUID string,
	container WrkqTimelineContainer,
	campaign *WrkqCampaignAdornment,
	taskUUID string,
	limit int,
) (*WrkqContainerTimelineView, error) {
	selectedTasks, err := loadTimelineSelection(ctx, tx, taskUUID)
	if err != nil {
		return nil, err
	}
	scope, affiliation, err := loadTimelineScope(ctx, tx, p.Scope, containerUUID, container, campaign)
	if err != nil {
		return nil, err
	}
	req, err := newTimelineV2Request(p, containerUUID, scope)
	if err != nil {
		return nil, err
	}
	if err := req.positionCursor(ctx, tx, p); err != nil {
		return nil, err
	}

	page, err := scanTimelineSources(ctx, tx, req, selectedTasks, affiliation, containerUUID)
	if err != nil {
		return nil, err
	}
	entries, err := page.merge(req, p, limit, containerUUID, affiliation, selectedTasks)
	if err != nil {
		return nil, err
	}
	// Addressees are hydrated for the DELIVERED messages only, in one query,
	// rather than as a correlated subquery on every scanned row: the scan reads
	// up to a full page cap, the delivery is bounded by `limit`.
	if err := hydrateTimelineAddressees(ctx, tx, entries); err != nil {
		return nil, err
	}

	hasMore := page.advanceCursor(req)
	nextCursor := ""
	boundedTailClosed := req.before != nil && !time.Now().UTC().Before(*req.before) && !hasMore
	if (p.Tail && !boundedTailClosed) || hasMore {
		nextCursor, err = encodeTimelineCursor(req.cur)
		if err != nil {
			return nil, NewInternalError(err)
		}
	}

	var members []WrkqTimelineMember
	var rollup WrkqTimelineRollup
	var missing []WrkqCampaignMemberDiagnostic
	var decisions []WrkqTimelineMember
	var footprint []WrkqCampaignFootprint
	memberActivityAt := ""
	if !p.EntriesOnly {
		members, rollup, missing, decisions, footprint, memberActivityAt, err = loadTimelineMembersTx(ctx, tx, containerUUID)
		if err != nil {
			return nil, err
		}
	}
	lastActivityAt := maxTimestamp(container.UpdatedAt, memberActivityAt)
	for _, entry := range entries {
		lastActivityAt = maxTimestamp(lastActivityAt, entry.Timestamp)
	}
	if err := tx.Commit(); err != nil {
		return nil, NewInternalError(err)
	}
	return &WrkqContainerTimelineView{
		Container: container, Campaign: campaign,
		Members: members, Rollup: rollup, MissingOutcomes: missing,
		Footprint: footprint, LastActivityAt: lastActivityAt,
		DecisionTasks: decisions, Entries: entries,
		SnapshotEventID:        req.cur.SnapshotEventID,
		SnapshotProjectEventID: req.cur.SnapshotProjectEventID,
		NextCursor:             nextCursor, entriesOnly: p.EntriesOnly,
	}, nil
}

// loadTimelineSelection is the task family a --task read narrows to: the task
// and its subtasks. An empty set selects everything.
func loadTimelineSelection(ctx context.Context, tx *sql.Tx, taskUUID string) (map[string]bool, error) {
	selected := map[string]bool{}
	if taskUUID == "" {
		return selected, nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT uuid FROM tasks WHERE "+taskfamily.Filter("uuid"), taskUUID, taskUUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return nil, NewInternalError(err)
		}
		selected[uuid] = true
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(err)
	}
	return selected, nil
}

// loadTimelineScope validates the scope and returns the containers the read
// affiliates with: the container alone, or an unadorned project's subtree.
func loadTimelineScope(ctx context.Context, tx *sql.Tx, raw, containerUUID string,
	container WrkqTimelineContainer, campaign *WrkqCampaignAdornment) (string, map[string]bool, error) {
	scope := strings.TrimSpace(raw)
	if scope == "" {
		scope = "container"
	}
	if scope != "container" && scope != "subtree" {
		return "", nil, NewValidationError("scope must be container or subtree", map[string]any{"field": "scope"})
	}
	if scope == "subtree" && (container.Kind != "project" || campaign != nil) {
		return "", nil, NewValidationError("subtree scope requires an unadorned project", map[string]any{
			"field": "scope", "reason": "subtree_requires_unadorned_project",
		})
	}
	affiliation := map[string]bool{containerUUID: true}
	if scope == "subtree" {
		values, err := loadTimelineAffiliationSet(ctx, tx, containerUUID)
		if err != nil {
			return "", nil, NewInternalError(err)
		}
		for _, value := range values {
			affiliation[value] = true
		}
	}
	return scope, affiliation, nil
}
