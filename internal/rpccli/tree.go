package rpccli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/lherron/wrkq/internal/style"
	"github.com/spf13/cobra"
)

// newTreeCmd mirrors `wrkq tree` via wrkq.task.treeView (the server-owned
// compatibility tree projection that reproduces legacy recursive traversal:
// container pruning, "all done" rollups, subtask nesting, hidden-container
// counting). The SERVER owns the entire hierarchy walk; the CLI owns ONLY byte
// rendering (json / ndjson / porcelain).
//
// Implemented surface (byte-proven against legacy, non-TTY): the bare non-TTY
// default (NDJSON), --json, --ndjson, --porcelain, --pretty, -L/--level,
// -a/--all, --open, single path, and project-root scoping. --pretty forces the
// human tree layout even when stdout is piped; WRKQ_NOW pins the relative
// "opened N ago" text in parity tests and color remains terminal-gated, so the
// forced output is deterministic.
func newTreeCmd() *cobra.Command {
	var asJSON, ndjson, porcelain, pretty, includeArchived, openOnly bool
	var depth int
	var fields string
	cmd := &cobra.Command{
		Use:   "tree [path...]",
		Short: "Display containers and tasks in a tree structure",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, stable, err := resolveTreeMode(cmd, asJSON, ndjson, porcelain, pretty)
			if err != nil {
				return err
			}

			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			// Legacy: empty args → applyProjectRootToPath("", defaultToRoot=true);
			// one or more paths → applyProjectRootToPath(args[0], false). Extra path
			// args and --fields are accepted but ignored by legacy tree.
			scoped := sc.paths(args, true)
			path := ""
			if len(scoped) > 0 {
				path = scoped[0]
			}

			params := map[string]any{"path": path}
			if depth > 0 {
				params["maxDepth"] = depth
			}
			if includeArchived {
				params["includeArchived"] = true
			}
			if openOnly {
				params["openOnly"] = true
			}
			if mode == "human" {
				params["includeCampaignMembers"] = true
			}

			raw, err := tr.Call(cmd.Context(), "wrkq.task.treeView", params)
			if err != nil {
				if re, ok := err.(*Error); ok {
					return errors.New(re.Message)
				}
				return err
			}
			var view treeWireView
			if err := json.Unmarshal(raw, &view); err != nil {
				return err
			}
			if mode == "human" {
				if err := hydrateTreePriorities(cmd.Context(), tr, view.Children); err != nil {
					if re, ok := err.(*Error); ok {
						return errors.New(re.Message)
					}
					return err
				}
			}

			out := cmd.OutOrStdout()
			switch mode {
			case "porcelain":
				return renderTreePorcelain(out, &view)
			case "json":
				return renderTreeJSON(out, &view, stable)
			case "human":
				return renderTreeHuman(out, &view)
			default: // ndjson
				return renderTreeNDJSON(out, &view)
			}
		},
	}
	cmd.Flags().IntVarP(&depth, "level", "L", 0, "Maximum depth to display (0 = unlimited)")
	cmd.Flags().BoolVarP(&includeArchived, "all", "a", false, "Include completed/archived/deleted tasks and empty containers")
	cmd.Flags().BoolVar(&openOnly, "open", false, "Show only open tasks")
	cmd.Flags().StringVar(&fields, "fields", "", "Fields to display (comma-separated)")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&pretty, "pretty", false, "Force human-readable tree output even when not a TTY")
	return cmd
}

// resolveTreeMode reproduces legacy runTree's mode decision for tree's surface.
// Legacy tree is outputShapeList with DefaultTTY=human and NO DefaultNonTTY, so a
// non-TTY default resolves to NDJSON (defaultNonTTYOutputMode for list). --pretty
// forces the same human tree renderer in non-TTY mode.
func resolveTreeMode(cmd *cobra.Command, asJSON, ndjson, porcelain, pretty bool) (mode string, stable bool, err error) {
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
	if pretty {
		return "human", false, nil
	}
	// Legacy: bare --porcelain (no --json/--ndjson/--output) → the original
	// tab-separated tree porcelain format (legacyPorcelain), NOT the list NDJSON.
	outputChanged := false
	if outF := cmd.Flag("output"); outF != nil {
		outputChanged = outF.Changed
	}
	if porcelain && explicit == "" && !outputChanged {
		return "porcelain", true, nil
	}
	if explicit != "" {
		return explicit, false, nil
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
		case "raw":
			// raw is not in tree's allowed output set → legacy errors with this exact
			// message. Emit it verbatim for byte-parity (not a gate).
			return "", false, fmt.Errorf("output mode %q is not supported for this command", m)
		case "table", "human":
			return "human", false, nil
		case "yaml", "tsv":
			return "", false, fmt.Errorf("output mode %q is not supported for this command", m)
		default:
			return "", false, fmt.Errorf("invalid output mode %q: choose table, human, json, ndjson, porcelain, yaml, tsv, or raw", outF.Value.String())
		}
	}
	if isStdoutTTY(cmd.OutOrStdout()) {
		return "human", false, nil
	}
	return "ndjson", false, nil
}

// ── wire DTO (mirrors internal/wrkqapi.WrkqTreeView / WrkqTreeNode) ──

type treeWireNode struct {
	Type                 string          `json:"type"`
	ID                   string          `json:"id"`
	Slug                 string          `json:"slug"`
	Title                string          `json:"title"`
	State                string          `json:"state,omitempty"`
	UUID                 string          `json:"uuid"`
	RequestedByProjectID *string         `json:"requested_by_project_id,omitempty"`
	AssignedProjectID    *string         `json:"assigned_project_id,omitempty"`
	AcknowledgedAt       *string         `json:"acknowledged_at,omitempty"`
	Resolution           *string         `json:"resolution,omitempty"`
	IsArchived           bool            `json:"is_archived"`
	IsDeleted            bool            `json:"is_deleted"`
	AllTasksCompleted    bool            `json:"all_tasks_completed,omitempty"`
	Children             []*treeWireNode `json:"children,omitempty"`
	ExternalChildren     []*treeWireNode `json:"external_children,omitempty"`
	ExternalBacklink     bool            `json:"external_backlink,omitempty"`
	ExternalProjectID    string          `json:"external_project_id,omitempty"`
	ExternalPath         string          `json:"external_path,omitempty"`
	WireCreatedAt        string          `json:"wire_created_at,omitempty"`
	WireParentTaskUUID   string          `json:"wire_parent_task_uuid,omitempty"`
	Priority             int             `json:"-"`
}

type treeWireView struct {
	Path                         string          `json:"path"`
	ProjectID                    string          `json:"project_id,omitempty"`
	Children                     []*treeWireNode `json:"children"`
	HiddenContainersNotDisplayed int             `json:"hidden_containers_not_displayed"`
	WireRawPath                  string          `json:"wire_raw_path,omitempty"`
}

// ── JSON rendering (mirrors legacy treeOutput / treeNode exactly) ──

// treeJSONNode reproduces legacy treeNode's JSON projection byte-for-byte: same
// exported field order and json tags, with created_at/parentTaskUUID hidden (the
// wire-only carriers are dropped here).
type treeJSONNode struct {
	Type                 string          `json:"type"`
	ID                   string          `json:"id"`
	Slug                 string          `json:"slug"`
	Title                string          `json:"title"`
	State                string          `json:"state,omitempty"`
	UUID                 string          `json:"uuid"`
	RequestedByProjectID *string         `json:"requested_by_project_id,omitempty"`
	AssignedProjectID    *string         `json:"assigned_project_id,omitempty"`
	AcknowledgedAt       *string         `json:"acknowledged_at,omitempty"`
	Resolution           *string         `json:"resolution,omitempty"`
	IsArchived           bool            `json:"is_archived"`
	IsDeleted            bool            `json:"is_deleted"`
	AllTasksCompleted    bool            `json:"all_tasks_completed,omitempty"`
	Children             []*treeJSONNode `json:"children,omitempty"`
}

type treeJSONOutput struct {
	Path                         string          `json:"path"`
	ProjectID                    string          `json:"project_id,omitempty"`
	Children                     []*treeJSONNode `json:"children"`
	HiddenContainersNotDisplayed int             `json:"hidden_containers_not_displayed"`
}

func toTreeJSONNodes(nodes []*treeWireNode) []*treeJSONNode {
	if nodes == nil {
		return nil
	}
	out := make([]*treeJSONNode, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, &treeJSONNode{
			Type:                 n.Type,
			ID:                   n.ID,
			Slug:                 n.Slug,
			Title:                n.Title,
			State:                n.State,
			UUID:                 n.UUID,
			RequestedByProjectID: n.RequestedByProjectID,
			AssignedProjectID:    n.AssignedProjectID,
			AcknowledgedAt:       n.AcknowledgedAt,
			Resolution:           n.Resolution,
			IsArchived:           n.IsArchived,
			IsDeleted:            n.IsDeleted,
			AllTasksCompleted:    n.AllTasksCompleted,
			Children:             toTreeJSONNodes(n.Children),
		})
	}
	return out
}

func renderTreeJSON(w io.Writer, view *treeWireView, stable bool) error {
	out := treeJSONOutput{
		Path:                         view.Path,
		ProjectID:                    view.ProjectID,
		Children:                     toTreeJSONNodes(view.Children),
		HiddenContainersNotDisplayed: view.HiddenContainersNotDisplayed,
	}
	// Legacy writeJSONOutput: json.Encoder (HTML-escaping, trailing newline);
	// SetIndent for non-stable, compact for stable (porcelain via --output).
	enc := json.NewEncoder(w)
	if !stable {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(out)
}

// ── NDJSON rendering (mirrors legacy flattenTree + writeNDJSONOutput) ──

// treeStreamEntry reproduces legacy treeStreamEntry's JSON shape exactly.
type treeStreamEntry struct {
	Type                 string  `json:"type"`
	ID                   string  `json:"id"`
	Slug                 string  `json:"slug"`
	Title                string  `json:"title"`
	Path                 string  `json:"path"`
	Depth                int     `json:"depth"`
	ParentID             *string `json:"parent_id,omitempty"`
	ParentPath           *string `json:"parent_path,omitempty"`
	State                string  `json:"state,omitempty"`
	OpenedAt             *string `json:"opened_at,omitempty"`
	CreatedAt            string  `json:"created_at,omitempty"`
	UUID                 string  `json:"uuid"`
	RequestedByProjectID *string `json:"requested_by_project_id,omitempty"`
	AssignedProjectID    *string `json:"assigned_project_id,omitempty"`
	AcknowledgedAt       *string `json:"acknowledged_at,omitempty"`
	Resolution           *string `json:"resolution,omitempty"`
	IsArchived           bool    `json:"is_archived"`
	IsDeleted            bool    `json:"is_deleted"`
	AllTasksCompleted    bool    `json:"all_tasks_completed,omitempty"`
}

func flattenTreeWire(view *treeWireView) []treeStreamEntry {
	var entries []treeStreamEntry
	// Legacy passes the *raw* rootPath (empty for the root view) into flattenTree,
	// not the "." display path. With an empty rootPath the top level has no
	// parent_path; with "." legacy's joinTreePath strips it but the parentPath!=""
	// guard would wrongly emit parent_path=".". WireRawPath carries the raw value.
	rootPath := view.WireRawPath
	var walk func(nodes []*treeWireNode, parentID *string, parentPath string, depth int)
	walk = func(nodes []*treeWireNode, parentID *string, parentPath string, depth int) {
		for _, node := range nodes {
			path := joinTreePath(parentPath, node.Slug)
			entry := treeStreamEntry{
				Type:                 node.Type,
				ID:                   node.ID,
				Slug:                 node.Slug,
				Title:                node.Title,
				Path:                 path,
				Depth:                depth,
				State:                node.State,
				CreatedAt:            node.WireCreatedAt,
				UUID:                 node.UUID,
				RequestedByProjectID: node.RequestedByProjectID,
				AssignedProjectID:    node.AssignedProjectID,
				AcknowledgedAt:       node.AcknowledgedAt,
				Resolution:           node.Resolution,
				IsArchived:           node.IsArchived,
				IsDeleted:            node.IsDeleted,
				AllTasksCompleted:    node.AllTasksCompleted,
			}
			if parentID != nil {
				entry.ParentID = parentID
			}
			if parentPath != "" {
				parentPathCopy := parentPath
				entry.ParentPath = &parentPathCopy
			}
			if node.Type == "task" && node.State == "open" && node.WireCreatedAt != "" {
				openedAt := node.WireCreatedAt
				entry.OpenedAt = &openedAt
			}
			entries = append(entries, entry)

			nodeID := node.ID
			walk(node.Children, &nodeID, path, depth+1)
		}
	}
	walk(view.Children, nil, rootPath, 0)
	return entries
}

func joinTreePath(parentPath, slug string) string {
	if parentPath == "" || parentPath == "." {
		return slug
	}
	return parentPath + "/" + slug
}

func renderTreeNDJSON(w io.Writer, view *treeWireView) error {
	entries := flattenTreeWire(view)
	enc := json.NewEncoder(w)
	for i := range entries {
		if err := enc.Encode(entries[i]); err != nil {
			return err
		}
	}
	return nil
}

// ── porcelain rendering (mirrors legacy printTreeOutput/printTree porcelain) ──

func renderTreePorcelain(w io.Writer, view *treeWireView) error {
	header := view.Path
	// Legacy: rootPath "" → ".". TreeView already normalized Path to "." for the
	// root view, so header is the display path directly.
	fmt.Fprintln(w, header)
	printTreePorcelain(w, view.Children, "")
	return nil
}

func printTreePorcelain(w io.Writer, nodes []*treeWireNode, prefix string) {
	for _, child := range nodes {
		fmt.Fprintf(w, "%s%s\t%s\t%s\t%s\n", prefix, child.Type, child.ID, child.Slug, child.Title)
		if len(child.Children) > 0 {
			printTreePorcelain(w, child.Children, prefix+"  ")
		}
	}
}

func renderTreeHuman(w io.Writer, view *treeWireView) error {
	header := view.Path
	if header == "" {
		header = "."
	}
	if view.ProjectID != "" {
		header += " " + style.Paint(style.ColDim, fmt.Sprintf("[%s]", view.ProjectID))
	}
	fmt.Fprintln(w, header)
	printTreeHuman(w, view.Children, "")
	if view.HiddenContainersNotDisplayed > 0 {
		fmt.Fprintln(w, style.Paint(style.ColDim, fmt.Sprintf("(plus %d empty containers not displayed; use --all to show empty containers)", view.HiddenContainersNotDisplayed)))
	}
	return nil
}

// hydrateTreePriorities enriches the human-only tree projection through the
// existing task read surface. Priority intentionally remains absent from the
// compatibility tree wire DTO and all machine tree formats.
func hydrateTreePriorities(ctx context.Context, tr Transport, nodes []*treeWireNode) error {
	priorities := make(map[string]int)
	var walk func([]*treeWireNode) error
	walk = func(children []*treeWireNode) error {
		for _, child := range children {
			if child.Type == "task" {
				priority, ok := priorities[child.UUID]
				if !ok {
					raw, err := tr.Call(ctx, "wrkq.task.show", map[string]string{"task": child.UUID})
					if err != nil {
						return err
					}
					var task struct {
						Priority int `json:"priority"`
					}
					if err := json.Unmarshal(raw, &task); err != nil {
						return err
					}
					priority = task.Priority
					priorities[child.UUID] = priority
				}
				child.Priority = priority
			}
			if err := walk(child.Children); err != nil {
				return err
			}
			if err := walk(child.ExternalChildren); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(nodes)
}

func printTreeHuman(w io.Writer, nodes []*treeWireNode, prefix string) {
	for i, child := range nodes {
		isLastChild := i == len(nodes)-1
		connector := "├── "
		if isLastChild {
			connector = "└── "
		}
		fmt.Fprintf(w, "%s%s%s\n", prefix, style.Paint(style.ColDim, connector), formatTreeHumanNode(child))
		if len(child.Children) > 0 {
			newPrefix := prefix + style.Paint(style.ColDim, "│") + "   "
			if isLastChild {
				newPrefix = prefix + "    "
			}
			printTreeHuman(w, child.Children, newPrefix)
		}
		if len(child.ExternalChildren) > 0 {
			newPrefix := prefix + style.Paint(style.ColDim, "│") + "   "
			if isLastChild {
				newPrefix = prefix + "    "
			}
			printTreeHuman(w, child.ExternalChildren, newPrefix)
		}
	}
}

func formatTreeHumanNode(node *treeWireNode) string {
	var parts []string
	if node.Type == "task" {
		parts = append(parts, style.Paint(style.ColDim, node.ID))
		if strings.HasPrefix(node.ExternalPath, "campaign:") {
			parts = append(parts, node.Slug)
		}
		if node.Title != "" && node.Title != node.Slug {
			parts = append(parts, node.Title)
		}
		if node.Priority > 0 {
			parts = append(parts, style.Paint(style.PriorityColor(node.Priority), fmt.Sprintf("P%d", node.Priority)))
		}
		if node.State != "" {
			parts = append(parts, style.Paint(style.StateColor(node.State), fmt.Sprintf("<%s>", formatTreeHumanTaskState(node))))
		}
	} else {
		displayTitle := node.Title
		if node.Slug == "inbox" && strings.EqualFold(node.Title, "inbox") {
			displayTitle = "Inbox"
		}
		parts = append(parts, style.Paint(style.ColDir, node.Slug+"/"))
		if displayTitle != node.Slug {
			parts = append(parts, style.Paint(style.ColDim, fmt.Sprintf("(%s)", displayTitle)))
		}
		parts = append(parts, style.Paint(style.ColDim, fmt.Sprintf("[%s]", node.ID)))
		if node.AllTasksCompleted {
			parts = append(parts, style.Paint(style.ColDone, "(All done)"))
		}
	}
	if node.IsArchived {
		parts = append(parts, style.Paint(style.ColDim, "(archived)"))
	}
	if node.ExternalBacklink {
		context := strings.TrimSpace(node.ExternalPath)
		if context == "" {
			context = node.ExternalProjectID
		} else if node.ExternalProjectID != "" {
			context += " " + node.ExternalProjectID
		}
		if context != "" {
			parts = append(parts, style.Paint(style.ColDim, fmt.Sprintf("(external: %s)", context)))
		} else {
			parts = append(parts, style.Paint(style.ColDim, "(external)"))
		}
	}
	if project := strings.TrimPrefix(node.ExternalPath, "campaign:"); project != node.ExternalPath && project != "" {
		parts = append(parts, style.Paint(style.ColDim, "↗ "+project))
	}
	return strings.Join(parts, " ")
}

func formatTreeHumanTaskState(node *treeWireNode) string {
	if node.State != "open" {
		return node.State
	}
	openedAge := style.FormatOpenedAge(node.WireCreatedAt)
	if openedAge == "" {
		return node.State
	}
	return "opened " + openedAge + " ago"
}
