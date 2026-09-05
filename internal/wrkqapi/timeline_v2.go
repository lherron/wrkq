//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/selectors"
)

type timelineQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type timelineRawEvent struct {
	entry       WrkqTimelineEntry
	eventType   string
	payload     string
	serverTime  string
	commentID   string
	commentKind sql.NullString
	commentBody string
	commentMeta sql.NullString
}

type timelineRawProjectEvent struct {
	entry      WrkqTimelineEntry
	semantic   string
	serverTime string
}

func timelineRequestUsesV2(p ContainerTimelineViewParams) bool {
	if p.Scope != "" || p.Types != nil || p.Task != "" || p.Since != "" || p.EntriesOnly || p.Tail {
		return true
	}
	return timelineCursorVersion(p.Cursor) == 2
}

func timelineCursorVersion(raw string) int {
	if strings.TrimSpace(raw) == "" {
		return 0
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0
	}
	var head struct {
		Version int `json:"v"`
	}
	if json.Unmarshal(decoded, &head) != nil {
		return 0
	}
	return head.Version
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
	scope := strings.TrimSpace(p.Scope)
	if scope == "" {
		scope = "container"
	}
	if scope != "container" && scope != "subtree" {
		return nil, NewValidationError("scope must be container or subtree", map[string]any{"field": "scope"})
	}
	if scope == "subtree" && (container.Kind != "project" || campaign != nil) {
		return nil, NewValidationError("subtree scope requires an unadorned project", map[string]any{
			"field": "scope", "reason": "subtree_requires_unadorned_project",
		})
	}
	affiliation := map[string]bool{containerUUID: true}
	if scope == "subtree" {
		values, err := loadTimelineAffiliationSet(ctx, tx, containerUUID)
		if err != nil {
			return nil, NewInternalError(err)
		}
		for _, value := range values {
			affiliation[value] = true
		}
	}

	since, err := parseTimelineSince(p.Since)
	if err != nil {
		return nil, err
	}
	for _, filter := range p.Types {
		if strings.TrimSpace(filter) == "" || (strings.Contains(filter, "*") && !strings.HasSuffix(filter, ".*")) {
			return nil, NewValidationError("type filters must be exact or trailing globs", map[string]any{"field": "types"})
		}
	}

	cur := timelineCursor{Version: 2, ContainerUUID: containerUUID, Scope: scope}
	if p.Cursor != "" {
		cur, err = decodeTimelineCursorAny(p.Cursor)
		if err != nil {
			return nil, NewValidationError("invalid timeline cursor", map[string]any{"field": "cursor"})
		}
		if cur.ContainerUUID != containerUUID || (cur.Version == 2 && cur.Scope != scope) {
			return nil, NewValidationError("timeline cursor does not match the request", map[string]any{"field": "cursor"})
		}
		if cur.Version == 1 {
			cur.Scope = scope
			cur.SnapshotProjectEventID = 0
			cur.AfterProjectEventID = 0
		}
	}

	currentEventID, currentProjectEventID, err := timelineSourceMaxima(ctx, tx)
	if err != nil {
		return nil, err
	}
	if p.Cursor == "" {
		cur.SnapshotEventID = currentEventID
		cur.SnapshotProjectEventID = currentProjectEventID
		if p.Tail && since == nil {
			cur.AfterEventID = currentEventID
			cur.AfterProjectEventID = currentProjectEventID
		}
	} else if p.Tail {
		cur.SnapshotEventID = currentEventID
		cur.SnapshotProjectEventID = currentProjectEventID
	}
	cur.Version = 2

	eventRows, err := loadTimelineRawEvents(ctx, tx, cur.AfterEventID, cur.SnapshotEventID)
	if err != nil {
		return nil, err
	}
	projectRows, err := loadTimelineRawProjectEvents(ctx, tx, cur.AfterProjectEventID, cur.SnapshotProjectEventID)
	if err != nil {
		return nil, err
	}

	entries := make([]WrkqTimelineEntry, 0, limit)
	eventIndex, projectIndex := 0, 0
	for len(entries) < limit && (eventIndex < len(eventRows) || projectIndex < len(projectRows)) {
		popEvent := projectIndex >= len(projectRows)
		if eventIndex < len(eventRows) && projectIndex < len(projectRows) {
			popEvent = timelineHeadBefore(eventRows[eventIndex], projectRows[projectIndex])
		}
		if popEvent {
			raw := eventRows[eventIndex]
			eventIndex++
			cur.AfterEventID = raw.entry.EventID
			entry, included, err := deliverTimelineEvent(raw, containerUUID, affiliation, p.Types, taskUUID, since)
			if err != nil {
				return nil, err
			}
			if included {
				entries = append(entries, entry)
			}
			continue
		}
		raw := projectRows[projectIndex]
		projectIndex++
		cur.AfterProjectEventID = raw.entry.ProjectEventID
		if entry, included := deliverTimelineProjectEvent(raw, containerUUID, affiliation, p.Types, taskUUID, since); included {
			entries = append(entries, entry)
		}
	}

	hasMore := cur.AfterEventID < cur.SnapshotEventID || cur.AfterProjectEventID < cur.SnapshotProjectEventID
	nextCursor := ""
	if p.Tail || hasMore {
		nextCursor, err = encodeTimelineCursor(cur)
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
		SnapshotEventID:        cur.SnapshotEventID,
		SnapshotProjectEventID: cur.SnapshotProjectEventID,
		NextCursor:             nextCursor, entriesOnly: p.EntriesOnly,
	}, nil
}

func timelineSourceMaxima(ctx context.Context, tx *sql.Tx) (int64, int64, error) {
	var eventID, projectEventID int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM event_log`).Scan(&eventID); err != nil {
		return 0, 0, NewInternalError(err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM project_events`).Scan(&projectEventID); err != nil {
		return 0, 0, NewInternalError(err)
	}
	return eventID, projectEventID, nil
}

func loadTimelineRawEvents(ctx context.Context, tx *sql.Tx, after, snapshot int64) ([]timelineRawEvent, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT e.id, e.timestamp, COALESCE(e.principal_ref, ''), COALESCE(e.resource_uuid, ''),
		       e.event_type, COALESCE(e.payload, ''),
		       COALESCE(t.uuid, comment_task.uuid, ''),
		       COALESCE(t.id, comment_task.id, ''),
		       COALESCE(tp.path, comment_tp.path, json_extract(e.payload, '$.slug'), ''),
		       COALESCE(cm.id, ''), cm.kind, COALESCE(cm.body, ''), cm.meta
		  FROM event_log e
		  LEFT JOIN tasks t ON e.resource_type = 'task' AND t.uuid = e.resource_uuid
		  LEFT JOIN v_task_paths tp ON tp.uuid = t.uuid
		  LEFT JOIN comments cm ON e.event_type = 'comment.created' AND cm.uuid = e.resource_uuid
		  LEFT JOIN tasks comment_task ON e.event_type = 'comment.created' AND comment_task.uuid = json_extract(e.payload, '$.task_id')
		  LEFT JOIN v_task_paths comment_tp ON comment_tp.uuid = comment_task.uuid
		 WHERE e.id > ? AND e.id <= ?
		 ORDER BY e.id ASC LIMIT ?`, after, snapshot, monitorMaxPageLimit)
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	result := []timelineRawEvent{}
	for rows.Next() {
		var raw timelineRawEvent
		if err := rows.Scan(
			&raw.entry.EventID, &raw.serverTime, &raw.entry.PrincipalRef, &raw.entry.ResourceUUID,
			&raw.eventType, &raw.payload, &raw.entry.TaskUUID, &raw.entry.TaskID, &raw.entry.TaskPath,
			&raw.commentID, &raw.commentKind, &raw.commentBody, &raw.commentMeta,
		); err != nil {
			return nil, NewInternalError(err)
		}
		raw.entry.Timestamp = toRFC3339(raw.serverTime)
		result = append(result, raw)
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(err)
	}
	return result, nil
}

func loadTimelineRawProjectEvents(ctx context.Context, tx *sql.Tx, after, snapshot int64) ([]timelineRawProjectEvent, error) {
	rows, err := tx.QueryContext(ctx, timelineProjectEventsRawQuery, after, snapshot, monitorMaxPageLimit)
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	result := []timelineRawProjectEvent{}
	for rows.Next() {
		var raw timelineRawProjectEvent
		var node, payload, campaign, task sql.NullString
		var detail WrkqTimelineProjectEvent
		if err := rows.Scan(
			&raw.entry.ProjectEventID, &detail.FID, &raw.semantic, &detail.Source,
			&node, &detail.PrincipalRef, &detail.Summary, &payload, &detail.OccurredAt,
			&raw.serverTime, &raw.entry.ContainerUUID, &campaign, &task,
			&raw.entry.TaskID, &raw.entry.TaskPath,
		); err != nil {
			return nil, NewInternalError(err)
		}
		detail.Type = raw.semantic
		detail.Node = nullStringPtr(node)
		if payload.Valid {
			detail.Payload = json.RawMessage(payload.String)
		}
		detail.OccurredAt = toRFC3339(detail.OccurredAt)
		raw.entry.Type = "project.event"
		raw.entry.EventID = 0
		raw.entry.Timestamp = toRFC3339(raw.serverTime)
		raw.entry.PrincipalRef = detail.PrincipalRef
		raw.entry.CampaignUUID = nullStringPtr(campaign)
		if task.Valid {
			raw.entry.TaskUUID = task.String
		}
		raw.entry.ProjectEvent = &detail
		result = append(result, raw)
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(err)
	}
	return result, nil
}

// project_uuid is intentionally absent: current subtree membership is the one
// project reader authority; the stored project is idempotency scope only.
const timelineProjectEventsRawQuery = `
		SELECT pe.id, pe.fid, pe.type, pe.source, pe.node, pe.principal_ref,
		       pe.summary, pe.payload, pe.occurred_at, pe.created_at,
		       pe.container_uuid, pe.campaign_uuid, pe.task_uuid,
		       COALESCE(t.id, ''), COALESCE(tp.path, '')
		  FROM project_events pe
		  LEFT JOIN tasks t ON t.uuid = pe.task_uuid
		  LEFT JOIN v_task_paths tp ON tp.uuid = t.uuid
		 WHERE pe.id > ? AND pe.id <= ?
		 ORDER BY pe.id ASC LIMIT ?`

func timelineHeadBefore(event timelineRawEvent, project timelineRawProjectEvent) bool {
	if event.entry.Timestamp != project.entry.Timestamp {
		return event.entry.Timestamp < project.entry.Timestamp
	}
	// event_log has source rank zero, so it wins a server-time tie.
	return true
}

func deliverTimelineEvent(raw timelineRawEvent, root string, affiliation map[string]bool, filters []string, taskUUID string, since *time.Time) (WrkqTimelineEntry, bool, error) {
	if !timelineEventTypeSupported(raw.eventType, raw.payload) {
		return WrkqTimelineEntry{}, false, nil
	}
	entry := raw.entry
	if err := normalizeTimelineEntry(&entry, raw.eventType, raw.payload, root); err != nil {
		return WrkqTimelineEntry{}, false, NewInternalError(err)
	}
	applyTimelineMembership(&entry, root, affiliation)
	if entry.Membership == "" || !timelineTypeMatches(filters, entry.Type) ||
		(taskUUID != "" && entry.TaskUUID != taskUUID) || !timelineSinceMatches(entry.Timestamp, since) {
		return WrkqTimelineEntry{}, false, nil
	}
	if entry.Type == "comment" {
		comment := &WrkqTimelineComment{ID: raw.commentID, Body: raw.commentBody}
		if raw.commentKind.Valid {
			value := raw.commentKind.String
			comment.Kind = &value
		}
		if raw.commentMeta.Valid && json.Valid([]byte(raw.commentMeta.String)) {
			comment.Meta = json.RawMessage(raw.commentMeta.String)
		}
		entry.Comment = comment
	}
	return entry, true, nil
}

func deliverTimelineProjectEvent(raw timelineRawProjectEvent, root string, affiliation map[string]bool, filters []string, taskUUID string, since *time.Time) (WrkqTimelineEntry, bool) {
	applyTimelineMembership(&raw.entry, root, affiliation)
	if raw.entry.Membership == "" || !timelineTypeMatches(filters, raw.semantic) ||
		(taskUUID != "" && raw.entry.TaskUUID != taskUUID) || !timelineSinceMatches(raw.entry.Timestamp, since) {
		return WrkqTimelineEntry{}, false
	}
	return raw.entry, true
}

func timelineEventTypeSupported(eventType, payload string) bool {
	switch eventType {
	case "comment.created", "task.outcome_set", "task.archived", "task.deleted", "task.restored", "task.purged", "container.campaign_state_changed":
		return true
	case "task.updated":
		var fields map[string]json.RawMessage
		return json.Unmarshal([]byte(payload), &fields) == nil && fields["state"] != nil
	default:
		return false
	}
}

func applyTimelineMembership(entry *WrkqTimelineEntry, root string, affiliation map[string]bool) {
	entry.Membership = ""
	switch {
	case entry.CampaignUUID != nil && *entry.CampaignUUID == root:
		if entry.ContainerUUID == root {
			entry.Membership = "resident"
		} else {
			entry.Membership = "enrolled"
		}
	case entry.ContainerUUID == root:
		entry.Membership = "resident"
	case affiliation[entry.ContainerUUID]:
		entry.Membership = "subtree"
	case entry.Type == "container.state" && affiliation[entry.ResourceUUID]:
		entry.Membership = "subtree"
	}
}

func timelineTypeMatches(filters []string, value string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		filter = strings.TrimSpace(filter)
		if strings.HasSuffix(filter, ".*") {
			if strings.HasPrefix(value, strings.TrimSuffix(filter, "*")) {
				return true
			}
		} else if value == filter {
			return true
		}
	}
	return false
}

func parseTimelineSince(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if duration, err := time.ParseDuration(raw); err == nil {
		if duration < 0 {
			return nil, NewValidationError("since duration must not be negative", map[string]any{"field": "since"})
		}
		value := time.Now().UTC().Add(-duration)
		return &value, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, NewValidationError("since must be RFC3339 or a duration", map[string]any{"field": "since"})
	}
	value = value.UTC()
	return &value, nil
}

func timelineSinceMatches(raw string, since *time.Time) bool {
	if since == nil {
		return true
	}
	value, err := time.Parse(time.RFC3339, raw)
	return err == nil && !value.Before(*since)
}

func decodeTimelineCursorAny(raw string) (timelineCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return timelineCursor{}, err
	}
	var cur timelineCursor
	if err := json.Unmarshal(decoded, &cur); err != nil {
		return timelineCursor{}, err
	}
	if cur.ContainerUUID == "" || cur.AfterEventID < 0 || cur.SnapshotEventID < 0 || cur.AfterEventID > cur.SnapshotEventID {
		return timelineCursor{}, fmt.Errorf("invalid timeline cursor fields")
	}
	switch cur.Version {
	case 1:
		return cur, nil
	case 2:
		if (cur.Scope != "container" && cur.Scope != "subtree") || cur.AfterProjectEventID < 0 ||
			cur.SnapshotProjectEventID < 0 || cur.AfterProjectEventID > cur.SnapshotProjectEventID {
			return timelineCursor{}, fmt.Errorf("invalid timeline cursor fields")
		}
		return cur, nil
	default:
		return timelineCursor{}, fmt.Errorf("unsupported timeline cursor version")
	}
}

func loadTimelineAffiliationSet(ctx context.Context, q timelineQueryer, root string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `WITH RECURSIVE descendants(uuid) AS (
		SELECT uuid FROM containers WHERE uuid = ?
		UNION ALL
		SELECT c.uuid FROM containers c JOIN descendants d ON c.parent_uuid = d.uuid
	) SELECT uuid FROM descendants ORDER BY uuid`, root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (a *API) resolveUnadornedProject(ctx context.Context, raw, field string) (string, error) {
	uuid, _, err := selectors.ResolveContainer(a.db, raw)
	if err != nil {
		return "", NewValidationError("project must resolve to an unadorned project", map[string]any{"field": field, "reason": "subtree_requires_unadorned_project"})
	}
	var kind string
	var campaign sql.NullString
	if err := a.db.QueryRowContext(ctx, `SELECT kind, campaign_state FROM containers WHERE uuid = ?`, uuid).Scan(&kind, &campaign); err != nil || kind != "project" || campaign.Valid {
		return "", NewValidationError("project must resolve to an unadorned project", map[string]any{"field": field, "reason": "subtree_requires_unadorned_project"})
	}
	return uuid, nil
}
