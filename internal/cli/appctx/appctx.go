// Package appctx provides a shared bootstrap helper for CLI commands.
// It centralizes config loading, database opening, and actor resolution
// to reduce boilerplate across commands.
package appctx

import (
	"fmt"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/spf13/cobra"
)

// App holds the shared application context for commands.
type App struct {
	// Config is the loaded configuration
	Config *config.Config

	// DB is the opened database connection (nil if NeedsDB is false)
	DB *db.DB

	// ActorUUID is the resolved actor UUID (empty if NeedsActor is false)
	ActorUUID string

	// ActorID is the resolved actor friendly ID (e.g., "A-00001")
	ActorID string
}

// Close releases resources held by the App.
// Safe to call multiple times.
func (a *App) Close() {
	if a.DB != nil {
		a.DB.Close()
		a.DB = nil
	}
}

// Options configures the bootstrap behavior.
type Options struct {
	// NeedsDB indicates whether to open the database.
	// Defaults to true.
	NeedsDB bool

	// NeedsActor indicates whether to resolve the current actor.
	// Requires NeedsDB to also be true.
	NeedsActor bool
}

// DefaultOptions returns default options (DB required, no actor).
func DefaultOptions() Options {
	return Options{
		NeedsDB:    true,
		NeedsActor: false,
	}
}

// WithActor returns options that require both DB and actor.
func WithActor() Options {
	return Options{
		NeedsDB:    true,
		NeedsActor: true,
	}
}

// RunFunc is the signature for command run functions.
type RunFunc func(app *App, cmd *cobra.Command, args []string) error

// WithApp wraps a command's run function with shared bootstrap logic.
// It loads config, opens the database, and optionally resolves the actor.
// The database is closed automatically when the wrapped function returns.
func WithApp(opts Options, fn RunFunc) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		app, err := Bootstrap(cmd, opts)
		if err != nil {
			return err
		}
		defer app.Close()

		return fn(app, cmd, args)
	}
}

// Bootstrap initializes the App according to the given options.
// Callers are responsible for calling App.Close() when done.
func Bootstrap(cmd *cobra.Command, opts Options) (*App, error) {
	app := &App{}

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	app.Config = cfg

	// Override DB path from --db flag if provided
	if dbFlag := cmd.Flag("db"); dbFlag != nil {
		if dbPath := dbFlag.Value.String(); dbPath != "" {
			app.Config.DBPath = dbPath
		}
	}

	// Open database if needed
	if opts.NeedsDB {
		database, err := db.Open(app.Config.DBPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open database: %w", err)
		}

		// Check for pending migrations
		_, pending, err := database.MigrationStatus()
		if err != nil {
			database.Close()
			return nil, fmt.Errorf("failed to check migration status: %w", err)
		}
		if len(pending) > 0 {
			database.Close()
			return nil, fmt.Errorf("database requires migration: %d pending migration(s). Run 'wrkqadm migrate' to update", len(pending))
		}

		app.DB = database
	}

	// Resolve actor if needed
	if opts.NeedsActor {
		if app.DB == nil {
			app.Close()
			return nil, fmt.Errorf("actor resolution requires database (set NeedsDB: true)")
		}

		actorUUID, actorID, err := resolveActor(app.DB, app.Config, cmd)
		if err != nil {
			app.Close()
			return nil, err
		}
		app.ActorUUID = actorUUID
		app.ActorID = actorID
	}

	return app, nil
}

// resolveActor resolves the current actor from flags, env, or config.
func resolveActor(database *db.DB, cfg *config.Config, cmd *cobra.Command) (uuid, friendlyID string, err error) {
	// Get actor identifier from --as flag or config
	var actorIdentifier string
	if asFlag := cmd.Flag("as"); asFlag != nil {
		actorIdentifier = asFlag.Value.String()
	}
	if actorIdentifier == "" {
		actorIdentifier = cfg.GetActorID()
	}
	if actorIdentifier == "" {
		return "", "", fmt.Errorf("no actor configured (set WRKQ_ACTOR, WRKQ_ACTOR_ID, or use --as flag)")
	}

	// Resolve actor UUID
	resolver := actors.NewResolver(database.DB)
	actorUUID, err := resolver.Resolve(actorIdentifier)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve actor: %w", err)
	}

	// Get actor friendly ID
	var actorID string
	err = database.QueryRow("SELECT id FROM actors WHERE uuid = ?", actorUUID).Scan(&actorID)
	if err != nil {
		return "", "", fmt.Errorf("failed to get actor ID: %w", err)
	}

	return actorUUID, actorID, nil
}
