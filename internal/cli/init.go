package cli

import (
	"fmt"
	"os"

	"github.com/lherron/todo/internal/actors"
	"github.com/lherron/todo/internal/config"
	"github.com/lherron/todo/internal/db"
	"github.com/lherron/todo/internal/paths"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize the todo database and configuration",
	Long: `Initialize creates the SQLite database, runs migrations, creates the
attachment directory, and seeds a default actor and inbox project.`,
	RunE: runInit,
}

var (
	initActorSlug string
	initActorName string
	initAttachDir string
)

func init() {
	rootCmd.AddCommand(initCmd)

	initCmd.Flags().StringVar(&initActorSlug, "actor-slug", "local-human", "Slug for the default human actor")
	initCmd.Flags().StringVar(&initActorName, "actor-name", "Local Human", "Display name for the default human actor")
	initCmd.Flags().StringVar(&initAttachDir, "attach-dir", "", "Directory for attachments")
}

func runInit(cmd *cobra.Command, args []string) error {
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
	if initAttachDir != "" {
		cfg.AttachDir = initAttachDir
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
		if err := seedDatabase(database, initActorSlug, initActorName); err != nil {
			return exitError(1, fmt.Errorf("failed to seed database: %w", err))
		}

		fmt.Printf("✓ Initialized new database at %s\n", cfg.DBPath)
		fmt.Printf("✓ Created attachments directory at %s\n", cfg.AttachDir)
		fmt.Printf("✓ Seeded default actor: %s\n", initActorSlug)
		fmt.Printf("✓ Seeded inbox project\n")
	} else {
		fmt.Printf("✓ Database already initialized at %s\n", cfg.DBPath)
		fmt.Printf("✓ Migrations applied\n")
	}

	return nil
}

func seedDatabase(database *db.DB, actorSlug, actorName string) error {
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

// exitError returns an error that will cause the CLI to exit with the given code
func exitError(code int, err error) error {
	// For now, just return the error. We'll enhance this with proper exit codes later
	return err
}
