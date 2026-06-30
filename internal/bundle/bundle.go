package bundle

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
)

// Manifest represents the bundle manifest.json structure
type Manifest struct {
	MachineInterfaceVersion int      `json:"machine_interface_version"`
	Version                 string   `json:"version,omitempty"`
	Commit                  string   `json:"commit,omitempty"`
	BuildDate               string   `json:"build_date,omitempty"`
	Timestamp               string   `json:"timestamp"`
	Actor                   string   `json:"actor,omitempty"`
	Since                   string   `json:"since,omitempty"`
	Until                   string   `json:"until,omitempty"`
	SinceCursor             string   `json:"since_cursor,omitempty"`
	Project                 string   `json:"project,omitempty"`
	ProjectUUID             string   `json:"project_uuid,omitempty"`
	PathPrefixes            []string `json:"path_prefixes,omitempty"`
	WithAttachments         bool     `json:"with_attachments"`
	WithEvents              bool     `json:"with_events"`
	IncludeRefs             bool     `json:"include_refs,omitempty"`
	RefCount                int      `json:"ref_count,omitempty"`
}

// TaskDocument represents a task document from the bundle with metadata
type TaskDocument struct {
	Path            string `yaml:"path"`
	BaseEtag        int    `yaml:"base_etag,omitempty"`
	UUID            string `yaml:"uuid,omitempty"`
	Description     string // The actual task content (everything after frontmatter)
	OriginalContent string // The full original document including frontmatter
}

// Bundle represents a complete bundle with all its components
type Bundle struct {
	Dir        string
	Manifest   *Manifest
	Containers []string
	Tasks      []*TaskDocument
	Refs       []*TaskDocument
}

// LoadManifest reads and validates the bundle manifest
func LoadManifest(bundleDir string) (*Manifest, error) {
	manifestPath := filepath.Join(bundleDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	// Validate machine interface version
	if manifest.MachineInterfaceVersion == 0 {
		return nil, fmt.Errorf("manifest missing machine_interface_version")
	}

	return &manifest, nil
}

// LoadContainers reads the containers.txt file
func LoadContainers(bundleDir string) ([]string, error) {
	containersPath := filepath.Join(bundleDir, "containers.txt")

	// containers.txt is optional
	if _, err := os.Stat(containersPath); os.IsNotExist(err) {
		return nil, nil
	}

	data, err := os.ReadFile(containersPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read containers.txt: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	var containers []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			containers = append(containers, line)
		}
	}

	return containers, nil
}

// LoadTasks reads all task documents from the tasks/ directory
func LoadTasks(bundleDir string) ([]*TaskDocument, error) {
	tasksDir := filepath.Join(bundleDir, "tasks")

	// tasks directory is optional
	if _, err := os.Stat(tasksDir); os.IsNotExist(err) {
		return nil, nil
	}

	var tasks []*TaskDocument

	err := filepath.Walk(tasksDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		// Read task document
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		// Parse frontmatter to extract metadata
		task, err := ParseTaskDocument(string(data))
		if err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}

		// Derive path from file location relative to tasks/
		relPath, err := filepath.Rel(tasksDir, path)
		if err != nil {
			return err
		}
		// Remove .md extension to get the path
		task.Path = strings.TrimSuffix(relPath, ".md")

		tasks = append(tasks, task)
		return nil
	})

	if err != nil {
		return nil, err
	}

	return tasks, nil
}

// LoadRefs reads all reference task stubs from the refs/ directory
func LoadRefs(bundleDir string) ([]*TaskDocument, error) {
	refsDir := filepath.Join(bundleDir, "refs")

	// refs directory is optional
	if _, err := os.Stat(refsDir); os.IsNotExist(err) {
		return nil, nil
	}

	var refs []*TaskDocument

	err := filepath.Walk(refsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		ref, err := ParseTaskDocument(string(data))
		if err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}

		relPath, err := filepath.Rel(refsDir, path)
		if err != nil {
			return err
		}
		ref.Path = strings.TrimSuffix(relPath, ".md")

		refs = append(refs, ref)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return refs, nil
}

// ParseTaskDocument parses a task document and extracts metadata from frontmatter
func ParseTaskDocument(content string) (*TaskDocument, error) {
	task := &TaskDocument{
		OriginalContent: content,
	}

	// Split into frontmatter and description
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		// No frontmatter, entire content is description
		task.Description = content
		return task, nil
	}

	// parts[0] is empty (before first ---)
	// parts[1] is the frontmatter
	// parts[2] is the description
	frontmatter := parts[1]
	task.Description = strings.TrimSpace(parts[2])

	// Parse frontmatter for metadata we care about
	lines := strings.Split(frontmatter, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Simple YAML parsing for the fields we need
		if strings.HasPrefix(line, "uuid:") {
			task.UUID = strings.TrimSpace(strings.TrimPrefix(line, "uuid:"))
		} else if strings.HasPrefix(line, "path:") {
			// path might be in frontmatter or derived from filename
			if task.Path == "" {
				task.Path = strings.TrimSpace(strings.TrimPrefix(line, "path:"))
			}
		} else if strings.HasPrefix(line, "base_etag:") {
			var etag int
			_, err := fmt.Sscanf(line, "base_etag: %d", &etag)
			if err == nil {
				task.BaseEtag = etag
			}
		}
	}

	return task, nil
}

// Load reads an entire bundle from a directory
func Load(bundleDir string) (*Bundle, error) {
	manifest, err := LoadManifest(bundleDir)
	if err != nil {
		return nil, err
	}

	containers, err := LoadContainers(bundleDir)
	if err != nil {
		return nil, err
	}

	tasks, err := LoadTasks(bundleDir)
	if err != nil {
		return nil, err
	}

	refs, err := LoadRefs(bundleDir)
	if err != nil {
		return nil, err
	}

	return &Bundle{
		Dir:        bundleDir,
		Manifest:   manifest,
		Containers: containers,
		Tasks:      tasks,
		Refs:       refs,
	}, nil
}

// CreateOptions specifies options for bundle creation
type CreateOptions struct {
	// Actor filter (UUID or slug)
	Actor string
	// Time window
	Since string
	Until string
	// Cursor-based export (event:<id> or ts:<rfc3339>)
	SinceCursor string
	// Project scope
	ProjectUUID string
	ProjectPath string
	// Path prefix filters (absolute paths)
	PathPrefixes []string
	// Include refs/ stubs
	IncludeRefs bool
	// Include attachments
	WithAttachments bool
	// Include event log
	WithEvents bool
	// Output directory
	OutputDir string
	// Version information
	Version   string
	Commit    string
	BuildDate string
}

// TaskExport represents a task to be exported
type TaskExport struct {
	UUID     string
	Path     string
	BaseEtag int
	Content  string // Full cat output including frontmatter
}

// EventRow is one exported event in the legacy events.ndjson row shape and field
// order (the encoder used by legacy exportEvents). It is the ONLY public carrier
// of an event row so the RPC snapshot and the legacy writer share the EXACT same
// JSON tag order — events.ndjson byte parity depends on this being the wire shape.
type EventRow struct {
	ID           int     `json:"id"`
	Timestamp    string  `json:"timestamp"`
	PrincipalRef *string `json:"principal_ref"`
	ResourceType string  `json:"resource_type"`
	ResourceUUID string  `json:"resource_uuid"`
	EventType    string  `json:"event_type"`
	Etag         *int    `json:"etag"`
	Payload      *string `json:"payload,omitempty"`
}

// AttachmentDescriptor names one attachment that WOULD be exported under
// --with-attachments. The RPC snapshot carries descriptors only (no bytes): bytes
// cross the protocol via the chunked wrkq.attachment.getBytes path, never inline
// in the snapshot (wrkq.wrkf-rpc.attachment-byte-transfer arch record).
type AttachmentDescriptor struct {
	TaskUUID string `json:"task_uuid"`
	Filename string `json:"filename"`
}

// Snapshot is the in-memory LOGICAL bundle: everything Collect read from the DB
// under one transaction, with NO filesystem materialization. Materialize turns a
// Snapshot into the on-disk bundle directory. Splitting collect (durable read) from
// materialize (caller-host file write) is what lets the RPC server own the read
// while the CLI materializes files identically to legacy.
type Snapshot struct {
	Manifest    *Manifest
	Tasks       []*TaskExport
	Containers  []string
	Refs        []*TaskDocument
	Events      []EventRow
	Attachments []AttachmentDescriptor
}

// Create creates a new bundle from database content. It is now the legacy
// composition of Collect (durable read) + Materialize (file write): byte-identical
// to the pre-split behavior because both phases run the same code the RPC path uses.
func Create(db *sql.DB, opts CreateOptions) (*Bundle, error) {
	snap, err := Collect(db, opts)
	if err != nil {
		return nil, err
	}
	return Materialize(snap, opts.OutputDir)
}

// Collect runs the LOGICAL bundle export under ONE read transaction and returns an
// in-memory Snapshot. No files are written. This is the server-owned half of the
// bundle export: task/container/ref/event reads are internally consistent. With
// opts.WithAttachments it records attachment DESCRIPTORS only (no bytes).
func Collect(db *sql.DB, opts CreateOptions) (*Snapshot, error) {
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin read transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Parse since cursor (event:<id> or ts:<rfc3339>) or RFC3339 timestamp
	rawSince := opts.SinceCursor
	if rawSince == "" {
		rawSince = opts.Since
	}
	sinceEventID, sinceTimestamp, sinceCursor, err := parseSinceFilter(rawSince)
	if err != nil {
		return nil, err
	}
	if sinceCursor != "" {
		opts.SinceCursor = sinceCursor
	}
	if sinceTimestamp != "" {
		opts.Since = sinceTimestamp
	} else if sinceCursor != "" {
		opts.Since = ""
	}

	// Principal-only attribution filter: the legacy actor filter (UUID or slug
	// resolved via the actors table) is gone. The selector must normalize to a
	// canonical principal ref (agent:<id>); we then filter event_log by its
	// principal_ref column. Bare slugs are tolerated as a SELECTOR via
	// NormalizeCompat, but actor UUIDs / A-* / system:* refs are rejected.
	if opts.Actor != "" {
		normalized, err := attribution.NormalizeCompat(opts.Actor)
		if err != nil {
			return nil, fmt.Errorf("invalid actor filter %q: %w", opts.Actor, err)
		}
		opts.Actor = normalized
	}

	// Determine effective path prefixes
	var pathPrefixes []string
	if len(opts.PathPrefixes) > 0 {
		pathPrefixes = append(pathPrefixes, opts.PathPrefixes...)
	} else if opts.ProjectPath != "" {
		pathPrefixes = append(pathPrefixes, opts.ProjectPath)
	}

	// Build query to find tasks modified by actor/time window
	query := `
		SELECT DISTINCT t.uuid, t.slug, cp.path as container_path, t.etag
		FROM tasks t
		JOIN event_log e ON e.resource_uuid = t.uuid AND e.resource_type = 'task'
		LEFT JOIN v_container_paths cp ON t.project_uuid = cp.uuid
		WHERE 1=1
	`
	args := []interface{}{}

	// Filter by principal ref (canonical agent:<id>)
	if opts.Actor != "" {
		query += ` AND e.principal_ref = ?`
		args = append(args, opts.Actor)
	}

	// Filter by cursor or time window
	if sinceEventID != nil {
		query += ` AND e.id > ?`
		args = append(args, *sinceEventID)
	}
	if sinceTimestamp != "" {
		query += ` AND e.timestamp >= ?`
		args = append(args, sinceTimestamp)
	}

	// Filter by time window
	if opts.Until != "" {
		query += ` AND e.timestamp <= ?`
		args = append(args, opts.Until)
	}

	// Filter by path prefix
	if len(pathPrefixes) > 0 {
		var conditions []string
		for _, prefix := range pathPrefixes {
			conditions = append(conditions, "(cp.path = ? OR cp.path LIKE ? || '/%')")
			args = append(args, prefix, prefix)
		}
		query += " AND (" + strings.Join(conditions, " OR ") + ")"
	}

	query += ` ORDER BY container_path, t.slug`

	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var tasks []*TaskExport
	containerMap := make(map[string]bool)

	for rows.Next() {
		var taskUUID, taskSlug string
		var containerPath *string
		var currentEtag int

		if err := rows.Scan(&taskUUID, &taskSlug, &containerPath, &currentEtag); err != nil {
			return nil, fmt.Errorf("failed to scan task: %w", err)
		}

		// Compute base_etag (earliest etag from the filtered event log)
		baseEtag, err := computeBaseEtagTx(tx, taskUUID, opts, sinceEventID, sinceTimestamp)
		if err != nil {
			return nil, fmt.Errorf("failed to compute base_etag for task %s: %w", taskUUID, err)
		}

		// Build task path
		taskPath := taskSlug
		if containerPath != nil && *containerPath != "" {
			taskPath = *containerPath + "/" + taskSlug

			// Add all parent containers to map (for mkdir -p pattern)
			parts := strings.Split(*containerPath, "/")
			currentPath := ""
			for _, part := range parts {
				if currentPath != "" {
					currentPath += "/"
				}
				currentPath += part
				containerMap[currentPath] = true
			}
		}

		// Export task content
		content, err := exportTaskTx(tx, taskUUID)
		if err != nil {
			return nil, fmt.Errorf("failed to export task %s: %w", taskUUID, err)
		}

		// Add base_etag and path to frontmatter
		content = addBundleFieldsToFrontmatter(content, taskPath, baseEtag)

		tasks = append(tasks, &TaskExport{
			UUID:     taskUUID,
			Path:     taskPath,
			BaseEtag: baseEtag,
			Content:  content,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tasks: %w", err)
	}

	// Collect refs/ stubs if requested (logical only — no file writes)
	var refs []*TaskDocument
	if opts.IncludeRefs {
		var err error
		refs, err = collectRefs(tx, tasks)
		if err != nil {
			return nil, fmt.Errorf("failed to export refs: %w", err)
		}
	}

	// Build containers list (sorted parent-before-child)
	var containers []string
	for container := range containerMap {
		containers = append(containers, container)
	}
	sortContainersByDepth(containers)

	// Record attachment descriptors if requested (no bytes).
	var attachments []AttachmentDescriptor
	if opts.WithAttachments {
		attachments, err = collectAttachmentDescriptors(tx, tasks)
		if err != nil {
			return nil, fmt.Errorf("failed to collect attachments: %w", err)
		}
	}

	// Collect event log if requested
	var events []EventRow
	if opts.WithEvents {
		events, err = collectEvents(tx, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to export events: %w", err)
		}
	}

	manifest := &Manifest{
		MachineInterfaceVersion: 1,
		Version:                 opts.Version,
		Commit:                  opts.Commit,
		BuildDate:               opts.BuildDate,
		Timestamp:               time.Now().UTC().Format(time.RFC3339),
		Actor:                   opts.Actor,
		Since:                   opts.Since,
		Until:                   opts.Until,
		SinceCursor:             opts.SinceCursor,
		Project:                 opts.ProjectPath,
		ProjectUUID:             opts.ProjectUUID,
		PathPrefixes:            pathPrefixes,
		WithAttachments:         opts.WithAttachments,
		WithEvents:              opts.WithEvents,
		IncludeRefs:             opts.IncludeRefs,
		RefCount:                len(refs),
	}

	return &Snapshot{
		Manifest:    manifest,
		Tasks:       tasks,
		Containers:  containers,
		Refs:        refs,
		Events:      events,
		Attachments: attachments,
	}, nil
}

// Materialize writes a Snapshot to outputDir as the on-disk bundle (manifest.json,
// tasks/*.md, refs/*.md, containers.txt, events.ndjson). Attachment BYTE
// materialization is NOT done here (descriptors only); callers that need bytes
// fetch them via wrkq.attachment.getBytes.
func Materialize(snap *Snapshot, outputDir string) (*Bundle, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create bundle directory: %w", err)
	}
	tasksDir := filepath.Join(outputDir, "tasks")
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create tasks directory: %w", err)
	}

	for _, task := range snap.Tasks {
		taskFilePath := filepath.Join(tasksDir, task.Path+".md")
		if err := os.MkdirAll(filepath.Dir(taskFilePath), 0755); err != nil {
			return nil, fmt.Errorf("failed to create task directory: %w", err)
		}
		if err := os.WriteFile(taskFilePath, []byte(task.Content), 0644); err != nil {
			return nil, fmt.Errorf("failed to write task file: %w", err)
		}
	}

	if len(snap.Refs) > 0 {
		refsDir := filepath.Join(outputDir, "refs")
		if err := os.MkdirAll(refsDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create refs directory: %w", err)
		}
		for _, ref := range snap.Refs {
			refFilePath := filepath.Join(refsDir, ref.Path+".md")
			if err := os.MkdirAll(filepath.Dir(refFilePath), 0755); err != nil {
				return nil, fmt.Errorf("failed to create ref directory: %w", err)
			}
			if err := os.WriteFile(refFilePath, []byte(ref.OriginalContent), 0644); err != nil {
				return nil, fmt.Errorf("failed to write ref file: %w", err)
			}
		}
	}

	if len(snap.Containers) > 0 {
		containersPath := filepath.Join(outputDir, "containers.txt")
		containersContent := strings.Join(snap.Containers, "\n") + "\n"
		if err := os.WriteFile(containersPath, []byte(containersContent), 0644); err != nil {
			return nil, fmt.Errorf("failed to write containers.txt: %w", err)
		}
	}

	if snap.Manifest != nil && snap.Manifest.WithEvents {
		eventsPath := filepath.Join(outputDir, "events.ndjson")
		f, err := os.Create(eventsPath)
		if err != nil {
			return nil, fmt.Errorf("failed to create events file: %w", err)
		}
		encoder := json.NewEncoder(f)
		for i := range snap.Events {
			if err := encoder.Encode(snap.Events[i]); err != nil {
				_ = f.Close()
				return nil, fmt.Errorf("failed to encode event: %w", err)
			}
		}
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("failed to close events file: %w", err)
		}
	}

	manifestPath := filepath.Join(outputDir, "manifest.json")
	manifestJSON, err := json.MarshalIndent(snap.Manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, manifestJSON, 0644); err != nil {
		return nil, fmt.Errorf("failed to write manifest: %w", err)
	}

	return &Bundle{
		Dir:        outputDir,
		Manifest:   snap.Manifest,
		Containers: snap.Containers,
		Tasks:      convertTaskExportsToTaskDocuments(snap.Tasks),
		Refs:       snap.Refs,
	}, nil
}

// queryRower is the subset of *sql.DB / *sql.Tx that the collect helpers use, so
// the SAME read logic runs both legacy (was *sql.DB) and inside Collect's single
// read transaction (*sql.Tx).
type queryRower interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

// computeBaseEtagTx computes the base etag for a task based on the earliest event in the filtered set
func computeBaseEtagTx(db queryRower, taskUUID string, opts CreateOptions, sinceEventID *int64, sinceTimestamp string) (int, error) {
	// Query the earliest event for this task before any changes in the filtered window
	// This gives us the etag the task had when the filtered changes started
	query := `
		SELECT etag
		FROM event_log
		WHERE resource_uuid = ?
		AND resource_type = 'task'
	`
	args := []interface{}{taskUUID}

	// Apply same filters as main query
	if opts.Actor != "" {
		query += ` AND principal_ref = ?`
		args = append(args, opts.Actor)
	}
	if sinceEventID != nil {
		query += ` AND id > ?`
		args = append(args, *sinceEventID)
	}
	if sinceTimestamp != "" {
		query += ` AND timestamp >= ?`
		args = append(args, sinceTimestamp)
	}
	if opts.Until != "" {
		query += ` AND timestamp <= ?`
		args = append(args, opts.Until)
	}

	query += ` ORDER BY timestamp ASC, id ASC LIMIT 1`

	var baseEtag int
	err := db.QueryRow(query, args...).Scan(&baseEtag)
	if err != nil {
		// If no events found, return current etag - 1 as base
		var currentEtag int
		err = db.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentEtag)
		if err != nil {
			return 0, err
		}
		// Use etag before the changes (current - 1, or 0 if current is 1)
		if currentEtag > 1 {
			return currentEtag - 1, nil
		}
		return 0, nil
	}

	// Return etag from the event (this is the etag AFTER the event, so we use it as-is
	// since it represents the state when the changes started)
	return baseEtag, nil
}

// exportTaskTx exports a single task in wrkq cat format
func exportTaskTx(db queryRower, taskUUID string) (string, error) {
	var id, slug, title, state, description, specification string
	var priority int
	var startAt, dueAt, labels, meta, completedAt, archivedAt *string
	var createdAt, updatedAt string
	var etag int64
	var projectUUID string
	// created_by/updated_by principal refs are NULLABLE: a row created before
	// principal attribution (or by a path that recorded no principal) leaves these
	// NULL. Scanning into *string avoids a NULL→string scan crash on export.
	var createdByRef, updatedByRef *string

	err := db.QueryRow(`
		SELECT id, slug, title, project_uuid, state, priority,
		       start_at, due_at, labels, meta, description, specification, etag,
		       created_at, updated_at, completed_at, archived_at,
		       created_by_principal_ref, updated_by_principal_ref
		FROM tasks WHERE uuid = ?
	`, taskUUID).Scan(
		&id, &slug, &title, &projectUUID, &state, &priority,
		&startAt, &dueAt, &labels, &meta, &description, &specification, &etag,
		&createdAt, &updatedAt, &completedAt, &archivedAt,
		&createdByRef, &updatedByRef,
	)
	if err != nil {
		return "", fmt.Errorf("failed to get task: %w", err)
	}

	// Human-facing handles from the canonical principal refs (strip agent:
	// prefix; empty when no principal recorded).
	var createdBySlug, updatedBySlug string
	if createdByRef != nil {
		createdBySlug = attribution.PrincipalHandle(*createdByRef)
	}
	if updatedByRef != nil {
		updatedBySlug = attribution.PrincipalHandle(*updatedByRef)
	}

	// Get project info
	var projectID string
	_ = db.QueryRow("SELECT id FROM containers WHERE uuid = ?", projectUUID).Scan(&projectID)

	// Build frontmatter
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "id: %s\n", id)
	fmt.Fprintf(&sb, "uuid: %s\n", taskUUID)
	fmt.Fprintf(&sb, "project_id: %s\n", projectID)
	fmt.Fprintf(&sb, "project_uuid: %s\n", projectUUID)
	fmt.Fprintf(&sb, "slug: %s\n", slug)
	// Quote title if it contains special YAML characters
	fmt.Fprintf(&sb, "title: %s\n", quoteYAMLIfNeeded(title))
	fmt.Fprintf(&sb, "state: %s\n", state)
	fmt.Fprintf(&sb, "priority: %d\n", priority)
	if startAt != nil {
		fmt.Fprintf(&sb, "start_at: %s\n", *startAt)
	}
	if dueAt != nil {
		fmt.Fprintf(&sb, "due_at: %s\n", *dueAt)
	}
	if labels != nil && *labels != "" {
		fmt.Fprintf(&sb, "labels: %s\n", *labels)
	}
	if meta != nil && *meta != "" {
		fmt.Fprintf(&sb, "meta: %s\n", *meta)
	}
	if specification != "" {
		sb.WriteString("specification: |\n")
		for _, line := range strings.Split(specification, "\n") {
			sb.WriteString("  " + line + "\n")
		}
	}
	fmt.Fprintf(&sb, "etag: %d\n", etag)
	fmt.Fprintf(&sb, "created_at: %s\n", createdAt)
	fmt.Fprintf(&sb, "updated_at: %s\n", updatedAt)
	if completedAt != nil {
		fmt.Fprintf(&sb, "completed_at: %s\n", *completedAt)
	}
	if archivedAt != nil {
		fmt.Fprintf(&sb, "archived_at: %s\n", *archivedAt)
	}
	fmt.Fprintf(&sb, "created_by: %s\n", createdBySlug)
	fmt.Fprintf(&sb, "updated_by: %s\n", updatedBySlug)
	sb.WriteString("---\n\n")
	sb.WriteString(description)

	return sb.String(), nil
}

// addBundleFieldsToFrontmatter adds path and base_etag to the frontmatter
func addBundleFieldsToFrontmatter(content string, path string, baseEtag int) string {
	// Find the end of frontmatter
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return content
	}

	// parts[0] is empty (before first ---)
	// parts[1] is the frontmatter content (with leading/trailing newlines)
	// parts[2] is "\n\nbody" (starts with closing --- separator)
	frontmatter := strings.TrimSpace(parts[1])

	// Remove the leading "\n\n" from body (one newline from --- separator, one blank line)
	body := strings.TrimPrefix(parts[2], "\n\n")

	// Reconstruct with added fields
	// Format: ---\nfrontmatter\nbase_etag\npath\n---\n\nbody
	return fmt.Sprintf("---\n%s\nbase_etag: %d\npath: %s\n---\n\n%s", frontmatter, baseEtag, path, body)
}

// collectAttachmentDescriptors records, in legacy export order (tasks in their
// collect order, attachments per task in DB order), which attachment files WOULD
// be exported under --with-attachments. It returns DESCRIPTORS only (no bytes,
// no file copy). A missing config attach_dir means no attachments are configured
// and yields an empty descriptor set (legacy returned early without writing).
func collectAttachmentDescriptors(db queryRower, tasks []*TaskExport) ([]AttachmentDescriptor, error) {
	var attachDir string
	if err := db.QueryRow("SELECT value FROM config WHERE key = 'attach_dir'").Scan(&attachDir); err != nil {
		// No attachments configured.
		return nil, nil
	}

	var descriptors []AttachmentDescriptor
	for _, task := range tasks {
		rows, err := db.Query(`
			SELECT filename FROM attachments
			WHERE task_uuid = ? AND deleted_at IS NULL
		`, task.UUID)
		if err != nil {
			return nil, fmt.Errorf("failed to query attachments: %w", err)
		}
		for rows.Next() {
			var filename string
			if err := rows.Scan(&filename); err != nil {
				_ = rows.Close()
				return nil, err
			}
			descriptors = append(descriptors, AttachmentDescriptor{TaskUUID: task.UUID, Filename: filename})
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return descriptors, nil
}

// collectEvents reads the filtered event log (same filters + ordering as legacy
// exportEvents) into EventRow records. No file is written.
func collectEvents(db queryRower, opts CreateOptions) ([]EventRow, error) {
	rawSince := opts.SinceCursor
	if rawSince == "" {
		rawSince = opts.Since
	}
	sinceEventID, sinceTimestamp, _, err := parseSinceFilter(rawSince)
	if err != nil {
		return nil, err
	}

	query := `
		SELECT id, timestamp, principal_ref, resource_type, resource_uuid,
		       event_type, etag, payload
		FROM event_log
		WHERE 1=1
	`
	args := []interface{}{}

	// Apply same filters as main query
	if opts.Actor != "" {
		query += ` AND principal_ref = ?`
		args = append(args, opts.Actor)
	}
	if sinceEventID != nil {
		query += ` AND id > ?`
		args = append(args, *sinceEventID)
	}
	if sinceTimestamp != "" {
		query += ` AND timestamp >= ?`
		args = append(args, sinceTimestamp)
	}
	if opts.Until != "" {
		query += ` AND timestamp <= ?`
		args = append(args, opts.Until)
	}

	query += ` ORDER BY timestamp, id`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	events := []EventRow{}
	for rows.Next() {
		var event EventRow
		if err := rows.Scan(&event.ID, &event.Timestamp, &event.PrincipalRef,
			&event.ResourceType, &event.ResourceUUID, &event.EventType,
			&event.Etag, &event.Payload); err != nil {
			return nil, fmt.Errorf("failed to scan event: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// convertTaskExportsToTaskDocuments converts TaskExport slice to TaskDocument slice
func convertTaskExportsToTaskDocuments(exports []*TaskExport) []*TaskDocument {
	var docs []*TaskDocument
	for _, exp := range exports {
		docs = append(docs, &TaskDocument{
			Path:            exp.Path,
			BaseEtag:        exp.BaseEtag,
			UUID:            exp.UUID,
			OriginalContent: exp.Content,
		})
	}
	return docs
}

// sortContainersByDepth sorts containers by depth (parent before child) then alphabetically
func sortContainersByDepth(containers []string) {
	// Simple bubble sort by depth first, then alphabetically
	for i := 0; i < len(containers); i++ {
		for j := i + 1; j < len(containers); j++ {
			depthI := strings.Count(containers[i], "/")
			depthJ := strings.Count(containers[j], "/")

			// Sort by depth first
			if depthI > depthJ {
				containers[i], containers[j] = containers[j], containers[i]
			} else if depthI == depthJ && containers[i] > containers[j] {
				// Same depth, sort alphabetically
				containers[i], containers[j] = containers[j], containers[i]
			}
		}
	}
}

// quoteYAMLIfNeeded quotes a string if it contains special YAML characters
func quoteYAMLIfNeeded(s string) string {
	// If the string contains colons, quotes, or other special YAML characters, quote it
	needsQuoting := strings.ContainsAny(s, ":\"'[]{}#&*!|>@`")

	if needsQuoting {
		// Escape any double quotes in the string
		escaped := strings.ReplaceAll(s, "\"", "\\\"")
		return fmt.Sprintf("\"%s\"", escaped)
	}

	return s
}

func parseSinceFilter(raw string) (*int64, string, string, error) {
	if raw == "" {
		return nil, "", "", nil
	}

	if strings.HasPrefix(raw, "event:") {
		value := strings.TrimPrefix(raw, "event:")
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, "", "", fmt.Errorf("invalid since cursor %q: %w", raw, err)
		}
		return &id, "", raw, nil
	}

	if strings.HasPrefix(raw, "ts:") {
		value := strings.TrimPrefix(raw, "ts:")
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			return nil, "", "", fmt.Errorf("invalid since cursor %q: %w", raw, err)
		}
		return nil, value, raw, nil
	}

	// Treat as RFC3339 timestamp
	if _, err := time.Parse(time.RFC3339, raw); err != nil {
		return nil, "", "", fmt.Errorf("invalid since timestamp %q: %w", raw, err)
	}

	return nil, raw, "", nil
}

// collectRefs gathers ref/ stub TaskDocuments (logical only; OriginalContent holds
// the rendered stub). Materialize writes them to refs/<path>.md. The DISCOVERY set
// (relations out/in + parent task refs, excluding already-included tasks) and the
// stub rendering match legacy exportRefs exactly.
func collectRefs(db queryRower, tasks []*TaskExport) ([]*TaskDocument, error) {
	if len(tasks) == 0 {
		return nil, nil
	}

	included := make(map[string]bool, len(tasks))
	var uuids []string
	for _, task := range tasks {
		if task.UUID == "" {
			continue
		}
		included[task.UUID] = true
		uuids = append(uuids, task.UUID)
	}
	if len(uuids) == 0 {
		return nil, nil
	}

	refUUIDs := make(map[string]bool)

	placeholders := strings.TrimRight(strings.Repeat("?,", len(uuids)), ",")

	// Relations: outgoing
	queryOut := fmt.Sprintf(`SELECT DISTINCT to_task_uuid FROM task_relations WHERE from_task_uuid IN (%s)`, placeholders)
	rows, err := db.Query(queryOut, stringSliceToInterface(uuids)...)
	if err != nil {
		return nil, fmt.Errorf("failed to query relation refs: %w", err)
	}
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !included[uuid] {
			refUUIDs[uuid] = true
		}
	}
	_ = rows.Close()

	// Relations: incoming
	queryIn := fmt.Sprintf(`SELECT DISTINCT from_task_uuid FROM task_relations WHERE to_task_uuid IN (%s)`, placeholders)
	rows, err = db.Query(queryIn, stringSliceToInterface(uuids)...)
	if err != nil {
		return nil, fmt.Errorf("failed to query relation refs: %w", err)
	}
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !included[uuid] {
			refUUIDs[uuid] = true
		}
	}
	_ = rows.Close()

	// Parent task refs
	queryParent := fmt.Sprintf(`SELECT DISTINCT parent_task_uuid FROM tasks WHERE uuid IN (%s) AND parent_task_uuid IS NOT NULL`, placeholders)
	rows, err = db.Query(queryParent, stringSliceToInterface(uuids)...)
	if err != nil {
		return nil, fmt.Errorf("failed to query parent refs: %w", err)
	}
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !included[uuid] {
			refUUIDs[uuid] = true
		}
	}
	_ = rows.Close()

	if len(refUUIDs) == 0 {
		return nil, nil
	}

	// Deterministic order (legacy ranged a map → file-per-ref so order was
	// immaterial; the snapshot is a slice, so sort by uuid for a stable wire shape).
	orderedUUIDs := make([]string, 0, len(refUUIDs))
	for uuid := range refUUIDs {
		orderedUUIDs = append(orderedUUIDs, uuid)
	}
	sort.Strings(orderedUUIDs)

	var refs []*TaskDocument
	for _, uuid := range orderedUUIDs {
		content, refPath, err := exportRefStub(db, uuid)
		if err != nil {
			return nil, err
		}
		if refPath == "" {
			refPath = uuid
		}
		refs = append(refs, &TaskDocument{
			Path:            refPath,
			UUID:            uuid,
			OriginalContent: content,
		})
	}

	return refs, nil
}

func exportRefStub(db queryRower, taskUUID string) (string, string, error) {
	var (
		id            string
		slug          string
		title         string
		state         string
		priority      int
		projectUUID   string
		containerPath string
		projectID     string
	)

	err := db.QueryRow(`
		SELECT t.id, t.slug, t.title, t.state, t.priority, t.project_uuid,
		       cp.path
		FROM tasks t
		LEFT JOIN v_container_paths cp ON cp.uuid = t.project_uuid
		WHERE t.uuid = ?
	`, taskUUID).Scan(&id, &slug, &title, &state, &priority, &projectUUID, &containerPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to load ref task %s: %w", taskUUID, err)
	}

	_ = db.QueryRow("SELECT id FROM containers WHERE uuid = ?", projectUUID).Scan(&projectID)

	path := slug
	if containerPath != "" {
		path = containerPath + "/" + slug
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	if id != "" {
		fmt.Fprintf(&sb, "id: %s\n", id)
	}
	fmt.Fprintf(&sb, "uuid: %s\n", taskUUID)
	if projectID != "" {
		fmt.Fprintf(&sb, "project_id: %s\n", projectID)
	}
	fmt.Fprintf(&sb, "project_uuid: %s\n", projectUUID)
	fmt.Fprintf(&sb, "slug: %s\n", slug)
	fmt.Fprintf(&sb, "title: %s\n", quoteYAMLIfNeeded(title))
	fmt.Fprintf(&sb, "state: %s\n", state)
	fmt.Fprintf(&sb, "priority: %d\n", priority)
	fmt.Fprintf(&sb, "path: %s\n", path)
	sb.WriteString("ref: true\n")
	sb.WriteString("---\n")

	return sb.String(), path, nil
}

func stringSliceToInterface(values []string) []interface{} {
	args := make([]interface{}, len(values))
	for i, v := range values {
		args[i] = v
	}
	return args
}
