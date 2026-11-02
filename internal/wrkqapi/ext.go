//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/lherron/wrkq/internal/store"
)

func extInvalid(message string) error {
	return newError("WRKQ_EXT_INVALID", "ext_invalid: "+message, false, nil, nil)
}
func validExtKey(key string) bool {
	if len(key) > 128 {
		return false
	}
	parts := strings.Split(key, ".")
	if len(parts) > 4 {
		return false
	}
	for _, p := range parts {
		if len(p) == 0 || len(p) > 64 || p[0] < 'a' || p[0] > 'z' {
			return false
		}
		for _, b := range []byte(p) {
			if (b < 'a' || b > 'z') && (b < 'A' || b > 'Z') && (b < '0' || b > '9') {
				return false
			}
		}
	}
	return true
}
func validateExtKey(ns, kind, id string) error {
	if !validExtKey(ns) || !validExtKey(kind) {
		return extInvalid("ns and kind require 1–4 alphanumeric dot segments starting lowercase, at most 64 bytes each and 128 total")
	}
	if len(id) == 0 || len(id) > 200 {
		return extInvalid("id must be 1–200 bytes")
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return extInvalid("id cannot contain control characters")
		}
	}
	return nil
}
func (a *API) extTarget(ctx context.Context, s ExtSubject) (store.ExtTarget, error) {
	target := store.ExtTarget{}
	count := 0
	for _, entry := range []struct{ typ, id, prefix string }{{"task", s.Task, "T-"}, {"container", s.Container, "P-"}, {"room", s.Room, "R-"}} {
		if entry.id == "" {
			continue
		}
		count++
		target.Type, target.ID = entry.typ, entry.id
		if !strings.HasPrefix(entry.id, entry.prefix) {
			return target, extInvalid("subject requires a public T-, P-, or R- id")
		}
	}
	if count != 1 {
		return target, extInvalid("subject must name exactly one task, container or room")
	}
	err := a.db.QueryRowContext(ctx, "SELECT uuid FROM "+target.Type+"s WHERE id=?", target.ID).Scan(&target.UUID)
	if errors.Is(err, sql.ErrNoRows) {
		return target, NewNotFoundError(target.ID, target.Type)
	}
	if err != nil {
		return target, NewInternalError(err)
	}
	return target, nil
}
func extDTO(r *store.ExtRecord) WrkqExt {
	body := map[string]any{}
	decoder := json.NewDecoder(strings.NewReader(r.Body))
	decoder.UseNumber()
	_ = decoder.Decode(&body)
	return WrkqExt{Subject: r.Subject.ID, NS: r.NS, Kind: r.Kind, ID: r.ID, Body: body, ETag: r.ETag, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, UpdatedByPrincipalRef: r.UpdatedByPrincipalRef, UpdatedByScopeRef: r.UpdatedByScopeRef}
}
func mapExtStoreError(err error) error {
	if err == nil {
		return nil
	}
	var e *store.ExtError
	if errors.As(err, &e) {
		switch e.Code {
		case "conflict":
			return NewConflictError(e.Message, nil)
		case "ext_quota":
			return newError("WRKQ_EXT_QUOTA", e.Message, false, nil, err)
		case "meta_key_migrated":
			return newError("WRKQ_META_KEY_MIGRATED", e.Message, false, nil, err)
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return NewNotFoundError("", "extension")
	}
	return NewInternalError(err)
}
func (a *API) ExtPut(ctx context.Context, p ExtPutParams) (*ExtPutResult, error) {
	attr, err := a.attributionFor(ctx, p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	if err := validateExtKey(p.NS, p.Kind, p.ID); err != nil {
		return nil, err
	}
	if p.IfMatch != nil && *p.IfMatch < 0 {
		return nil, extInvalid("ifMatch cannot be negative")
	}
	body, err := json.Marshal(p.Body)
	if err != nil || p.Body == nil || len(body) > 65536 || len(body) == 0 || body[0] != '{' {
		return nil, extInvalid("body must be a JSON object at most 65536 bytes")
	}
	target, err := a.extTarget(ctx, p.Subject)
	if err != nil {
		return nil, err
	}
	record, created, err := a.store.ExtPut(attr, target, p.NS, p.Kind, p.ID, string(body), p.IfMatch)
	if err != nil {
		return nil, mapExtStoreError(err)
	}
	return &ExtPutResult{Ext: extDTO(record), Created: created}, nil
}
func (a *API) ExtShow(ctx context.Context, p ExtShowParams) (*ExtShowResult, error) {
	if err := validateExtKey(p.NS, p.Kind, p.ID); err != nil {
		return nil, err
	}
	target, err := a.extTarget(ctx, p.Subject)
	if err != nil {
		return nil, err
	}
	record, err := a.store.ExtShow(target, p.NS, p.Kind, p.ID)
	if err != nil {
		return nil, mapExtStoreError(err)
	}
	return &ExtShowResult{Ext: extDTO(record)}, nil
}
func (a *API) ExtDelete(ctx context.Context, p ExtDeleteParams) (*ExtDeleteResult, error) {
	attr, err := a.attributionFor(ctx, p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	if err := validateExtKey(p.NS, p.Kind, p.ID); err != nil {
		return nil, err
	}
	if p.IfMatch != nil && *p.IfMatch < 0 {
		return nil, extInvalid("ifMatch cannot be negative")
	}
	target, err := a.extTarget(ctx, p.Subject)
	if err != nil {
		return nil, err
	}
	deleted, err := a.store.ExtDelete(attr, target, p.NS, p.Kind, p.ID, p.IfMatch)
	if err != nil {
		return nil, mapExtStoreError(err)
	}
	return &ExtDeleteResult{Deleted: deleted}, nil
}

// extCursor is a keyset cursor bound to the filters, so a changed scan cannot
// accidentally consume a cursor from another population.
type extCursor struct {
	UUID    string `json:"u"`
	Filters string `json:"f"`
}

func (a *API) ExtListView(ctx context.Context, p ExtListViewParams) (*WrkqExtListView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.Limit < 0 || p.Limit > 500 {
		return nil, extInvalid("limit must be between 1 and 500")
	}
	limit := p.Limit
	if limit == 0 {
		limit = 100
	}
	where := []string{"1=1"}
	args := []any{}
	if p.Subject != nil {
		target, err := a.extTarget(ctx, *p.Subject)
		if err != nil {
			return nil, err
		}
		where = append(where, "e."+target.Type+"_uuid=?")
		args = append(args, target.UUID)
	}
	if p.NS != "" {
		where = append(where, "e.ns=?")
		args = append(args, p.NS)
	}
	if p.Kind != "" {
		where = append(where, "e.kind=?")
		args = append(args, p.Kind)
	}
	if len(p.SubjectStates) > 0 {
		where = append(where, "t.state IN ("+strings.TrimSuffix(strings.Repeat("?,", len(p.SubjectStates)), ",")+")")
		for _, state := range p.SubjectStates {
			args = append(args, state)
		}
	}
	filterParams := p
	filterParams.Cursor = ""
	filterParams.Limit = 0
	filterBytes, _ := json.Marshal(filterParams)
	filters := string(filterBytes)
	if p.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(p.Cursor)
		var cursor extCursor
		if err != nil || json.Unmarshal(raw, &cursor) != nil || cursor.UUID == "" || cursor.Filters != filters {
			return nil, extInvalid("invalid cursor for extension filters")
		}
		where = append(where, "e.uuid>?")
		args = append(args, cursor.UUID)
	}
	args = append(args, limit+1)
	query := `SELECT e.uuid,CASE WHEN e.task_uuid IS NOT NULL THEN 'task' WHEN e.container_uuid IS NOT NULL THEN 'container' ELSE 'room' END,coalesce(t.id,c.id,r.id),coalesce(t.state,c.campaign_state,r.state,''),e.ns,e.kind,e.id,e.body,e.etag,e.created_at,e.updated_at,coalesce(e.updated_by_principal_ref,''),coalesce(e.updated_by_scope_ref,'') FROM ext e LEFT JOIN tasks t ON t.uuid=e.task_uuid LEFT JOIN containers c ON c.uuid=e.container_uuid LEFT JOIN rooms r ON r.uuid=e.room_uuid WHERE ` + strings.Join(where, " AND ") + ` ORDER BY e.uuid LIMIT ?`
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	result := &WrkqExtListView{Items: []WrkqExtListItem{}}
	lastUUID := ""
	for rows.Next() {
		var uid, state string
		record := store.ExtRecord{}
		if err := rows.Scan(&uid, &record.Subject.Type, &record.Subject.ID, &state, &record.NS, &record.Kind, &record.ID, &record.Body, &record.ETag, &record.CreatedAt, &record.UpdatedAt, &record.UpdatedByPrincipalRef, &record.UpdatedByScopeRef); err != nil {
			return nil, NewInternalError(err)
		}
		if len(result.Items) == limit {
			encoded, _ := json.Marshal(extCursor{UUID: lastUUID, Filters: filters})
			result.NextCursor = base64.RawURLEncoding.EncodeToString(encoded)
			break
		}
		result.Items = append(result.Items, WrkqExtListItem{WrkqExt: extDTO(&record), Subject: ExtSubjectRef{Type: record.Subject.Type, ID: record.Subject.ID, State: state}})
		lastUUID = uid
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(fmt.Errorf("extension list: %w", err))
	}
	return result, nil
}
