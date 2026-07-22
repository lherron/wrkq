// Package appctx provides a shared bootstrap helper for CLI commands.
// It centralizes config loading, database opening, and actor resolution
// to reduce boilerplate across commands.
package appctx

import (
	"fmt"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/projectroot"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/spf13/cobra"
)

// App holds the shared application context for commands.
type App struct {
	// Config is the loaded configuration
	Config *config.Config

	// DB is the opened database connection (nil if NeedsDB is false)
	DB *db.DB

	// PrincipalRef is the canonical externally issued principal reference used
	// for new write attribution. It is non-empty when NeedsActor/WithActor is used.
	PrincipalRef string

	// ScopeRef is the full praesidium scopeRef of the invoking agent, resolved
	// best-effort from the environment (ASP_SCOPE_REF / ASP_HANDLE / ...).
	// Empty when no scope is resolvable (e.g. a human at a plain shell).
	// Used to attribute task creation to the originating agent scope.
	ScopeRef string
}

// Attribution returns the canonical write attribution resolved during bootstrap.
func (a *App) Attribution() attribution.Attribution {
	return attribution.Attribution{
		PrincipalRef: a.PrincipalRef,
		ScopeRef:     a.ScopeRef,
	}
}

// Close releases resources held by the App.
// Safe to call multiple times.
func (a *App) Close() {
	if a.DB != nil {
		_ = a.DB.Close()
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
			if err := config.ApplyDBLocator(app.Config, dbPath, true); err != nil {
				return nil, err
			}
		}
	}

	// Open database if needed
	if opts.NeedsDB {
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
	}

	// Resolve attribution if needed.
	if opts.NeedsActor {
		if app.DB == nil {
			app.Close()
			return nil, fmt.Errorf("attribution resolution requires database (set NeedsDB: true)")
		}

		resolvedScope := resolveScopeForCommand(cmd)
		attr, err := attribution.Resolve(attribution.ResolveOptions{
			DB:            app.DB.DB,
			Config:        app.Config,
			Command:       cmd,
			ResolvedScope: resolvedScope,
		})
		if err != nil {
			app.Close()
			return nil, err
		}
		app.PrincipalRef = attr.PrincipalRef
		app.ScopeRef = attr.ScopeRef
	}

	return app, nil
}

func resolveScopeForCommand(cmd *cobra.Command) *scope.ResolvedScope {
	var scopeOverride string
	if scopeFlag := cmd.Flag("scope"); scopeFlag != nil {
		scopeOverride = scopeFlag.Value.String()
	}
	if resolved, _, err := scope.Resolve(scopeOverride); err == nil {
		return &resolved
	}
	return nil
}

// resolveProjectFlag resolves a project selector (path, slug, or ID) to a project
// path, used to override the WRKQ_PROJECT_ROOT config from the --project flag. It
// delegates to the neutral projectroot package so the legacy CLI and the mirror
// resolve --project identically (no drift).
func resolveProjectFlag(database *db.DB, projectSelector string) (string, error) {
	return projectroot.ResolveProjectFlag(database, projectSelector)
}
