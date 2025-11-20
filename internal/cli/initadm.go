package cli

import (
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/spf13/cobra"
)

var initAdmCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the wrkq database and configuration",
	Long: `Initialize creates the SQLite database, runs migrations, creates the
attachment directory, and seeds a default actor and inbox project.

This is an administrative command and should not be exposed to agents.`,
	RunE: runInitAdm,
}

var (
	initAdmActorSlug string
	initAdmActorName string
	initAdmAttachDir string
)

func init() {
	rootAdmCmd.AddCommand(initAdmCmd)

	initAdmCmd.Flags().StringVar(&initAdmActorSlug, "actor-slug", "local-human", "Slug for the default human actor")
	initAdmCmd.Flags().StringVar(&initAdmActorName, "actor-name", "Local Human", "Display name for the default human actor")
	initAdmCmd.Flags().StringVar(&initAdmAttachDir, "attach-dir", "", "Directory for attachments")
}

func runInitAdm(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return exitError(1, fmt.Errorf("failed to load config: %w", err))
	}

	// Override DB path from flag if provided
	if dbPath := cmd.Flag("db").Value.String(); dbPath != "" {
		cfg.DBPath = dbPath
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
		if err := seedDatabaseAdm(database, initAdmActorSlug, initAdmActorName); err != nil {
			return exitError(1, fmt.Errorf("failed to seed database: %w", err))
		}

		fmt.Printf("✓ Initialized new database at %s\n", cfg.DBPath)
		fmt.Printf("✓ Created attachments directory at %s\n", cfg.AttachDir)
		fmt.Printf("✓ Seeded default actor: %s\n", initAdmActorSlug)
		fmt.Printf("✓ Seeded inbox project\n")
	} else {
		fmt.Printf("✓ Database already initialized at %s\n", cfg.DBPath)
		fmt.Printf("✓ Migrations applied\n")
	}

	return nil
}

func seedDatabaseAdm(database *db.DB, actorSlug, actorName string) error {
	// Normalize actor slug
	normalizedSlug, err := paths.NormalizeSlug(actorSlug)
	if err != nil {
		return fmt.Errorf("invalid actor slug: %w", err)
	}

	// Create default human actor
	resolver := actors.NewResolver(database.DB)
	actor, err := resolver.Create(normalizedSlug, actorName, "human")
	if err != nil {
		return fmt.Errorf("failed to create default actor: %w", err)
	}

	// Normalize inbox slug
	inboxSlug, err := paths.NormalizeSlug("inbox")
	if err != nil {
		return fmt.Errorf("failed to normalize inbox slug: %w", err)
	}

	// Create inbox project
	title := "Inbox"
	_, err = database.Exec(`
		INSERT INTO containers (id, slug, title, parent_uuid, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('', ?, ?, NULL, ?, ?)
	`, inboxSlug, title, actor.UUID, actor.UUID)
	if err != nil {
		return fmt.Errorf("failed to create inbox project: %w", err)
	}

	return nil
}
