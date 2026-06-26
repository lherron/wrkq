package rpccli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// newMvCmd mirrors `wrkq mv`: task sources go through wrkq.task.move and
// container sources go through the dedicated wrkq.container.move compatibility
// method. The mirror owns source-type probing, output rendering, and legacy's
// parsed-but-ignored --type/--yes/--nullglob flags.
func newMvCmd() *cobra.Command {
	var typ string
	var ifMatch int64
	var dryRun, yes, nullglob, overwriteTask bool
	cmd := &cobra.Command{
		Use:   "mv <src>... <dst>",
		Short: "Move or rename tasks and containers",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMv(cmd, args, mvFlags{ifMatch: ifMatch, dryRun: dryRun, nullglob: nullglob, overwriteTask: overwriteTask})
		},
	}
	cmd.Flags().StringVar(&typ, "type", "", "Force type: t (task), p (project/container)")
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Only move if etag matches")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be moved without applying")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompts")
	cmd.Flags().BoolVar(&nullglob, "nullglob", false, "Zero matches is a no-op instead of error")
	cmd.Flags().BoolVar(&overwriteTask, "overwrite-task", false, "Allow overwriting existing tasks")
	return cmd
}

type mvFlags struct {
	ifMatch       int64
	dryRun        bool
	nullglob      bool
	overwriteTask bool
}

func runMv(cmd *cobra.Command, args []string, flags mvFlags) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor := actorFlag(cmd)

	// Legacy: applyProjectRootToSelector(false) for every source and the dst.
	dst := sc.selector(args[len(args)-1], false)
	sources := make([]string, 0, len(args)-1)
	for _, src := range args[:len(args)-1] {
		sources = append(sources, sc.selector(src, false))
	}

	if len(sources) > 1 {
		if _, err := tr.Call(cmd.Context(), "wrkq.container.show", map[string]string{"path": dst}); err != nil {
			if re, ok := err.(*Error); ok {
				return fmt.Errorf("destination must be an existing container for multiple sources: %s", re.Message)
			}
			return fmt.Errorf("destination must be an existing container for multiple sources: %w", err)
		}
		for _, src := range sources {
			if err := runMvTask(cmd, tr, src, dst, actor, flags, true); err != nil {
				return err
			}
		}
		return nil
	}

	return runMvTask(cmd, tr, sources[0], dst, actor, flags, false)
}

func runMvTask(cmd *cobra.Command, tr Transport, src, dst, actor string, flags mvFlags, dstIsContainer bool) error {
	// Resolve the source task and capture its pre-move path (the legacy output's
	// "source"). If it is not a task, fall through to container resolution just
	// like legacy moveToContainer / runMv does.
	show, err := tr.Call(cmd.Context(), "wrkq.task.show", map[string]string{"task": src})
	if err != nil {
		if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
			return runMvContainer(cmd, tr, src, dst, actor, flags, dstIsContainer)
		}
		return err
	}
	var t struct {
		UUID string `json:"uuid"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(show, &t); err != nil {
		return err
	}

	if !dstIsContainer {
		if _, cerr := tr.Call(cmd.Context(), "wrkq.container.show", map[string]string{"path": dst}); cerr == nil {
			dstIsContainer = true
		}
	}
	sourceLabel := t.ID
	if !dstIsContainer {
		sourceLabel = src
	}

	if flags.dryRun {
		if !dstIsContainer {
			wouldOverwrite := false
			if destShow, serr := tr.Call(cmd.Context(), "wrkq.task.show", map[string]string{"task": dst}); serr == nil {
				var dest struct {
					UUID string `json:"uuid"`
				}
				if err := json.Unmarshal(destShow, &dest); err != nil {
					return err
				}
				if dest.UUID != "" && dest.UUID != t.UUID {
					if !flags.overwriteTask {
						return fmt.Errorf("destination task already exists: %s (use --overwrite-task to replace)", dst)
					}
					wouldOverwrite = true
				}
			}
			if isStdoutTTY(cmd.OutOrStdout()) {
				if wouldOverwrite {
					fmt.Fprintf(cmd.OutOrStdout(), "Would overwrite task at %s\n", dst)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Would rename/move task %s -> %s\n", sourceLabel, dst)
				return nil
			}
			return encodeJSONIndent(cmd, map[string]interface{}{
				"type": "task", "source": sourceLabel, "destination": dst, "dry_run": true, "would_overwrite": wouldOverwrite,
			})
		}
		if isStdoutTTY(cmd.OutOrStdout()) {
			fmt.Fprintf(cmd.OutOrStdout(), "Would move task %s -> %s\n", sourceLabel, dst)
			return nil
		}
		return encodeJSONIndent(cmd, map[string]interface{}{
			"type": "task", "source": sourceLabel, "destination": dst, "dry_run": true,
		})
	}

	params := map[string]any{"task": src, "targetPath": dst}
	if actor != "" {
		params["actor"] = actor
	}
	if flags.ifMatch != 0 {
		params["expectEtag"] = flags.ifMatch
	}
	if flags.overwriteTask {
		params["overwriteTask"] = true
	}
	if _, err := tr.Call(cmd.Context(), "wrkq.task.move", params); err != nil {
		if re, ok := err.(*Error); ok {
			return fmt.Errorf("%s", re.Message)
		}
		return err
	}

	if isStdoutTTY(cmd.OutOrStdout()) {
		if dstIsContainer {
			fmt.Fprintf(cmd.OutOrStdout(), "Moved task: %s -> %s\n", sourceLabel, dst)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Moved/renamed task: %s -> %s\n", sourceLabel, dst)
		}
		return nil
	}
	return encodeJSONIndent(cmd, map[string]interface{}{
		"type": "task", "source": sourceLabel, "destination": dst, "moved": true,
	})
}

func runMvContainer(cmd *cobra.Command, tr Transport, src, dst, actor string, flags mvFlags, dstIsContainer bool) error {
	if _, err := tr.Call(cmd.Context(), "wrkq.container.show", map[string]string{"path": src}); err != nil {
		if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
			return fmt.Errorf("source not found: %s", src)
		}
		return err
	}
	if !dstIsContainer {
		if _, cerr := tr.Call(cmd.Context(), "wrkq.container.show", map[string]string{"path": dst}); cerr == nil {
			dstIsContainer = true
		}
	}

	params := map[string]any{
		"container":              src,
		"destination":            dst,
		"destinationIsContainer": dstIsContainer,
	}
	if flags.dryRun {
		params["dryRun"] = true
	}
	if flags.ifMatch != 0 {
		params["expectEtag"] = flags.ifMatch
	}
	if actor != "" {
		params["actor"] = actor
	}
	if _, err := tr.Call(cmd.Context(), "wrkq.container.move", params); err != nil {
		if re, ok := err.(*Error); ok {
			return fmt.Errorf("%s", re.Message)
		}
		return err
	}

	if flags.dryRun {
		if isStdoutTTY(cmd.OutOrStdout()) {
			if dstIsContainer {
				fmt.Fprintf(cmd.OutOrStdout(), "Would move container %s -> %s\n", src, dst)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Would rename/move container %s -> %s\n", src, dst)
			}
			return nil
		}
		return encodeJSONIndent(cmd, map[string]interface{}{
			"type": "container", "source": src, "destination": dst, "dry_run": true,
		})
	}

	if isStdoutTTY(cmd.OutOrStdout()) {
		if dstIsContainer {
			fmt.Fprintf(cmd.OutOrStdout(), "Moved container: %s -> %s\n", src, dst)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Moved/renamed container: %s -> %s\n", src, dst)
		}
		return nil
	}
	return encodeJSONIndent(cmd, map[string]interface{}{
		"type": "container", "source": src, "destination": dst, "moved": true,
	})
}
