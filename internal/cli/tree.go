package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

// Subtle ANSI palette for the tree view. Scaffolding recedes (faint),
// containers anchor (bold blue, matching ls dir convention), and color is
// reserved for structure and task state rather than painting whole lines.
const (
	colDim       = "2"    // tree branches, IDs, secondary metadata
	colDir       = "1;34" // container slug — bold blue
	colStateOpen = "33"   // amber — active/needs attention
	colStateWIP  = "36"   // cyan — in progress
	colStateStop = "31"   // red — blocked
	colDone      = "32"   // green — completed / all done
)

// colorEnabled reports whether to emit ANSI styling. Honors NO_COLOR and only
// colors when stdout is an interactive terminal (clean output when piped).
var colorEnabled = func() bool {
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}()

// paint wraps s in an ANSI SGR sequence when color is enabled.
func paint(code, s string) string {
	if !colorEnabled || code == "" {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

// stateColor maps a task state to its subtle accent color.
func stateColor(state string) string {
	switch state {
	case "open":
		return colStateOpen
	case "in_progress":
		return colStateWIP
	case "blocked":
		return colStateStop
	case "completed":
		return colDone
	default: // draft and anything else recede
		return colDim
	}
}

var treeCmd = &cobra.Command{
	Use:   "tree [PATH...]",
	Short: "Display containers and tasks in a tree structure",
	Long: `Display containers and tasks in a hierarchical tree structure.

By default, archived and deleted containers are hidden, only draft/open tasks are shown,
and containers without visible task descendants are collapsed.
Use -a/--all to include archived and empty containers.
When all tasks in a container are completed/archived, they are collapsed
and an "(All done)" indicator is shown on the container.

Examples:
  wrkq tree                    # Show active containers and draft/open tasks
  wrkq tree --open             # Show only open tasks
  wrkq tree -a                 # Include archived and empty containers
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
)

func init() {
	rootCmd.AddCommand(treeCmd)

	treeCmd.Flags().IntVarP(&treeDepth, "level", "L", 0, "Maximum depth to display (0 = unlimited)")
	treeCmd.Flags().BoolVarP(&treeIncludeArchived, "all", "a", false, "Include archived and empty containers")
	treeCmd.Flags().BoolVar(&treeOpenOnly, "open", false, "Show only open tasks")
	treeCmd.Flags().StringVar(&treeFields, "fields", "", "Fields to display (comma-separated)")
	treeCmd.Flags().BoolVar(&treePorcelain, "porcelain", false, "Machine-readable output")
	treeCmd.Flags().BoolVar(&treeJSON, "json", false, "Output as JSON")
	treeCmd.Flags().BoolVar(&treeNDJSON, "ndjson", false, "Output as newline-delimited JSON")
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
	legacyPorcelain := treePorcelain && !treeJSON && !treeNDJSON && !outputFlagChanged
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
	HasVisibleTasks      bool        `json:"-"`
	HasVisibleContent    bool        `json:"-"`
	HiddenContainerCount int         `json:"-"`
	Children             []*treeNode `json:"children,omitempty"`

	parentTaskUUID string // for tasks: nests under the parent task when present
}

type treeOutput struct {
	Path                         string      `json:"path"`
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

	switch {
	case legacyPorcelain:
		return printTreeOutput(w, root, rootPath, true)
	case sel.Mode == outputModeJSON:
		return writeJSONOutput(w, sel, treeOutput{
			Path:                         outputPath,
			Children:                     root.Children,
			HiddenContainersNotDisplayed: root.HiddenContainerCount,
		})
	case sel.Mode == outputModeNDJSON:
		return writeNDJSONOutput(w, flattenTree(root, rootPath))
	default:
		return printTreeOutput(w, root, rootPath, false)
	}
}

func printTreeOutput(w io.Writer, root *treeNode, rootPath string, porcelain bool) error {
	// Print tree
	if rootPath == "" {
		fmt.Fprintln(w, ".")
	} else {
		fmt.Fprintln(w, rootPath)
	}

	printTree(w, root, "", true, porcelain)
	if root.HiddenContainerCount > 0 && !porcelain {
		fmt.Fprintln(w, paint(colDim, fmt.Sprintf("(plus %d empty containers not displayed; use --all to show empty containers)", root.HiddenContainerCount)))
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
			if !includeArchived && (node.IsArchived || node.IsDeleted) {
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
		if !root.AllTasksCompleted || totalTasks == 0 {
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
			connector = paint(colDim, connector)
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

		// Print children recursively
		if len(child.Children) > 0 {
			var newPrefix string
			if porcelain {
				newPrefix = prefix + "  "
			} else {
				if isLastChild {
					newPrefix = prefix + "    "
				} else {
					newPrefix = prefix + paint(colDim, "│") + "   "
				}
			}
			printTree(w, child, newPrefix, isLastChild, porcelain)
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
		parts = append(parts, paint(colDim, node.ID)) // ID recedes; title leads
		if node.Title != "" && node.Title != node.Slug {
			parts = append(parts, node.Title) // content stays plain
		}
		if node.State != "" {
			parts = append(parts, paint(stateColor(node.State), fmt.Sprintf("<%s>", formatTreeTaskState(node))))
		}
	} else {
		displayTitle := node.Title
		if node.Slug == "inbox" && strings.EqualFold(node.Title, "inbox") {
			displayTitle = "Inbox"
		}
		parts = append(parts, paint(colDir, node.Slug+"/")) // bold blue directory
		if displayTitle != node.Slug {
			parts = append(parts, paint(colDim, fmt.Sprintf("(%s)", displayTitle)))
		}
		parts = append(parts, paint(colDim, fmt.Sprintf("[%s]", node.ID)))
		if node.AllTasksCompleted {
			parts = append(parts, paint(colDone, "(All done)"))
		}
	}

	if node.IsArchived {
		parts = append(parts, paint(colDim, "(archived)"))
	}

	return strings.Join(parts, " ")
}

func formatTreeTaskState(node *treeNode) string {
	if node.State != "open" {
		return node.State
	}

	openedAge := formatTreeOpenedAge(node.CreatedAt, time.Now().UTC())
	if openedAge == "" {
		return node.State
	}
	return "opened " + openedAge + " ago"
}

func formatTreeOpenedAge(timestamp string, now time.Time) string {
	createdAt, ok := parseTreeTimestamp(timestamp)
	if !ok {
		return ""
	}

	elapsed := now.Sub(createdAt)
	if elapsed < 0 {
		elapsed = 0
	}

	return formatTreeDuration(elapsed)
}

func parseTreeTimestamp(timestamp string) (time.Time, bool) {
	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	} {
		parsed, err := time.Parse(layout, timestamp)
		if err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func formatTreeDuration(elapsed time.Duration) string {
	type durationUnit struct {
		name    string
		seconds int64
	}

	units := []durationUnit{
		{"year", 365 * 24 * 60 * 60},
		{"month", 30 * 24 * 60 * 60},
		{"week", 7 * 24 * 60 * 60},
		{"day", 24 * 60 * 60},
		{"hour", 60 * 60},
		{"minute", 60},
	}

	elapsedSeconds := int64(elapsed.Seconds())
	for _, unit := range units {
		if elapsedSeconds >= unit.seconds {
			value := elapsedSeconds / unit.seconds
			name := unit.name
			if value != 1 {
				name += "s"
			}
			return fmt.Sprintf("%d %s", value, name)
		}
	}
	return "less than a minute"
}
