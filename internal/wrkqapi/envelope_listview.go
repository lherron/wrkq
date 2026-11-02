//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/store"
)

// EnvelopeListView reads stored envelope state without sweeping expiry or
// admitting an observer. Replay uses IDs to read terminal transitions too.
func (a *API) EnvelopeListView(ctx context.Context, p EnvelopeListViewParams) (*WrkqEnvelopeListView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit := 500
	if p.Limit != nil {
		limit = *p.Limit
	}
	if limit < 1 || limit > 500 {
		return nil, NewValidationError("limit must be between 1 and 500", map[string]any{"field": "limit"})
	}
	if len(p.IDs) > 500 {
		return nil, NewValidationError("ids must contain at most 500 selectors", map[string]any{"field": "ids"})
	}
	states := p.States
	if states == nil && p.IDs == nil {
		states = []string{"pending", "presented", "deferred"}
	}
	selected := make([]domain.EnvelopeState, 0, len(states))
	for _, state := range states {
		if err := domain.ValidateEnvelopeState(domain.EnvelopeState(state)); err != nil {
			return nil, NewValidationError(err.Error(), map[string]any{"field": "states"})
		}
		selected = append(selected, domain.EnvelopeState(state))
	}
	var after int64
	if p.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(p.Cursor)
		if err == nil && strings.HasPrefix(string(raw), "envelope.listView:") {
			after, err = strconv.ParseInt(strings.TrimPrefix(string(raw), "envelope.listView:"), 10, 64)
		} else {
			return nil, NewValidationError("invalid cursor", map[string]any{"field": "cursor"})
		}
		if err != nil || after < 1 {
			return nil, NewValidationError("invalid cursor", map[string]any{"field": "cursor"})
		}
	}
	result := &WrkqEnvelopeListView{Items: []WrkqEnvelope{}}
	if states != nil && len(states) == 0 {
		return result, nil
	}
	rows, err := a.store.Rooms.ListEnvelopes(ctx, store.EnvelopeListParams{IDs: p.IDs, States: selected, AfterMessageSeq: &after, Limit: limit + 1})
	if err != nil {
		return nil, mapRoomStoreError(err, "")
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	if len(rows) == 0 {
		return result, nil
	}
	// envelopeDTO uses only room kind and key. Fetch those together for every
	// room on the page instead of hydrating a full room separately per envelope.
	roomIDs := []any{}
	seen := map[string]bool{}
	for _, row := range rows {
		if !seen[row.RoomUUID] {
			seen[row.RoomUUID] = true
			roomIDs = append(roomIDs, row.RoomUUID)
		}
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(roomIDs)), ",")
	roomRows, err := a.db.QueryContext(ctx, `SELECT r.uuid,r.kind,
 CASE WHEN r.kind='task' THEN t.id WHEN r.kind IN ('campaign','project') THEN COALESCE(v.path,c.slug) ELSE COALESCE(r.id,r.uuid) END
 FROM rooms r LEFT JOIN tasks t ON t.uuid=r.task_uuid
 LEFT JOIN containers c ON c.uuid=r.container_uuid LEFT JOIN v_container_paths v ON v.uuid=c.uuid
 WHERE r.uuid IN (`+marks+`)`, roomIDs...)
	if err != nil {
		return nil, NewInternalError(err)
	}
	rooms := roomStates{}
	for roomRows.Next() {
		var uuid, key string
		var kind domain.RoomKind
		if err = roomRows.Scan(&uuid, &kind, &key); err != nil {
			_ = roomRows.Close()
			return nil, NewInternalError(err)
		}
		rooms[uuid] = &roomState{row: &domain.Room{UUID: uuid, Kind: kind}, key: key}
	}
	err = roomRows.Err()
	_ = roomRows.Close()
	if err != nil {
		return nil, NewInternalError(err)
	}
	for i := range rows {
		room := rooms[rows[i].RoomUUID]
		if room == nil {
			return nil, NewNotFoundError(rows[i].RoomUUID, "room")
		}
		item, err := a.envelopeDTO(ctx, &rows[i], room)
		if err != nil {
			return nil, err
		}
		result.Items = append(result.Items, *item)
	}
	if more {
		last := result.Items[len(result.Items)-1]
		result.NextCursor = base64.RawURLEncoding.EncodeToString([]byte("envelope.listView:" + strconv.FormatInt(last.MessageSeq, 10)))
	}
	return result, nil
}
