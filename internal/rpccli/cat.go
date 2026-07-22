package rpccli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/style"
	"github.com/spf13/cobra"
)

// newCatCmd mirrors `wrkq cat`. RPC-backed via the server-owned wrkq.task.catView
// compatibility projection (one consistent snapshot per task). The CLI re-renders
// that projection into legacy `cat` output for every exposed render mode:
//
//	json (non-TTY default + --json, indented; +--porcelain → compact),
//	ndjson (--ndjson, one compact object per line),
//	raw (--output raw / --porcelain / TTY default markdown front-matter).
//
// The json/ndjson/raw modes are byte-parity proven against legacy. The styled
// task card (style.RenderStyledTask) renders on an interactive TTY — matching
// legacy — or whenever --pretty forces it; color follows style.ColorEnabled, so
// a non-TTY --pretty card is plain text and byte-comparable to legacy's.
func newCatCmd() *cobra.Command {
	var noFrontmatter, excludeComments, asJSON, ndjson, porcelain, pretty bool
	cmd := &cobra.Command{
		Use:     "cat <path|id>...",
		Aliases: []string{"show"},
		Short:   "Print one or more tasks",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCat(cmd, args, noFrontmatter, excludeComments, asJSON, ndjson, porcelain, pretty)
		},
	}
	cmd.Flags().BoolVar(&noFrontmatter, "no-frontmatter", false, "Print body only without front matter")
	cmd.Flags().BoolVar(&excludeComments, "exclude-comments", false, "Exclude comments from output")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	cmd.Flags().BoolVar(&pretty, "pretty", false, "Force the styled task card even when not a TTY")
	return cmd
}

func runCat(cmd *cobra.Command, args []string, noFrontmatter, excludeComments, asJSON, ndjson, porcelain, pretty bool) error {
	mode, stable, err := resolveCatMode(cmd, asJSON, ndjson, porcelain)
	if err != nil {
		return err
	}

	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()

	includeComments := !excludeComments
	objs := make([]json.RawMessage, 0, len(args))
	for _, ref := range args {
		// Legacy: applyProjectRootToSelector(arg, false) before resolving the task.
		sref := sc.selector(ref, false)
		raw, err := tr.Call(cmd.Context(), "wrkq.task.catView",
			map[string]any{"task": sref, "includeComments": includeComments})
		if err != nil {
			if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
				return fmt.Errorf("task not found: %s", sref)
			}
			return err
		}
		objs = append(objs, raw)
	}

	out := cmd.OutOrStdout()
	// --pretty forces the styled card (overriding an explicit machine mode and the
	// non-TTY JSON default); on a TTY it is the default, matching legacy.
	if pretty || (mode == "raw" && !stable && style.ColorEnabled) {
		return writeCatStyled(out, objs, noFrontmatter, excludeComments)
	}
	switch mode {
	case "json":
		return writeCatJSON(out, objs, stable)
	case "ndjson":
		return writeCatNDJSON(out, objs)
	default: // "raw"
		return writeCatRaw(out, objs, noFrontmatter, excludeComments)
	}
}

// writeCatStyled renders each task projection as the shared styled card. It maps
// the mirror's catTask into style.StyledTask — the exact same renderer legacy
// calls — so output is byte-identical by construction.
func writeCatStyled(w io.Writer, objs []json.RawMessage, noFrontmatter, excludeComments bool) error {
	for i, obj := range objs {
		var t catTask
		if err := json.Unmarshal(obj, &t); err != nil {
			return err
		}
		if i > 0 {
			fmt.Fprintln(w)
		}
		var comments []style.StyledComment
		if !excludeComments {
			for _, c := range t.Comments {
				comments = append(comments, style.StyledComment{
					ID:        c.ID,
					CreatedAt: c.CreatedAt,
					Actor:     attribution.PrincipalHandle(c.PrincipalRef),
					Body:      c.Body,
				})
			}
		}
		style.RenderStyledTask(w, style.StyledTask{
			ID:            t.ID,
			Path:          t.Path,
			Title:         t.Title,
			State:         t.State,
			Priority:      t.Priority,
			Assignee:      t.AssigneeSlug,
			Labels:        t.Labels,
			DueAt:         t.DueAt,
			UpdatedAt:     t.UpdatedAt,
			BlockedCount:  len(t.BlockedBy),
			Description:   t.Description,
			Specification: t.Specification,
			NoFrontmatter: noFrontmatter,
		}, comments)
	}
	return nil
}

// resolveCatMode reproduces legacy cat's resolveOutputMode decision. Cat's allowed
// modes are {raw, json, ndjson}; DefaultTTY=raw, DefaultNonTTY=json. The explicit
// bool flags --json/--ndjson select directly; --porcelain alone resolves to the
// canonical machine mode for content shape, which is raw+stable. The global
// --output flag is honored (porcelain→raw+stable; table/human/yaml/tsv are not in
// cat's allowed set → legacy's verbatim "not supported" error, a byte-parity case,
// not a gate).
func resolveCatMode(cmd *cobra.Command, asJSON, ndjson, porcelain bool) (mode string, stable bool, err error) {
	count := 0
	explicit := ""
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
	if stable { // bare --porcelain → canonicalMachineOutputMode(content) = raw, stable
		return "raw", true, nil
	}
	if outF := cmd.Flag("output"); outF != nil && outF.Changed {
		m := strings.ToLower(strings.TrimSpace(outF.Value.String()))
		switch m {
		case "json":
			return "json", false, nil
		case "ndjson":
			return "ndjson", false, nil
		case "raw":
			return "raw", false, nil
		case "porcelain":
			return "raw", true, nil
		case "table", "human", "yaml", "tsv":
			// Legacy: requireAllowedOutput rejects these for cat with this exact
			// message. Emit verbatim → byte-parity (not a gate).
			return "", false, fmt.Errorf("output mode %q is not supported for this command", m)
		default:
			return "", false, fmt.Errorf("invalid output mode %q: choose table, human, json, ndjson, porcelain, yaml, tsv, or raw", outF.Value.String())
		}
	}
	if isStdoutTTY(cmd.OutOrStdout()) {
		return "raw", false, nil
	}
	return "json", false, nil
}

// writeCatJSON renders the per-task projections as one JSON array. Legacy uses a
// json.Encoder (HTML-escaping on, trailing newline); indented unless Stable
// (porcelain), in which case compact. The server already produced each element
// HTML-escaped in legacy field order, so array (re)marshaling preserves bytes.
func writeCatJSON(w io.Writer, objs []json.RawMessage, stable bool) error {
	var data []byte
	var err error
	if stable {
		data, err = json.Marshal(objs)
	} else {
		data, err = json.MarshalIndent(objs, "", "  ")
	}
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// writeCatNDJSON renders one compact JSON object per line. Legacy uses
// json.Encoder.Encode per Task (compact, HTML-escaped, newline-terminated). Each
// catView RawMessage is already compact and in legacy field order; json.Compact
// preserves its bytes (including HTML escaping).
func writeCatNDJSON(w io.Writer, objs []json.RawMessage) error {
	for _, obj := range objs {
		var buf bytes.Buffer
		if err := json.Compact(&buf, obj); err != nil {
			return err
		}
		buf.WriteByte('\n')
		if _, err := w.Write(buf.Bytes()); err != nil {
			return err
		}
	}
	return nil
}

// catTask is the mirror-side projection of one wrkq.task.catView object, used to
// re-render legacy raw markdown. Field tags match the server compat DTO so the
// RawMessage decodes losslessly. The mirror MUST NOT import wrkqapi (import
// guard), so this struct is duplicated here.
type catTask struct {
	ID                    string          `json:"id"`
	UUID                  string          `json:"uuid"`
	Path                  string          `json:"path"`
	ArtifactDir           string          `json:"artifact_dir"`
	ProjectID             string          `json:"project_id"`
	ProjectUUID           string          `json:"project_uuid"`
	RequestedByProjectID  *string         `json:"requested_by_project_id,omitempty"`
	AssignedProjectID     *string         `json:"assigned_project_id,omitempty"`
	Slug                  string          `json:"slug"`
	Title                 string          `json:"title"`
	State                 string          `json:"state"`
	Priority              int             `json:"priority"`
	Kind                  string          `json:"kind"`
	ParentTaskID          *string         `json:"parent_task_id,omitempty"`
	ParentTaskUUID        *string         `json:"parent_task_uuid,omitempty"`
	AssigneeSlug          *string         `json:"assignee,omitempty"`
	AssigneePrincipalRef  *string         `json:"assignee_principal_ref,omitempty"`
	ClaimedBy             *string         `json:"claimed_by,omitempty"`
	ClaimedScope          *string         `json:"claimed_scope,omitempty"`
	ClaimedNode           *string         `json:"claimed_node,omitempty"`
	ClaimedAt             *string         `json:"claimed_at,omitempty"`
	ClaimGeneration       int64           `json:"claim_generation,omitempty"`
	StartAt               *string         `json:"start_at,omitempty"`
	DueAt                 *string         `json:"due_at,omitempty"`
	Labels                *string         `json:"labels,omitempty"`
	Meta                  json.RawMessage `json:"meta"`
	Description           string          `json:"description"`
	Specification         string          `json:"specification"`
	AcknowledgedAt        *string         `json:"acknowledged_at,omitempty"`
	Resolution            *string         `json:"resolution,omitempty"`
	Etag                  int64           `json:"etag"`
	CreatedAt             string          `json:"created_at"`
	UpdatedAt             string          `json:"updated_at"`
	CompletedAt           *string         `json:"completed_at,omitempty"`
	ArchivedAt            *string         `json:"archived_at,omitempty"`
	CreatedBy             string          `json:"created_by"`
	CreatedByPrincipalRef string          `json:"created_by_principal_ref,omitempty"`
	CreatedByScopeRef     *string         `json:"created_by_scope_ref,omitempty"`
	UpdatedBy             string          `json:"updated_by"`
	UpdatedByPrincipalRef string          `json:"updated_by_principal_ref,omitempty"`
	CausedBy              []string        `json:"caused_by"`
	BlockedBy             []catBlocker    `json:"blocked_by,omitempty"`
	Comments              []catComment    `json:"comments,omitempty"`
}

type catComment struct {
	ID           string `json:"id"`
	CreatedAt    string `json:"created_at"`
	Body         string `json:"body"`
	PrincipalRef string `json:"principal_ref,omitempty"`
}

type catBlocker struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

// writeCatRaw reproduces legacy cat's byte-stable markdown rendering (front matter
// + description + comments) for each task, mirroring internal/rpccli/cat.go lines
// 461-595. The interactive styled view is intentionally omitted (TTY-only, never
// byte-tested).
func writeCatRaw(w io.Writer, objs []json.RawMessage, noFrontmatter, excludeComments bool) error {
	for i, obj := range objs {
		var t catTask
		if err := json.Unmarshal(obj, &t); err != nil {
			return err
		}
		if i > 0 {
			fmt.Fprintln(w)
		}

		if !noFrontmatter {
			metaValue := "{}"
			if len(t.Meta) > 0 {
				metaValue = string(t.Meta)
			}
			fmt.Fprintln(w, "---")
			fmt.Fprintf(w, "id: %s\n", t.ID)
			fmt.Fprintf(w, "uuid: %s\n", t.UUID)
			fmt.Fprintf(w, "path: %s\n", t.Path)
			fmt.Fprintf(w, "artifact_dir: %s\n", t.ArtifactDir)
			fmt.Fprintf(w, "project_id: %s\n", t.ProjectID)
			fmt.Fprintf(w, "project_uuid: %s\n", t.ProjectUUID)
			if t.RequestedByProjectID != nil {
				fmt.Fprintf(w, "requested_by_project_id: %s\n", *t.RequestedByProjectID)
			}
			if t.AssignedProjectID != nil {
				fmt.Fprintf(w, "assigned_project_id: %s\n", *t.AssignedProjectID)
			}
			fmt.Fprintf(w, "slug: %s\n", t.Slug)
			fmt.Fprintf(w, "title: %s\n", t.Title)
			fmt.Fprintf(w, "state: %s\n", t.State)
			fmt.Fprintf(w, "priority: %d\n", t.Priority)
			fmt.Fprintf(w, "kind: %s\n", t.Kind)
			if t.ParentTaskID != nil {
				fmt.Fprintf(w, "parent_task_id: %s\n", *t.ParentTaskID)
			}
			if t.ParentTaskUUID != nil {
				fmt.Fprintf(w, "parent_task_uuid: %s\n", *t.ParentTaskUUID)
			}
			if t.AssigneeSlug != nil {
				fmt.Fprintf(w, "assignee: %s\n", *t.AssigneeSlug)
			}
			if t.AssigneePrincipalRef != nil {
				fmt.Fprintf(w, "assignee_principal_ref: %s\n", *t.AssigneePrincipalRef)
			}
			if t.ClaimedBy != nil {
				fmt.Fprintf(w, "claimed_by: %s\n", *t.ClaimedBy)
				if t.ClaimedScope != nil {
					fmt.Fprintf(w, "claimed_scope: %s\n", *t.ClaimedScope)
				}
				if t.ClaimedNode != nil {
					fmt.Fprintf(w, "claimed_node: %s\n", *t.ClaimedNode)
				}
				if t.ClaimedAt != nil {
					fmt.Fprintf(w, "claimed_at: %s\n", *t.ClaimedAt)
				}
				fmt.Fprintf(w, "claim_generation: %d\n", t.ClaimGeneration)
			}
			if t.StartAt != nil {
				fmt.Fprintf(w, "start_at: %s\n", *t.StartAt)
			}
			if t.DueAt != nil {
				fmt.Fprintf(w, "due_at: %s\n", *t.DueAt)
			}
			if t.Labels != nil && *t.Labels != "" {
				fmt.Fprintf(w, "labels: %s\n", *t.Labels)
			}
			if len(t.CausedBy) > 0 {
				fmt.Fprintf(w, "caused_by: [%s]\n", strings.Join(t.CausedBy, ", "))
			}
			fmt.Fprintf(w, "meta: %s\n", metaValue)
			if t.Specification != "" {
				fmt.Fprintln(w, "specification: |")
				for _, line := range strings.Split(t.Specification, "\n") {
					fmt.Fprintf(w, "  %s\n", line)
				}
			}
			if t.AcknowledgedAt != nil {
				fmt.Fprintf(w, "acknowledged_at: %s\n", *t.AcknowledgedAt)
			}
			if t.Resolution != nil {
				fmt.Fprintf(w, "resolution: %s\n", *t.Resolution)
			}
			if len(t.BlockedBy) > 0 {
				parts := make([]string, len(t.BlockedBy))
				for i, b := range t.BlockedBy {
					parts[i] = fmt.Sprintf("%s (%s)", b.ID, b.State)
				}
				fmt.Fprintf(w, "blocked_by: [%s]\n", strings.Join(parts, ", "))
			}
			fmt.Fprintf(w, "etag: %d\n", t.Etag)
			fmt.Fprintf(w, "created_at: %s\n", t.CreatedAt)
			fmt.Fprintf(w, "updated_at: %s\n", t.UpdatedAt)
			if t.CompletedAt != nil {
				fmt.Fprintf(w, "completed_at: %s\n", *t.CompletedAt)
			}
			if t.ArchivedAt != nil {
				fmt.Fprintf(w, "archived_at: %s\n", *t.ArchivedAt)
			}
			fmt.Fprintf(w, "created_by: %s\n", t.CreatedBy)
			if t.CreatedByPrincipalRef != "" {
				fmt.Fprintf(w, "created_by_principal_ref: %s\n", t.CreatedByPrincipalRef)
			}
			if t.CreatedByScopeRef != nil {
				fmt.Fprintf(w, "created_by_scope_ref: %s\n", *t.CreatedByScopeRef)
			}
			fmt.Fprintf(w, "updated_by: %s\n", t.UpdatedBy)
			if t.UpdatedByPrincipalRef != "" {
				fmt.Fprintf(w, "updated_by_principal_ref: %s\n", t.UpdatedByPrincipalRef)
			}
			fmt.Fprintln(w, "---")
			fmt.Fprintln(w)
		}

		fmt.Fprintln(w, t.Description)

		if !excludeComments && len(t.Comments) > 0 {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "---")
			fmt.Fprintln(w)
			fmt.Fprintln(w, "<!-- wrkq-comments: do not edit below -->")
			fmt.Fprintln(w)
			for _, c := range t.Comments {
				fmt.Fprintf(w, "> [%s] [%s] %s\n", c.ID, c.CreatedAt, attribution.PrincipalHandle(c.PrincipalRef))
				for _, line := range strings.Split(c.Body, "\n") {
					fmt.Fprintf(w, "> %s\n", line)
				}
				fmt.Fprintln(w)
			}
		}
	}
	return nil
}
