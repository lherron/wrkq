//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
)

// TreeView walks the container/task hierarchy under p.Path and returns the legacy
// tree projection. It is a faithful port of internal/cli buildTree +
// resolveTopLevelProjectID, so the mirror reproduces legacy byte-for-byte.
func (a *API) TreeView(ctx context.Context, p TreeViewParams) (*WrkqTreeView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pruneEmpty := !p.IncludeArchived
	root, err := a.buildTreeNode(ctx, p.Path, p.MaxDepth, p.IncludeArchived, p.OpenOnly, pruneEmpty, 0)
	if err != nil {
		return nil, err
	}
	if err := a.attachExternalTreeBacklinks(ctx, root, p.IncludeArchived, p.OpenOnly); err != nil {
		return nil, err
	}
	if p.IncludeCampaignMembers {
		if err := a.attachCampaignEnrollments(ctx, root, p.Path, p.IncludeArchived, p.OpenOnly); err != nil {
			return nil, err
		}
	}

	outputPath := p.Path
	if outputPath == "" {
		outputPath = "."
	}

	return &WrkqTreeView{
		Path:                         outputPath,
		ProjectID:                    a.treeTopLevelProjectID(p.Path),
		Children:                     root.Children,
		HiddenContainersNotDisplayed: root.hiddenContainerCount,
		WireRawPath:                  p.Path,
	}, nil
}

func (a *API) attachCampaignEnrollments(ctx context.Context, root *WrkqTreeNode, path string, includeArchived, openOnly bool) error {
	if path == "" {
		return nil
	}
	campaignUUID, _, err := selectors.WalkContainerPath(a.db, path)
	if err != nil {
		return nil
	}
	var state sql.NullString
	if err := a.db.QueryRowContext(ctx, "SELECT campaign_state FROM containers WHERE uuid = ?", campaignUUID).Scan(&state); err != nil || !state.Valid {
		return nil
	}
	campaignProject := strings.Split(path, "/")[0]
	for _, child := range root.Children {
		if child.Type == "task" {
			child.ExternalPath = "campaign:"
		}
	}
	rows, err := a.db.QueryContext(ctx, `
		WITH RECURSIVE container_ancestors(task_uuid, uuid, parent_uuid, slug, kind) AS (
		    SELECT t.uuid, c.uuid, c.parent_uuid, c.slug, c.kind
		      FROM tasks t JOIN containers c ON c.uuid = t.project_uuid
		    UNION ALL
		    SELECT ca.task_uuid, c.uuid, c.parent_uuid, c.slug, c.kind
		      FROM container_ancestors ca JOIN containers c ON c.uuid = ca.parent_uuid
		)
		SELECT t.uuid, t.id, t.slug, t.title, t.state, t.created_at, t.archived_at, t.deleted_at,
		       t.requested_by_project_id, t.assigned_project_id, t.acknowledged_at, t.resolution,
		       COALESCE((SELECT slug FROM container_ancestors ca
		                  WHERE ca.task_uuid = t.uuid AND ca.kind = 'project' LIMIT 1), '')
		  FROM tasks t
		 WHERE t.campaign_uuid = ? AND t.project_uuid != ?
		 ORDER BY t.created_at, t.id
	`, campaignUUID, campaignUUID)
	if err != nil {
		return NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var node WrkqTreeNode
		var archivedAt, deletedAt, requestedBy, assigned, acknowledged, resolution *string
		var residentProject string
		if err := rows.Scan(&node.UUID, &node.ID, &node.Slug, &node.Title, &node.State, &node.WireCreatedAt,
			&archivedAt, &deletedAt, &requestedBy, &assigned, &acknowledged, &resolution, &residentProject); err != nil {
			return NewInternalError(err)
		}
		node.Type = "task"
		node.RequestedByProjectID, node.AssignedProjectID = requestedBy, assigned
		node.AcknowledgedAt, node.Resolution = acknowledged, resolution
		node.IsArchived, node.IsDeleted = archivedAt != nil, deletedAt != nil
		show := node.State == "draft" || node.State == "open"
		if includeArchived {
			show = true
		} else if node.IsArchived || node.IsDeleted {
			show = false
		}
		if openOnly && node.State != "open" {
			show = false
		}
		if !show {
			continue
		}
		node.ExternalPath = "campaign:"
		if residentProject != campaignProject {
			node.ExternalPath += residentProject
		}
		root.Children = append(root.Children, &node)
	}
	return rows.Err()
}

// treeTopLevelProjectID returns the friendly ID of the top-level project owning
// path (legacy resolveTopLevelProjectID). "" for the multi-project root view.
func (a *API) treeTopLevelProjectID(rootPath string) string {
	segments := paths.SplitPath(rootPath)
	if len(segments) == 0 {
		return ""
	}
	_, friendlyID, err := selectors.WalkContainerPath(a.db, segments[0])
	if err != nil {
		return ""
	}
	return friendlyID
}

// buildTreeNode is the faithful port of internal/cli buildTree.
func (a *API) buildTreeNode(ctx context.Context, path string, maxDepth int, includeArchived, openOnly, pruneEmptyContainers bool, currentDepth int) (*WrkqTreeNode, error) {
	root := &WrkqTreeNode{
		Type:     "container",
		Slug:     path,
		Children: make([]*WrkqTreeNode, 0),
	}

	if maxDepth > 0 && currentDepth >= maxDepth {
		return root, nil
	}

	var parentUUID *string
	if path != "" {
		uuid, _, err := selectors.WalkContainerPath(a.db, path)
		if err != nil {
			return nil, NewValidationError(fmt.Sprintf("failed to resolve path %q: %v", path, err), map[string]any{"field": "path"})
		}
		parentUUID = &uuid
	}

	containerQuery := `
		SELECT uuid, id, slug, COALESCE(title, slug) as title, created_at, archived_at
		FROM containers
		WHERE `
	var containerArgs []any
	if parentUUID == nil {
		containerQuery += `parent_uuid = (SELECT uuid FROM containers WHERE kind = 'root')`
	} else {
		containerQuery += `parent_uuid = ?`
		containerArgs = append(containerArgs, *parentUUID)
	}
	if !includeArchived {
		containerQuery += ` AND archived_at IS NULL`
	}
	containerQuery += ` ORDER BY slug`

	rows, err := a.db.QueryContext(ctx, containerQuery, containerArgs...)
	if err != nil {
		return nil, NewInternalError(err)
	}
	for rows.Next() {
		var node WrkqTreeNode
		var archivedAt *string
		if err := rows.Scan(&node.UUID, &node.ID, &node.Slug, &node.Title, &node.WireCreatedAt, &archivedAt); err != nil {
			_ = rows.Close()
			return nil, NewInternalError(err)
		}
		node.Type = "container"
		node.IsArchived = archivedAt != nil

		childPath := path
		if childPath != "" {
			childPath += "/"
		}
		childPath += node.Slug

		child, err := a.buildTreeNode(ctx, childPath, maxDepth, includeArchived, openOnly, pruneEmptyContainers, currentDepth+1)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}

		node.Children = child.Children
		node.AllTasksCompleted = child.AllTasksCompleted
		node.hasVisibleTasks = child.hasVisibleTasks
		node.hasVisibleContent = child.hasVisibleContent
		node.hiddenContainerCount = child.hiddenContainerCount

		shouldShowContainer := !pruneEmptyContainers || node.hasVisibleContent || treeAlwaysShow(&node)
		if shouldShowContainer {
			root.Children = append(root.Children, &node)
			root.hasVisibleContent = true
			root.hiddenContainerCount += node.hiddenContainerCount
		} else {
			root.hiddenContainerCount += 1 + node.hiddenContainerCount
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(err)
	}

	if parentUUID != nil || path == "" {
		if parentUUID == nil {
			return root, nil
		}
		taskQuery := `
			SELECT uuid, id, slug, title, state, created_at, archived_at, deleted_at,
			       requested_by_project_id, assigned_project_id, acknowledged_at, resolution,
			       parent_task_uuid
			FROM tasks
			WHERE project_uuid = ?`
		taskArgs := []any{*parentUUID}
		taskQuery += ` ORDER BY created_at ASC, id ASC`

		taskRows, err := a.db.QueryContext(ctx, taskQuery, taskArgs...)
		if err != nil {
			return nil, NewInternalError(err)
		}

		var tasks []*WrkqTreeNode
		totalTasks := 0
		closedTasks := 0

		for taskRows.Next() {
			var node WrkqTreeNode
			var archivedAt, deletedAt *string
			var requestedBy, assignedProject, acknowledgedAt, resolution, parentTaskUUID *string
			if err := taskRows.Scan(&node.UUID, &node.ID, &node.Slug, &node.Title, &node.State, &node.WireCreatedAt, &archivedAt, &deletedAt,
				&requestedBy, &assignedProject, &acknowledgedAt, &resolution, &parentTaskUUID); err != nil {
				_ = taskRows.Close()
				return nil, NewInternalError(err)
			}
			if parentTaskUUID != nil {
				node.WireParentTaskUUID = *parentTaskUUID
			}
			node.Type = "task"
			node.RequestedByProjectID = requestedBy
			node.AssignedProjectID = assignedProject
			node.AcknowledgedAt = acknowledgedAt
			node.Resolution = resolution
			node.IsArchived = archivedAt != nil
			node.IsDeleted = deletedAt != nil
			node.Children = make([]*WrkqTreeNode, 0)

			totalTasks++
			isClosed := node.IsArchived || node.IsDeleted || node.State == "completed"
			if isClosed {
				closedTasks++
			}

			showTask := node.State == "draft" || node.State == "open"
			if includeArchived {
				showTask = true
			} else if node.IsArchived || node.IsDeleted {
				showTask = false
			}
			if openOnly && node.State != "open" {
				showTask = false
			}

			if showTask {
				tasks = append(tasks, &node)
				root.hasVisibleTasks = true
				root.hasVisibleContent = true
			}
		}
		_ = taskRows.Close()
		if err := taskRows.Err(); err != nil {
			return nil, NewInternalError(err)
		}

		allDirectTasksClosed := totalTasks == 0 || (totalTasks > 0 && closedTasks == totalTasks)
		allChildContainersDone := true
		for _, child := range root.Children {
			if child.Type == "container" {
				if !child.AllTasksCompleted {
					allChildContainersDone = false
					break
				}
			}
		}
		root.AllTasksCompleted = allDirectTasksClosed && allChildContainersDone

		byUUID := make(map[string]*WrkqTreeNode, len(tasks))
		for _, t := range tasks {
			byUUID[t.UUID] = t
		}
		topTasks := make([]*WrkqTreeNode, 0, len(tasks))
		for _, t := range tasks {
			if parent, ok := byUUID[t.WireParentTaskUUID]; ok && t.WireParentTaskUUID != "" {
				parent.Children = append(parent.Children, t)
				continue
			}
			topTasks = append(topTasks, t)
		}

		if includeArchived || !root.AllTasksCompleted || totalTasks == 0 {
			root.Children = append(root.Children, topTasks...)
		}
	}

	root.hasVisibleContent = root.hasVisibleContent || root.hasVisibleTasks || len(root.Children) > 0
	return root, nil
}

func treeAlwaysShow(node *WrkqTreeNode) bool {
	return node.Type == "container" && node.Slug == "inbox"
}

func (a *API) attachExternalTreeBacklinks(ctx context.Context, root *WrkqTreeNode, includeArchived bool, openOnly bool) error {
	var walk func(nodes []*WrkqTreeNode) error
	walk = func(nodes []*WrkqTreeNode) error {
		for _, node := range nodes {
			if node.Type == "task" {
				children, err := a.loadExternalTreeBacklinks(ctx, node.UUID, includeArchived, openOnly)
				if err != nil {
					return err
				}
				node.ExternalChildren = children
			}
			if err := walk(node.Children); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(root.Children)
}

func (a *API) loadExternalTreeBacklinks(ctx context.Context, parentUUID string, includeArchived bool, openOnly bool) ([]*WrkqTreeNode, error) {
	rows, err := a.db.QueryContext(ctx, `
		SELECT c.uuid, c.id, c.slug, c.title, c.state, c.created_at, c.archived_at, c.deleted_at,
		       c.requested_by_project_id, c.assigned_project_id, c.acknowledged_at, c.resolution,
		       COALESCE(cp.id, ''), COALESCE(vcp.path, '')
		  FROM tasks c
		  JOIN tasks p ON p.uuid = c.parent_task_uuid
		  LEFT JOIN containers cp ON cp.uuid = c.project_uuid
		  LEFT JOIN v_container_paths vcp ON vcp.uuid = c.project_uuid
		 WHERE c.parent_task_uuid = ?
		   AND c.project_uuid != p.project_uuid
		 ORDER BY c.created_at ASC, c.id ASC
	`, parentUUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()

	out := []*WrkqTreeNode{}
	for rows.Next() {
		var node WrkqTreeNode
		var archivedAt, deletedAt *string
		var requestedBy, assignedProject, acknowledgedAt, resolution *string
		if err := rows.Scan(&node.UUID, &node.ID, &node.Slug, &node.Title, &node.State, &node.WireCreatedAt, &archivedAt, &deletedAt,
			&requestedBy, &assignedProject, &acknowledgedAt, &resolution, &node.ExternalProjectID, &node.ExternalPath); err != nil {
			return nil, NewInternalError(err)
		}
		node.Type = "task"
		node.RequestedByProjectID = requestedBy
		node.AssignedProjectID = assignedProject
		node.AcknowledgedAt = acknowledgedAt
		node.Resolution = resolution
		node.IsArchived = archivedAt != nil
		node.IsDeleted = deletedAt != nil
		node.ExternalBacklink = true

		showTask := node.State == "draft" || node.State == "open"
		if includeArchived {
			showTask = true
		} else if node.IsArchived || node.IsDeleted {
			showTask = false
		}
		if openOnly && node.State != "open" {
			showTask = false
		}
		if showTask {
			out = append(out, &node)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(err)
	}
	return out, nil
}

var _ = sql.ErrNoRows
