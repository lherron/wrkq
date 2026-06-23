package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/id"
	"github.com/spf13/cobra"
)

// newCommentRmCmd mirrors `wrkq comment rm` on the caller-owned-confirmation seam
// (architecture/records/invariants/wrkq.mutation.caller-owned-confirmation.yaml).
// The durable mutation runs through wrkq.comment.delete with an EXPLICIT mode
// (soft default, purge for --purge); the mirror owns the legacy [y/N] prompt +
// abort + --yes, the --if-match warn/skip, dry-run rendering, the unknown-ref
// warn-and-continue, and the TTY/non-TTY output shapes.
//
// Per daedalus #10190, the comment-rm prompt DIFFERS from rm --purge: it is a
// single inline "[y/N]: " line accepting EXACTLY "y"/"Y", and it prompts EVEN for
// soft-delete (legacy comment rm prompts on every disposition).
func newCommentRmCmd() *cobra.Command {
	var yes, dryRun, purge bool
	var ifMatch int64
	cmd := &cobra.Command{
		Use:   "rm <comment-id|c:token>...",
		Short: "Remove comment(s)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCommentRm(cmd, args, commentRmFlags{yes: yes, dryRun: dryRun, purge: purge, ifMatch: ifMatch})
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Preview without deleting")
	cmd.Flags().BoolVar(&purge, "purge", false, "Hard delete (remove from database)")
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Only delete if comment etag matches (0 = skip check)")
	return cmd
}

type commentRmFlags struct {
	yes     bool
	dryRun  bool
	purge   bool
	ifMatch int64
}

// commentShowDTO is the subset of wrkq.comment.show the mirror needs.
type commentShowDTO struct {
	ID   string `json:"id"`
	UUID string `json:"uuid"`
	Task string `json:"task"`
	Body string `json:"body"`
	ETag int64  `json:"etag"`
}

func runCommentRm(cmd *cobra.Command, args []string, f commentRmFlags) error {
	tr, _, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor := actorFlag(cmd)
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()
	tty := isStdoutTTY(out)

	results := []map[string]interface{}{}

	for _, orig := range args {
		// Comment refs are comment IDs / c: tokens, not paths — no project-root
		// scoping applies (legacy comment rm does not scope).
		ref := strings.TrimPrefix(orig, "c:")

		// Legacy validates ref SHAPE before any lookup: a non-UUID, non-friendly
		// ref is a hard error that aborts the whole loop (NOT a warn-continue).
		if !id.IsUUID(ref) && !id.IsFriendlyID(ref) {
			return fmt.Errorf("invalid comment reference: %s (expected friendly ID like C-00001 or UUID)", orig)
		}

		// Resolve via wrkq.comment.show. NOT_FOUND → warn + continue (legacy).
		raw, serr := tr.Call(ctx, "wrkq.comment.show", map[string]string{"id": ref})
		if serr != nil {
			if isNotFound(serr) {
				fmt.Fprintf(errOut, "Warning: comment not found: %s\n", orig)
				continue
			}
			return errors.New(rpcMessage(serr))
		}
		var c commentShowDTO
		if uerr := json.Unmarshal(raw, &c); uerr != nil {
			return uerr
		}

		// --if-match: legacy WARN+SKIP on mismatch (not an error).
		if f.ifMatch > 0 && c.ETag != f.ifMatch {
			fmt.Fprintf(errOut, "Warning: etag mismatch for %s (current: %d, expected: %d), skipping\n",
				c.ID, c.ETag, f.ifMatch)
			continue
		}

		action := "soft-delete"
		if f.purge {
			action = "purge"
		}

		// Dry-run: TTY prints a one-line preview with a 60-char body truncation;
		// non-TTY appends a {id, task_id, action, dry_run} result map.
		if f.dryRun {
			if !tty {
				results = append(results, map[string]interface{}{
					"id":      c.ID,
					"task_id": c.Task,
					"action":  action,
					"dry_run": true,
				})
				continue
			}
			fmt.Fprintf(out, "[DRY RUN] Would %s comment %s (task %s): %s\n",
				action, c.ID, c.Task, commentBodyPreview(c.Body))
			continue
		}

		// Confirm (legacy: capitalized action + "[y/N]: ", accept exactly y/Y,
		// prompts EVEN for soft-delete). --yes skips.
		actionTitle := strings.ToUpper(action[:1]) + action[1:]
		promptLine := fmt.Sprintf("%s comment %s (task %s)? [y/N]: ", actionTitle, c.ID, c.Task)
		if cerr := commentRmConfirm(cmd, f.yes, promptLine); cerr != nil {
			// Legacy treats a declined prompt as a per-comment SKIP (continue), not
			// a fatal abort.
			continue
		}

		mode := "soft"
		if f.purge {
			mode = "purge"
		}
		params := map[string]any{"id": c.UUID, "mode": mode}
		if actor != "" {
			params["actor"] = actor
		}
		if _, derr := tr.Call(ctx, "wrkq.comment.delete", params); derr != nil {
			return errors.New(rpcMessage(derr))
		}

		if f.purge {
			results = append(results, map[string]interface{}{
				"id":      c.ID,
				"uuid":    c.UUID,
				"task_id": c.Task,
				"purged":  true,
			})
			if tty {
				fmt.Fprintf(out, "Purged: %s\n", c.ID)
			}
		} else {
			results = append(results, map[string]interface{}{
				"id":      c.ID,
				"uuid":    c.UUID,
				"task_id": c.Task,
				"deleted": true,
			})
			if tty {
				fmt.Fprintf(out, "Deleted: %s\n", c.ID)
			}
		}
	}

	if !tty {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}
	return nil
}

// commentBodyPreview mirrors legacy: newlines → spaces, truncate >60 to 57+"...".
func commentBodyPreview(body string) string {
	preview := strings.ReplaceAll(body, "\n", " ")
	if len(preview) > 60 {
		preview = preview[:57] + "..."
	}
	return preview
}
