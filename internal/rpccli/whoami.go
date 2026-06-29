package rpccli

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
	"github.com/spf13/cobra"
)

func newWhoamiCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "whoami",
		Short: "Print the current principal",
		Long:  `Displays the resolved external principal and runtime scope based on configuration and environment.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, cfgErr := config.Load()
			if cfgErr != nil {
				return cfgErr
			}
			if override := dbOverride(cmd); override != "" {
				if err := config.ApplyDBLocator(cfg, override, false); err != nil {
					return err
				}
			}
			if cfg.RemoteEndpoint != "" {
				tr, err := NewRemote(cfg.RemoteEndpoint, remoteTokenFromEnv())
				if err != nil {
					return err
				}
				defer func() { _ = tr.Close() }()
				if _, err := newScoperFromConfig(cmd, cfg, tr); err != nil {
					return err
				}
				var resolvedScope *scope.ResolvedScope
				if resolved, _, err := scope.Resolve(""); err == nil {
					resolvedScope = &resolved
				}
				attr, err := attribution.Resolve(attribution.ResolveOptions{
					Config:        cfg,
					Command:       cmd,
					ResolvedScope: resolvedScope,
				})
				if err != nil {
					return err
				}
				if asJSON || !isStdoutTTY(cmd.OutOrStdout()) {
					output := map[string]interface{}{
						"principal_ref":   attr.PrincipalRef,
						"scope_ref":       attr.ScopeRef,
						"db_mode":         "remote",
						"db_locator":      cfg.DBLocator,
						"remote_endpoint": cfg.RemoteEndpoint,
					}
					encoder := json.NewEncoder(cmd.OutOrStdout())
					encoder.SetIndent("", "  ")
					return encoder.Encode(output)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Principal: %s\n", attr.PrincipalRef)
				if attr.ScopeRef != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Scope:     %s\n", attr.ScopeRef)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "DB:      %s\n", cfg.DBLocator)
				return nil
			}

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
				if attr.LegacyActorUUID != nil {
					output["legacy_actor_uuid"] = *attr.LegacyActorUUID
				}
				if attr.LegacyActorID != "" {
					output["legacy_actor_id"] = attr.LegacyActorID
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
			if attr.LegacyActorUUID != nil || attr.LegacyActorID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "Legacy actor cache:")
				if attr.LegacyActorID != "" {
					fmt.Fprintf(cmd.OutOrStdout(), " %s", attr.LegacyActorID)
				}
				if attr.LegacyActorUUID != nil {
					fmt.Fprintf(cmd.OutOrStdout(), " %s", *attr.LegacyActorUUID)
				}
				fmt.Fprintln(cmd.OutOrStdout())
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}
