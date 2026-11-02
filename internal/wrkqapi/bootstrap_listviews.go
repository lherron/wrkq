//go:build wrkq_local

package wrkqapi

import (
	"context"

	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/workflow"
)

// bootstrapViewPage validates the common bounds and the method-specific opaque
// cursor. The anchor uses the stored timestamp, never its DTO normalization.
func bootstrapViewPage(method string, requested *int, encoded string) (limit int, createdAt, id string, err error) {
	limit = 500
	if requested != nil {
		limit = *requested
	}
	if limit < 1 || limit > 500 {
		return 0, "", "", NewValidationError("limit must be between 1 and 500", map[string]any{"field": "limit"})
	}
	if encoded == "" {
		return limit, "", "", nil
	}
	c, e := cursor.Decode(encoded)
	if e == nil && len(c.SortFields) == 1 && c.SortFields[0] == method+".created_at" && len(c.Descending) == 0 {
		if at, ok := c.LastValues[0].(string); ok && at != "" {
			return limit, at, c.LastID, nil
		}
	}
	return 0, "", "", NewValidationError("invalid cursor", map[string]any{"field": "cursor"})
}

func (a *API) PromiseListView(ctx context.Context, p PromiseListViewParams) (*WrkqPromiseListView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit, at, id, err := bootstrapViewPage("promise.listView", p.Limit, p.Cursor)
	if err != nil {
		return nil, err
	}
	states := p.States
	if states == nil {
		states = []string{"open"}
	}
	for _, state := range states {
		if err := domain.ValidatePromiseState(domain.PromiseState(state)); err != nil {
			return nil, NewValidationError(err.Error(), map[string]any{"field": "states"})
		}
	}
	result := &WrkqPromiseListView{Items: []WrkqPromise{}}
	if len(states) == 0 {
		return result, nil
	}
	rows, err := a.store.Promises.ListPage(ctx, states, at, id, limit+1)
	if err != nil {
		return nil, NewInternalError(err)
	}
	more := len(rows) > limit
	if more {
		rows = rows[:limit]
	}
	for i := range rows {
		item, err := a.promiseDTO(ctx, &rows[i])
		if err != nil {
			return nil, err
		}
		result.Items = append(result.Items, *item)
	}
	if more {
		last := rows[len(rows)-1]
		result.NextCursor, err = cursor.BuildNextCursor([]string{"promise.listView.created_at"}, []any{last.CreatedAt}, last.UUID)
	}
	return result, err
}

func (a *API) WorkflowInstancesView(ctx context.Context, p WorkflowInstancesViewParams) (*WrkqWorkflowInstancesView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	limit, at, id, err := bootstrapViewPage("workflow.instancesView", p.Limit, p.Cursor)
	if err != nil {
		return nil, err
	}
	if len(p.Tasks) > 500 {
		return nil, NewValidationError("tasks must contain at most 500 selectors", map[string]any{"field": "tasks"})
	}
	statuses := p.Statuses
	if statuses == nil {
		statuses = []string{"open", "active", "waiting"}
	}
	for _, status := range statuses {
		switch status {
		case "open", "active", "waiting", "closed":
		default:
			return nil, NewValidationError("invalid workflow status", map[string]any{"field": "statuses"})
		}
	}
	var tasks []string
	if p.Tasks != nil {
		tasks = []string{}
		for _, task := range p.Tasks {
			uuid, err := a.resolveTaskUUID(task)
			if err != nil {
				return nil, err
			}
			tasks = append(tasks, uuid)
		}
	}
	result := &WrkqWorkflowInstancesView{Items: []*workflow.Instance{}}
	if len(statuses) == 0 || (tasks != nil && len(tasks) == 0) {
		return result, nil
	}
	if a.wf == nil {
		return nil, NewInternalError(errWorkflowUnavailable)
	}
	items, err := a.wf.TaskInstancesPage(ctx, statuses, tasks, at, id, limit+1)
	if err != nil {
		return nil, err
	}
	more := len(items) > limit
	if more {
		items = items[:limit]
	}
	result.Items = items
	if more {
		last := items[len(items)-1]
		result.NextCursor, err = cursor.BuildNextCursor([]string{"workflow.instancesView.created_at"}, []any{last.CreatedAt}, last.ID)
	}
	return result, err
}
