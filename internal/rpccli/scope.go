package rpccli

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/projectroot"
	"github.com/spf13/cobra"
)

// scoper applies the project-root transform to raw user path/selector arguments
// BEFORE they become RPC params. It reproduces legacy appctx caller semantics:
// the configured project root (config.Load precedence: WRKQ_PROJECT_ROOT /
// ASP_PROJECT / .env.local / config.yaml), optionally overridden by --project
// resolved against the database. The RPC layer receives already-scoped
// selectors/paths and never reads project-root env or flags itself.
type scoper struct{ cfg *config.Config }

func newScoperFromConfig(cmd *cobra.Command, cfg *config.Config, tr Transport) (*scoper, error) {
	if pf := cmd.Flag("project"); pf != nil {
		if sel := pf.Value.String(); sel != "" {
			raw, err := tr.Call(cmd.Context(), "wrkq.container.show", map[string]string{"path": sel})
			if err != nil {
				return nil, err
			}
			var out struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				return nil, err
			}
			c := *cfg
			c.ProjectRoot = out.Path
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

// containerSelectorWithAbsoluteFallback scopes a CONTAINER selector under the
// caller's project root FIRST, and only when that scoped form does not resolve
// does it fall back to the raw selector as an ABSOLUTE path rooted at a
// registered project.
//
// Scoped-first is what keeps wrkq.project-root.caller-semantics intact: a
// selector that resolves under the caller's own root always wins, and the error
// a caller sees for a genuinely missing path still names the SCOPED path. The
// fallback exists because a campaign in ANOTHER project was otherwise reachable
// only by its P- id — `--campaign alpha/camp1` from project root `beta` became
// `beta/alpha/camp1` with no way to write an absolute path (T-07701).
//
// The fallback is deliberately narrow. It requires a multi-segment selector
// whose FIRST segment names a top-level container of kind `project`, so a bare
// slug (`camp1`) is never silently re-pointed at another project's container.
func (s *scoper) containerSelectorWithAbsoluteFallback(ctx context.Context, tr Transport, raw string) string {
	scoped := s.selector(raw, false)
	token := strings.TrimPrefix(strings.TrimPrefix(raw, "c:"), "t:")
	if scoped == raw || !strings.Contains(token, "/") {
		return scoped
	}
	if _, err := tr.Call(ctx, "wrkq.container.show", map[string]string{"path": scoped}); err == nil {
		return scoped
	}
	if _, err := tr.Call(ctx, "wrkq.container.show", map[string]string{"path": raw}); err != nil {
		return scoped
	}
	rawProject, err := tr.Call(ctx, "wrkq.container.show", map[string]string{
		"path": strings.SplitN(token, "/", 2)[0],
	})
	if err != nil {
		return scoped
	}
	var project struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(rawProject, &project); err != nil || project.Kind != "project" {
		return scoped
	}
	return raw
}
