package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/snapshot"
	"github.com/spf13/cobra"
)

var stateAdmCmd = &cobra.Command{
	Use:   "state",
	Short: "Manage canonical state snapshots",
	Long: `Commands for exporting, importing, and verifying canonical JSON
state snapshots of the wrkq database.

Snapshots are deterministic JSON representations of the entire database,
designed for use in patch-first Git workflows.`,
}

// Export command
var stateExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export the database to a canonical JSON snapshot",
	Long: `Export reads the current database and produces a canonical JSON snapshot.

The snapshot includes all actors, containers, tasks, and comments in a
deterministic format suitable for diffing and version control.

Canonicalization ensures byte-for-byte identical output for the same
database state (sorted keys, no insignificant whitespace, sorted arrays).`,
	RunE: runStateExport,
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
	stateImportCmd.Flags().BoolVar(&stateImportIfEmpty, "if-empty", false, "Require database to be empty")
	stateImportCmd.Flags().BoolVar(&stateImportForce, "force", false, "Truncate existing data before import")
	stateImportCmd.Flags().BoolVar(&stateImportJSON, "json", false, "Output result as JSON")

	// Verify flags
	stateVerifyCmd.Flags().BoolVar(&stateVerifyJSON, "json", false, "Output result as JSON")
}

func runStateExport(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return exitError(1, fmt.Errorf("failed to load config: %w", err))
	}

	// Override DB path from flag if provided
	dbPathFlag := cmd.Flag("db").Value.String()
	if dbPathFlag != "" {
		cfg.DBPath = dbPathFlag
	}

	// Open database
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return exitError(1, fmt.Errorf("failed to open database: %w", err))
	}
	defer database.Close()

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
		fmt.Printf("  actors: %d, containers: %d, tasks: %d, comments: %d\n",
			result.ActorCount, result.ContainerCount, result.TaskCount, result.CommentCount)
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
	Long: `Import reads a canonical JSON snapshot and hydrates the database.

By default, import requires the database to be essentially empty (only
seeded defaults). Use --force to truncate existing data before import.

Use --dry-run to validate the snapshot without writing to the database.`,
	RunE: runStateImport,
}

var (
	stateImportFrom    string
	stateImportDryRun  bool
	stateImportIfEmpty bool
	stateImportForce   bool
	stateImportJSON    bool
)

func runStateImport(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return exitError(1, fmt.Errorf("failed to load config: %w", err))
	}

	// Override DB path from flag if provided
	dbPathFlag := cmd.Flag("db").Value.String()
	if dbPathFlag != "" {
		cfg.DBPath = dbPathFlag
	}

	// Open database
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return exitError(1, fmt.Errorf("failed to open database: %w", err))
	}
	defer database.Close()

	// Import snapshot
	opts := snapshot.ImportOptions{
		InputPath: stateImportFrom,
		DryRun:    stateImportDryRun,
		IfEmpty:   stateImportIfEmpty,
		Force:     stateImportForce,
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
		fmt.Printf("  actors: %d, containers: %d, tasks: %d, comments: %d\n",
			result.ActorCount, result.ContainerCount, result.TaskCount, result.CommentCount)
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
	RunE: runStateVerify,
}

var stateVerifyJSON bool

func runStateVerify(cmd *cobra.Command, args []string) error {
	inputPath := args[0]

	// Load configuration (needed for potential DB operations)
	cfg, err := config.Load()
	if err != nil {
		return exitError(1, fmt.Errorf("failed to load config: %w", err))
	}

	// Override DB path from flag if provided
	dbPathFlag := cmd.Flag("db").Value.String()
	if dbPathFlag != "" {
		cfg.DBPath = dbPathFlag
	}

	// Open database (for potential future use in verify)
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return exitError(1, fmt.Errorf("failed to open database: %w", err))
	}
	defer database.Close()

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
