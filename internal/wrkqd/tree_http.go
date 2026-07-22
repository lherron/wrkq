package wrkqd

import (
	"fmt"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/selectors"
)

// treeNode is the historical /v1/containers/tree response model. It remains
// daemon-owned while day-to-day command rendering lives only in rpccli.
type treeNode struct {
	Type                 string      `json:"type"`
	ID                   string      `json:"id"`
	Slug                 string      `json:"slug"`
	Title                string      `json:"title"`
	State                string      `json:"state,omitempty"`
	UUID                 string      `json:"uuid"`
	CreatedAt            string      `json:"-"`
	RequestedByProjectID *string     `json:"requested_by_project_id,omitempty"`
	AssignedProjectID    *string     `json:"assigned_project_id,omitempty"`
	AcknowledgedAt       *string     `json:"acknowledged_at,omitempty"`
	Resolution           *string     `json:"resolution,omitempty"`
	IsArchived           bool        `json:"is_archived"`
	IsDeleted            bool        `json:"is_deleted"`
	AllTasksCompleted    bool        `json:"all_tasks_completed,omitempty"`
	HasVisibleTasks      bool        `json:"-"`
	HasVisibleContent    bool        `json:"-"`
	HiddenContainerCount int         `json:"-"`
	Children             []*treeNode `json:"children,omitempty"`
	parentTaskUUID       string
}

func buildTree(database *db.DB, path string, maxDepth int, includeArchived bool, openOnly bool, pruneEmptyContainers bool, currentDepth int) (*treeNode, error) {
	root := &treeNode{Type: "container", Slug: path, Children: make([]*treeNode, 0)}
	if maxDepth > 0 && currentDepth >= maxDepth {
		return root, nil
	}

	var parentUUID *string
	if path != "" {
		uuid, _, err := selectors.WalkContainerPath(database, path)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve path %q: %w", path, err)
		}
		parentUUID = &uuid
	}

	containerQuery := `
		SELECT uuid, id, slug, COALESCE(title, slug), created_at, archived_at
		FROM containers WHERE `
	var containerArgs []interface{}
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

	rows, err := database.Query(containerQuery, containerArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query containers: %w", err)
	}
	for rows.Next() {
		var node treeNode
		var archivedAt *string
		if err := rows.Scan(&node.UUID, &node.ID, &node.Slug, &node.Title, &node.CreatedAt, &archivedAt); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("failed to scan container: %w", err)
		}
		node.Type = "container"
		node.IsArchived = archivedAt != nil
		childPath := path
		if childPath != "" {
			childPath += "/"
		}
		child, err := buildTree(database, childPath+node.Slug, maxDepth, includeArchived, openOnly, pruneEmptyContainers, currentDepth+1)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		node.Children = child.Children
		node.AllTasksCompleted = child.AllTasksCompleted
		node.HasVisibleTasks = child.HasVisibleTasks
		node.HasVisibleContent = child.HasVisibleContent
		node.HiddenContainerCount = child.HiddenContainerCount
		if !pruneEmptyContainers || node.HasVisibleContent || alwaysShowTreeContainer(&node) {
			root.Children = append(root.Children, &node)
			root.HasVisibleContent = true
			root.HiddenContainerCount += node.HiddenContainerCount
		} else {
			root.HiddenContainerCount += 1 + node.HiddenContainerCount
		}
	}
	_ = rows.Close()

	if parentUUID == nil {
		return root, nil
	}
	taskRows, err := database.Query(`
		SELECT uuid, id, slug, title, state, created_at, archived_at, deleted_at,
		       requested_by_project_id, assigned_project_id, acknowledged_at, resolution,
		       parent_task_uuid
		FROM tasks WHERE project_uuid = ? ORDER BY created_at ASC, id ASC
	`, *parentUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query tasks: %w", err)
	}
	var tasks []*treeNode
	totalTasks, closedTasks := 0, 0
	for taskRows.Next() {
		var node treeNode
		var archivedAt, deletedAt *string
		var requestedBy, assignedProject, acknowledgedAt, resolution, parentTaskUUID *string
		if err := taskRows.Scan(&node.UUID, &node.ID, &node.Slug, &node.Title, &node.State, &node.CreatedAt, &archivedAt, &deletedAt,
			&requestedBy, &assignedProject, &acknowledgedAt, &resolution, &parentTaskUUID); err != nil {
			_ = taskRows.Close()
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}
		if parentTaskUUID != nil {
			node.parentTaskUUID = *parentTaskUUID
		}
		node.Type = "task"
		node.RequestedByProjectID = requestedBy
		node.AssignedProjectID = assignedProject
		node.AcknowledgedAt = acknowledgedAt
		node.Resolution = resolution
		node.IsArchived = archivedAt != nil
		node.IsDeleted = deletedAt != nil
		node.Children = make([]*treeNode, 0)
		totalTasks++
		if node.IsArchived || node.IsDeleted || node.State == "completed" {
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
			root.HasVisibleTasks = true
			root.HasVisibleContent = true
		}
	}
	_ = taskRows.Close()

	allChildrenDone := true
	for _, child := range root.Children {
		if child.Type == "container" && !child.AllTasksCompleted {
			allChildrenDone = false
			break
		}
	}
	root.AllTasksCompleted = (totalTasks == 0 || closedTasks == totalTasks) && allChildrenDone
	byUUID := make(map[string]*treeNode, len(tasks))
	for _, task := range tasks {
		byUUID[task.UUID] = task
	}
	topTasks := make([]*treeNode, 0, len(tasks))
	for _, task := range tasks {
		if parent, ok := byUUID[task.parentTaskUUID]; ok && task.parentTaskUUID != "" {
			parent.Children = append(parent.Children, task)
		} else {
			topTasks = append(topTasks, task)
		}
	}
	if includeArchived || !root.AllTasksCompleted || totalTasks == 0 {
		root.Children = append(root.Children, topTasks...)
	}
	root.HasVisibleContent = root.HasVisibleContent || root.HasVisibleTasks || len(root.Children) > 0
	return root, nil
}

func alwaysShowTreeContainer(node *treeNode) bool {
	return node.Type == "container" && node.Slug == "inbox"
}
