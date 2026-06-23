package wrkqapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/bundle"
	"github.com/lherron/wrkq/internal/selectors"
)

// BundleExportViewParams mirrors the legacy `wrkq bundle create` filter surface.
// The CLI applies project-root scoping to the raw --project/--path-prefix args
// BEFORE these params are sent; the server NEVER reads project-root env/flags.
// Project selector→UUID/path resolution and the manifest path-prefix anchoring are
// durable read behavior and so are owned here on the server side, matching the
// legacy CLI's runBundleCreate resolution exactly.
//
// Field order here is the SEMANTIC request envelope order (params are decoded by
// name, so order is documentation, not wire contract). version/commit/buildDate
// carry the CALLER's build identity into the manifest (legacy stamps the CLI
// binary's version), so the mirror passes its own.
type BundleExportViewParams struct {
	Actor           string   `json:"actor,omitempty"`
	Since           string   `json:"since,omitempty"`
	Until           string   `json:"until,omitempty"`
	Project         string   `json:"project,omitempty"`
	PathPrefixes    []string `json:"pathPrefixes,omitempty"`
	IncludeRefs     bool     `json:"includeRefs,omitempty"`
	WithAttachments bool     `json:"withAttachments,omitempty"`
	WithEvents      bool     `json:"withEvents,omitempty"`
	Version         string   `json:"version,omitempty"`
	Commit          string   `json:"commit,omitempty"`
	BuildDate       string   `json:"buildDate,omitempty"`
}

// WrkqBundleTaskDoc is one exported task document: its bundle path (relative,
// without the .md suffix), base_etag, uuid, and the full rendered markdown content
// (frontmatter + body) the CLI writes verbatim to tasks/<path>.md.
type WrkqBundleTaskDoc struct {
	Path     string `json:"path"`
	BaseEtag int    `json:"base_etag,omitempty"`
	UUID     string `json:"uuid,omitempty"`
	Content  string `json:"content"`
}

// WrkqBundleExportView is the server-owned LOGICAL bundle snapshot read model for
// `wrkq bundle create`. It carries everything read from the DB under ONE read
// transaction (task/container/ref/event consistency) and NOTHING about the
// caller-host filesystem: the CLI materializes the directory, manifest.json,
// task/ref markdown, containers.txt, and events.ndjson from this snapshot.
//
// It is NOT a server-filesystem exporter and does NOT return server-local output
// paths. manifest is the bundle.Manifest field-order struct (the manifest.json
// wire shape). events are legacy events.ndjson row order. attachments are
// DESCRIPTORS only (no bytes): under --with-attachments the mirror either fetches
// bytes via the chunked wrkq.attachment.getBytes path OR hard-gates the flag —
// the snapshot never inlines attachment bytes (wrkq.wrkf-rpc.attachment-byte-transfer).
type WrkqBundleExportView struct {
	Manifest    *bundle.Manifest              `json:"manifest"`
	Tasks       []WrkqBundleTaskDoc           `json:"tasks"`
	Containers  []string                      `json:"containers"`
	Refs        []WrkqBundleTaskDoc           `json:"refs,omitempty"`
	Events      []bundle.EventRow             `json:"events,omitempty"`
	Attachments []bundle.AttachmentDescriptor `json:"attachments,omitempty"`
}

// BundleExportView runs the logical bundle export under one read transaction and
// returns the snapshot. It reproduces legacy runBundleCreate's project resolution
// (selector→UUID + v_container_paths path) and path-prefix anchoring so the
// manifest fields (project/project_uuid/path_prefixes) and the collected set match
// legacy byte-for-byte; the CLI then materializes files identically.
func (a *API) BundleExportView(ctx context.Context, p BundleExportViewParams) (*WrkqBundleExportView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	opts := bundle.CreateOptions{
		Actor:           p.Actor,
		Since:           p.Since,
		Until:           p.Until,
		WithAttachments: p.WithAttachments,
		WithEvents:      p.WithEvents,
		IncludeRefs:     p.IncludeRefs,
		Version:         p.Version,
		Commit:          p.Commit,
		BuildDate:       p.BuildDate,
	}

	// Resolve project scope (mirrors legacy runBundleCreate). The selector is
	// already project-root-scoped by the caller.
	if p.Project != "" {
		projectUUID, _, err := selectors.ResolveContainer(a.db, p.Project)
		if err != nil {
			return nil, NewValidationError(fmt.Sprintf("failed to resolve project %q: %s", p.Project, err.Error()), map[string]any{"field": "project"})
		}
		var projectPath string
		if err := a.db.QueryRow("SELECT path FROM v_container_paths WHERE uuid = ?", projectUUID).Scan(&projectPath); err != nil {
			return nil, NewValidationError(fmt.Sprintf("failed to resolve project path: %s", err.Error()), map[string]any{"field": "project"})
		}
		opts.ProjectUUID = projectUUID
		opts.ProjectPath = projectPath
	}

	// Normalize path prefixes (mirrors legacy runBundleCreate anchoring). The raw
	// prefixes are already project-root-scoped by the caller.
	for _, prefix := range p.PathPrefixes {
		trimmed := strings.Trim(strings.TrimSpace(prefix), "/")
		if trimmed == "" {
			continue
		}
		if opts.ProjectPath != "" && !strings.HasPrefix(trimmed, opts.ProjectPath) {
			trimmed = strings.Trim(opts.ProjectPath+"/"+trimmed, "/")
		}
		opts.PathPrefixes = append(opts.PathPrefixes, trimmed)
	}

	// At least one filter is required (mirrors legacy validation). The CLI also
	// validates this, but the server is the durable boundary so it enforces too.
	if opts.Actor == "" && opts.Since == "" && opts.Until == "" && opts.ProjectPath == "" && len(opts.PathPrefixes) == 0 {
		return nil, NewValidationError("at least one filter required (--actor, --since, --until, --project, or --path-prefix)", nil)
	}

	snap, err := bundle.Collect(a.db.DB, opts)
	if err != nil {
		return nil, NewInternalError(err)
	}

	view := &WrkqBundleExportView{
		Manifest:    snap.Manifest,
		Tasks:       toBundleTaskDocs(snap.Tasks),
		Containers:  snap.Containers,
		Refs:        refDocsToBundleTaskDocs(snap.Refs),
		Events:      snap.Events,
		Attachments: snap.Attachments,
	}
	if view.Containers == nil {
		view.Containers = []string{}
	}
	if view.Tasks == nil {
		view.Tasks = []WrkqBundleTaskDoc{}
	}
	return view, nil
}

func toBundleTaskDocs(exports []*bundle.TaskExport) []WrkqBundleTaskDoc {
	docs := make([]WrkqBundleTaskDoc, 0, len(exports))
	for _, e := range exports {
		docs = append(docs, WrkqBundleTaskDoc{
			Path:     e.Path,
			BaseEtag: e.BaseEtag,
			UUID:     e.UUID,
			Content:  e.Content,
		})
	}
	return docs
}

func refDocsToBundleTaskDocs(refs []*bundle.TaskDocument) []WrkqBundleTaskDoc {
	if len(refs) == 0 {
		return nil
	}
	docs := make([]WrkqBundleTaskDoc, 0, len(refs))
	for _, r := range refs {
		docs = append(docs, WrkqBundleTaskDoc{
			Path:    r.Path,
			UUID:    r.UUID,
			Content: r.OriginalContent,
		})
	}
	return docs
}
