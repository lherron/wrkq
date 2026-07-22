// Package bootstrap is the single, neutral source of truth for constructing the
// wrkq/wrkf JSON-RPC server's *wrkfapi.API and workrpc.RegistryOptions from
// already-open config/database inputs.
//
// It exists so the public stdio entrypoint (`wrkq rpc --stdio`) and the
// production RPC-backed CLI build the server identically and cannot drift. It
// lives under internal/workrpc (the protocol boundary), so rpccli can depend on
// it without importing the daemon adapter.
package bootstrap

import (
	"fmt"
	"os"
	"time"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workflow"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/lherron/wrkq/internal/wrkqapi"
)

// Server builds the wrkf API and registry options for an already-open database
// and loaded config. This is the shared core used by both the stdio server and
// the mirror CLI; keep all server-construction policy (hook catalog, default
// actor, attach-dir policy, attachment limits, entrypoint identity) here.
func Server(database *db.DB, cfg *config.Config) (*wrkfapi.API, workrpc.RegistryOptions, error) {
	hookPath, err := workflow.ResolveHookCatalogPath("")
	if err != nil {
		return nil, workrpc.RegistryOptions{}, fmt.Errorf("failed to resolve hook catalog: %w", err)
	}
	return serverWithHookPath(database, cfg, hookPath, 0)
}

// DaemonServer constructs the canonical HTTP daemon registry. Unlike local
// InProcess/stdio construction, it never performs workspace/home catalog
// autodiscovery: WRKF_HOOK_CATALOG must name the deployed bundle explicitly.
func DaemonServer(database *db.DB, cfg *config.Config) (*wrkfapi.API, workrpc.RegistryOptions, error) {
	return serverWithHookPath(database, cfg, os.Getenv("WRKF_HOOK_CATALOG"), workrpc.RemoteHookTimeoutCeiling)
}

func serverWithHookPath(database *db.DB, cfg *config.Config, hookPath string, hookTimeoutCeiling time.Duration) (*wrkfapi.API, workrpc.RegistryOptions, error) {
	var cat *workflow.HookCatalog
	var err error
	if hookPath != "" {
		cat, err = workflow.LoadHookCatalog(hookPath)
	}
	if err != nil {
		return nil, workrpc.RegistryOptions{}, fmt.Errorf("failed to load hook catalog: %w", err)
	}
	api := wrkfapi.New(
		workflow.NewService(database),
		wrkfapi.WithHookCatalog(cat),
		wrkfapi.WithTemplateDir(workflow.HookCatalogDir(hookPath)),
		wrkfapi.WithHookTimeoutCeiling(hookTimeoutCeiling),
	)
	opts := workrpc.RegistryOptions{
		Database:         database,
		DatabasePath:     database.Path(),
		ServerVersion:    "dev",
		Entrypoint:       "wrkq",
		DefaultActor:     DefaultPrincipalRef(cfg.DefaultPrincipalRef),
		DefaultRole:      os.Getenv("WRKF_ROLE"),
		AttachDir:        AttachDir(cfg.AttachDir),
		AttachmentsMaxMB: cfg.AttachmentsMaxMB,
		Search:           SearchConfig(cfg, database.Path()),
	}
	return api, opts, nil
}

// Handle bundles an opened database with the constructed API and registry
// options. Callers that opened the database via Open own its lifecycle and must
// call Close. Callers passing a pre-opened database to Server retain ownership.
type Handle struct {
	DB   *db.DB
	API  *wrkfapi.API
	Opts workrpc.RegistryOptions
	// Config is the loaded configuration (db-path override applied). The mirror
	// CLI reads Config.ProjectRoot to scope path/selector args before sending RPC
	// params — project-root scoping is caller semantics, not a server concern.
	Config *config.Config
}

// Close releases the database opened by Open. Safe to call once.
func (h *Handle) Close() error {
	if h == nil || h.DB == nil {
		return nil
	}
	err := h.DB.Close()
	h.DB = nil
	return err
}

// Open loads config, opens the database (honoring an optional db locator override),
// verifies migrations, and builds the server API + options. The mirror CLI uses
// this because it has no pre-opened database; the stdio entrypoint uses Server
// directly with the database it already opened through appctx.
func Open(dbLocatorOverride string) (*Handle, error) {
	return open(dbLocatorOverride, "", false)
}

// OpenWithHookCatalog builds a local InProcess server using the caller-selected
// local catalog path. Empty retains local autodiscovery semantics.
func OpenWithHookCatalog(dbLocatorOverride, hookCatalogOverride string) (*Handle, error) {
	hookPath, err := workflow.ResolveHookCatalogPath(hookCatalogOverride)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve hook catalog: %w", err)
	}
	return open(dbLocatorOverride, hookPath, true)
}

func open(dbLocatorOverride, hookPath string, explicitHookPath bool) (*Handle, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	if dbLocatorOverride != "" {
		if err := config.ApplyDBLocator(cfg, dbLocatorOverride, false); err != nil {
			return nil, err
		}
	}
	if cfg.RemoteEndpoint != "" {
		return nil, fmt.Errorf("remote database locator %q cannot be opened as a local SQLite database", cfg.DBLocator)
	}
	if cfg.DBPath == "" {
		return nil, config.MissingDatabasePathError()
	}
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	if err := database.RequiresMigrationError(); err != nil {
		_ = database.Close()
		return nil, err
	}
	var api *wrkfapi.API
	var opts workrpc.RegistryOptions
	if explicitHookPath {
		api, opts, err = serverWithHookPath(database, cfg, hookPath, 0)
	} else {
		api, opts, err = Server(database, cfg)
	}
	if err != nil {
		_ = database.Close()
		return nil, err
	}
	return &Handle{DB: database, API: api, Opts: opts, Config: cfg}, nil
}

// SearchConfig maps the loaded config's search settings into the server-owned
// search host configuration. canonicalDBPath is the canonical database path (the
// sidecar defaults to <path>.search.sqlite). The SERVER owns the derived sidecar
// + dense embedder; the mirror never opens the sidecar or calls EnsureLlamaReady
// (T-05114).
func SearchConfig(cfg *config.Config, canonicalDBPath string) wrkqapi.SearchConfig {
	return wrkqapi.SearchConfig{
		Enabled:          cfg.Search.Enabled,
		CanonicalDBPath:  canonicalDBPath,
		DBPath:           cfg.Search.DBPath,
		DenseProvider:    cfg.Search.DenseProvider,
		DenseBaseURL:     cfg.Search.DenseBaseURL,
		DenseModel:       cfg.Search.DenseModel,
		DenseDimension:   cfg.Search.DenseDimension,
		QueryInstruction: cfg.Search.QueryInstruction,
		IndexBatchSize:   cfg.Search.IndexBatchSize,
		CandidateLimit:   cfg.Search.CandidateLimit,
	}
}

// DefaultPrincipalRef returns the configured principal-only default. Empty means
// wrkq mutations must receive an explicit principal or fail.
func DefaultPrincipalRef(principalRef string) string {
	return principalRef
}

// AttachDir resolves the attachment storage directory for the RPC server. It
// returns only an explicitly configured directory. When nothing is configured it
// returns "" so attachment.add reports WRKQ_VALIDATION rather than silently
// writing to an implicit host path.
func AttachDir(configured string) string {
	if env := os.Getenv("WRKQ_ATTACH_DIR"); env != "" {
		return env
	}
	return configured
}
