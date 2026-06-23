package rpccli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
	"github.com/spf13/cobra"
)

// newMkdirCmd mirrors `wrkq mkdir`. RPC-backed via wrkq.container.create; output
// (the legacy [{path, created}] summary) is reconstructed client-side.
func newMkdirCmd() *cobra.Command {
	var parents bool
	var kind string
	cmd := &cobra.Command{
		Use:   "mkdir <path>...",
		Short: "Create one or more containers",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMkdir(cmd, args, kind)
		},
	}
	cmd.Flags().BoolVarP(&parents, "parents", "p", false, "Create parent containers as needed")
	cmd.Flags().StringVar(&kind, "kind", "", "Container kind: project, directory, feature, area (default: directory)")
	return cmd
}

func runMkdir(cmd *cobra.Command, args []string, kind string) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor := actorFlag(cmd)

	// Legacy: applyProjectRootToPaths(args, defaultToRoot=false).
	paths := sc.paths(args, false)
	results := make([]map[string]interface{}, 0, len(paths))
	for _, path := range paths {
		// Legacy mkdir infers kind: top-level containers must be projects;
		// nested default to directory (mirrors internal/cli/mkdir.go:98-112).
		segKind, err := mkdirKindFor(path, kind)
		if err != nil {
			return err
		}
		params := map[string]any{"path": path, "kind": segKind}
		if actor != "" {
			params["actor"] = actor
		}
		if _, err := tr.Call(cmd.Context(), "wrkq.container.create", params); err != nil {
			return err
		}
		results = append(results, map[string]interface{}{"path": path, "created": true})
	}

	if isStdoutTTY(cmd.OutOrStdout()) {
		for _, r := range results {
			fmt.Fprintf(cmd.OutOrStdout(), "Created: %s\n", r["path"])
		}
		return nil
	}
	return encodeJSONIndent(cmd, results)
}

// newRmdirCmd mirrors `wrkq rmdir` on the caller-owned-confirmation seam
// (architecture/records/invariants/wrkq.mutation.caller-owned-confirmation.yaml).
// Empty containers go through wrkq.container.delete. `--force` (non-empty
// recursive) uses the TWO-PHASE wrkq.container.deleteRecursive contract: a
// dryRun:true preflight returns the impact {containers,tasks,attachments,bytes},
// the mirror renders the legacy WARNING block + prompts "Are you sure? (yes/no):"
// requiring EXACTLY "yes", then commits echoing expected:{...} (the CAS race
// guard; stale impact → WRKQ_CONFLICT). The server stays non-interactive.
// Output is the legacy [{path, removed, forced}] summary.
func newRmdirCmd() *cobra.Command {
	var force, yes bool
	cmd := &cobra.Command{
		Use:   "rmdir <path|id>...",
		Short: "Remove one or more containers",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRmdir(cmd, args, force, yes)
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force removal of non-empty containers")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompts")
	return cmd
}

// rmdirImpact is the subset of wrkq.container.deleteRecursive's dry-run result the
// mirror needs: the resolved container (id/path) + the recursive impact counts.
type rmdirImpact struct {
	Container struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	} `json:"container"`
	Containers  int64 `json:"containers"`
	Tasks       int64 `json:"tasks"`
	Attachments int64 `json:"attachments"`
	Bytes       int64 `json:"bytes"`
}

func runRmdir(cmd *cobra.Command, args []string, force, yes bool) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor := actorFlag(cmd)
	ctx := cmd.Context()

	// Legacy: applyProjectRootToPaths(args, defaultToRoot=false).
	paths := sc.paths(args, false)
	tty := isStdoutTTY(cmd.OutOrStdout())
	results := make([]map[string]interface{}, 0, len(paths))
	for _, path := range paths {
		var containerID string
		var rerr error
		if force {
			containerID, rerr = rmdirForcePath(ctx, cmd, tr, actor, path, yes)
		} else {
			containerID, rerr = rmdirEmptyPath(ctx, tr, actor, path)
		}
		if rerr != nil {
			return rerr
		}
		// Legacy renders a per-container "✓ Removed: <id> (<path>)" line on a TTY
		// (from removeContainerWithAttribution); the JSON summary is the non-TTY form.
		if tty {
			fmt.Fprintf(cmd.OutOrStdout(), "✓ Removed: %s (%s)\n", containerID, path)
		}
		results = append(results, map[string]interface{}{"path": path, "removed": true, "forced": force})
	}
	if tty {
		return nil
	}
	return encodeJSONIndent(cmd, results)
}

// rmdirEmptyPath removes an empty container via wrkq.container.delete and returns
// the container's friendly ID (for the legacy "✓ Removed" TTY line).
func rmdirEmptyPath(ctx context.Context, tr Transport, actor, path string) (string, error) {
	containerID, _, serr := rmdirResolve(ctx, tr, path)
	if serr != nil {
		return "", serr
	}
	params := map[string]any{"path": path}
	if actor != "" {
		params["actor"] = actor
	}
	if _, derr := tr.Call(ctx, "wrkq.container.delete", params); derr != nil {
		return "", errors.New(rpcMessage(derr))
	}
	return containerID, nil
}

// rmdirResolve fetches the container's friendly id + path via wrkq.container.show.
// Legacy surfaces "container not found: <path>" for an unresolvable path.
func rmdirResolve(ctx context.Context, tr Transport, path string) (id, resolvedPath string, err error) {
	raw, serr := tr.Call(ctx, "wrkq.container.show", map[string]string{"path": path})
	if serr != nil {
		if isNotFound(serr) {
			return "", "", fmt.Errorf("container not found: %s", path)
		}
		return "", "", errors.New(rpcMessage(serr))
	}
	var c struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if uerr := json.Unmarshal(raw, &c); uerr != nil {
		return "", "", uerr
	}
	return c.ID, c.Path, nil
}

// rmdirForcePath runs the two-phase deleteRecursive for one --force path: preflight
// (dryRun) → caller-owned confirmation (only when non-empty) → commit with the
// expected-impact CAS. The destructive WARNING block + prompt + abort live on the
// CLI side; the server method is non-interactive. Returns the container's friendly
// ID for the legacy "✓ Removed" TTY line.
func rmdirForcePath(ctx context.Context, cmd *cobra.Command, tr Transport, actor, path string, yes bool) (string, error) {
	// Phase 1: preflight impact (no mutation).
	dryParams := map[string]any{"path": path, "dryRun": true}
	raw, derr := tr.Call(ctx, "wrkq.container.deleteRecursive", dryParams)
	if derr != nil {
		return "", errors.New(rpcMessage(derr))
	}
	var impact rmdirImpact
	if uerr := json.Unmarshal(raw, &impact); uerr != nil {
		return "", uerr
	}

	// The impact's container count INCLUDES the target itself (subtree depth 0), so
	// descendant containers = Containers-1. The container is "non-empty" when it has
	// recursive tasks or descendant containers — exactly when legacy prompts.
	descendants := impact.Containers - 1
	nonEmpty := impact.Tasks > 0 || descendants > 0

	if nonEmpty && !yes {
		warning := rmdirForceWarning(impact.Container.ID, path, impact.Tasks, descendants)
		if cerr := rmdirForceConfirm(cmd, false, warning); cerr != nil {
			return "", cerr
		}
	}

	// Phase 2: commit echoing the exact expected impact (CAS race guard).
	commitParams := map[string]any{
		"path": path,
		"expected": map[string]any{
			"containers":  impact.Containers,
			"tasks":       impact.Tasks,
			"attachments": impact.Attachments,
			"bytes":       impact.Bytes,
		},
	}
	if actor != "" {
		commitParams["actor"] = actor
	}
	if _, cerr := tr.Call(ctx, "wrkq.container.deleteRecursive", commitParams); cerr != nil {
		return "", errors.New(rpcMessage(cerr))
	}
	return impact.Container.ID, nil
}

// rmdirForceWarning renders the legacy rmdir --force destructive WARNING block
// (internal/cli/rmdir.go) verbatim to stderr before the "Are you sure? (yes/no): "
// prompt line.
func rmdirForceWarning(containerID, path string, tasks, descendants int64) string {
	var b strings.Builder
	b.WriteString("\nWARNING: This will permanently delete:\n")
	fmt.Fprintf(&b, "  - Container: %s (%s)\n", containerID, path)
	if tasks > 0 {
		fmt.Fprintf(&b, "  - %d task(s)\n", tasks)
	}
	if descendants > 0 {
		fmt.Fprintf(&b, "  - %d child container(s) (and all their contents)\n", descendants)
	}
	b.WriteString("\nThis action CANNOT be undone.\n\n")
	return b.String()
}

// ── shared mirror helpers ────────────────────────────────────────────────────

// openMirror opens the bootstrap handle + in-process transport and returns the
// project-root scoper (built from the handle config + --project override) and a
// close function that tears down both. Callers MUST scope every raw path/selector
// argument through the returned scoper before sending it as an RPC param.
func openMirror(cmd *cobra.Command) (Transport, *scoper, func(), error) {
	h, err := bootstrap.Open(dbOverride(cmd))
	if err != nil {
		return nil, nil, nil, err
	}
	// Resolve --project against the DB before the serve loop owns the handle.
	sc, err := newScoper(cmd, h)
	if err != nil {
		_ = h.Close()
		return nil, nil, nil, err
	}
	tr, err := NewInProcess(h)
	if err != nil {
		_ = h.Close()
		return nil, nil, nil, err
	}
	return tr, sc, func() { _ = tr.Close() }, nil
}

// mkdirKindFor mirrors legacy mkdir's kind inference for the final path segment.
func mkdirKindFor(path, userKind string) (string, error) {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	topLevel := len(segs) == 1
	if topLevel {
		if userKind != "" && userKind != "project" {
			return "", fmt.Errorf("top-level containers must be projects (got --kind %s)", userKind)
		}
		return "project", nil
	}
	if userKind == "project" {
		return "", fmt.Errorf("--kind project is only valid for top-level containers")
	}
	if userKind != "" {
		return userKind, nil
	}
	return "directory", nil
}

func actorFlag(cmd *cobra.Command) string {
	if f := cmd.Flag("as"); f != nil {
		return f.Value.String()
	}
	return ""
}

// encodeJSONIndent matches the legacy json.NewEncoder(SetIndent("", "  ")).Encode
// rendering (2-space indent + trailing newline) used by the summary commands.
func encodeJSONIndent(cmd *cobra.Command, v interface{}) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
