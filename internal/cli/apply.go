package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/parse"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

var applyCmd = &cobra.Command{
	Use:   "apply <PATHSPEC|ID> <FILE|->",
	Short: "Update task description from file or stdin",
	Long: `Update task description from file or stdin.

By default, only the description field is updated. Use --with-metadata to also
update title, state, priority, and due_at fields.

For quick metadata-only updates, use 'wrkq set' instead:
  wrkq set T-00001 state=in_progress priority=1

Accepts:
- Plain markdown (description only)
- Markdown with YAML front matter (requires --with-metadata)
- YAML (requires --with-metadata)
- JSON (requires --with-metadata)

Examples:
  wrkq apply T-00001 description.md              # Update description from file
  wrkq apply T-00001 -                           # Update description from stdin
  cat notes.md | wrkq apply T-00001 -            # Pipe description
  wrkq apply T-00001 full.md --with-metadata     # Update all fields
  wrkq apply T-00001 - --if-match 5              # Conditional update
`,
	Args: cobra.ExactArgs(2),
	RunE: appctx.WithApp(appctx.DefaultOptions(), runApply),
}

var (
	applyFormat       string
	applyWithMetadata bool
	applyIfMatch      int64
	applyDryRun       bool
)

func init() {
	rootCmd.AddCommand(applyCmd)

	applyCmd.Flags().StringVar(&applyFormat, "format", "", "Input format: md, yaml, json (auto-detected if not specified)")
	applyCmd.Flags().BoolVar(&applyWithMetadata, "with-metadata", false, "Update metadata fields (title, state, priority, due_at) in addition to description")
	applyCmd.Flags().Int64Var(&applyIfMatch, "if-match", 0, "Only apply if etag matches (0 = no check)")
	applyCmd.Flags().BoolVar(&applyDryRun, "dry-run", false, "Show what would be changed without applying")
}

func runApply(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	// Resolve task
	taskID := args[0]
	taskUUID, friendlyID, err := selectors.ResolveTask(database, taskID)
	if err != nil {
		return fmt.Errorf("failed to resolve task: %w", err)
	}

	// Determine input source
	var input io.Reader
	var inputSource string
	if args[1] == "-" {
		input = os.Stdin
		inputSource = "stdin"
	} else {
		file, err := os.Open(args[1])
		if err != nil {
			absPath, _ := os.Getwd()
			if absPath != "" {
				absPath = absPath + "/" + args[1]
			} else {
				absPath = args[1]
			}
			return fmt.Errorf("failed to open file %s: %w", absPath, err)
		}
		defer file.Close()
		input = file
		inputSource = args[1]
	}

	// Read input
	data, err := io.ReadAll(input)
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}

	// Validate input is not empty
	if len(data) == 0 {
		return fmt.Errorf("input is empty (from %s)", inputSource)
	}

	// Check size limits
	const (
		warnSize  = 1 * 1024 * 1024  // 1MB
		errorSize = 10 * 1024 * 1024 // 10MB
	)
	if len(data) > errorSize {
		return fmt.Errorf("input too large: %d bytes (max %d bytes)", len(data), errorSize)
	}
	if len(data) > warnSize {
		fmt.Fprintf(os.Stderr, "Warning: Large input detected (%d bytes). This may take a moment to process.\n", len(data))
	}

	// Parse input
	updates, err := parse.Parse(data, applyFormat)
	if err != nil {
		// Show first few lines of input to help debugging
		preview := string(data)
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		lines := strings.Split(preview, "\n")
		if len(lines) > 5 {
			lines = lines[:5]
		}
		previewText := strings.Join(lines, "\n")
		return fmt.Errorf("failed to parse input: %w\n\nFirst few lines of input:\n%s", err, previewText)
	}

	// Validate metadata usage
	if !applyWithMetadata {
		// Check if metadata fields are present
		hasMetadata := updates.Title != nil || updates.State != nil || updates.Priority != nil || updates.DueAt != nil
		if hasMetadata {
			// Only show warning if not suppressed
			if os.Getenv("WRKQ_SUPPRESS_METADATA_WARNING") == "" {
				fmt.Fprintf(os.Stderr, "Warning: Metadata ignored without --with-metadata. Use 'wrkq set %s' for quick updates.\n", taskID)
			}
			// Clear metadata fields
			updates.Title = nil
			updates.State = nil
			updates.Priority = nil
			updates.DueAt = nil
		}
	}

	// Fetch current task state
	currentTask, err := fetchTaskData(database, taskUUID)
	if err != nil {
		return fmt.Errorf("failed to fetch current task: %w", err)
	}

	// Validate parsed content
	if !applyWithMetadata && updates.Description == nil {
		return fmt.Errorf("no description found in input (description-only mode requires description field)")
	}

	// Check etag if --if-match is specified
	if applyIfMatch > 0 && currentTask.ETag != applyIfMatch {
		return fmt.Errorf("etag mismatch: task was modified (expected etag %d, current etag %d). Use --if-match %d to update, or --if-match 0 to force",
			applyIfMatch, currentTask.ETag, currentTask.ETag)
	}

	// Apply updates
	if applyDryRun {
		fmt.Printf("Would update task %s (%s)\n", friendlyID, taskUUID)
		if updates.Description != nil {
			fmt.Printf("  description: %s\n", *updates.Description)
		}
		if applyWithMetadata {
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
		}
		return nil
	}

	// Execute update
	return applyTaskUpdates(database, taskUUID, updates, !applyWithMetadata)
}

func applyTaskUpdates(database *db.DB, taskUUID string, updates *parse.TaskUpdate, bodyOnly bool) error {
	// Build update query
	var setClauses []string
	var args []interface{}

	if bodyOnly {
		// Only update description
		if updates.Description != nil {
			setClauses = append(setClauses, "description = ?")
			args = append(args, *updates.Description)
		} else {
			return fmt.Errorf("no description provided in --body-only mode")
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
		if updates.Description != nil {
			setClauses = append(setClauses, "description = ?")
			args = append(args, *updates.Description)
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
