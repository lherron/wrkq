package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// newTreeCmd mirrors `wrkq tree` via wrkq.task.treeView (the server-owned
// compatibility tree projection that reproduces legacy recursive traversal:
// container pruning, "all done" rollups, subtask nesting, hidden-container
// counting). The SERVER owns the entire hierarchy walk; the CLI owns ONLY byte
// rendering (json / ndjson / porcelain).
//
// Implemented surface (byte-proven against legacy, non-TTY): the bare non-TTY
// default (NDJSON), --json, --ndjson, --porcelain, -L/--level, -a/--all, --open,
// single path, project-root scoping. The interactive PRETTY (human) renderer is
// HARD-GATED: it only fires on a real TTY (legacy gates the non-TTY default to
// NDJSON for outputShapeList), and it embeds wall-clock-relative "opened N ago"
// strings + ANSI color that cannot be byte-reproduced in a hermetic harness.
// Multi-path and --fields are likewise hard-gated pending parity.
func newTreeCmd() *cobra.Command {
	var asJSON, ndjson, porcelain, includeArchived, openOnly bool
	var depth int
	var fields string
	cmd := &cobra.Command{
		Use:   "tree [path...]",
		Short: "Display containers and tasks in a tree structure",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return fmt.Errorf("tree: multi-path not yet implemented in wrkq-rpccli (hard-gated pending parity)")
			}
			if fields != "" {
				return fmt.Errorf("tree: --fields not yet implemented in wrkq-rpccli (hard-gated pending parity)")
			}
			mode, stable, err := resolveTreeMode(cmd, asJSON, ndjson, porcelain)
			if err != nil {
				return err
			}

			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			// Legacy: empty args → applyProjectRootToPath("", defaultToRoot=true);
			// single path → applyProjectRootToPath(arg, false). The scoper reproduces
			// both via paths(args, true): no args defaults to the configured root,
			// one path is root-prefixed.
			scoped := sc.paths(args, true)
			path := ""
			if len(scoped) == 1 {
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

			out := cmd.OutOrStdout()
			switch mode {
			case "porcelain":
				return renderTreePorcelain(out, &view)
			case "json":
				return renderTreeJSON(out, &view, stable)
			default: // ndjson
				return renderTreeNDJSON(out, &view)
			}
		},
	}
	cmd.Flags().IntVarP(&depth, "level", "L", 0, "Maximum depth to display (0 = unlimited)")
	cmd.Flags().BoolVarP(&includeArchived, "all", "a", false, "Include archived and empty containers")
	cmd.Flags().BoolVar(&openOnly, "open", false, "Show only open tasks")
	cmd.Flags().StringVar(&fields, "fields", "", "Fields to display (comma-separated) (hard-gated: not yet implemented)")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	return cmd
}

// resolveTreeMode reproduces legacy runTree's mode decision for tree's surface.
// Legacy tree is outputShapeList with DefaultTTY=human and NO DefaultNonTTY, so a
// non-TTY default resolves to NDJSON (defaultNonTTYOutputMode for list). The TTY
// pretty/human renderer is hard-gated (non-deterministic, TTY-only).
func resolveTreeMode(cmd *cobra.Command, asJSON, ndjson, porcelain bool) (mode string, stable bool, err error) {
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
		case "table", "human", "yaml", "tsv":
			// Legacy RENDERS these; the mirror cannot, so hard-gate (no silent
			// degradation). Not a parity case.
			return "", false, fmt.Errorf("tree: %s output not yet implemented in wrkq-rpccli (hard-gated pending parity)", m)
		default:
			return "", false, fmt.Errorf("invalid output mode %q: choose table, human, json, ndjson, porcelain, yaml, tsv, or raw", outF.Value.String())
		}
	}
	if isStdoutTTY(cmd.OutOrStdout()) {
		// Legacy non-porcelain TTY default is the pretty human tree (DefaultTTY).
		return "", false, fmt.Errorf("tree: pretty/human output not yet implemented in wrkq-rpccli (use --json / --ndjson / --porcelain)")
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
	WireCreatedAt        string          `json:"wire_created_at,omitempty"`
	WireParentTaskUUID   string          `json:"wire_parent_task_uuid,omitempty"`
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
