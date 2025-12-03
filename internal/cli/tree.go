package cli

import (
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

var treeCmd = &cobra.Command{
	Use:   "tree [PATH...]",
	Short: "Display containers and tasks in a tree structure",
	Long: `Display containers and tasks in a hierarchical tree structure.

By default, all items including completed and archived are shown. When all tasks
in a container are completed/archived, they are collapsed and an "(All done)"
indicator is shown on the container.

Examples:
  wrkq tree                    # Show tree from root
  wrkq tree portal             # Show tree under portal
  wrkq tree -L 2               # Limit depth to 2 levels
  wrkq tree --json             # Output as JSON
`,
	RunE: appctx.WithApp(appctx.DefaultOptions(), runTree),
}

var (
	treeDepth           int
	treeIncludeArchived bool
	treeFields          string
	treePorcelain       bool
	treeJSON            bool
)

func init() {
	rootCmd.AddCommand(treeCmd)

	treeCmd.Flags().IntVarP(&treeDepth, "level", "L", 0, "Maximum depth to display (0 = unlimited)")
	treeCmd.Flags().BoolVarP(&treeIncludeArchived, "all", "a", true, "Include completed and archived items (default true)")
	treeCmd.Flags().StringVar(&treeFields, "fields", "", "Fields to display (comma-separated)")
	treeCmd.Flags().BoolVar(&treePorcelain, "porcelain", false, "Machine-readable output")
	treeCmd.Flags().BoolVar(&treeJSON, "json", false, "Output as JSON")
}

func runTree(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	// Determine root path
	rootPath := ""
	if len(args) > 0 {
		rootPath = args[0]
	}

	// Build and display tree
	return displayTree(database, rootPath, treeDepth, treeIncludeArchived, treePorcelain, treeJSON)
}

type treeNode struct {
	Type              string      `json:"type"`       // "container" or "task"
	ID                string      `json:"id"`
	Slug              string      `json:"slug"`
	Title             string      `json:"title"`
	State             string      `json:"state,omitempty"` // for tasks
	UUID              string      `json:"uuid"`
	IsArchived        bool        `json:"is_archived"`
	AllTasksCompleted bool        `json:"all_tasks_completed,omitempty"` // for containers
	Children          []*treeNode `json:"children,omitempty"`
}

func displayTree(database *db.DB, rootPath string, maxDepth int, includeArchived bool, porcelain bool, jsonOutput bool) error {
	// Build tree structure
	root, err := buildTree(database, rootPath, maxDepth, includeArchived, 0)
	if err != nil {
		return err
	}

	// Handle JSON output
	if jsonOutput {
		// Create a wrapper structure with metadata
		output := map[string]interface{}{
			"path":     rootPath,
			"children": root.Children,
		}
		if rootPath == "" {
			output["path"] = "."
		}
		return render.RenderJSON(output, false)
	}

	// Print tree
	if rootPath == "" {
		fmt.Println(".")
	} else {
		fmt.Println(rootPath)
	}

	printTree(root, "", true, porcelain)
	return nil
}

func buildTree(database *db.DB, path string, maxDepth int, includeArchived bool, currentDepth int) (*treeNode, error) {
	root := &treeNode{
		Type:     "container",
		Slug:     path,
		Children: make([]*treeNode, 0),
	}

	// Check depth limit
	if maxDepth > 0 && currentDepth >= maxDepth {
		return root, nil
	}

	// Determine parent UUID
	var parentUUID *string
	if path != "" {
		// Resolve path to container UUID
		uuid, err := resolveContainerPath(database, path)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve path %q: %w", path, err)
		}
		parentUUID = &uuid
	}

	// Query child containers
	containerQuery := `
		SELECT uuid, id, slug, COALESCE(title, slug) as title, archived_at
		FROM containers
		WHERE `
	var containerArgs []interface{}

	if parentUUID == nil {
		containerQuery += `parent_uuid IS NULL`
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

		err := rows.Scan(&node.UUID, &node.ID, &node.Slug, &node.Title, &archivedAt)
		if err != nil {
			rows.Close()
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

		child, err := buildTree(database, childPath, maxDepth, includeArchived, currentDepth+1)
		if err != nil {
			rows.Close()
			return nil, err
		}

		// Merge child's children and metadata into node
		node.Children = child.Children
		node.AllTasksCompleted = child.AllTasksCompleted

		root.Children = append(root.Children, &node)
	}
	rows.Close()

	// Query tasks at this level
	if parentUUID != nil || path == "" {
		taskQuery := `
			SELECT uuid, id, slug, title, state, archived_at
			FROM tasks
			WHERE `
		var taskArgs []interface{}

		if parentUUID == nil {
			// This shouldn't happen in normal use, but handle it
			return root, nil
		}

		taskQuery += `project_uuid = ?`
		taskArgs = append(taskArgs, *parentUUID)

		// Always query all tasks to check if all are completed
		taskQuery += ` ORDER BY slug`

		taskRows, err := database.Query(taskQuery, taskArgs...)
		if err != nil {
			return nil, fmt.Errorf("failed to query tasks: %w", err)
		}

		var tasks []*treeNode
		totalTasks := 0
		closedTasks := 0

		for taskRows.Next() {
			var node treeNode
			var archivedAt *string

			err := taskRows.Scan(&node.UUID, &node.ID, &node.Slug, &node.Title, &node.State, &archivedAt)
			if err != nil {
				taskRows.Close()
				return nil, fmt.Errorf("failed to scan task: %w", err)
			}

			node.Type = "task"
			node.IsArchived = archivedAt != nil
			node.Children = make([]*treeNode, 0)

			totalTasks++
			isClosed := node.IsArchived || node.State == "completed"
			if isClosed {
				closedTasks++
			}

			// Only add to tasks slice if we should include it based on includeArchived flag
			if includeArchived || !isClosed {
				tasks = append(tasks, &node)
			}
		}
		taskRows.Close()

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

		// If all tasks are completed (and all child containers are done), don't add tasks to the tree
		// Otherwise, add the tasks we collected
		if !root.AllTasksCompleted || totalTasks == 0 {
			for _, task := range tasks {
				root.Children = append(root.Children, task)
			}
		}
	}

	return root, nil
}

func printTree(node *treeNode, prefix string, isLast bool, porcelain bool) {
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
		}

		// Format node display
		display := formatNodeDisplay(child, porcelain)

		if porcelain {
			// Porcelain: tab-separated values
			fmt.Printf("%s%s\t%s\t%s\t%s\n", prefix, child.Type, child.ID, child.Slug, child.Title)
		} else {
			// Pretty tree
			fmt.Printf("%s%s%s\n", prefix, connector, display)
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
					newPrefix = prefix + "│   "
				}
			}
			printTree(child, newPrefix, isLastChild, porcelain)
		}
	}
}

func formatNodeDisplay(node *treeNode, porcelain bool) string {
	if porcelain {
		return fmt.Sprintf("%s\t%s\t%s", node.ID, node.Slug, node.Title)
	}

	// Pretty display
	var parts []string

	if node.Type == "task" {
		parts = append(parts, fmt.Sprintf("\033[1m%s\033[0m", node.Slug)) // Bold task slug
		if node.Title != node.Slug {
			parts = append(parts, fmt.Sprintf("(%s)", node.Title))
		}
		parts = append(parts, fmt.Sprintf("[%s]", node.ID))
		if node.State != "" {
			parts = append(parts, fmt.Sprintf("<%s>", node.State))
		}
	} else {
		parts = append(parts, fmt.Sprintf("\033[34m%s/\033[0m", node.Slug)) // Blue directory
		if node.Title != node.Slug {
			parts = append(parts, fmt.Sprintf("(%s)", node.Title))
		}
		parts = append(parts, fmt.Sprintf("[%s]", node.ID))
		if node.AllTasksCompleted {
			parts = append(parts, "\033[32m(All done)\033[0m") // Green "All done"
		}
	}

	if node.IsArchived {
		parts = append(parts, "\033[2m(archived)\033[0m")
	}

	return strings.Join(parts, " ")
}

func resolveContainerPath(database *db.DB, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty path")
	}

	segments := strings.Split(path, "/")
	var parentUUID *string

	for _, segment := range segments {
		query := `SELECT uuid FROM containers WHERE slug = ? AND `
		args := []interface{}{segment}

		if parentUUID == nil {
			query += `parent_uuid IS NULL`
		} else {
			query += `parent_uuid = ?`
			args = append(args, *parentUUID)
		}

		var uuid string
		err := database.QueryRow(query, args...).Scan(&uuid)
		if err != nil {
			return "", fmt.Errorf("segment %q not found: %w", segment, err)
		}

		parentUUID = &uuid
	}

	if parentUUID == nil {
		return "", fmt.Errorf("failed to resolve path")
	}

	return *parentUUID, nil
}
