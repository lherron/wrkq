package wrkqd

import (
	"database/sql"
	"fmt"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/db"
)

func loadTaskDetail(database *db.DB, taskUUID string, includeComments bool, includeRelations bool) (*Task, error) {
	var id, slug, title, state, description, specification, kind string
	var priority int
	var startAt, dueAt, labels, completedAt, archivedAt, deletedAt *string
	var parentTaskUUID, assigneePrincipalRef *string
	var createdAt, updatedAt string
	var etag int64
	var projectUUID string
	var createdByPrincipalRef, updatedByPrincipalRef, createdByScopeRef, updatedByScopeRef sql.NullString

	err := database.QueryRow(`
		SELECT id, slug, title, project_uuid, state, priority,
		       kind, parent_task_uuid, assignee_principal_ref,
		       start_at, due_at, labels, description, specification, etag,
		       created_at, updated_at, completed_at, archived_at, deleted_at,
		       created_by_principal_ref, updated_by_principal_ref,
		       created_by_scope_ref, updated_by_scope_ref
		FROM tasks WHERE uuid = ?
	`, taskUUID).Scan(
		&id, &slug, &title, &projectUUID, &state, &priority,
		&kind, &parentTaskUUID, &assigneePrincipalRef,
		&startAt, &dueAt, &labels, &description, &specification, &etag,
		&createdAt, &updatedAt, &completedAt, &archivedAt, &deletedAt,
		&createdByPrincipalRef, &updatedByPrincipalRef,
		&createdByScopeRef, &updatedByScopeRef,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	var createdBySlug, updatedBySlug string
	if createdByPrincipalRef.Valid && createdByPrincipalRef.String != "" {
		createdBySlug = attribution.PrincipalHandle(createdByPrincipalRef.String)
	}
	if updatedByPrincipalRef.Valid && updatedByPrincipalRef.String != "" {
		updatedBySlug = attribution.PrincipalHandle(updatedByPrincipalRef.String)
	}

	var projectID string
	_ = database.QueryRow("SELECT id FROM containers WHERE uuid = ?", projectUUID).Scan(&projectID)

	var parentTaskID *string
	if parentTaskUUID != nil {
		var ptID string
		if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", *parentTaskUUID).Scan(&ptID); err == nil {
			parentTaskID = &ptID
		}
	}

	var assigneeSlug *string
	if assigneePrincipalRef != nil && *assigneePrincipalRef != "" {
		display := attribution.PrincipalHandle(*assigneePrincipalRef)
		assigneeSlug = &display
	}

	task := &Task{
		ID:                    id,
		UUID:                  taskUUID,
		ArtifactDir:           taskArtifactDir(id),
		ProjectID:             projectID,
		ProjectUUID:           projectUUID,
		Slug:                  slug,
		Title:                 title,
		State:                 state,
		Priority:              priority,
		Kind:                  kind,
		ParentTaskID:          parentTaskID,
		ParentTaskUUID:        parentTaskUUID,
		AssigneeSlug:          assigneeSlug,
		AssigneePrincipalRef:  assigneePrincipalRef,
		StartAt:               startAt,
		DueAt:                 dueAt,
		Labels:                labels,
		Description:           description,
		Specification:         specification,
		Etag:                  etag,
		CreatedAt:             createdAt,
		UpdatedAt:             updatedAt,
		CompletedAt:           completedAt,
		ArchivedAt:            archivedAt,
		DeletedAt:             deletedAt,
		CreatedBy:             createdBySlug,
		UpdatedBy:             updatedBySlug,
		CreatedByPrincipalRef: valueOrEmpty(createdByPrincipalRef),
		UpdatedByPrincipalRef: valueOrEmpty(updatedByPrincipalRef),
		CreatedByScopeRef:     valueOrEmpty(createdByScopeRef),
		UpdatedByScopeRef:     valueOrEmpty(updatedByScopeRef),
	}

	if includeComments {
		rows, err := database.Query(`
			SELECT c.id, c.created_at, c.body,
			       c.created_by_principal_ref, c.created_by_scope_ref
			FROM comments c
			WHERE c.task_uuid = ? AND c.deleted_at IS NULL
			ORDER BY c.created_at ASC
		`, taskUUID)
		if err != nil {
			return nil, fmt.Errorf("failed to query comments: %w", err)
		}

		var comments []Comment
		for rows.Next() {
			var comment Comment
			var principalRef, scopeRef sql.NullString
			if err := rows.Scan(&comment.ID, &comment.CreatedAt, &comment.Body, &principalRef, &scopeRef); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("failed to scan comment: %w", err)
			}
			comment.PrincipalRef = valueOrEmpty(principalRef)
			comment.ScopeRef = valueOrEmpty(scopeRef)
			comment.Author = attribution.PrincipalHandle(comment.PrincipalRef)
			comments = append(comments, comment)
		}
		_ = rows.Close()

		if len(comments) > 0 {
			task.Comments = comments
		}
	}

	if includeRelations {
		relations, err := queryTaskRelations(database, taskUUID)
		if err != nil {
			return nil, err
		}
		if len(relations) > 0 {
			task.Relations = relations
		}
	}

	return task, nil
}
