package rpccli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/lherron/wrkq/internal/bulk"
	"github.com/spf13/cobra"
)

// newCpCmd mirrors `wrkq cp <source>... <destination>`. The CLI owns the human
// fan-out: stdin sources, project-root scoping, source/destination resolution
// (so nullglob / the >5-source prompt / dry-run see the resolved set), the
// caller-owned >5-source confirmation, the bulk jobs/continue-on-error engine,
// and the output rendering. Each surviving source is copied by ONE server-owned
// wrkq.task.copy call (source resolution, dest-container resolution, source CAS,
// create-or-overwrite, attachment cascade + optional same-store file copy, the
// task.copied event, and the post-commit created webhook all live server-side).
func newCpCmd() *cobra.Command {
	var (
		dryRun          bool
		jobs            int
		continueOnError bool
		withAttachments bool
		shallow         bool
		recursive       bool
		overwrite       bool
		yes             bool
		nullglob        bool
		ifMatch         int64
		asJSON          bool
		ndjson          bool
		porcelain       bool
		one             bool
		zero            bool
	)

	cmd := &cobra.Command{
		Use:   "cp <source>... <destination>",
		Short: "Copy tasks and containers",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cpFlags{
				dryRun: dryRun, jobs: jobs, continueOnError: continueOnError,
				withAttachments: withAttachments, shallow: shallow, recursive: recursive,
				overwrite: overwrite, yes: yes, nullglob: nullglob, ifMatch: ifMatch,
				asJSON: asJSON, ndjson: ndjson, porcelain: porcelain, one: one, zero: zero,
			}
			return runCp(cmd, args, f)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be copied")
	cmd.Flags().IntVarP(&jobs, "jobs", "j", 1, "Parallel workers")
	cmd.Flags().BoolVar(&continueOnError, "continue-on-error", false, "Continue on errors")
	cmd.Flags().BoolVar(&withAttachments, "with-attachments", false, "Copy attachment files")
	cmd.Flags().BoolVar(&shallow, "shallow", false, "Skip attachments entirely")
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "Copy containers recursively")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "Overwrite existing tasks")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompts")
	cmd.Flags().BoolVar(&nullglob, "nullglob", false, "Zero matches is not an error")
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Conditional copy based on source etag")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output NDJSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	cmd.Flags().BoolVar(&one, "1", false, "One per line")
	cmd.Flags().BoolVar(&zero, "0", false, "NUL-separated output")
	return cmd
}

type cpFlags struct {
	dryRun, continueOnError, withAttachments, shallow, recursive, overwrite, yes, nullglob bool
	asJSON, ndjson, porcelain, one, zero                                                   bool
	jobs                                                                                   int
	ifMatch                                                                                int64
}

// cpSrcTask is one resolved source: the (project-root-scoped) selector and its
// resolved task uuid.
type cpSrcTask struct{ selector, uuid string }

// cpResult is the mirror's per-source output row; the snake_case keys + omitempty
// match the legacy copyResult byte-for-byte (and the server WrkqTaskCopyResult).
type cpResult struct {
	SourceID    string `json:"source_id"`
	SourceUUID  string `json:"source_uuid"`
	DestID      string `json:"dest_id"`
	DestUUID    string `json:"dest_uuid"`
	DestPath    string `json:"dest_path"`
	Attachments int    `json:"attachments_copied,omitempty"`
	WithFiles   bool   `json:"with_files,omitempty"`
}

func runCp(cmd *cobra.Command, args []string, f cpFlags) error {
	// Legacy validates the mutually-exclusive flags FIRST (before any DB open).
	if f.withAttachments && f.shallow {
		return fmt.Errorf("--with-attachments and --shallow are mutually exclusive")
	}

	// Container/recursive copy is NOT in this tranche. Hard-gate -r with a
	// mirror-specific error (no silent degradation) rather than risk a wrong copy.
	if f.recursive {
		return fmt.Errorf("cp: --recursive (container copy) not yet implemented in wrkq-rpccli (hard-gated pending parity)")
	}

	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor := actorFlag(cmd)

	sources := args[:len(args)-1]
	destination := args[len(args)-1]

	// stdin sources (`cp - <dest>`): one selector per non-empty line.
	if len(sources) == 1 && sources[0] == "-" {
		scanner := bufio.NewScanner(cmd.InOrStdin())
		sources = nil
		for scanner.Scan() {
			line := scanner.Text()
			if line != "" {
				sources = append(sources, line)
			}
		}
		if serr := scanner.Err(); serr != nil {
			return fmt.Errorf("failed to read stdin: %w", serr)
		}
	}

	for i, src := range sources {
		sources[i] = sc.selector(src, false)
	}
	destination = sc.selector(destination, false)

	// Resolve the destination container first (mirror legacy ordering). On nullglob
	// a missing destination is a silent success.
	destShow, err := tr.Call(cmd.Context(), "wrkq.container.show", map[string]string{"path": destination})
	if err != nil {
		if f.nullglob {
			return nil
		}
		return cpStripErr(err)
	}
	var destDTO struct {
		UUID string `json:"uuid"`
		Path string `json:"path"`
	}
	if uerr := json.Unmarshal(destShow, &destDTO); uerr != nil {
		return uerr
	}

	// Resolve source tasks (mirror legacy selectors.ResolveTask fan-out). nullglob
	// skips unresolved/empty matches; otherwise the first failure aborts.
	var sourceTasks []cpSrcTask
	for _, src := range sources {
		show, serr := tr.Call(cmd.Context(), "wrkq.task.show", map[string]string{"task": src})
		if serr != nil {
			if f.nullglob {
				continue
			}
			return cpStripErr(serr)
		}
		var dto struct {
			UUID string `json:"uuid"`
		}
		if uerr := json.Unmarshal(show, &dto); uerr != nil {
			return uerr
		}
		sourceTasks = append(sourceTasks, cpSrcTask{selector: src, uuid: dto.UUID})
	}

	if len(sourceTasks) == 0 {
		if f.nullglob {
			return nil
		}
		return fmt.Errorf("no tasks found to copy")
	}

	if f.dryRun {
		return cpShowPlan(cmd, tr, sourceTasks, destDTO.Path, f)
	}

	// Caller-owned >5-source confirmation (legacy cp prompt bytes: a single inline
	// "Copy <n> tasks? [y/N] " on stderr accepting a y-prefixed answer). Reuses the
	// promptConfirm core; --yes bypasses.
	if !f.yes && len(sourceTasks) > 5 {
		if perr := promptConfirm(cmd, "",
			fmt.Sprintf("Copy %d tasks? [y/N] ", len(sourceTasks)),
			func(answer string) bool { return strings.HasPrefix(strings.ToLower(answer), "y") },
		); perr != nil {
			return perr
		}
	}

	machineJSON := f.asJSON || (!isStdoutTTY(cmd.OutOrStdout()) && !f.ndjson && !f.porcelain && !f.one && !f.zero)

	op := &bulk.Operation{
		Jobs:            f.jobs,
		ContinueOnError: f.continueOnError,
		ShowProgress:    isStdoutTTY(cmd.OutOrStdout()) && !f.asJSON && !f.ndjson && !f.porcelain,
	}

	results := []cpResult{}
	var resultsMu sync.Mutex
	uuids := make([]string, len(sourceTasks))
	for i, st := range sourceTasks {
		uuids[i] = st.uuid
	}
	result := op.Execute(uuids, func(taskUUID string) error {
		params := map[string]any{
			"source":      taskUUID,
			"destination": destDTO.UUID,
		}
		if f.overwrite {
			params["overwrite"] = true
		}
		if f.withAttachments {
			params["withAttachments"] = true
		}
		if f.shallow {
			params["shallow"] = true
		}
		if f.ifMatch > 0 {
			params["expectEtag"] = f.ifMatch
		}
		if actor != "" {
			params["actor"] = actor
		}
		raw, cerr := tr.Call(cmd.Context(), "wrkq.task.copy", params)
		if cerr != nil {
			return cpStripErr(cerr)
		}
		var r cpResult
		if uerr := json.Unmarshal(raw, &r); uerr != nil {
			return uerr
		}
		resultsMu.Lock()
		results = append(results, r)
		resultsMu.Unlock()
		return nil
	})

	// Output rendering mirrors legacy runCp EXACTLY, including its early returns:
	// the machine-output modes return BEFORE the result.Failed exit-code check, so a
	// single-source failure in machine mode is exit 0 with an empty/partial array —
	// only the human-summary path maps failures to a non-zero exit.
	if machineJSON {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(results)
	}
	if f.ndjson {
		enc := json.NewEncoder(cmd.OutOrStdout())
		for _, r := range results {
			if eerr := enc.Encode(r); eerr != nil {
				return eerr
			}
		}
		return nil
	}
	if f.porcelain || f.one || f.zero {
		delimiter := "\n"
		if f.zero {
			delimiter = "\x00"
		}
		for _, r := range results {
			fmt.Fprintf(cmd.OutOrStdout(), "%s%s", r.DestID, delimiter)
		}
		return nil
	}

	result.PrintSummary(cmd.OutOrStdout())

	if result.Failed > 0 {
		if f.continueOnError {
			os.Exit(5) // Partial success
		}
		os.Exit(1)
	}
	return nil
}

// cpShowPlan mirrors legacy showCopyPlan: a JSON dry-run plan on non-TTY (the
// parity harness path) and a human plan on a TTY. The per-source attachment count
// is read via wrkq.attachment.list so the mirror never touches the DB directly.
//
// containerPath is the dest container's path from the DTO (COALESCE(v.path,
// c.slug)). Legacy renders the plan off container_paths.path, which EXCLUDES the
// root and is empty for a top-level project; the DTO's coalesce falls back to the
// slug for that case, so a single-segment (top-level project) path is normalized
// to "" to byte-match legacy's "<empty>/<slug>" → "/<slug>" rendering.
func cpShowPlan(cmd *cobra.Command, tr Transport, sourceTasks []cpSrcTask, containerPath string, f cpFlags) error {
	type srcMeta struct {
		id, slug    string
		attachments int
	}
	metas := make([]srcMeta, 0, len(sourceTasks))
	destPath := containerPath
	if !strings.Contains(destPath, "/") {
		destPath = ""
	}
	for _, st := range sourceTasks {
		show, err := tr.Call(cmd.Context(), "wrkq.task.show", map[string]string{"task": st.uuid})
		if err != nil {
			continue
		}
		var dto struct {
			ID   string `json:"id"`
			Slug string `json:"slug"`
		}
		if uerr := json.Unmarshal(show, &dto); uerr != nil {
			continue
		}
		m := srcMeta{id: dto.ID, slug: dto.Slug}
		if !f.shallow {
			al, aerr := tr.Call(cmd.Context(), "wrkq.attachment.list", map[string]string{"task": st.uuid})
			if aerr == nil {
				var lst struct {
					Items []json.RawMessage `json:"items"`
				}
				if json.Unmarshal(al, &lst) == nil {
					m.attachments = len(lst.Items)
				}
			}
		}
		metas = append(metas, m)
	}

	if !isStdoutTTY(cmd.OutOrStdout()) {
		entries := make([]map[string]interface{}, 0, len(metas))
		for i, m := range metas {
			entries = append(entries, map[string]interface{}{
				"source_id":   m.id,
				"source_uuid": sourceTasks[i].uuid,
				"dest_path":   destPath + "/" + m.slug,
				"would_copy":  true,
			})
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]interface{}{
			"dry_run": true,
			"copies":  entries,
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Would copy %d task(s):\n\n", len(metas))
	totalAttachments := 0
	for _, m := range metas {
		fmt.Fprintf(cmd.OutOrStdout(), "  %s (%s) → %s/%s (new)\n", m.id, m.slug, destPath, m.slug)
		if !f.shallow {
			totalAttachments += m.attachments
		}
	}
	if totalAttachments > 0 && !f.shallow {
		fmt.Fprintf(cmd.OutOrStdout(), "\nAttachments:\n")
		fmt.Fprintf(cmd.OutOrStdout(), "  %d file(s)\n", totalAttachments)
		if f.withAttachments {
			fmt.Fprintf(cmd.OutOrStdout(), "  Files will be copied to new location\n")
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "  Only metadata will be copied (use --with-attachments to copy files)\n")
		}
	}
	return nil
}

// cpStripErr strips the RPC domain-code prefix so the mirror surfaces the bare
// resolver/conflict message legacy emits (legacy cp returns the raw error text).
func cpStripErr(err error) error {
	if re, ok := err.(*Error); ok {
		return errors.New(re.Message)
	}
	return err
}
