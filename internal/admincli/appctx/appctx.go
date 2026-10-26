// Package appctx provides a shared bootstrap helper for CLI commands.
// It centralizes config loading and database opening
// to reduce boilerplate across commands.
package appctx

import (
	"fmt"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/projectroot"
	"github.com/spf13/cobra"
)

// App holds the shared application context for commands.
type App struct {
	// Config is the loaded configuration
	Config *config.Config

	// DB is the opened database connection
	DB *db.DB
}

// Close releases resources held by the App.
// Safe to call multiple times.
func (a *App) Close() {
	if a.DB != nil {
		_ = a.DB.Close()
		a.DB = nil
	}
}

// RunFunc is the signature for command run functions.
type RunFunc func(app *App, cmd *cobra.Command, args []string) error

// WithApp wraps a command's run function with shared bootstrap logic.
// It loads config and opens the database.
// The database is closed automatically when the wrapped function returns.
func WithApp(fn RunFunc) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		app, err := Bootstrap(cmd)
		if err != nil {
			return err
		}
		defer app.Close()

		return fn(app, cmd, args)
	}
}

// Bootstrap initializes the App.
// Callers are responsible for calling App.Close() when done.
func Bootstrap(cmd *cobra.Command) (*App, error) {
	app := &App{}

	// Load configuration; --db overrides the configured locator.
	var dbPath string
	if dbFlag := cmd.Flag("db"); dbFlag != nil {
		dbPath = dbFlag.Value.String()
	}
	cfg, err := config.LoadWithDBOverride(dbPath, true)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	app.Config = cfg

	if app.Config.RemoteEndpoint != "" {
		return nil, fmt.Errorf("remote database locator %q is not valid for this local command", app.Config.DBLocator)
	}
	if app.Config.DBPath == "" {
		return nil, config.MissingDatabasePathError()
	}
	database, err := db.Open(app.Config.DBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Check for pending migrations
	if err := database.RequiresMigrationError(); err != nil {
		_ = database.Close()
		return nil, err
	}

	app.DB = database

	// Override project root from --project flag if provided
	if projectFlag := cmd.Flag("project"); projectFlag != nil {
		if projectSelector := projectFlag.Value.String(); projectSelector != "" {
			projectPath, err := resolveProjectFlag(database, projectSelector)
			if err != nil {
				_ = database.Close()
				return nil, err
			}
			app.Config.ProjectRoot = projectPath
		}
	}

	return app, nil
}

// resolveProjectFlag resolves a project selector (path, slug, or ID) to a project
// path, used to override the WRKQ_PROJECT_ROOT config from the --project flag. It
// delegates to the neutral projectroot package so the legacy CLI and the mirror
// resolve --project identically (no drift).
func resolveProjectFlag(database *db.DB, projectSelector string) (string, error) {
	return projectroot.ResolveProjectFlag(database, projectSelector)
}
