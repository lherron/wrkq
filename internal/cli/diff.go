package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

var diffCmd = &cobra.Command{
	Use:   "diff <A> [B]",
	Short: "Compare two tasks or task versions",
	Long: `Compare two tasks or task versions and display differences.

Examples:
  wrkq diff T-00001 T-00002      # Compare two tasks
  wrkq diff T-00001              # Compare task with working copy (future)
  wrkq diff T-00001 --json       # Output differences as JSON
`,
	Args: cobra.RangeArgs(1, 2),
	RunE: appctx.WithApp(appctx.DefaultOptions(), runDiff),
}

var (
	diffUnified int
	diffJSON    bool
)

func init() {
	rootCmd.AddCommand(diffCmd)

	diffCmd.Flags().IntVar(&diffUnified, "unified", 3, "Lines of unified context")
	diffCmd.Flags().BoolVar(&diffJSON, "json", false, "Output as JSON")
}

func runDiff(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	// Resolve first task
	uuidA, _, err := selectors.ResolveTask(database, args[0])
	if err != nil {
		return fmt.Errorf("failed to resolve task A: %w", err)
	}

	taskA, err := fetchTaskData(database, uuidA)
	if err != nil {
		return fmt.Errorf("failed to fetch task A: %w", err)
	}

	// If only one argument, compare with working copy (not implemented yet)
	if len(args) == 1 {
		return fmt.Errorf("comparing with working copy not yet implemented")
	}

	// Resolve second task
	uuidB, _, err := selectors.ResolveTask(database, args[1])
	if err != nil {
		return fmt.Errorf("failed to resolve task B: %w", err)
	}

	taskB, err := fetchTaskData(database, uuidB)
	if err != nil {
		return fmt.Errorf("failed to fetch task B: %w", err)
	}

	// Compare tasks
	diff := compareTasksDetailed(taskA, taskB)

	// Render output
	if diffJSON {
		return renderDiffJSON(diff)
	}

	return renderDiffHuman(diff, taskA, taskB)
}

type taskData struct {
	UUID        string
	ID          string
	Slug        string
	Title       string
	Description *string
	State       string
	Priority    *int
	DueAt       *string
	ETag        int64
	CreatedAt   string
	UpdatedAt   string
}

func fetchTaskData(database *db.DB, uuid string) (*taskData, error) {
	var task taskData

	query := `
		SELECT uuid, id, slug, title, description, state, priority, due_at, etag, created_at, updated_at
		FROM tasks
		WHERE uuid = ?
	`

	err := database.QueryRow(query, uuid).Scan(
		&task.UUID,
		&task.ID,
		&task.Slug,
		&task.Title,
		&task.Description,
		&task.State,
		&task.Priority,
		&task.DueAt,
		&task.ETag,
		&task.CreatedAt,
		&task.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("task not found: %s", uuid)
	}

	return &task, nil
}

type taskDiff struct {
	FieldsChanged []string               `json:"fields_changed"`
	Changes       map[string]fieldChange `json:"changes"`
}

type fieldChange struct {
	Field string      `json:"field"`
	Old   interface{} `json:"old"`
	New   interface{} `json:"new"`
}

func compareTasksDetailed(a, b *taskData) *taskDiff {
	diff := &taskDiff{
		FieldsChanged: []string{},
		Changes:       make(map[string]fieldChange),
	}

	// Compare each field
	if a.Slug != b.Slug {
		diff.FieldsChanged = append(diff.FieldsChanged, "slug")
		diff.Changes["slug"] = fieldChange{"slug", a.Slug, b.Slug}
	}

	if a.Title != b.Title {
		diff.FieldsChanged = append(diff.FieldsChanged, "title")
		diff.Changes["title"] = fieldChange{"title", a.Title, b.Title}
	}

	// Compare nullable description
	descriptionA := ""
	if a.Description != nil {
		descriptionA = *a.Description
	}
	descriptionB := ""
	if b.Description != nil {
		descriptionB = *b.Description
	}
	if descriptionA != descriptionB {
		diff.FieldsChanged = append(diff.FieldsChanged, "description")
		diff.Changes["description"] = fieldChange{"description", descriptionA, descriptionB}
	}

	if a.State != b.State {
		diff.FieldsChanged = append(diff.FieldsChanged, "state")
		diff.Changes["state"] = fieldChange{"state", a.State, b.State}
	}

	// Compare nullable priority
	prioA := 0
	if a.Priority != nil {
		prioA = *a.Priority
	}
	prioB := 0
	if b.Priority != nil {
		prioB = *b.Priority
	}
	if prioA != prioB {
		diff.FieldsChanged = append(diff.FieldsChanged, "priority")
		diff.Changes["priority"] = fieldChange{"priority", prioA, prioB}
	}

	// Compare nullable due_at
	dueA := ""
	if a.DueAt != nil {
		dueA = *a.DueAt
	}
	dueB := ""
	if b.DueAt != nil {
		dueB = *b.DueAt
	}
	if dueA != dueB {
		diff.FieldsChanged = append(diff.FieldsChanged, "due_at")
		diff.Changes["due_at"] = fieldChange{"due_at", dueA, dueB}
	}

	if a.ETag != b.ETag {
		diff.FieldsChanged = append(diff.FieldsChanged, "etag")
		diff.Changes["etag"] = fieldChange{"etag", a.ETag, b.ETag}
	}

	return diff
}

func renderDiffJSON(diff *taskDiff) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(diff)
}

func renderDiffHuman(diff *taskDiff, a, b *taskData) error {
	if len(diff.FieldsChanged) == 0 {
		fmt.Printf("No differences between %s and %s\n", a.ID, b.ID)
		return nil
	}

	fmt.Printf("Comparing %s (%s) vs %s (%s)\n\n", a.ID, a.Slug, b.ID, b.Slug)
	fmt.Printf("%d field(s) changed:\n\n", len(diff.FieldsChanged))

	for _, field := range diff.FieldsChanged {
		change := diff.Changes[field]
		fmt.Printf("\033[33m%s:\033[0m\n", field)
		fmt.Printf("  \033[31m- %v\033[0m\n", change.Old)
		fmt.Printf("  \033[32m+ %v\033[0m\n\n", change.New)
	}

	return nil
}
