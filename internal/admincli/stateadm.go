package admincli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/admincli/appctx"
	"github.com/lherron/wrkq/internal/snapshot"
	"github.com/spf13/cobra"
)

var stateAdmCmd = &cobra.Command{
	Use:   "state",
	Short: "Manage canonical state snapshots",
	Long: `Commands for exporting, importing, and verifying canonical JSON
state snapshots of the wrkq task ledger.

A snapshot is the complete task-ledger domain: every container, task,
comment, promise and task relation (archived, deleted and soft-deleted rows
included) with every domain column, plus friendly-id high-water marks.
Snapshots are deterministic JSON designed for patch-first Git workflows.

A snapshot is NOT a disaster-recovery artifact. Rooms, envelopes, handoffs,
wrkf workflow runtime, attachments and event history are outside it. For DR,
take a file-level copy: ` + "`wrkqadm db snapshot`" + ` (SQLite online backup) or
a copy of the database file with the daemon stopped.`,
}

// Export command
var stateExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export the database to a canonical JSON snapshot",
	Long: `Export reads the current database and produces a canonical JSON snapshot.

The snapshot includes every container, task, comment, promise and task
relation — archived, deleted and soft-deleted rows included — in a
deterministic format suitable for diffing and version control. It is not a
disaster-recovery artifact: rooms, envelopes, handoffs, wrkf runtime,
attachments and event history are not in it (--include-events adds the event
log and project events for reading; import does not replay them).

Canonicalization ensures byte-for-byte identical output for the same
database state (sorted keys, no insignificant whitespace, sorted arrays).`,
	RunE: appctx.WithApp(appctx.DefaultOptions(), runStateExport),
}

var (
	stateExportOut           string
	stateExportNoCanonical   bool
	stateExportIncludeEvents bool
	stateExportJSON          bool
)

func init() {
	rootAdmCmd.AddCommand(stateAdmCmd)
	stateAdmCmd.AddCommand(stateExportCmd)
	stateAdmCmd.AddCommand(stateImportCmd)
	stateAdmCmd.AddCommand(stateVerifyCmd)

	// Export flags
	stateExportCmd.Flags().StringVar(&stateExportOut, "out", snapshot.DefaultOutputPath, "Output file path")
	stateExportCmd.Flags().BoolVar(&stateExportNoCanonical, "no-canonical", false, "Disable canonicalization (pretty print)")
	stateExportCmd.Flags().BoolVar(&stateExportIncludeEvents, "include-events", false, "Include full event log in snapshot")
	stateExportCmd.Flags().BoolVar(&stateExportJSON, "json", false, "Output result as JSON")

	// Import flags
	stateImportCmd.Flags().StringVar(&stateImportFrom, "from", snapshot.DefaultOutputPath, "Input file path")
	stateImportCmd.Flags().BoolVar(&stateImportDryRun, "dry-run", false, "Validate only, don't write to database")
	stateImportCmd.Flags().BoolVar(&stateImportIfEmpty, "if-empty", false, "Require database to be empty (the default without --force; exit 4 on refusal)")
	stateImportCmd.Flags().BoolVar(&stateImportForce, "force", false, "Truncate the task ledger before import (never against a live ledger)")
	stateImportCmd.Flags().BoolVar(&stateImportAllowCascade, "allow-cascade", false, "With --force: proceed although out-of-model rows (rooms, envelopes, workflow instances, ...) reference the ledger and will be cascade-deleted")
	stateImportCmd.Flags().BoolVar(&stateImportJSON, "json", false, "Output result as JSON")

	// Verify flags
	stateVerifyCmd.Flags().BoolVar(&stateVerifyJSON, "json", false, "Output result as JSON")
}

func runStateExport(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	// Export snapshot
	opts := snapshot.ExportOptions{
		OutputPath:    stateExportOut,
		Canonical:     !stateExportNoCanonical,
		IncludeEvents: stateExportIncludeEvents,
	}

	result, err := snapshot.Export(database.DB, opts)
	if err != nil {
		return exitError(1, fmt.Errorf("failed to export snapshot: %w", err))
	}

	// Output result
	if stateExportJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return exitError(1, fmt.Errorf("failed to encode result: %w", err))
		}
	} else {
		fmt.Printf("✓ Exported snapshot to %s\n", result.OutputPath)
		fmt.Printf("  snapshot_rev: %s\n", result.SnapshotRev)
		fmt.Printf("  containers: %d, tasks: %d, comments: %d, promises: %d, links: %d\n",
			result.ContainerCount, result.TaskCount, result.CommentCount, result.PromiseCount, result.LinkCount)
		if result.EventCount > 0 {
			fmt.Printf("  events: %d\n", result.EventCount)
		}
	}

	return nil
}

// Import command
var stateImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Import a snapshot into the database",
	Long: `Import reads a canonical JSON snapshot and replaces the task ledger with it.

By default, import requires the database to be essentially empty (only the
root and the inbox that init seeds). Rows are restored verbatim: ids, etags,
timestamps and attribution are the snapshot's.

--force truncates the modelled tables (containers, tasks, comments, promises,
task relations) before import. WARNING: deleting those rows cascade-deletes
or nulls the rows outside the snapshot model that reference them — rooms,
envelopes, workflow instances, attachments, task causes, project events.
--force therefore refuses when such rows exist unless --allow-cascade is
also given. Never run --force against a live ledger.

Use --dry-run to validate the snapshot without writing to the database.`,
	RunE: appctx.WithApp(appctx.DefaultOptions(), runStateImport),
}

var (
	stateImportFrom    string
	stateImportDryRun  bool
	stateImportIfEmpty bool
	stateImportForce   bool
	stateImportJSON    bool

	stateImportAllowCascade bool
)

func runStateImport(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	// Import snapshot
	opts := snapshot.ImportOptions{
		InputPath: stateImportFrom,
		DryRun:    stateImportDryRun,
		IfEmpty:   stateImportIfEmpty,
		Force:     stateImportForce,

		AllowCascade: stateImportAllowCascade,
	}

	result, err := snapshot.Import(database.DB, opts)
	if err != nil {
		// Exit code 4 for conflicts
		if stateImportIfEmpty {
			return exitError(4, err)
		}
		return exitError(1, err)
	}

	// Output result
	if stateImportJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return exitError(1, fmt.Errorf("failed to encode result: %w", err))
		}
	} else {
		if result.DryRun {
			fmt.Printf("✓ Validated snapshot from %s (dry run)\n", result.InputPath)
		} else {
			fmt.Printf("✓ Imported snapshot from %s\n", result.InputPath)
		}
		fmt.Printf("  snapshot_rev: %s\n", result.SnapshotRev)
		fmt.Printf("  containers: %d, tasks: %d, comments: %d, promises: %d, links: %d\n",
			result.ContainerCount, result.TaskCount, result.CommentCount, result.PromiseCount, result.LinkCount)
	}

	return nil
}

// Verify command
var stateVerifyCmd = &cobra.Command{
	Use:   "verify <snapshot-file>",
	Short: "Verify a snapshot is canonical (round-trip deterministic)",
	Long: `Verify checks that a snapshot file is canonical by:

1. Loading the snapshot
2. Re-exporting it to canonical JSON
3. Comparing the bytes

If the snapshot is not byte-identical after canonicalization, verification
fails with exit code 4.`,
	Args: cobra.ExactArgs(1),
	RunE: appctx.WithApp(appctx.DefaultOptions(), runStateVerify),
}

var stateVerifyJSON bool

func runStateVerify(app *appctx.App, cmd *cobra.Command, args []string) error {
	inputPath := args[0]
	database := app.DB

	// Verify snapshot
	result, err := snapshot.Verify(database.DB, inputPath)
	if err != nil {
		return exitError(1, err)
	}

	// Output result
	if stateVerifyJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return exitError(1, fmt.Errorf("failed to encode result: %w", err))
		}
	} else {
		if result.Valid {
			fmt.Printf("✓ %s\n", result.Message)
			fmt.Printf("  snapshot_rev: %s\n", result.SnapshotRev)
		} else {
			fmt.Printf("✗ %s\n", result.Message)
			fmt.Printf("  snapshot_rev: %s\n", result.SnapshotRev)
		}
	}

	if !result.Valid {
		return exitError(4, fmt.Errorf("verification failed"))
	}

	return nil
}
