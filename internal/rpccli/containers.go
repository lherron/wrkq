package rpccli

import (
	"encoding/json"
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

// newRmdirCmd mirrors `wrkq rmdir`. RPC-backed via wrkq.container.delete (or
// deleteRecursive with --force); output is the legacy [{path, removed, forced}]
// summary.
func newRmdirCmd() *cobra.Command {
	var force, yes bool
	cmd := &cobra.Command{
		Use:   "rmdir <path|id>...",
		Short: "Remove one or more containers",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRmdir(cmd, args, force)
		},
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force removal of non-empty containers")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompts")
	return cmd
}

func runRmdir(cmd *cobra.Command, args []string, force bool) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor := actorFlag(cmd)

	method := "wrkq.container.delete"
	if force {
		method = "wrkq.container.deleteRecursive"
	}
	// Legacy: applyProjectRootToPaths(args, defaultToRoot=false).
	paths := sc.paths(args, false)
	results := make([]map[string]interface{}, 0, len(paths))
	for _, path := range paths {
		params := map[string]any{"path": path}
		if actor != "" {
			params["actor"] = actor
		}
		if _, err := tr.Call(cmd.Context(), method, params); err != nil {
			return err
		}
		results = append(results, map[string]interface{}{"path": path, "removed": true, "forced": force})
	}
	return encodeJSONIndent(cmd, results)
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
