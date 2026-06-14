package cli

import (
	"context"
	"fmt"
	"os"

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
			Database:      app.DB,
			DatabasePath:  app.DB.Path(),
			ServerVersion: "dev",
			Entrypoint:    "wrkq",
			DefaultActor:  defaultRPCActor(app.Config.DefaultActor),
			DefaultRole:   os.Getenv("WRKF_ROLE"),
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
