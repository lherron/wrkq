package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/style"
	"github.com/spf13/cobra"
)

// ANSI palette, coloring, and relative-time helpers now live in internal/style,
// shared verbatim with the RPC mirror so both binaries render identically.

var treeCmd = &cobra.Command{
	Use:   "tree [PATH...]",
	Short: "Display containers and tasks in a tree structure",
	Long: `Display containers and tasks in a hierarchical tree structure.

By default, archived and deleted containers are hidden, only draft/open tasks are shown,
and containers without visible task descendants are collapsed.
Use -a/--all to include completed/archived/deleted tasks and empty containers.
When all tasks in a container are completed/archived, they are collapsed
and an "(All done)" indicator is shown on the container.

Examples:
  wrkq tree                    # Show active containers and draft/open tasks
  wrkq tree --open             # Show only open tasks
  wrkq tree -a                 # Include completed/archived/deleted tasks and empty containers
  wrkq tree portal             # Show tree under portal
  wrkq tree -L 2               # Limit depth to 2 levels
  wrkq tree --json             # Output as JSON
  wrkq tree --ndjson           # Output as newline-delimited JSON
`,
	RunE: appctx.WithApp(appctx.DefaultOptions(), runTree),
}

var (
	treeDepth           int
	treeIncludeArchived bool
	treeOpenOnly        bool
	treeFields          string
	treePorcelain       bool
	treeJSON            bool
	treeNDJSON          bool
	treePretty          bool
)

func init() {
	rootCmd.AddCommand(treeCmd)

	treeCmd.Flags().IntVarP(&treeDepth, "level", "L", 0, "Maximum depth to display (0 = unlimited)")
	treeCmd.Flags().BoolVarP(&treeIncludeArchived, "all", "a", false, "Include completed/archived/deleted tasks and empty containers")
	treeCmd.Flags().BoolVar(&treeOpenOnly, "open", false, "Show only open tasks")
	treeCmd.Flags().StringVar(&treeFields, "fields", "", "Fields to display (comma-separated)")
	treeCmd.Flags().BoolVar(&treePorcelain, "porcelain", false, "Machine-readable output")
	treeCmd.Flags().BoolVar(&treeJSON, "json", false, "Output as JSON")
	treeCmd.Flags().BoolVar(&treeNDJSON, "ndjson", false, "Output as newline-delimited JSON")
	treeCmd.Flags().BoolVar(&treePretty, "pretty", false, "Force human-readable tree output even when not a TTY")
}

func runTree(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	// Determine root path
	rootPath := ""
	if len(args) > 0 {
		rootPath = applyProjectRootToPath(app.Config, args[0], false)
	} else {
		rootPath = applyProjectRootToPath(app.Config, "", true)
	}

	// Preserve the original tab-separated tree porcelain format for explicit
	// `wrkq tree --porcelain`.
	outputFlagChanged := false
	if outputFlag := cmd.Flag("output"); outputFlag != nil {
		outputFlagChanged = outputFlag.Changed
	}
	legacyPorcelain := treePorcelain && !treePretty && !treeJSON && !treeNDJSON && !outputFlagChanged
	sel := outputSelection{Mode: outputModeRaw}
	if !legacyPorcelain {
		var err error
		sel, err = resolveOutputMode(cmd, app.Config, outputShapeList, outputResolveOptions{
			Allow:      []outputMode{outputModeTable, outputModeHuman, outputModeJSON, outputModeNDJSON},
			DefaultTTY: outputModeHuman,
		})
		if err != nil {
			return err
		}
		if treePretty {
			sel = outputSelection{Mode: outputModeHuman}
		}
	}

	// Build and display tree
	return displayTree(cmd.OutOrStdout(), database, rootPath, treeDepth, treeIncludeArchived, treeOpenOnly, sel, legacyPorcelain)
}

type treeNode struct {
	Type                 string      `json:"type"` // "container" or "task"
	ID                   string      `json:"id"`
	Slug                 string      `json:"slug"`
	Title                string      `json:"title"`
	State                string      `json:"state,omitempty"` // for tasks
	UUID                 string      `json:"uuid"`
	CreatedAt            string      `json:"-"`
	RequestedByProjectID *string     `json:"requested_by_project_id,omitempty"`
	AssignedProjectID    *string     `json:"assigned_project_id,omitempty"`
	AcknowledgedAt       *string     `json:"acknowledged_at,omitempty"`
	Resolution           *string     `json:"resolution,omitempty"`
	IsArchived           bool        `json:"is_archived"`
	IsDeleted            bool        `json:"is_deleted"`
	AllTasksCompleted    bool        `json:"all_tasks_completed,omitempty"` // for containers
	ExternalChildren     []*treeNode `json:"-"`
	ExternalBacklink     bool        `json:"-"`
	ExternalProjectID    string      `json:"-"`
	ExternalPath         string      `json:"-"`
	HasVisibleTasks      bool        `json:"-"`
	HasVisibleContent    bool        `json:"-"`
	HiddenContainerCount int         `json:"-"`
	Children             []*treeNode `json:"children,omitempty"`

	parentTaskUUID string // for tasks: nests under the parent task when present
}

type treeOutput struct {
	Path                         string      `json:"path"`
	ProjectID                    string      `json:"project_id,omitempty"`
	Children                     []*treeNode `json:"children"`
	HiddenContainersNotDisplayed int         `json:"hidden_containers_not_displayed"`
}

type treeStreamEntry struct {
	Type                 string  `json:"type"`
	ID                   string  `json:"id"`
	Slug                 string  `json:"slug"`
	Title                string  `json:"title"`
	Path                 string  `json:"path"`
	Depth                int     `json:"depth"`
	ParentID             *string `json:"parent_id,omitempty"`
	ParentPath           *string `json:"parent_path,omitempty"`
	State                string  `json:"state,omitempty"`
	OpenedAt             *string `json:"opened_at,omitempty"`
	CreatedAt            string  `json:"created_at,omitempty"`
	UUID                 string  `json:"uuid"`
	RequestedByProjectID *string `json:"requested_by_project_id,omitempty"`
	AssignedProjectID    *string `json:"assigned_project_id,omitempty"`
	AcknowledgedAt       *string `json:"acknowledged_at,omitempty"`
	Resolution           *string `json:"resolution,omitempty"`
	IsArchived           bool    `json:"is_archived"`
	IsDeleted            bool    `json:"is_deleted"`
	AllTasksCompleted    bool    `json:"all_tasks_completed,omitempty"`
}

func displayTree(w io.Writer, database *db.DB, rootPath string, maxDepth int, includeArchived bool, openOnly bool, sel outputSelection, legacyPorcelain bool) error {
	// Build tree structure
	root, err := buildTree(database, rootPath, maxDepth, includeArchived, openOnly, !includeArchived, 0)
	if err != nil {
		return err
	}

	outputPath := rootPath
	if outputPath == "" {
		outputPath = "."
	}

	// Resolve the friendly ID of the top-level parent project so the tree header
	// can anchor the view to a concrete project (e.g. `wrkq [P-00060]`).
	projectID := resolveTopLevelProjectID(database, rootPath)

	switch {
	case legacyPorcelain:
		return printTreeOutput(w, root, rootPath, projectID, true)
	case sel.Mode == outputModeJSON:
		return writeJSONOutput(w, sel, treeOutput{
			Path:                         outputPath,
			ProjectID:                    projectID,
			Children:                     root.Children,
			HiddenContainersNotDisplayed: root.HiddenContainerCount,
		})
	case sel.Mode == outputModeNDJSON:
		return writeNDJSONOutput(w, flattenTree(root, rootPath))
	default:
		if err := attachExternalTreeBacklinks(database, root, includeArchived, openOnly); err != nil {
			return err
		}
		return printTreeOutput(w, root, rootPath, projectID, false)
	}
}

// resolveTopLevelProjectID returns the friendly ID (e.g. "P-00060") of the
// top-level project that owns rootPath. The top-level project is the first
// path segment; subpaths still resolve to their owning project. Returns "" for
// the multi-project root view or when the project can't be resolved.
func resolveTopLevelProjectID(database *db.DB, rootPath string) string {
	segments := paths.SplitPath(rootPath)
	if len(segments) == 0 {
		return ""
	}
	_, friendlyID, err := selectors.WalkContainerPath(database, segments[0])
	if err != nil {
		return ""
	}
	return friendlyID
}

func printTreeOutput(w io.Writer, root *treeNode, rootPath, projectID string, porcelain bool) error {
	// Print tree header. The top-level parent project's ID is annotated on the
	// header in the pretty view so the tree is anchored to a concrete project.
	header := rootPath
	if header == "" {
		header = "."
	}
	if projectID != "" && !porcelain {
		header += " " + style.Paint(style.ColDim, fmt.Sprintf("[%s]", projectID))
	}
	fmt.Fprintln(w, header)

	printTree(w, root, "", true, porcelain)
	if root.HiddenContainerCount > 0 && !porcelain {
		fmt.Fprintln(w, style.Paint(style.ColDim, fmt.Sprintf("(plus %d empty containers not displayed; use --all to show empty containers)", root.HiddenContainerCount)))
	}
	return nil
}

func buildTree(database *db.DB, path string, maxDepth int, includeArchived bool, openOnly bool, pruneEmptyContainers bool, currentDepth int) (*treeNode, error) {
	root := &treeNode{
		Type:     "container",
		Slug:     path,
		Children: make([]*treeNode, 0),
	}

	// Check depth limit
	if maxDepth > 0 && currentDepth >= maxDepth {
		return root, nil
	}

	// Determine parent UUID using shared resolver
	var parentUUID *string
	if path != "" {
		uuid, _, err := selectors.WalkContainerPath(database, path)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve path %q: %w", path, err)
		}
		parentUUID = &uuid
	}

	// Query child containers
	containerQuery := `
		SELECT uuid, id, slug, COALESCE(title, slug) as title, created_at, archived_at
		FROM containers
		WHERE `
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

		err := rows.Scan(&node.UUID, &node.ID, &node.Slug, &node.Title, &node.CreatedAt, &archivedAt)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("failed to scan container: %w", err)
		}

		node.Type = "container"
		node.IsArchived = archivedAt != nil

		// Recursively build children
		childPath := path
		if childPath != "" {
			childPath += "/"
		}
		childPath += node.Slug

		child, err := buildTree(database, childPath, maxDepth, includeArchived, openOnly, pruneEmptyContainers, currentDepth+1)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}

		// Merge child's children and metadata into node
		node.Children = child.Children
		node.AllTasksCompleted = child.AllTasksCompleted
		node.HasVisibleTasks = child.HasVisibleTasks
		node.HasVisibleContent = child.HasVisibleContent
		node.HiddenContainerCount = child.HiddenContainerCount

		shouldShowContainer := !pruneEmptyContainers || node.HasVisibleContent || alwaysShowTreeContainer(&node)
		if shouldShowContainer {
			root.Children = append(root.Children, &node)
			root.HasVisibleContent = true
			root.HiddenContainerCount += node.HiddenContainerCount
		} else {
			root.HiddenContainerCount += 1 + node.HiddenContainerCount
		}
	}
	_ = rows.Close()

	// Query tasks at this level
	if parentUUID != nil || path == "" {
		taskQuery := `
			SELECT uuid, id, slug, title, state, created_at, archived_at, deleted_at,
			       requested_by_project_id, assigned_project_id, acknowledged_at, resolution,
			       parent_task_uuid
			FROM tasks
			WHERE `
		var taskArgs []interface{}

		if parentUUID == nil {
			// This shouldn't happen in normal use, but handle it
			return root, nil
		}

		taskQuery += `project_uuid = ?`
		taskArgs = append(taskArgs, *parentUUID)

		// Always query all tasks to check if all are completed.
		// Order oldest to newest within the container (id is monotonic,
		// zero-padded, assigned in creation order; created_at as primary key).
		taskQuery += ` ORDER BY created_at ASC, id ASC`

		taskRows, err := database.Query(taskQuery, taskArgs...)
		if err != nil {
			return nil, fmt.Errorf("failed to query tasks: %w", err)
		}

		var tasks []*treeNode
		totalTasks := 0
		closedTasks := 0

		for taskRows.Next() {
			var node treeNode
			var archivedAt, deletedAt *string
			var requestedBy, assignedProject, acknowledgedAt, resolution, parentTaskUUID *string

			err := taskRows.Scan(&node.UUID, &node.ID, &node.Slug, &node.Title, &node.State, &node.CreatedAt, &archivedAt, &deletedAt,
				&requestedBy, &assignedProject, &acknowledgedAt, &resolution, &parentTaskUUID)
			if err != nil {
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
			isClosed := node.IsArchived || node.IsDeleted || node.State == "completed"
			if isClosed {
				closedTasks++
			}

			// Determine if task should be shown based on filters.
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

		// Recursively check if all children (containers + tasks) are "done"
		// A container is "all done" if:
		// 1. All direct tasks are completed/archived (or no direct tasks)
		// 2. All child containers have AllTasksCompleted = true (or no child containers)
		// This means empty containers are considered "all done"

		allDirectTasksClosed := totalTasks == 0 || (totalTasks > 0 && closedTasks == totalTasks)
		allChildContainersDone := true

		// Check child containers
		for _, child := range root.Children {
			if child.Type == "container" {
				// If any child container isn't all done, this container isn't all done
				if !child.AllTasksCompleted {
					allChildContainersDone = false
					break
				}
			}
		}

		// Set AllTasksCompleted: true if all tasks (if any) are closed and all child containers are done
		root.AllTasksCompleted = allDirectTasksClosed && allChildContainersDone

		// Nest visible subtasks under their parent task. A task whose parent is
		// also visible is attached to that parent's children (recursively, since
		// we key on the actual node pointers); tasks whose parent is absent from
		// the visible set surface at the container's top level so they aren't lost.
		byUUID := make(map[string]*treeNode, len(tasks))
		for _, t := range tasks {
			byUUID[t.UUID] = t
		}
		topTasks := make([]*treeNode, 0, len(tasks))
		for _, t := range tasks {
			if parent, ok := byUUID[t.parentTaskUUID]; ok && t.parentTaskUUID != "" {
				parent.Children = append(parent.Children, t)
				continue
			}
			topTasks = append(topTasks, t)
		}

		// If all tasks are completed (and all child containers are done), don't add tasks to the tree
		// Otherwise, add the tasks we collected
		if includeArchived || !root.AllTasksCompleted || totalTasks == 0 {
			root.Children = append(root.Children, topTasks...)
		}
	}

	root.HasVisibleContent = root.HasVisibleContent || root.HasVisibleTasks || len(root.Children) > 0
	return root, nil
}

func alwaysShowTreeContainer(node *treeNode) bool {
	return node.Type == "container" && node.Slug == "inbox"
}

func printTree(w io.Writer, node *treeNode, prefix string, isLast bool, porcelain bool) {
	for i, child := range node.Children {
		isLastChild := i == len(node.Children)-1

		// Print current node
		var connector string
		if porcelain {
			connector = ""
		} else {
			if isLastChild {
				connector = "└── "
			} else {
				connector = "├── "
			}
			connector = style.Paint(style.ColDim, connector)
		}

		// Format node display
		display := formatNodeDisplay(child, porcelain)

		if porcelain {
			// Porcelain: tab-separated values
			fmt.Fprintf(w, "%s%s\t%s\t%s\t%s\n", prefix, child.Type, child.ID, child.Slug, child.Title)
		} else {
			// Pretty tree
			fmt.Fprintf(w, "%s%s%s\n", prefix, connector, display)
		}

		// Print resident children recursively.
		if len(child.Children) > 0 {
			var newPrefix string
			if porcelain {
				newPrefix = prefix + "  "
			} else {
				if isLastChild {
					newPrefix = prefix + "    "
				} else {
					newPrefix = prefix + style.Paint(style.ColDim, "│") + "   "
				}
			}
			printTree(w, child, newPrefix, isLastChild, porcelain)
		}
		if !porcelain && len(child.ExternalChildren) > 0 {
			newPrefix := prefix + style.Paint(style.ColDim, "│") + "   "
			if isLastChild {
				newPrefix = prefix + "    "
			}
			printTree(w, &treeNode{Children: child.ExternalChildren}, newPrefix, true, false)
		}
	}
}

func flattenTree(root *treeNode, rootPath string) []treeStreamEntry {
	var entries []treeStreamEntry
	var walk func(nodes []*treeNode, parentID *string, parentPath string, depth int)
	walk = func(nodes []*treeNode, parentID *string, parentPath string, depth int) {
		for _, node := range nodes {
			path := joinTreePath(parentPath, node.Slug)
			entry := treeStreamEntry{
				Type:                 node.Type,
				ID:                   node.ID,
				Slug:                 node.Slug,
				Title:                node.Title,
				Path:                 path,
				Depth:                depth,
				State:                node.State,
				CreatedAt:            node.CreatedAt,
				UUID:                 node.UUID,
				RequestedByProjectID: node.RequestedByProjectID,
				AssignedProjectID:    node.AssignedProjectID,
				AcknowledgedAt:       node.AcknowledgedAt,
				Resolution:           node.Resolution,
				IsArchived:           node.IsArchived,
				IsDeleted:            node.IsDeleted,
				AllTasksCompleted:    node.AllTasksCompleted,
			}
			if parentID != nil {
				entry.ParentID = parentID
			}
			if parentPath != "" {
				parentPathCopy := parentPath
				entry.ParentPath = &parentPathCopy
			}
			if node.Type == "task" && node.State == "open" && node.CreatedAt != "" {
				openedAt := node.CreatedAt
				entry.OpenedAt = &openedAt
			}
			entries = append(entries, entry)

			nodeID := node.ID
			walk(node.Children, &nodeID, path, depth+1)
		}
	}

	walk(root.Children, nil, rootPath, 0)
	return entries
}

func joinTreePath(parentPath, slug string) string {
	if parentPath == "" || parentPath == "." {
		return slug
	}
	return parentPath + "/" + slug
}

func formatNodeDisplay(node *treeNode, porcelain bool) string {
	if porcelain {
		return fmt.Sprintf("%s\t%s\t%s", node.ID, node.Slug, node.Title)
	}

	// Pretty display
	var parts []string

	if node.Type == "task" {
		parts = append(parts, style.Paint(style.ColDim, node.ID)) // ID recedes; title leads
		if node.Title != "" && node.Title != node.Slug {
			parts = append(parts, node.Title) // content stays plain
		}
		if node.State != "" {
			parts = append(parts, style.Paint(style.StateColor(node.State), fmt.Sprintf("<%s>", formatTreeTaskState(node))))
		}
	} else {
		displayTitle := node.Title
		if node.Slug == "inbox" && strings.EqualFold(node.Title, "inbox") {
			displayTitle = "Inbox"
		}
		parts = append(parts, style.Paint(style.ColDir, node.Slug+"/")) // bold blue directory
		if displayTitle != node.Slug {
			parts = append(parts, style.Paint(style.ColDim, fmt.Sprintf("(%s)", displayTitle)))
		}
		parts = append(parts, style.Paint(style.ColDim, fmt.Sprintf("[%s]", node.ID)))
		if node.AllTasksCompleted {
			parts = append(parts, style.Paint(style.ColDone, "(All done)"))
		}
	}

	if node.IsArchived {
		parts = append(parts, style.Paint(style.ColDim, "(archived)"))
	}
	if node.ExternalBacklink {
		context := strings.TrimSpace(node.ExternalPath)
		if context == "" {
			context = node.ExternalProjectID
		} else if node.ExternalProjectID != "" {
			context += " " + node.ExternalProjectID
		}
		if context != "" {
			parts = append(parts, style.Paint(style.ColDim, fmt.Sprintf("(external: %s)", context)))
		} else {
			parts = append(parts, style.Paint(style.ColDim, "(external)"))
		}
	}

	return strings.Join(parts, " ")
}

func formatTreeTaskState(node *treeNode) string {
	if node.State != "open" {
		return node.State
	}

	openedAge := style.FormatOpenedAge(node.CreatedAt)
	if openedAge == "" {
		return node.State
	}
	return "opened " + openedAge + " ago"
}

func attachExternalTreeBacklinks(database *db.DB, root *treeNode, includeArchived bool, openOnly bool) error {
	var walk func(nodes []*treeNode) error
	walk = func(nodes []*treeNode) error {
		for _, node := range nodes {
			if node.Type == "task" {
				children, err := loadExternalTreeBacklinks(database, node.UUID, includeArchived, openOnly)
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

func loadExternalTreeBacklinks(database *db.DB, parentUUID string, includeArchived bool, openOnly bool) ([]*treeNode, error) {
	rows, err := database.Query(`
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
		return nil, fmt.Errorf("failed to query external child tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []*treeNode{}
	for rows.Next() {
		var node treeNode
		var archivedAt, deletedAt *string
		var requestedBy, assignedProject, acknowledgedAt, resolution *string
		if err := rows.Scan(&node.UUID, &node.ID, &node.Slug, &node.Title, &node.State, &node.CreatedAt, &archivedAt, &deletedAt,
			&requestedBy, &assignedProject, &acknowledgedAt, &resolution, &node.ExternalProjectID, &node.ExternalPath); err != nil {
			return nil, fmt.Errorf("failed to scan external child task: %w", err)
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
		return nil, fmt.Errorf("failed to iterate external child tasks: %w", err)
	}
	return out, nil
}
