//go:build wrkq_local

package rpccli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/projectroot"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
	workrpcclient "github.com/lherron/wrkq/pkg/client"
	"github.com/spf13/cobra"
)

// Local-locator seam (T-07090). Everything that opens durable local state lives
// behind the wrkq_local build tag so the portable client links no SQLite. The
// portable counterparts are in local_seam_portable.go and must refuse before any
// bootstrap/DB work rather than failing later with a driver error.

func NewInProcess(h *bootstrap.Handle) (Transport, error) {
	return workrpcclient.NewInProcess(h, workrpcclient.WrkqProfile)
}

// openLocalTransport opens the local SQLite database and serves the same workrpc
// method catalog in-process.
func openLocalTransport(locator string) (Transport, *config.Config, func(), error) {
	h, err := bootstrap.Open(locator)
	if err != nil {
		return nil, nil, nil, err
	}
	tr, err := NewInProcess(h)
	if err != nil {
		_ = h.Close()
		return nil, nil, nil, err
	}
	return tr, h.Config, func() { _ = tr.Close() }, nil
}

// newScoper builds the scoper from a bootstrap handle (which carries the loaded
// config) and the command's --project override (resolved against the handle DB).
func newScoper(cmd *cobra.Command, h *bootstrap.Handle) (*scoper, error) {
	cfg := h.Config
	if pf := cmd.Flag("project"); pf != nil {
		if sel := pf.Value.String(); sel != "" {
			projectPath, err := projectroot.ResolveProjectFlag(h.DB, sel)
			if err != nil {
				return nil, err
			}
			// Copy so we never mutate the shared handle config.
			scoped := *cfg
			scoped.ProjectRoot = projectPath
			cfg = &scoped
		}
	}
	return &scoper{cfg: cfg}, nil
}

// serveLocalStdio serves the JSON-RPC stdio protocol from a local database.
func serveLocalStdio(cmd *cobra.Command, cfg *config.Config) error {
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
		h.Opts.DefaultPrincipalRef = principal
	}
	return workrpc.ServeStdio(context.Background(), os.Stdin, os.Stdout, h.API, h.Opts)
}

// reportLocalWhoami renders whoami against a local database.
func reportLocalWhoami(cmd *cobra.Command, asJSON bool) error {
	h, err := bootstrap.Open(dbOverride(cmd))
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()

	// Match legacy appctx.Bootstrap: validate --project even though whoami's
	// output does not otherwise use ProjectRoot.
	if _, err := newScoper(cmd, h); err != nil {
		return err
	}

	var resolvedScope *scope.ResolvedScope
	if resolved, _, err := scope.Resolve(""); err == nil {
		resolvedScope = &resolved
	}
	attr, err := attribution.Resolve(attribution.ResolveOptions{
		DB:            h.DB.DB,
		Config:        h.Config,
		Command:       cmd,
		ResolvedScope: resolvedScope,
	})
	if err != nil {
		return err
	}

	if asJSON || !isStdoutTTY(cmd.OutOrStdout()) {
		output := map[string]interface{}{
			"principal_ref": attr.PrincipalRef,
			"scope_ref":     attr.ScopeRef,
			"db_path":       h.Config.DBPath,
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Principal: %s\n", attr.PrincipalRef)
	if attr.ScopeRef != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Scope:     %s\n", attr.ScopeRef)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "DB:      %s\n", h.Config.DBPath)
	return nil
}
