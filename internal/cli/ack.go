package cli

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

var ackCmd = &cobra.Command{
	Use:   "ack <task|id>...",
	Short: "Acknowledge completed tasks",
	Long: `Marks tasks as acknowledged by setting acknowledged_at.

By default, tasks must be in completed or cancelled state unless --force is used.
Examples:
  wrkq ack T-00001 T-00002
  wrkq ack inbox/fix-bug --force
`,
	Args: cobra.MinimumNArgs(1),
	RunE: appctx.WithApp(appctx.WithActor(), runAck),
}

var (
	ackForce bool
)

func init() {
	rootCmd.AddCommand(ackCmd)
	ackCmd.Flags().BoolVar(&ackForce, "force", false, "Allow ack on non-completed tasks")
}

type ackCounts struct {
	Total        int
	Acknowledged int
	Skipped      int
}

func runAck(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	actorUUID := app.ActorUUID

	for i, arg := range args {
		args[i] = applyProjectRootToSelector(app.Config, arg, false)
	}

	counts, err := ackTasks(database, actorUUID, args, ackForce)
	if err != nil {
		return err
	}

	if counts.Acknowledged > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Acknowledged %d task(s)\n", counts.Acknowledged)
	}
	if counts.Skipped > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Skipped %d task(s) already acknowledged\n", counts.Skipped)
	}
	if counts.Acknowledged == 0 && counts.Skipped == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No tasks acknowledged")
	}

	return nil
}

func ackTasks(database *db.DB, actorUUID string, refs []string, force bool) (ackCounts, error) {
	counts := ackCounts{Total: len(refs)}
	s := store.New(database)
	now := time.Now().UTC().Format(time.RFC3339)

	for _, ref := range refs {
		taskUUID, _, err := selectors.ResolveTask(database, ref)
		if err != nil {
			return counts, err
		}

		var state string
		var acknowledgedAt sql.NullString
		if err := database.QueryRow("SELECT state, acknowledged_at FROM tasks WHERE uuid = ?", taskUUID).Scan(&state, &acknowledgedAt); err != nil {
			if err == sql.ErrNoRows {
				return counts, fmt.Errorf("task not found: %s", ref)
			}
			return counts, fmt.Errorf("failed to load task %s: %w", ref, err)
		}

		if acknowledgedAt.Valid {
			counts.Skipped++
			continue
		}

		if !force && state != "completed" && state != "cancelled" {
			return counts, fmt.Errorf("cannot ack %s: state is %s (requires completed or cancelled)", ref, state)
		}

		_, err = s.Tasks.UpdateFields(actorUUID, taskUUID, map[string]interface{}{"acknowledged_at": now}, 0)
		if err != nil {
			return counts, err
		}
		counts.Acknowledged++
	}

	return counts, nil
}
