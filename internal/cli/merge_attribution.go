package cli

import (
	"fmt"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/spf13/cobra"
)

func resolveMergeAttribution(database *db.DB, cmd *cobra.Command, cfg *config.Config) (attribution.Attribution, error) {
	var resolvedScope *scope.ResolvedScope
	if resolved, _, err := scope.Resolve(""); err == nil {
		resolvedScope = &resolved
	}
	return attribution.Resolve(attribution.ResolveOptions{
		DB:            database.DB,
		Config:        cfg,
		Command:       cmd,
		ResolvedScope: resolvedScope,
	})
}

func resolveMergeActor(database *db.DB, cmd *cobra.Command, cfg *config.Config) (string, error) {
	attr, err := resolveMergeAttribution(database, cmd, cfg)
	if err != nil {
		return "", err
	}
	if attr.LegacyActorUUID == nil || *attr.LegacyActorUUID == "" {
		return "", fmt.Errorf("legacy actor cache not found for %s", attr.PrincipalRef)
	}
	return *attr.LegacyActorUUID, nil
}
