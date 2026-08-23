//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/lherron/wrkq/internal/store"
)

// TaskCatView assembles the legacy cat projection for one task. Selector→UUID
// resolution happens first (outside the snapshot); the single read transaction
// then covers the RESOLVED task UUID's projection — scalars, comments, relations,
// and blockers are internally consistent for that UUID. The snapshot does not
// cover the selector-resolution step (a concurrent rename/move/delete between
// resolution and the tx is possible, as with any resolve-then-read). It mirrors
// internal/rpccli/cat.go's projection logic exactly.
func (a *API) TaskCatView(ctx context.Context, p TaskCatViewParams) (*WrkqTaskCatView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	taskUUID, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}
	includeComments := p.IncludeComments == nil || *p.IncludeComments

	tx, err := a.db.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		id, slug, title, state, description, specification, kind       string
		priority                                                       int
		startAt, dueAt, labels, meta, outcome, completedAt, archivedAt *string
		requestedBy, assignedProject, acknowledgedAt, resolution       *string
		parentTaskUUID, assigneePrincipalRef                           *string
		claimedBy, claimedScope, claimedNode, claimedAt                *string
		claimGeneration                                                int64
		createdAt, updatedAt                                           string
		etag                                                           int64
		projectUUID                                                    string
		createdByPrincipalRef, updatedByPrincipalRef                   sql.NullString
		createdByScopeRef                                              *string
	)
	err = tx.QueryRowContext(ctx, `
		SELECT id, slug, title, project_uuid, requested_by_project_id, assigned_project_id,
		       state, priority,
		       kind, parent_task_uuid, assignee_principal_ref,
		       claimed_by_principal_ref, claimed_scope_ref, claimed_node, claimed_at, claim_generation,
		       start_at, due_at, labels, meta, description, specification, outcome, etag,
		       created_at, updated_at, completed_at, archived_at,
		       acknowledged_at, resolution,
		       created_by_principal_ref, updated_by_principal_ref, created_by_scope_ref
		FROM tasks WHERE uuid = ?`, taskUUID).Scan(
		&id, &slug, &title, &projectUUID, &requestedBy, &assignedProject, &state, &priority,
		&kind, &parentTaskUUID, &assigneePrincipalRef,
		&claimedBy, &claimedScope, &claimedNode, &claimedAt, &claimGeneration,
		&startAt, &dueAt, &labels, &meta, &description, &specification, &outcome, &etag,
		&createdAt, &updatedAt, &completedAt, &archivedAt,
		&acknowledgedAt, &resolution,
		&createdByPrincipalRef, &updatedByPrincipalRef, &createdByScopeRef,
	)
	if err == sql.ErrNoRows {
		return nil, NewNotFoundError(p.Task, "task")
	}
	if err != nil {
		return nil, NewInternalError(err)
	}

	var projectID string
	_ = tx.QueryRowContext(ctx, "SELECT id FROM containers WHERE uuid = ?", projectUUID).Scan(&projectID)

	var taskPath string
	_ = tx.QueryRowContext(ctx, "SELECT path FROM v_task_paths WHERE uuid = ?", taskUUID).Scan(&taskPath)

	var parentTaskID *string
	if parentTaskUUID != nil {
		var ptID string
		if e := tx.QueryRowContext(ctx, "SELECT id FROM tasks WHERE uuid = ?", *parentTaskUUID).Scan(&ptID); e == nil {
			parentTaskID = &ptID
		}
	}

	var assigneeSlug *string
	if assigneePrincipalRef != nil {
		display := principalHandle(*assigneePrincipalRef)
		assigneeSlug = &display
	}
	var createdBy string
	if createdByPrincipalRef.Valid {
		createdBy = principalHandle(createdByPrincipalRef.String)
	}
	var updatedBy string
	if updatedByPrincipalRef.Valid {
		updatedBy = principalHandle(updatedByPrincipalRef.String)
	}

	metaValue := "{}"
	if meta != nil && *meta != "" && json.Valid([]byte(*meta)) {
		metaValue = *meta
	}

	view := &WrkqTaskCatView{
		ID:                    id,
		UUID:                  taskUUID,
		Path:                  taskPath,
		ArtifactDir:           taskArtifactDir(id),
		ProjectID:             projectID,
		ProjectUUID:           projectUUID,
		RequestedByProjectID:  requestedBy,
		AssignedProjectID:     assignedProject,
		Slug:                  slug,
		Title:                 title,
		State:                 state,
		Priority:              priority,
		Kind:                  kind,
		ParentTaskID:          parentTaskID,
		ParentTaskUUID:        parentTaskUUID,
		AssigneeSlug:          assigneeSlug,
		AssigneePrincipalRef:  assigneePrincipalRef,
		ClaimedBy:             claimedBy,
		ClaimedScope:          claimedScope,
		ClaimedNode:           claimedNode,
		ClaimedAt:             claimedAt,
		ClaimGeneration:       claimGeneration,
		StartAt:               startAt,
		DueAt:                 dueAt,
		Labels:                labels,
		Meta:                  json.RawMessage(metaValue),
		Description:           description,
		Specification:         specification,
		Outcome:               outcome,
		AcknowledgedAt:        acknowledgedAt,
		Resolution:            resolution,
		Etag:                  etag,
		CreatedAt:             createdAt,
		UpdatedAt:             updatedAt,
		CompletedAt:           completedAt,
		ArchivedAt:            archivedAt,
		CreatedBy:             createdBy,
		CreatedByPrincipalRef: nullStr(createdByPrincipalRef),
		CreatedByScopeRef:     createdByScopeRef,
		UpdatedBy:             updatedBy,
		UpdatedByPrincipalRef: nullStr(updatedByPrincipalRef),
	}

	causedBy, cerr := store.CausedByIDs(tx, taskUUID)
	if cerr != nil {
		return nil, NewInternalError(cerr)
	}
	view.CausedBy = causedBy

	if includeComments {
		comments, cerr := catViewComments(ctx, tx, taskUUID)
		if cerr != nil {
			return nil, cerr
		}
		if len(comments) > 0 {
			view.Comments = comments
		}
	}

	relations, rerr := catViewRelations(ctx, tx, taskUUID)
	if rerr != nil {
		return nil, rerr
	}
	if len(relations) > 0 {
		view.Relations = relations
	}

	blockers, berr := catViewBlockers(ctx, tx, taskUUID)
	if berr != nil {
		return nil, berr
	}
	if len(blockers) > 0 {
		view.BlockedBy = blockers
	}

	if err := tx.Commit(); err != nil {
		return nil, NewInternalError(err)
	}
	promises, err := a.attachedPromiseDTOs(ctx, taskUUID, "")
	if err != nil {
		return nil, err
	}
	view.Promises = promises
	return view, nil
}

func catViewComments(ctx context.Context, tx *sql.Tx, taskUUID string) ([]CatViewComment, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT c.id, c.created_at, c.body, c.created_by_principal_ref
		FROM comments c
		WHERE c.task_uuid = ? AND c.deleted_at IS NULL
		ORDER BY c.created_at ASC`, taskUUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	var comments []CatViewComment
	for rows.Next() {
		var comment CatViewComment
		var principalRef sql.NullString
		if err := rows.Scan(&comment.ID, &comment.CreatedAt, &comment.Body, &principalRef); err != nil {
			return nil, NewInternalError(err)
		}
		if principalRef.Valid {
			comment.PrincipalRef = principalRef.String
		}
		comments = append(comments, comment)
	}
	return comments, rows.Err()
}

func catViewRelations(ctx context.Context, tx *sql.Tx, taskUUID string) ([]CatViewRelation, error) {
	var relations []CatViewRelation
	for _, dir := range []struct {
		direction string
		query     string
	}{
		{"outgoing", `
			SELECT r.kind, r.created_at, t.id, t.uuid, t.slug, t.title,
			       COALESCE(r.created_by_principal_ref, '')
			FROM task_relations r JOIN tasks t ON r.to_task_uuid = t.uuid
			WHERE r.from_task_uuid = ? ORDER BY r.kind, t.id`},
		{"incoming", `
			SELECT r.kind, r.created_at, t.id, t.uuid, t.slug, t.title,
			       COALESCE(r.created_by_principal_ref, '')
			FROM task_relations r JOIN tasks t ON r.from_task_uuid = t.uuid
			WHERE r.to_task_uuid = ? ORDER BY r.kind, t.id`},
	} {
		rows, err := tx.QueryContext(ctx, dir.query, taskUUID)
		if err != nil {
			return nil, NewInternalError(err)
		}
		for rows.Next() {
			var rel CatViewRelation
			if err := rows.Scan(&rel.Kind, &rel.CreatedAt, &rel.TaskID, &rel.TaskUUID, &rel.TaskSlug, &rel.TaskTitle, &rel.CreatedByID); err != nil {
				_ = rows.Close()
				return nil, NewInternalError(err)
			}
			rel.Direction = dir.direction
			relations = append(relations, rel)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, NewInternalError(err)
		}
		_ = rows.Close()
	}
	return relations, nil
}

func catViewBlockers(ctx context.Context, tx *sql.Tx, taskUUID string) ([]CatViewBlocker, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT t.id, t.state
		FROM task_relations r JOIN tasks t ON r.from_task_uuid = t.uuid
		WHERE r.to_task_uuid = ? AND r.kind = 'blocks'
		  AND t.state NOT IN ('completed', 'archived', 'deleted', 'cancelled', 'idea')
		ORDER BY t.id`, taskUUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	var blockers []CatViewBlocker
	for rows.Next() {
		var b CatViewBlocker
		if err := rows.Scan(&b.ID, &b.State); err != nil {
			return nil, NewInternalError(err)
		}
		blockers = append(blockers, b)
	}
	return blockers, rows.Err()
}

// principalHandle mirrors attribution.PrincipalHandle (strip "agent:" prefix).
func principalHandle(principalRef string) string {
	return strings.TrimPrefix(principalRef, "agent:")
}

func nullStr(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

// taskArtifactDir mirrors internal/cli's artifact path: a server-local host hint
// (PRAESIDIUM_HOME or ~/praesidium)/var/wrkq-artifacts/<id>.
func taskArtifactDir(taskID string) string {
	root := os.Getenv("PRAESIDIUM_HOME")
	if root == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			root = filepath.Join(home, "praesidium")
		} else {
			root = "praesidium"
		}
	}
	return filepath.Join(root, "var", "wrkq-artifacts", taskID)
}
