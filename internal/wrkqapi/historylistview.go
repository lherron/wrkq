package wrkqapi

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/id"
)

// HistoryListViewParams mirrors the legacy `wrkq log <PATHSPEC|ID>` surface. The
// CLI scopes the raw target through the project-root scoper BEFORE these params
// are sent; the server NEVER reads project-root env/flags. Resource resolution
// (friendly ID / UUID → resource_type+resource_uuid among task/container/actor)
// is durable read behavior and so is owned here on the server side.
type HistoryListViewParams struct {
	Target string `json:"target"`
	Since  string `json:"since,omitempty"`
	Until  string `json:"until,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// WrkqLogEvent matches the legacy logEvent shape EXACTLY (field order + json tags
// + pointer/omitempty). Marshaled by encoding/json — NOT alphabetical (legacy
// uses a struct, not a map), so the field order here is the wire order. payload
// stays a STRING (the raw event_log.payload); --patch is rendered CLIENT-side.
type WrkqLogEvent struct {
	ID           int64     `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	PrincipalRef *string   `json:"principal_ref,omitempty"`
	ScopeRef     *string   `json:"scope_ref,omitempty"`
	ResourceType string    `json:"resource_type"`
	ResourceUUID string    `json:"resource_uuid"`
	EventType    string    `json:"event_type"`
	ETag         *int64    `json:"etag,omitempty"`
	Payload      *string   `json:"payload,omitempty"`
}

// WrkqHistoryListView is the server-owned CLI COMPATIBILITY history read model for
// `wrkq log`. It reads the generic event_log table (NOT workflow_events, which is
// the wrkf.event.query substrate), resolves the caller-scoped target to exactly
// one (resource_type, resource_uuid), and owns cursor.Apply + limit+1 +
// BuildNextCursor over event_log.id DESC. Not a canonical resource/event-stream
// API. Field order is legacy struct order, NOT alphabetical.
type WrkqHistoryListView struct {
	Items      []WrkqLogEvent `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
}

// HistoryListView reproduces legacy `wrkq log` byte-for-byte: the same resource
// resolution, the same event_log query with since/until + cursor pagination over
// id DESC, and the same legacy error wrapping ("failed to resolve resource: ..."
// / "failed to query event log: ..."). The legacy command wraps the inner errors
// CLIENT-side; here the server returns the fully-wrapped message so the mirror can
// surface it raw (after stripping the domain-code prefix), matching legacy bytes.
func (a *API) HistoryListView(ctx context.Context, p HistoryListViewParams) (*WrkqHistoryListView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	resourceUUID, resourceType, err := a.resolveLogResource(ctx, p.Target)
	if err != nil {
		// Legacy runLog wraps with "failed to resolve resource: %w".
		return nil, prefixLogError("failed to resolve resource: ", err)
	}

	events, hasMore, err := a.queryLogEventLog(ctx, resourceUUID, resourceType, p)
	if err != nil {
		// Legacy runLog wraps with "failed to query event log: %w".
		return nil, prefixLogError("failed to query event log: ", err)
	}

	view := &WrkqHistoryListView{Items: events}
	if hasMore && len(events) > 0 {
		last := events[len(events)-1]
		view.NextCursor, _ = cursor.BuildNextCursor(
			[]string{"id"},
			[]any{last.ID},
			fmt.Sprintf("%d", last.ID),
		)
	}
	return view, nil
}

// resolveLogResource resolves the already-caller-scoped target to exactly one
// (resource_uuid, resource_type) among task/container/actor, by friendly ID
// (T-*/P-*/A-*) AND UUID — byte-matching legacy resolveResource error text. The
// path-resolution branch is a PINNED divergence: legacy advertises paths in help
// but intentionally errors today.
func (a *API) resolveLogResource(ctx context.Context, target string) (string, string, error) {
	if id.IsFriendlyID(target) {
		prefix := target[:1]
		switch prefix {
		case "T":
			var uuid string
			if err := a.db.QueryRowContext(ctx, "SELECT uuid FROM tasks WHERE id = ?", target).Scan(&uuid); err != nil {
				return "", "", NewValidationError(fmt.Sprintf("task not found: %s", target), nil)
			}
			return uuid, "task", nil
		case "P":
			var uuid string
			if err := a.db.QueryRowContext(ctx, "SELECT uuid FROM containers WHERE id = ?", target).Scan(&uuid); err != nil {
				return "", "", NewValidationError(fmt.Sprintf("container not found: %s", target), nil)
			}
			return uuid, "container", nil
		default:
			return "", "", NewValidationError(fmt.Sprintf("unknown friendly ID prefix: %s", prefix), nil)
		}
	}

	if len(target) == 36 && strings.Count(target, "-") == 4 {
		var count int
		if err := a.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM tasks WHERE uuid = ?", target).Scan(&count); err == nil && count > 0 {
			return target, "task", nil
		}
		if err := a.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM containers WHERE uuid = ?", target).Scan(&count); err == nil && count > 0 {
			return target, "container", nil
		}
		return "", "", NewValidationError(fmt.Sprintf("UUID not found: %s", target), nil)
	}

	// PINNED DIVERGENCE: legacy path resolution is intentionally a TODO; path args
	// advertised in help but error today. Do NOT add path resolution here unless
	// legacy changes in the same deliberate semantic-fix increment.
	return "", "", NewValidationError(fmt.Sprintf("path resolution not yet implemented: %s", target), nil)
}

// queryLogEventLog reproduces legacy queryEventLog: cursor.Apply over e.id DESC,
// since/until time filters with legacy error text, limit+1 hasMore detection, and
// the LEFT JOIN to actors for slug/id. Server default limit = 50 (0 = unlimited)
// is applied by the registry/mirror, not here (limit flows in via p.Limit).
func (a *API) queryLogEventLog(ctx context.Context, resourceUUID, resourceType string, p HistoryListViewParams) ([]WrkqLogEvent, bool, error) {
	pag, err := cursor.Apply(p.Cursor, cursor.ApplyOptions{
		SortFields: []string{"id"},
		SQLFields:  []string{"e.id"},
		Descending: []bool{true},
		IDField:    "e.id",
		Limit:      p.Limit,
	})
	if err != nil {
		// cursor.Apply prefixes "invalid cursor:"; surface it raw (matches legacy).
		return nil, false, NewValidationError(err.Error(), nil)
	}

	query := `
		SELECT e.id, e.timestamp, e.principal_ref, e.scope_ref,
		       e.resource_type, e.resource_uuid, e.event_type, e.etag, e.payload
		FROM event_log e
		WHERE e.resource_uuid = ? AND e.resource_type = ?
	`
	args := []any{resourceUUID, resourceType}

	if p.Since != "" {
		sinceTime, perr := parseLogTimeFilter(p.Since)
		if perr != nil {
			return nil, false, NewValidationError(fmt.Sprintf("invalid --since value: %s", perr.Error()), nil)
		}
		query += " AND e.timestamp >= ?"
		args = append(args, sinceTime.Format(time.RFC3339))
	}

	if p.Until != "" {
		untilTime, perr := parseLogTimeFilter(p.Until)
		if perr != nil {
			return nil, false, NewValidationError(fmt.Sprintf("invalid --until value: %s", perr.Error()), nil)
		}
		query += " AND e.timestamp <= ?"
		args = append(args, untilTime.Format(time.RFC3339))
	}

	if pag.WhereClause != "" {
		query += " AND " + pag.WhereClause
		args = append(args, pag.Params...)
	}

	query += " " + pag.OrderByClause

	if pag.LimitClause != "" {
		query += " " + pag.LimitClause
		args = append(args, *pag.LimitParam)
	}

	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()

	var events []WrkqLogEvent
	for rows.Next() {
		var e WrkqLogEvent
		var timestampStr string

		if err := rows.Scan(
			&e.ID,
			&timestampStr,
			&e.PrincipalRef,
			&e.ScopeRef,
			&e.ResourceType,
			&e.ResourceUUID,
			&e.EventType,
			&e.ETag,
			&e.Payload,
		); err != nil {
			return nil, false, NewInternalError(err)
		}

		// Parse timestamp the same way legacy does (RFC3339, then a fallback).
		e.Timestamp, err = time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			e.Timestamp, _ = time.Parse("2006-01-02T15:04:05Z", timestampStr)
		}

		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, false, NewInternalError(err)
	}

	hasMore := false
	if p.Limit > 0 && len(events) > p.Limit {
		hasMore = true
		events = events[:p.Limit]
	}

	return events, hasMore, nil
}

// parseLogTimeFilter reproduces legacy parseTimeFilter: RFC3339, then YYYY-MM-DD,
// else the exact legacy error string.
func parseLogTimeFilter(value string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid time format: %s (use YYYY-MM-DD or RFC3339)", value)
}

// prefixLogError reproduces legacy runLog's "failed to resolve resource: %w" /
// "failed to query event log: %w" wrapping while preserving the underlying domain
// code (so the wire error.data.code and the mirror's stripped message both match
// the legacy raw text). A non-domain error is wrapped as an internal error.
func prefixLogError(prefix string, err error) error {
	if de, ok := err.(*DomainError); ok {
		return newError(de.Code(), prefix+de.Error(), de.Retryable(), de.Data(), de.Unwrap())
	}
	return NewInternalError(fmt.Errorf("%s%w", prefix, err))
}
