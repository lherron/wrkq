package rpccli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/lherron/wrkq/internal/bulk"
	"github.com/spf13/cobra"
)

// newRmCmd mirrors `wrkq rm` on the caller-owned-confirmation seam
// (architecture/records/invariants/wrkq.mutation.caller-owned-confirmation.yaml).
// The durable mutation runs through wrkq.task.delete with an EXPLICIT mode
// (archive for the legacy default, purge for --purge); the mirror owns the
// project-root scoping, the legacy purge prompt + abort + --yes, dry-run
// rendering, output-mode selection, and the partial-failure exit-code taxonomy.
//
// Task targets use wrkq.task.delete with an explicit mode. Container targets use
// wrkq.container.archive for the legacy default soft-delete and wrkq.container.delete
// for --purge (empty-only hard delete, as legacy rm documents; recursive deletion
// remains the rmdir --force surface).
func newRmCmd() *cobra.Command {
	var (
		recursive       bool
		force           bool
		yes             bool
		dryRun          bool
		purge           bool
		nullglob        bool
		jobs            int
		continueOnError bool
		asJSON          bool
		ndjson          bool
		porcelain       bool
	)
	cmd := &cobra.Command{
		Use:   "rm <path|id>...",
		Short: "Archive or delete tasks and containers",
		Long: `Archives (soft delete) or permanently deletes tasks and containers.

By default, performs soft delete (sets archived_at). Use --purge for hard delete.
Container purge only removes empty containers; use rmdir --force for recursive deletion.

WARNING: --purge permanently deletes tasks and attachments. This CANNOT be undone!`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRm(cmd, args, rmFlags{
				recursive: recursive, force: force, yes: yes, dryRun: dryRun,
				purge: purge, nullglob: nullglob, jobs: jobs,
				continueOnError: continueOnError, asJSON: asJSON, ndjson: ndjson, porcelain: porcelain,
			})
		},
	}
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Remove containers recursively")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Force removal")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompts")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be removed")
	cmd.Flags().BoolVar(&purge, "purge", false, "Permanently delete (CANNOT BE UNDONE)")
	cmd.Flags().BoolVar(&nullglob, "nullglob", false, "Zero matches is not an error")
	cmd.Flags().IntVarP(&jobs, "jobs", "j", 1, "Parallel workers")
	cmd.Flags().BoolVar(&continueOnError, "continue-on-error", false, "Continue on errors")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output NDJSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	return cmd
}

type rmFlags struct {
	recursive       bool
	force           bool
	yes             bool
	dryRun          bool
	purge           bool
	nullglob        bool
	jobs            int
	continueOnError bool
	asJSON          bool
	ndjson          bool
	porcelain       bool
}

// rmResult mirrors internal/cli rmResult byte-for-byte (JSON tags + omitempty).
type rmResult struct {
	Type               string `json:"type"`
	ID                 string `json:"id"`
	UUID               string `json:"uuid"`
	Slug               string `json:"slug"`
	Path               string `json:"path"`
	Purged             bool   `json:"purged"`
	AttachmentsDeleted int    `json:"attachments_deleted,omitempty"`
	BytesFreed         int64  `json:"bytes_freed,omitempty"`
}

// rmTarget is a resolved removal target.
type rmTarget struct {
	Type  string // "task" | "container" | "promise"
	UUID  string
	ID    string
	Slug  string
	Title string
	State string
	Prio  int
	Kind  string
}

func (t rmTarget) key() string { return t.Type + ":" + t.UUID }

func sortRmResults(results []rmResult, targetKeys []string) {
	targetOrder := make(map[string]int, len(targetKeys))
	for i, key := range targetKeys {
		if _, ok := targetOrder[key]; !ok {
			targetOrder[key] = i
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		return targetOrder[results[i].Type+":"+results[i].UUID] < targetOrder[results[j].Type+":"+results[j].UUID]
	})
}

func runRm(cmd *cobra.Command, args []string, f rmFlags) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor, err := actorFlag(cmd)
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	// stdin `-` expansion (legacy: single "-" reads newline-separated refs).
	if len(args) == 1 && args[0] == "-" {
		claims := &stdinClaims{}
		if err := claims.claim("targets"); err != nil {
			return err
		}
		if isReaderTTY(cmd.InOrStdin()) {
			return fmt.Errorf("stdin is a terminal; pipe input or use a heredoc")
		}
		scanner := bufio.NewScanner(cmd.InOrStdin())
		args = nil
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				args = append(args, line)
			}
		}
		if serr := scanner.Err(); serr != nil {
			return fmt.Errorf("failed to read stdin: %w", serr)
		}
	}

	// Project-root scoping per arg (legacy applyProjectRootToSelector(arg, false)).
	for i, arg := range args {
		args[i] = sc.selector(arg, false)
	}

	// Resolve targets. Task resolution wins when a path is ambiguous (legacy order).
	var targets []rmTarget
	targetsByKey := map[string]rmTarget{}
	var targetKeys []string
	for _, arg := range args {
		tgt, found, rerr := resolveRmTarget(ctx, tr, arg)
		if rerr != nil {
			return rerr
		}
		if !found {
			if !f.nullglob {
				return fmt.Errorf("target not found: %s", arg)
			}
			continue
		}
		targets = append(targets, tgt)
		targetsByKey[tgt.key()] = tgt
		targetKeys = append(targetKeys, tgt.key())
	}

	if len(targets) == 0 {
		if !f.nullglob {
			return fmt.Errorf("no targets found to remove")
		}
		return nil
	}

	// Dry run.
	if f.dryRun {
		if !isStdoutTTY(cmd.OutOrStdout()) {
			return showRemovalPlanJSON(ctx, cmd, tr, targets, f.purge)
		}
		return showRemovalPlan(ctx, cmd, tr, targets, f.purge)
	}

	// Confirmation for purge (legacy prompts ONLY for purge; archive never prompts).
	if f.purge && !f.yes {
		warning, perr := purgeWarning(ctx, tr, targets)
		if perr != nil {
			return perr
		}
		if cerr := purgeConfirm(cmd, false, warning); cerr != nil {
			return cerr
		}
	}

	op := &bulk.Operation{
		Jobs:            f.jobs,
		ContinueOnError: f.continueOnError,
		ShowProgress:    isStdoutTTY(cmd.OutOrStdout()) && !f.asJSON && !f.ndjson && !f.porcelain,
	}

	results := []rmResult{}
	var resultsMu sync.Mutex
	result := op.Execute(targetKeys, func(key string) error {
		tgt := targetsByKey[key]
		res, rerr := removeTarget(ctx, tr, actor, tgt, f.purge)
		if rerr == nil && res != nil {
			resultsMu.Lock()
			results = append(results, *res)
			resultsMu.Unlock()
		}
		return rerr
	})
	sortRmResults(results, targetKeys)

	// Output (legacy precedence).
	out := cmd.OutOrStdout()
	switch {
	case f.asJSON || (!isStdoutTTY(out) && !f.ndjson && !f.porcelain):
		if werr := writeRmJSON(out, results); werr != nil {
			return werr
		}
	case f.ndjson:
		if werr := writeRmNDJSON(out, results); werr != nil {
			return werr
		}
	case f.porcelain:
		for _, r := range results {
			fmt.Fprintf(out, "%s\n", r.ID)
		}
	default:
		for _, r := range results {
			if r.Purged {
				if r.AttachmentsDeleted > 0 {
					fmt.Fprintf(out, "✓ %s permanently deleted (%d attachments, %.1f MB)\n",
						r.ID, r.AttachmentsDeleted, float64(r.BytesFreed)/(1024*1024))
				} else {
					fmt.Fprintf(out, "✓ %s permanently deleted\n", r.ID)
				}
			} else if r.Type == "promise" {
				fmt.Fprintf(out, "✓ %s abandoned\n", r.ID)
			} else {
				fmt.Fprintf(out, "✓ %s archived\n", r.ID)
			}
		}
	}

	// A refused removal must say why (T-07641 follow-up): the store's reason —
	// a room that would be destroyed, a caused_by lineage — is the operator's
	// only cue toward the alternative, and machine modes were exiting 1 with
	// `[]` and nothing on stderr.
	if result.Failed > 0 {
		errOut := cmd.ErrOrStderr()
		for _, ie := range result.Errors {
			label := ie.Item
			if tgt, ok := targetsByKey[ie.Item]; ok && tgt.ID != "" {
				label = tgt.ID
			}
			fmt.Fprintf(errOut, "Error: %s: %s\n", label, ie.Error)
		}
		if f.continueOnError {
			os.Exit(5) // Partial success
		}
		os.Exit(1)
	}
	return nil
}

// resolveRmTarget resolves a scoped ref to a task (preferred) or container,
// mirroring legacy ResolveTask-then-ResolveContainer ordering. Returns found=false
// when neither resolves (the caller decides nullglob vs error).
func resolveRmTarget(ctx context.Context, tr Transport, ref string) (rmTarget, bool, error) {
	if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(ref)), "PR-") {
		return resolveRmPromise(ctx, tr, ref)
	}
	show, err := tr.Call(ctx, "wrkq.task.show", map[string]string{"task": ref})
	if err == nil {
		var t struct {
			ID       string `json:"id"`
			UUID     string `json:"uuid"`
			Slug     string `json:"slug"`
			Title    string `json:"title"`
			State    string `json:"state"`
			Priority int    `json:"priority"`
		}
		if uerr := json.Unmarshal(show, &t); uerr != nil {
			return rmTarget{}, false, uerr
		}
		return rmTarget{Type: "task", UUID: t.UUID, ID: t.ID, Slug: t.Slug, Title: t.Title, State: t.State, Prio: t.Priority}, true, nil
	}
	if !isNotFound(err) {
		return rmTarget{}, false, errors.New(rpcMessage(err))
	}

	cshow, cerr := tr.Call(ctx, "wrkq.container.show", map[string]string{"path": ref})
	if cerr == nil {
		var c struct {
			ID    string `json:"id"`
			UUID  string `json:"uuid"`
			Slug  string `json:"slug"`
			Title string `json:"title"`
			Kind  string `json:"kind"`
		}
		if uerr := json.Unmarshal(cshow, &c); uerr != nil {
			return rmTarget{}, false, uerr
		}
		return rmTarget{Type: "container", UUID: c.UUID, ID: c.ID, Slug: c.Slug, Title: c.Title, Kind: c.Kind}, true, nil
	}
	if !isNotFound(cerr) {
		return rmTarget{}, false, errors.New(rpcMessage(cerr))
	}
	return resolveRmPromise(ctx, tr, ref)
}

func resolveRmPromise(ctx context.Context, tr Transport, ref string) (rmTarget, bool, error) {
	raw, err := tr.Call(ctx, "wrkq.promise.show", map[string]any{"promise": ref})
	if err != nil {
		if isNotFound(err) {
			return rmTarget{}, false, nil
		}
		return rmTarget{}, false, err
	}
	promise, err := decodePromise(raw)
	if err != nil {
		return rmTarget{}, false, err
	}
	return rmTarget{Type: "promise", UUID: promise.UUID, ID: promise.ID, Slug: promise.ID, Title: promise.Subject, State: promise.State}, true, nil
}

// removeTarget performs the durable removal. Tasks go through wrkq.task.delete
// with an explicit mode. Containers go through wrkq.container.archive for the
// default soft delete and wrkq.container.delete for --purge.
func removeTarget(ctx context.Context, tr Transport, actor string, tgt rmTarget, purge bool) (*rmResult, error) {
	res := &rmResult{
		Type:   tgt.Type,
		ID:     tgt.ID,
		UUID:   tgt.UUID,
		Slug:   tgt.Slug,
		Path:   tgt.Slug,
		Purged: purge,
	}
	if tgt.Type == "promise" {
		params := map[string]any{"promise": tgt.UUID, "mode": "soft"}
		if purge {
			params["mode"] = "purge"
		}
		if actor != "" {
			params["principalRef"] = actor
		}
		if _, err := tr.Call(ctx, "wrkq.promise.delete", params); err != nil {
			return nil, err
		}
		res.Path = tgt.ID
		return res, nil
	}

	if tgt.Type == "container" {
		method := "wrkq.container.archive"
		params := map[string]any{"container": tgt.UUID}
		if purge {
			method = "wrkq.container.delete"
		}
		if actor != "" {
			params["actor"] = actor
		}
		if _, err := tr.Call(ctx, method, params); err != nil {
			if purge && strings.Contains(rpcMessage(err), "container is not empty") {
				return nil, nil
			}
			return nil, errors.New(rpcMessage(err))
		}
		return res, nil
	}

	mode := "archive"
	if purge {
		mode = "purge"
		count, bytes, aerr := taskAttachmentStats(ctx, tr, tgt.UUID)
		if aerr != nil {
			return nil, aerr
		}
		res.AttachmentsDeleted = count
		res.BytesFreed = bytes
	}

	params := map[string]any{"task": tgt.UUID, "mode": mode}
	if actor != "" {
		params["actor"] = actor
	}
	if _, err := tr.Call(ctx, "wrkq.task.delete", params); err != nil {
		return nil, errors.New(rpcMessage(err))
	}
	return res, nil
}

// taskAttachmentStats returns (count, total bytes) of a task's attachments via
// wrkq.attachment.list, paging through any cursor. Mirrors the legacy pre-purge
// SUM(size_bytes)/COUNT(*).
func taskAttachmentStats(ctx context.Context, tr Transport, taskUUID string) (int, int64, error) {
	var count int
	var bytes int64
	cursor := ""
	for {
		params := map[string]any{"task": taskUUID, "limit": 200}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := tr.Call(ctx, "wrkq.attachment.list", params)
		if err != nil {
			return 0, 0, errors.New(rpcMessage(err))
		}
		var page struct {
			Items []struct {
				SizeBytes int64 `json:"sizeBytes"`
			} `json:"items"`
			NextCursor string `json:"nextCursor"`
		}
		if uerr := json.Unmarshal(raw, &page); uerr != nil {
			return 0, 0, uerr
		}
		for _, it := range page.Items {
			count++
			bytes += it.SizeBytes
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return count, bytes, nil
}

// purgeWarning renders the legacy confirmPurge warning block (stderr text BEFORE
// the "Type 'yes' to confirm: " line). It aggregates attachment count/bytes
// across all task targets, exactly as legacy confirmPurge does.
func purgeWarning(ctx context.Context, tr Transport, targets []rmTarget) (string, error) {
	var totalAttachments int
	var totalBytes int64
	for _, tgt := range targets {
		if tgt.Type != "task" {
			continue
		}
		c, b, err := taskAttachmentStats(ctx, tr, tgt.UUID)
		if err != nil {
			return "", err
		}
		totalAttachments += c
		totalBytes += b
	}

	var b []byte
	b = append(b, "\nWARNING: This will permanently delete:\n"...)
	b = append(b, fmt.Sprintf("  - %d item(s)\n", len(targets))...)
	if totalAttachments > 0 {
		b = append(b, fmt.Sprintf("  - %d attachments (%.1f MB)\n", totalAttachments, float64(totalBytes)/(1024*1024))...)
	}
	b = append(b, "\nThis action CANNOT be undone.\n\n"...)
	return string(b), nil
}

// showRemovalPlan renders the legacy TTY dry-run plan.
func showRemovalPlan(ctx context.Context, cmd *cobra.Command, tr Transport, targets []rmTarget, purge bool) error {
	out := cmd.OutOrStdout()
	var totalAttachments int
	var totalBytes int64

	if purge {
		fmt.Fprintf(out, "Would permanently delete %d item(s):\n\n", len(targets))
	} else {
		fmt.Fprintf(out, "Would archive %d item(s):\n\n", len(targets))
	}

	for _, tgt := range targets {
		if tgt.Type == "promise" {
			fmt.Fprintf(out, "  %s\n", tgt.ID)
			fmt.Fprintf(out, "    Subject: %s\n", tgt.Title)
			fmt.Fprintf(out, "    State: %s\n\n", tgt.State)
			continue
		}
		if tgt.Type == "container" {
			fmt.Fprintf(out, "  %s (%s/)\n", tgt.ID, tgt.Slug)
			if tgt.Title != "" {
				fmt.Fprintf(out, "    Title: %s\n", tgt.Title)
			}
			fmt.Fprintf(out, "    Kind: %s\n\n", tgt.Kind)
			continue
		}

		fmt.Fprintf(out, "  %s (%s)\n", tgt.ID, tgt.Slug)
		if tgt.Title != "" {
			fmt.Fprintf(out, "    Title: %s\n", tgt.Title)
		}
		fmt.Fprintf(out, "    State: %s, Priority: %d\n", tgt.State, tgt.Prio)

		if purge {
			count, bytes, err := taskAttachmentStats(ctx, tr, tgt.UUID)
			if err != nil {
				return err
			}
			if count > 0 {
				totalAttachments += count
				totalBytes += bytes
				fmt.Fprintf(out, "    Attachments: %d file(s) (%.1f MB)\n", count, float64(bytes)/(1024*1024))
			}
		}
		fmt.Fprintln(out)
	}

	if purge && totalAttachments > 0 {
		fmt.Fprintf(out, "Total: %d attachments (%.1f MB)\n\n", totalAttachments, float64(totalBytes)/(1024*1024))
		fmt.Fprintf(out, "WARNING: This action CANNOT be undone!\n")
	}
	return nil
}

// showRemovalPlanJSON renders the legacy non-TTY dry-run JSON.
func showRemovalPlanJSON(ctx context.Context, cmd *cobra.Command, tr Transport, targets []rmTarget, purge bool) error {
	_ = ctx
	_ = tr
	entries := make([]map[string]interface{}, 0, len(targets))
	for _, tgt := range targets {
		entry := map[string]interface{}{
			"type":  tgt.Type,
			"uuid":  tgt.UUID,
			"purge": purge,
			"id":    tgt.ID,
			"slug":  tgt.Slug,
			"title": tgt.Title,
		}
		switch tgt.Type {
		case "container":
			entry["kind"] = tgt.Kind
		case "promise":
			entry["state"] = tgt.State
		default:
			entry["state"] = tgt.State
			entry["priority"] = tgt.Prio
		}
		entries = append(entries, entry)
	}
	return writeRmJSON(cmd.OutOrStdout(), map[string]interface{}{
		"dry_run": true,
		"targets": entries,
	})
}

// writeRmJSON matches legacy writeJSONOutput(outputSelection{}, v): 2-space indent
// + trailing newline.
func writeRmJSON(w interface{ Write([]byte) (int, error) }, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// writeRmNDJSON matches legacy writeNDJSONOutput for a slice: one JSON doc per line.
func writeRmNDJSON(w interface{ Write([]byte) (int, error) }, items []rmResult) error {
	enc := json.NewEncoder(w)
	for i := range items {
		if err := enc.Encode(items[i]); err != nil {
			return err
		}
	}
	return nil
}

// isNotFound reports whether err is an RPC WRKQ_NOT_FOUND.
func isNotFound(err error) bool {
	var re *Error
	if errors.As(err, &re) {
		return re.DomainID == "WRKQ_NOT_FOUND"
	}
	return false
}

// rpcMessage strips the domain-code prefix that *Error.Error() prepends, so the
// mirror surfaces the bare server message (matching legacy error wording).
func rpcMessage(err error) string {
	var re *Error
	if errors.As(err, &re) {
		return re.Message
	}
	return err.Error()
}
