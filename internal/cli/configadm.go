package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/spf13/cobra"
)

var configAdmCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration management and introspection",
	Long:  `Commands for inspecting and validating configuration. These are administrative operations.`,
}

var configDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Show effective configuration and validate settings",
	Long: `Displays the effective configuration values and their sources, and validates
that all required settings are correctly configured.`,
	RunE: runConfigDoctor,
}

var (
	configDoctorJSON bool
)

type configValue struct {
	Value  string `json:"value"`
	Source string `json:"source"`
	Valid  bool   `json:"valid"`
	Note   string `json:"note,omitempty"`
}

type configDoctorReport struct {
	Config   map[string]configValue `json:"config"`
	Warnings []string               `json:"warnings"`
}

func init() {
	rootAdmCmd.AddCommand(configAdmCmd)
	configAdmCmd.AddCommand(configDoctorCmd)

	configDoctorCmd.Flags().BoolVar(&configDoctorJSON, "json", false, "Output as JSON")
}

func runConfigDoctor(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	report := &configDoctorReport{
		Config:   make(map[string]configValue),
		Warnings: []string{},
	}

	// Check DB path
	dbSource := "config file"
	if os.Getenv("WRKQ_DB_PATH") != "" {
		dbSource = "environment variable WRKQ_DB_PATH"
	}
	if os.Getenv("WRKQ_DB_PATH_FILE") != "" {
		dbSource = "environment variable WRKQ_DB_PATH_FILE"
	}
	if dbFlag := cmd.Flag("db"); dbFlag != nil && dbFlag.Changed {
		dbSource = "command-line flag --db"
	}

	dbValid := false
	dbNote := ""
	if _, err := os.Stat(cfg.DBPath); err == nil {
		dbValid = true

		// Check if it's a valid SQLite database
		database, err := db.Open(cfg.DBPath)
		if err != nil {
			dbNote = fmt.Sprintf("File exists but failed to open: %v", err)
			dbValid = false
		} else {
			defer func() { _ = database.Close() }()

			// Check WAL mode
			var journalMode string
			_ = database.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
			if journalMode != "wal" {
				report.Warnings = append(report.Warnings, "Database is not in WAL mode")
			}
		}
	} else {
		dbNote = "File does not exist"
		report.Warnings = append(report.Warnings, "Database file does not exist - run 'wrkqadm init' to create it")
	}

	report.Config["db_path"] = configValue{
		Value:  cfg.DBPath,
		Source: dbSource,
		Valid:  dbValid,
		Note:   dbNote,
	}

	// Check attach dir
	attachDirSource := "config file"
	if os.Getenv("WRKQ_ATTACH_DIR") != "" {
		attachDirSource = "environment variable WRKQ_ATTACH_DIR"
	}

	attachDirValid := false
	attachDirNote := ""
	if info, err := os.Stat(cfg.AttachDir); err == nil {
		if info.IsDir() {
			attachDirValid = true
		} else {
			attachDirNote = "Path exists but is not a directory"
			report.Warnings = append(report.Warnings, "Attachment path is not a directory")
		}
	} else {
		attachDirNote = "Directory does not exist"
		report.Warnings = append(report.Warnings, "Attachment directory does not exist")
	}

	report.Config["attach_dir"] = configValue{
		Value:  cfg.AttachDir,
		Source: attachDirSource,
		Valid:  attachDirValid,
		Note:   attachDirNote,
	}

	// Check project root
	projectRootSource := "config file"
	if os.Getenv("WRKQ_PROJECT_ROOT") != "" {
		projectRootSource = "environment variable WRKQ_PROJECT_ROOT"
	}

	projectRootValue := cfg.ProjectRoot
	if projectRootValue == "" {
		projectRootValue = "(not set)"
	}

	report.Config["project_root"] = configValue{
		Value:  projectRootValue,
		Source: projectRootSource,
		Valid:  true,
	}

	// Check acting principal. WRKQ_ACTOR/WRKQ_ACTOR_ID remain compatibility aliases.
	actorSource := "default_actor compatibility setting"
	actorValue := cfg.GetActorID()
	if principal := os.Getenv("WRKQ_PRINCIPAL_REF"); principal != "" {
		actorValue = principal
		actorSource = "environment variable WRKQ_PRINCIPAL_REF"
	}
	if actorValue == "" {
		actorValue = "(not set)"
	}

	if os.Getenv("WRKQ_PRINCIPAL_REF") != "" {
		// Canonical principal env already set source/value above.
	} else if os.Getenv("WRKQ_ACTOR") != "" {
		actorSource = "environment variable WRKQ_ACTOR"
	} else if os.Getenv("WRKQ_ACTOR_ID") != "" {
		actorSource = "environment variable WRKQ_ACTOR_ID"
	}
	if asFlag := cmd.Flag("as"); asFlag != nil && asFlag.Changed {
		actorSource = "command-line flag --as"
	}

	actorValid := false
	actorNote := ""

	if actorValue != "(not set)" {
		var principalRef string
		var err error
		if actorSource == "environment variable WRKQ_PRINCIPAL_REF" {
			principalRef, err = attribution.NormalizeCanonical(actorValue)
		} else {
			principalRef, err = attribution.NormalizeCompat(actorValue)
		}
		if err == nil {
			actorValid = true
			actorNote = fmt.Sprintf("Normalizes to principal %s", principalRef)
		} else {
			actorNote = fmt.Sprintf("Invalid principal value: %v", err)
			report.Warnings = append(report.Warnings, fmt.Sprintf("Principal value %q is invalid", actorValue))
		}
	} else if actorValue == "(not set)" {
		report.Warnings = append(report.Warnings, "No principal configured - set WRKQ_PRINCIPAL_REF, WRKQ_ACTOR, --as, or a valid ASP scope")
	}

	report.Config["actor"] = configValue{
		Value:  actorValue,
		Source: actorSource,
		Valid:  actorValid,
		Note:   actorNote,
	}

	// Output report
	if configDoctorJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}

	// Human-readable output
	fmt.Fprintln(cmd.OutOrStdout(), "Configuration Report")
	fmt.Fprintln(cmd.OutOrStdout(), "====================")
	fmt.Fprintln(cmd.OutOrStdout())

	fmt.Fprintln(cmd.OutOrStdout(), "Database:")
	fmt.Fprintf(cmd.OutOrStdout(), "  WRKQ_DB_PATH: %s\n", report.Config["db_path"].Value)
	fmt.Fprintf(cmd.OutOrStdout(), "    Source: %s\n", report.Config["db_path"].Source)
	if report.Config["db_path"].Valid {
		fmt.Fprintln(cmd.OutOrStdout(), "    Status: ✓ Valid")
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "    Status: ✗ %s\n", report.Config["db_path"].Note)
	}
	fmt.Fprintln(cmd.OutOrStdout())

	fmt.Fprintln(cmd.OutOrStdout(), "Attachments:")
	fmt.Fprintf(cmd.OutOrStdout(), "  WRKQ_ATTACH_DIR: %s\n", report.Config["attach_dir"].Value)
	fmt.Fprintf(cmd.OutOrStdout(), "    Source: %s\n", report.Config["attach_dir"].Source)
	if report.Config["attach_dir"].Valid {
		fmt.Fprintln(cmd.OutOrStdout(), "    Status: ✓ Valid")
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "    Status: ✗ %s\n", report.Config["attach_dir"].Note)
	}
	fmt.Fprintln(cmd.OutOrStdout())

	fmt.Fprintln(cmd.OutOrStdout(), "Project Root:")
	fmt.Fprintf(cmd.OutOrStdout(), "  WRKQ_PROJECT_ROOT: %s\n", report.Config["project_root"].Value)
	fmt.Fprintf(cmd.OutOrStdout(), "    Source: %s\n", report.Config["project_root"].Source)
	fmt.Fprintln(cmd.OutOrStdout(), "    Status: ✓ Loaded")
	fmt.Fprintln(cmd.OutOrStdout())

	fmt.Fprintln(cmd.OutOrStdout(), "Actor:")
	fmt.Fprintf(cmd.OutOrStdout(), "  Principal: %s\n", report.Config["actor"].Value)
	fmt.Fprintf(cmd.OutOrStdout(), "    Source: %s\n", report.Config["actor"].Source)
	if report.Config["actor"].Valid {
		fmt.Fprintf(cmd.OutOrStdout(), "    Status: ✓ %s\n", report.Config["actor"].Note)
	} else {
		if report.Config["actor"].Note != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "    Status: ✗ %s\n", report.Config["actor"].Note)
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), "    Status: ✗ Not configured")
		}
	}
	fmt.Fprintln(cmd.OutOrStdout())

	// Warnings
	if len(report.Warnings) > 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Warnings:")
		for _, warning := range report.Warnings {
			fmt.Fprintf(cmd.OutOrStdout(), "  ⚠  %s\n", warning)
		}
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "✓ No warnings")
	}

	return nil
}
