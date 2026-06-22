package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
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
		// Construct the server through the neutral bootstrap helper so the
		// stdio entrypoint and the RPC-backed mirror CLI build identical
		// API/options and cannot drift.
		api, opts, err := bootstrap.Server(app.DB, app.Config)
		if err != nil {
			return err
		}
		return workrpc.ServeStdio(context.Background(), os.Stdin, os.Stdout, api, opts)
	}),
}

func init() {
	rpcCmd.Flags().BoolVar(&rpcStdio, "stdio", false, "Use stdin/stdout JSON-RPC transport")
	rootCmd.AddCommand(rpcCmd)
}
