// Package bootstrap is the single, neutral source of truth for constructing the
// wrkq/wrkf JSON-RPC server's *wrkfapi.API and workrpc.RegistryOptions from
// already-open config/database inputs.
//
// It exists so the public stdio entrypoint (`wrkq rpc --stdio`) and the
// RPC-backed mirror CLI build the server identically and cannot drift. It lives
// under internal/workrpc (the protocol boundary), NOT under internal/cli, so the
// mirror CLI can depend on it without importing command handlers.
package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"

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
	cat, err := workflow.LoadHookCatalog(hookPath)
	if err != nil {
		return nil, workrpc.RegistryOptions{}, fmt.Errorf("failed to load hook catalog: %w", err)
	}
	api := wrkfapi.New(
		workflow.NewService(database),
		wrkfapi.WithHookCatalog(cat),
		wrkfapi.WithTemplateDir(workflow.HookCatalogDir(hookPath)),
	)
	opts := workrpc.RegistryOptions{
		Database:         database,
		DatabasePath:     database.Path(),
		ServerVersion:    "dev",
		Entrypoint:       "wrkq",
		DefaultActor:     DefaultActor(cfg.DefaultActor),
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

// Open loads config, opens the database (honoring an optional db-path override),
// verifies migrations, and builds the server API + options. The mirror CLI uses
// this because it has no pre-opened database; the stdio entrypoint uses Server
// directly with the database it already opened through appctx.
func Open(dbPathOverride string) (*Handle, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	if dbPathOverride != "" {
		cfg.DBPath = dbPathOverride
	}
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	if err := database.RequiresMigrationError(); err != nil {
		_ = database.Close()
		return nil, err
	}
	api, opts, err := Server(database, cfg)
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

// DefaultActor mirrors the stdio server's default-actor policy: an explicit
// configured actor wins, otherwise the system actor is used.
func DefaultActor(actor string) string {
	if actor != "" {
		return actor
	}
	return "system:wrkq"
}

// AttachDir resolves the attachment storage directory for the RPC server. It
// returns only an *explicitly configured* directory: WRKQ_ATTACH_DIR takes
// precedence, then a config.yaml attach_dir that differs from the cwd/home
// auto-default. When nothing is explicitly configured it returns "" so
// attachment.add reports WRKQ_VALIDATION rather than silently writing to the
// per-user default location.
func AttachDir(configured string) string {
	if env := os.Getenv("WRKQ_ATTACH_DIR"); env != "" {
		return env
	}
	if configured != "" && configured != defaultAttachDir() {
		return configured
	}
	return ""
}

// defaultAttachDir mirrors config.Load's auto-default so AttachDir can ignore it.
func defaultAttachDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "wrkq", "attachments")
}
