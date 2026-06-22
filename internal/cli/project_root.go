package cli

import (
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/projectroot"
)

// The project-root transform lives in the neutral internal/projectroot package so
// the legacy CLI and the RPC-backed mirror share ONE implementation (no drift).
// These thin wrappers keep the existing call sites unchanged.

func normalizeProjectRoot(cfg *config.Config) string { return projectroot.Normalize(cfg) }

func applyProjectRootToPath(cfg *config.Config, path string, defaultToRoot bool) string {
	return projectroot.ApplyToPath(cfg, path, defaultToRoot)
}

func applyProjectRootToSelector(cfg *config.Config, selector string, defaultToRoot bool) string {
	return projectroot.ApplyToSelector(cfg, selector, defaultToRoot)
}

func applyProjectRootToPaths(cfg *config.Config, pathsIn []string, defaultToRoot bool) []string {
	return projectroot.ApplyToPaths(cfg, pathsIn, defaultToRoot)
}
