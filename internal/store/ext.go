package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
)

// ExtTarget is an already resolved subject; Type is restricted to these three tables.
type ExtTarget struct{ Type, UUID, ID string }
type ExtRecord struct {
	Subject                                                        ExtTarget
	NS, Kind, ID, Body                                             string
	ETag                                                           int64
	CreatedAt, UpdatedAt, UpdatedByPrincipalRef, UpdatedByScopeRef string
}
type ExtError struct{ Code, Message string }

func (e *ExtError) Error() string           { return e.Message }
func extFailure(code, message string) error { return &ExtError{Code: code, Message: message} }

const extColumns = `e.ns,e.kind,e.id,e.body,e.etag,e.created_at,e.updated_at,coalesce(e.updated_by_principal_ref,''),coalesce(e.updated_by_scope_ref,'')`

func scanExt(row interface{ Scan(...any) error }, target ExtTarget) (*ExtRecord, error) {
	r := &ExtRecord{Subject: target}
	err := row.Scan(&r.NS, &r.Kind, &r.ID, &r.Body, &r.ETag, &r.CreatedAt, &r.UpdatedAt, &r.UpdatedByPrincipalRef, &r.UpdatedByScopeRef)
	return r, err
}
func extParentColumn(target ExtTarget) (string, error) {
	switch target.Type {
	case "task", "container", "room":
		return target.Type + "_uuid", nil
	}
	return "", fmt.Errorf("invalid extension subject type")
}
func (s *Store) ExtShow(target ExtTarget, ns, kind, id string) (*ExtRecord, error) {
	column, err := extParentColumn(target)
	if err != nil {
		return nil, err
	}
	return scanExt(s.db.QueryRow("SELECT "+extColumns+" FROM ext e WHERE "+column+"=? AND ns=? AND kind=? AND id=?", target.UUID, ns, kind, id), target)
}

// ExtPut serializes quota, CAS, row write and the body-free ledger event.
func (s *Store) ExtPut(attr attribution.Attribution, target ExtTarget, ns, kind, id, body string, ifMatch *int64) (*ExtRecord, bool, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, false, err
	}
	column, err := extParentColumn(target)
	if err != nil {
		return nil, false, err
	}
	var record *ExtRecord
	created := false
	err = s.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		current, err := scanExt(tx.QueryRow("SELECT "+extColumns+" FROM ext e WHERE "+column+"=? AND ns=? AND kind=? AND id=?", target.UUID, ns, kind, id), target)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		created = errors.Is(err, sql.ErrNoRows)
		if ifMatch != nil && ((*ifMatch == 0 && !created) || (*ifMatch > 0 && (created || current.ETag != *ifMatch))) {
			return extFailure("conflict", "extension etag conflict")
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		if created {
			var count int
			if err := tx.QueryRow("SELECT count(*) FROM ext WHERE "+column+"=?", target.UUID).Scan(&count); err != nil {
				return err
			}
			if count >= 256 {
				return extFailure("ext_quota", "ext_quota: subject has 256 extension rows")
			}
			_, err = tx.Exec("INSERT INTO ext(uuid,"+column+",ns,kind,id,body,etag,created_at,updated_at,updated_by_principal_ref,updated_by_scope_ref) VALUES(?,?,?,?,?,?,1,?,?,?,?)", uuid.NewString(), target.UUID, ns, kind, id, body, now, now, attr.PrincipalRef, scopeSQL(attr))
		} else {
			_, err = tx.Exec("UPDATE ext SET body=?,etag=etag+1,updated_at=?,updated_by_principal_ref=?,updated_by_scope_ref=? WHERE "+column+"=? AND ns=? AND kind=? AND id=?", body, now, attr.PrincipalRef, scopeSQL(attr), target.UUID, ns, kind, id)
		}
		if err != nil {
			return err
		}
		record, err = scanExt(tx.QueryRow("SELECT "+extColumns+" FROM ext e WHERE "+column+"=? AND ns=? AND kind=? AND id=?", target.UUID, ns, kind, id), target)
		if err != nil {
			return err
		}
		return logExtEvent(tx, ew, attr, target, "put", ns, kind, id, record.ETag)
	})
	return record, created, err
}
func (s *Store) ExtDelete(attr attribution.Attribution, target ExtTarget, ns, kind, id string, ifMatch *int64) (bool, error) {
	if err := requireAttribution(attr); err != nil {
		return false, err
	}
	column, err := extParentColumn(target)
	if err != nil {
		return false, err
	}
	deleted := false
	err = s.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		record, err := scanExt(tx.QueryRow("SELECT "+extColumns+" FROM ext e WHERE "+column+"=? AND ns=? AND kind=? AND id=?", target.UUID, ns, kind, id), target)
		if errors.Is(err, sql.ErrNoRows) {
			if ifMatch != nil {
				return extFailure("conflict", "extension etag conflict")
			}
			return nil
		}
		if err != nil {
			return err
		}
		if ifMatch != nil && record.ETag != *ifMatch {
			return extFailure("conflict", "extension etag conflict")
		}
		if _, err := tx.Exec("DELETE FROM ext WHERE "+column+"=? AND ns=? AND kind=? AND id=?", target.UUID, ns, kind, id); err != nil {
			return err
		}
		deleted = true
		return logExtEvent(tx, ew, attr, target, "deleted", ns, kind, id, record.ETag)
	})
	return deleted, err
}
func logExtEvent(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, target ExtTarget, verb, ns, kind, id string, etag int64) error {
	payload, err := json.Marshal(map[string]any{"ns": ns, "kind": kind, "id": id, "etag": etag})
	if err != nil {
		return err
	}
	text := string(payload)
	return ew.LogEvent(tx, &domain.Event{PrincipalRef: attr.PrincipalRef, ScopeRef: attr.ScopeRef, ResourceType: target.Type, ResourceUUID: &target.UUID, EventType: target.Type + ".ext." + verb, ETag: &etag, Payload: &text})
}

// CheckMigratedMeta runs under the same writer transaction as task.update.
// It compares only present incoming paths: removal remains admitted.
func CheckMigratedMeta(tx *sql.Tx, taskUUID string, incoming any) error {
	var raw sql.NullString
	if err := tx.QueryRow("SELECT meta FROM tasks WHERE uuid=?", taskUUID).Scan(&raw); err != nil {
		return err
	}
	before := map[string]any{}
	if raw.Valid {
		if err := json.Unmarshal([]byte(raw.String), &before); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(incoming)
	if err != nil {
		return err
	}
	after := map[string]any{}
	if string(encoded) != "null" {
		if err := json.Unmarshal(encoded, &after); err != nil {
			return err
		}
	}
	type moved struct{ path, ns, kind, id string }
	paths := []moved{{"acp.request", "acp", "request", "main"}, {"scribe", "scribe", "request", "main"}, {"wrkf.taskStateMirror", "agentloop", "taskStateMirror", "main"}}
	for _, key := range strings.Fields("technique source risk package file apiImpact deltaId target targetSlug gate sourceWorkItem reportPath path") {
		paths = append(paths, moved{key, "archagent", "refactor", ""})
	}
	for key := range after {
		if strings.HasPrefix(key, "hcs_") {
			paths = append(paths, moved{key, "hcs", "launch", "main"})
		}
	}
	for _, p := range paths {
		next, present := metaPath(after, p.path)
		if !present {
			continue
		}
		previous, previousPresent := metaPath(before, p.path)
		a, _ := json.Marshal(previous)
		b, _ := json.Marshal(next)
		if previousPresent && string(a) == string(b) {
			continue
		}
		query := "SELECT EXISTS(SELECT 1 FROM ext WHERE task_uuid=? AND ns=? AND kind=?"
		args := []any{taskUUID, p.ns, p.kind}
		if p.id != "" {
			query += " AND id=?"
			args = append(args, p.id)
		}
		query += ")"
		var exists bool
		if err := tx.QueryRow(query, args...).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return extFailure("meta_key_migrated", fmt.Sprintf("meta_key_migrated: meta.%s moved; use wrkq ext put <task> %s %s %s --body <json>", p.path, p.ns, p.kind, defaultExtID(p.id)))
		}
	}
	return nil
}
func defaultExtID(id string) string {
	if id == "" {
		return "<id>"
	}
	return id
}
func metaPath(meta map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	for i, p := range parts {
		v, ok := meta[p]
		if !ok {
			return nil, false
		}
		if i == len(parts)-1 {
			return v, true
		}
		meta, ok = v.(map[string]any)
		if !ok {
			return nil, false
		}
	}
	return nil, false
}
