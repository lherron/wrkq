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
)

// The v2 reader's request: its vocabulary (delivery order, public type filters
// and their stored types, the since/before window), its validation, and the
// cursor it resumes or opens.

// timelineV2Request is one validated v2 read: what each source scans, the time
// window, the delivery direction, and the cursor position the page resumes
// from and advances.
type timelineV2Request struct {
	filters         []string
	eventTypes      []string
	eventEligible   bool
	projectEligible bool
	since           *time.Time
	before          *time.Time
	desc            bool
	cur             timelineCursor
}

// newTimelineV2Request validates the request's filters, window, and order, and
// resumes or opens its cursor. A resumed cursor is bound to the query it was
// minted for, so a page can never be continued under a different filter.
func newTimelineV2Request(p ContainerTimelineViewParams, containerUUID, scope string) (*timelineV2Request, error) {
	for _, filter := range p.Types {
		if strings.TrimSpace(filter) == "" || (strings.Contains(filter, "*") && !strings.HasSuffix(filter, ".*")) {
			return nil, NewValidationError("type filters must be exact or trailing globs", map[string]any{"field": "types"})
		}
	}
	req := &timelineV2Request{filters: timelineCanonicalFilters(p.Types)}
	req.eventTypes = timelineStoredEventTypes(req.filters)
	req.eventEligible = len(req.filters) == 0 || len(req.eventTypes) > 0
	req.projectEligible = timelineProjectSourceEligible(req.filters)
	if p.Before != "" {
		value, err := time.Parse(time.RFC3339Nano, p.Before)
		if err != nil {
			return nil, NewValidationError("before must be RFC3339", map[string]any{"field": "before"})
		}
		value = value.UTC()
		req.before = &value
	}

	requested, err := parseTimelineOrder(p.Order)
	if err != nil {
		return nil, err
	}
	req.desc = requested == timelineOrderDesc

	req.cur = timelineCursor{Version: 2, ContainerUUID: containerUUID, Scope: scope}
	if p.Cursor != "" {
		if err := req.resume(p, containerUUID, scope, requested); err != nil {
			return nil, err
		}
	}
	if req.since == nil && (!req.cur.QueryBound || p.Since == "") {
		req.since, err = parseTimelineSince(p.Since)
		if err != nil {
			return nil, err
		}
	}
	if req.before != nil && req.since != nil && !req.before.After(*req.since) {
		return nil, NewValidationError("before must be after since", map[string]any{"field": "before"})
	}
	if p.Cursor == "" && (len(req.filters) > 0 || p.Task != "" || p.Since != "" || req.before != nil) {
		req.cur.QueryBound = true
		req.cur.QueryTypes = req.filters
		req.cur.QueryTask = p.Task
		req.cur.QuerySinceInput = p.Since
		if req.since != nil {
			req.cur.QuerySinceFloor = req.since.Format(time.RFC3339Nano)
		}
		req.cur.QueryBefore = timelineCanonicalBefore(req.before)
	}
	// A tail follows APPENDS, which only ever arrive at the newest end, so it is
	// ascending by construction.
	if req.desc && p.Tail {
		return nil, NewValidationError("tail requires ascending order", map[string]any{
			"field": "order", "reason": "tail_requires_ascending",
		})
	}
	return req, nil
}

// resume decodes the request's cursor and checks it against the request.
func (req *timelineV2Request) resume(p ContainerTimelineViewParams, containerUUID, scope, requested string) error {
	cur, err := decodeTimelineCursorAny(p.Cursor)
	if err != nil {
		return NewValidationError("invalid timeline cursor", map[string]any{"field": "cursor"})
	}
	if cur.ContainerUUID != containerUUID || (cur.Version >= 2 && cur.Scope != scope) {
		return NewValidationError("timeline cursor does not match the request", map[string]any{"field": "cursor"})
	}
	// A cursor is a POSITION in one direction and cannot be reinterpreted in
	// the other: the ascending reader's position is an exclusive lower bound
	// and the descending reader's an exclusive upper one. Refuse the
	// contradiction rather than silently paging the wrong way.
	cursorDesc := cur.Version == 3
	if requested != timelineOrderUnset && cursorDesc != req.desc {
		return NewValidationError("order contradicts the cursor's direction", map[string]any{
			"field": "order", "reason": "cursor_direction_mismatch",
		})
	}
	req.desc = cursorDesc
	if cur.Version == 1 {
		cur.Scope = scope
		cur.SnapshotProjectEventID = 0
		cur.AfterProjectEventID = 0
	}
	if cur.QueryBound {
		if !timelineSameFilters(req.filters, cur.QueryTypes) || p.Task != cur.QueryTask ||
			p.Since != cur.QuerySinceInput || timelineCanonicalBefore(req.before) != cur.QueryBefore {
			return NewValidationError("timeline cursor does not match the request", map[string]any{"field": "cursor"})
		}
		if cur.QuerySinceFloor != "" {
			value, err := time.Parse(time.RFC3339Nano, cur.QuerySinceFloor)
			if err != nil {
				return NewValidationError("invalid timeline cursor", map[string]any{"field": "cursor"})
			}
			req.since = &value
		}
	}
	req.cur = cur
	return nil
}

// positionCursor fixes the fence each source scans up to and, on an opening
// page, where each scan starts. A resumed page keeps its cursor's positions;
// only a tail moves its fence to pick up appends.
func (req *timelineV2Request) positionCursor(ctx context.Context, tx *sql.Tx, p ContainerTimelineViewParams) error {
	currentEventID, currentProjectEventID, err := timelineSourceMaxima(ctx, tx, req.eventEligible, req.projectEligible)
	if err != nil {
		return err
	}
	cur := &req.cur
	if p.Cursor == "" {
		cur.SnapshotEventID = currentEventID
		cur.SnapshotProjectEventID = currentProjectEventID
		if req.before != nil {
			ceiling := timelineDBCeil(*req.before)
			if req.eventEligible {
				cur.SnapshotEventID, err = timelineSeekCeiling(ctx, tx,
					"SELECT (SELECT id FROM event_log INDEXED BY event_log_time_id_idx WHERE timestamp < ? ORDER BY timestamp DESC, id DESC LIMIT 1)", ceiling)
				if err != nil {
					return err
				}
			}
			if req.projectEligible {
				cur.SnapshotProjectEventID, err = timelineSeekCeiling(ctx, tx,
					"SELECT (SELECT id FROM project_events INDEXED BY project_events_time_id_idx WHERE created_at < ? ORDER BY created_at DESC, id DESC LIMIT 1)", ceiling)
				if err != nil {
					return err
				}
			}
		}
		// The descending reader starts AT the fence and walks down, so its
		// exclusive upper bound opens one past the newest row.
		cur.BeforeEventID = cur.SnapshotEventID + 1
		cur.BeforeProjectEventID = cur.SnapshotProjectEventID + 1
		if p.Tail && req.since == nil {
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
		if !req.desc && req.since != nil && len(req.filters) == 0 {
			floor := req.since.UTC().Format(time.RFC3339)
			if req.eventEligible {
				eventFloor, ferr := timelineSeekFloor(ctx, tx,
					"SELECT (SELECT id FROM event_log INDEXED BY event_log_time_id_idx WHERE timestamp >= ? ORDER BY timestamp, id LIMIT 1)", floor, currentEventID)
				if ferr != nil {
					return ferr
				}
				if eventFloor > cur.AfterEventID {
					cur.AfterEventID = eventFloor
				}
			}
			projectFloor, ferr := timelineSeekFloor(ctx, tx,
				"SELECT (SELECT id FROM project_events INDEXED BY project_events_time_id_idx WHERE created_at >= ? ORDER BY created_at, id LIMIT 1)", floor, currentProjectEventID)
			if ferr != nil {
				return ferr
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
	if req.desc {
		cur.Version = 3
	}
	return nil
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

func timelineSeekCeiling(ctx context.Context, tx *sql.Tx, query, ceiling string) (int64, error) {
	var last sql.NullInt64
	if err := tx.QueryRowContext(ctx, query, ceiling).Scan(&last); err != nil {
		return 0, NewInternalError(err)
	}
	if !last.Valid {
		return 0, nil
	}
	return last.Int64, nil
}

func timelineSourceMaxima(ctx context.Context, tx *sql.Tx, eventEligible, projectEligible bool) (int64, int64, error) {
	var eventID, projectEventID int64
	if eventEligible {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM event_log`).Scan(&eventID); err != nil {
			return 0, 0, NewInternalError(err)
		}
	}
	if projectEligible {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(id), 0) FROM project_events`).Scan(&projectEventID); err != nil {
			return 0, 0, NewInternalError(err)
		}
	}
	return eventID, projectEventID, nil
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

// The public vocabulary is smaller than the stored event vocabulary. Keep
// task.updated for both state and edited: its payload decides the public type.
var timelinePublicStoredTypes = map[string][]string{
	"comment": {"comment.created"}, "message": {"envelope.created"},
	"task.outcome": {"task.outcome_set"}, "task.created": {"task.created"},
	"task.claimed": {"task.claimed"}, "task.claim_released": {"task.claim_released"},
	"task.edited": {"task.updated"}, "task.moved": {"task.moved"},
	"task.state":      {"task.updated", "task.archived", "task.deleted", "task.restored", "task.purged"},
	"container.state": {"container.campaign_state_changed"},
}

func timelineCanonicalFilters(filters []string) []string {
	if len(filters) == 0 {
		return nil
	}
	seen := map[string]bool{}
	result := []string{}
	for _, filter := range filters {
		filter = strings.TrimSpace(filter)
		if !seen[filter] {
			result = append(result, filter)
			seen[filter] = true
		}
	}
	sort.Strings(result)
	return result
}

func timelineSameFilters(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func timelineStoredEventTypes(filters []string) []string {
	if len(filters) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for public, stored := range timelinePublicStoredTypes {
		if !timelineTypeMatches(filters, public) {
			continue
		}
		for _, value := range stored {
			seen[value] = true
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func timelineProjectSourceEligible(filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		candidate := filter
		if strings.HasSuffix(filter, ".*") {
			candidate = strings.TrimSuffix(filter, "*") + "x"
		}
		if !projectEventTypePattern.MatchString(candidate) {
			continue
		}
		namespace := strings.SplitN(candidate, ".", 2)[0]
		if _, reserved := reservedProjectEventNamespaces[namespace]; !reserved {
			return true
		}
	}
	return false
}

func timelineCanonicalBefore(before *time.Time) string {
	if before == nil {
		return ""
	}
	return before.UTC().Format(time.RFC3339Nano)
}

// Stored server timestamps have second precision. Ceiling preserves inclusive
// since and exclusive before when a caller supplies fractional seconds.
func timelineDBCeil(value time.Time) string {
	value = value.UTC()
	if value.Nanosecond() != 0 {
		value = value.Truncate(time.Second).Add(time.Second)
	}
	return value.Format(time.RFC3339)
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
