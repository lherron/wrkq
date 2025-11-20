package cli

import (
	"encoding/json"
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
	"github.com/spf13/cobra"
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
	bundleApplyFrom         string
	bundleApplyDryRun       bool
	bundleApplyContinue     bool
	bundleApplyJSON         bool
	bundleApplyPorcelain    bool
)

type applyResult struct {
	Success         bool     `json:"success"`
	ContainersAdded int      `json:"containers_added"`
	TasksApplied    int      `json:"tasks_applied"`
	TasksFailed     int      `json:"tasks_failed"`
	AttachmentsAdded int     `json:"attachments_added"`
	Conflicts       []string `json:"conflicts,omitempty"`
	Errors          []string `json:"errors,omitempty"`
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
	var actorUUID string
	actorIdentifier := cmd.Flag("as").Value.String()
	if actorIdentifier == "" {
		actorIdentifier = cfg.GetActorID()
	}
	if actorIdentifier != "" {
		resolver := actors.NewResolver(database.DB)
		actorUUID, err = resolver.Resolve(actorIdentifier)
		if err != nil {
			return fmt.Errorf("failed to resolve actor: %w", err)
		}
	} else {
		return fmt.Errorf("no actor configured (set WRKQ_ACTOR, WRKQ_ACTOR_ID, or use --as flag)")
	}

	// Step 1: Ensure all containers exist (mkdir -p pattern)
	if !bundleApplyDryRun {
		for _, containerPath := range b.Containers {
			if err := ensureContainer(database, actorUUID, containerPath); err != nil {
				if !bundleApplyContinue {
					return fmt.Errorf("failed to create container %s: %w", containerPath, err)
				}
				result.Errors = append(result.Errors, fmt.Sprintf("container %s: %v", containerPath, err))
				result.Success = false
			} else {
				result.ContainersAdded++
			}
		}
	} else {
		result.ContainersAdded = len(b.Containers)
	}

	// Step 2: Apply each task document
	for _, task := range b.Tasks {
		if err := applyTaskDocument(cmd, cfg, task, bundleApplyDryRun); err != nil {
			result.TasksFailed++
			result.Success = false

			// Check if it's a conflict (etag mismatch)
			if isConflictError(err) {
				result.Conflicts = append(result.Conflicts, task.Path)
			} else {
				result.Errors = append(result.Errors, fmt.Sprintf("task %s: %v", task.Path, err))
			}

			if !bundleApplyContinue {
				if bundleApplyJSON {
					encoder := json.NewEncoder(cmd.OutOrStdout())
					encoder.SetIndent("", "  ")
					encoder.Encode(result)
				}
				return fmt.Errorf("failed to apply task %s: %w", task.Path, err)
			}
		} else {
			result.TasksApplied++
		}
	}

	// Step 3: Re-attach files from attachments/<task_uuid>/
	if !bundleApplyDryRun && b.Manifest.WithAttachments {
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
		for _, path := range result.Conflicts {
			fmt.Fprintf(cmd.OutOrStderr(), "  - %s\n", path)
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
func ensureContainer(database *db.DB, actorUUID string, path string) error {
	// Split path into segments
	segments := strings.Split(path, "/")

	var parentUUID *string
	currentPath := ""

	for _, slug := range segments {
		if currentPath != "" {
			currentPath += "/"
		}
		currentPath += slug

		// Check if container exists
		var uuid string
		query := `
			SELECT uuid FROM containers
			WHERE slug = ? AND (
				(parent_uuid IS NULL AND ? IS NULL) OR
				(parent_uuid = ?)
			)
		`
		err := database.QueryRow(query, slug, parentUUID, parentUUID).Scan(&uuid)

		if err != nil {
			// Container doesn't exist, create it
			// Generate UUID
			newUUID := generateUUID()

			// Generate title from slug (capitalize first letter)
			title := strings.ToUpper(slug[:1]) + slug[1:]

			insertQuery := `
				INSERT INTO containers (uuid, slug, title, parent_uuid, created_by_actor_uuid, updated_by_actor_uuid)
				VALUES (?, ?, ?, ?, ?, ?)
			`
			_, err = database.Exec(insertQuery, newUUID, slug, title, parentUUID, actorUUID, actorUUID)
			if err != nil {
				return fmt.Errorf("failed to create container %s: %w", currentPath, err)
			}
			uuid = newUUID
		}

		// Update parent for next iteration
		parentUUID = &uuid
	}

	return nil
}

// applyTaskDocument applies a single task document using wrkq apply
func applyTaskDocument(cmd *cobra.Command, cfg *config.Config, task *bundle.TaskDocument, dryRun bool) error {
	// Create temporary file with task content
	tmpFile, err := os.CreateTemp("", "wrkq-bundle-*.md")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write task document to temp file (use original content if available, otherwise just body)
	content := task.OriginalContent
	if content == "" {
		content = task.Body
	}
	if _, err := tmpFile.WriteString(content); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	if dryRun {
		// For dry-run, just validate the document can be read
		return nil
	}

	// Prepare environment for wrkq commands
	env := os.Environ()
	env = append(env, "WRKQ_DB_PATH="+cfg.DBPath)
	actorIdentifier := cfg.GetActorID()
	if actorIdentifier != "" {
		env = append(env, "WRKQ_ACTOR="+actorIdentifier)
	}

	// Determine selector: prefer UUID, fallback to path
	// For tasks without UUID, we need to create them first
	selector := ""
	needsCreation := false

	if task.UUID != "" {
		// Try UUID-based selector first
		selector = "t:" + task.UUID
	} else if task.Path != "" {
		// No UUID, use path and mark that we need to create it
		selector = task.Path
		needsCreation = true
	} else {
		return fmt.Errorf("task has no UUID or path")
	}

	// Create task if needed (for tasks without UUID)
	if needsCreation {
		touchCmd := exec.Command("wrkq", "touch", task.Path)
		touchCmd.Env = env

		output, err := touchCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("wrkq touch failed: %w\nOutput: %s", err, output)
		}
	}

	args := []string{"apply", selector, tmpFile.Name()}

	// Add --if-match if base_etag is present AND task was not just created
	// (newly created tasks start with etag=1, so we shouldn't check base_etag)
	if task.BaseEtag > 0 && !needsCreation {
		args = append(args, "--if-match", fmt.Sprintf("%d", task.BaseEtag))
	}

	// Execute wrkq apply
	applyCmd := exec.Command("wrkq", args...)
	applyCmd.Env = env

	output, err := applyCmd.CombinedOutput()
	if err != nil {
		// Check exit code for conflict (4)
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 4 {
				return &conflictError{
					taskPath: task.Path,
					message:  string(output),
				}
			}
		}

		// If UUID-based selector failed with "task not found", fall back to path-based application
		if task.UUID != "" && task.Path != "" && strings.Contains(string(output), "task not found") {
			// Try to create task using touch with the path
			touchCmd := exec.Command("wrkq", "touch", task.Path)
			touchCmd.Env = env

			touchOutput, touchErr := touchCmd.CombinedOutput()
			// Ignore UNIQUE constraint errors - it means task already exists at that path
			if touchErr != nil && !strings.Contains(string(touchOutput), "UNIQUE constraint") {
				return fmt.Errorf("wrkq touch failed after UUID fallback: %w\nOutput: %s", touchErr, touchOutput)
			}

			// Retry apply with path-based selector WITHOUT --if-match (task was just created)
			retryArgs := []string{"apply", task.Path, tmpFile.Name()}
			retryCmd := exec.Command("wrkq", retryArgs...)
			retryCmd.Env = env

			retryOutput, retryErr := retryCmd.CombinedOutput()
			if retryErr != nil {
				return fmt.Errorf("wrkq apply failed after fallback: %w\nOutput: %s", retryErr, retryOutput)
			}

			return nil
		}

		return fmt.Errorf("wrkq apply failed: %w\nOutput: %s", err, output)
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
	taskPath string
	message  string
}

func (e *conflictError) Error() string {
	return fmt.Sprintf("conflict in task %s: %s", e.taskPath, e.message)
}

func isConflictError(err error) bool {
	_, ok := err.(*conflictError)
	return ok
}

// generateUUID generates a UUID v4
func generateUUID() string {
	return uuid.New().String()
}
