package cli

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check database health and configuration",
	Long:  `Performs health checks on the database, schema, configuration, and attachments.`,
	RunE:  runDoctor,
}

var (
	doctorJSON    bool
	doctorFix     bool
	doctorVerbose bool
)

type checkResult struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"` // "ok", "warning", "error"
	Message string   `json:"message,omitempty"`
	Details []string `json:"details,omitempty"`
}

type doctorReport struct {
	Version     string        `json:"version"`
	DBPath      string        `json:"db_path"`
	Checks      []checkResult `json:"checks"`
	Warnings    int           `json:"warnings"`
	Errors      int           `json:"errors"`
	OverallStatus string      `json:"overall_status"`
}

func init() {
	rootCmd.AddCommand(doctorCmd)
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "Output JSON")
	doctorCmd.Flags().BoolVar(&doctorFix, "fix", false, "Auto-repair issues")
	doctorCmd.Flags().BoolVar(&doctorVerbose, "verbose", false, "Verbose output")
}

func runDoctor(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if dbPath := cmd.Flag("db").Value.String(); dbPath != "" {
		cfg.DBPath = dbPath
	}

	report := &doctorReport{
		Version:       "0.2.0",
		DBPath:        cfg.DBPath,
		Checks:        []checkResult{},
		OverallStatus: "ok",
	}

	// Run checks
	report.Checks = append(report.Checks, checkDatabaseFile(cfg.DBPath)...)

	database, err := db.Open(cfg.DBPath)
	if err == nil {
		defer database.Close()
		report.Checks = append(report.Checks, checkDatabasePragmas(database)...)
		report.Checks = append(report.Checks, checkSchema(database)...)
		report.Checks = append(report.Checks, checkDataIntegrity(database)...)
		report.Checks = append(report.Checks, checkAttachments(database, cfg.AttachDir)...)
		report.Checks = append(report.Checks, checkPerformance(database)...)
	} else {
		report.Checks = append(report.Checks, checkResult{
			Name:    "database_open",
			Status:  "error",
			Message: fmt.Sprintf("Failed to open database: %v", err),
		})
	}

	// Count warnings and errors
	for _, check := range report.Checks {
		if check.Status == "warning" {
			report.Warnings++
		} else if check.Status == "error" {
			report.Errors++
			report.OverallStatus = "error"
		}
	}

	if report.Warnings > 0 && report.OverallStatus == "ok" {
		report.OverallStatus = "warning"
	}

	// Apply fixes if requested
	if doctorFix && database != nil {
		applyFixes(database, report)
	}

	// Output report
	if doctorJSON {
		return render.RenderJSON(report, false)
	}

	printHumanReport(cmd, report)

	if report.Errors > 0 {
		os.Exit(1)
	}

	return nil
}

func checkDatabaseFile(dbPath string) []checkResult {
	var results []checkResult

	// Check file exists
	info, err := os.Stat(dbPath)
	if err != nil {
		results = append(results, checkResult{
			Name:    "db_file_exists",
			Status:  "error",
			Message: fmt.Sprintf("Database file not found: %s", dbPath),
		})
		return results
	}

	results = append(results, checkResult{
		Name:    "db_file_exists",
		Status:  "ok",
		Message: fmt.Sprintf("Database file: %s (%.1f MB)", dbPath, float64(info.Size())/(1024*1024)),
	})

	// Check file is readable/writable
	f, err := os.OpenFile(dbPath, os.O_RDWR, 0)
	if err != nil {
		results = append(results, checkResult{
			Name:    "db_file_permissions",
			Status:  "error",
			Message: fmt.Sprintf("Database file not writable: %v", err),
		})
	} else {
		f.Close()
		results = append(results, checkResult{
			Name:    "db_file_permissions",
			Status:  "ok",
			Message: "Database file is readable and writable",
		})
	}

	return results
}

func checkDatabasePragmas(database *db.DB) []checkResult {
	var results []checkResult

	// Check WAL mode
	var journalMode string
	database.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if journalMode == "wal" {
		results = append(results, checkResult{
			Name:    "wal_mode",
			Status:  "ok",
			Message: "WAL mode enabled",
		})
	} else {
		results = append(results, checkResult{
			Name:    "wal_mode",
			Status:  "warning",
			Message: fmt.Sprintf("WAL mode not enabled (current: %s)", journalMode),
			Details: []string{"Run 'PRAGMA journal_mode=WAL' to enable"},
		})
	}

	// Check foreign keys
	var foreignKeys int
	database.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys)
	if foreignKeys == 1 {
		results = append(results, checkResult{
			Name:    "foreign_keys",
			Status:  "ok",
			Message: "Foreign keys enabled",
		})
	} else {
		results = append(results, checkResult{
			Name:    "foreign_keys",
			Status:  "error",
			Message: "Foreign keys not enabled",
			Details: []string{"Critical: foreign key constraints are not enforced"},
		})
	}

	// Check integrity
	var integrityCheck string
	database.QueryRow("PRAGMA integrity_check").Scan(&integrityCheck)
	if integrityCheck == "ok" {
		results = append(results, checkResult{
			Name:    "integrity_check",
			Status:  "ok",
			Message: "Database integrity check passed",
		})
	} else {
		results = append(results, checkResult{
			Name:    "integrity_check",
			Status:  "error",
			Message: fmt.Sprintf("Database integrity check failed: %s", integrityCheck),
			Details: []string{"Database may be corrupted", "Restore from backup recommended"},
		})
	}

	return results
}

func checkSchema(database *db.DB) []checkResult {
	var results []checkResult

	// Check required tables exist
	requiredTables := []string{"actors", "containers", "tasks", "event_log", "attachments"}
	var missingTables []string

	for _, table := range requiredTables {
		var count int
		err := database.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count)
		if err != nil || count == 0 {
			missingTables = append(missingTables, table)
		}
	}

	if len(missingTables) == 0 {
		results = append(results, checkResult{
			Name:    "schema_tables",
			Status:  "ok",
			Message: fmt.Sprintf("All required tables present (%d/%d)", len(requiredTables), len(requiredTables)),
		})
	} else {
		results = append(results, checkResult{
			Name:    "schema_tables",
			Status:  "error",
			Message: fmt.Sprintf("Missing tables: %v", missingTables),
			Details: []string{"Run migrations to create missing tables"},
		})
	}

	return results
}

func checkDataIntegrity(database *db.DB) []checkResult {
	var results []checkResult

	// Check for orphaned tasks
	var orphanedTasks int
	database.QueryRow(`
		SELECT COUNT(*) FROM tasks
		WHERE project_uuid NOT IN (SELECT uuid FROM containers)
	`).Scan(&orphanedTasks)

	if orphanedTasks == 0 {
		results = append(results, checkResult{
			Name:    "orphaned_tasks",
			Status:  "ok",
			Message: "No orphaned tasks",
		})
	} else {
		results = append(results, checkResult{
			Name:    "orphaned_tasks",
			Status:  "warning",
			Message: fmt.Sprintf("%d tasks reference non-existent containers", orphanedTasks),
			Details: []string{"Use --fix to remove orphaned tasks"},
		})
	}

	// Check for orphaned attachments
	var orphanedAttachments int
	database.QueryRow(`
		SELECT COUNT(*) FROM attachments
		WHERE task_uuid NOT IN (SELECT uuid FROM tasks)
	`).Scan(&orphanedAttachments)

	if orphanedAttachments == 0 {
		results = append(results, checkResult{
			Name:    "orphaned_attachments",
			Status:  "ok",
			Message: "No orphaned attachments",
		})
	} else {
		results = append(results, checkResult{
			Name:    "orphaned_attachments",
			Status:  "warning",
			Message: fmt.Sprintf("%d attachments reference non-existent tasks", orphanedAttachments),
			Details: []string{"Use --fix to remove orphaned attachments"},
		})
	}

	// Check for duplicate slugs
	var duplicateSlugs int
	database.QueryRow(`
		SELECT COUNT(*) FROM (
			SELECT project_uuid, slug, COUNT(*) as cnt
			FROM tasks
			GROUP BY project_uuid, slug
			HAVING cnt > 1
		)
	`).Scan(&duplicateSlugs)

	if duplicateSlugs == 0 {
		results = append(results, checkResult{
			Name:    "duplicate_slugs",
			Status:  "ok",
			Message: "No duplicate slugs",
		})
	} else {
		results = append(results, checkResult{
			Name:    "duplicate_slugs",
			Status:  "error",
			Message: fmt.Sprintf("%d duplicate slugs found", duplicateSlugs),
			Details: []string{"Manual intervention required to resolve duplicates"},
		})
	}

	return results
}

func checkAttachments(database *db.DB, attachDir string) []checkResult {
	var results []checkResult

	// Check attach_dir exists
	info, err := os.Stat(attachDir)
	if err != nil || !info.IsDir() {
		results = append(results, checkResult{
			Name:    "attach_dir_exists",
			Status:  "error",
			Message: fmt.Sprintf("Attachment directory not found: %s", attachDir),
		})
		return results
	}

	results = append(results, checkResult{
		Name:    "attach_dir_exists",
		Status:  "ok",
		Message: fmt.Sprintf("Attachment directory: %s", attachDir),
	})

	// Check attachment count and size
	var count int
	var totalSize sql.NullInt64
	database.QueryRow("SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM attachments").Scan(&count, &totalSize)

	if count > 0 {
		results = append(results, checkResult{
			Name:    "attachments_count",
			Status:  "ok",
			Message: fmt.Sprintf("%d attachments (%.1f MB total)", count, float64(totalSize.Int64)/(1024*1024)),
		})
	} else {
		results = append(results, checkResult{
			Name:    "attachments_count",
			Status:  "ok",
			Message: "No attachments",
		})
	}

	// Check for orphaned files
	tasksDir := filepath.Join(attachDir, "tasks")
	orphanedDirs := 0
	if _, err := os.Stat(tasksDir); err == nil {
		entries, _ := os.ReadDir(tasksDir)
		for _, entry := range entries {
			if entry.IsDir() {
				taskUUID := entry.Name()
				var exists int
				database.QueryRow("SELECT COUNT(*) FROM tasks WHERE uuid = ?", taskUUID).Scan(&exists)
				if exists == 0 {
					orphanedDirs++
				}
			}
		}
	}

	if orphanedDirs == 0 {
		results = append(results, checkResult{
			Name:    "orphaned_files",
			Status:  "ok",
			Message: "No orphaned attachment directories",
		})
	} else {
		results = append(results, checkResult{
			Name:    "orphaned_files",
			Status:  "warning",
			Message: fmt.Sprintf("%d orphaned attachment directories", orphanedDirs),
			Details: []string{"Use --fix to remove orphaned directories"},
		})
	}

	return results
}

func checkPerformance(database *db.DB) []checkResult {
	var results []checkResult

	// Count tasks
	var activeTasks, archivedTasks int
	database.QueryRow("SELECT COUNT(*) FROM tasks WHERE state != 'archived'").Scan(&activeTasks)
	database.QueryRow("SELECT COUNT(*) FROM tasks WHERE state = 'archived'").Scan(&archivedTasks)

	results = append(results, checkResult{
		Name:    "task_counts",
		Status:  "ok",
		Message: fmt.Sprintf("%d active tasks, %d archived", activeTasks, archivedTasks),
	})

	// Count containers
	var containers int
	database.QueryRow("SELECT COUNT(*) FROM containers").Scan(&containers)

	results = append(results, checkResult{
		Name:    "container_count",
		Status:  "ok",
		Message: fmt.Sprintf("%d containers", containers),
	})

	// Database size
	var pageCount, pageSize int64
	database.QueryRow("PRAGMA page_count").Scan(&pageCount)
	database.QueryRow("PRAGMA page_size").Scan(&pageSize)
	dbSize := pageCount * pageSize

	results = append(results, checkResult{
		Name:    "database_size",
		Status:  "ok",
		Message: fmt.Sprintf("Database size: %.1f MB (%d pages)", float64(dbSize)/(1024*1024), pageCount),
	})

	return results
}

func applyFixes(database *db.DB, report *doctorReport) {
	// For now, just report that --fix is not yet implemented
	fmt.Println("\n--fix flag is not yet fully implemented")
	fmt.Println("Future version will auto-repair safe issues")
}

func printHumanReport(cmd *cobra.Command, report *doctorReport) {
	fmt.Fprintf(cmd.OutOrStdout(), "wrkq doctor v%s\n\n", report.Version)
	fmt.Fprintf(cmd.OutOrStdout(), "Database: %s\n\n", report.DBPath)

	// Group checks by category
	categories := map[string][]checkResult{
		"Database File":    {},
		"Database Health":  {},
		"Schema":           {},
		"Data Integrity":   {},
		"Attachments":      {},
		"Performance":      {},
	}

	for _, check := range report.Checks {
		switch check.Name {
		case "db_file_exists", "db_file_permissions":
			categories["Database File"] = append(categories["Database File"], check)
		case "wal_mode", "foreign_keys", "integrity_check":
			categories["Database Health"] = append(categories["Database Health"], check)
		case "schema_tables":
			categories["Schema"] = append(categories["Schema"], check)
		case "orphaned_tasks", "orphaned_attachments", "duplicate_slugs":
			categories["Data Integrity"] = append(categories["Data Integrity"], check)
		case "attach_dir_exists", "attachments_count", "orphaned_files":
			categories["Attachments"] = append(categories["Attachments"], check)
		case "task_counts", "container_count", "database_size":
			categories["Performance"] = append(categories["Performance"], check)
		}
	}

	// Print each category
	for _, category := range []string{"Database File", "Database Health", "Schema", "Data Integrity", "Attachments", "Performance"} {
		checks := categories[category]
		if len(checks) == 0 {
			continue
		}

		fmt.Fprintf(cmd.OutOrStdout(), "%s\n", category)
		for _, check := range checks {
			icon := "✓"
			if check.Status == "warning" {
				icon = "⚠"
			} else if check.Status == "error" {
				icon = "✗"
			}

			fmt.Fprintf(cmd.OutOrStdout(), "  %s %s\n", icon, check.Message)

			if doctorVerbose && len(check.Details) > 0 {
				for _, detail := range check.Details {
					fmt.Fprintf(cmd.OutOrStdout(), "      %s\n", detail)
				}
			}
		}
		fmt.Fprintln(cmd.OutOrStdout())
	}

	// Summary
	if report.Errors > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Summary: %d error(s), %d warning(s)\n", report.Errors, report.Warnings)
	} else if report.Warnings > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Summary: %d warning(s)\n", report.Warnings)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Summary: All checks passed ✓\n")
	}

	if report.Warnings > 0 || report.Errors > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "\nRun with --verbose for detailed information\n")
	}
}
