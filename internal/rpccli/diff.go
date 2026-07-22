package rpccli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// newDiffCmd mirrors `wrkq diff`. It compares two tasks (or, with a single
// argument, returns the legacy "not yet implemented" error) field-by-field. It
// is RPC-backed: each task is read through the server-owned wrkq.task.catView
// compatibility projection, and the field comparison + JSON/human rendering is
// CLI-local (the diff DTO is a pure presentation projection, not durable state).
// Output and error wording match legacy `wrkq diff` byte-for-byte (proven by
// TestParity). Default output is JSON for non-TTY stdout (the parity-proven
// mode); the human-readable colorized form mirrors legacy for TTY stdout.
func newDiffCmd() *cobra.Command {
	var diffUnified int
	var diffJSON bool
	cmd := &cobra.Command{
		Use:   "diff <A> [B]",
		Short: "Compare two tasks or task versions",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(cmd, args, diffJSON)
		},
	}
	cmd.Flags().IntVar(&diffUnified, "unified", 3, "Lines of unified context")
	cmd.Flags().BoolVar(&diffJSON, "json", false, "Output as JSON")
	return cmd
}

// diffTaskData holds the legacy-comparable fields projected from wrkq.task.catView.
// Mirrors internal/rpccli/diff.go's taskData (the subset diff actually compares plus
// the id/slug used in human rendering). priority and etag keep their numeric Go
// types so the JSON encoding of the diff "old"/"new" values is byte-identical to
// legacy (int for priority, int64 for etag).
type diffTaskData struct {
	ID            string  `json:"id"`
	Slug          string  `json:"slug"`
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	Specification string  `json:"specification"`
	State         string  `json:"state"`
	Priority      int     `json:"priority"`
	DueAt         *string `json:"due_at,omitempty"`
	Etag          int64   `json:"etag"`
}

type diffResult struct {
	FieldsChanged []string                   `json:"fields_changed"`
	Changes       map[string]diffFieldChange `json:"changes"`
}

type diffFieldChange struct {
	Field string `json:"field"`
	Old   any    `json:"old"`
	New   any    `json:"new"`
}

func runDiff(cmd *cobra.Command, args []string, diffJSON bool) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()

	// Resolve + fetch task A. Legacy: applyProjectRootToSelector(arg, false) then
	// ResolveTask + fetchTaskData; the wrapping prefixes are reproduced here.
	taskA, err := fetchDiffTask(cmd.Context(), tr, sc.selector(args[0], false), "A")
	if err != nil {
		return err
	}

	// One argument: legacy returns the pinned "not yet implemented" error AFTER
	// successfully fetching A.
	if len(args) == 1 {
		return fmt.Errorf("comparing with working copy not yet implemented")
	}

	taskB, err := fetchDiffTask(cmd.Context(), tr, sc.selector(args[1], false), "B")
	if err != nil {
		return err
	}

	diff := compareDiffTasks(taskA, taskB)

	if diffJSON || !isStdoutTTY(cmd.OutOrStdout()) {
		return renderDiffJSON(cmd.OutOrStdout(), diff)
	}
	return renderDiffHuman(cmd.OutOrStdout(), diff, taskA, taskB)
}

// fetchDiffTask reads one task's diff-comparable fields over wrkq.task.catView.
// Legacy diff first resolves the selector ("failed to resolve task <label>: ...")
// then fetches the row ("failed to fetch task <label>: ..."). catView fuses both
// steps, surfacing NOT_FOUND for an unresolvable/missing task; the mirror restores
// the legacy "failed to resolve task <label>: task not found: <ref>" wording so a
// missing ref is byte-identical. Comments are excluded (diff ignores them).
func fetchDiffTask(ctx context.Context, tr Transport, ref, label string) (*diffTaskData, error) {
	includeComments := false
	raw, err := tr.Call(ctx, "wrkq.task.catView",
		map[string]any{"task": ref, "includeComments": &includeComments})
	if err != nil {
		if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
			return nil, fmt.Errorf("failed to resolve task %s: task not found: %s", label, ref)
		}
		return nil, err
	}
	var td diffTaskData
	if err := json.Unmarshal(raw, &td); err != nil {
		return nil, err
	}
	return &td, nil
}

// compareDiffTasks mirrors internal/rpccli/diff.go compareTasksDetailed exactly:
// field order slug, title, description, specification, state, priority, due_at,
// etag, with nullable due_at coalesced to "".
func compareDiffTasks(a, b *diffTaskData) *diffResult {
	diff := &diffResult{
		FieldsChanged: []string{},
		Changes:       make(map[string]diffFieldChange),
	}

	record := func(field string, old, new any) {
		diff.FieldsChanged = append(diff.FieldsChanged, field)
		diff.Changes[field] = diffFieldChange{Field: field, Old: old, New: new}
	}

	if a.Slug != b.Slug {
		record("slug", a.Slug, b.Slug)
	}
	if a.Title != b.Title {
		record("title", a.Title, b.Title)
	}
	if a.Description != b.Description {
		record("description", a.Description, b.Description)
	}
	if a.Specification != b.Specification {
		record("specification", a.Specification, b.Specification)
	}
	if a.State != b.State {
		record("state", a.State, b.State)
	}
	if a.Priority != b.Priority {
		record("priority", a.Priority, b.Priority)
	}
	dueA := ""
	if a.DueAt != nil {
		dueA = *a.DueAt
	}
	dueB := ""
	if b.DueAt != nil {
		dueB = *b.DueAt
	}
	if dueA != dueB {
		record("due_at", dueA, dueB)
	}
	if a.Etag != b.Etag {
		record("etag", a.Etag, b.Etag)
	}

	return diff
}

func renderDiffJSON(w io.Writer, diff *diffResult) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(diff)
}

func renderDiffHuman(w io.Writer, diff *diffResult, a, b *diffTaskData) error {
	if len(diff.FieldsChanged) == 0 {
		fmt.Fprintf(w, "No differences between %s and %s\n", a.ID, b.ID)
		return nil
	}

	fmt.Fprintf(w, "Comparing %s (%s) vs %s (%s)\n\n", a.ID, a.Slug, b.ID, b.Slug)
	fmt.Fprintf(w, "%d field(s) changed:\n\n", len(diff.FieldsChanged))

	for _, field := range diff.FieldsChanged {
		change := diff.Changes[field]
		fmt.Fprintf(w, "\033[33m%s:\033[0m\n", field)
		fmt.Fprintf(w, "  \033[31m- %v\033[0m\n", change.Old)
		fmt.Fprintf(w, "  \033[32m+ %v\033[0m\n\n", change.New)
	}

	return nil
}
