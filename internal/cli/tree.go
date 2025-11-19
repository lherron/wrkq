package cli

import (
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/spf13/cobra"
)

var treeCmd = &cobra.Command{
	Use:   "tree [PATH...]",
	Short: "Display containers and tasks in a tree structure",
	Long: `Display containers and tasks in a hierarchical tree structure.

Examples:
  todo tree                    # Show tree from root
  todo tree portal             # Show tree under portal
  todo tree -L 2               # Limit depth to 2 levels
  todo tree -a                 # Include archived items
`,
	RunE: runTree,
}

var (
	treeDepth       int
	treeIncludeArchived bool
	treeFields      string
	treePorcelain   bool
)

func init() {
	rootCmd.AddCommand(treeCmd)

	treeCmd.Flags().IntVarP(&treeDepth, "level", "L", 0, "Maximum depth to display (0 = unlimited)")
	treeCmd.Flags().BoolVarP(&treeIncludeArchived, "all", "a", false, "Include archived items")
	treeCmd.Flags().StringVar(&treeFields, "fields", "", "Fields to display (comma-separated)")
	treeCmd.Flags().BoolVar(&treePorcelain, "porcelain", false, "Machine-readable output")
}

func runTree(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Override DB path from flag if provided
	if dbPath := cmd.Flag("db").Value.String(); dbPath != "" {
		cfg.DBPath = dbPath
	}

	// Open database
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Determine root path
	rootPath := ""
	if len(args) > 0 {
		rootPath = args[0]
	}

	// Build and display tree
	return displayTree(database, rootPath, treeDepth, treeIncludeArchived, treePorcelain)
}

type treeNode struct {
	Type       string // "container" or "task"
	ID         string
	Slug       string
	Title      string
	State      string // for tasks
	UUID       string
	IsArchived bool
	Children   []*treeNode
}

func displayTree(database *db.DB, rootPath string, maxDepth int, includeArchived bool, porcelain bool) error {
	// Build tree structure
	root, err := buildTree(database, rootPath, maxDepth, includeArchived, 0)
	if err != nil {
		return err
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

		// Merge child's children into node
		node.Children = child.Children

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

		if !includeArchived {
			taskQuery += ` AND archived_at IS NULL`
		}

		taskQuery += ` ORDER BY slug`

		taskRows, err := database.Query(taskQuery, taskArgs...)
		if err != nil {
			return nil, fmt.Errorf("failed to query tasks: %w", err)
		}

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

			root.Children = append(root.Children, &node)
		}
		taskRows.Close()
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
