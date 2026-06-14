package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/workflow"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/spf13/cobra"
)

var rpcStdio bool

var rpcCmd = &cobra.Command{
	Use:   "rpc --stdio",
	Short: "Serve wrkq/wrkf JSON-RPC over stdio",
	Args:  cobra.NoArgs,
	RunE: appctx.WithApp(appctx.DefaultOptions(), func(app *appctx.App, cmd *cobra.Command, args []string) error {
		if !rpcStdio {
			return fmt.Errorf("--stdio is required")
		}
		hookPath, err := workflow.ResolveHookCatalogPath("")
		if err != nil {
			return fmt.Errorf("failed to resolve hook catalog: %w", err)
		}
		cat, err := workflow.LoadHookCatalog(hookPath)
		if err != nil {
			return fmt.Errorf("failed to load hook catalog: %w", err)
		}
		api := wrkfapi.New(
			workflow.NewService(app.DB),
			wrkfapi.WithHookCatalog(cat),
			wrkfapi.WithTemplateDir(workflow.HookCatalogDir(hookPath)),
		)
		return workrpc.ServeStdio(context.Background(), os.Stdin, os.Stdout, api, workrpc.RegistryOptions{
			Database:         app.DB,
			DatabasePath:     app.DB.Path(),
			ServerVersion:    "dev",
			Entrypoint:       "wrkq",
			DefaultActor:     defaultRPCActor(app.Config.DefaultActor),
			DefaultRole:      os.Getenv("WRKF_ROLE"),
			AttachDir:        rpcAttachDir(app.Config.AttachDir),
			AttachmentsMaxMB: app.Config.AttachmentsMaxMB,
		})
	}),
}

func init() {
	rpcCmd.Flags().BoolVar(&rpcStdio, "stdio", false, "Use stdin/stdout JSON-RPC transport")
	rootCmd.AddCommand(rpcCmd)
}

func defaultRPCActor(actor string) string {
	if actor != "" {
		return actor
	}
	return "system:wrkq"
}

// rpcAttachDir resolves the attachment storage directory for the RPC server. It
// returns only an *explicitly configured* directory: WRKQ_ATTACH_DIR takes
// precedence, then a config.yaml attach_dir that differs from the cwd/home
// auto-default. When nothing is explicitly configured it returns "" so
// attachment.add reports WRKQ_VALIDATION rather than silently writing to the
// per-user default location.
func rpcAttachDir(configured string) string {
	if env := os.Getenv("WRKQ_ATTACH_DIR"); env != "" {
		return env
	}
	if configured != "" && configured != defaultAttachDir() {
		return configured
	}
	return ""
}

// defaultAttachDir mirrors config.Load's auto-default so rpcAttachDir can ignore
// it. An empty result (no home dir) means "treat any configured value as
// explicit".
func defaultAttachDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "wrkq", "attachments")
}
