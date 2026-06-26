package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/bundle"
	"github.com/spf13/cobra"
)

// bundleVersion mirrors legacy runBundleCreate's stamped build identity exactly
// (internal/cli/bundle.go pins Version="0.1.0", Commit="", BuildDate=""). The
// manifest carries these verbatim, so the mirror MUST pass the same values to the
// server for manifest.json byte-parity.
const (
	bundleVersion   = "0.1.0"
	bundleCommit    = ""
	bundleBuildDate = ""
)

// newBundleCmd mirrors `wrkq bundle create`. Durable behavior — the LOGICAL bundle
// read under ONE transaction (task/container/ref/event consistency) — runs
// server-side via wrkq.bundle.exportView; the CLI owns the caller-host work:
// project-root scoping, the dry-run preview, and MATERIALIZING the snapshot into a
// directory (manifest.json, tasks/*.md, refs/*.md, containers.txt, events.ndjson)
// byte-identically to legacy (the shared internal/bundle.Materialize writer).
//
// --with-attachments matches current legacy bundle behavior: the logical snapshot
// carries attachment DESCRIPTORS only, never bytes, and the shared materializer
// sets manifest.with_attachments without writing attachment files. If bundle byte
// materialization is added later, bytes must cross via chunked attachment transfer,
// not inline in the snapshot.
func newBundleCmd() *cobra.Command {
	bundleCmd := &cobra.Command{
		Use:   "bundle",
		Short: "Bundle operations for Git-ops workflow",
		Long:  `Commands for creating and managing PR bundles for the Git-ops workflow.`,
	}

	var (
		out             string
		actor           string
		since           string
		until           string
		project         string
		pathPrefixes    []string
		includeRefs     bool
		withAttachments bool
		noEvents        bool
		asJSON          bool
		porcelain       bool
		dryRun          bool
	)

	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a bundle of changes for PR workflow",
		Long: `Create a bundle of changes for PR workflow.

Export tasks touched by a specific actor or time window as a reviewable bundle.
Bundles can be committed to git and applied using wrkqadm bundle apply.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			f := bundleFlags{
				out: out, actor: actor, since: since, until: until, project: project,
				pathPrefixes: pathPrefixes, includeRefs: includeRefs,
				withAttachments: withAttachments, noEvents: noEvents,
				asJSON: asJSON, porcelain: porcelain, dryRun: dryRun,
			}
			return runBundleCreate(cmd, f)
		},
	}
	createCmd.Flags().StringVar(&out, "out", ".wrkq", "Output directory for bundle")
	createCmd.Flags().StringVar(&actor, "actor", "", "Filter by actor (slug or friendly ID)")
	createCmd.Flags().StringVar(&since, "since", "", "Filter by cursor (event:<id> or ts:<rfc3339>) or RFC3339 timestamp")
	createCmd.Flags().StringVar(&until, "until", "", "Filter by end timestamp (RFC3339)")
	createCmd.Flags().StringVar(&project, "project", "", "Restrict export to a project (path or UUID)")
	createCmd.Flags().StringArrayVar(&pathPrefixes, "path-prefix", nil, "Restrict export to path prefix (repeatable)")
	createCmd.Flags().BoolVar(&includeRefs, "include-refs", false, "Include refs/ stubs for related tasks outside scope")
	createCmd.Flags().BoolVar(&withAttachments, "with-attachments", false, "Include attachment files")
	createCmd.Flags().BoolVar(&noEvents, "no-events", false, "Skip events.ndjson")
	createCmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	createCmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	createCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be exported without writing")

	bundleCmd.AddCommand(createCmd)
	return bundleCmd
}

type bundleFlags struct {
	out, actor, since, until, project                         string
	pathPrefixes                                              []string
	includeRefs, withAttachments, noEvents, asJSON, porcelain bool
	dryRun                                                    bool
}

func runBundleCreate(cmd *cobra.Command, f bundleFlags) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()

	// Resolve project scope on the caller host for the dry-run options echo AND so
	// the path-prefix anchoring + the manifest project fields match legacy. The
	// server independently re-resolves the (already-scoped) project selector.
	var projectScoped string
	var projectUUID, projectPath string
	if f.project != "" {
		projectScoped = sc.selector(f.project, false)
		show, serr := tr.Call(cmd.Context(), "wrkq.container.show", map[string]string{"path": projectScoped})
		if serr != nil {
			return bundleStripErr(fmt.Errorf("failed to resolve project %q: %w", projectScoped, serr))
		}
		var dto struct {
			UUID string `json:"uuid"`
			Path string `json:"path"`
		}
		if uerr := json.Unmarshal(show, &dto); uerr != nil {
			return uerr
		}
		projectUUID = dto.UUID
		projectPath = dto.Path
	}

	// Build the legacy-equivalent CreateOptions for filter validation + the dry-run
	// echo. PathPrefixes are project-root-scoped then anchored exactly as legacy.
	opts := bundle.CreateOptions{
		OutputDir:       f.out,
		Actor:           f.actor,
		Since:           f.since,
		Until:           f.until,
		WithAttachments: f.withAttachments,
		WithEvents:      !f.noEvents,
		IncludeRefs:     f.includeRefs,
		ProjectUUID:     projectUUID,
		ProjectPath:     projectPath,
		Version:         bundleVersion,
		Commit:          bundleCommit,
		BuildDate:       bundleBuildDate,
	}
	for _, prefix := range f.pathPrefixes {
		trimmed := sc.path(prefix, false)
		trimmed = strings.Trim(strings.TrimSpace(trimmed), "/")
		if trimmed == "" {
			continue
		}
		if opts.ProjectPath != "" && !strings.HasPrefix(trimmed, opts.ProjectPath) {
			trimmed = strings.Trim(opts.ProjectPath+"/"+trimmed, "/")
		}
		opts.PathPrefixes = append(opts.PathPrefixes, trimmed)
	}

	// Validate filters (legacy checks this before touching the DB / RPC).
	if opts.Actor == "" && opts.Since == "" && opts.Until == "" && opts.ProjectPath == "" && len(opts.PathPrefixes) == 0 {
		return errors.New("at least one filter required (--actor, --since, --until, --project, or --path-prefix)")
	}

	if f.dryRun {
		if !isStdoutTTY(cmd.OutOrStdout()) {
			return encodeJSONIndent(cmd, map[string]interface{}{
				"dry_run": true,
				"options": opts,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Dry run - would create bundle with:\n")
		fmt.Fprintf(cmd.OutOrStdout(), "  Output: %s\n", opts.OutputDir)
		if opts.Actor != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  Actor: %s\n", opts.Actor)
		}
		if opts.Since != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  Since: %s\n", opts.Since)
		}
		if opts.Until != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  Until: %s\n", opts.Until)
		}
		if opts.ProjectPath != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  Project: %s\n", opts.ProjectPath)
		}
		if len(opts.PathPrefixes) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "  Path prefixes: %s\n", strings.Join(opts.PathPrefixes, ", "))
		}
		if opts.IncludeRefs {
			fmt.Fprintf(cmd.OutOrStdout(), "  Include refs: true\n")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  With attachments: %v\n", opts.WithAttachments)
		fmt.Fprintf(cmd.OutOrStdout(), "  With events: %v\n", opts.WithEvents)
		return nil
	}

	// Server-owned LOGICAL snapshot (one read transaction). The scoped project
	// selector + scoped/anchored path-prefixes are sent; the server never reads
	// project-root env/flags.
	params := map[string]interface{}{
		"withEvents": opts.WithEvents,
		"version":    opts.Version,
		"commit":     opts.Commit,
		"buildDate":  opts.BuildDate,
	}
	if opts.Actor != "" {
		params["actor"] = opts.Actor
	}
	if opts.Since != "" {
		params["since"] = opts.Since
	}
	if opts.Until != "" {
		params["until"] = opts.Until
	}
	if projectScoped != "" {
		params["project"] = projectScoped
	}
	if len(opts.PathPrefixes) > 0 {
		params["pathPrefixes"] = opts.PathPrefixes
	}
	if opts.IncludeRefs {
		params["includeRefs"] = true
	}
	if opts.WithAttachments {
		params["withAttachments"] = true
	}

	raw, cerr := tr.Call(cmd.Context(), "wrkq.bundle.exportView", params)
	if cerr != nil {
		return bundleStripErr(fmt.Errorf("failed to create bundle: %w", cerr))
	}

	snap, err := decodeBundleSnapshot(raw)
	if err != nil {
		return fmt.Errorf("failed to create bundle: %w", err)
	}

	// Materialize on the caller host — byte-identical to legacy (shared writer).
	b, err := bundle.Materialize(snap, opts.OutputDir)
	if err != nil {
		return fmt.Errorf("failed to create bundle: %w", err)
	}

	// Output rendering mirrors legacy runBundleCreate EXACTLY.
	result := map[string]interface{}{
		"bundle_dir":       b.Dir,
		"tasks_count":      len(b.Tasks),
		"containers_count": len(b.Containers),
		"manifest":         b.Manifest,
	}
	if f.asJSON || (!f.porcelain && !isStdoutTTY(cmd.OutOrStdout())) {
		return encodeJSONIndent(cmd, result)
	}

	if f.porcelain {
		fmt.Fprintf(cmd.OutOrStdout(), "%d\t%d\t%s\n", len(b.Tasks), len(b.Containers), b.Dir)
		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✓ Bundle created successfully\n")
	fmt.Fprintf(cmd.OutOrStdout(), "  Location: %s\n", b.Dir)
	fmt.Fprintf(cmd.OutOrStdout(), "  Tasks: %d\n", len(b.Tasks))
	fmt.Fprintf(cmd.OutOrStdout(), "  Containers: %d\n", len(b.Containers))

	if b.Manifest.Actor != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Actor: %s\n", b.Manifest.Actor)
	}
	if b.Manifest.Since != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Since: %s\n", b.Manifest.Since)
	}
	if b.Manifest.SinceCursor != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Since cursor: %s\n", b.Manifest.SinceCursor)
	}
	if b.Manifest.Until != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Until: %s\n", b.Manifest.Until)
	}
	if b.Manifest.Project != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Project: %s\n", b.Manifest.Project)
	}
	if len(b.Manifest.PathPrefixes) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  Path prefixes: %s\n", strings.Join(b.Manifest.PathPrefixes, ", "))
	}

	if b.Manifest.WithAttachments {
		fmt.Fprintf(cmd.OutOrStdout(), "  Attachments: included\n")
	}
	if b.Manifest.WithEvents {
		fmt.Fprintf(cmd.OutOrStdout(), "  Events: included\n")
	}
	if b.Manifest.IncludeRefs {
		fmt.Fprintf(cmd.OutOrStdout(), "  Refs: included (%d)\n", b.Manifest.RefCount)
	}

	return nil
}

// bundleSnapshotWire is the wire shape of wrkq.bundle.exportView (mirrors the
// server WrkqBundleExportView). The CLI decodes it into the in-memory
// bundle.Snapshot that bundle.Materialize writes to disk.
type bundleSnapshotWire struct {
	Manifest    *bundle.Manifest              `json:"manifest"`
	Tasks       []bundleTaskDocWire           `json:"tasks"`
	Containers  []string                      `json:"containers"`
	Refs        []bundleTaskDocWire           `json:"refs"`
	Events      []bundle.EventRow             `json:"events"`
	Attachments []bundle.AttachmentDescriptor `json:"attachments"`
}

type bundleTaskDocWire struct {
	Path     string `json:"path"`
	BaseEtag int    `json:"base_etag"`
	UUID     string `json:"uuid"`
	Content  string `json:"content"`
}

func decodeBundleSnapshot(raw json.RawMessage) (*bundle.Snapshot, error) {
	var w bundleSnapshotWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	snap := &bundle.Snapshot{
		Manifest:    w.Manifest,
		Containers:  w.Containers,
		Events:      w.Events,
		Attachments: w.Attachments,
	}
	for _, td := range w.Tasks {
		snap.Tasks = append(snap.Tasks, &bundle.TaskExport{
			UUID:     td.UUID,
			Path:     td.Path,
			BaseEtag: td.BaseEtag,
			Content:  td.Content,
		})
	}
	for _, rd := range w.Refs {
		snap.Refs = append(snap.Refs, &bundle.TaskDocument{
			Path:            rd.Path,
			UUID:            rd.UUID,
			OriginalContent: rd.Content,
		})
	}
	return snap, nil
}

// bundleStripErr strips the RPC domain-code prefix so the mirror surfaces the bare
// message legacy emits (legacy bundle wraps with "failed to create bundle: %w" or
// "failed to resolve project ...: %w" around the raw resolver error).
func bundleStripErr(err error) error {
	var re *Error
	if errors.As(err, &re) {
		return errors.New(strings.Replace(err.Error(), re.Error(), re.Message, 1))
	}
	return err
}
