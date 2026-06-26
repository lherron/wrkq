package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

// projectEntry mirrors legacy runProjects' local Project struct exactly.
type projectEntry struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title,omitempty"`
	Path  string `json:"path"`
}

func newProjectsCmd() *cobra.Command {
	var asJSON, ndjson, porcelain, one, nul, all bool
	var limit int
	var cursorTok string
	cmd := &cobra.Command{
		Use:   "projects",
		Short: "List all top-level projects",
		Long: `Lists all top-level projects (containers with no parent) in the database.

This command always shows all projects regardless of WRKQ_PROJECT_ROOT,
making it easy to see what projects exist when working in a scoped context.

Examples:
  wrkq projects              # List all projects in table format
  wrkq projects --json       # Output as JSON
  wrkq projects -1           # One project slug per line
  wrkq projects -a           # Include archived projects`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, stable, err := resolveProjectsMode(cmd, asJSON, ndjson, porcelain)
			if err != nil {
				return err
			}
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			params := map[string]any{}
			if all {
				params["includeArchived"] = true
			}
			if limit > 0 {
				params["limit"] = limit
			}
			if cursorTok != "" {
				params["cursor"] = cursorTok
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.project.listView", params)
			if err != nil {
				if re, ok := err.(*Error); ok {
					return errors.New(re.Message)
				}
				return err
			}
			var res struct {
				Items      []json.RawMessage `json:"items"`
				NextCursor string            `json:"next_cursor"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return err
			}
			if stable && res.NextCursor != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", res.NextCursor)
			}

			projects, err := decodeProjectEntries(res.Items)
			if err != nil {
				return err
			}
			if one || nul {
				slugs := make([]string, 0, len(projects))
				for _, project := range projects {
					slugs = append(slugs, project.Slug)
				}
				delimiter := "\n"
				if nul {
					delimiter = "\x00"
				}
				fmt.Fprint(cmd.OutOrStdout(), strings.Join(slugs, delimiter))
				if len(slugs) > 0 && !nul {
					fmt.Fprintln(cmd.OutOrStdout())
				}
				return nil
			}

			switch mode {
			case "json":
				var data []byte
				if stable {
					data, err = json.Marshal(projects)
				} else {
					data, err = json.MarshalIndent(projects, "", "  ")
				}
				if err != nil {
					return err
				}
				_, err = cmd.OutOrStdout().Write(append(data, '\n'))
				return err
			case "ndjson":
				for _, project := range projects {
					line, err := json.Marshal(project)
					if err != nil {
						return err
					}
					if _, err := cmd.OutOrStdout().Write(append(line, '\n')); err != nil {
						return err
					}
				}
				return nil
			case "yaml":
				return render.NewRenderer(cmd.OutOrStdout(), render.Options{Format: render.FormatYAML}).RenderYAML(projects)
			case "tsv":
				headers, rows := projectTableData(projects)
				return render.NewRenderer(cmd.OutOrStdout(), render.Options{Format: render.FormatTSV}).RenderTSV(headers, rows)
			default:
				headers, rows := projectTableData(projects)
				return render.NewRenderer(cmd.OutOrStdout(), render.Options{Format: render.FormatTable, Porcelain: stable}).RenderTable(headers, rows)
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	cmd.Flags().BoolVarP(&one, "one", "1", false, "One entry per line")
	cmd.Flags().BoolVarP(&nul, "nul", "0", false, "NUL-separated output")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum number of results to return (0 = no limit)")
	cmd.Flags().StringVar(&cursorTok, "cursor", "", "Pagination cursor from previous page")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Include archived projects")
	return cmd
}

func decodeProjectEntries(items []json.RawMessage) ([]projectEntry, error) {
	projects := make([]projectEntry, 0, len(items))
	for _, raw := range items {
		var p projectEntry
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, nil
}

func projectTableData(projects []projectEntry) ([]string, [][]string) {
	headers := []string{"ID", "Slug", "Title"}
	rows := make([][]string, 0, len(projects))
	for _, project := range projects {
		rows = append(rows, []string{project.ID, project.Slug, project.Title})
	}
	return headers, rows
}

func resolveProjectsMode(cmd *cobra.Command, asJSON, ndjson, porcelain bool) (mode string, stable bool, err error) {
	count := 0
	var explicit string
	if asJSON {
		explicit = "json"
		count++
	}
	if ndjson {
		explicit = "ndjson"
		count++
	}
	if count > 1 {
		return "", false, fmt.Errorf("choose only one output mode")
	}
	stable = porcelain
	if explicit != "" {
		return explicit, stable, nil
	}
	if stable {
		return "ndjson", true, nil
	}
	if outF := cmd.Flag("output"); outF != nil && outF.Changed {
		m := strings.ToLower(strings.TrimSpace(outF.Value.String()))
		switch m {
		case "json":
			return "json", false, nil
		case "ndjson":
			return "ndjson", false, nil
		case "porcelain":
			return "ndjson", true, nil
		case "table", "human", "yaml", "tsv":
			if m == "human" {
				return "table", false, nil
			}
			return m, false, nil
		case "raw":
			return "", false, fmt.Errorf("output mode %q is not supported for this command", m)
		default:
			return "", false, fmt.Errorf("unknown output mode %q", m)
		}
	}
	if isStdoutTTY(cmd.OutOrStdout()) {
		return "table", false, nil
	}
	return "ndjson", false, nil
}
