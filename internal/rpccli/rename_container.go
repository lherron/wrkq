package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lherron/wrkq/internal/paths"
	"github.com/spf13/cobra"
)

// newRenameContainerCmd mirrors `wrkq rename-container <path|id> <new-slug>`.
//
// rename-container is a NON-destructive identity-preserving mutation (no prompt):
// it changes a container's slug + title in place via the new server method
// wrkq.container.update (T-05112 daedalus ruling, hrcchat#10196). The container's
// UUID, friendly ID, children, and history survive — the server does an in-place
// UPDATE (never delete+recreate) and path views reflect the new slug.
//
// The mirror owns ONLY the caller-side project-root scoping of the container
// selector and the legacy dry-run / TTY / non-TTY output rendering. Slug
// normalization/validation, etag CAS, attribution, and the container.updated
// event are SERVER-owned. The mirror normalizes the slug client-side purely to
// reproduce the legacy dry-run display + invalid-slug error wording byte-for-byte;
// the server independently re-validates the slug on commit (authoritative).
func newRenameContainerCmd() *cobra.Command {
	var (
		title   string
		ifMatch int64
		dryRun  bool
	)
	cmd := &cobra.Command{
		Use:   "rename-container <path|id> <new-slug>",
		Short: "Rename a container's slug and title",
		Long: `Rename a container by updating both its slug and title.

By default, the title is set to match the new slug. Use --title to specify
a custom title.

Examples:
  # Rename both slug and title to "new-project"
  wrkq rename-container old-project new-project

  # Rename with custom title
  wrkq rename-container P-00001 new-project --title "New Project Name"

  # Rename a top-level container in canonical DB
  WRKQ_PROJECT_ROOT="" wrkq rename-container rex rex-control-plane`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRenameContainer(cmd, args[0], args[1], renameContainerFlags{
				title: title, ifMatch: ifMatch, dryRun: dryRun,
			})
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "Custom title (defaults to new slug)")
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Only rename if etag matches")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would change without applying")
	return cmd
}

type renameContainerFlags struct {
	title   string
	ifMatch int64
	dryRun  bool
}

func runRenameContainer(cmd *cobra.Command, ref, newSlug string, f renameContainerFlags) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor := actorFlag(cmd)
	ctx := cmd.Context()

	// Caller-side scoping (legacy: applyProjectRootToSelector(ref, false)). The
	// server never reads project-root.
	containerSelector := sc.selector(ref, false)

	// Normalize the new slug client-side, mirroring legacy's invalid-slug error
	// text exactly. The server re-validates on commit (authoritative).
	normalizedSlug, nerr := paths.NormalizeSlug(newSlug)
	if nerr != nil {
		return fmt.Errorf("invalid new slug %q: %w", newSlug, nerr)
	}

	// Resolve the container via wrkq.container.show to obtain its current
	// slug/title/path/etag for the legacy display + output (legacy: ResolveContainer
	// + GetByUUID). Legacy surfaces "container not found: <selector>" on a miss.
	raw, serr := tr.Call(ctx, "wrkq.container.show", map[string]string{"path": containerSelector})
	if serr != nil {
		if isNotFound(serr) {
			return fmt.Errorf("container not found: %s", containerSelector)
		}
		return errors.New(rpcMessage(serr))
	}
	var c struct {
		UUID  string `json:"uuid"`
		ID    string `json:"id"`
		Slug  string `json:"slug"`
		Title string `json:"title"`
	}
	if uerr := json.Unmarshal(raw, &c); uerr != nil {
		return uerr
	}
	// Legacy renders "container_path" / the TTY line from selectors.ResolveContainer's
	// SECOND return value, which is the container's FRIENDLY ID (e.g. P-00002), not
	// the slug path.
	containerPath := c.ID

	// Determine the new title (legacy: default = new slug; --title overrides).
	newTitle := normalizedSlug
	if f.title != "" {
		newTitle = f.title
	}

	// Legacy display title: the container's current title if set, else its slug.
	oldTitle := c.Slug
	if c.Title != "" {
		oldTitle = c.Title
	}

	out := cmd.OutOrStdout()
	tty := isStdoutTTY(out)

	if f.dryRun {
		if !tty {
			return encodeJSONIndent(cmd, map[string]interface{}{
				"dry_run":        true,
				"container_uuid": c.UUID,
				"container_path": containerPath,
				"old_slug":       c.Slug,
				"new_slug":       normalizedSlug,
				"old_title":      oldTitle,
				"new_title":      newTitle,
			})
		}
		fmt.Fprintf(out, "Would rename container:\n")
		fmt.Fprintf(out, "  Path:  %s\n", containerPath)
		fmt.Fprintf(out, "  Slug:  %s -> %s\n", c.Slug, normalizedSlug)
		fmt.Fprintf(out, "  Title: %s -> %s\n", oldTitle, newTitle)
		return nil
	}

	// Commit via the narrow {slug, title} patch. The server normalizes/validates
	// the slug, enforces the etag CAS, records attribution, and emits
	// container.updated. A stale etag / slug conflict / invalid slug map to typed
	// WRKQ_CONFLICT / WRKQ_VALIDATION; surface the bare server message (legacy
	// returns the raw store error here).
	params := map[string]any{
		"container": containerSelector,
		"patch": map[string]any{
			"slug":  normalizedSlug,
			"title": newTitle,
		},
	}
	if actor != "" {
		params["actor"] = actor
	}
	if f.ifMatch != 0 {
		params["expectEtag"] = f.ifMatch
	}
	if _, uerr := tr.Call(ctx, "wrkq.container.update", params); uerr != nil {
		return errors.New(renameUpdateErr(uerr, f.ifMatch))
	}

	if !tty {
		return encodeJSONIndent(cmd, map[string]interface{}{
			"container_uuid": c.UUID,
			"container_path": containerPath,
			"old_slug":       c.Slug,
			"new_slug":       normalizedSlug,
			"old_title":      oldTitle,
			"new_title":      newTitle,
			"renamed":        true,
		})
	}
	fmt.Fprintf(out, "Renamed container: %s -> %s\n", containerPath, normalizedSlug)
	return nil
}

// renameUpdateErr maps the wrkq.container.update error to the legacy error wording.
// Legacy returns the bare store error from UpdateFieldsWithAttribution. For an etag
// mismatch (only possible when --if-match is set) legacy emits
// "etag mismatch: expected <ifMatch>, got <currentEtag>"; the typed WRKQ_CONFLICT
// carries the server-observed currentEtag in its data, so reconstruct it. Every
// other error surfaces the bare server message.
func renameUpdateErr(err error, ifMatch int64) string {
	var re *Error
	if ifMatch != 0 && errors.As(err, &re) && re.DomainID == "WRKQ_CONFLICT" {
		var data struct {
			CurrentEtag *int64 `json:"currentEtag"`
		}
		if len(re.Data) > 0 && json.Unmarshal(re.Data, &data) == nil && data.CurrentEtag != nil {
			return fmt.Sprintf("etag mismatch: expected %d, got %d", ifMatch, *data.CurrentEtag)
		}
	}
	return rpcMessage(err)
}
