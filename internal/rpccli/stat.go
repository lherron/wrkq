package rpccli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// statMetadata mirrors internal/cli stat's local Metadata struct EXACTLY (field
// order + json tags + omitempty) so encoded output is byte-identical.
type statMetadata struct {
	Type     string                 `json:"type"`
	UUID     string                 `json:"uuid"`
	ID       string                 `json:"id"`
	Slug     string                 `json:"slug"`
	Title    string                 `json:"title,omitempty"`
	State    string                 `json:"state,omitempty"`
	Priority int                    `json:"priority,omitempty"`
	ETag     int64                  `json:"etag"`
	Extra    map[string]interface{} `json:"extra,omitempty"`
}

// newStatCmd mirrors `wrkq stat`. RPC-backed: each ref is resolved via
// wrkq.task.show, falling back to wrkq.container.show, and re-projected into the
// legacy stat metadata shape. Output is byte-identical to the legacy CLI
// (proven by TestParity).
func newStatCmd() *cobra.Command {
	var asJSON, nul bool
	cmd := &cobra.Command{
		Use:   "stat <path|id>...",
		Short: "Print metadata for tasks or containers",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStat(cmd, args, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVarP(&nul, "nul", "0", false, "NUL-separated output")
	return cmd
}

func runStat(cmd *cobra.Command, args []string, asJSON bool) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()

	results := []statMetadata{}
	for _, ref := range args {
		// Legacy: applyProjectRootToSelector(arg, false) — scoped value is used for
		// both resolution and the "path not found" message.
		meta, err := statResolve(cmd, tr, sc.selector(ref, false))
		if err != nil {
			return err
		}
		results = append(results, meta)
	}

	out := cmd.OutOrStdout()
	if asJSON || !isStdoutTTY(out) {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}
	for _, meta := range results {
		fmt.Fprintf(out, "type: %s\n", meta.Type)
		fmt.Fprintf(out, "uuid: %s\n", meta.UUID)
		fmt.Fprintf(out, "id: %s\n", meta.ID)
		fmt.Fprintf(out, "slug: %s\n", meta.Slug)
		if meta.Title != "" {
			fmt.Fprintf(out, "title: %s\n", meta.Title)
		}
		if meta.State != "" {
			fmt.Fprintf(out, "state: %s\n", meta.State)
		}
		if meta.Priority > 0 {
			fmt.Fprintf(out, "priority: %d\n", meta.Priority)
		}
		fmt.Fprintf(out, "etag: %d\n", meta.ETag)
		fmt.Fprintln(out)
	}
	return nil
}

// statResolve resolves a ref to its metadata, trying task then container,
// mirroring the legacy CLI's resolution order and "path not found" error.
func statResolve(cmd *cobra.Command, tr Transport, ref string) (statMetadata, error) {
	show, err := tr.Call(cmd.Context(), "wrkq.task.show", map[string]string{"task": ref})
	if err == nil {
		var t struct {
			UUID     string `json:"uuid"`
			ID       string `json:"id"`
			Slug     string `json:"slug"`
			Title    string `json:"title"`
			State    string `json:"state"`
			Priority int    `json:"priority"`
			ETag     int64  `json:"etag"`
		}
		if jerr := json.Unmarshal(show, &t); jerr != nil {
			return statMetadata{}, jerr
		}
		return statMetadata{Type: "task", UUID: t.UUID, ID: t.ID, Slug: t.Slug,
			Title: t.Title, State: t.State, Priority: t.Priority, ETag: t.ETag}, nil
	}
	if re, ok := err.(*Error); !ok || re.DomainID != "WRKQ_NOT_FOUND" {
		return statMetadata{}, err
	}

	cshow, cerr := tr.Call(cmd.Context(), "wrkq.container.show", map[string]string{"path": ref})
	if cerr != nil {
		return statMetadata{}, fmt.Errorf("path not found: %s", ref)
	}
	var c struct {
		UUID  string `json:"uuid"`
		ID    string `json:"id"`
		Slug  string `json:"slug"`
		Title string `json:"title"`
		ETag  int64  `json:"etag"`
	}
	if jerr := json.Unmarshal(cshow, &c); jerr != nil {
		return statMetadata{}, jerr
	}
	return statMetadata{Type: "container", UUID: c.UUID, ID: c.ID, Slug: c.Slug, Title: c.Title, ETag: c.ETag}, nil
}
