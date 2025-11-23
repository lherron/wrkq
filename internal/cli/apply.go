package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/edit"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var applyCmd = &cobra.Command{
	Use:   "apply [<PATHSPEC|ID>] [-]",
	Short: "Apply changes to a task from file or stdin",
	Long: `Apply full task document from file or stdin.

Accepts:
- Markdown with YAML front matter
- YAML
- JSON

Examples:
  todo apply T-00001 task.md        # Apply from file
  todo apply T-00001 -              # Apply from stdin
  todo cat T-00001 | sed 's/foo/bar/' | todo apply T-00001 -
  todo apply T-00001 --body-only    # Update only body
  todo apply T-00001 --if-match 5   # Conditional update
  todo apply T-00001 new.md --base old.md --if-match 47  # 3-way merge
`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runApply,
}

var (
	applyFormat   string
	applyBodyOnly bool
	applyIfMatch  int64
	applyDryRun   bool
	applyBase     string
)

func init() {
	rootCmd.AddCommand(applyCmd)

	applyCmd.Flags().StringVar(&applyFormat, "format", "", "Input format: md, yaml, json (auto-detected if not specified)")
	applyCmd.Flags().BoolVar(&applyBodyOnly, "body-only", false, "Update only body without touching metadata")
	applyCmd.Flags().Int64Var(&applyIfMatch, "if-match", 0, "Only apply if etag matches (0 = no check)")
	applyCmd.Flags().BoolVar(&applyDryRun, "dry-run", false, "Show what would be changed without applying")
	applyCmd.Flags().StringVar(&applyBase, "base", "", "Base document for 3-way merge (enables merge mode)")
}

func runApply(cmd *cobra.Command, args []string) error {
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

	// Resolve task
	taskID := args[0]
	taskUUID, friendlyID, err := selectors.ResolveTask(database, taskID)
	if err != nil {
		return fmt.Errorf("failed to resolve task: %w", err)
	}

	// Determine input source
	var input io.Reader
	if len(args) == 2 && args[1] == "-" {
		input = os.Stdin
	} else if len(args) == 2 {
		file, err := os.Open(args[1])
		if err != nil {
			return fmt.Errorf("failed to open file: %w", err)
		}
		defer file.Close()
		input = file
	} else {
		return fmt.Errorf("missing input source (file path or -)")
	}

	// Read input
	data, err := io.ReadAll(input)
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}

	// Parse input based on format
	var updates applyUpdates
	format := applyFormat
	if format == "" {
		// Auto-detect format
		format = detectFormat(data)
	}

	switch format {
	case "json":
		err = parseJSON(data, &updates)
	case "yaml", "yml":
		err = parseYAML(data, &updates)
	case "md", "markdown":
		err = parseMarkdown(data, &updates)
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}

	if err != nil {
		return fmt.Errorf("failed to parse input: %w", err)
	}

	// Fetch current task state
	currentTask, err := fetchTaskData(database, taskUUID)
	if err != nil {
		return fmt.Errorf("failed to fetch current task: %w", err)
	}

	// Check etag if --if-match is specified
	if applyIfMatch > 0 && currentTask.ETag != applyIfMatch {
		return fmt.Errorf("etag mismatch: expected %d, got %d", applyIfMatch, currentTask.ETag)
	}

	// If --base flag is provided, perform 3-way merge
	if applyBase != "" {
		return runApplyWithMerge(cmd, database, taskUUID, friendlyID, updates, currentTask, data)
	}

	// Apply updates (non-merge mode)
	if applyDryRun {
		fmt.Printf("Would update task %s (%s)\n", friendlyID, taskUUID)
		if applyBodyOnly {
			fmt.Printf("  body: %s\n", stringOrNil(updates.Body))
		} else {
			if updates.Title != nil {
				fmt.Printf("  title: %s\n", *updates.Title)
			}
			if updates.State != nil {
				fmt.Printf("  state: %s\n", *updates.State)
			}
			if updates.Priority != nil {
				fmt.Printf("  priority: %d\n", *updates.Priority)
			}
			if updates.DueAt != nil {
				fmt.Printf("  due_at: %s\n", *updates.DueAt)
			}
			if updates.Body != nil {
				fmt.Printf("  body: %s\n", *updates.Body)
			}
		}
		return nil
	}

	// Execute update
	return applyTaskUpdates(database, taskUUID, updates, applyBodyOnly)
}

type applyUpdates struct {
	Title    *string `json:"title,omitempty" yaml:"title,omitempty"`
	State    *string `json:"state,omitempty" yaml:"state,omitempty"`
	Priority *int    `json:"priority,omitempty" yaml:"priority,omitempty"`
	DueAt    *string `json:"due_at,omitempty" yaml:"due_at,omitempty"`
	Body     *string `json:"body,omitempty" yaml:"body,omitempty"`
}

func detectFormat(data []byte) string {
	text := string(data)

	// Check for JSON
	if strings.HasPrefix(strings.TrimSpace(text), "{") {
		return "json"
	}

	// Check for markdown front matter
	if strings.HasPrefix(text, "---\n") {
		return "md"
	}

	// Default to YAML
	return "yaml"
}

func parseJSON(data []byte, updates *applyUpdates) error {
	return json.Unmarshal(data, updates)
}

func parseYAML(data []byte, updates *applyUpdates) error {
	return yaml.Unmarshal(data, updates)
}

func parseMarkdown(data []byte, updates *applyUpdates) error {
	text := string(data)

	// Split front matter and body
	if !strings.HasPrefix(text, "---\n") {
		// No front matter, treat entire content as body
		updates.Body = &text
		return nil
	}

	parts := strings.SplitN(text[4:], "\n---\n", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid markdown front matter")
	}

	frontMatter := parts[0]
	body := strings.TrimSpace(parts[1])

	// Parse front matter as YAML
	err := yaml.Unmarshal([]byte(frontMatter), updates)
	if err != nil {
		return fmt.Errorf("failed to parse front matter: %w", err)
	}

	// Set body
	if body != "" {
		updates.Body = &body
	}

	return nil
}

func applyTaskUpdates(database *db.DB, taskUUID string, updates applyUpdates, bodyOnly bool) error {
	// Build update query
	var setClauses []string
	var args []interface{}

	if bodyOnly {
		// Only update body
		if updates.Body != nil {
			setClauses = append(setClauses, "body = ?")
			args = append(args, *updates.Body)
		} else {
			return fmt.Errorf("no body provided in --body-only mode")
		}
	} else {
		// Update all provided fields
		if updates.Title != nil {
			setClauses = append(setClauses, "title = ?")
			args = append(args, *updates.Title)
		}
		if updates.State != nil {
			setClauses = append(setClauses, "state = ?")
			args = append(args, *updates.State)
		}
		if updates.Priority != nil {
			setClauses = append(setClauses, "priority = ?")
			args = append(args, *updates.Priority)
		}
		if updates.DueAt != nil {
			setClauses = append(setClauses, "due_at = ?")
			args = append(args, *updates.DueAt)
		}
		if updates.Body != nil {
			setClauses = append(setClauses, "body = ?")
			args = append(args, *updates.Body)
		}
	}

	if len(setClauses) == 0 {
		return fmt.Errorf("no fields to update")
	}

	// Add etag increment and updated_at
	setClauses = append(setClauses, "etag = etag + 1")
	setClauses = append(setClauses, "updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')")

	// Build and execute query
	query := fmt.Sprintf("UPDATE tasks SET %s WHERE uuid = ?", strings.Join(setClauses, ", "))
	args = append(args, taskUUID)

	_, err := database.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to update task: %w", err)
	}

	fmt.Printf("Updated task %s\n", taskUUID)
	return nil
}

func stringOrNil(s *string) string {
	if s == nil {
		return "(nil)"
	}
	return *s
}

// runApplyWithMerge performs a 3-way merge when --base is specified
func runApplyWithMerge(cmd *cobra.Command, database *db.DB, taskUUID string, friendlyID string, editedUpdates applyUpdates, currentTask *taskData, editedData []byte) error {
	// Read base document from file
	baseData, err := os.ReadFile(applyBase)
	if err != nil {
		return fmt.Errorf("failed to read base file: %w", err)
	}

	// Parse base document
	var baseUpdates applyUpdates
	baseFormat := detectFormat(baseData)
	switch baseFormat {
	case "json":
		err = parseJSON(baseData, &baseUpdates)
	case "yaml", "yml":
		err = parseYAML(baseData, &baseUpdates)
	case "md", "markdown":
		err = parseMarkdown(baseData, &baseUpdates)
	default:
		return fmt.Errorf("unsupported base format: %s", baseFormat)
	}

	if err != nil {
		return fmt.Errorf("failed to parse base document: %w", err)
	}

	// Convert to edit.TaskDocument for merge
	baseDoc := applyUpdatesToTaskDocument(baseUpdates, currentTask)
	currentDoc := taskDataToDocument(currentTask)
	editedDoc := applyUpdatesToTaskDocument(editedUpdates, currentTask)

	// Perform 3-way merge
	mergeResult := edit.Merge3Way(baseDoc, currentDoc, editedDoc)

	// Check for conflicts
	if mergeResult.HasConflict {
		fmt.Fprintf(os.Stderr, "%s", mergeResult.FormatConflicts())
		os.Exit(4) // Conflict exit code
	}

	// Convert merged result back to applyUpdates
	mergedUpdates := applyUpdates{
		Title:    &mergeResult.Merged.Title,
		State:    &mergeResult.Merged.State,
		Priority: &mergeResult.Merged.Priority,
		DueAt:    stringPtr(mergeResult.Merged.DueAt),
		Body:     &mergeResult.Merged.Body,
	}

	// Apply merged updates
	if applyDryRun {
		fmt.Printf("Would update task %s (%s) after 3-way merge\n", friendlyID, taskUUID)
		fmt.Printf("  title: %s\n", *mergedUpdates.Title)
		fmt.Printf("  state: %s\n", *mergedUpdates.State)
		fmt.Printf("  priority: %d\n", *mergedUpdates.Priority)
		if mergedUpdates.DueAt != nil {
			fmt.Printf("  due_at: %s\n", *mergedUpdates.DueAt)
		}
		fmt.Printf("  body: %s\n", *mergedUpdates.Body)
		return nil
	}

	err = applyTaskUpdates(database, taskUUID, mergedUpdates, false)
	if err != nil {
		return fmt.Errorf("failed to apply merged updates: %w", err)
	}

	fmt.Printf("Updated task %s (3-way merge successful)\n", friendlyID)
	return nil
}

// applyUpdatesToTaskDocument converts applyUpdates to edit.TaskDocument
// Uses fallback values from currentTask if fields are not set
func applyUpdatesToTaskDocument(updates applyUpdates, fallback *taskData) *edit.TaskDocument {
	doc := &edit.TaskDocument{}

	// Title
	if updates.Title != nil {
		doc.Title = *updates.Title
	} else {
		doc.Title = fallback.Title
	}

	// State
	if updates.State != nil {
		doc.State = *updates.State
	} else {
		doc.State = fallback.State
	}

	// Priority
	if updates.Priority != nil {
		doc.Priority = *updates.Priority
	} else if fallback.Priority != nil {
		doc.Priority = *fallback.Priority
	} else {
		doc.Priority = 3 // default
	}

	// DueAt
	if updates.DueAt != nil {
		doc.DueAt = *updates.DueAt
	} else if fallback.DueAt != nil {
		doc.DueAt = *fallback.DueAt
	}

	// Body
	if updates.Body != nil {
		doc.Body = *updates.Body
	} else if fallback.Body != nil {
		doc.Body = *fallback.Body
	}

	return doc
}
