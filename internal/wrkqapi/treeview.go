package wrkqapi

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
)

// TreeViewParams mirrors the legacy `wrkq tree [PATH]` traversal surface for one
// root path. Project-root scoping is the CALLER's responsibility: Path is already
// scoped before it reaches this method.
type TreeViewParams struct {
	Path            string `json:"path,omitempty"`
	MaxDepth        int    `json:"maxDepth,omitempty"`
	IncludeArchived bool   `json:"includeArchived,omitempty"`
	OpenOnly        bool   `json:"openOnly,omitempty"`
}

// WrkqTreeNode is the server-owned COMPATIBILITY projection of one tree node. Its
// exported-field/json-tag shape EXACTLY reproduces the legacy `treeNode` so the
// mirror CLI can render byte-identical `tree --json` output by marshaling these
// straight through. The two wire-only fields the legacy treeNode hides from JSON
// (`created_at` via json:"-", and the unexported parentTaskUUID) are carried here
// under distinct wire tags (wire_created_at / wire_parent_task_uuid) so the CLI
// can reconstruct the NDJSON stream + nesting without a second RPC; the CLI strips
// them before rendering the JSON mode. Not a canonical resource.
type WrkqTreeNode struct {
	Type                 string          `json:"type"`
	ID                   string          `json:"id"`
	Slug                 string          `json:"slug"`
	Title                string          `json:"title"`
	State                string          `json:"state,omitempty"`
	UUID                 string          `json:"uuid"`
	RequestedByProjectID *string         `json:"requested_by_project_id,omitempty"`
	AssignedProjectID    *string         `json:"assigned_project_id,omitempty"`
	AcknowledgedAt       *string         `json:"acknowledged_at,omitempty"`
	Resolution           *string         `json:"resolution,omitempty"`
	IsArchived           bool            `json:"is_archived"`
	IsDeleted            bool            `json:"is_deleted"`
	AllTasksCompleted    bool            `json:"all_tasks_completed,omitempty"`
	Children             []*WrkqTreeNode `json:"children,omitempty"`

	// Wire-only carriers (legacy hides these from `tree --json`). The CLI uses them
	// to build NDJSON stream entries + subtask nesting, then drops them for JSON.
	WireCreatedAt      string `json:"wire_created_at,omitempty"`
	WireParentTaskUUID string `json:"wire_parent_task_uuid,omitempty"`

	// hasVisibleContent / hiddenContainerCount are server-internal traversal state,
	// not part of the wire contract.
	hasVisibleTasks      bool
	hasVisibleContent    bool
	hiddenContainerCount int
}

// WrkqTreeView is the server-owned COMPATIBILITY tree projection for `wrkq tree`.
// It owns the full recursive traversal: container pruning, "all done" rollups,
// subtask nesting, and hidden-container counting. The CLI owns ONLY byte
// rendering (pretty/porcelain/json/ndjson). Not a canonical resource.
type WrkqTreeView struct {
	Path                         string          `json:"path"`
	ProjectID                    string          `json:"project_id,omitempty"`
	Children                     []*WrkqTreeNode `json:"children"`
	HiddenContainersNotDisplayed int             `json:"hidden_containers_not_displayed"`

	// WireRawPath carries the UN-normalized request path (empty for the root view)
	// so the CLI can reproduce legacy's NDJSON stream, which joins paths from the
	// RAW rootPath while the JSON `path` field shows the normalized "." display
	// form. Not part of the JSON `tree` projection (the CLI strips it).
	WireRawPath string `json:"wire_raw_path,omitempty"`
}

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
			if !includeArchived && (node.IsArchived || node.IsDeleted) {
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

		if !root.AllTasksCompleted || totalTasks == 0 {
			root.Children = append(root.Children, topTasks...)
		}
	}

	root.hasVisibleContent = root.hasVisibleContent || root.hasVisibleTasks || len(root.Children) > 0
	return root, nil
}

func treeAlwaysShow(node *WrkqTreeNode) bool {
	return node.Type == "container" && node.Slug == "inbox"
}

var _ = sql.ErrNoRows
