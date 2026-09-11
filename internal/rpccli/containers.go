package rpccli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/scope"
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

// newArchiveCmd exposes the existing non-destructive container archive
// mutation directly. Archive intentionally has no emptiness guard: preserving
// a project together with its child containers and tasks is its primary use.
func newArchiveCmd() *cobra.Command {
	var ifMatch int64
	var yes bool
	cmd := &cobra.Command{
		Use:   "archive <path|id>...",
		Short: "Archive one or more containers",
		Long: `Archives one or more containers without deleting their descendants or tasks.

Before acting, archive reports how many live tasks in the complete descendant
subtree will move to cancelled and asks for confirmation; --yes skips the prompt.
Archived containers remain available with --all. Use "wrkq unarchive" to restore
the container and exactly the task states changed by its archive cascade.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runContainerArchive(cmd, args, ifMatch, false, yes)
		},
	}
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Conditional archive (etag)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip archive confirmation")
	return cmd
}

// newUnarchiveCmd is the explicit inverse of archive over the existing
// wrkq.container.restore RPC.
func newUnarchiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unarchive <path|id>...",
		Short: "Unarchive one or more containers",
		Long: `Restores one or more archived containers to active listings.

This reverses "wrkq archive", restores exactly the task states changed by its
archive cascade, and does not recreate hard-deleted containers.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runContainerArchive(cmd, args, 0, true, true)
		},
	}
	return cmd
}

type containerArchivePreflight struct {
	UUID        string
	ActiveTasks int
}

func runContainerArchive(cmd *cobra.Command, args []string, ifMatch int64, restore, yes bool) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor, err := actorFlag(cmd)
	if err != nil {
		return err
	}

	selectors := sc.paths(args, false)
	preflights := make(map[string]containerArchivePreflight, len(selectors))
	if !restore {
		var total int
		for _, selector := range selectors {
			preflight, err := loadContainerArchivePreflight(cmd.Context(), tr, selector)
			if err != nil {
				return err
			}
			preflights[selector] = preflight
			total += preflight.ActiveTasks
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Archive %d container(s): %d live task(s) will be cancelled.\n", len(selectors), total)
		if err := archiveConfirm(cmd, yes); err != nil {
			return err
		}
	}
	results := make([]map[string]interface{}, 0, len(selectors))
	for _, selector := range selectors {
		params := map[string]any{"container": selector}
		if actor != "" {
			params["actor"] = actor
		}
		method := "wrkq.container.archive"
		if ifMatch != 0 {
			params["expectEtag"] = ifMatch
		}
		if restore {
			method = "wrkq.container.restore"
		}
		raw, callErr := tr.Call(cmd.Context(), method, params)
		if callErr != nil {
			return errors.New(rpcMessage(callErr))
		}

		result := map[string]interface{}{"path": selector}
		if restore {
			var restored struct {
				UUID string `json:"uuid"`
			}
			if err := json.Unmarshal(raw, &restored); err != nil {
				return err
			}
			result["uuid"] = restored.UUID
			result["unarchived"] = true
		} else {
			result["archived"] = true
			result["tasks_cancelled"] = preflights[selector].ActiveTasks
		}
		results = append(results, result)
	}

	if isStdoutTTY(cmd.OutOrStdout()) {
		verb := "Archived"
		if restore {
			verb = "Unarchived"
		}
		for _, result := range results {
			fmt.Fprintf(cmd.OutOrStdout(), "%s container: %s\n", verb, result["path"])
		}
		return nil
	}
	return encodeJSONIndent(cmd, results)
}

func loadContainerArchivePreflight(ctx context.Context, tr Transport, selector string) (containerArchivePreflight, error) {
	raw, err := tr.Call(ctx, "wrkq.container.show", map[string]string{"path": selector})
	if err != nil {
		return containerArchivePreflight{}, errors.New(rpcMessage(err))
	}
	var container struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal(raw, &container); err != nil {
		return containerArchivePreflight{}, err
	}

	raw, err = tr.Call(ctx, "wrkq.container.taskCounts", map[string]any{"includeArchived": true})
	if err != nil {
		return containerArchivePreflight{}, errors.New(rpcMessage(err))
	}
	var counts struct {
		Items []struct {
			UUID            string `json:"uuid"`
			ActiveTaskCount int    `json:"activeTaskCount"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &counts); err != nil {
		return containerArchivePreflight{}, err
	}
	for _, item := range counts.Items {
		if item.UUID == container.UUID {
			return containerArchivePreflight{UUID: container.UUID, ActiveTasks: item.ActiveTaskCount}, nil
		}
	}
	return containerArchivePreflight{}, fmt.Errorf("container task count not found: %s", selector)
}

func runMkdir(cmd *cobra.Command, args []string, kind string) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor, err := actorFlag(cmd)
	if err != nil {
		return err
	}

	// Legacy: applyProjectRootToPaths(args, defaultToRoot=false).
	paths := sc.paths(args, false)
	results := make([]map[string]interface{}, 0, len(paths))
	for _, path := range paths {
		// Legacy mkdir infers kind: top-level containers must be projects;
		// nested default to directory (mirrors internal/rpccli/containers.go:98-112).
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
		Short: "Permanently delete one or more containers",
		Long: `Permanently destroys one or more containers.

Plain rmdir hard-deletes only an empty container. With --force, rmdir
cascade-deletes every descendant container and task. Neither mode is reversible;
use "wrkq archive" when you want to preserve the container and its contents.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRmdir(cmd, args, force, yes)
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Permanently cascade-delete descendant containers and tasks")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompts except when deleting a project")
	return cmd
}

// rmdirImpact is the subset of wrkq.container.deleteRecursive's dry-run result the
// mirror needs: the resolved container (id/path) + the recursive impact counts.
type rmdirImpact struct {
	Container struct {
		ID   string `json:"id"`
		Path string `json:"path"`
		Kind string `json:"kind"`
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
	actor, err := actorFlag(cmd)
	if err != nil {
		return err
	}
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
			containerID, rerr = rmdirEmptyPath(ctx, cmd, tr, actor, path)
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
func rmdirEmptyPath(ctx context.Context, cmd *cobra.Command, tr Transport, actor, path string) (string, error) {
	containerID, _, kind, serr := rmdirResolve(ctx, tr, path)
	if serr != nil {
		return "", serr
	}
	if kind == "project" {
		impact, ierr := rmdirPreflight(ctx, tr, path)
		if ierr != nil {
			return "", ierr
		}
		if impact.Containers == 1 && impact.Tasks == 0 {
			warning := rmdirForceWarning(impact.Container.ID, path, 0, 0)
			if cerr := confirmProjectDeletion(cmd, warning); cerr != nil {
				return "", cerr
			}
		}
	}
	params := map[string]any{"path": path}
	if actor != "" {
		params["actor"] = actor
	}
	if _, derr := tr.Call(ctx, "wrkq.container.delete", params); derr != nil {
		message := rpcMessage(derr)
		if strings.Contains(message, "container is not empty") {
			message += "; use 'wrkq archive <path|id>' to preserve it, or 'wrkq rmdir --force' to permanently cascade-delete descendants and tasks"
		}
		return "", errors.New(message)
	}
	return containerID, nil
}

// rmdirResolve fetches the container's friendly id + path via wrkq.container.show.
// Legacy surfaces "container not found: <path>" for an unresolvable path.
func rmdirResolve(ctx context.Context, tr Transport, path string) (id, resolvedPath, kind string, err error) {
	raw, serr := tr.Call(ctx, "wrkq.container.show", map[string]string{"path": path})
	if serr != nil {
		if isNotFound(serr) {
			return "", "", "", fmt.Errorf("container not found: %s", path)
		}
		return "", "", "", errors.New(rpcMessage(serr))
	}
	var c struct {
		ID   string `json:"id"`
		Path string `json:"path"`
		Kind string `json:"kind"`
	}
	if uerr := json.Unmarshal(raw, &c); uerr != nil {
		return "", "", "", uerr
	}
	return c.ID, c.Path, c.Kind, nil
}

func rmdirPreflight(ctx context.Context, tr Transport, path string) (rmdirImpact, error) {
	raw, err := tr.Call(ctx, "wrkq.container.deleteRecursive", map[string]any{"path": path, "dryRun": true})
	if err != nil {
		return rmdirImpact{}, errors.New(rpcMessage(err))
	}
	var impact rmdirImpact
	if err := json.Unmarshal(raw, &impact); err != nil {
		return rmdirImpact{}, err
	}
	return impact, nil
}

func confirmProjectDeletion(cmd *cobra.Command, warning string) error {
	if err := rmdirForceConfirm(cmd, false, warning); err != nil {
		return errors.New("project deletion aborted: --yes does not bypass confirmation for projects; use 'wrkq archive <path|id>' for reversible removal")
	}
	return nil
}

// rmdirForcePath runs the two-phase deleteRecursive for one --force path: preflight
// (dryRun) → caller-owned confirmation (only when non-empty) → commit with the
// expected-impact CAS. The destructive WARNING block + prompt + abort live on the
// CLI side; the server method is non-interactive. Returns the container's friendly
// ID for the legacy "✓ Removed" TTY line.
func rmdirForcePath(ctx context.Context, cmd *cobra.Command, tr Transport, actor, path string, yes bool) (string, error) {
	// Phase 1: preflight impact (no mutation).
	impact, derr := rmdirPreflight(ctx, tr, path)
	if derr != nil {
		return "", derr
	}

	// The impact's container count INCLUDES the target itself (subtree depth 0), so
	// descendant containers = Containers-1. The container is "non-empty" when it has
	// recursive tasks or descendant containers — exactly when legacy prompts.
	descendants := impact.Containers - 1
	nonEmpty := impact.Tasks > 0 || descendants > 0

	if impact.Container.Kind == "project" {
		warning := rmdirForceWarning(impact.Container.ID, path, impact.Tasks, descendants)
		if cerr := confirmProjectDeletion(cmd, warning); cerr != nil {
			return "", cerr
		}
	} else if nonEmpty && !yes {
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
// (internal/rpccli/containers.go) verbatim to stderr before the "Are you sure? (yes/no): "
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

// openMirror opens the configured transport and returns the project-root scoper
// (built from local config plus a local DB or remote RPC --project lookup) and a
// close function that tears it down. Callers MUST scope every raw path/selector
// argument through the returned scoper before sending it as an RPC param.
func openMirror(cmd *cobra.Command) (Transport, *scoper, func(), error) {
	tr, sc, _, closeFn, err := openMirrorConfig(cmd)
	return tr, sc, closeFn, err
}

// openMirrorConfig is openMirror plus the resolved config. Commands that must
// know whether the server shares this machine's filesystem (`attach put` and its
// host-path fast path) read cfg.RemoteEndpoint; everything else uses openMirror.
func openMirrorConfig(cmd *cobra.Command) (Transport, *scoper, *config.Config, func(), error) {
	tr, cfg, closeFn, err := openConfiguredTransport(cmd)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	sc, err := newScoperFromConfig(cmd, cfg, tr)
	if err != nil {
		closeFn()
		return nil, nil, nil, nil, err
	}
	return tr, sc, cfg, closeFn, nil
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

// actorFlag returns the caller principal from the global attribution flags.
// --principal-ref is canonical; --as is its transitional alias. Both accept
// agent:<id> or a full agent ScopeRef, which is reduced to agent:<id> before it
// crosses the RPC boundary.
func actorFlag(cmd *cobra.Command) (string, error) {
	attr, err := optionalCommandAttribution(cmd, attribution.PrincipalEnv)
	if err != nil {
		return "", err
	}
	return attr.PrincipalRef, nil
}

func optionalCommandAttribution(cmd *cobra.Command, principalEnvNames ...string) (attribution.Attribution, error) {
	attr, err := attribution.ResolveWithPrincipalEnvs(attribution.ResolveOptions{
		Command:       cmd,
		ResolvedScope: resolvedRuntimeScope(),
	}, principalEnvNames...)
	if attribution.IsNoPrincipalConfigured(err) {
		return attribution.Attribution{}, nil
	}
	if err != nil {
		return attribution.Attribution{}, err
	}
	return attr, nil
}

func resolvedRuntimeScope() *scope.ResolvedScope {
	resolved, _, err := scope.Resolve("")
	if err != nil {
		return nil
	}
	return &resolved
}

// encodeJSONIndent matches the legacy json.NewEncoder(SetIndent("", "  ")).Encode
// rendering (2-space indent + trailing newline) used by the summary commands.
func encodeJSONIndent(cmd *cobra.Command, v interface{}) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
