//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/causedby"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/nodeauth"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/taskmember"
)

// Task edits under the etag CAS: patch fields in place, or move the task.

const nsTaskUpdate = "wrkq.task.update"

// TaskUpdate checks the owner receipt before current-state validation. Receipt,
// original result snapshot and mutation share the store's writer transaction.
func (a *API) TaskUpdate(ctx context.Context, p TaskUpdateParams) (*WrkqTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionForScope(ctx, p.Actor, p.ScopeRef)
	if err != nil {
		return nil, err
	}
	requestHash := ""
	if p.IdempotencyKey != "" {
		hp := p
		hp.IdempotencyKey = ""
		node, _ := nodeauth.FromContext(ctx)
		// Bind the receipt to the effective caller as well as the complete request,
		// including claim credentials and its original optimistic fence.
		requestHash = canonicalRequestHash(struct {
			Request TaskUpdateParams
			Caller  attribution.Attribution
			Node    string
		}{hp, attr, node})
	}
	var result *WrkqTask
	err = a.store.Tasks.WithUpdateTransaction(attr, "rpc", func(tx *sql.Tx, update store.TaskFieldUpdate) error {
		if p.IdempotencyKey != "" {
			raw, found, err := idempotentReplayOn(tx, nsTaskUpdate, p.IdempotencyKey, requestHash)
			if err != nil {
				return err
			}
			if found {
				var original WrkqTask
				if err := json.Unmarshal(raw, &original); err != nil {
					return NewInternalError(err)
				}
				result = &original
				return nil
			}
		}
		var err error
		result, err = a.taskUpdateTx(ctx, p, attr, tx, update)
		if err != nil {
			return err
		}
		if p.IdempotencyKey != "" {
			return idempotentStoreOn(tx, nsTaskUpdate, p.IdempotencyKey, requestHash, result)
		}
		return nil
	})
	if err != nil {
		var domainErr Error
		if errors.As(err, &domainErr) {
			return nil, err
		}
		return nil, mapStoreError(err, p.Task)
	}
	return result, nil
}

func (a *API) taskUpdateTx(ctx context.Context, p TaskUpdateParams, attr attribution.Attribution, tx *sql.Tx, update store.TaskFieldUpdate) (*WrkqTask, error) {
	uuid, err := resolveTaskUUIDOn(tx, p.Task)
	if err != nil {
		return nil, err
	}

	// Read current etag atomically against the CAS precondition. A supplied
	// expectEtag of 0 is a real (stale) precondition, not "skip".
	currentEtag, err := taskEtagOn(tx, uuid, p.Task)
	if err != nil {
		return nil, err
	}
	if p.ExpectEtag != nil && *p.ExpectEtag != currentEtag {
		return nil, NewConflictError("task etag precondition failed", map[string]any{
			"expectEtag":  *p.ExpectEtag,
			"currentEtag": currentEtag,
		})
	}

	fields, ferr := a.patchFieldsOn(tx, p.Patch)
	if ferr != nil {
		return nil, ferr
	}

	if err := validateCoversPatchOn(tx, uuid, fields, p.Patch.Covers); err != nil {
		return nil, err
	}

	if cbv, ok := fields["caused_by"]; ok {
		if cu, ok := cbv.(store.CausedByUpdate); ok {
			for _, r := range cu.Refs {
				if r.TaskUUID == uuid {
					return nil, NewValidationError("a task cannot be caused by itself", map[string]any{"field": "causedBy"})
				}
			}
		}
	}
	if len(fields) == 0 {
		return loadTaskOn(ctx, tx, uuid)
	}
	// A new requester principal without a scope drops a stored scope that
	// names a different agent, keeping the pair consistent.
	if principal, ok := fields["requester_principal_ref"].(string); ok {
		if _, scopeSet := fields["requester_scope_ref"]; !scopeSet {
			var stored sql.NullString
			if qerr := tx.QueryRow("SELECT requester_scope_ref FROM tasks WHERE uuid = ?", uuid).Scan(&stored); qerr != nil {
				return nil, NewInternalError(qerr)
			}
			if stored.Valid && requesterScopeAgent(stored.String) != principal {
				fields["requester_scope_ref"] = nil
			}
		}
	}

	var claimPrecondition func(*sql.Tx) error
	if state, ok := fields["state"].(string); ok && state == string(domain.StateCompleted) {
		claimPrecondition = func(tx *sql.Tx) error {
			row, qerr := loadTaskClaimRow(tx, uuid)
			if qerr != nil {
				return qerr
			}
			if !row.claimedBy.Valid {
				return nil
			}
			nodeID, nodeOK := nodeauth.FromContext(ctx)
			if !nodeOK {
				return NewNodeIdentityError()
			}
			if !claimMatches(row, attr.PrincipalRef, p.ClaimScope, nodeID, p.ClaimGeneration, p.ClaimToken) {
				return NewClaimSupersededError(claimHolderData(row))
			}
			return nil
		}
	}
	_, err = update(uuid, fields, currentEtag, claimPrecondition)
	if err != nil {
		var extErr *store.ExtError
		if errors.As(err, &extErr) {
			return nil, mapExtStoreError(err)
		}
		var mismatch *domain.ETagMismatchError
		if errors.As(err, &mismatch) {
			return nil, NewConflictError("task update conflict", map[string]any{
				"currentEtag": mismatch.Actual,
			})
		}
		return nil, mapStoreError(err, p.Task)
	}
	return loadTaskOn(ctx, tx, uuid)
}

// patchFields converts a TaskPatch into the store field map (DB column names).
func (a *API) patchFieldsOn(q taskQueryer, patch TaskPatch) (map[string]any, error) {
	fields := map[string]any{}
	if patch.Slug != nil {
		normalized, err := paths.NormalizeSlug(*patch.Slug)
		if err != nil {
			return nil, NewValidationError("invalid slug: "+err.Error(), map[string]any{"field": "slug"})
		}
		slug, err := paths.NewSlug(normalized)
		if err != nil {
			return nil, NewValidationError("invalid slug: "+err.Error(), map[string]any{"field": "slug"})
		}
		fields["slug"] = string(slug)
	}
	if patch.Title != nil {
		fields["title"] = *patch.Title
	}
	if patch.Description != nil {
		fields["description"] = *patch.Description
	}
	if patch.Specification != nil {
		fields["specification"] = *patch.Specification
	}
	if patch.Outcome != nil {
		if strings.TrimSpace(*patch.Outcome) == "" {
			fields["outcome"] = nil
		} else {
			fields["outcome"] = *patch.Outcome
		}
	}
	if patch.State != nil {
		if _, err := domain.ParseState(*patch.State); err != nil {
			return nil, NewValidationError(err.Error(), map[string]any{"field": "state"})
		}
		fields["state"] = *patch.State
	}
	if patch.Priority != nil {
		if err := domain.ValidatePriority(*patch.Priority); err != nil {
			return nil, NewValidationError(err.Error(), map[string]any{"field": "priority"})
		}
		fields["priority"] = *patch.Priority
	}
	if patch.Kind != nil {
		if err := domain.ValidateTaskKind(*patch.Kind); err != nil {
			return nil, NewValidationError(err.Error(), map[string]any{"field": "kind"})
		}
		fields["kind"] = *patch.Kind
	}
	if patch.RiskClass != nil {
		riskClass := strings.TrimSpace(*patch.RiskClass)
		if riskClass == "" {
			fields["risk_class"] = nil
		} else {
			if err := domain.ValidateTaskRiskClass(riskClass); err != nil {
				return nil, NewValidationError(err.Error(), map[string]any{"field": "riskClass"})
			}
			fields["risk_class"] = riskClass
		}
	}
	if patch.ParentTask != nil {
		parentRef := strings.TrimSpace(*patch.ParentTask)
		if parentRef == "" {
			fields["parent_task_uuid"] = nil
			if patch.Kind == nil {
				fields["kind"] = "task"
			}
		} else {
			parentUUID, _, err := selectors.ResolveTask(q, parentRef)
			if err != nil {
				return nil, NewNotFoundError(parentRef, "task")
			}
			fields["parent_task_uuid"] = parentUUID
			if patch.Kind == nil {
				fields["kind"] = "subtask"
			}
		}
	}
	if patch.Labels != nil {
		fields["labels"] = labelsString(*patch.Labels)
	}
	if patch.MetaRaw != nil {
		metaRaw := strings.TrimSpace(*patch.MetaRaw)
		if metaRaw == "" || metaRaw == "null" {
			fields["meta"] = nil
		} else {
			var meta map[string]any
			if err := json.Unmarshal([]byte(metaRaw), &meta); err != nil {
				return nil, NewValidationError("invalid meta JSON: "+err.Error(), map[string]any{"field": "metaRaw"})
			}
			if meta == nil {
				fields["meta"] = nil
			} else {
				fields["meta"] = metaRaw
			}
		}
	} else if patch.Meta != nil {
		fields["meta"] = metaString(*patch.Meta)
	}
	if patch.AssigneePrincipalRef != nil {
		assignee := strings.TrimSpace(*patch.AssigneePrincipalRef)
		if assignee == "" {
			fields["assignee_principal_ref"] = nil
		} else {
			principalRef, err := attribution.NormalizeCompat(assignee)
			if err != nil {
				return nil, NewValidationError(err.Error(), map[string]any{"field": "assigneePrincipalRef"})
			}
			fields["assignee_principal_ref"] = principalRef
		}
	}
	if err := requesterPatchFields(patch, fields); err != nil {
		return nil, err
	}
	if patch.RequestedByProjectID != nil {
		fields["requested_by_project_id"] = *patch.RequestedByProjectID
	}
	if patch.AssignedProjectID != nil {
		fields["assigned_project_id"] = *patch.AssignedProjectID
	}
	if patch.Resolution != nil {
		if err := domain.ValidateResolution(*patch.Resolution); err != nil {
			return nil, NewValidationError(err.Error(), map[string]any{"field": "resolution"})
		}
		fields["resolution"] = *patch.Resolution
	}
	if patch.DueAt != nil {
		fields["due_at"] = *patch.DueAt
	}
	if patch.StartAt != nil {
		fields["start_at"] = *patch.StartAt
	}
	if patch.Campaign != nil {
		campaign := strings.TrimSpace(*patch.Campaign)
		if campaign == "" {
			fields["campaign_uuid"] = nil
		} else {
			uuid, _, err := selectors.ResolveContainer(q, campaign)
			if err != nil {
				return nil, NewNotFoundError(campaign, "campaign")
			}
			fields["campaign_uuid"] = uuid
		}
	}
	if patch.CausedBy != nil {
		refs, cerr := causedby.ResolveTokens(q, *patch.CausedBy, "")
		if cerr != nil {
			return nil, NewValidationError(cerr.Error(), map[string]any{"field": "causedBy"})
		}
		fields["caused_by"] = store.CausedByUpdate{Refs: refs}
	}
	return fields, nil
}

// TaskMove moves a root task subtree to an existing target container.
func (a *API) TaskMove(ctx context.Context, p TaskMoveParams) (*WrkqTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	uuid, err := a.resolveTaskUUID(p.Task)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.TargetPath) == "" {
		return nil, NewValidationError("targetPath is required", map[string]any{"field": "targetPath"})
	}
	currentEtag, err := a.taskEtag(uuid, p.Task)
	if err != nil {
		return nil, err
	}
	if p.ExpectEtag != nil && *p.ExpectEtag != currentEtag {
		return nil, NewConflictError("task move conflict", map[string]any{
			"expectEtag":  *p.ExpectEtag,
			"currentEtag": currentEtag,
		})
	}

	ifMatch := int64(0)
	if p.ExpectEtag != nil {
		ifMatch = currentEtag
	}
	attr, aerr := a.attributionFor(ctx, p.Actor)
	if aerr != nil {
		return nil, aerr
	}
	if targetUUID, _, rerr := selectors.ResolveContainer(a.db, p.TargetPath); rerr == nil {
		if _, merr := a.store.Tasks.MoveWithViaAttribution(attr, uuid, targetUUID, ifMatch, "rpc"); merr != nil {
			return nil, mapStoreError(merr, p.Task)
		}
		return a.loadTask(ctx, uuid)
	}

	targetProjectUUID, targetSlug, rerr := a.resolveTaskMoveTarget(uuid, p.TargetPath)
	if rerr != nil {
		return nil, rerr
	}
	var existingTaskUUID string
	existingErr := a.db.QueryRow(
		`SELECT uuid FROM tasks WHERE slug = ? AND project_uuid = ? AND `+taskmember.Filter("", false),
		targetSlug, targetProjectUUID,
	).Scan(&existingTaskUUID)
	if existingErr != nil && existingErr != sql.ErrNoRows {
		return nil, NewInternalError(existingErr)
	}
	if existingErr == nil && existingTaskUUID != uuid {
		if !p.OverwriteTask {
			return nil, NewConflictError(
				"destination task already exists: "+p.TargetPath+" (use --overwrite-task to replace)",
				map[string]any{"targetPath": p.TargetPath},
			)
		}
		if _, perr := a.store.Tasks.PurgeWithAttribution(attr, existingTaskUUID, 0); perr != nil {
			return nil, mapStoreError(perr, p.TargetPath)
		}
	}

	fields := map[string]any{
		"slug":         targetSlug,
		"project_uuid": targetProjectUUID,
	}
	if _, uerr := a.store.Tasks.UpdateFieldsWithViaAttribution(attr, uuid, fields, ifMatch, "rpc"); uerr != nil {
		return nil, mapStoreError(uerr, p.Task)
	}
	return a.loadTask(ctx, uuid)
}

func (a *API) resolveTaskMoveTarget(taskUUID, targetPath string) (string, string, error) {
	parentUUID, normalizedSlug, _, err := selectors.ResolveParentContainer(a.db, targetPath)
	if err != nil {
		dstSegments := paths.SplitPath(targetPath)
		if len(dstSegments) != 1 {
			return "", "", NewNotFoundError(targetPath, "container")
		}
		currentProjectUUID, qerr := a.taskProjectUUID(taskUUID)
		if qerr != nil {
			return "", "", qerr
		}
		normalized, nerr := paths.NormalizeSlug(dstSegments[0])
		if nerr != nil {
			return "", "", NewValidationError("invalid destination slug "+strconv.Quote(dstSegments[0])+": "+nerr.Error(), map[string]any{"field": "targetPath"})
		}
		return currentProjectUUID, normalized, nil
	}
	if parentUUID != nil {
		return *parentUUID, normalizedSlug, nil
	}
	currentProjectUUID, err := a.taskProjectUUID(taskUUID)
	if err != nil {
		return "", "", err
	}
	return currentProjectUUID, normalizedSlug, nil
}

// taskProjectUUID is the container a task currently lives in.
func (a *API) taskProjectUUID(taskUUID string) (string, error) {
	var projectUUID string
	if err := a.db.QueryRow("SELECT project_uuid FROM tasks WHERE uuid = ?", taskUUID).Scan(&projectUUID); err != nil {
		if err == sql.ErrNoRows {
			return "", NewNotFoundError(taskUUID, "task")
		}
		return "", NewInternalError(err)
	}
	return projectUUID, nil
}

// taskEtag reads a task's current etag, the CAS precondition update and move
// compare against.
func (a *API) taskEtag(uuid, selector string) (int64, error) { return taskEtagOn(a.db, uuid, selector) }
func taskEtagOn(q taskQueryer, uuid, selector string) (int64, error) {
	var etag int64
	if err := q.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", uuid).Scan(&etag); err != nil {
		if err == sql.ErrNoRows {
			return 0, NewNotFoundError(selector, "task")
		}
		return 0, NewInternalError(err)
	}
	return etag, nil
}
