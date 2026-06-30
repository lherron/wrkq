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

// TaskCatViewParams selects the task to project and whether to include comments.
type TaskCatViewParams struct {
	Task            string `json:"task"`
	IncludeComments *bool  `json:"includeComments,omitempty"` // default true
}

// WrkqTaskCatView is the server-owned COMPATIBILITY read model for `wrkq cat`.
// It is NOT the canonical task resource (that is WrkqTask / wrkq.task.show). Its
// shape — snake_case names, legacy time formats, nested comments/relations/
// blockers — exactly reproduces a single legacy `wrkq cat --json` object so the
// RPC CLI can render byte-identical output. Do not add fields here unless they
// are needed to reproduce a current `cat` invariant (T-05090 ruling).
//
// artifact_dir is a CANONICAL-HOST/server-local path hint, not a guarantee that
// the path exists on a remote caller's filesystem.
type WrkqTaskCatView struct {
	ID                    string            `json:"id"`
	UUID                  string            `json:"uuid"`
	Path                  string            `json:"path"`
	ArtifactDir           string            `json:"artifact_dir"`
	ProjectID             string            `json:"project_id"`
	ProjectUUID           string            `json:"project_uuid"`
	RequestedByProjectID  *string           `json:"requested_by_project_id,omitempty"`
	AssignedProjectID     *string           `json:"assigned_project_id,omitempty"`
	Slug                  string            `json:"slug"`
	Title                 string            `json:"title"`
	State                 string            `json:"state"`
	Priority              int               `json:"priority"`
	Kind                  string            `json:"kind"`
	ParentTaskID          *string           `json:"parent_task_id,omitempty"`
	ParentTaskUUID        *string           `json:"parent_task_uuid,omitempty"`
	AssigneeSlug          *string           `json:"assignee,omitempty"`
	AssigneeUUID          *string           `json:"assignee_uuid,omitempty"`
	AssigneePrincipalRef  *string           `json:"assignee_principal_ref,omitempty"`
	StartAt               *string           `json:"start_at,omitempty"`
	DueAt                 *string           `json:"due_at,omitempty"`
	Labels                *string           `json:"labels,omitempty"`
	Meta                  json.RawMessage   `json:"meta"`
	Description           string            `json:"description"`
	Specification         string            `json:"specification"`
	AcknowledgedAt        *string           `json:"acknowledged_at,omitempty"`
	Resolution            *string           `json:"resolution,omitempty"`
	Etag                  int64             `json:"etag"`
	CreatedAt             string            `json:"created_at"`
	UpdatedAt             string            `json:"updated_at"`
	CompletedAt           *string           `json:"completed_at,omitempty"`
	ArchivedAt            *string           `json:"archived_at,omitempty"`
	CreatedBy             string            `json:"created_by"`
	CreatedByPrincipalRef string            `json:"created_by_principal_ref,omitempty"`
	CreatedByActor        string            `json:"created_by_actor,omitempty"`
	CreatedByScopeRef     *string           `json:"created_by_scope_ref,omitempty"`
	UpdatedBy             string            `json:"updated_by"`
	UpdatedByPrincipalRef string            `json:"updated_by_principal_ref,omitempty"`
	CausedBy              []string          `json:"caused_by"`
	BlockedBy             []CatViewBlocker  `json:"blocked_by,omitempty"`
	Comments              []CatViewComment  `json:"comments,omitempty"`
	Relations             []CatViewRelation `json:"relations,omitempty"`
}

type CatViewComment struct {
	ID           string `json:"id"`
	CreatedAt    string `json:"created_at"`
	Body         string `json:"body"`
	PrincipalRef string `json:"principal_ref,omitempty"`
	ActorSlug    string `json:"actor_slug,omitempty"`
	ActorRole    string `json:"actor_role,omitempty"`
}

type CatViewRelation struct {
	Direction   string `json:"direction"`
	Kind        string `json:"kind"`
	TaskID      string `json:"task_id"`
	TaskUUID    string `json:"task_uuid"`
	TaskSlug    string `json:"task_slug"`
	TaskTitle   string `json:"task_title"`
	CreatedAt   string `json:"created_at"`
	CreatedByID string `json:"created_by_id"`
}

type CatViewBlocker struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

// TaskCatView assembles the legacy cat projection for one task. Selector→UUID
// resolution happens first (outside the snapshot); the single read transaction
// then covers the RESOLVED task UUID's projection — scalars, comments, relations,
// and blockers are internally consistent for that UUID. The snapshot does not
// cover the selector-resolution step (a concurrent rename/move/delete between
// resolution and the tx is possible, as with any resolve-then-read). It mirrors
// internal/cli/cat.go's projection logic exactly.
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
		id, slug, title, state, description, specification, kind string
		priority                                                 int
		startAt, dueAt, labels, meta, completedAt, archivedAt    *string
		requestedBy, assignedProject, acknowledgedAt, resolution *string
		parentTaskUUID, assigneeActorUUID, assigneePrincipalRef  *string
		createdAt, updatedAt                                     string
		etag                                                     int64
		projectUUID                                              string
		createdByUUID, updatedByUUID                             sql.NullString
		createdByPrincipalRef, updatedByPrincipalRef             sql.NullString
		createdByScopeRef                                        *string
	)
	err = tx.QueryRowContext(ctx, `
		SELECT id, slug, title, project_uuid, requested_by_project_id, assigned_project_id,
		       state, priority,
		       kind, parent_task_uuid, assignee_actor_uuid, assignee_principal_ref,
		       start_at, due_at, labels, meta, description, specification, etag,
		       created_at, updated_at, completed_at, archived_at,
		       acknowledged_at, resolution,
		       created_by_actor_uuid, updated_by_actor_uuid,
		       created_by_principal_ref, updated_by_principal_ref, created_by_scope_ref
		FROM tasks WHERE uuid = ?`, taskUUID).Scan(
		&id, &slug, &title, &projectUUID, &requestedBy, &assignedProject, &state, &priority,
		&kind, &parentTaskUUID, &assigneeActorUUID, &assigneePrincipalRef,
		&startAt, &dueAt, &labels, &meta, &description, &specification, &etag,
		&createdAt, &updatedAt, &completedAt, &archivedAt,
		&acknowledgedAt, &resolution,
		&createdByUUID, &updatedByUUID, &createdByPrincipalRef, &updatedByPrincipalRef, &createdByScopeRef,
	)
	if err == sql.ErrNoRows {
		return nil, NewNotFoundError(p.Task, "task")
	}
	if err != nil {
		return nil, NewInternalError(err)
	}

	var createdBySlug, updatedBySlug string
	if createdByUUID.Valid {
		_ = tx.QueryRowContext(ctx, "SELECT slug FROM actors WHERE uuid = ?", createdByUUID.String).Scan(&createdBySlug)
	}
	if updatedByUUID.Valid {
		_ = tx.QueryRowContext(ctx, "SELECT slug FROM actors WHERE uuid = ?", updatedByUUID.String).Scan(&updatedBySlug)
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
	if assigneeActorUUID != nil {
		var aSlug string
		if e := tx.QueryRowContext(ctx, "SELECT slug FROM actors WHERE uuid = ?", *assigneeActorUUID).Scan(&aSlug); e == nil {
			assigneeSlug = &aSlug
		}
	}
	if assigneePrincipalRef != nil {
		display := principalHandle(*assigneePrincipalRef)
		assigneeSlug = &display
	}
	createdBy := createdBySlug
	if createdByPrincipalRef.Valid {
		createdBy = principalHandle(createdByPrincipalRef.String)
	}
	updatedBy := updatedBySlug
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
		AssigneeUUID:          assigneeActorUUID,
		AssigneePrincipalRef:  assigneePrincipalRef,
		StartAt:               startAt,
		DueAt:                 dueAt,
		Labels:                labels,
		Meta:                  json.RawMessage(metaValue),
		Description:           description,
		Specification:         specification,
		AcknowledgedAt:        acknowledgedAt,
		Resolution:            resolution,
		Etag:                  etag,
		CreatedAt:             createdAt,
		UpdatedAt:             updatedAt,
		CompletedAt:           completedAt,
		ArchivedAt:            archivedAt,
		CreatedBy:             createdBy,
		CreatedByPrincipalRef: nullStr(createdByPrincipalRef),
		CreatedByActor:        createdBySlug,
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
	return view, nil
}

func catViewComments(ctx context.Context, tx *sql.Tx, taskUUID string) ([]CatViewComment, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT c.id, c.created_at, c.body, c.created_by_principal_ref,
		       a.slug as actor_slug, a.role as actor_role
		FROM comments c
		LEFT JOIN actors a ON c.actor_uuid = a.uuid
		WHERE c.task_uuid = ? AND c.deleted_at IS NULL
		ORDER BY c.created_at ASC`, taskUUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	var comments []CatViewComment
	for rows.Next() {
		var comment CatViewComment
		var principalRef, actorSlug, actorRole sql.NullString
		if err := rows.Scan(&comment.ID, &comment.CreatedAt, &comment.Body, &principalRef, &actorSlug, &actorRole); err != nil {
			return nil, NewInternalError(err)
		}
		if principalRef.Valid {
			comment.PrincipalRef = principalRef.String
			comment.ActorSlug = principalHandle(principalRef.String)
		}
		if actorSlug.Valid && comment.ActorSlug == "" {
			comment.ActorSlug = actorSlug.String
		}
		if actorRole.Valid {
			comment.ActorRole = actorRole.String
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
			       COALESCE(r.created_by_principal_ref, a.id, '')
			FROM task_relations r JOIN tasks t ON r.to_task_uuid = t.uuid
			LEFT JOIN actors a ON r.created_by_actor_uuid = a.uuid
			WHERE r.from_task_uuid = ? ORDER BY r.kind, t.id`},
		{"incoming", `
			SELECT r.kind, r.created_at, t.id, t.uuid, t.slug, t.title,
			       COALESCE(r.created_by_principal_ref, a.id, '')
			FROM task_relations r JOIN tasks t ON r.from_task_uuid = t.uuid
			LEFT JOIN actors a ON r.created_by_actor_uuid = a.uuid
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
