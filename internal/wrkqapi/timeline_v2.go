//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
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
	envelope    timelineRawEnvelope
}

// timelineRawEnvelope carries the room-message columns for an envelope.created
// row. Unlike a comment, an envelope event's payload holds no container or
// campaign uuid, so affiliation is resolved in SQL through the ROOM: a task
// room affiliates exactly as a comment on that task would, and a container room
// affiliates to its own container.
type timelineRawEnvelope struct {
	id         sql.NullString
	groupID    sql.NullString
	roomID     sql.NullString
	roomKind   sql.NullString
	from       sql.NullString
	obligation sql.NullString
	body       sql.NullString
	container  sql.NullString
	campaign   sql.NullString
}

type timelineRawProjectEvent struct {
	entry      WrkqTimelineEntry
	id         int64
	semantic   string
	serverTime string
}

func timelineRequestUsesV2(p ContainerTimelineViewParams) bool {
	if p.Scope != "" || p.Types != nil || p.Task != "" || p.Since != "" || p.EntriesOnly || p.Tail || p.Order != "" {
		return true
	}
	version := timelineCursorVersion(p.Cursor)
	return version == 2 || version == 3
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

	requested, err := parseTimelineOrder(p.Order)
	if err != nil {
		return nil, err
	}
	desc := requested == timelineOrderDesc

	cur := timelineCursor{Version: 2, ContainerUUID: containerUUID, Scope: scope}
	if p.Cursor != "" {
		cur, err = decodeTimelineCursorAny(p.Cursor)
		if err != nil {
			return nil, NewValidationError("invalid timeline cursor", map[string]any{"field": "cursor"})
		}
		if cur.ContainerUUID != containerUUID || (cur.Version >= 2 && cur.Scope != scope) {
			return nil, NewValidationError("timeline cursor does not match the request", map[string]any{"field": "cursor"})
		}
		// A cursor is a POSITION in one direction and cannot be reinterpreted in
		// the other: the ascending reader's position is an exclusive lower bound
		// and the descending reader's an exclusive upper one. Refuse the
		// contradiction rather than silently paging the wrong way.
		cursorDesc := cur.Version == 3
		if requested != timelineOrderUnset && cursorDesc != desc {
			return nil, NewValidationError("order contradicts the cursor's direction", map[string]any{
				"field": "order", "reason": "cursor_direction_mismatch",
			})
		}
		desc = cursorDesc
		if cur.Version == 1 {
			cur.Scope = scope
			cur.SnapshotProjectEventID = 0
			cur.AfterProjectEventID = 0
		}
	}
	// A tail follows APPENDS, which only ever arrive at the newest end, so it is
	// ascending by construction.
	if desc && p.Tail {
		return nil, NewValidationError("tail requires ascending order", map[string]any{
			"field": "order", "reason": "tail_requires_ascending",
		})
	}

	currentEventID, currentProjectEventID, err := timelineSourceMaxima(ctx, tx)
	if err != nil {
		return nil, err
	}
	if p.Cursor == "" {
		cur.SnapshotEventID = currentEventID
		cur.SnapshotProjectEventID = currentProjectEventID
		// The descending reader starts AT the fence and walks down, so its
		// exclusive upper bound opens one past the newest row.
		cur.BeforeEventID = currentEventID + 1
		cur.BeforeProjectEventID = currentProjectEventID + 1
		if p.Tail && since == nil {
			cur.AfterEventID = currentEventID
			cur.AfterProjectEventID = currentProjectEventID
		}
		// An ascending read with a floor must SEEK to it. The descending reader
		// starts at the newest row and closes each source as soon as it passes
		// below `since`, so it never scans history it cannot deliver. The
		// ascending reader starts at the OLDEST row, so without this it grinds
		// through every event ever written before reaching the window -- and a
		// tail is ascending by construction. That was the whole cost of
		// `--follow --since`: on an 80k-event ledger it paged ~78 times at the
		// poll interval, ~16s, before printing a backlog the same `--since`
		// without `--follow` returned in 0.15s.
		//
		// The seek is sound because both sources order by an id monotonic with
		// the timestamp the timeline actually filters on: event_log.timestamp
		// and project_events.created_at are server-assigned at insert (schema
		// default strftime('%Y-%m-%dT%H:%M:%SZ','now')). Note this is created_at,
		// NOT the caller-supplied occurred_at -- a backdated `wrkp post
		// --occurred-at` never moves a project event's position in the scan.
		// Only the opening page seeks; later pages carry an authoritative cursor.
		if !desc && since != nil {
			floor := since.UTC().Format(time.RFC3339)
			eventFloor, ferr := timelineSeekFloor(ctx, tx,
				"SELECT MIN(id) FROM event_log WHERE timestamp >= ?", floor, currentEventID)
			if ferr != nil {
				return nil, ferr
			}
			projectFloor, ferr := timelineSeekFloor(ctx, tx,
				"SELECT MIN(id) FROM project_events WHERE created_at >= ?", floor, currentProjectEventID)
			if ferr != nil {
				return nil, ferr
			}
			if eventFloor > cur.AfterEventID {
				cur.AfterEventID = eventFloor
			}
			if projectFloor > cur.AfterProjectEventID {
				cur.AfterProjectEventID = projectFloor
			}
		}
	} else if p.Tail {
		cur.SnapshotEventID = currentEventID
		cur.SnapshotProjectEventID = currentProjectEventID
	}
	cur.Version = 2
	if desc {
		cur.Version = 3
	}

	eventLow, eventHigh := timelineScanWindow(desc, cur.AfterEventID, cur.SnapshotEventID, cur.BeforeEventID)
	projectLow, projectHigh := timelineScanWindow(desc, cur.AfterProjectEventID, cur.SnapshotProjectEventID, cur.BeforeProjectEventID)
	eventRows, eventTruncated, err := loadTimelineRawEvents(ctx, tx, eventLow, eventHigh, desc)
	if err != nil {
		return nil, err
	}
	projectRows, projectTruncated, err := loadTimelineRawProjectEvents(ctx, tx, projectLow, projectHigh, desc)
	if err != nil {
		return nil, err
	}
	horizon := timelineMergeHorizon(desc,
		timelineScanBound(eventTruncated, len(eventRows) > 0, lastEventTimestamp(eventRows)),
		timelineScanBound(projectTruncated, len(projectRows) > 0, lastProjectTimestamp(projectRows)),
	)
	eventKept := trimTimelineEventRows(eventRows, horizon, desc)
	projectKept := trimTimelineProjectRows(projectRows, horizon, desc)
	eventDrained := !eventTruncated && len(eventKept) == len(eventRows)
	projectDrained := !projectTruncated && len(projectKept) == len(projectRows)
	eventRows, projectRows = eventKept, projectKept

	entries := make([]WrkqTimelineEntry, 0, limit)
	eventIndex, projectIndex := 0, 0
	for len(entries) < limit && (eventIndex < len(eventRows) || projectIndex < len(projectRows)) {
		popEvent := projectIndex >= len(projectRows)
		if eventIndex < len(eventRows) && projectIndex < len(projectRows) {
			popEvent = timelineHeadFirst(eventRows[eventIndex], projectRows[projectIndex], desc)
		}
		if popEvent {
			raw := eventRows[eventIndex]
			eventIndex++
			cur.AfterEventID = raw.entry.EventID
			cur.BeforeEventID = raw.entry.EventID
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
		cur.AfterProjectEventID = raw.id
		cur.BeforeProjectEventID = raw.id
		if entry, included := deliverTimelineProjectEvent(raw, containerUUID, affiliation, p.Types, taskUUID, since); included {
			entries = append(entries, entry)
		}
	}

	// Addressees are hydrated for the DELIVERED messages only, in one query,
	// rather than as a correlated subquery on every scanned row: the scan reads
	// up to a full page cap, the delivery is bounded by `limit`.
	if err := hydrateTimelineAddressees(ctx, tx, entries); err != nil {
		return nil, err
	}

	hasMore := cur.AfterEventID < cur.SnapshotEventID || cur.AfterProjectEventID < cur.SnapshotProjectEventID
	if desc {
		// The descending reader has no cheap fence to compare against -- it
		// walks toward id 0 -- so a source reports itself drained when its scan
		// was neither truncated by the cap nor trimmed by the horizon. `since`
		// closes it earlier: below the floor no older row can ever match.
		// Only the CONSUMED prefix may close a source. A page that filled the
		// delivery limit early leaves kept rows unread, and those are re-read
		// from the unchanged position on the next page.
		if (eventDrained && eventIndex == len(eventRows)) || timelineEventFloorReached(eventRows[:eventIndex], since) {
			cur.BeforeEventID = 0
		}
		if (projectDrained && projectIndex == len(projectRows)) || timelineProjectFloorReached(projectRows[:projectIndex], since) {
			cur.BeforeProjectEventID = 0
		}
		hasMore = cur.BeforeEventID > 0 || cur.BeforeProjectEventID > 0
	}
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

// timelineSeekFloor returns the exclusive lower bound for an ascending scan
// with a `since` floor: one before the first row at or after it. When no row
// qualifies, the entire source lies behind the floor, so the bound is its
// current maximum and the scan starts at the end with nothing to skip.
func timelineSeekFloor(ctx context.Context, tx *sql.Tx, query, floor string, max int64) (int64, error) {
	var first sql.NullInt64
	if err := tx.QueryRowContext(ctx, query, floor).Scan(&first); err != nil {
		return 0, NewInternalError(err)
	}
	if !first.Valid {
		return max, nil
	}
	return first.Int64 - 1, nil
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

// hydrateTimelineAddressees fills each message entry's `to` with every
// addressee of that one say. The rows were collapsed to one entry per group, so
// the addressees have to be read back from the group; obligation 'none' (a log
// entry) addresses nobody and keeps an empty list. Handles are sorted so the
// projection is deterministic regardless of scan order.
func hydrateTimelineAddressees(ctx context.Context, tx *sql.Tx, entries []WrkqTimelineEntry) error {
	groups := make([]any, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Message == nil || entry.Message.GroupID == "" || seen[entry.Message.GroupID] {
			continue
		}
		seen[entry.Message.GroupID] = true
		groups = append(groups, entry.Message.GroupID)
	}
	if len(groups) == 0 {
		return nil
	}
	query := `SELECT group_id, COALESCE(to_scope_ref, to_principal_ref)
		    FROM envelopes
		   WHERE group_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(groups)), ",") + `)
		     AND (to_scope_ref IS NOT NULL OR to_principal_ref IS NOT NULL)`
	rows, err := tx.QueryContext(ctx, query, groups...)
	if err != nil {
		return NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	addressees := map[string][]string{}
	for rows.Next() {
		var group, handle string
		if err := rows.Scan(&group, &handle); err != nil {
			return NewInternalError(err)
		}
		addressees[group] = append(addressees[group], handle)
	}
	if err := rows.Err(); err != nil {
		return NewInternalError(err)
	}
	for index := range entries {
		message := entries[index].Message
		if message == nil {
			continue
		}
		if to := addressees[message.GroupID]; len(to) > 0 {
			sort.Strings(to)
			message.To = to
		}
	}
	return nil
}

func loadTimelineRawEvents(ctx context.Context, tx *sql.Tx, low, high int64, desc bool) ([]timelineRawEvent, bool, error) {
	// The envelope joins are guarded on event_type, so a non-envelope row costs
	// one NULL probe. The `env` join carries the fan-out collapse in its own ON
	// clause: a say to N addressees writes N rows sharing one group_id whose
	// value is the FIRST envelope's own id, so exactly one row per say satisfies
	// id = group_id. The other N-1 join to nothing and are dropped by the same
	// delivery filter that drops an unsupported event type. COALESCE guards a
	// null group_id so an unstamped row reports itself rather than vanishing.
	rows, err := tx.QueryContext(ctx, timelineOrdered(`
		SELECT e.id, e.timestamp, COALESCE(e.principal_ref, ''), COALESCE(e.resource_uuid, ''),
		       e.event_type, COALESCE(e.payload, ''),
		       COALESCE(t.uuid, comment_task.uuid, env_task.uuid, ''),
		       COALESCE(t.id, comment_task.id, env_task.id, ''),
		       COALESCE(tp.path, comment_tp.path, env_tp.path, json_extract(e.payload, '$.slug'), ''),
		       COALESCE(cm.id, ''), cm.kind, COALESCE(cm.body, ''), cm.meta,
		       env.id, COALESCE(env.group_id, env.id), rm.id, rm.kind,
		       COALESCE(env.from_scope_ref, env.from_principal_ref),
		       env.obligation, env.body,
		       COALESCE(rm.container_uuid, room_task.project_uuid),
		       room_task.campaign_uuid
		  FROM event_log e
		  LEFT JOIN tasks t ON e.resource_type = 'task' AND t.uuid = e.resource_uuid
		  LEFT JOIN v_task_paths tp ON tp.uuid = t.uuid
		  LEFT JOIN comments cm ON e.event_type = 'comment.created' AND cm.uuid = e.resource_uuid
		  LEFT JOIN tasks comment_task ON e.event_type = 'comment.created' AND comment_task.uuid = json_extract(e.payload, '$.task_id')
		  LEFT JOIN v_task_paths comment_tp ON comment_tp.uuid = comment_task.uuid
		  LEFT JOIN envelopes env ON e.event_type = 'envelope.created' AND env.uuid = e.resource_uuid
		                         AND env.id = COALESCE(env.group_id, env.id)
		  LEFT JOIN rooms rm ON rm.uuid = env.room_uuid
		  LEFT JOIN tasks room_task ON room_task.uuid = rm.task_uuid
		  LEFT JOIN tasks env_task ON env_task.uuid = COALESCE(env.task_uuid, rm.task_uuid)
		  LEFT JOIN v_task_paths env_tp ON env_tp.uuid = env_task.uuid
		 WHERE e.id > ? AND e.id <= ?
		 ORDER BY e.id %s LIMIT ?`, desc), low, high, monitorMaxPageLimit)
	if err != nil {
		return nil, false, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	result := []timelineRawEvent{}
	for rows.Next() {
		var raw timelineRawEvent
		if err := rows.Scan(
			&raw.entry.EventID, &raw.serverTime, &raw.entry.PrincipalRef, &raw.entry.ResourceUUID,
			&raw.eventType, &raw.payload, &raw.entry.TaskUUID, &raw.entry.TaskID, &raw.entry.TaskPath,
			&raw.commentID, &raw.commentKind, &raw.commentBody, &raw.commentMeta,
			&raw.envelope.id, &raw.envelope.groupID, &raw.envelope.roomID, &raw.envelope.roomKind,
			&raw.envelope.from, &raw.envelope.obligation, &raw.envelope.body,
			&raw.envelope.container, &raw.envelope.campaign,
		); err != nil {
			return nil, false, NewInternalError(err)
		}
		raw.entry.Timestamp = toRFC3339(raw.serverTime)
		result = append(result, raw)
	}
	if err := rows.Err(); err != nil {
		return nil, false, NewInternalError(err)
	}
	return result, len(result) == monitorMaxPageLimit, nil
}

func loadTimelineRawProjectEvents(ctx context.Context, tx *sql.Tx, low, high int64, desc bool) ([]timelineRawProjectEvent, bool, error) {
	rows, err := tx.QueryContext(ctx, timelineOrdered(timelineProjectEventsRawQuery, desc), low, high, monitorMaxPageLimit)
	if err != nil {
		return nil, false, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	result := []timelineRawProjectEvent{}
	for rows.Next() {
		var raw timelineRawProjectEvent
		var principal, scope, campaign, task sql.NullString
		var detail WrkqTimelineProjectEvent
		var attributes string
		if err := rows.Scan(
			&raw.id, &detail.UUID, &raw.semantic, &attributes,
			&principal, &scope, &detail.Summary, &detail.OccurredAt,
			&raw.serverTime, &raw.entry.ContainerUUID, &campaign, &task,
			&raw.entry.TaskID, &raw.entry.TaskPath,
		); err != nil {
			return nil, false, NewInternalError(err)
		}
		detail.Attributes = json.RawMessage(attributes)
		detail.Type = raw.semantic
		detail.PrincipalRef = nullStringPtr(principal)
		detail.ScopeRef = nullStringPtr(scope)
		detail.OccurredAt = toRFC3339(detail.OccurredAt)
		raw.entry.Type = "project.event"
		raw.entry.Timestamp = toRFC3339(raw.serverTime)
		if detail.PrincipalRef != nil {
			raw.entry.PrincipalRef = *detail.PrincipalRef
		}
		raw.entry.CampaignUUID = nullStringPtr(campaign)
		if task.Valid {
			raw.entry.TaskUUID = task.String
		}
		raw.entry.ProjectEvent = &detail
		result = append(result, raw)
	}
	if err := rows.Err(); err != nil {
		return nil, false, NewInternalError(err)
	}
	return result, len(result) == monitorMaxPageLimit, nil
}

// project_uuid is intentionally absent: current subtree membership is the one
// project reader authority; the stored project is idempotency scope only.
const timelineProjectEventsRawQuery = `
		SELECT pe.id, pe.uuid, pe.type, pe.attributes, pe.principal_ref, pe.scope_ref,
		       pe.summary, pe.occurred_at, pe.created_at,
		       pe.container_uuid, pe.campaign_uuid, pe.task_uuid,
		       COALESCE(t.id, ''), COALESCE(tp.path, '')
		  FROM project_events pe
		  LEFT JOIN tasks t ON t.uuid = pe.task_uuid
		  LEFT JOIN v_task_paths tp ON tp.uuid = t.uuid
		 WHERE pe.id > ? AND pe.id <= ?
		 ORDER BY pe.id %s LIMIT ?`

// The merged timeline is assembled from two independently scanned sources, and
// each scan is bounded by a ROW COUNT, not by time. Those two facts do not
// compose: event_log is large and project_events is small, so one page of the
// fixed fence drains project_events entirely while event_log is still tens of
// thousands of rows into its own past. Merging those two pages is locally
// correct and globally wrong — every project event is emitted before the event
// rows that share its week, which arrive pages later and jump the delivered
// stream backwards (T-08328: a 47-day reversal at the page-one boundary, so any
// --limit under the project-event count returned nothing but git facts).
//
// The horizon is the repair. A source truncated by its scan cap still has
// unread rows immediately after its last one, so NO source may emit past that
// timestamp; the withheld rows would otherwise be delivered on a later page,
// behind entries older than themselves. A source that drained to the fence has
// no such rows and imposes no bound. The page's horizon is therefore the
// EARLIEST bound any truncated source imposes, and every source is trimmed to
// it before the merge. Per-source cursors advance only to the last KEPT row, so
// the withheld rows are re-read, in order, on the next page.
//
// Progress is guaranteed: the source that DEFINES the horizon keeps its whole
// page, because its own last row is the horizon. A page can therefore never
// trim both sources to nothing, and the fence always drains.

// Delivery direction. Ascending is the default and the shape every caller
// before T-08328 got; descending is what a `log` verb wants, where --limit N
// means "the N most recent" and the newest entry is the first one delivered.
// Descending is also the cheaper read for that question: it starts AT the fence
// instead of walking the whole history to reach it.
const (
	timelineOrderUnset = ""
	timelineOrderAsc   = "asc"
	timelineOrderDesc  = "desc"
)

func parseTimelineOrder(raw string) (string, error) {
	switch value := strings.TrimSpace(raw); value {
	case timelineOrderUnset, timelineOrderAsc, timelineOrderDesc:
		return value, nil
	default:
		return "", NewValidationError("order must be asc or desc", map[string]any{"field": "order"})
	}
}

// timelineOrdered stamps the scan direction into a source query. The token is
// from a closed set, never caller text.
func timelineOrdered(query string, desc bool) string {
	if desc {
		return fmt.Sprintf(query, "DESC")
	}
	return fmt.Sprintf(query, "ASC")
}

// timelineScanWindow maps a cursor position to the source's id window. The two
// directions carry opposite positions: ascending holds an exclusive LOWER bound
// under a fixed fence, descending an exclusive UPPER bound walking toward zero.
func timelineScanWindow(desc bool, after, snapshot, before int64) (int64, int64) {
	if desc {
		return 0, before - 1
	}
	return after, snapshot
}

// timelineEventFloorReached reports that a descending scan has passed below
// `since`: within one source id order is timestamp order, so once a consumed
// row is older than the floor no older row can ever match.
func timelineEventFloorReached(consumed []timelineRawEvent, since *time.Time) bool {
	if since == nil || len(consumed) == 0 {
		return false
	}
	return !timelineSinceMatches(consumed[len(consumed)-1].entry.Timestamp, since)
}

func timelineProjectFloorReached(consumed []timelineRawProjectEvent, since *time.Time) bool {
	if since == nil || len(consumed) == 0 {
		return false
	}
	return !timelineSinceMatches(consumed[len(consumed)-1].entry.Timestamp, since)
}

// timelineScanBound reports the timestamp bound one source imposes, or "" when
// it imposes none. Only a truncated source with rows bounds the page.
func timelineScanBound(truncated, hasRows bool, last string) string {
	if !truncated || !hasRows {
		return ""
	}
	return last
}

// timelineMergeHorizon is the earliest bound across sources. Timestamps are
// normalized by toRFC3339 to a fixed-width UTC layout, so lexical order is
// chronological order — the same comparison the merge itself makes.
func timelineMergeHorizon(desc bool, bounds ...string) string {
	horizon := ""
	for _, bound := range bounds {
		if bound == "" {
			continue
		}
		if horizon == "" || timelineAheadOf(horizon, bound, desc) {
			horizon = bound
		}
	}
	return horizon
}

// timelineAheadOf reports whether a is further along the delivery direction
// than b. Ascending delivery runs toward later timestamps, descending toward
// earlier ones, so "the earliest bound wins" inverts with the direction.
func timelineAheadOf(a, b string, desc bool) bool {
	if desc {
		return a < b
	}
	return a > b
}

func lastEventTimestamp(rows []timelineRawEvent) string {
	if len(rows) == 0 {
		return ""
	}
	return rows[len(rows)-1].entry.Timestamp
}

func lastProjectTimestamp(rows []timelineRawProjectEvent) string {
	if len(rows) == 0 {
		return ""
	}
	return rows[len(rows)-1].entry.Timestamp
}

// trimTimelineEventRows drops the trailing rows past the horizon. Rows are
// already in ascending timestamp order within a source, so the kept prefix is
// contiguous and the source cursor still advances monotonically.
func trimTimelineEventRows(rows []timelineRawEvent, horizon string, desc bool) []timelineRawEvent {
	if horizon == "" {
		return rows
	}
	for index, row := range rows {
		if timelineAheadOf(row.entry.Timestamp, horizon, desc) {
			return rows[:index]
		}
	}
	return rows
}

func trimTimelineProjectRows(rows []timelineRawProjectEvent, horizon string, desc bool) []timelineRawProjectEvent {
	if horizon == "" {
		return rows
	}
	for index, row := range rows {
		if timelineAheadOf(row.entry.Timestamp, horizon, desc) {
			return rows[:index]
		}
	}
	return rows
}

// timelineHeadFirst reports whether the event head is delivered before the
// project head. Descending delivery is the exact reverse of ascending, ties
// included: event_log has source rank zero and wins an ascending server-time
// tie, so it must LOSE the descending one for a descending page to be the
// reverse of the ascending page over the same rows.
func timelineHeadFirst(event timelineRawEvent, project timelineRawProjectEvent, desc bool) bool {
	if event.entry.Timestamp != project.entry.Timestamp {
		return timelineAheadOf(project.entry.Timestamp, event.entry.Timestamp, desc)
	}
	return !desc
}

func deliverTimelineEvent(raw timelineRawEvent, root string, affiliation map[string]bool, filters []string, taskUUID string, since *time.Time) (WrkqTimelineEntry, bool, error) {
	if !timelineEventTypeSupported(raw.eventType, raw.payload) {
		return WrkqTimelineEntry{}, false, nil
	}
	// The fan-out siblings of a say join to no envelope row (the leader
	// predicate lives in the join), so they are dropped here exactly as an
	// unsupported event type is: the cursor has already advanced past them.
	if raw.eventType == "envelope.created" && !raw.envelope.id.Valid {
		return WrkqTimelineEntry{}, false, nil
	}
	entry := raw.entry
	if err := normalizeTimelineEntry(&entry, raw.eventType, raw.payload, root); err != nil {
		return WrkqTimelineEntry{}, false, NewInternalError(err)
	}
	if raw.eventType == "envelope.created" {
		applyTimelineEnvelope(&entry, raw.envelope)
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

// applyTimelineEnvelope carries the room-message columns onto the entry. An
// envelope event's payload holds no affiliation, so container and campaign come
// from the ROOM: a task room resolves to the same container and campaign a
// comment on that task would carry, and a container room to its own container.
// An ad-hoc room is anchored to neither, so it resolves to no container and the
// membership test that follows excludes it with no special case.
func applyTimelineEnvelope(entry *WrkqTimelineEntry, env timelineRawEnvelope) {
	if env.container.Valid {
		entry.ContainerUUID = env.container.String
	}
	if env.campaign.Valid {
		value := env.campaign.String
		entry.CampaignUUID = &value
	}
	message := &WrkqTimelineMessage{
		EnvelopeID: env.id.String,
		GroupID:    env.groupID.String,
		RoomID:     env.roomID.String,
		RoomKind:   env.roomKind.String,
		From:       env.from.String,
		Obligation: env.obligation.String,
		Body:       env.body.String,
		To:         []string{},
	}
	entry.Message = message
}

func timelineEventTypeSupported(eventType, payload string) bool {
	switch eventType {
	case "comment.created", "envelope.created", "task.outcome_set", "task.archived", "task.deleted", "task.restored", "task.purged", "container.campaign_state_changed":
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
	case 3:
		if (cur.Scope != "container" && cur.Scope != "subtree") ||
			cur.BeforeEventID < 0 || cur.BeforeProjectEventID < 0 ||
			cur.BeforeEventID > cur.SnapshotEventID+1 || cur.BeforeProjectEventID > cur.SnapshotProjectEventID+1 {
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
