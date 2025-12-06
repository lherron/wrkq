package cli

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/render"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

var relationCmd = &cobra.Command{
	Use:   "relation",
	Short: "Manage task relations",
	Long: `Manage relations between tasks.

Relations can be:
  - blocks: Task A blocks Task B (B cannot proceed until A is done)
  - relates_to: Task A is related to Task B (informational link)
  - duplicates: Task A duplicates Task B (same work)

Examples:
  wrkq relation add T-00001 blocks T-00002
  wrkq relation add T-00003 relates_to T-00004
  wrkq relation rm T-00001 blocks T-00002
  wrkq relation ls T-00001`,
}

var relationAddCmd = &cobra.Command{
	Use:   "add <from-task> <kind> <to-task>",
	Short: "Add a relation between tasks",
	Long: `Create a relation from one task to another.

Relation kinds:
  - blocks: First task blocks the second (second cannot proceed until first is done)
  - relates_to: Tasks are related (informational link)
  - duplicates: First task duplicates the second (same work)

Examples:
  wrkq relation add T-00001 blocks T-00002
  wrkq relation add myproject/task-a relates_to myproject/task-b`,
	Args: cobra.ExactArgs(3),
	RunE: appctx.WithApp(appctx.WithActor(), runRelationAdd),
}

var relationRmCmd = &cobra.Command{
	Use:   "rm <from-task> <kind> <to-task>",
	Short: "Remove a relation between tasks",
	Long: `Remove an existing relation between two tasks.

Examples:
  wrkq relation rm T-00001 blocks T-00002`,
	Args: cobra.ExactArgs(3),
	RunE: appctx.WithApp(appctx.DefaultOptions(), runRelationRm),
}

var relationLsCmd = &cobra.Command{
	Use:   "ls <task>",
	Short: "List relations for a task",
	Long: `List all relations involving a task (both incoming and outgoing).

Examples:
  wrkq relation ls T-00001
  wrkq relation ls T-00001 --json`,
	Args: cobra.ExactArgs(1),
	RunE: appctx.WithApp(appctx.DefaultOptions(), runRelationLs),
}

var (
	relationJSON      bool
	relationNDJSON    bool
	relationPorcelain bool
)

func init() {
	rootCmd.AddCommand(relationCmd)
	relationCmd.AddCommand(relationAddCmd)
	relationCmd.AddCommand(relationRmCmd)
	relationCmd.AddCommand(relationLsCmd)

	relationLsCmd.Flags().BoolVar(&relationJSON, "json", false, "Output as JSON")
	relationLsCmd.Flags().BoolVar(&relationNDJSON, "ndjson", false, "Output as newline-delimited JSON")
	relationLsCmd.Flags().BoolVar(&relationPorcelain, "porcelain", false, "Machine-readable output")
}

func runRelationAdd(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	actorUUID := app.ActorUUID

	fromRef := args[0]
	kind := args[1]
	toRef := args[2]

	// Validate kind
	if err := domain.ValidateTaskRelationKind(kind); err != nil {
		return err
	}

	// Resolve from task
	fromTaskUUID, fromTaskID, err := selectors.ResolveTask(database, fromRef)
	if err != nil {
		return fmt.Errorf("failed to resolve from-task: %w", err)
	}

	// Resolve to task
	toTaskUUID, toTaskID, err := selectors.ResolveTask(database, toRef)
	if err != nil {
		return fmt.Errorf("failed to resolve to-task: %w", err)
	}

	// Prevent self-reference (also enforced by DB trigger)
	if fromTaskUUID == toTaskUUID {
		return fmt.Errorf("task cannot have a relation to itself")
	}

	// Insert the relation
	_, err = database.Exec(`
		INSERT INTO task_relations (from_task_uuid, to_task_uuid, kind, created_by_actor_uuid)
		VALUES (?, ?, ?, ?)
	`, fromTaskUUID, toTaskUUID, kind, actorUUID)
	if err != nil {
		return fmt.Errorf("failed to create relation: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Created relation: %s %s %s\n", fromTaskID, kind, toTaskID)
	return nil
}

func runRelationRm(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	fromRef := args[0]
	kind := args[1]
	toRef := args[2]

	// Validate kind
	if err := domain.ValidateTaskRelationKind(kind); err != nil {
		return err
	}

	// Resolve from task
	fromTaskUUID, fromTaskID, err := selectors.ResolveTask(database, fromRef)
	if err != nil {
		return fmt.Errorf("failed to resolve from-task: %w", err)
	}

	// Resolve to task
	toTaskUUID, toTaskID, err := selectors.ResolveTask(database, toRef)
	if err != nil {
		return fmt.Errorf("failed to resolve to-task: %w", err)
	}

	// Delete the relation
	result, err := database.Exec(`
		DELETE FROM task_relations
		WHERE from_task_uuid = ? AND to_task_uuid = ? AND kind = ?
	`, fromTaskUUID, toTaskUUID, kind)
	if err != nil {
		return fmt.Errorf("failed to delete relation: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("relation not found: %s %s %s", fromTaskID, kind, toTaskID)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Removed relation: %s %s %s\n", fromTaskID, kind, toTaskID)
	return nil
}

type Relation struct {
	Direction    string `json:"direction"` // "outgoing" or "incoming"
	Kind         string `json:"kind"`
	TaskID       string `json:"task_id"`
	TaskUUID     string `json:"task_uuid"`
	TaskSlug     string `json:"task_slug"`
	TaskTitle    string `json:"task_title"`
	CreatedAt    string `json:"created_at"`
	CreatedByID  string `json:"created_by_id"`
}

func runRelationLs(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	taskRef := args[0]

	// Resolve task
	taskUUID, _, err := selectors.ResolveTask(database, taskRef)
	if err != nil {
		return fmt.Errorf("failed to resolve task: %w", err)
	}

	var relations []Relation

	// Get outgoing relations (this task -> other tasks)
	outgoingRows, err := database.Query(`
		SELECT r.kind, r.created_at,
		       t.id AS task_id, t.uuid AS task_uuid, t.slug, t.title,
		       a.id AS created_by_id
		FROM task_relations r
		JOIN tasks t ON r.to_task_uuid = t.uuid
		JOIN actors a ON r.created_by_actor_uuid = a.uuid
		WHERE r.from_task_uuid = ?
		ORDER BY r.kind, t.id
	`, taskUUID)
	if err != nil {
		return fmt.Errorf("failed to query outgoing relations: %w", err)
	}

	for outgoingRows.Next() {
		var rel Relation
		if err := outgoingRows.Scan(&rel.Kind, &rel.CreatedAt, &rel.TaskID, &rel.TaskUUID, &rel.TaskSlug, &rel.TaskTitle, &rel.CreatedByID); err != nil {
			outgoingRows.Close()
			return fmt.Errorf("failed to scan relation: %w", err)
		}
		rel.Direction = "outgoing"
		relations = append(relations, rel)
	}
	outgoingRows.Close()

	// Get incoming relations (other tasks -> this task)
	incomingRows, err := database.Query(`
		SELECT r.kind, r.created_at,
		       t.id AS task_id, t.uuid AS task_uuid, t.slug, t.title,
		       a.id AS created_by_id
		FROM task_relations r
		JOIN tasks t ON r.from_task_uuid = t.uuid
		JOIN actors a ON r.created_by_actor_uuid = a.uuid
		WHERE r.to_task_uuid = ?
		ORDER BY r.kind, t.id
	`, taskUUID)
	if err != nil {
		return fmt.Errorf("failed to query incoming relations: %w", err)
	}

	for incomingRows.Next() {
		var rel Relation
		if err := incomingRows.Scan(&rel.Kind, &rel.CreatedAt, &rel.TaskID, &rel.TaskUUID, &rel.TaskSlug, &rel.TaskTitle, &rel.CreatedByID); err != nil {
			incomingRows.Close()
			return fmt.Errorf("failed to scan relation: %w", err)
		}
		rel.Direction = "incoming"
		relations = append(relations, rel)
	}
	incomingRows.Close()

	// Output
	if relationJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		if !relationPorcelain {
			encoder.SetIndent("", "  ")
		}
		return encoder.Encode(relations)
	}

	if relationNDJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		for _, rel := range relations {
			if err := encoder.Encode(rel); err != nil {
				return err
			}
		}
		return nil
	}

	// Table output
	if len(relations) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No relations found")
		return nil
	}

	headers := []string{"Direction", "Kind", "Task ID", "Slug", "Title"}
	var rowsData [][]string
	for _, rel := range relations {
		rowsData = append(rowsData, []string{
			rel.Direction,
			rel.Kind,
			rel.TaskID,
			rel.TaskSlug,
			rel.TaskTitle,
		})
	}

	r := render.NewRenderer(cmd.OutOrStdout(), render.Options{
		Format:    render.FormatTable,
		Porcelain: relationPorcelain,
	})

	return r.RenderTable(headers, rowsData)
}
