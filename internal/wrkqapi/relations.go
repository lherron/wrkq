package wrkqapi

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/selectors"
)

const nsRelationAdd = "wrkq.relation.add"

// RelationAdd creates a relation between two tasks. Relations are identified by
// the composite key (fromTask, kind, toTask). Idempotency is honored when an
// idempotencyKey is supplied.
func (a *API) RelationAdd(ctx context.Context, p RelationAddParams) (*WrkqRelation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if verr := domain.ValidateTaskRelationKind(p.Kind); verr != nil {
		return nil, NewValidationError(verr.Error(), map[string]any{"field": "kind"})
	}

	fromUUID, fromID, ferr := a.resolveTaskRef(p.FromTask)
	if ferr != nil {
		return nil, ferr
	}
	toUUID, toID, terr := a.resolveTaskRef(p.ToTask)
	if terr != nil {
		return nil, terr
	}
	if fromUUID == toUUID {
		return nil, NewValidationError("task cannot have a relation to itself", map[string]any{"field": "toTask"})
	}

	// Idempotency replay BEFORE mutation.
	var requestHash string
	if p.IdempotencyKey != "" {
		hp := p
		hp.IdempotencyKey = ""
		requestHash = canonicalRequestHash(hp)
		if raw, ok, rerr := a.idempotentReplay(nsRelationAdd, p.IdempotencyKey, requestHash); rerr != nil {
			return nil, rerr
		} else if ok {
			var dto WrkqRelation
			if uerr := json.Unmarshal(raw, &dto); uerr != nil {
				return nil, NewInternalError(uerr)
			}
			return &dto, nil
		}
	}

	attr := a.attributionFor(p.Actor)

	tx, err := a.db.Begin()
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, ierr := tx.Exec(`
		INSERT INTO task_relations (
			from_task_uuid, to_task_uuid, kind,
			created_by_actor_uuid, created_by_principal_ref, created_by_scope_ref
		)
		VALUES (?, ?, ?, ?, ?, ?)
	`, fromUUID, toUUID, p.Kind, legacyActorBind(attr), attr.PrincipalRef, scopeBind(attr)); ierr != nil {
		return nil, mapStoreError(ierr, p.FromTask)
	}

	payload := `{"from_task_uuid":"` + fromUUID + `","to_task_uuid":"` + toUUID + `","kind":"` + p.Kind + `"}`
	if eerr := events.NewWriter(a.db.DB).LogEvent(tx, &domain.Event{
		ActorUUID:    attr.LegacyActorUUID,
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "task",
		ResourceUUID: &fromUUID,
		EventType:    "task.relation.created",
		Payload:      &payload,
	}); eerr != nil {
		return nil, NewInternalError(eerr)
	}

	if cerr := tx.Commit(); cerr != nil {
		return nil, NewInternalError(cerr)
	}

	dto := &WrkqRelation{
		FromTask:  fromID,
		ToTask:    toID,
		Kind:      p.Kind,
		Direction: "outgoing",
	}
	if p.IdempotencyKey != "" {
		if serr := a.idempotentStore(nsRelationAdd, p.IdempotencyKey, requestHash, dto); serr != nil {
			return nil, serr
		}
	}
	return dto, nil
}

// RelationList returns all relations involving a task (outgoing + incoming),
// each tagged with its direction. Mirrors internal/cli/relation.go.
func (a *API) RelationList(ctx context.Context, p RelationListParams) (*WrkqRelationListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	taskUUID, taskID, err := a.resolveTaskRef(p.Task)
	if err != nil {
		return nil, err
	}

	items := []WrkqRelation{}

	// Outgoing: this task → other tasks.
	outRows, oerr := a.db.Query(`
		SELECT r.kind, r.created_at, t.id
		FROM task_relations r
		JOIN tasks t ON r.to_task_uuid = t.uuid
		WHERE r.from_task_uuid = ?
		ORDER BY r.kind, t.id
	`, taskUUID)
	if oerr != nil {
		return nil, NewInternalError(oerr)
	}
	for outRows.Next() {
		var kind, createdAt, otherID string
		if scanErr := outRows.Scan(&kind, &createdAt, &otherID); scanErr != nil {
			_ = outRows.Close()
			return nil, NewInternalError(scanErr)
		}
		items = append(items, WrkqRelation{
			FromTask:  taskID,
			ToTask:    otherID,
			Kind:      kind,
			Direction: "outgoing",
			CreatedAt: toRFC3339(createdAt),
		})
	}
	_ = outRows.Close()
	if rerr := outRows.Err(); rerr != nil {
		return nil, NewInternalError(rerr)
	}

	// Incoming: other tasks → this task.
	inRows, ierr := a.db.Query(`
		SELECT r.kind, r.created_at, t.id
		FROM task_relations r
		JOIN tasks t ON r.from_task_uuid = t.uuid
		WHERE r.to_task_uuid = ?
		ORDER BY r.kind, t.id
	`, taskUUID)
	if ierr != nil {
		return nil, NewInternalError(ierr)
	}
	for inRows.Next() {
		var kind, createdAt, otherID string
		if scanErr := inRows.Scan(&kind, &createdAt, &otherID); scanErr != nil {
			_ = inRows.Close()
			return nil, NewInternalError(scanErr)
		}
		items = append(items, WrkqRelation{
			FromTask:  otherID,
			ToTask:    taskID,
			Kind:      kind,
			Direction: "incoming",
			CreatedAt: toRFC3339(createdAt),
		})
	}
	_ = inRows.Close()
	if rerr := inRows.Err(); rerr != nil {
		return nil, NewInternalError(rerr)
	}

	return &WrkqRelationListResult{Items: items}, nil
}

// RelationRemove deletes a relation by its composite identity. A 0-row delete →
// WRKQ_NOT_FOUND.
func (a *API) RelationRemove(ctx context.Context, p RelationRemoveParams) (*WrkqRelation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if verr := domain.ValidateTaskRelationKind(p.Kind); verr != nil {
		return nil, NewValidationError(verr.Error(), map[string]any{"field": "kind"})
	}
	fromUUID, fromID, ferr := a.resolveTaskRef(p.FromTask)
	if ferr != nil {
		return nil, ferr
	}
	toUUID, toID, terr := a.resolveTaskRef(p.ToTask)
	if terr != nil {
		return nil, terr
	}

	attr := a.attributionFor(p.Actor)

	tx, err := a.db.Begin()
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	res, derr := tx.Exec(
		"DELETE FROM task_relations WHERE from_task_uuid = ? AND to_task_uuid = ? AND kind = ?",
		fromUUID, toUUID, p.Kind,
	)
	if derr != nil {
		return nil, NewInternalError(derr)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, NewNotFoundError(fromID+" "+p.Kind+" "+toID, "relation")
	}

	payload := `{"from_task_uuid":"` + fromUUID + `","to_task_uuid":"` + toUUID + `","kind":"` + p.Kind + `"}`
	if eerr := events.NewWriter(a.db.DB).LogEvent(tx, &domain.Event{
		ActorUUID:    attr.LegacyActorUUID,
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "task",
		ResourceUUID: &fromUUID,
		EventType:    "task.relation.deleted",
		Payload:      &payload,
	}); eerr != nil {
		return nil, NewInternalError(eerr)
	}

	if cerr := tx.Commit(); cerr != nil {
		return nil, NewInternalError(cerr)
	}

	return &WrkqRelation{
		FromTask:  fromID,
		ToTask:    toID,
		Kind:      p.Kind,
		Direction: "outgoing",
	}, nil
}

// resolveTaskRef resolves a task selector to (uuid, friendlyID), returning
// WRKQ_NOT_FOUND when the task does not exist or the selector is empty.
func (a *API) resolveTaskRef(selector string) (uuid, id string, err error) {
	if strings.TrimSpace(selector) == "" {
		return "", "", NewValidationError("task selector is required", map[string]any{"field": "task"})
	}
	u, friendly, rerr := selectors.ResolveTask(a.db, selector)
	if rerr != nil {
		return "", "", NewNotFoundError(selector, "task")
	}
	return u, friendly, nil
}
