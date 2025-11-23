package bundle

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Manifest represents the bundle manifest.json structure
type Manifest struct {
	MachineInterfaceVersion int    `json:"machine_interface_version"`
	Version                 string `json:"version,omitempty"`
	Commit                  string `json:"commit,omitempty"`
	BuildDate               string `json:"build_date,omitempty"`
	Timestamp               string `json:"timestamp"`
	Actor                   string `json:"actor,omitempty"`
	Since                   string `json:"since,omitempty"`
	Until                   string `json:"until,omitempty"`
	WithAttachments         bool   `json:"with_attachments"`
	WithEvents              bool   `json:"with_events"`
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

	return &Bundle{
		Dir:        bundleDir,
		Manifest:   manifest,
		Containers: containers,
		Tasks:      tasks,
	}, nil
}

// CreateOptions specifies options for bundle creation
type CreateOptions struct {
	// Actor filter (UUID or slug)
	Actor string
	// Time window
	Since string
	Until string
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
	UUID      string
	Path      string
	BaseEtag  int
	Content   string // Full cat output including frontmatter
}

// Create creates a new bundle from database content
func Create(db *sql.DB, opts CreateOptions) (*Bundle, error) {
	// Create output directory structure
	if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create bundle directory: %w", err)
	}

	tasksDir := filepath.Join(opts.OutputDir, "tasks")
	if err := os.MkdirAll(tasksDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create tasks directory: %w", err)
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

	// Filter by actor
	if opts.Actor != "" {
		query += ` AND e.actor_uuid IN (SELECT uuid FROM actors WHERE uuid = ? OR slug = ?)`
		args = append(args, opts.Actor, opts.Actor)
	}

	// Filter by time window
	if opts.Since != "" {
		query += ` AND e.timestamp >= ?`
		args = append(args, opts.Since)
	}
	if opts.Until != "" {
		query += ` AND e.timestamp <= ?`
		args = append(args, opts.Until)
	}

	query += ` ORDER BY container_path, t.slug`

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query tasks: %w", err)
	}
	defer rows.Close()

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
		baseEtag, err := computeBaseEtag(db, taskUUID, opts)
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
		content, err := exportTask(db, taskUUID)
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

		// Write task file
		taskFilePath := filepath.Join(tasksDir, taskPath+".md")
		taskFileDir := filepath.Dir(taskFilePath)
		if err := os.MkdirAll(taskFileDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create task directory: %w", err)
		}
		if err := os.WriteFile(taskFilePath, []byte(content), 0644); err != nil {
			return nil, fmt.Errorf("failed to write task file: %w", err)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating tasks: %w", err)
	}

	// Write containers.txt (sorted to ensure parent-before-child order)
	var containers []string
	for container := range containerMap {
		containers = append(containers, container)
	}
	// Sort by depth (number of slashes) then alphabetically
	// This ensures parents come before children
	sortContainersByDepth(containers)

	if len(containers) > 0 {
		containersPath := filepath.Join(opts.OutputDir, "containers.txt")
		containersContent := strings.Join(containers, "\n") + "\n"
		if err := os.WriteFile(containersPath, []byte(containersContent), 0644); err != nil {
			return nil, fmt.Errorf("failed to write containers.txt: %w", err)
		}
	}

	// Copy attachments if requested
	if opts.WithAttachments {
		if err := exportAttachments(db, opts.OutputDir, tasks); err != nil {
			return nil, fmt.Errorf("failed to export attachments: %w", err)
		}
	}

	// Export event log if requested
	if opts.WithEvents {
		if err := exportEvents(db, opts.OutputDir, opts); err != nil {
			return nil, fmt.Errorf("failed to export events: %w", err)
		}
	}

	// Generate and write manifest
	manifest := &Manifest{
		MachineInterfaceVersion: 1,
		Version:                 opts.Version,
		Commit:                  opts.Commit,
		BuildDate:               opts.BuildDate,
		Timestamp:               time.Now().UTC().Format(time.RFC3339),
		Actor:                   opts.Actor,
		Since:                   opts.Since,
		Until:                   opts.Until,
		WithAttachments:         opts.WithAttachments,
		WithEvents:              opts.WithEvents,
	}

	manifestPath := filepath.Join(opts.OutputDir, "manifest.json")
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, manifestJSON, 0644); err != nil {
		return nil, fmt.Errorf("failed to write manifest: %w", err)
	}

	return &Bundle{
		Dir:        opts.OutputDir,
		Manifest:   manifest,
		Containers: containers,
		Tasks:      convertTaskExportsToTaskDocuments(tasks),
	}, nil
}

// computeBaseEtag computes the base etag for a task based on the earliest event in the filtered set
func computeBaseEtag(db *sql.DB, taskUUID string, opts CreateOptions) (int, error) {
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
		query += ` AND actor_uuid IN (SELECT uuid FROM actors WHERE uuid = ? OR slug = ?)`
		args = append(args, opts.Actor, opts.Actor)
	}
	if opts.Since != "" {
		query += ` AND timestamp >= ?`
		args = append(args, opts.Since)
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

// exportTask exports a single task in wrkq cat format
func exportTask(db *sql.DB, taskUUID string) (string, error) {
	var id, slug, title, state, body string
	var priority int
	var startAt, dueAt, labels, completedAt, archivedAt *string
	var createdAt, updatedAt string
	var etag int64
	var projectUUID, createdByUUID, updatedByUUID string

	err := db.QueryRow(`
		SELECT id, slug, title, project_uuid, state, priority,
		       start_at, due_at, labels, body, etag,
		       created_at, updated_at, completed_at, archived_at,
		       created_by_actor_uuid, updated_by_actor_uuid
		FROM tasks WHERE uuid = ?
	`, taskUUID).Scan(
		&id, &slug, &title, &projectUUID, &state, &priority,
		&startAt, &dueAt, &labels, &body, &etag,
		&createdAt, &updatedAt, &completedAt, &archivedAt,
		&createdByUUID, &updatedByUUID,
	)
	if err != nil {
		return "", fmt.Errorf("failed to get task: %w", err)
	}

	// Get actor slugs
	var createdBySlug, updatedBySlug string
	db.QueryRow("SELECT slug FROM actors WHERE uuid = ?", createdByUUID).Scan(&createdBySlug)
	db.QueryRow("SELECT slug FROM actors WHERE uuid = ?", updatedByUUID).Scan(&updatedBySlug)

	// Get project info
	var projectID string
	db.QueryRow("SELECT id FROM containers WHERE uuid = ?", projectUUID).Scan(&projectID)

	// Build frontmatter
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("id: %s\n", id))
	sb.WriteString(fmt.Sprintf("uuid: %s\n", taskUUID))
	sb.WriteString(fmt.Sprintf("project_id: %s\n", projectID))
	sb.WriteString(fmt.Sprintf("project_uuid: %s\n", projectUUID))
	sb.WriteString(fmt.Sprintf("slug: %s\n", slug))
	// Quote title if it contains special YAML characters
	sb.WriteString(fmt.Sprintf("title: %s\n", quoteYAMLIfNeeded(title)))
	sb.WriteString(fmt.Sprintf("state: %s\n", state))
	sb.WriteString(fmt.Sprintf("priority: %d\n", priority))
	if startAt != nil {
		sb.WriteString(fmt.Sprintf("start_at: %s\n", *startAt))
	}
	if dueAt != nil {
		sb.WriteString(fmt.Sprintf("due_at: %s\n", *dueAt))
	}
	if labels != nil && *labels != "" {
		sb.WriteString(fmt.Sprintf("labels: %s\n", *labels))
	}
	sb.WriteString(fmt.Sprintf("etag: %d\n", etag))
	sb.WriteString(fmt.Sprintf("created_at: %s\n", createdAt))
	sb.WriteString(fmt.Sprintf("updated_at: %s\n", updatedAt))
	if completedAt != nil {
		sb.WriteString(fmt.Sprintf("completed_at: %s\n", *completedAt))
	}
	if archivedAt != nil {
		sb.WriteString(fmt.Sprintf("archived_at: %s\n", *archivedAt))
	}
	sb.WriteString(fmt.Sprintf("created_by: %s\n", createdBySlug))
	sb.WriteString(fmt.Sprintf("updated_by: %s\n", updatedBySlug))
	sb.WriteString("---\n\n")
	sb.WriteString(body)

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
	body := parts[2]
	if strings.HasPrefix(body, "\n\n") {
		body = body[2:]
	}

	// Reconstruct with added fields
	// Format: ---\nfrontmatter\nbase_etag\npath\n---\n\nbody
	return fmt.Sprintf("---\n%s\nbase_etag: %d\npath: %s\n---\n\n%s", frontmatter, baseEtag, path, body)
}

// exportAttachments copies attachments for all tasks in the bundle
func exportAttachments(db *sql.DB, bundleDir string, tasks []*TaskExport) error {
	attachmentsDir := filepath.Join(bundleDir, "attachments")

	// Query to find attachment directory
	var attachDir string
	err := db.QueryRow("SELECT value FROM config WHERE key = 'attach_dir'").Scan(&attachDir)
	if err != nil {
		// No attachments configured
		return nil
	}

	for _, task := range tasks {
		// Check if task has attachments
		var hasAttachments bool
		err := db.QueryRow(`
			SELECT EXISTS(SELECT 1 FROM attachments WHERE task_uuid = ? AND deleted_at IS NULL)
		`, task.UUID).Scan(&hasAttachments)
		if err != nil {
			return fmt.Errorf("failed to check attachments for task %s: %w", task.UUID, err)
		}

		if !hasAttachments {
			continue
		}

		// Create attachments/<task_uuid>/ directory
		taskAttachDir := filepath.Join(attachmentsDir, task.UUID)
		if err := os.MkdirAll(taskAttachDir, 0755); err != nil {
			return fmt.Errorf("failed to create attachment directory: %w", err)
		}

		// Copy attachment files
		rows, err := db.Query(`
			SELECT filename FROM attachments
			WHERE task_uuid = ? AND deleted_at IS NULL
		`, task.UUID)
		if err != nil {
			return fmt.Errorf("failed to query attachments: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var filename string
			if err := rows.Scan(&filename); err != nil {
				return err
			}

			srcPath := filepath.Join(attachDir, "tasks", task.UUID, filename)
			dstPath := filepath.Join(taskAttachDir, filename)

			if err := copyFile(srcPath, dstPath); err != nil {
				return fmt.Errorf("failed to copy attachment %s: %w", filename, err)
			}
		}

		if err := rows.Err(); err != nil {
			return err
		}
	}

	return nil
}

// exportEvents exports the event log as NDJSON
func exportEvents(db *sql.DB, bundleDir string, opts CreateOptions) error {
	query := `
		SELECT id, timestamp, actor_uuid, resource_type, resource_uuid,
		       event_type, etag, payload
		FROM event_log
		WHERE 1=1
	`
	args := []interface{}{}

	// Apply same filters as main query
	if opts.Actor != "" {
		query += ` AND actor_uuid IN (SELECT uuid FROM actors WHERE uuid = ? OR slug = ?)`
		args = append(args, opts.Actor, opts.Actor)
	}
	if opts.Since != "" {
		query += ` AND timestamp >= ?`
		args = append(args, opts.Since)
	}
	if opts.Until != "" {
		query += ` AND timestamp <= ?`
		args = append(args, opts.Until)
	}

	query += ` ORDER BY timestamp, id`

	rows, err := db.Query(query, args...)
	if err != nil {
		return fmt.Errorf("failed to query events: %w", err)
	}
	defer rows.Close()

	eventsPath := filepath.Join(bundleDir, "events.ndjson")
	f, err := os.Create(eventsPath)
	if err != nil {
		return fmt.Errorf("failed to create events file: %w", err)
	}
	defer f.Close()

	encoder := json.NewEncoder(f)

	for rows.Next() {
		var event struct {
			ID           int             `json:"id"`
			Timestamp    string          `json:"timestamp"`
			ActorUUID    *string         `json:"actor_uuid"`
			ResourceType string          `json:"resource_type"`
			ResourceUUID string          `json:"resource_uuid"`
			EventType    string          `json:"event_type"`
			Etag         *int            `json:"etag"`
			Payload      *string         `json:"payload,omitempty"`
		}

		if err := rows.Scan(&event.ID, &event.Timestamp, &event.ActorUUID,
			&event.ResourceType, &event.ResourceUUID, &event.EventType,
			&event.Etag, &event.Payload); err != nil {
			return fmt.Errorf("failed to scan event: %w", err)
		}

		if err := encoder.Encode(event); err != nil {
			return fmt.Errorf("failed to encode event: %w", err)
		}
	}

	return rows.Err()
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	return dstFile.Sync()
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
