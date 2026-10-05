//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
)

// The task read model and the plumbing every task verb shares: one SELECT, one
// row scanner, selector resolution, and store-error mapping.

// TaskShow returns the WrkqTask DTO for a task selector.
func (a *API) TaskShow(ctx context.Context, p TaskShowParams) (*WrkqTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uuid, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}
	task, err := a.loadTask(ctx, uuid)
	if err != nil {
		return nil, err
	}
	if task.SubtaskOwnerUUID != "" {
		room, err := a.roomOrDerivedForTask(ctx, uuid)
		if err != nil {
			return nil, err
		}
		task.RoomLocator = room.key
	}
	return task, nil
}

const taskPresenceTrimCharsSQL = "char(9)||char(10)||char(11)||char(12)||char(13)||' '"

// resolveTaskUUID resolves a task selector to its UUID, returning WRKQ_NOT_FOUND
// when the task does not exist.
func (a *API) resolveTaskUUID(selector string) (string, error) {
	if strings.TrimSpace(selector) == "" {
		return "", NewValidationError("task selector is required", map[string]any{"field": "task"})
	}
	uuid, _, err := selectors.ResolveTask(a.db, selector)
	if err != nil {
		return "", NewNotFoundError(selector, "task")
	}
	return uuid, nil
}

// loadTask reads a task by UUID into a WrkqTask DTO.
func (a *API) loadTask(ctx context.Context, uuid string) (*WrkqTask, error) {
	row := a.db.QueryRowContext(ctx, taskSelectSQL(taskBodyColumns)+" WHERE t.uuid = ?", uuid)
	task, _, err := scanTaskRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, NewNotFoundError(uuid, "task")
		}
		return nil, NewInternalError(err)
	}
	if err := a.loadCausedBy(task); err != nil {
		return nil, err
	}
	if task.SubtaskOwnerUUID == "" {
		rows, err := a.db.QueryContext(ctx, "SELECT id,slug,title,state,COALESCE(claimed_by_principal_ref,'') FROM tasks WHERE subtask_owner_uuid=? ORDER BY slug", uuid)
		if err != nil {
			return nil, NewInternalError(err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var summary SubtaskSummary
			if err := rows.Scan(&summary.ID, &summary.Slug, &summary.Title, &summary.State, &summary.ClaimedBy); err != nil {
				return nil, NewInternalError(err)
			}
			task.Subtasks = append(task.Subtasks, summary)
		}
		if err := rows.Err(); err != nil {
			return nil, NewInternalError(err)
		}
	}
	return task, nil
}

// taskBodyColumns selects a task's full prose; a summary listing blanks it.
const taskBodyColumns = "t.description, t.specification"

// taskSelectSQL is the one task read model: every column scanTaskRow expects,
// in its order, over the joins that supply the path and subtask owner.
func taskSelectSQL(bodyColumns string) string {
	return "SELECT t.uuid, t.id, t.slug, t.title, t.project_uuid, t.campaign_uuid, t.state, t.priority, t.kind, " + bodyColumns + ", t.outcome, " +
		"t.labels, t.meta, t.etag, t.start_at, t.due_at, t.created_at, t.updated_at, t.completed_at, t.archived_at, t.deleted_at, t.acknowledged_at, " +
		"t.assignee_principal_ref, t.requester_principal_ref, t.requester_scope_ref, t.claimed_by_principal_ref, t.claimed_scope_ref, t.claimed_node, t.claimed_at, t.claim_generation, " +
		"t.created_by_principal_ref, t.updated_by_principal_ref, COALESCE(t.risk_class,''), " +
		"COALESCE(cp.path || '/' || CASE WHEN owner.uuid IS NULL THEN t.slug ELSE owner.slug || '/' || t.slug END, t.slug), " +
		"CAST(LENGTH(TRIM(COALESCE(t.description,''), " + taskPresenceTrimCharsSQL + ")) > 0 AS INTEGER) AS has_description, " +
		"CAST(LENGTH(TRIM(COALESCE(t.specification,''), " + taskPresenceTrimCharsSQL + ")) > 0 AS INTEGER) AS has_specification, owner.id, t.subtask_owner_uuid, (SELECT COUNT(*) FROM tasks st WHERE st.subtask_owner_uuid=t.uuid AND st.state NOT IN ('completed','cancelled','archived','deleted')) " +
		"FROM tasks t LEFT JOIN v_container_paths cp ON cp.uuid = t.project_uuid LEFT JOIN tasks owner ON owner.uuid = t.subtask_owner_uuid"
}

// loadCausedBy attaches the ids of the tasks that caused this one.
func (a *API) loadCausedBy(task *WrkqTask) error {
	causedBy, err := store.CausedByIDs(a.db, task.UUID)
	if err != nil {
		return NewInternalError(err)
	}
	if len(causedBy) > 0 {
		task.CausedBy = causedBy
	}
	return nil
}

// scanTaskRow scans a task row (column order matches the queries above) into a
// WrkqTask DTO, returning the raw created_at for cursor anchoring.
func scanTaskRow(s rowScanner) (*WrkqTask, string, error) {
	var (
		uuid, id, slug, title, projectUUID, state, kind, description, specification string
		campaignUUID                                                                sql.NullString
		outcome                                                                     sql.NullString
		labels, meta                                                                sql.NullString
		priority                                                                    int
		etag                                                                        int64
		createdAt, updatedAt                                                        string
		startAt, dueAt, completedAt, archivedAt, deletedAt, acknowledgedAt          sql.NullString
		assignee, claimedBy, claimedScope, claimedNode, claimedAt                   sql.NullString
		requesterPrincipal, requesterScope                                          sql.NullString
		createdByPrincipal, updatedByPrincipal                                      sql.NullString
		claimGeneration                                                             int64
		riskClass, path                                                             string
		hasDescription, hasSpecification                                            int
		subtaskOwner, subtaskOwnerUUID                                              sql.NullString
		openSubtaskCount                                                            int
	)
	if err := s.Scan(
		&uuid, &id, &slug, &title, &projectUUID, &campaignUUID, &state, &priority, &kind, &description, &specification, &outcome,
		&labels, &meta, &etag, &startAt, &dueAt, &createdAt, &updatedAt, &completedAt, &archivedAt, &deletedAt, &acknowledgedAt,
		&assignee, &requesterPrincipal, &requesterScope, &claimedBy, &claimedScope, &claimedNode, &claimedAt, &claimGeneration,
		&createdByPrincipal, &updatedByPrincipal, &riskClass, &path, &hasDescription, &hasSpecification, &subtaskOwner, &subtaskOwnerUUID, &openSubtaskCount,
	); err != nil {
		return nil, "", err
	}
	task := &WrkqTask{
		SubtaskOwner: subtaskOwner.String, SubtaskOwnerUUID: subtaskOwnerUUID.String, OpenSubtaskCount: openSubtaskCount,
		UUID:                  uuid,
		ID:                    id,
		Slug:                  slug,
		Title:                 title,
		ProjectUUID:           projectUUID,
		CampaignUUID:          campaignUUID.String,
		Path:                  path,
		State:                 state,
		Priority:              priority,
		Kind:                  kind,
		RiskClass:             riskClass,
		Description:           description,
		Specification:         specification,
		Outcome:               nullStringPtr(outcome),
		HasDescription:        hasDescription != 0,
		HasSpecification:      hasSpecification != 0,
		Labels:                parseLabels(labels.String),
		Meta:                  parseMeta(meta.String),
		ETag:                  etag,
		StartAt:               toRFC3339(startAt.String),
		DueAt:                 toRFC3339(dueAt.String),
		CreatedAt:             toRFC3339(createdAt),
		UpdatedAt:             toRFC3339(updatedAt),
		CompletedAt:           toRFC3339(completedAt.String),
		ArchivedAt:            toRFC3339(archivedAt.String),
		DeletedAt:             toRFC3339(deletedAt.String),
		AcknowledgedAt:        toRFC3339(acknowledgedAt.String),
		AssigneePrincipalRef:  assignee.String,
		RequesterPrincipalRef: requesterPrincipal.String,
		RequesterScopeRef:     requesterScope.String,
		ClaimedBy:             claimedBy.String,
		ClaimedScope:          claimedScope.String,
		ClaimedNode:           claimedNode.String,
		ClaimedAt:             toRFC3339(claimedAt.String),
		ClaimGeneration:       claimGeneration,
		CreatedByPrincipalRef: createdByPrincipal.String,
		UpdatedByPrincipalRef: updatedByPrincipal.String,
		createdAtRaw:          createdAt,
		updatedAtRaw:          updatedAt,
	}
	return task, createdAt, nil
}

// mapStoreError converts a raw store/domain error from a task verb into a
// typed WRKQ error.
func mapStoreError(err error, selector string) error {
	if err == nil {
		return nil
	}
	var domainErr Error
	if errors.As(err, &domainErr) {
		return domainErr
	}
	var mismatch *domain.ETagMismatchError
	if errors.As(err, &mismatch) {
		return NewConflictError("task update conflict", map[string]any{"currentEtag": mismatch.Actual})
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "not found"):
		return NewNotFoundError(selector, "task")
	case strings.Contains(lower, "unique") || strings.Contains(lower, "constraint"):
		return NewConflictError(msg, nil)
	case strings.Contains(lower, "invalid") || strings.Contains(lower, "required") || strings.Contains(lower, "must ") || strings.Contains(lower, "cannot "):
		return NewValidationError(msg, nil)
	default:
		return NewInternalError(err)
	}
}
