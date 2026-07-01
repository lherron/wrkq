package rpccli

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
	"github.com/spf13/cobra"
)

// launchPrincipalRef resolves the rpc server's default caller principal from the
// global --principal-ref / --as flags. Both accept agent:<id> or full agent
// ScopeRefs and resolve to agent:<id>; if both are supplied they must resolve to
// the same agent. Empty when neither is set (the server then falls back to
// WRKQ_PRINCIPAL_REF / config default_principal_ref).
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
		p, err := normalizePrincipalFlag("--principal-ref", principalFlag)
		if err != nil {
			return "", err
		}
		principal = p
	}
	if asFlag != "" {
		p, err := normalizePrincipalFlag("--as", asFlag)
		if err != nil {
			return "", err
		}
		if principal != "" && principal != p {
			return "", fmt.Errorf("--principal-ref resolves to %s but --as resolves to %s; use one flag or make both point to the same agent", principal, p)
		}
		principal = p
	}
	return principal, nil
}

func normalizePrincipalFlag(name, value string) (string, error) {
	principal, err := attribution.NormalizeCanonical(value)
	if err != nil {
		return "", fmt.Errorf("invalid %s: %w", name, err)
	}
	return principal, nil
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
			h, err := bootstrap.Open(cfg.DBLocator)
			if err != nil {
				return err
			}
			defer func() { _ = h.Close() }()
			// Apply the launch-time caller principal (--principal-ref / --as) as
			// the rpc server's default principal so a session launched with an
			// explicit agent identity attributes writes without every mutation
			// re-passing principalRef. Full ScopeRefs are reduced to agent:<id>;
			// the flags must resolve to the same agent when both are set.
			if principal, perr := launchPrincipalRef(cmd); perr != nil {
				return perr
			} else if principal != "" {
				h.Opts.DefaultActor = principal
			}
			return workrpc.ServeStdio(context.Background(), os.Stdin, os.Stdout, h.API, h.Opts)
		},
	}
	cmd.Flags().BoolVar(&stdio, "stdio", false, "Use stdin/stdout JSON-RPC transport")
	return cmd
}
