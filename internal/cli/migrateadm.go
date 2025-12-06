package cli

import (
	"fmt"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/spf13/cobra"
)

var migrateAdmCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Run any pending database migrations",
	Long: `Migrate applies any pending SQL migrations to the database.

Migrations are embedded in the wrkq binary and tracked via the schema_migrations
table. Each migration file (e.g., 000001_baseline.sql) is applied exactly once.

This command is safe to run multiple times - it only applies migrations that
haven't been applied yet.

Use --dry-run to see which migrations would be applied without running them.
Use --status to show the current migration status.`,
	RunE: runMigrateAdm,
}

var (
	migrateDryRun bool
	migrateStatus bool
)

func init() {
	rootAdmCmd.AddCommand(migrateAdmCmd)

	migrateAdmCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "Show which migrations would be applied without running them")
	migrateAdmCmd.Flags().BoolVar(&migrateStatus, "status", false, "Show current migration status")
}

func runMigrateAdm(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return exitError(1, fmt.Errorf("failed to load config: %w", err))
	}

	// Use database path from flag if provided
	dbPathFlag := cmd.Flag("db").Value.String()
	if dbPathFlag != "" {
		cfg.DBPath = dbPathFlag
	}

	if cfg.DBPath == "" {
		return exitError(2, fmt.Errorf("database path not specified (use --db flag or set WRKQ_DB_PATH)"))
	}

	// Open database
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return exitError(1, fmt.Errorf("failed to open database: %w", err))
	}
	defer database.Close()

	// Handle --status flag
	if migrateStatus {
		return showMigrationStatus(database)
	}

	// Handle --dry-run flag
	if migrateDryRun {
		return showPendingMigrations(database)
	}

	// Run migrations
	applied, err := database.MigrateWithInfo()
	if err != nil {
		return exitError(1, fmt.Errorf("failed to run migrations: %w", err))
	}

	if len(applied) == 0 {
		fmt.Println("Database is up to date. No migrations to apply.")
	} else {
		for _, m := range applied {
			fmt.Printf("✓ Applied migration: %s\n", m)
		}
		fmt.Printf("\nApplied %d migration(s).\n", len(applied))
	}

	return nil
}

func showMigrationStatus(database *db.DB) error {
	applied, pending, err := database.MigrationStatus()
	if err != nil {
		return exitError(1, fmt.Errorf("failed to get migration status: %w", err))
	}

	if len(applied) == 0 && len(pending) == 0 {
		fmt.Println("No migrations found.")
		return nil
	}

	if len(applied) > 0 {
		fmt.Println("Applied migrations:")
		for _, m := range applied {
			fmt.Printf("  ✓ %s\n", m)
		}
	}

	if len(pending) > 0 {
		if len(applied) > 0 {
			fmt.Println()
		}
		fmt.Println("Pending migrations:")
		for _, m := range pending {
			fmt.Printf("  ○ %s\n", m)
		}
	}

	return nil
}

func showPendingMigrations(database *db.DB) error {
	_, pending, err := database.MigrationStatus()
	if err != nil {
		return exitError(1, fmt.Errorf("failed to get migration status: %w", err))
	}

	if len(pending) == 0 {
		fmt.Println("No pending migrations. Database is up to date.")
		return nil
	}

	fmt.Println("Pending migrations (would be applied):")
	for _, m := range pending {
		fmt.Printf("  ○ %s\n", m)
	}
	fmt.Printf("\nTotal: %d migration(s) would be applied.\n", len(pending))

	return nil
}
