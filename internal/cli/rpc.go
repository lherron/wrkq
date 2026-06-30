package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
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
		// Apply the launch-time caller principal (--principal-ref / --as) as the
		// rpc server's default principal so a session launched with an explicit
		// agent identity attributes writes without every mutation re-passing
		// principalRef in its params. Exact agent:<id> only; the two flags must
		// agree when both are set.
		if principal, err := launchPrincipalRef(cmd); err != nil {
			return err
		} else if principal != "" {
			app.Config.DefaultPrincipalRef = principal
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

// launchPrincipalRef resolves the rpc server's default caller principal from the
// global --principal-ref / --as flags. Both must be exact agent:<id>; if both are
// supplied they must match. Empty when neither flag is set (the server then falls
// back to WRKQ_PRINCIPAL_REF / config default_principal_ref).
func launchPrincipalRef(cmd *cobra.Command) (string, error) {
	read := func(name string) string {
		if f := cmd.Flags().Lookup(name); f != nil {
			return strings.TrimSpace(f.Value.String())
		}
		return ""
	}
	principalFlag := read("principal-ref")
	asFlag := read("as")
	var principal string
	if principalFlag != "" {
		p, err := attribution.NormalizeCanonical(principalFlag)
		if err != nil {
			return "", err
		}
		principal = p
	}
	if asFlag != "" {
		p, err := attribution.NormalizeCanonical(asFlag)
		if err != nil {
			return "", err
		}
		if principal != "" && principal != p {
			return "", fmt.Errorf("--principal-ref and --as must match")
		}
		principal = p
	}
	return principal, nil
}

func init() {
	rpcCmd.Flags().BoolVar(&rpcStdio, "stdio", false, "Use stdin/stdout JSON-RPC transport")
	rootCmd.AddCommand(rpcCmd)
}
