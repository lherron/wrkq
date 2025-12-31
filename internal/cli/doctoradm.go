package cli

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

var doctorAdmCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check database health and configuration",
	Long:  `Performs health checks on the database, schema, configuration, and attachments. This is an administrative operation.`,
	RunE:  runDoctorAdm,
}

var (
	doctorAdmJSON    bool
	doctorAdmFix     bool
	doctorAdmVerbose bool
)

type checkResultAdm struct {
	Name    string   `json:"name"`
	Status  string   `json:"status"` // "ok", "warning", "error"
	Message string   `json:"message,omitempty"`
	Details []string `json:"details,omitempty"`
}

type doctorReportAdm struct {
	Version       string           `json:"version"`
	DBPath        string           `json:"db_path"`
	Checks        []checkResultAdm `json:"checks"`
	Warnings      int              `json:"warnings"`
	Errors        int              `json:"errors"`
	OverallStatus string           `json:"overall_status"`
}

func init() {
	rootAdmCmd.AddCommand(doctorAdmCmd)
	doctorAdmCmd.Flags().BoolVar(&doctorAdmJSON, "json", false, "Output JSON")
	doctorAdmCmd.Flags().BoolVar(&doctorAdmFix, "fix", false, "Auto-repair issues")
	doctorAdmCmd.Flags().BoolVar(&doctorAdmVerbose, "verbose", false, "Verbose output")
}

func runDoctorAdm(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if dbPath := cmd.Flag("db").Value.String(); dbPath != "" {
		cfg.DBPath = dbPath
	}

	report := &doctorReportAdm{
		Version:       "0.2.0",
		DBPath:        cfg.DBPath,
		Checks:        []checkResultAdm{},
		OverallStatus: "ok",
	}

	// Run checks
	report.Checks = append(report.Checks, checkDatabaseFileAdm(cfg.DBPath)...)

	database, err := db.Open(cfg.DBPath)
	if err == nil {
		defer database.Close()
		report.Checks = append(report.Checks, checkDatabasePragmasAdm(database)...)
		report.Checks = append(report.Checks, checkSchemaAdm(database)...)
		report.Checks = append(report.Checks, checkDataIntegrityAdm(database)...)
		report.Checks = append(report.Checks, checkSequenceDriftAdm(database)...)
		report.Checks = append(report.Checks, checkAttachmentsAdm(database, cfg.AttachDir)...)
		report.Checks = append(report.Checks, checkPerformanceAdm(database)...)
	} else {
		report.Checks = append(report.Checks, checkResultAdm{
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
	if doctorAdmFix && database != nil {
		applyFixesAdm(database, report)
	}

	// Output report
	if doctorAdmJSON {
		return render.RenderJSON(report, false)
	}

	printHumanReportAdm(cmd, report)

	if report.Errors > 0 {
		os.Exit(1)
	}

	return nil
}

func checkDatabaseFileAdm(dbPath string) []checkResultAdm {
	var results []checkResultAdm

	// Check file exists
	info, err := os.Stat(dbPath)
	if err != nil {
		results = append(results, checkResultAdm{
			Name:    "db_file_exists",
			Status:  "error",
			Message: fmt.Sprintf("Database file not found: %s", dbPath),
		})
		return results
	}

	results = append(results, checkResultAdm{
		Name:    "db_file_exists",
		Status:  "ok",
		Message: fmt.Sprintf("Database file: %s (%.1f MB)", dbPath, float64(info.Size())/(1024*1024)),
	})

	// Check file is readable/writable
	f, err := os.OpenFile(dbPath, os.O_RDWR, 0)
	if err != nil {
		results = append(results, checkResultAdm{
			Name:    "db_file_permissions",
			Status:  "error",
			Message: fmt.Sprintf("Database file not writable: %v", err),
		})
	} else {
		f.Close()
		results = append(results, checkResultAdm{
			Name:    "db_file_permissions",
			Status:  "ok",
			Message: "Database file is readable and writable",
		})
	}

	return results
}

func checkDatabasePragmasAdm(database *db.DB) []checkResultAdm {
	var results []checkResultAdm

	// Check WAL mode
	var journalMode string
	database.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if journalMode == "wal" {
		results = append(results, checkResultAdm{
			Name:    "wal_mode",
			Status:  "ok",
			Message: "WAL mode enabled",
		})
	} else {
		results = append(results, checkResultAdm{
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
		results = append(results, checkResultAdm{
			Name:    "foreign_keys",
			Status:  "ok",
			Message: "Foreign keys enabled",
		})
	} else {
		results = append(results, checkResultAdm{
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
		results = append(results, checkResultAdm{
			Name:    "integrity_check",
			Status:  "ok",
			Message: "Database integrity check passed",
		})
	} else {
		results = append(results, checkResultAdm{
			Name:    "integrity_check",
			Status:  "error",
			Message: fmt.Sprintf("Database integrity check failed: %s", integrityCheck),
			Details: []string{"Database may be corrupted", "Restore from backup recommended"},
		})
	}

	return results
}

func checkSchemaAdm(database *db.DB) []checkResultAdm {
	var results []checkResultAdm

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
		results = append(results, checkResultAdm{
			Name:    "schema_tables",
			Status:  "ok",
			Message: fmt.Sprintf("All required tables present (%d/%d)", len(requiredTables), len(requiredTables)),
		})
	} else {
		results = append(results, checkResultAdm{
			Name:    "schema_tables",
			Status:  "error",
			Message: fmt.Sprintf("Missing tables: %v", missingTables),
			Details: []string{"Run migrations to create missing tables"},
		})
	}

	return results
}

func checkDataIntegrityAdm(database *db.DB) []checkResultAdm {
	var results []checkResultAdm

	// Check for orphaned tasks
	var orphanedTasks int
	database.QueryRow(`
		SELECT COUNT(*) FROM tasks
		WHERE project_uuid NOT IN (SELECT uuid FROM containers)
	`).Scan(&orphanedTasks)

	if orphanedTasks == 0 {
		results = append(results, checkResultAdm{
			Name:    "orphaned_tasks",
			Status:  "ok",
			Message: "No orphaned tasks",
		})
	} else {
		results = append(results, checkResultAdm{
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
		results = append(results, checkResultAdm{
			Name:    "orphaned_attachments",
			Status:  "ok",
			Message: "No orphaned attachments",
		})
	} else {
		results = append(results, checkResultAdm{
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
		results = append(results, checkResultAdm{
			Name:    "duplicate_slugs",
			Status:  "ok",
			Message: "No duplicate slugs",
		})
	} else {
		results = append(results, checkResultAdm{
			Name:    "duplicate_slugs",
			Status:  "error",
			Message: fmt.Sprintf("%d duplicate slugs found", duplicateSlugs),
			Details: []string{"Manual intervention required to resolve duplicates"},
		})
	}

	return results
}

func checkSequenceDriftAdm(database *db.DB) []checkResultAdm {
	var results []checkResultAdm

	drifts, err := db.SequenceDrifts(database, db.DefaultSequenceSpecs())
	if err != nil {
		results = append(results, checkResultAdm{
			Name:    "sequence_drift",
			Status:  "error",
			Message: fmt.Sprintf("Failed to check sqlite_sequence drift: %v", err),
		})
		return results
	}

	if len(drifts) == 0 {
		results = append(results, checkResultAdm{
			Name:    "sequence_drift",
			Status:  "ok",
			Message: "All sqlite_sequence values are in sync",
		})
		return results
	}

	details := make([]string, 0, len(drifts))
	for _, drift := range drifts {
		details = append(details, fmt.Sprintf("%s (table %s): sqlite_sequence=%d, max_id=%d", drift.SeqTable, drift.EntityTable, drift.SeqValue, drift.MaxID))
	}

	results = append(results, checkResultAdm{
		Name:    "sequence_drift",
		Status:  "error",
		Message: fmt.Sprintf("Detected sqlite_sequence drift (%d table(s))", len(drifts)),
		Details: details,
	})

	return results
}

func checkAttachmentsAdm(database *db.DB, attachDir string) []checkResultAdm {
	var results []checkResultAdm

	// Check attach_dir exists
	info, err := os.Stat(attachDir)
	if err != nil || !info.IsDir() {
		results = append(results, checkResultAdm{
			Name:    "attach_dir_exists",
			Status:  "error",
			Message: fmt.Sprintf("Attachment directory not found: %s", attachDir),
		})
		return results
	}

	results = append(results, checkResultAdm{
		Name:    "attach_dir_exists",
		Status:  "ok",
		Message: fmt.Sprintf("Attachment directory: %s", attachDir),
	})

	// Check attachment count and size
	var count int
	var totalSize sql.NullInt64
	database.QueryRow("SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM attachments").Scan(&count, &totalSize)

	if count > 0 {
		results = append(results, checkResultAdm{
			Name:    "attachments_count",
			Status:  "ok",
			Message: fmt.Sprintf("%d attachments (%.1f MB total)", count, float64(totalSize.Int64)/(1024*1024)),
		})
	} else {
		results = append(results, checkResultAdm{
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
		results = append(results, checkResultAdm{
			Name:    "orphaned_files",
			Status:  "ok",
			Message: "No orphaned attachment directories",
		})
	} else {
		results = append(results, checkResultAdm{
			Name:    "orphaned_files",
			Status:  "warning",
			Message: fmt.Sprintf("%d orphaned attachment directories", orphanedDirs),
			Details: []string{"Use --fix to remove orphaned directories"},
		})
	}

	return results
}

func checkPerformanceAdm(database *db.DB) []checkResultAdm {
	var results []checkResultAdm

	// Count tasks
	var activeTasks, archivedTasks int
	database.QueryRow("SELECT COUNT(*) FROM tasks WHERE state != 'archived'").Scan(&activeTasks)
	database.QueryRow("SELECT COUNT(*) FROM tasks WHERE state = 'archived'").Scan(&archivedTasks)

	results = append(results, checkResultAdm{
		Name:    "task_counts",
		Status:  "ok",
		Message: fmt.Sprintf("%d active tasks, %d archived", activeTasks, archivedTasks),
	})

	// Count containers
	var containers int
	database.QueryRow("SELECT COUNT(*) FROM containers").Scan(&containers)

	results = append(results, checkResultAdm{
		Name:    "container_count",
		Status:  "ok",
		Message: fmt.Sprintf("%d containers", containers),
	})

	// Database size
	var pageCount, pageSize int64
	database.QueryRow("PRAGMA page_count").Scan(&pageCount)
	database.QueryRow("PRAGMA page_size").Scan(&pageSize)
	dbSize := pageCount * pageSize

	results = append(results, checkResultAdm{
		Name:    "database_size",
		Status:  "ok",
		Message: fmt.Sprintf("Database size: %.1f MB (%d pages)", float64(dbSize)/(1024*1024), pageCount),
	})

	return results
}

func applyFixesAdm(database *db.DB, report *doctorReportAdm) {
	var outputs []string

	if drifts, err := db.FixSequenceDrifts(database, db.DefaultSequenceSpecs()); err != nil {
		outputs = append(outputs, fmt.Sprintf("Sequence repair failed: %v", err))
	} else if len(drifts) > 0 {
		outputs = append(outputs, fmt.Sprintf("Fixed sqlite_sequence drift for %d table(s)", len(drifts)))
	} else {
		outputs = append(outputs, "No sqlite_sequence drift detected")
	}

	if len(outputs) > 0 {
		fmt.Fprintln(os.Stdout, "\n--fix results")
		fmt.Fprintln(os.Stdout, strings.Join(outputs, "\n"))
	}
}

func printHumanReportAdm(cmd *cobra.Command, report *doctorReportAdm) {
	fmt.Fprintf(cmd.OutOrStdout(), "wrkqadm doctor v%s\n\n", report.Version)
	fmt.Fprintf(cmd.OutOrStdout(), "Database: %s\n\n", report.DBPath)

	// Group checks by category
	categories := map[string][]checkResultAdm{
		"Database File":   {},
		"Database Health": {},
		"Schema":          {},
		"Data Integrity":  {},
		"Sequences":       {},
		"Attachments":     {},
		"Performance":     {},
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
		case "sequence_drift":
			categories["Sequences"] = append(categories["Sequences"], check)
		case "attach_dir_exists", "attachments_count", "orphaned_files":
			categories["Attachments"] = append(categories["Attachments"], check)
		case "task_counts", "container_count", "database_size":
			categories["Performance"] = append(categories["Performance"], check)
		}
	}

	// Print each category
	for _, category := range []string{"Database File", "Database Health", "Schema", "Data Integrity", "Sequences", "Attachments", "Performance"} {
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

			if doctorAdmVerbose && len(check.Details) > 0 {
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
