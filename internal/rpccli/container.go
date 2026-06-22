package rpccli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// containerCatModel mirrors the legacy `runContainerCat` local `Container` struct
// (internal/cli/container_cat.go) field-for-field and tag-for-tag. The server-owned
// wrkq.container.catView returns this exact shape; the mirror decodes the raw RPC
// result into this struct so every render mode (json/ndjson/porcelain/markdown/raw)
// is produced from the SAME projection, byte-identical to legacy. Field ORDER here
// is load-bearing: legacy encodes the struct directly (struct order, not alpha), so
// json/ndjson/porcelain must preserve it.
type containerCatModel struct {
	ID          string   `json:"id"`
	UUID        string   `json:"uuid"`
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Kind        string   `json:"kind"`
	ParentID    *string  `json:"parent_id,omitempty"`
	ParentUUID  *string  `json:"parent_uuid,omitempty"`
	ParentPath  *string  `json:"parent_path,omitempty"`
	Path        string   `json:"path"`
	WebhookURLs []string `json:"webhook_urls,omitempty"`
	SortIndex   int      `json:"sort_index"`
	Etag        int64    `json:"etag"`
	CreatedAt   string   `json:"created_at"`
	UpdatedAt   string   `json:"updated_at"`
	ArchivedAt  *string  `json:"archived_at,omitempty"`
	CreatedBy   string   `json:"created_by"`
	UpdatedBy   string   `json:"updated_by"`
}

// newContainerCmd mirrors `wrkq container`. `cat` is RPC-backed via the
// server-owned wrkq.container.catView compat projection; `set` is pending.
func newContainerCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "container", Short: "Manage containers"}
	cmd.AddCommand(newContainerCatCmd())
	cmd.AddCommand(newStubCmd(mirroredCommand{use: "set <container>"}))
	return cmd
}

func newContainerCatCmd() *cobra.Command {
	var asJSON, ndjson, porcelain, noFrontmatter bool
	cmd := &cobra.Command{
		Use:     "cat <path|id>",
		Aliases: []string{"show"},
		Short:   "Print container details",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runContainerCat(cmd, args, asJSON, ndjson, porcelain, noFrontmatter)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	cmd.Flags().BoolVar(&noFrontmatter, "no-frontmatter", false, "Print body only without front matter")
	return cmd
}

func runContainerCat(cmd *cobra.Command, args []string, asJSON, ndjson, porcelain, noFrontmatter bool) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()

	// Legacy: applyProjectRootToSelector(arg, false).
	sref := sc.selector(args[0], false)
	raw, err := tr.Call(cmd.Context(), "wrkq.container.catView", map[string]string{"container": sref})
	if err != nil {
		if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
			// Legacy container cat surfaces the raw selectors.ResolveContainer error
			// ("container not found: <ref>"), not the generic "path not found".
			return fmt.Errorf("container not found: %s", sref)
		}
		return err
	}

	var c containerCatModel
	if err := json.Unmarshal(raw, &c); err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	// Legacy emits JSON for --json/--ndjson/--porcelain, or for a non-TTY stdout
	// when --no-frontmatter is not set. Indent is applied unless ndjson/porcelain.
	jsonMode := asJSON || ndjson || porcelain || (!isStdoutTTY(out) && !noFrontmatter)
	if jsonMode {
		enc := json.NewEncoder(out)
		if !ndjson && !porcelain {
			enc.SetIndent("", "  ")
		}
		return enc.Encode(&c)
	}

	// Markdown output. With --no-frontmatter this collapses to "raw" body-only:
	// just the description (if any).
	renderContainerMarkdown(out, &c, noFrontmatter)
	return nil
}

// renderContainerMarkdown reproduces legacy runContainerCat's markdown branch
// byte-for-byte: a YAML front matter block (suppressed by noFrontmatter) followed
// by the description. With noFrontmatter set it is the "raw" body-only mode.
func renderContainerMarkdown(out io.Writer, c *containerCatModel, noFrontmatter bool) {
	if !noFrontmatter {
		fmt.Fprintln(out, "---")
		fmt.Fprintf(out, "id: %s\n", c.ID)
		fmt.Fprintf(out, "uuid: %s\n", c.UUID)
		fmt.Fprintf(out, "slug: %s\n", c.Slug)
		fmt.Fprintf(out, "title: %s\n", c.Title)
		fmt.Fprintf(out, "kind: %s\n", c.Kind)
		fmt.Fprintf(out, "path: %s\n", c.Path)
		if c.ParentID != nil {
			fmt.Fprintf(out, "parent_id: %s\n", *c.ParentID)
		}
		if c.ParentUUID != nil {
			fmt.Fprintf(out, "parent_uuid: %s\n", *c.ParentUUID)
		}
		if c.ParentPath != nil {
			fmt.Fprintf(out, "parent_path: %s\n", *c.ParentPath)
		}
		if len(c.WebhookURLs) > 0 {
			webhooksJSON, _ := json.Marshal(c.WebhookURLs)
			fmt.Fprintf(out, "webhook_urls: %s\n", string(webhooksJSON))
		}
		fmt.Fprintf(out, "sort_index: %d\n", c.SortIndex)
		fmt.Fprintf(out, "etag: %d\n", c.Etag)
		fmt.Fprintf(out, "created_at: %s\n", c.CreatedAt)
		fmt.Fprintf(out, "updated_at: %s\n", c.UpdatedAt)
		if c.ArchivedAt != nil {
			fmt.Fprintf(out, "archived_at: %s\n", *c.ArchivedAt)
		}
		fmt.Fprintf(out, "created_by: %s\n", c.CreatedBy)
		fmt.Fprintf(out, "updated_by: %s\n", c.UpdatedBy)
		fmt.Fprintln(out, "---")
		fmt.Fprintln(out)
	}

	if c.Description != "" {
		fmt.Fprintln(out, c.Description)
	}
}
