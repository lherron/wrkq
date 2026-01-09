package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/spf13/cobra"
)

var dbAdmCmd = &cobra.Command{
	Use:   "db",
	Short: "Database lifecycle operations",
	Long:  `Commands for database snapshot, backup, and maintenance operations. These are administrative operations.`,
}

var dbSnapshotCmd = &cobra.Command{
	Use:   "snapshot",
	Short: "Create a WAL-safe database snapshot",
	Long: `Creates a consistent point-in-time snapshot of the SQLite database using
SQLite's online backup API. The snapshot is immediately usable without WAL/SHM files.

This command is intended for creating ephemeral working copies for agents and CI.`,
	RunE: runDBSnapshot,
}

var (
	dbSnapshotOut  string
	dbSnapshotJSON bool
)

type snapshotManifest struct {
	Timestamp               string `json:"timestamp"`
	SourceDBPath            string `json:"source_db_path"`
	SnapshotDBPath          string `json:"snapshot_db_path"`
	MachineInterfaceVersion int    `json:"machine_interface_version"`
}

func init() {
	rootAdmCmd.AddCommand(dbAdmCmd)
	dbAdmCmd.AddCommand(dbSnapshotCmd)

	dbSnapshotCmd.Flags().StringVar(&dbSnapshotOut, "out", "", "Output path for snapshot database (required)")
	dbSnapshotCmd.Flags().BoolVar(&dbSnapshotJSON, "json", false, "Output JSON manifest")
	dbSnapshotCmd.MarkFlagRequired("out")
}

func runDBSnapshot(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Override DB path from flag if provided
	if dbPath := cmd.Flag("db").Value.String(); dbPath != "" {
		cfg.DBPath = dbPath
	}

	// Validate source database exists
	if _, err := os.Stat(cfg.DBPath); err != nil {
		return fmt.Errorf("source database not found: %w", err)
	}

	// Validate output path doesn't exist (or confirm overwrite)
	if _, err := os.Stat(dbSnapshotOut); err == nil {
		return fmt.Errorf("output file already exists: %s (remove it first or choose a different path)", dbSnapshotOut)
	}

	// Open source database
	sourceDB, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open source database: %w", err)
	}
	defer sourceDB.Close()

	// Perform online backup using VACUUM INTO
	// This creates a clean, optimized copy without WAL/SHM files
	_, err = sourceDB.Exec(fmt.Sprintf("VACUUM INTO '%s'", dbSnapshotOut))
	if err != nil {
		// Clean up failed snapshot
		os.Remove(dbSnapshotOut)
		return fmt.Errorf("failed to create snapshot: %w", err)
	}

	// Create manifest
	manifest := snapshotManifest{
		Timestamp:               time.Now().UTC().Format(time.RFC3339),
		SourceDBPath:            cfg.DBPath,
		SnapshotDBPath:          dbSnapshotOut,
		MachineInterfaceVersion: 1,
	}

	// Output
	if dbSnapshotJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(manifest)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✓ Created snapshot: %s\n", dbSnapshotOut)
	fmt.Fprintf(cmd.OutOrStdout(), "  Source: %s\n", cfg.DBPath)
	fmt.Fprintf(cmd.OutOrStdout(), "  Timestamp: %s\n", manifest.Timestamp)
	fmt.Fprintf(cmd.OutOrStdout(), "\nTo use this snapshot:\n")
	fmt.Fprintf(cmd.OutOrStdout(), "  export WRKQ_DB_PATH=%s\n", dbSnapshotOut)
	fmt.Fprintf(cmd.OutOrStdout(), "  export WRKQ_ATTACH_DIR=/path/to/branch/attachments\n")
	fmt.Fprintf(cmd.OutOrStdout(), "  export WRKQ_ACTOR=agent-branch-name\n")

	return nil
}
