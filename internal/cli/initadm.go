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

var initAdmCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the wrkq database and configuration",
	Long: `Initialize creates the SQLite database, runs migrations, creates the
attachment directory, and seeds a default actor and inbox project.

This is an administrative command and should not be exposed to agents.`,
	RunE: runInitAdm,
}

var (
	initAdmHumanSlug  string
	initAdmHumanName  string
	initAdmAgentSlug  string
	initAdmAgentName  string
	initAdmAttachDir  string
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

	// Update project files (runs on both new and existing databases)
	// Update .env or .env.local if needed (use human actor as default for WRKQ_ACTOR)
	if err := updateEnvLocal(cfg.DBPath, initAdmHumanSlug); err != nil {
		// Don't fail the command, just warn
		fmt.Fprintf(os.Stderr, "Warning: failed to update env file: %v\n", err)
	}

	// Write WRKQ-USAGE.md to project directory (overwrites if exists)
	if err := writeWrkqUsage(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write WRKQ-USAGE.md: %v\n", err)
	}

	// Update CLAUDE.md with @WRKQ-USAGE.md reference
	if err := updateClaudeMd(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to update CLAUDE.md: %v\n", err)
	}

	// Update AGENTS.md with @WRKQ-USAGE.md reference if it exists
	if err := updateAgentsMd(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to update AGENTS.md: %v\n", err)
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

// updateEnvLocal checks .env or .env.local and adds WRKQ_DB_PATH and WRKQ_ACTOR if missing
// Prefers .env if it exists, otherwise uses .env.local
func updateEnvLocal(dbPath, actorSlug string) error {
	// Check if .env exists, prefer it over .env.local
	envPath := ".env.local"
	if _, err := os.Stat(".env"); err == nil {
		envPath = ".env"
	}

	// Read existing env file content if it exists
	existingContent := ""
	hasDBPath := false
	hasActor := false

	if data, err := os.ReadFile(envPath); err == nil {
		existingContent = string(data)
		lines := strings.Split(existingContent, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "WRKQ_DB_PATH=") {
				hasDBPath = true
			}
			if strings.HasPrefix(trimmed, "WRKQ_ACTOR=") {
				hasActor = true
			}
		}
	}

	// If both already exist, nothing to do
	if hasDBPath && hasActor {
		return nil
	}

	// Build content to append
	var toAppend []string
	if !hasDBPath {
		toAppend = append(toAppend, fmt.Sprintf("WRKQ_DB_PATH=%s", dbPath))
	}
	if !hasActor {
		toAppend = append(toAppend, fmt.Sprintf("WRKQ_ACTOR=%s", actorSlug))
	}

	// Open file for appending (create if doesn't exist)
	f, err := os.OpenFile(envPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open .env.local: %w", err)
	}
	defer f.Close()

	// If file existed and has content, ensure we start on a new line
	if existingContent != "" && !strings.HasSuffix(existingContent, "\n") {
		if _, err := f.WriteString("\n"); err != nil {
			return fmt.Errorf("failed to write newline: %w", err)
		}
	}

	// Write the new values
	for _, line := range toAppend {
		if _, err := f.WriteString(line + "\n"); err != nil {
			return fmt.Errorf("failed to write to .env.local: %w", err)
		}
	}

	fmt.Printf("✓ Updated %s with configuration\n", envPath)
	return nil
}

// writeWrkqUsage writes WRKQ-USAGE.md to the project directory
// If the file already exists, it is overwritten with the latest embedded content
func writeWrkqUsage() error {
	usagePath := "WRKQ-USAGE.md"

	// Check if file already exists for logging purposes
	exists := false
	if _, err := os.Stat(usagePath); err == nil {
		exists = true
	}

	// Write the embedded content (always overwrite to ensure latest version)
	if err := os.WriteFile(usagePath, []byte(wrkqUsageContent), 0644); err != nil {
		return fmt.Errorf("failed to write WRKQ-USAGE.md: %w", err)
	}

	if exists {
		fmt.Printf("✓ Updated WRKQ-USAGE.md\n")
	} else {
		fmt.Printf("✓ Created WRKQ-USAGE.md\n")
	}
	return nil
}

// updateClaudeMd adds @WRKQ-USAGE.md reference to CLAUDE.md if it doesn't exist
func updateClaudeMd() error {
	claudePath := "CLAUDE.md"

	// Check if CLAUDE.md exists
	if _, err := os.Stat(claudePath); os.IsNotExist(err) {
		// CLAUDE.md doesn't exist, create a minimal one
		content := `# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## WRKQ Usage Reference

** ALWAYS USE WRKQ TO TRACK YOUR TASK **

@WRKQ-USAGE.md
`
		if err := os.WriteFile(claudePath, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to create CLAUDE.md: %w", err)
		}
		fmt.Printf("✓ Created CLAUDE.md with @WRKQ-USAGE.md reference\n")
		return nil
	}

	// Read existing CLAUDE.md
	data, err := os.ReadFile(claudePath)
	if err != nil {
		return fmt.Errorf("failed to read CLAUDE.md: %w", err)
	}
	content := string(data)

	// Check if @WRKQ-USAGE.md already exists
	if strings.Contains(content, "@WRKQ-USAGE.md") {
		return nil
	}

	// Find where to insert the reference
	// Look for "# CLAUDE.md" or the first "## " heading
	lines := strings.Split(content, "\n")
	insertIndex := -1

	for i, line := range lines {
		if strings.HasPrefix(line, "# ") {
			// Found the main heading, insert after it and any following blank lines
			insertIndex = i + 1
			for insertIndex < len(lines) && strings.TrimSpace(lines[insertIndex]) == "" {
				insertIndex++
			}
			break
		}
	}

	if insertIndex == -1 {
		// No heading found, insert at the beginning
		insertIndex = 0
	}

	// Build the new content
	var newLines []string
	newLines = append(newLines, lines[:insertIndex]...)
	newLines = append(newLines, "")
	newLines = append(newLines, "## WRKQ Usage Reference")
	newLines = append(newLines, "")
	newLines = append(newLines, "** ALWAYS USE WRKQ TO TRACK YOUR TASK **")
	newLines = append(newLines, "")
	newLines = append(newLines, "@WRKQ-USAGE.md")
	newLines = append(newLines, "")
	newLines = append(newLines, lines[insertIndex:]...)

	// Write the updated content
	newContent := strings.Join(newLines, "\n")
	if err := os.WriteFile(claudePath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to update CLAUDE.md: %w", err)
	}

	fmt.Printf("✓ Updated CLAUDE.md with @WRKQ-USAGE.md reference\n")
	return nil
}

// updateAgentsMd adds @WRKQ-USAGE.md reference to AGENTS.md if it exists and doesn't already have it
func updateAgentsMd() error {
	agentsPath := "AGENTS.md"

	// Check if AGENTS.md exists - if not, do nothing (don't create it)
	if _, err := os.Stat(agentsPath); os.IsNotExist(err) {
		return nil
	}

	// Read existing AGENTS.md
	data, err := os.ReadFile(agentsPath)
	if err != nil {
		return fmt.Errorf("failed to read AGENTS.md: %w", err)
	}
	content := string(data)

	// Check if @WRKQ-USAGE.md already exists
	if strings.Contains(content, "@WRKQ-USAGE.md") {
		return nil
	}

	// Find where to insert the reference
	// Look for the first "# " heading
	lines := strings.Split(content, "\n")
	insertIndex := -1

	for i, line := range lines {
		if strings.HasPrefix(line, "# ") {
			// Found the main heading, insert after it and any following blank lines
			insertIndex = i + 1
			for insertIndex < len(lines) && strings.TrimSpace(lines[insertIndex]) == "" {
				insertIndex++
			}
			break
		}
	}

	if insertIndex == -1 {
		// No heading found, insert at the beginning
		insertIndex = 0
	}

	// Build the new content
	var newLines []string
	newLines = append(newLines, lines[:insertIndex]...)
	newLines = append(newLines, "")
	newLines = append(newLines, "## WRKQ Usage Reference")
	newLines = append(newLines, "")
	newLines = append(newLines, "** ALWAYS USE WRKQ TO TRACK YOUR TASK **")
	newLines = append(newLines, "")
	newLines = append(newLines, "@WRKQ-USAGE.md")
	newLines = append(newLines, "")
	newLines = append(newLines, lines[insertIndex:]...)

	// Write the updated content
	newContent := strings.Join(newLines, "\n")
	if err := os.WriteFile(agentsPath, []byte(newContent), 0644); err != nil {
		return fmt.Errorf("failed to update AGENTS.md: %w", err)
	}

	fmt.Printf("✓ Updated AGENTS.md with @WRKQ-USAGE.md reference\n")
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
