package rpccli

import (
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/spf13/cobra"
)

// launchPrincipalRef resolves the rpc server's default caller principal from the
// shared wrkq caller attribution sources. Empty means no launch-time default was
// configured and the server keeps its configured default_principal_ref.
func launchPrincipalRef(cmd *cobra.Command) (string, error) {
	attr, err := optionalCommandAttribution(cmd, attribution.PrincipalEnv)
	if err != nil {
		return "", err
	}
	return attr.PrincipalRef, nil
}

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
			return serveLocalStdio(cmd, cfg)
		},
	}
	cmd.Flags().BoolVar(&stdio, "stdio", false, "Use stdin/stdout JSON-RPC transport")
	return cmd
}
