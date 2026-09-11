//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
)

var projectEventTypePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

var reservedProjectEventNamespaces = map[string]struct{}{
	"task": {}, "container": {}, "campaign": {}, "workflow": {}, "promise": {},
	"comment": {}, "room": {}, "envelope": {}, "member": {}, "handoff": {},
	"attachment": {}, "actor": {}, "config": {}, "system": {},
}

func (a *API) ProjectEventPost(ctx context.Context, p ProjectEventPostParams) (*WrkqProjectEventPostResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateProjectEventPost(&p); err != nil {
		return nil, err
	}

	projectUUID, containerUUID, campaignUUID, taskUUID, err := a.resolveProjectEventAffiliation(ctx, p.Project, p.Task)
	if err != nil {
		return nil, err
	}
	occurredAt := strings.TrimSpace(p.OccurredAt)
	if occurredAt == "" {
		occurredAt = time.Now().UTC().Format(time.RFC3339)
	} else {
		parsed, _ := time.Parse(time.RFC3339, occurredAt)
		occurredAt = parsed.UTC().Format(time.RFC3339)
	}

	principal := optionalTrimmedString(p.PrincipalRef)
	scope := optionalTrimmedString(p.ScopeRef)
	event, created, err := a.store.ProjectEvents.Create(store.ProjectEventCreateParams{
		ProjectUUID: projectUUID, ContainerUUID: containerUUID, CampaignUUID: campaignUUID,
		TaskUUID: taskUUID, Type: p.Type, Summary: p.Summary, Attributes: string(p.Attributes),
		PrincipalRef: principal, ScopeRef: scope, IdempotencyKey: optionalTrimmedString(p.IdempotencyKey),
		OccurredAt: occurredAt,
	})
	if err != nil {
		return nil, NewInternalError(err)
	}
	return &WrkqProjectEventPostResult{UUID: event.UUID, Created: created, ID: event.ID}, nil
}

func (a *API) ProjectEventGet(ctx context.Context, p ProjectEventGetParams) (*WrkqProjectEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ref := strings.TrimSpace(p.ProjectEvent)
	if ref == "" {
		return nil, NewValidationError("projectEvent is required", map[string]any{"field": "projectEvent"})
	}
	event, err := a.store.ProjectEvents.Get(ref)
	if err == sql.ErrNoRows {
		return nil, NewNotFoundError(ref, "project event")
	}
	if err != nil {
		return nil, NewInternalError(err)
	}
	return a.projectEventDTO(ctx, event), nil
}

func (a *API) ProjectEventTypesView(ctx context.Context, p ProjectEventTypesViewParams) (*WrkqProjectEventTypesView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var containers []string
	if strings.TrimSpace(p.Project) != "" {
		projectUUID, err := a.resolveUnadornedProject(ctx, p.Project, "project")
		if err != nil {
			return nil, err
		}
		containers, err = loadTimelineAffiliationSet(ctx, a.db.DB, projectUUID)
		if err != nil {
			return nil, NewInternalError(err)
		}
	}
	rows, err := a.store.ProjectEvents.TypesForContainers(containers)
	if err != nil {
		return nil, NewInternalError(err)
	}
	result := &WrkqProjectEventTypesView{Items: make([]WrkqProjectEventType, 0, len(rows))}
	for _, row := range rows {
		result.Items = append(result.Items, WrkqProjectEventType{Type: row.Type, Count: row.Count, LastCreatedAt: toRFC3339(row.LastCreatedAt)})
	}
	return result, nil
}

func validateProjectEventPost(p *ProjectEventPostParams) error {
	p.Type = strings.TrimSpace(p.Type)
	if !projectEventTypePattern.MatchString(p.Type) {
		return NewValidationError("type must be a dotted lowercase event name", map[string]any{"field": "type", "reason": "invalid_format"})
	}
	namespace := strings.SplitN(p.Type, ".", 2)[0]
	if _, reserved := reservedProjectEventNamespaces[namespace]; reserved {
		return NewValidationError("project event type uses a reserved namespace", map[string]any{"field": "type", "reason": "reserved_namespace"})
	}
	if strings.TrimSpace(p.Summary) == "" || len(p.Summary) > 512 || strings.ContainsAny(p.Summary, "\r\n") {
		return NewValidationError("summary is required, single-line, and at most 512 characters", map[string]any{"field": "summary"})
	}
	if len(p.Attributes) == 0 {
		return NewValidationError("attributes are required", map[string]any{"field": "attributes", "reason": "required"})
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(p.Attributes, &object) != nil || object == nil {
		return NewValidationError("attributes must be a JSON object", map[string]any{"field": "attributes", "reason": "invalid_value"})
	}
	if len(object) == 0 {
		return NewValidationError("attributes are required", map[string]any{"field": "attributes", "reason": "required"})
	}
	if len(object) > 32 {
		return NewValidationError("attributes has too many keys", map[string]any{"field": "attributes", "reason": "too_many_keys"})
	}
	keyPattern := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	for key, raw := range object {
		if !keyPattern.MatchString(key) {
			return NewValidationError("attributes has an invalid key", map[string]any{"field": "attributes", "reason": "invalid_key"})
		}
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return NewValidationError("attributes has an invalid value", map[string]any{"field": "attributes", "reason": "invalid_value"})
		}
		if len(value) > 1024 {
			return NewValidationError("attributes value is too long", map[string]any{"field": "attributes", "reason": "value_too_long"})
		}
	}
	if value := strings.TrimSpace(p.PrincipalRef); value != "" {
		if err := attribution.ValidatePrincipalRef(value); err != nil {
			return NewValidationError("principalRef must be agent:<id>", map[string]any{"field": "principalRef"})
		}
		p.PrincipalRef = value
	}
	if value := strings.TrimSpace(p.OccurredAt); value != "" {
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return NewValidationError("occurredAt must be RFC3339", map[string]any{"field": "occurredAt"})
		}
	}
	return nil
}

func (a *API) resolveProjectEventAffiliation(ctx context.Context, project, task string) (string, string, *string, *string, error) {
	if strings.TrimSpace(task) == "" {
		if strings.TrimSpace(project) == "" {
			return "", "", nil, nil, NewValidationError("project is required when task is omitted", map[string]any{"field": "project"})
		}
		projectUUID, _, err := selectors.ResolveContainer(a.db, project)
		if err != nil {
			return "", "", nil, nil, NewValidationError("project must resolve to a project", map[string]any{"field": "project", "reason": "not_a_project"})
		}
		var kind string
		var campaign sql.NullString
		if err := a.db.QueryRowContext(ctx, `SELECT kind, campaign_state FROM containers WHERE uuid = ?`, projectUUID).Scan(&kind, &campaign); err != nil || kind != "project" {
			return "", "", nil, nil, NewValidationError("project must resolve to a project", map[string]any{"field": "project", "reason": "not_a_project"})
		}
		var campaignUUID *string
		if campaign.Valid {
			campaignUUID = &projectUUID
		}
		return projectUUID, projectUUID, campaignUUID, nil, nil
	}

	taskUUID, _, err := selectors.ResolveTask(a.db, task)
	if err != nil {
		return "", "", nil, nil, NewNotFoundError(task, "task")
	}
	var containerUUID string
	var enrolled, residentCampaign sql.NullString
	if err := a.db.QueryRowContext(ctx, `SELECT t.project_uuid, t.campaign_uuid, c.campaign_state FROM tasks t JOIN containers c ON c.uuid = t.project_uuid WHERE t.uuid = ?`, taskUUID).Scan(&containerUUID, &enrolled, &residentCampaign); err != nil {
		return "", "", nil, nil, NewInternalError(err)
	}
	var projectUUID string
	if err := a.db.QueryRowContext(ctx, `WITH RECURSIVE ancestry(uuid, parent_uuid, kind) AS (
		SELECT uuid, parent_uuid, kind FROM containers WHERE uuid = ?
		UNION ALL
		SELECT c.uuid, c.parent_uuid, c.kind FROM containers c JOIN ancestry a ON c.uuid = a.parent_uuid
	) SELECT uuid FROM ancestry WHERE kind = 'project' LIMIT 1`, containerUUID).Scan(&projectUUID); err != nil {
		return "", "", nil, nil, NewValidationError("task has no owning project", map[string]any{"field": "task", "reason": "task_not_in_project"})
	}
	if strings.TrimSpace(project) != "" {
		expected, _, rerr := selectors.ResolveContainer(a.db, project)
		if rerr != nil || expected != projectUUID {
			return "", "", nil, nil, NewValidationError("task is not in project", map[string]any{"field": "task", "reason": "task_not_in_project"})
		}
	}
	var campaignUUID *string
	if residentCampaign.Valid {
		campaignUUID = &containerUUID
	} else if enrolled.Valid {
		value := enrolled.String
		campaignUUID = &value
	}
	return projectUUID, containerUUID, campaignUUID, &taskUUID, nil
}

func (a *API) projectEventDTO(ctx context.Context, event *domain.ProjectEvent) *WrkqProjectEvent {
	result := &WrkqProjectEvent{
		UUID: event.UUID, ProjectUUID: event.ProjectUUID,
		ContainerUUID: event.ContainerUUID, CampaignUUID: event.CampaignUUID,
		TaskUUID: event.TaskUUID, Type: event.Type, Attributes: json.RawMessage(event.Attributes),
		PrincipalRef: event.PrincipalRef, ScopeRef: event.ScopeRef, Summary: event.Summary,
		IdempotencyKey: event.IdempotencyKey, OccurredAt: toRFC3339(event.OccurredAt), CreatedAt: toRFC3339(event.CreatedAt),
	}
	if event.TaskUUID != nil {
		var id string
		if a.db.QueryRowContext(ctx, `SELECT id FROM tasks WHERE uuid = ?`, *event.TaskUUID).Scan(&id) == nil {
			result.Task = &id
		}
	}
	{
		var path string
		if a.db.QueryRowContext(ctx, `SELECT path FROM v_container_paths WHERE uuid = ?`, event.ContainerUUID).Scan(&path) == nil {
			result.Container = &path
		}
	}
	if event.CampaignUUID != nil {
		var path string
		if a.db.QueryRowContext(ctx, `SELECT path FROM v_container_paths WHERE uuid = ?`, *event.CampaignUUID).Scan(&path) == nil {
			result.Campaign = &path
		}
	}
	return result
}

func optionalTrimmedString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
