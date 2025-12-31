package cli

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/edit"
	"github.com/lherron/wrkq/internal/parse"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var editCmd = &cobra.Command{
	Use:   "edit <PATHSPEC|ID>",
	Short: "Edit a task in $EDITOR with 3-way merge",
	Long: `Edit a task in your default editor with automatic 3-way merge.

When you save and exit the editor, the changes are merged with any concurrent
updates that may have occurred in the database. If there are conflicts that
cannot be automatically resolved, the command exits with code 4.

Examples:
  wrkq edit T-00001            # Edit task in $EDITOR
  wrkq edit portal/auth/login  # Edit by path
  wrkq edit T-00001 --if-match 5  # Only edit if etag matches
`,
	Args: cobra.ExactArgs(1),
	RunE: appctx.WithApp(appctx.DefaultOptions(), runEdit),
}

var (
	editIfMatch int64
)

func init() {
	rootCmd.AddCommand(editCmd)

	editCmd.Flags().Int64Var(&editIfMatch, "if-match", 0, "Only edit if etag matches (0 = no check)")
}

func runEdit(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	// Resolve task
	taskID := applyProjectRootToSelector(app.Config, args[0], false)
	taskUUID, friendlyID, err := selectors.ResolveTask(database, taskID)
	if err != nil {
		return fmt.Errorf("failed to resolve task: %w", err)
	}

	// Fetch initial task state (this is the "base" for 3-way merge)
	baseTask, err := fetchTaskData(database, taskUUID)
	if err != nil {
		return fmt.Errorf("failed to fetch task: %w", err)
	}

	// Check etag if --if-match is specified
	if editIfMatch > 0 && baseTask.ETag != editIfMatch {
		return fmt.Errorf("etag mismatch: expected %d, got %d", editIfMatch, baseTask.ETag)
	}

	// Create temporary file with task content
	tmpfile, err := createTempFile(baseTask)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpfile)

	// Open in editor
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi" // fallback
	}

	editorCmd := exec.Command(editor, tmpfile)
	editorCmd.Stdin = os.Stdin
	editorCmd.Stdout = os.Stdout
	editorCmd.Stderr = os.Stderr

	if err := editorCmd.Run(); err != nil {
		return fmt.Errorf("editor failed: %w", err)
	}

	// Read edited content
	editedContent, err := ioutil.ReadFile(tmpfile)
	if err != nil {
		return fmt.Errorf("failed to read edited file: %w", err)
	}

	// Parse edited document
	editedTask, err := parseTaskDocument(editedContent)
	if err != nil {
		return fmt.Errorf("failed to parse edited document: %w", err)
	}

	// Fetch current task state (might have changed while user was editing)
	currentTask, err := fetchTaskData(database, taskUUID)
	if err != nil {
		return fmt.Errorf("failed to fetch current task: %w", err)
	}

	// Perform 3-way merge
	baseDoc := taskDataToDocument(baseTask)
	currentDoc := taskDataToDocument(currentTask)
	editedDoc := taskDocumentFromParsed(editedTask, baseTask)

	mergeResult := edit.Merge3Way(baseDoc, currentDoc, editedDoc)

	// Check for conflicts
	if mergeResult.HasConflict {
		fmt.Fprintf(os.Stderr, "%s", mergeResult.FormatConflicts())
		os.Exit(4) // Conflict exit code
	}

	// Apply merged changes
	updates := &parse.TaskUpdate{
		Title:       &mergeResult.Merged.Title,
		State:       &mergeResult.Merged.State,
		Priority:    &mergeResult.Merged.Priority,
		DueAt:       stringPtr(mergeResult.Merged.DueAt),
		Description: &mergeResult.Merged.Description,
	}

	err = applyTaskUpdates(database, taskUUID, updates, false)
	if err != nil {
		return fmt.Errorf("failed to apply updates: %w", err)
	}

	fmt.Printf("Updated task %s\n", friendlyID)
	return nil
}

func createTempFile(task *taskData) (string, error) {
	// Create markdown document with front matter
	var content strings.Builder

	content.WriteString("---\n")
	content.WriteString(fmt.Sprintf("title: %s\n", task.Title))
	content.WriteString(fmt.Sprintf("state: %s\n", task.State))
	if task.Priority != nil {
		content.WriteString(fmt.Sprintf("priority: %d\n", *task.Priority))
	}
	if task.DueAt != nil {
		content.WriteString(fmt.Sprintf("due_at: %s\n", *task.DueAt))
	}
	content.WriteString("---\n\n")

	if task.Description != nil {
		content.WriteString(*task.Description)
	}

	// Create temp file
	tmpfile, err := ioutil.TempFile("", "wrkq-edit-*.md")
	if err != nil {
		return "", err
	}

	if _, err := tmpfile.Write([]byte(content.String())); err != nil {
		tmpfile.Close()
		os.Remove(tmpfile.Name())
		return "", err
	}

	if err := tmpfile.Close(); err != nil {
		os.Remove(tmpfile.Name())
		return "", err
	}

	return tmpfile.Name(), nil
}

type parsedTask struct {
	Title       string  `yaml:"title"`
	State       string  `yaml:"state"`
	Priority    *int    `yaml:"priority,omitempty"`
	DueAt       *string `yaml:"due_at,omitempty"`
	Description string
}

func parseTaskDocument(content []byte) (*parsedTask, error) {
	text := string(content)

	// Check for front matter
	if !strings.HasPrefix(text, "---\n") {
		return nil, fmt.Errorf("missing YAML front matter")
	}

	parts := strings.SplitN(text[4:], "\n---\n", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid front matter format")
	}

	var task parsedTask
	if err := yaml.Unmarshal([]byte(parts[0]), &task); err != nil {
		return nil, fmt.Errorf("failed to parse front matter: %w", err)
	}

	task.Description = strings.TrimSpace(parts[1])
	return &task, nil
}

func taskDataToDocument(task *taskData) *edit.TaskDocument {
	doc := &edit.TaskDocument{
		Title: task.Title,
		State: task.State,
	}

	if task.Priority != nil {
		doc.Priority = *task.Priority
	} else {
		doc.Priority = 3 // default priority
	}

	if task.DueAt != nil {
		doc.DueAt = *task.DueAt
	}

	if task.Description != nil {
		doc.Description = *task.Description
	}

	return doc
}

func taskDocumentFromParsed(parsed *parsedTask, base *taskData) *edit.TaskDocument {
	doc := &edit.TaskDocument{
		Title:       parsed.Title,
		State:       parsed.State,
		Description: parsed.Description,
	}

	if parsed.Priority != nil {
		doc.Priority = *parsed.Priority
	} else if base.Priority != nil {
		doc.Priority = *base.Priority
	} else {
		doc.Priority = 3
	}

	if parsed.DueAt != nil {
		doc.DueAt = *parsed.DueAt
	} else if base.DueAt != nil {
		doc.DueAt = *base.DueAt
	}

	return doc
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
