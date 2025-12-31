package cli

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/bundle"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var bundleAdmCmd = &cobra.Command{
	Use:   "bundle",
	Short: "Bundle operations for Git-ops workflow",
	Long:  `Commands for applying PR bundles into the canonical database. Part of the Git-ops workflow.`,
}

var bundleApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply a bundle into the current database",
	Long: `Apply a PR bundle into the canonical database with conflict detection.

Reads manifest.json, ensures containers exist, applies task documents with
etag checking, and re-hydrates attachments. Exit code 4 on conflicts.`,
	RunE: runBundleApply,
}

var (
	bundleApplyFrom      string
	bundleApplyDryRun    bool
	bundleApplyContinue  bool
	bundleApplyJSON      bool
	bundleApplyPorcelain bool
)

type applyResult struct {
	Success          bool            `json:"success"`
	ContainersAdded  int             `json:"containers_added"`
	TasksApplied     int             `json:"tasks_applied"`
	TasksFailed      int             `json:"tasks_failed"`
	AttachmentsAdded int             `json:"attachments_added"`
	Conflicts        []applyConflict `json:"conflicts,omitempty"`
	Errors           []string        `json:"errors,omitempty"`
}

type applyConflict struct {
	Path            string                      `json:"path"`
	UUID            string                      `json:"uuid,omitempty"`
	Reason          string                      `json:"reason"`
	ExpectedETag    int64                       `json:"expected_etag,omitempty"`
	ActualETag      int64                       `json:"actual_etag,omitempty"`
	FieldChanges    map[string]applyFieldChange `json:"field_changes,omitempty"`
	DescriptionDiff string                      `json:"description_diff,omitempty"`
	Message         string                      `json:"message,omitempty"`
}

type applyFieldChange struct {
	Current  interface{} `json:"current,omitempty"`
	Incoming interface{} `json:"incoming,omitempty"`
}

func init() {
	rootAdmCmd.AddCommand(bundleAdmCmd)
	bundleAdmCmd.AddCommand(bundleApplyCmd)

	bundleApplyCmd.Flags().StringVar(&bundleApplyFrom, "from", ".wrkq", "Bundle directory path")
	bundleApplyCmd.Flags().BoolVar(&bundleApplyDryRun, "dry-run", false, "Validate without writing")
	bundleApplyCmd.Flags().BoolVar(&bundleApplyContinue, "continue-on-error", false, "Continue after errors")
	bundleApplyCmd.Flags().BoolVar(&bundleApplyJSON, "json", false, "Output as JSON")
	bundleApplyCmd.Flags().BoolVar(&bundleApplyPorcelain, "porcelain", false, "Machine-readable output")
}

func runBundleApply(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Override DB path from flag if provided
	if dbPath := cmd.Flag("db").Value.String(); dbPath != "" {
		cfg.DBPath = dbPath
	}

	// Validate database exists
	if _, err := os.Stat(cfg.DBPath); err != nil {
		return fmt.Errorf("database not found: %w", err)
	}

	// Validate bundle directory exists
	if _, err := os.Stat(bundleApplyFrom); err != nil {
		return fmt.Errorf("bundle directory not found: %w", err)
	}

	// Load bundle
	b, err := bundle.Load(bundleApplyFrom)
	if err != nil {
		return fmt.Errorf("failed to load bundle: %w", err)
	}

	// Validate machine interface version
	currentVersion := 1 // This should match the version in version.go
	if b.Manifest.MachineInterfaceVersion != currentVersion {
		return fmt.Errorf("bundle machine_interface_version (%d) doesn't match current version (%d)",
			b.Manifest.MachineInterfaceVersion, currentVersion)
	}

	result := &applyResult{
		Success: true,
	}

	// Open database to ensure containers
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Resolve actor for container creation
	// Bundle apply should attribute changes to the actor who applied the bundle
	actorUUID, err := resolveBundleActor(database, cmd, cfg)
	if err != nil {
		return err
	}

	if bundleApplyContinue {
		// Non-transactional apply (partial mode)
		for _, containerPath := range b.Containers {
			created, err := ensureContainer(database, actorUUID, containerPath, bundleApplyDryRun)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("container %s: %v", containerPath, err))
				result.Success = false
				continue
			}
			if created {
				result.ContainersAdded++
			}
		}

		for _, task := range b.Tasks {
			if err := applyTaskDocumentWithDB(database, actorUUID, task, bundleApplyDryRun); err != nil {
				result.TasksFailed++
				result.Success = false
				if conflict := conflictFromError(err); conflict != nil {
					result.Conflicts = append(result.Conflicts, *conflict)
				} else {
					result.Errors = append(result.Errors, fmt.Sprintf("task %s: %v", task.Path, err))
				}
				continue
			} else {
				result.TasksApplied++
			}
		}
	} else {
		// Transactional apply (all-or-nothing)
		tx, err := database.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer tx.Rollback()

		ew := events.NewWriter(database.DB)

		for _, containerPath := range b.Containers {
			created, err := ensureContainerTx(tx, ew, actorUUID, containerPath, bundleApplyDryRun)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("container %s: %v", containerPath, err))
				result.Success = false
				if bundleApplyJSON {
					encoder := json.NewEncoder(cmd.OutOrStdout())
					encoder.SetIndent("", "  ")
					_ = encoder.Encode(result)
				}
				return fmt.Errorf("failed to create container %s: %w", containerPath, err)
			}
			if created {
				result.ContainersAdded++
			}
		}

		for _, task := range b.Tasks {
			if err := applyTaskDocumentTx(tx, ew, actorUUID, task, bundleApplyDryRun); err != nil {
				result.TasksFailed++
				result.Success = false
				if conflict := conflictFromError(err); conflict != nil {
					result.Conflicts = append(result.Conflicts, *conflict)
				} else {
					result.Errors = append(result.Errors, fmt.Sprintf("task %s: %v", task.Path, err))
				}
				if bundleApplyJSON {
					encoder := json.NewEncoder(cmd.OutOrStdout())
					encoder.SetIndent("", "  ")
					_ = encoder.Encode(result)
				}
				return fmt.Errorf("failed to apply task %s: %w", task.Path, err)
			}
			result.TasksApplied++
		}

		if !bundleApplyDryRun {
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("failed to commit bundle apply: %w", err)
			}
		}
	}

	// Step 3: Re-attach files from attachments/<task_uuid>/
	if !bundleApplyDryRun && b.Manifest.WithAttachments && result.Success {
		attachmentsDir := filepath.Join(b.Dir, "attachments")
		if _, err := os.Stat(attachmentsDir); err == nil {
			attached, err := reattachFiles(cmd, cfg, attachmentsDir)
			if err != nil {
				if !bundleApplyContinue {
					return fmt.Errorf("failed to reattach files: %w", err)
				}
				result.Errors = append(result.Errors, fmt.Sprintf("attachments: %v", err))
				result.Success = false
			}
			result.AttachmentsAdded = attached
		}
	}

	// Output results
	if bundleApplyJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	if bundleApplyPorcelain {
		fmt.Fprintf(cmd.OutOrStdout(), "%d\t%d\t%d\t%d\n",
			result.ContainersAdded, result.TasksApplied, result.TasksFailed, result.AttachmentsAdded)
		return nil
	}

	// Human-readable output
	if bundleApplyDryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "Dry run - no changes made\n")
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✓ Bundle applied successfully\n")
	fmt.Fprintf(cmd.OutOrStdout(), "  Containers: %d\n", result.ContainersAdded)
	fmt.Fprintf(cmd.OutOrStdout(), "  Tasks applied: %d\n", result.TasksApplied)
	if result.TasksFailed > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  Tasks failed: %d\n", result.TasksFailed)
	}
	if result.AttachmentsAdded > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  Attachments: %d\n", result.AttachmentsAdded)
	}

	if len(result.Conflicts) > 0 {
		fmt.Fprintf(cmd.OutOrStderr(), "\nConflicts detected:\n")
		for _, conflict := range result.Conflicts {
			fmt.Fprintf(cmd.OutOrStderr(), "  - %s (%s)\n", conflict.Path, conflict.Reason)
		}
		if !bundleApplyJSON {
			os.Exit(4)
		}
		return fmt.Errorf("conflicts detected, use 'wrkq diff' to resolve")
	}

	if len(result.Errors) > 0 {
		fmt.Fprintf(cmd.OutOrStderr(), "\nErrors:\n")
		for _, errMsg := range result.Errors {
			fmt.Fprintf(cmd.OutOrStderr(), "  - %s\n", errMsg)
		}
	}

	if !result.Success {
		if len(result.Conflicts) > 0 {
			os.Exit(4) // Conflict exit code
		}
		return fmt.Errorf("bundle apply completed with errors")
	}

	return nil
}

// ensureContainer creates a container hierarchy if it doesn't exist (mkdir -p)
func ensureContainer(database *db.DB, actorUUID string, path string, dryRun bool) (bool, error) {
	tx, err := database.Begin()
	if err != nil {
		return false, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	created, err := ensureContainerTx(tx, events.NewWriter(database.DB), actorUUID, path, dryRun)
	if err != nil {
		return false, err
	}

	if dryRun {
		return created, nil
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("failed to commit container creation: %w", err)
	}

	return created, nil
}

func ensureContainerTx(tx *sql.Tx, ew *events.Writer, actorUUID string, path string, dryRun bool) (bool, error) {
	segments := paths.SplitPath(path)
	var parentUUID *string
	createdAny := false

	for _, segment := range segments {
		slug, err := paths.NormalizeSlug(segment)
		if err != nil {
			return createdAny, fmt.Errorf("invalid container slug %q: %w", segment, err)
		}

		query := `
			SELECT uuid FROM containers
			WHERE slug = ? AND (
				(parent_uuid IS NULL AND ? IS NULL) OR
				(parent_uuid = ?)
			)
		`

		var uuid string
		err = tx.QueryRow(query, slug, parentUUID, parentUUID).Scan(&uuid)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return createdAny, fmt.Errorf("failed to query container %s: %w", slug, err)
			}

			createdAny = true
			if dryRun {
				newUUID := generateUUID()
				parentUUID = &newUUID
				continue
			}

			title := strings.ToUpper(slug[:1]) + slug[1:]
			res, err := tx.Exec(`
				INSERT INTO containers (id, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
				VALUES ('', ?, ?, ?, 'project', ?, ?)
			`, slug, title, parentUUID, actorUUID, actorUUID)
			if err != nil {
				return createdAny, fmt.Errorf("failed to create container %s: %w", slug, err)
			}

			rowID, err := res.LastInsertId()
			if err != nil {
				return createdAny, fmt.Errorf("failed to get container row id: %w", err)
			}

			var etag int64
			if err := tx.QueryRow("SELECT uuid, etag FROM containers WHERE rowid = ?", rowID).Scan(&uuid, &etag); err != nil {
				return createdAny, fmt.Errorf("failed to fetch created container: %w", err)
			}

			payload, _ := json.Marshal(map[string]interface{}{
				"slug":  slug,
				"title": title,
				"kind":  "project",
			})
			payloadStr := string(payload)

			if err := ew.LogEvent(tx, &domain.Event{
				ActorUUID:    &actorUUID,
				ResourceType: "container",
				ResourceUUID: &uuid,
				EventType:    "container.created",
				ETag:         &etag,
				Payload:      &payloadStr,
			}); err != nil {
				return createdAny, fmt.Errorf("failed to log container event: %w", err)
			}
		}

		parentUUID = &uuid
	}

	return createdAny, nil
}

func resolveBundleActor(database *db.DB, cmd *cobra.Command, cfg *config.Config) (string, error) {
	actorIdentifier := cmd.Flag("as").Value.String()
	if actorIdentifier == "" {
		actorIdentifier = cfg.GetActorID()
	}
	if actorIdentifier == "" {
		return "", fmt.Errorf("no actor configured (set WRKQ_ACTOR, WRKQ_ACTOR_ID, or use --as flag)")
	}

	resolver := actors.NewResolver(database.DB)
	actorUUID, err := resolver.Resolve(actorIdentifier)
	if err == nil {
		return actorUUID, nil
	}

	normalized, normErr := paths.NormalizeSlug(actorIdentifier)
	if normErr != nil {
		return "", fmt.Errorf("failed to resolve actor: %w", err)
	}

	actor, createErr := resolver.Create(normalized, "", "agent")
	if createErr != nil {
		return "", fmt.Errorf("failed to resolve actor: %w", err)
	}

	return actor.UUID, nil
}

type bundleTaskUpdate struct {
	Title       *string
	State       *string
	Priority    *int
	DueAt       *string
	StartAt     *string
	Labels      *string
	Description *string
}

type bundleTaskCurrent struct {
	UUID        string
	ID          string
	Slug        string
	Title       string
	Description string
	State       string
	Priority    int
	DueAt       *string
	StartAt     *string
	Labels      *string
	ETag        int64
	ProjectUUID string
}

func applyTaskDocumentWithDB(database *db.DB, actorUUID string, task *bundle.TaskDocument, dryRun bool) error {
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if err := applyTaskDocumentTx(tx, events.NewWriter(database.DB), actorUUID, task, dryRun); err != nil {
		return err
	}

	if dryRun {
		return nil
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit task apply: %w", err)
	}

	return nil
}

func applyTaskDocumentTx(tx *sql.Tx, ew *events.Writer, actorUUID string, task *bundle.TaskDocument, dryRun bool) error {
	content := task.OriginalContent
	if content == "" {
		content = task.Description
	}

	update, err := parseBundleTaskContent(content)
	if err != nil {
		return err
	}

	if update.State != nil {
		if err := domain.ValidateState(*update.State); err != nil {
			return err
		}
	}
	if update.Priority != nil {
		if err := domain.ValidatePriority(*update.Priority); err != nil {
			return err
		}
	}

	var current *bundleTaskCurrent
	var taskUUID string

	switch {
	case task.UUID != "":
		taskUUID = task.UUID
		current, err = fetchTaskCurrentTx(tx, taskUUID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if errors.Is(err, sql.ErrNoRows) {
			if task.Path != "" {
				existingUUID, _, err := resolveTaskByPathTx(tx, task.Path)
				if err == nil && existingUUID != "" {
					conflict := buildConflictDetail(task, nil, update, "uuid_mismatch", int64(task.BaseEtag), 0)
					return &conflictError{detail: conflict}
				}
			}
			return createTaskTx(tx, ew, actorUUID, task, update, dryRun)
		}
	case task.Path != "":
		taskUUID, _, err = resolveTaskByPathTx(tx, task.Path)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if errors.Is(err, sql.ErrNoRows) {
			return createTaskTx(tx, ew, actorUUID, task, update, dryRun)
		}
		current, err = fetchTaskCurrentTx(tx, taskUUID)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("task has no UUID or path")
	}

	if current == nil {
		return fmt.Errorf("failed to resolve task %s", task.Path)
	}

	if task.BaseEtag > 0 && current.ETag != int64(task.BaseEtag) {
		conflict := buildConflictDetail(task, current, update, "etag_mismatch", int64(task.BaseEtag), current.ETag)
		return &conflictError{detail: conflict}
	}

	return updateTaskTx(tx, ew, actorUUID, current, update, dryRun)
}

func parseBundleTaskContent(content string) (*bundleTaskUpdate, error) {
	update := &bundleTaskUpdate{}
	frontmatter, body, err := splitFrontmatter(content)
	if err != nil {
		return nil, err
	}

	if frontmatter != "" {
		var fm map[string]interface{}
		if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
			return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
		}

		if v, ok := fm["title"].(string); ok && v != "" {
			update.Title = &v
		}
		if v, ok := fm["state"].(string); ok && v != "" {
			update.State = &v
		}
		if v, ok := fm["priority"]; ok {
			if p, ok := coerceInt(v); ok {
				update.Priority = &p
			}
		}
		if v, ok := fm["due_at"].(string); ok && v != "" {
			update.DueAt = &v
		}
		if v, ok := fm["start_at"].(string); ok && v != "" {
			update.StartAt = &v
		}
		if v, ok := fm["labels"]; ok {
			switch labels := v.(type) {
			case string:
				if labels != "" {
					update.Labels = &labels
				}
			case []interface{}:
				if data, err := json.Marshal(labels); err == nil {
					labelStr := string(data)
					update.Labels = &labelStr
				}
			}
		}
	}

	body = strings.TrimSpace(body)
	if body != "" {
		update.Description = &body
	}

	return update, nil
}

func splitFrontmatter(content string) (string, string, error) {
	if !strings.HasPrefix(content, "---\n") {
		return "", content, nil
	}

	parts := strings.SplitN(content[4:], "\n---\n", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid frontmatter format")
	}

	return parts[0], parts[1], nil
}

func coerceInt(value interface{}) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}

func fetchTaskCurrentTx(tx *sql.Tx, taskUUID string) (*bundleTaskCurrent, error) {
	var current bundleTaskCurrent
	var dueAt, startAt, labels sql.NullString

	err := tx.QueryRow(`
		SELECT uuid, id, slug, title, description, state, priority, due_at, start_at, labels, etag, project_uuid
		FROM tasks WHERE uuid = ?
	`, taskUUID).Scan(
		&current.UUID, &current.ID, &current.Slug, &current.Title, &current.Description, &current.State,
		&current.Priority, &dueAt, &startAt, &labels, &current.ETag, &current.ProjectUUID,
	)
	if err != nil {
		return nil, err
	}

	if dueAt.Valid {
		current.DueAt = &dueAt.String
	}
	if startAt.Valid {
		current.StartAt = &startAt.String
	}
	if labels.Valid {
		current.Labels = &labels.String
	}

	return &current, nil
}

func resolveTaskByPathTx(tx *sql.Tx, path string) (string, string, error) {
	segments := paths.SplitPath(path)
	if len(segments) == 0 {
		return "", "", fmt.Errorf("invalid path: empty")
	}

	var parentUUID *string
	if len(segments) > 1 {
		parentPath := paths.JoinPath(segments[:len(segments)-1]...)
		uuid, err := walkContainerPathTx(tx, parentPath)
		if err != nil {
			return "", "", err
		}
		parentUUID = &uuid
	}

	normalizedSlug, err := paths.NormalizeSlug(segments[len(segments)-1])
	if err != nil {
		return "", "", fmt.Errorf("invalid task slug %q: %w", segments[len(segments)-1], err)
	}

	var taskUUID, taskID string
	if parentUUID == nil {
		err = tx.QueryRow(`
			SELECT uuid, id FROM tasks WHERE slug = ? AND project_uuid IN (
				SELECT uuid FROM containers WHERE parent_uuid IS NULL
			) LIMIT 1
		`, normalizedSlug).Scan(&taskUUID, &taskID)
	} else {
		err = tx.QueryRow(`
			SELECT uuid, id FROM tasks WHERE slug = ? AND project_uuid = ?
		`, normalizedSlug, *parentUUID).Scan(&taskUUID, &taskID)
	}
	if err != nil {
		return "", "", err
	}

	return taskUUID, taskID, nil
}

func walkContainerPathTx(tx *sql.Tx, path string) (string, error) {
	segments := paths.SplitPath(path)
	if len(segments) == 0 {
		return "", fmt.Errorf("invalid path: empty")
	}

	var currentUUID *string
	for _, segment := range segments {
		slug, err := paths.NormalizeSlug(segment)
		if err != nil {
			return "", fmt.Errorf("invalid slug %q: %w", segment, err)
		}

		query := `SELECT uuid FROM containers WHERE slug = ? AND `
		args := []interface{}{slug}
		if currentUUID == nil {
			query += `parent_uuid IS NULL`
		} else {
			query += `parent_uuid = ?`
			args = append(args, *currentUUID)
		}

		var uuid string
		if err := tx.QueryRow(query, args...).Scan(&uuid); err != nil {
			return "", err
		}
		currentUUID = &uuid
	}

	return *currentUUID, nil
}

func resolveParentContainerTx(tx *sql.Tx, path string) (*string, string, error) {
	segments := paths.SplitPath(path)
	if len(segments) == 0 {
		return nil, "", fmt.Errorf("invalid path: empty")
	}

	slug, err := paths.NormalizeSlug(segments[len(segments)-1])
	if err != nil {
		return nil, "", fmt.Errorf("invalid slug %q: %w", segments[len(segments)-1], err)
	}

	if len(segments) == 1 {
		return nil, slug, nil
	}

	parentPath := paths.JoinPath(segments[:len(segments)-1]...)
	parentUUID, err := walkContainerPathTx(tx, parentPath)
	if err != nil {
		return nil, "", err
	}

	return &parentUUID, slug, nil
}

func createTaskTx(tx *sql.Tx, ew *events.Writer, actorUUID string, task *bundle.TaskDocument, update *bundleTaskUpdate, dryRun bool) error {
	if dryRun {
		return nil
	}

	parentUUID, slug, err := resolveParentContainerTx(tx, task.Path)
	if err != nil {
		return err
	}

	projectUUID := ""
	if parentUUID != nil {
		projectUUID = *parentUUID
	} else {
		if err := tx.QueryRow(`SELECT uuid FROM containers WHERE parent_uuid IS NULL LIMIT 1`).Scan(&projectUUID); err != nil {
			return fmt.Errorf("no root container found for %s", task.Path)
		}
	}

	title := slug
	if update.Title != nil && *update.Title != "" {
		title = *update.Title
	}

	state := "open"
	if update.State != nil && *update.State != "" {
		state = *update.State
	}

	priority := 3
	if update.Priority != nil && *update.Priority > 0 {
		priority = *update.Priority
	}

	description := ""
	if update.Description != nil {
		description = *update.Description
	}

	labels := interface{}(nil)
	if update.Labels != nil {
		labels = *update.Labels
	}

	dueAt := interface{}(nil)
	if update.DueAt != nil {
		dueAt = *update.DueAt
	}

	startAt := interface{}(nil)
	if update.StartAt != nil {
		startAt = *update.StartAt
	}

	var (
		res    sql.Result
		errIns error
	)

	if task.UUID != "" {
		res, errIns = tx.Exec(`
			INSERT INTO tasks (
				uuid, id, slug, title, description, project_uuid, state, priority, kind,
				labels, due_at, start_at, created_by_actor_uuid, updated_by_actor_uuid
			) VALUES (?, '', ?, ?, ?, ?, ?, ?, 'task', ?, ?, ?, ?, ?)
		`, task.UUID, slug, title, description, projectUUID, state, priority, labels, dueAt, startAt, actorUUID, actorUUID)
	} else {
		res, errIns = tx.Exec(`
			INSERT INTO tasks (
				id, slug, title, description, project_uuid, state, priority, kind,
				labels, due_at, start_at, created_by_actor_uuid, updated_by_actor_uuid
			) VALUES ('', ?, ?, ?, ?, ?, ?, 'task', ?, ?, ?, ?, ?)
		`, slug, title, description, projectUUID, state, priority, labels, dueAt, startAt, actorUUID, actorUUID)
	}
	if errIns != nil {
		return fmt.Errorf("failed to create task %s: %w", task.Path, errIns)
	}

	rowID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get task row id: %w", err)
	}

	var uuid, id string
	var etag int64
	if err := tx.QueryRow("SELECT uuid, id, etag FROM tasks WHERE rowid = ?", rowID).Scan(&uuid, &id, &etag); err != nil {
		return fmt.Errorf("failed to fetch created task: %w", err)
	}

	payload := map[string]interface{}{
		"slug":     slug,
		"title":    title,
		"state":    state,
		"priority": priority,
		"kind":     "task",
	}
	payloadJSON, _ := json.Marshal(payload)
	payloadStr := string(payloadJSON)

	if err := ew.LogEvent(tx, &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "task",
		ResourceUUID: &uuid,
		EventType:    "task.created",
		ETag:         &etag,
		Payload:      &payloadStr,
	}); err != nil {
		return fmt.Errorf("failed to log task.created: %w", err)
	}

	_ = id
	return nil
}

func updateTaskTx(tx *sql.Tx, ew *events.Writer, actorUUID string, current *bundleTaskCurrent, update *bundleTaskUpdate, dryRun bool) error {
	fields := map[string]interface{}{}

	if update.Title != nil {
		fields["title"] = *update.Title
	}
	if update.State != nil {
		fields["state"] = *update.State
	}
	if update.Priority != nil {
		fields["priority"] = *update.Priority
	}
	if update.DueAt != nil {
		fields["due_at"] = *update.DueAt
	}
	if update.StartAt != nil {
		fields["start_at"] = *update.StartAt
	}
	if update.Labels != nil {
		fields["labels"] = *update.Labels
	}
	if update.Description != nil {
		fields["description"] = *update.Description
	}

	if len(fields) == 0 {
		return nil
	}

	if dryRun {
		return nil
	}

	var setClauses []string
	var args []interface{}
	for key, value := range fields {
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", key))
		args = append(args, value)
	}

	setClauses = append(setClauses, "etag = etag + 1")
	setClauses = append(setClauses, "updated_by_actor_uuid = ?")
	args = append(args, actorUUID)
	args = append(args, current.UUID)

	query := fmt.Sprintf("UPDATE tasks SET %s WHERE uuid = ?", strings.Join(setClauses, ", "))
	if _, err := tx.Exec(query, args...); err != nil {
		return fmt.Errorf("failed to update task %s: %w", current.UUID, err)
	}

	newETag := current.ETag + 1
	payloadJSON, _ := json.Marshal(fields)
	payloadStr := string(payloadJSON)

	if err := ew.LogEvent(tx, &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "task",
		ResourceUUID: &current.UUID,
		EventType:    "task.updated",
		ETag:         &newETag,
		Payload:      &payloadStr,
	}); err != nil {
		return fmt.Errorf("failed to log task.updated: %w", err)
	}

	if update.State != nil && *update.State == "deleted" {
		if err := cascadeDeleteSubtasksTx(tx, ew, actorUUID, current.UUID); err != nil {
			return err
		}
	}

	return nil
}

func cascadeDeleteSubtasksTx(tx *sql.Tx, ew *events.Writer, actorUUID, parentTaskUUID string) error {
	rows, err := tx.Query(`
		SELECT uuid FROM tasks
		WHERE parent_task_uuid = ? AND state != 'deleted'
	`, parentTaskUUID)
	if err != nil {
		return fmt.Errorf("failed to query subtasks: %w", err)
	}
	defer rows.Close()

	var subtaskUUIDs []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return fmt.Errorf("failed to scan subtask: %w", err)
		}
		subtaskUUIDs = append(subtaskUUIDs, uuid)
	}

	for _, subtaskUUID := range subtaskUUIDs {
		if _, err := tx.Exec(`
			UPDATE tasks
			SET state = 'deleted',
			    updated_by_actor_uuid = ?
			WHERE uuid = ?
		`, actorUUID, subtaskUUID); err != nil {
			return fmt.Errorf("failed to delete subtask %s: %w", subtaskUUID, err)
		}

		payload := `{"action":"cascade_deleted","parent_deleted":true}`
		if err := ew.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "task",
			ResourceUUID: &subtaskUUID,
			EventType:    "task.deleted",
			Payload:      &payload,
		}); err != nil {
			return fmt.Errorf("failed to log subtask delete: %w", err)
		}

		if err := cascadeDeleteSubtasksTx(tx, ew, actorUUID, subtaskUUID); err != nil {
			return err
		}
	}

	return nil
}

func buildConflictDetail(task *bundle.TaskDocument, current *bundleTaskCurrent, update *bundleTaskUpdate, reason string, expectedETag int64, actualETag int64) applyConflict {
	conflict := applyConflict{
		Path:         task.Path,
		UUID:         task.UUID,
		Reason:       reason,
		ExpectedETag: expectedETag,
		ActualETag:   actualETag,
	}

	if current == nil || update == nil {
		return conflict
	}

	if conflict.UUID == "" {
		conflict.UUID = current.UUID
	}

	changes := map[string]applyFieldChange{}

	if update.Title != nil && current.Title != *update.Title {
		changes["title"] = applyFieldChange{Current: current.Title, Incoming: *update.Title}
	}
	if update.State != nil && current.State != *update.State {
		changes["state"] = applyFieldChange{Current: current.State, Incoming: *update.State}
	}
	if update.Priority != nil && current.Priority != *update.Priority {
		changes["priority"] = applyFieldChange{Current: current.Priority, Incoming: *update.Priority}
	}
	if update.DueAt != nil && (current.DueAt == nil || *current.DueAt != *update.DueAt) {
		currentVal := ""
		if current.DueAt != nil {
			currentVal = *current.DueAt
		}
		changes["due_at"] = applyFieldChange{Current: currentVal, Incoming: *update.DueAt}
	}
	if update.StartAt != nil && (current.StartAt == nil || *current.StartAt != *update.StartAt) {
		currentVal := ""
		if current.StartAt != nil {
			currentVal = *current.StartAt
		}
		changes["start_at"] = applyFieldChange{Current: currentVal, Incoming: *update.StartAt}
	}
	if update.Labels != nil && (current.Labels == nil || *current.Labels != *update.Labels) {
		currentVal := ""
		if current.Labels != nil {
			currentVal = *current.Labels
		}
		changes["labels"] = applyFieldChange{Current: currentVal, Incoming: *update.Labels}
	}

	if update.Description != nil && current.Description != *update.Description {
		diff := difflib.UnifiedDiff{
			A:        difflib.SplitLines(current.Description),
			B:        difflib.SplitLines(*update.Description),
			FromFile: "current",
			ToFile:   "incoming",
			Context:  3,
		}
		if diffText, err := difflib.GetUnifiedDiffString(diff); err == nil {
			conflict.DescriptionDiff = diffText
		}
	}

	if len(changes) > 0 {
		conflict.FieldChanges = changes
	}

	return conflict
}

func conflictFromError(err error) *applyConflict {
	var conflictErr *conflictError
	if errors.As(err, &conflictErr) {
		return &conflictErr.detail
	}
	return nil
}

// reattachFiles re-attaches files from the bundle's attachments directory
func reattachFiles(cmd *cobra.Command, cfg *config.Config, attachmentsDir string) (int, error) {
	count := 0

	// Walk through attachments/<task_uuid>/ directories
	entries, err := os.ReadDir(attachmentsDir)
	if err != nil {
		return 0, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		taskUUID := entry.Name()
		taskAttachDir := filepath.Join(attachmentsDir, taskUUID)

		// Walk through files in this task's attachment directory
		files, err := os.ReadDir(taskAttachDir)
		if err != nil {
			return count, fmt.Errorf("failed to read %s: %w", taskAttachDir, err)
		}

		for _, file := range files {
			if file.IsDir() {
				continue
			}

			filePath := filepath.Join(taskAttachDir, file.Name())

			// Execute wrkq attach put
			attachCmd := exec.Command("wrkq", "attach", "put", "t:"+taskUUID, filePath)
			attachCmd.Env = os.Environ()
			attachCmd.Env = append(attachCmd.Env, "WRKQ_DB_PATH="+cfg.DBPath)
			actorIdentifier := cfg.GetActorID()
			if actorIdentifier != "" {
				attachCmd.Env = append(attachCmd.Env, "WRKQ_ACTOR="+actorIdentifier)
			}

			output, err := attachCmd.CombinedOutput()
			if err != nil {
				return count, fmt.Errorf("wrkq attach put failed for %s: %w\nOutput: %s",
					file.Name(), err, output)
			}

			count++
		}
	}

	return count, nil
}

// conflictError represents an etag mismatch or merge conflict
type conflictError struct {
	detail applyConflict
}

func (e *conflictError) Error() string {
	if e.detail.Message != "" {
		return e.detail.Message
	}
	return fmt.Sprintf("conflict in task %s (%s)", e.detail.Path, e.detail.Reason)
}

// generateUUID generates a UUID v4
func generateUUID() string {
	return uuid.New().String()
}
