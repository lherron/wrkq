//go:build wrkq_local

package wrkqapi

import (
	"database/sql"
	"encoding/json"
	"strings"
)

func coversKindError() error {
	return newError("WRKQ_COVERS_REQUIRES_WORKSTREAM", "covers_requires_workstream: covers requires kind workstream", false, nil, nil)
}

func (a *API) validateCovers(kind string, covers *TaskCovers) (*string, error) {
	return validateCoversOn(a.db, kind, covers)
}
func validateCoversOn(q taskQueryer, kind string, covers *TaskCovers) (*string, error) {
	if covers == nil {
		return nil, nil
	}
	if kind != "workstream" {
		return nil, coversKindError()
	}
	if covers.Container != "" {
		var found string
		if !strings.HasPrefix(covers.Container, "P-") {
			return nil, NewNotFoundError(covers.Container, "container")
		}
		if err := q.QueryRow("SELECT id FROM containers WHERE id=?", covers.Container).Scan(&found); err != nil {
			if err == sql.ErrNoRows {
				return nil, NewNotFoundError(covers.Container, "container")
			}
			return nil, NewInternalError(err)
		}
	}
	for _, task := range covers.Tasks {
		var found string
		if !strings.HasPrefix(task, "T-") {
			return nil, NewNotFoundError(task, "task")
		}
		if err := q.QueryRow("SELECT id FROM tasks WHERE id=?", task).Scan(&found); err != nil {
			if err == sql.ErrNoRows {
				return nil, NewNotFoundError(task, "task")
			}
			return nil, NewInternalError(err)
		}
	}
	raw, err := json.Marshal(covers)
	if err != nil {
		return nil, NewValidationError(err.Error(), nil)
	}
	value := string(raw)
	return &value, nil
}

func validateCoversPatchOn(q taskQueryer, uuid string, fields map[string]any, raw json.RawMessage) error {
	if len(raw) == 0 {
		if _, ok := fields["kind"]; !ok {
			return nil
		}
	}
	var kind string
	var old sql.NullString
	if err := q.QueryRow("SELECT kind,covers FROM tasks WHERE uuid=?", uuid).Scan(&kind, &old); err != nil {
		return NewInternalError(err)
	}
	if next, ok := fields["kind"].(string); ok {
		kind = next
	}
	if len(raw) > 0 {
		var covers *TaskCovers
		if err := json.Unmarshal(raw, &covers); err != nil {
			return NewValidationError("invalid covers: "+err.Error(), map[string]any{"field": "covers"})
		}
		value, err := validateCoversOn(q, kind, covers)
		if err != nil {
			return err
		}
		if value == nil {
			fields["covers"] = nil
		} else {
			fields["covers"] = *value
		}
	} else if old.Valid && kind != "workstream" {
		return coversKindError()
	}
	return nil
}
