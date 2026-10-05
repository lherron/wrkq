//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/causedby"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/id"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
)

const nsTaskCreate = "wrkq.task.create"

// TaskCreate creates a task and returns the WrkqTask DTO. It enforces mandatory
// idempotency when an idempotencyKey is supplied (§8.2).
func (a *API) TaskCreate(ctx context.Context, p TaskCreateParams) (*WrkqTask, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Title) == "" {
		return nil, NewValidationError("title is required", map[string]any{
			"field":    "title",
			"expected": "non-empty string",
		})
	}

	var requestHash string
	if p.IdempotencyKey != "" {
		hp := p
		hp.IdempotencyKey = ""
		requestHash = canonicalRequestHash(hp)
		if raw, ok, err := a.idempotentReplay(nsTaskCreate, p.IdempotencyKey, requestHash); err != nil {
			return nil, err
		} else if ok {
			var dto WrkqTask
			if err := json.Unmarshal(raw, &dto); err != nil {
				return nil, NewInternalError(err)
			}
			return &dto, nil
		}
	}

	var projectUUID, slug string
	var subtaskOwnerUUID *string
	var err error
	if p.SubtaskOwner != "" {
		if p.Path != "" || p.Project != "" || p.ParentTask != "" || p.Campaign != "" {
			return nil, NewValidationError("subtaskOwner refuses path, project, parentTask and campaign", nil)
		}
		ownerUUID, ownerID, e := selectors.ResolveTask(a.db, p.SubtaskOwner)
		if e != nil {
			return nil, NewNotFoundError(p.SubtaskOwner, "task")
		}
		if !id.IsTask(ownerID + "." + p.Slug) {
			return nil, NewValidationError("invalid subtask slug", map[string]any{"field": "slug"})
		}
		subtaskOwnerUUID = &ownerUUID
		slug = p.Slug
	} else {
		if p.Slug != "" {
			return nil, NewValidationError("slug requires subtaskOwner", nil)
		}
		projectUUID, slug, err = a.resolveCreateTarget(p)
		if err != nil {
			return nil, err
		}
	}

	state := domain.StateOpen
	if strings.TrimSpace(p.State) != "" {
		parsed, perr := domain.ParseState(p.State)
		if perr != nil {
			return nil, NewValidationError(perr.Error(), map[string]any{"field": "state"})
		}
		state = parsed
	}
	if p.Kind != "" {
		if kerr := domain.ValidateTaskKind(p.Kind); kerr != nil {
			return nil, NewValidationError(kerr.Error(), map[string]any{"field": "kind"})
		}
	}
	if strings.TrimSpace(p.Resolution) != "" {
		if rerr := domain.ValidateResolution(p.Resolution); rerr != nil {
			return nil, NewValidationError(rerr.Error(), map[string]any{"field": "resolution"})
		}
	}
	if strings.TrimSpace(p.ForceUUID) != "" {
		if uerr := domain.ValidateUUID(p.ForceUUID); uerr != nil {
			return nil, NewValidationError(uerr.Error(), map[string]any{"field": "forceUuid"})
		}
	}
	var riskClass *string
	if strings.TrimSpace(p.RiskClass) != "" {
		trimmed := strings.TrimSpace(p.RiskClass)
		if rerr := domain.ValidateTaskRiskClass(trimmed); rerr != nil {
			return nil, NewValidationError(rerr.Error(), map[string]any{"field": "riskClass"})
		}
		riskClass = &trimmed
	}
	priority := p.Priority
	if priority == 0 {
		priority = 3
	}
	if verr := domain.ValidatePriority(priority); verr != nil {
		return nil, NewValidationError(verr.Error(), map[string]any{"field": "priority"})
	}

	var parentTaskUUID *string
	if strings.TrimSpace(p.ParentTask) != "" {
		uuid, _, rerr := selectors.ResolveTask(a.db, p.ParentTask)
		if rerr != nil {
			return nil, NewNotFoundError(p.ParentTask, "task")
		}
		parentTaskUUID = &uuid
	}
	// Normalized exactly as task.update does, so a bare agent slug works here too.
	var assigneePrincipalRef *string
	if assignee := strings.TrimSpace(p.AssigneePrincipalRef); assignee != "" {
		principalRef, err := attribution.NormalizeCompat(assignee)
		if err != nil {
			return nil, NewValidationError(err.Error(), map[string]any{"field": "assigneePrincipalRef"})
		}
		assigneePrincipalRef = &principalRef
	}
	requesterPrincipalRef, requesterScopeRef, rqerr := resolveRequester(p.RequesterPrincipalRef, p.RequesterScopeRef)
	if rqerr != nil {
		return nil, rqerr
	}
	meta, merr := taskMetaString(p.Meta, p.MetaRaw)
	if merr != nil {
		return nil, merr
	}

	var causedByRefs []store.CausedByRef
	if len(p.CausedBy) > 0 {
		refs, cerr := causedby.ResolveTokens(a.db, p.CausedBy, "")
		if cerr != nil {
			return nil, NewValidationError(cerr.Error(), map[string]any{"field": "causedBy"})
		}
		causedByRefs = refs
	}

	// Campaign ENROLMENT at create time. Resolution is deliberately the same
	// selectors.ResolveContainer the task-update path uses, with no project
	// constraint: a campaign may enroll a task resident in any project.
	var campaignUUID *string
	if strings.TrimSpace(p.Campaign) != "" {
		uuid, _, cerr := selectors.ResolveContainer(a.db, p.Campaign)
		if cerr != nil {
			return nil, NewNotFoundError(p.Campaign, "campaign")
		}
		campaignUUID = &uuid
	}

	attr, aerr := a.attributionFor(p.PrincipalRef)
	if aerr != nil {
		return nil, aerr
	}
	result, err := a.store.Tasks.CreateWithAttribution(attr, store.CreateParams{
		UUID:                  p.ForceUUID,
		Slug:                  slug,
		Title:                 p.Title,
		Description:           p.Description,
		Specification:         p.Specification,
		ProjectUUID:           projectUUID,
		State:                 state,
		Priority:              priority,
		Kind:                  p.Kind,
		ParentTaskUUID:        parentTaskUUID,
		SubtaskOwnerUUID:      subtaskOwnerUUID,
		AssigneePrincipalRef:  assigneePrincipalRef,
		RequesterPrincipalRef: requesterPrincipalRef,
		RequesterScopeRef:     requesterScopeRef,
		RequestedByProjectID:  optionalTrimmedString(p.RequestedByProjectID),
		AssignedProjectID:     optionalTrimmedString(p.AssignedProjectID),
		Resolution:            optionalTrimmedString(p.Resolution),
		Labels:                labelsString(p.Labels),
		Meta:                  meta,
		DueAt:                 p.DueAt,
		StartAt:               p.StartAt,
		RiskClass:             riskClass,
		CausedBy:              causedByRefs,
		CampaignUUID:          campaignUUID,
		Via:                   "rpc",
	})
	if err != nil {
		if isUniqueViolation(err) {
			// Never leak "UNIQUE constraint failed: tasks.project_uuid, tasks.slug":
			// name the colliding task and the two ways forward.
			where := strings.TrimSpace(p.Path)
			if where == "" {
				where = slug
			}
			return nil, NewConflictError(fmt.Sprintf(
				"a task with slug %q already exists at %s; pick another slug, or inspect the existing task with: wrkq cat %s",
				slug, where, where), map[string]any{"slug": slug})
		}
		return nil, mapStoreError(err, "")
	}

	dto, err := a.loadTask(ctx, result.UUID)
	if err != nil {
		return nil, err
	}
	if p.IdempotencyKey != "" {
		if serr := a.idempotentStore(nsTaskCreate, p.IdempotencyKey, requestHash, dto); serr != nil {
			return nil, serr
		}
	}
	return dto, nil
}

// resolveCreateTarget resolves the destination project UUID and task slug for a
// create request from its path/project selectors (or defaults).
func (a *API) resolveCreateTarget(p TaskCreateParams) (projectUUID, slug string, err error) {
	switch {
	case strings.TrimSpace(p.Path) != "":
		parentUUID, finalSlug, _, rerr := selectors.ResolveParentContainer(a.db, p.Path)
		if rerr != nil {
			// The missing thing is the parent container, not the task path being
			// created: name the parent so the caller creates the right thing.
			return "", "", NewNotFoundError(parentContainerPath(p.Path), "container")
		}
		slug = finalSlug
		if parentUUID != nil {
			projectUUID = *parentUUID
		} else {
			projectUUID, err = a.defaultProjectUUID()
			if err != nil {
				return "", "", err
			}
		}
	case strings.TrimSpace(p.Project) != "":
		uuid, _, rerr := selectors.ResolveContainer(a.db, p.Project)
		if rerr != nil {
			return "", "", NewNotFoundError(p.Project, "container")
		}
		projectUUID = uuid
		slug = slugFromTitle(p.Title)
	default:
		projectUUID, err = a.defaultProjectUUID()
		if err != nil {
			return "", "", err
		}
		slug = slugFromTitle(p.Title)
	}
	return projectUUID, slug, nil
}

// defaultProjectUUID returns the first project container. Tasks cannot live
// directly under the root container, so when no project exists yet (e.g. a
// freshly migrated database) a default "inbox" project is auto-created.
func (a *API) defaultProjectUUID() (string, error) {
	var uuid string
	err := a.db.QueryRow("SELECT uuid FROM containers WHERE kind = 'project' ORDER BY id LIMIT 1").Scan(&uuid)
	if err == nil {
		return uuid, nil
	}
	if err != sql.ErrNoRows {
		return "", NewInternalError(err)
	}
	attr, aerr := a.attributionFor("")
	if aerr != nil {
		return "", aerr
	}
	res, cerr := a.store.Containers.CreateWithAttribution(attr, store.ContainerCreateParams{
		Slug:  "inbox",
		Title: "Inbox",
		Kind:  "project",
	})
	if cerr != nil {
		return "", mapStoreError(cerr, "")
	}
	return res.UUID, nil
}

func slugFromTitle(title string) string {
	if slug, err := paths.NormalizeSlug(title); err == nil && slug != "" {
		return slug
	}
	return "task"
}

func taskMetaString(meta map[string]any, metaRaw string) (*string, error) {
	metaRaw = strings.TrimSpace(metaRaw)
	if metaRaw == "" {
		return metaString(meta), nil
	}
	if metaRaw == "null" {
		return nil, nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(metaRaw), &parsed); err != nil {
		return nil, NewValidationError("invalid meta JSON: "+err.Error(), map[string]any{"field": "metaRaw"})
	}
	if parsed == nil {
		return nil, nil
	}
	return &metaRaw, nil
}

// parentContainerPath is the container part of a task create path
// ("proj/inbox/slug" -> "proj/inbox"); a bare slug is returned unchanged.
func parentContainerPath(taskPath string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(taskPath), "/")
	if i := strings.LastIndex(trimmed, "/"); i > 0 {
		return trimmed[:i]
	}
	return trimmed
}

func isUniqueViolation(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}
