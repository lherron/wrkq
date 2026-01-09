package cli

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/spf13/cobra"
)

//go:embed embedded/WRKQ-USAGE.md
var wrkqUsageContent string

//go:embed embedded/AGENT-WRKQ-USAGE.md
var agentUsageContent string

var initAdmCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the wrkq database and configuration",
	Long: `Initialize creates the SQLite database, runs migrations, creates the
attachment directory, and seeds a default actor and inbox project.

This is an administrative command and should not be exposed to agents.`,
	RunE: runInitAdm,
}

var (
	initAdmHumanSlug string
	initAdmHumanName string
	initAdmAgentSlug string
	initAdmAgentName string
	initAdmAttachDir string
)

func init() {
	rootAdmCmd.AddCommand(initAdmCmd)

	initAdmCmd.Flags().StringVar(&initAdmHumanSlug, "human-slug", "local-human", "Slug for the default human actor")
	initAdmCmd.Flags().StringVar(&initAdmHumanName, "human-name", "Local Human", "Display name for the default human actor")
	initAdmCmd.Flags().StringVar(&initAdmAgentSlug, "agent-slug", "claude-code-agent", "Slug for the default agent actor")
	initAdmCmd.Flags().StringVar(&initAdmAgentName, "agent-name", "Claude Code Agent", "Display name for the default agent actor")
	initAdmCmd.Flags().StringVar(&initAdmAttachDir, "attach-dir", "", "Directory for attachments")
}

func runInitAdm(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return exitError(1, fmt.Errorf("failed to load config: %w", err))
	}

	// Use database path from flag or default to .wrkq/wrkq.db
	dbPathFlag := cmd.Flag("db").Value.String()
	if dbPathFlag != "" {
		cfg.DBPath = dbPathFlag
	} else {
		cfg.DBPath = ".wrkq/wrkq.db"
		// Use project-local attachments for local database
		if initAdmAttachDir == "" {
			cfg.AttachDir = ".wrkq/attachments"
		}
	}

	// Override attach dir from flag if provided
	if initAdmAttachDir != "" {
		cfg.AttachDir = initAdmAttachDir
	}

	// Check if database already exists
	dbExists := false
	if _, err := os.Stat(cfg.DBPath); err == nil {
		dbExists = true
	}

	// Create database directory if it doesn't exist
	dbDir := filepath.Dir(cfg.DBPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return exitError(1, fmt.Errorf("failed to create database directory: %w", err))
	}

	// Open database (creates file if it doesn't exist)
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return exitError(1, fmt.Errorf("failed to open database: %w", err))
	}
	defer database.Close()

	// Run migrations
	if err := database.Migrate(); err != nil {
		return exitError(1, fmt.Errorf("failed to run migrations: %w", err))
	}

	// Create attachments directory
	if err := os.MkdirAll(cfg.AttachDir, 0755); err != nil {
		return exitError(1, fmt.Errorf("failed to create attachments directory: %w", err))
	}

	// Seed data only if this is a new database
	if !dbExists {
		if err := seedDatabaseAdm(database, initAdmHumanSlug, initAdmHumanName, initAdmAgentSlug, initAdmAgentName); err != nil {
			return exitError(1, fmt.Errorf("failed to seed database: %w", err))
		}

		fmt.Printf("✓ Initialized new database at %s\n", cfg.DBPath)
		fmt.Printf("✓ Created attachments directory at %s\n", cfg.AttachDir)
		fmt.Printf("✓ Seeded human actor: %s (%s)\n", initAdmHumanSlug, initAdmHumanName)
		fmt.Printf("✓ Seeded agent actor: %s (%s)\n", initAdmAgentSlug, initAdmAgentName)
		fmt.Printf("✓ Seeded inbox project\n")
	} else {
		fmt.Printf("✓ Database already initialized at %s\n", cfg.DBPath)
		fmt.Printf("✓ Migrations applied\n")
	}

	// Update .gitignore to exclude wrkq database
	if err := updateGitignore(cfg.DBPath); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to update .gitignore: %v\n", err)
	}

	return nil
}

func seedDatabaseAdm(database *db.DB, humanSlug, humanName, agentSlug, agentName string) error {
	resolver := actors.NewResolver(database.DB)

	// Normalize and create human actor
	normalizedHumanSlug, err := paths.NormalizeSlug(humanSlug)
	if err != nil {
		return fmt.Errorf("invalid human actor slug: %w", err)
	}
	humanActor, err := resolver.Create(normalizedHumanSlug, humanName, "human")
	if err != nil {
		return fmt.Errorf("failed to create human actor: %w", err)
	}

	// Normalize and create agent actor
	normalizedAgentSlug, err := paths.NormalizeSlug(agentSlug)
	if err != nil {
		return fmt.Errorf("invalid agent actor slug: %w", err)
	}
	_, err = resolver.Create(normalizedAgentSlug, agentName, "agent")
	if err != nil {
		return fmt.Errorf("failed to create agent actor: %w", err)
	}

	// Normalize inbox slug
	inboxSlug, err := paths.NormalizeSlug("inbox")
	if err != nil {
		return fmt.Errorf("failed to normalize inbox slug: %w", err)
	}

	// Create inbox project (use human actor as creator)
	title := "Inbox"
	_, err = database.Exec(`
		INSERT INTO containers (id, slug, title, parent_uuid, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('', ?, ?, NULL, ?, ?)
	`, inboxSlug, title, humanActor.UUID, humanActor.UUID)
	if err != nil {
		return fmt.Errorf("failed to create inbox project: %w", err)
	}

	return nil
}

// updateGitignore adds the database path to .gitignore if not already present
func updateGitignore(dbPath string) error {
	gitignorePath := ".gitignore"

	// Read existing .gitignore content if it exists
	existingContent := ""
	if data, err := os.ReadFile(gitignorePath); err == nil {
		existingContent = string(data)
	}

	// Check if the database path is already in .gitignore
	lines := strings.Split(existingContent, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == dbPath {
			// Already present
			return nil
		}
	}

	// Open file for appending (create if doesn't exist)
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open .gitignore: %w", err)
	}
	defer f.Close()

	// If file existed and has content, ensure we start on a new line
	if existingContent != "" && !strings.HasSuffix(existingContent, "\n") {
		if _, err := f.WriteString("\n"); err != nil {
			return fmt.Errorf("failed to write newline: %w", err)
		}
	}

	// Add a comment and the database path
	toWrite := ""
	if existingContent == "" || !strings.Contains(existingContent, "# wrkq") {
		toWrite = "# wrkq database\n"
	}
	toWrite += dbPath + "\n"

	if _, err := f.WriteString(toWrite); err != nil {
		return fmt.Errorf("failed to write to .gitignore: %w", err)
	}

	fmt.Printf("✓ Added %s to .gitignore\n", dbPath)
	return nil
}
