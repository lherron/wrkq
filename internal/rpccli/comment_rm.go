package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/bulk"
	"github.com/lherron/wrkq/internal/id"
	"github.com/spf13/cobra"
)

// newCommentRmCmd mirrors `wrkq comment rm` on the caller-owned-confirmation seam
// (architecture/records/invariants/wrkq.mutation.caller-owned-confirmation.yaml).
// The durable mutation runs through wrkq.comment.delete with an EXPLICIT mode
// (soft default, purge for --purge); the mirror owns the legacy [y/N] prompt +
// abort + --yes, dry-run rendering, the unknown-ref warn-and-continue, and the
// TTY/non-TTY output shapes.
//
// A stale --if-match is a per-item failure, not a skip (T-12765): the item is
// refused with a typed WRKQ_CONFLICT, the remaining ids still run, the non-TTY
// stdout array lists only what was deleted, and the command exits 1 — bulk
// `set --if-match` semantics.
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
	actor, err := actorFlag(cmd)
	if err != nil {
		return err
	}
	ctx := cmd.Context()
	out := cmd.OutOrStdout()
	errOut := cmd.ErrOrStderr()
	tty := isStdoutTTY(out)

	results := []map[string]interface{}{}
	failed := &bulk.Result{TotalItems: len(args)}

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
			return rpcRefusal(serr)
		}
		var c commentShowDTO
		if uerr := json.Unmarshal(raw, &c); uerr != nil {
			return uerr
		}

		// --if-match: a stale fence fails this item before any prompt or preview,
		// with the conflict the server CAS would return.
		if f.ifMatch > 0 && c.ETag != f.ifMatch {
			failed.Failed++
			failed.Errors = append(failed.Errors, bulk.ItemError{Item: c.ID, Error: &Error{
				DomainID: "WRKQ_CONFLICT",
				Message:  fmt.Sprintf("comment etag precondition failed for %s (current: %d, expected: %d)", c.ID, c.ETag, f.ifMatch),
			}})
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
		params := commentDeleteParams(c.UUID, mode, actor, f.ifMatch)
		if _, derr := tr.Call(ctx, "wrkq.comment.delete", params); derr != nil {
			var re *Error
			if errors.As(derr, &re) && re.DomainID == "WRKQ_CONFLICT" {
				// Edited between show and delete: the server CAS refused this item.
				failed.Failed++
				failed.Errors = append(failed.Errors, bulk.ItemError{Item: c.ID, Error: &Error{
					DomainID: re.DomainID, Message: fmt.Sprintf("%s for %s", re.Message, c.ID),
				}})
				continue
			}
			return rpcRefusal(derr)
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
		if err := enc.Encode(results); err != nil {
			return err
		}
	}
	return commentRmFailure(failed)
}

// commentRmFailure turns per-item failures into the command's error: the item's
// own typed error when one failed, otherwise a WRKQ_CONFLICT whose hint lines
// name each refused item. Every failure recorded here is a fence conflict.
func commentRmFailure(r *bulk.Result) error {
	switch len(r.Errors) {
	case 0:
		return nil
	case 1:
		return r.Errors[0].Error
	}
	lines := make([]string, 0, len(r.Errors)+1)
	lines = append(lines, fmt.Sprintf("%d comment deletions failed", len(r.Errors)))
	for _, e := range r.Errors {
		lines = append(lines, rpcMessage(e.Error))
	}
	return &Error{DomainID: "WRKQ_CONFLICT", Message: strings.Join(lines, "\n")}
}

// commentDeleteParams builds the wrkq.comment.delete params the mirror sends
// AFTER the local prechecks (fence, prompt) have passed: the resolved comment UUID, the
// EXPLICIT mode (caller-owned-confirmation invariant — never the absent-mode
// default), the optional actor, and — crucially — the etag precondition forwarded
// as the server CAS (`ifMatch`) so the COMMIT is race-guarded against a concurrent
// bump between the resolve (show) and the delete. ifMatch is omitted when ≤ 0 (no
// --if-match supplied), exactly as legacy skips the check for if-match=0.
func commentDeleteParams(commentUUID, mode, actor string, ifMatch int64) map[string]any {
	params := map[string]any{"id": commentUUID, "mode": mode}
	if actor != "" {
		params["actor"] = actor
	}
	if ifMatch > 0 {
		params["ifMatch"] = ifMatch
	}
	return params
}

// commentBodyPreview mirrors legacy: newlines → spaces, truncate >60 to 57+"...".
func commentBodyPreview(body string) string {
	preview := strings.ReplaceAll(body, "\n", " ")
	if len(preview) > 60 {
		preview = preview[:57] + "..."
	}
	return preview
}
