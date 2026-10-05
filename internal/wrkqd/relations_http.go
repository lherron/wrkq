package wrkqd

import (
	"fmt"
	"net/http"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/selectors"
)

type relationsListRequest struct {
	Task string `json:"task"`
}

func (s *daemonServer) handleRelationsList(w http.ResponseWriter, r *http.Request) {
	var req relationsListRequest
	if !s.decodePost(w, r, &req) {
		return
	}

	taskUUID, ok := s.resolveTask(w, req.Task)
	if !ok {
		return
	}

	relations, err := queryTaskRelations(s.db, taskUUID)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"relations": relations,
	})
}

type relationsCreateRequest struct {
	From string `json:"from"`
	Kind string `json:"kind"`
	To   string `json:"to"`
}

func (s *daemonServer) handleRelationsCreate(w http.ResponseWriter, r *http.Request) {
	var req relationsCreateRequest
	if !s.decodePost(w, r, &req) {
		return
	}

	if err := domain.ValidateTaskRelationKind(req.Kind); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	attr, err := s.resolveAttribution(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	fromUUID, _, err := selectors.ResolveTask(s.db, req.From)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	toUUID, _, err := selectors.ResolveTask(s.db, req.To)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if fromUUID == toUUID {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("task cannot have a relation to itself"))
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		INSERT INTO task_relations (
			from_task_uuid, to_task_uuid, kind,
			created_by_principal_ref, created_by_scope_ref
		)
		VALUES (?, ?, ?, ?, ?)
	`, fromUUID, toUUID, req.Kind, attr.PrincipalRef, scopeBind(attr)); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	payload := fmt.Sprintf(`{"from_task_uuid":"%s","to_task_uuid":"%s","kind":"%s"}`, fromUUID, toUUID, req.Kind)
	if err := events.NewWriter(s.db.DB).LogEvent(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "task",
		ResourceUUID: &fromUUID,
		EventType:    "task.relation.created",
		Payload:      &payload,
	}); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true,
	})
}

type relationsDeleteRequest struct {
	From string `json:"from"`
	Kind string `json:"kind"`
	To   string `json:"to"`
}

func (s *daemonServer) handleRelationsDelete(w http.ResponseWriter, r *http.Request) {
	var req relationsDeleteRequest
	if !s.decodePost(w, r, &req) {
		return
	}

	if err := domain.ValidateTaskRelationKind(req.Kind); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	attr, err := s.resolveAttribution(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	fromUUID, _, err := selectors.ResolveTask(s.db, req.From)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	toUUID, _, err := selectors.ResolveTask(s.db, req.To)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.Exec(`
		DELETE FROM task_relations
		WHERE from_task_uuid = ? AND to_task_uuid = ? AND kind = ?
	`, fromUUID, toUUID, req.Kind)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		s.writeError(w, http.StatusNotFound, fmt.Errorf("relation not found"))
		return
	}
	payload := fmt.Sprintf(`{"from_task_uuid":"%s","to_task_uuid":"%s","kind":"%s","deleted_by_principal_ref":"%s"}`,
		fromUUID, toUUID, req.Kind, attr.PrincipalRef)
	if err := events.NewWriter(s.db.DB).LogEvent(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "task",
		ResourceUUID: &fromUUID,
		EventType:    "task.relation.deleted",
		Payload:      &payload,
	}); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true,
	})
}

// queryTaskRelations returns a task's outgoing then incoming relations, each
// ordered by kind and counterpart task id.
func queryTaskRelations(database *db.DB, taskUUID string) ([]Relation, error) {
	outgoing, err := queryRelationsInDirection(database, taskUUID, "outgoing")
	if err != nil {
		return nil, err
	}
	incoming, err := queryRelationsInDirection(database, taskUUID, "incoming")
	if err != nil {
		return nil, err
	}
	return append(outgoing, incoming...), nil
}

func queryRelationsInDirection(database *db.DB, taskUUID, direction string) ([]Relation, error) {
	// An outgoing relation names its counterpart in to_task_uuid; an incoming
	// one in from_task_uuid.
	counterpart, self := "to_task_uuid", "from_task_uuid"
	if direction == "incoming" {
		counterpart, self = self, counterpart
	}
	rows, err := database.Query(fmt.Sprintf(`
		SELECT r.kind, r.created_at,
		       t.id AS task_id, t.uuid AS task_uuid, t.slug, t.title,
		       COALESCE(r.created_by_principal_ref, '') AS created_by_id
		FROM task_relations r
		JOIN tasks t ON r.%s = t.uuid
		WHERE r.%s = ?
		ORDER BY r.kind, t.id
	`, counterpart, self), taskUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query %s relations: %w", direction, err)
	}
	defer func() { _ = rows.Close() }()

	var relations []Relation
	for rows.Next() {
		rel := Relation{Direction: direction}
		if err := rows.Scan(&rel.Kind, &rel.CreatedAt, &rel.TaskID, &rel.TaskUUID, &rel.TaskSlug, &rel.TaskTitle, &rel.CreatedByID); err != nil {
			return nil, fmt.Errorf("failed to scan relation: %w", err)
		}
		relations = append(relations, rel)
	}
	return relations, nil
}
