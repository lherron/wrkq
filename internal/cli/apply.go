package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/parse"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

var applyCmd = &cobra.Command{
	Use:   "apply <PATHSPEC|ID> <FILE|->",
	Short: "Update task description/specification from file or stdin",
	Long: `Update task description/specification from file or stdin.

By default, only the description and specification fields are updated. Use --with-metadata to also
update title, state, priority, and due_at fields.

For quick metadata-only updates, use 'wrkq set' instead:
  wrkq set T-00001 --state in_progress --priority 1

Accepts:
- Plain markdown (description only)
- Markdown with YAML front matter (specification supported; metadata requires --with-metadata)
- YAML (specification supported; metadata requires --with-metadata)
- JSON (specification supported; metadata requires --with-metadata)

Examples:
  wrkq apply T-00001 description.md              # Update description from file
  wrkq apply T-00001 -                           # Update description from stdin
  cat notes.md | wrkq apply T-00001 -            # Pipe description
  wrkq apply T-00001 full.md --with-metadata     # Update all fields
  wrkq apply T-00001 - --if-match 5              # Conditional update
`,
	Args: cobra.ExactArgs(2),
	RunE: appctx.WithApp(appctx.WithActor(), runApply),
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
	applyCmd.Flags().BoolVar(&applyWithMetadata, "with-metadata", false, "Update metadata fields (title, state, priority, due_at) in addition to description/specification")
	applyCmd.Flags().Int64Var(&applyIfMatch, "if-match", 0, "Only apply if etag matches (0 = no check)")
	applyCmd.Flags().BoolVar(&applyDryRun, "dry-run", false, "Show what would be changed without applying")
}

func runApply(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	// Resolve task
	taskID := applyProjectRootToSelector(app.Config, args[0], false)
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
		defer func() { _ = file.Close() }()
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
	if !applyWithMetadata && updates.Description == nil && updates.Specification == nil {
		return fmt.Errorf("no description or specification found in input (body-only mode requires description or specification)")
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
		if updates.Specification != nil {
			fmt.Printf("  specification: %s\n", *updates.Specification)
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
	return applyTaskUpdates(database, app.Attribution(), taskUUID, updates, !applyWithMetadata, applyIfMatch)
}

func applyTaskUpdates(database *db.DB, attr attribution.Attribution, taskUUID string, updates *parse.TaskUpdate, bodyOnly bool, ifMatch int64) error {
	fields := map[string]interface{}{}
	if bodyOnly {
		if updates.Description != nil {
			fields["description"] = *updates.Description
		}
		if updates.Specification != nil {
			fields["specification"] = *updates.Specification
		}
		if len(fields) == 0 {
			return fmt.Errorf("no description or specification provided in --body-only mode")
		}
	} else {
		if updates.Title != nil {
			fields["title"] = *updates.Title
		}
		if updates.State != nil {
			fields["state"] = *updates.State
		}
		if updates.Priority != nil {
			fields["priority"] = *updates.Priority
		}
		if updates.DueAt != nil {
			fields["due_at"] = *updates.DueAt
		}
		if updates.Description != nil {
			fields["description"] = *updates.Description
		}
		if updates.Specification != nil {
			fields["specification"] = *updates.Specification
		}
	}
	if len(fields) == 0 {
		return fmt.Errorf("no fields to update")
	}
	if _, err := store.New(database).Tasks.UpdateFieldsWithAttribution(attr, taskUUID, fields, ifMatch); err != nil {
		return fmt.Errorf("failed to update task: %w", err)
	}
	fmt.Printf("Updated task %s\n", taskUUID)
	return nil
}
