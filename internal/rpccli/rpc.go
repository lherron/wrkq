package rpccli

import (
	"context"
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
	"github.com/spf13/cobra"
)

func newRPCCmd() *cobra.Command {
	var stdio bool
	cmd := &cobra.Command{
		Use:   "rpc --stdio",
		Short: "Serve wrkq/wrkf JSON-RPC over stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !stdio {
				return fmt.Errorf("--stdio is required")
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if override := dbOverride(cmd); override != "" {
				if err := config.ApplyDBLocator(cfg, override, false); err != nil {
					return err
				}
			}
			if cfg.RemoteEndpoint != "" {
				return workrpc.ServeRemoteStdio(cmd.Context(), os.Stdin, os.Stdout, cfg.RemoteEndpoint, remoteTokenFromEnv())
			}
			h, err := bootstrap.Open(cfg.DBLocator)
			if err != nil {
				return err
			}
			defer func() { _ = h.Close() }()
			return workrpc.ServeStdio(context.Background(), os.Stdin, os.Stdout, h.API, h.Opts)
		},
	}
	cmd.Flags().BoolVar(&stdio, "stdio", false, "Use stdin/stdout JSON-RPC transport")
	return cmd
}
