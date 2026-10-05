package wrkfcli

import (
	"fmt"
	"os"

	workrpcclient "github.com/lherron/wrkq/pkg/client"
	"github.com/spf13/cobra"
)

func rpcCmd() *cobra.Command {
	var stdio bool
	cmd := &cobra.Command{
		Use:   "rpc --stdio",
		Short: "Serve wrkf JSON-RPC over stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !stdio {
				return fmt.Errorf("--stdio is required")
			}
			cfg, err := loadConfiguredConfig()
			if err != nil {
				return err
			}
			principalRef, err := wrkfPrincipalDefault(cmd, cfg)
			if err != nil {
				return err
			}
			if cfg.RemoteEndpoint != "" {
				if flagHookCatalog != "" {
					return fmt.Errorf("--hook-catalog is local-only; hook catalog is canonical-node configuration in remote mode")
				}
				return workrpcclient.ServeRemoteStdio(cmd.Context(), os.Stdin, os.Stdout, cfg.RemoteEndpoint, workrpcclient.TokenFromEnv(), principalRef)
			}
			return serveLocalStdio(cmd.Context(), cfg, principalRef)
		},
	}
	cmd.Flags().BoolVar(&stdio, "stdio", false, "Use stdin/stdout JSON-RPC transport")
	return cmd
}
