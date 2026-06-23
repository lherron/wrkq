package rpccli

import (
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/projectroot"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
	"github.com/spf13/cobra"
)

// scoper applies the project-root transform to raw user path/selector arguments
// BEFORE they become RPC params. It reproduces legacy appctx caller semantics:
// the configured project root (config.Load precedence: WRKQ_PROJECT_ROOT /
// ASP_PROJECT / .env.local / config.yaml), optionally overridden by --project
// resolved against the database. The RPC layer receives already-scoped
// selectors/paths and never reads project-root env or flags itself.
type scoper struct{ cfg *config.Config }

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
			c := *cfg
			c.ProjectRoot = projectPath
			cfg = &c
		}
	}
	return &scoper{cfg: cfg}, nil
}

func (s *scoper) selector(raw string, defaultToRoot bool) string {
	return projectroot.ApplyToSelector(s.cfg, raw, defaultToRoot)
}

func (s *scoper) paths(raw []string, defaultToRoot bool) []string {
	return projectroot.ApplyToPaths(s.cfg, raw, defaultToRoot)
}

// path scopes a single raw PATH argument (not a typed selector) — mirrors legacy
// applyProjectRootToPath. restore --to is a destination path, so it uses this
// rather than selector() (which exempts typed t:/c: + ID/UUID tokens).
func (s *scoper) path(raw string, defaultToRoot bool) string {
	return projectroot.ApplyToPath(s.cfg, raw, defaultToRoot)
}

// projectRoot returns the configured project root (normalized, "" when unset).
// check-inbox needs it as the project ID for the requested-by ack-pending query,
// mirroring legacy normalizeProjectRoot(app.Config).
func (s *scoper) projectRoot() string {
	return projectroot.Normalize(s.cfg)
}
