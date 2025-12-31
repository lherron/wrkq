package cli

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lherron/wrkq/internal/attach"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

var mergeAdmCmd = &cobra.Command{
	Use:   "merge",
	Short: "Merge a project database into a canonical database",
	Long: `Merge a per-project wrkq database into a canonical database.

This imports containers, tasks, comments, relations, and attachments from a
source database into a destination database under a project path prefix.
UUIDs are preserved and conflicts are resolved by favoring the newest record.
Use --dry-run to validate and emit a report without writing.`,
	RunE: runMergeAdm,
}

var (
	mergeSourceDB      string
	mergeDestDB        string
	mergeProject       string
	mergePathPrefix    string
	mergeReportPath    string
	mergeDryRun        bool
	mergeSrcAttachDir  string
	mergeDestAttachDir string
)

func init() {
	rootAdmCmd.AddCommand(mergeAdmCmd)

	mergeAdmCmd.Flags().StringVar(&mergeSourceDB, "source", "", "Source database path")
	mergeAdmCmd.Flags().StringVar(&mergeDestDB, "dest", "", "Destination database path (overrides --db)")
	mergeAdmCmd.Flags().StringVar(&mergeProject, "project", "", "Source project selector (slug, path, ID, or UUID)")
	mergeAdmCmd.Flags().StringVar(&mergePathPrefix, "path-prefix", "", "Destination path prefix override")
	mergeAdmCmd.Flags().BoolVar(&mergeDryRun, "dry-run", false, "Validate without writing")
	mergeAdmCmd.Flags().StringVar(&mergeReportPath, "report", "", "Write JSON report to path")
	mergeAdmCmd.Flags().StringVar(&mergeSrcAttachDir, "source-attach-dir", "", "Source attachments directory (defaults to WRKQ_ATTACH_DIR)")
	mergeAdmCmd.Flags().StringVar(&mergeDestAttachDir, "dest-attach-dir", "", "Destination attachments directory (defaults to WRKQ_ATTACH_DIR)")
}

func runMergeAdm(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return exitError(1, fmt.Errorf("failed to load config: %w", err))
	}

	if mergeSourceDB == "" {
		return exitError(2, fmt.Errorf("source database path not specified (use --source)"))
	}

	destPath := mergeDestDB
	if destPath == "" {
		dbFlag := cmd.Flag("db").Value.String()
		if dbFlag != "" {
			destPath = dbFlag
		} else {
			destPath = cfg.DBPath
		}
	}
	if destPath == "" {
		return exitError(2, fmt.Errorf("destination database path not specified (use --dest or --db or set WRKQ_DB_PATH)"))
	}

	if mergeProject == "" {
		return exitError(2, fmt.Errorf("project selector not specified (use --project)"))
	}

	srcDB, err := db.Open(mergeSourceDB)
	if err != nil {
		return exitError(1, fmt.Errorf("failed to open source database: %w", err))
	}
	defer srcDB.Close()

	destDB, err := db.Open(destPath)
	if err != nil {
		return exitError(1, fmt.Errorf("failed to open destination database: %w", err))
	}
	defer destDB.Close()

	if err := ensureMigrationsReady(srcDB, "source", false, mergeDryRun); err != nil {
		return exitError(1, err)
	}
	if err := ensureMigrationsReady(destDB, "destination", !mergeDryRun, mergeDryRun); err != nil {
		return exitError(1, err)
	}

	if !mergeDryRun {
		if _, err := destDB.MigrateWithInfo(); err != nil {
			return exitError(1, fmt.Errorf("failed to migrate destination database: %w", err))
		}
	}

	actorUUID, err := resolveBundleActor(destDB, cmd, cfg)
	if err != nil {
		return exitError(1, fmt.Errorf("failed to resolve actor: %w", err))
	}

	attachDir := cfg.AttachDir
	if mergeDestAttachDir != "" {
		attachDir = mergeDestAttachDir
	}
	srcAttachDir := attachDir
	if mergeSrcAttachDir != "" {
		srcAttachDir = mergeSrcAttachDir
	}

	opts := mergeOptions{
		SourceDB:        srcDB,
		DestDB:          destDB,
		SourceAttachDir: srcAttachDir,
		DestAttachDir:   attachDir,
		ProjectSelector: mergeProject,
		PathPrefix:      mergePathPrefix,
		DryRun:          mergeDryRun,
		ActorUUID:       actorUUID,
	}

	report, err := mergeProjectIntoCanonical(opts)
	if err != nil {
		return exitError(1, err)
	}

	if mergeReportPath != "" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return exitError(1, fmt.Errorf("failed to encode report: %w", err))
		}
		if err := os.WriteFile(mergeReportPath, data, 0644); err != nil {
			return exitError(1, fmt.Errorf("failed to write report: %w", err))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "✓ Report written to %s\n", mergeReportPath)
	}

	printMergeSummary(cmd, report)

	return nil
}

func ensureMigrationsReady(database *db.DB, label string, allowPending bool, dryRun bool) error {
	_, pending, err := database.MigrationStatus()
	if err != nil {
		return fmt.Errorf("failed to check %s migration status: %w", label, err)
	}
	if len(pending) == 0 {
		return nil
	}
	if !allowPending || dryRun {
		return fmt.Errorf("%s database has pending migrations; run wrkqadm migrate --db %s", label, database.Path())
	}
	return nil
}

type mergeOptions struct {
	SourceDB        *db.DB
	DestDB          *db.DB
	SourceAttachDir string
	DestAttachDir   string
	ProjectSelector string
	PathPrefix      string
	DryRun          bool
	ActorUUID       string
}

type mergeReport struct {
	SourceDB           string          `json:"source_db"`
	DestDB             string          `json:"dest_db"`
	ProjectSelector    string          `json:"project_selector"`
	SourceProjectUUID  string          `json:"source_project_uuid"`
	SourceProjectPath  string          `json:"source_project_path"`
	DestPrefix         string          `json:"dest_prefix"`
	DryRun             bool            `json:"dry_run"`
	Stats              mergeStats      `json:"stats"`
	Renames            []mergeRename   `json:"renames,omitempty"`
	Conflicts          []mergeConflict `json:"conflicts,omitempty"`
	ActorMismatches    []actorMismatch `json:"actor_mismatches,omitempty"`
	Warnings           []string        `json:"warnings,omitempty"`
	AttachmentWarnings []string        `json:"attachment_warnings,omitempty"`
}

type mergeStats struct {
	Actors       mergeCounts `json:"actors"`
	Sections     mergeCounts `json:"sections"`
	Containers   mergeCounts `json:"containers"`
	Tasks        mergeCounts `json:"tasks"`
	Comments     mergeCounts `json:"comments"`
	Relations    mergeCounts `json:"relations"`
	Attachments  mergeCounts `json:"attachments"`
	FilesCopied  int         `json:"files_copied"`
	FilesMissing int         `json:"files_missing"`
}

type mergeCounts struct {
	Seen      int `json:"seen"`
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Skipped   int `json:"skipped"`
	Renamed   int `json:"renamed"`
	Conflicts int `json:"conflicts"`
	Deduped   int `json:"deduped"`
}

type mergeRename struct {
	Entity string `json:"entity"`
	UUID   string `json:"uuid"`
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

type mergeConflict struct {
	Entity   string `json:"entity"`
	UUID     string `json:"uuid"`
	Field    string `json:"field"`
	Source   string `json:"source"`
	Dest     string `json:"dest"`
	Resolved string `json:"resolved"`
}

type actorMismatch struct {
	Slug       string `json:"slug"`
	SourceUUID string `json:"source_uuid"`
	DestUUID   string `json:"dest_uuid"`
	SourceRole string `json:"source_role"`
	DestRole   string `json:"dest_role"`
}

func mergeProjectIntoCanonical(opts mergeOptions) (*mergeReport, error) {
	projectUUID, _, err := selectors.ResolveContainer(opts.SourceDB, opts.ProjectSelector)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve source project: %w", err)
	}

	var sourceProjectPath string
	if err := opts.SourceDB.QueryRow("SELECT path FROM v_container_paths WHERE uuid = ?", projectUUID).Scan(&sourceProjectPath); err != nil {
		return nil, fmt.Errorf("failed to resolve source project path: %w", err)
	}

	destPrefix, err := resolveDestPrefix(opts.ProjectSelector, opts.PathPrefix, sourceProjectPath)
	if err != nil {
		return nil, err
	}

	report := &mergeReport{
		SourceDB:          opts.SourceDB.Path(),
		DestDB:            opts.DestDB.Path(),
		ProjectSelector:   opts.ProjectSelector,
		SourceProjectUUID: projectUUID,
		SourceProjectPath: sourceProjectPath,
		DestPrefix:        destPrefix,
		DryRun:            opts.DryRun,
	}

	sourceData, err := loadSourceData(opts.SourceDB, projectUUID, sourceProjectPath)
	if err != nil {
		return nil, err
	}

	actorUUIDs := collectActorUUIDs(sourceData)
	actors, err := loadSourceActors(opts.SourceDB, actorUUIDs)
	if err != nil {
		return nil, err
	}
	sourceData.Actors = actors

	var tx *sql.Tx
	if !opts.DryRun {
		tx, err = opts.DestDB.Begin()
		if err != nil {
			return nil, fmt.Errorf("failed to begin destination transaction: %w", err)
		}
		defer tx.Rollback()
	}

	writer := events.NewWriter(opts.DestDB.DB)
	exec := newMergeExecutor(opts.DestDB, tx)

	actorMap, err := mergeActors(exec, writer, opts.ActorUUID, sourceData.Actors, report, opts.DryRun)
	if err != nil {
		return nil, err
	}

	prefixParentUUID, prefixParentPath, err := ensurePrefixChain(exec, writer, opts.ActorUUID, destPrefix, report, opts.DryRun)
	if err != nil {
		return nil, err
	}

	containerMap := make(map[string]string)
	containerPath := make(map[string]string)

	if err := mergeContainers(exec, writer, opts.ActorUUID, sourceData, sourceProjectPath, destPrefix, prefixParentUUID, prefixParentPath, actorMap, containerMap, containerPath, report, opts.DryRun); err != nil {
		return nil, err
	}

	sectionMap, err := mergeSections(exec, writer, opts.ActorUUID, sourceData.Sections, projectUUID, containerMap[projectUUID], actorMap, report, opts.DryRun)
	if err != nil {
		return nil, err
	}

	if err := applySectionRefs(exec, sourceData.Containers, sectionMap, containerMap, report, opts.DryRun); err != nil {
		return nil, err
	}

	taskMap, err := mergeTasks(exec, writer, opts.ActorUUID, sourceData.Tasks, containerMap, actorMap, report, opts.DryRun)
	if err != nil {
		return nil, err
	}

	if err := mergeComments(exec, writer, opts.ActorUUID, sourceData.Comments, taskMap, actorMap, report, opts.DryRun); err != nil {
		return nil, err
	}

	if err := mergeRelations(exec, writer, opts.ActorUUID, sourceData.Relations, taskMap, actorMap, report, opts.DryRun); err != nil {
		return nil, err
	}

	fileCopies, err := mergeAttachments(exec, writer, opts.ActorUUID, sourceData.Attachments, taskMap, actorMap, report, opts.DryRun)
	if err != nil {
		return nil, err
	}

	if !opts.DryRun {
		if _, err := db.FixSequenceDrifts(exec, db.DefaultSequenceSpecs()); err != nil {
			return nil, fmt.Errorf("failed to sync sequences: %w", err)
		}
		if err := syncCommentSequence(exec); err != nil {
			return nil, fmt.Errorf("failed to sync comment sequence: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("failed to commit merge: %w", err)
		}

		copied, missing, warnings := performFileCopies(fileCopies, opts.SourceAttachDir, opts.DestAttachDir)
		report.Stats.FilesCopied = copied
		report.Stats.FilesMissing = missing
		report.AttachmentWarnings = append(report.AttachmentWarnings, warnings...)
	}

	return report, nil
}

type mergeExecutor struct {
	db *db.DB
	tx *sql.Tx
}

func newMergeExecutor(database *db.DB, tx *sql.Tx) *mergeExecutor {
	return &mergeExecutor{db: database, tx: tx}
}

func (e *mergeExecutor) Exec(query string, args ...any) (sql.Result, error) {
	if e.tx != nil {
		return e.tx.Exec(query, args...)
	}
	return e.db.Exec(query, args...)
}

func (e *mergeExecutor) Query(query string, args ...any) (*sql.Rows, error) {
	if e.tx != nil {
		return e.tx.Query(query, args...)
	}
	return e.db.Query(query, args...)
}

func (e *mergeExecutor) QueryRow(query string, args ...any) *sql.Row {
	if e.tx != nil {
		return e.tx.QueryRow(query, args...)
	}
	return e.db.QueryRow(query, args...)
}

func resolveDestPrefix(selector, override, sourcePath string) (string, error) {
	prefix := override
	if prefix == "" {
		if looksLikePath(selector) && !looksLikeFriendlyID(selector) && !looksLikeUUID(selector) {
			prefix = selector
		} else {
			prefix = sourcePath
		}
	}
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return "", fmt.Errorf("path prefix cannot be empty")
	}
	segments := paths.SplitPath(prefix)
	for _, seg := range segments {
		if err := paths.ValidateSlug(seg); err != nil {
			return "", fmt.Errorf("invalid path prefix segment %q: %w", seg, err)
		}
	}
	return prefix, nil
}

func looksLikePath(value string) bool {
	return strings.Contains(value, "/")
}

func looksLikeFriendlyID(value string) bool {
	return strings.HasPrefix(value, "P-")
}

func looksLikeUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func printMergeSummary(cmd *cobra.Command, report *mergeReport) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Merge %s -> %s\n", report.SourceDB, report.DestDB)
	fmt.Fprintf(out, "Project: %s (%s)\n", report.ProjectSelector, report.SourceProjectPath)
	fmt.Fprintf(out, "Prefix: %s\n", report.DestPrefix)
	if report.DryRun {
		fmt.Fprintln(out, "Mode: dry-run")
	}
	fmt.Fprintf(out, "Actors: %d created, %d updated, %d skipped\n", report.Stats.Actors.Created, report.Stats.Actors.Updated, report.Stats.Actors.Skipped)
	fmt.Fprintf(out, "Containers: %d created, %d updated, %d renamed, %d skipped\n", report.Stats.Containers.Created, report.Stats.Containers.Updated, report.Stats.Containers.Renamed, report.Stats.Containers.Skipped)
	fmt.Fprintf(out, "Tasks: %d created, %d updated, %d renamed, %d skipped\n", report.Stats.Tasks.Created, report.Stats.Tasks.Updated, report.Stats.Tasks.Renamed, report.Stats.Tasks.Skipped)
	fmt.Fprintf(out, "Comments: %d created, %d updated, %d skipped\n", report.Stats.Comments.Created, report.Stats.Comments.Updated, report.Stats.Comments.Skipped)
	fmt.Fprintf(out, "Relations: %d created, %d skipped\n", report.Stats.Relations.Created, report.Stats.Relations.Skipped)
	fmt.Fprintf(out, "Attachments: %d created, %d deduped, %d skipped\n", report.Stats.Attachments.Created, report.Stats.Attachments.Deduped, report.Stats.Attachments.Skipped)
	if !report.DryRun {
		fmt.Fprintf(out, "Files copied: %d, missing: %d\n", report.Stats.FilesCopied, report.Stats.FilesMissing)
	}
	if len(report.Renames) > 0 {
		fmt.Fprintf(out, "Renames: %d\n", len(report.Renames))
	}
	if len(report.Conflicts) > 0 {
		fmt.Fprintf(out, "Conflicts: %d\n", len(report.Conflicts))
	}
	if len(report.ActorMismatches) > 0 {
		fmt.Fprintf(out, "Actor mismatches: %d\n", len(report.ActorMismatches))
	}
}

// -----------------------------------------------------------------------------
// Source loading
// -----------------------------------------------------------------------------

type sourceData struct {
	Containers  []sourceContainer
	Tasks       []sourceTask
	Comments    []sourceComment
	Relations   []sourceRelation
	Attachments []sourceAttachment
	Sections    []sourceSection
	Actors      []sourceActor
}

type sourceContainer struct {
	UUID        string
	ID          sql.NullString
	Slug        string
	Title       string
	Description string
	ParentUUID  sql.NullString
	Kind        string
	SectionUUID sql.NullString
	SortIndex   int
	ETag        int64
	CreatedAt   string
	UpdatedAt   string
	ArchivedAt  sql.NullString
	CreatedBy   string
	UpdatedBy   string
	Path        string
}

type sourceTask struct {
	UUID           string
	ID             sql.NullString
	Slug           string
	Title          string
	ProjectUUID    string
	State          string
	Priority       int
	Kind           string
	ParentTaskUUID sql.NullString
	AssigneeUUID   sql.NullString
	StartAt        sql.NullString
	DueAt          sql.NullString
	Labels         sql.NullString
	Description    string
	ETag           int64
	CreatedAt      string
	UpdatedAt      string
	CompletedAt    sql.NullString
	ArchivedAt     sql.NullString
	DeletedAt      sql.NullString
	CreatedBy      string
	UpdatedBy      string
}

type sourceComment struct {
	UUID          string
	ID            string
	TaskUUID      string
	ActorUUID     string
	Body          string
	Meta          sql.NullString
	ETag          int64
	CreatedAt     string
	UpdatedAt     sql.NullString
	DeletedAt     sql.NullString
	DeletedByUUID sql.NullString
}

type sourceRelation struct {
	FromTaskUUID string
	ToTaskUUID   string
	Kind         string
	Meta         sql.NullString
	CreatedAt    string
	CreatedBy    string
}

type sourceAttachment struct {
	UUID      string
	ID        sql.NullString
	TaskUUID  string
	Filename  string
	RelPath   string
	MimeType  sql.NullString
	SizeBytes int64
	Checksum  sql.NullString
	CreatedAt string
	CreatedBy sql.NullString
}

type sourceSection struct {
	UUID        string
	ID          sql.NullString
	ProjectUUID string
	Slug        string
	Title       string
	OrderIndex  int
	Role        string
	IsDefault   bool
	WIPLimit    sql.NullInt64
	Meta        sql.NullString
	CreatedAt   string
	UpdatedAt   string
	ArchivedAt  sql.NullString
}

type sourceActor struct {
	UUID        string
	ID          sql.NullString
	Slug        string
	DisplayName sql.NullString
	Role        string
	Meta        sql.NullString
	CreatedAt   string
	UpdatedAt   string
}

func loadSourceData(database *db.DB, projectUUID, projectPath string) (*sourceData, error) {
	data := &sourceData{}
	pathLike := projectPath + "/%"

	containers, err := database.Query(`
		SELECT c.uuid, c.id, c.slug, c.title, c.description, c.parent_uuid, c.kind,
		       c.section_uuid, c.sort_index, c.etag, c.created_at, c.updated_at,
		       c.archived_at, c.created_by_actor_uuid, c.updated_by_actor_uuid, v.path
		FROM containers c
		JOIN v_container_paths v ON v.uuid = c.uuid
		WHERE v.path = ? OR v.path LIKE ?
	`, projectPath, pathLike)
	if err != nil {
		return nil, fmt.Errorf("failed to query source containers: %w", err)
	}
	defer containers.Close()

	for containers.Next() {
		var c sourceContainer
		if err := containers.Scan(&c.UUID, &c.ID, &c.Slug, &c.Title, &c.Description, &c.ParentUUID,
			&c.Kind, &c.SectionUUID, &c.SortIndex, &c.ETag, &c.CreatedAt, &c.UpdatedAt,
			&c.ArchivedAt, &c.CreatedBy, &c.UpdatedBy, &c.Path); err != nil {
			return nil, fmt.Errorf("failed to scan source container: %w", err)
		}
		data.Containers = append(data.Containers, c)
	}
	if err := containers.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate containers: %w", err)
	}

	tasks, err := database.Query(`
		SELECT t.uuid, t.id, t.slug, t.title, t.project_uuid, t.state, t.priority, t.kind,
		       t.parent_task_uuid, t.assignee_actor_uuid, t.start_at, t.due_at, t.labels,
		       t.description, t.etag, t.created_at, t.updated_at, t.completed_at,
		       t.archived_at, t.deleted_at, t.created_by_actor_uuid, t.updated_by_actor_uuid
		FROM tasks t
		JOIN v_container_paths v ON v.uuid = t.project_uuid
		WHERE v.path = ? OR v.path LIKE ?
	`, projectPath, pathLike)
	if err != nil {
		return nil, fmt.Errorf("failed to query source tasks: %w", err)
	}
	defer tasks.Close()

	for tasks.Next() {
		var t sourceTask
		if err := tasks.Scan(&t.UUID, &t.ID, &t.Slug, &t.Title, &t.ProjectUUID, &t.State,
			&t.Priority, &t.Kind, &t.ParentTaskUUID, &t.AssigneeUUID, &t.StartAt,
			&t.DueAt, &t.Labels, &t.Description, &t.ETag, &t.CreatedAt, &t.UpdatedAt,
			&t.CompletedAt, &t.ArchivedAt, &t.DeletedAt, &t.CreatedBy, &t.UpdatedBy); err != nil {
			return nil, fmt.Errorf("failed to scan source task: %w", err)
		}
		data.Tasks = append(data.Tasks, t)
	}
	if err := tasks.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate tasks: %w", err)
	}

	comments, err := database.Query(`
		SELECT c.uuid, c.id, c.task_uuid, c.actor_uuid, c.body, c.meta, c.etag, c.created_at,
		       c.updated_at, c.deleted_at, c.deleted_by_actor_uuid
		FROM comments c
		JOIN tasks t ON t.uuid = c.task_uuid
		JOIN v_container_paths v ON v.uuid = t.project_uuid
		WHERE v.path = ? OR v.path LIKE ?
	`, projectPath, pathLike)
	if err != nil {
		return nil, fmt.Errorf("failed to query source comments: %w", err)
	}
	defer comments.Close()

	for comments.Next() {
		var c sourceComment
		if err := comments.Scan(&c.UUID, &c.ID, &c.TaskUUID, &c.ActorUUID, &c.Body, &c.Meta,
			&c.ETag, &c.CreatedAt, &c.UpdatedAt, &c.DeletedAt, &c.DeletedByUUID); err != nil {
			return nil, fmt.Errorf("failed to scan source comment: %w", err)
		}
		data.Comments = append(data.Comments, c)
	}
	if err := comments.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate comments: %w", err)
	}

	relations, err := database.Query(`
		SELECT r.from_task_uuid, r.to_task_uuid, r.kind, r.meta, r.created_at, r.created_by_actor_uuid
		FROM task_relations r
		JOIN tasks t ON t.uuid = r.from_task_uuid
		JOIN v_container_paths v ON v.uuid = t.project_uuid
		WHERE v.path = ? OR v.path LIKE ?
	`, projectPath, pathLike)
	if err != nil {
		return nil, fmt.Errorf("failed to query source relations: %w", err)
	}
	defer relations.Close()

	for relations.Next() {
		var r sourceRelation
		if err := relations.Scan(&r.FromTaskUUID, &r.ToTaskUUID, &r.Kind, &r.Meta, &r.CreatedAt, &r.CreatedBy); err != nil {
			return nil, fmt.Errorf("failed to scan source relation: %w", err)
		}
		data.Relations = append(data.Relations, r)
	}
	if err := relations.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate relations: %w", err)
	}

	attachments, err := database.Query(`
		SELECT a.uuid, a.id, a.task_uuid, a.filename, a.relative_path, a.mime_type,
		       a.size_bytes, a.checksum, a.created_at, a.created_by_actor_uuid
		FROM attachments a
		JOIN tasks t ON t.uuid = a.task_uuid
		JOIN v_container_paths v ON v.uuid = t.project_uuid
		WHERE v.path = ? OR v.path LIKE ?
	`, projectPath, pathLike)
	if err != nil {
		return nil, fmt.Errorf("failed to query source attachments: %w", err)
	}
	defer attachments.Close()

	for attachments.Next() {
		var a sourceAttachment
		if err := attachments.Scan(&a.UUID, &a.ID, &a.TaskUUID, &a.Filename, &a.RelPath,
			&a.MimeType, &a.SizeBytes, &a.Checksum, &a.CreatedAt, &a.CreatedBy); err != nil {
			return nil, fmt.Errorf("failed to scan source attachment: %w", err)
		}
		data.Attachments = append(data.Attachments, a)
	}
	if err := attachments.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate attachments: %w", err)
	}

	sections, err := database.Query(`
		SELECT uuid, id, project_uuid, slug, title, order_index, role, is_default,
		       wip_limit, meta, created_at, updated_at, archived_at
		FROM sections
		WHERE project_uuid = ?
	`, projectUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query source sections: %w", err)
	}
	defer sections.Close()

	for sections.Next() {
		var s sourceSection
		if err := sections.Scan(&s.UUID, &s.ID, &s.ProjectUUID, &s.Slug, &s.Title,
			&s.OrderIndex, &s.Role, &s.IsDefault, &s.WIPLimit, &s.Meta,
			&s.CreatedAt, &s.UpdatedAt, &s.ArchivedAt); err != nil {
			return nil, fmt.Errorf("failed to scan source section: %w", err)
		}
		data.Sections = append(data.Sections, s)
	}
	if err := sections.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate sections: %w", err)
	}

	return data, nil
}

func collectActorUUIDs(data *sourceData) []string {
	set := make(map[string]struct{})
	for _, c := range data.Containers {
		set[c.CreatedBy] = struct{}{}
		set[c.UpdatedBy] = struct{}{}
	}
	for _, t := range data.Tasks {
		set[t.CreatedBy] = struct{}{}
		set[t.UpdatedBy] = struct{}{}
		if t.AssigneeUUID.Valid {
			set[t.AssigneeUUID.String] = struct{}{}
		}
		if t.ParentTaskUUID.Valid {
			// no actor
		}
	}
	for _, c := range data.Comments {
		set[c.ActorUUID] = struct{}{}
		if c.DeletedByUUID.Valid {
			set[c.DeletedByUUID.String] = struct{}{}
		}
	}
	for _, r := range data.Relations {
		set[r.CreatedBy] = struct{}{}
	}
	for _, a := range data.Attachments {
		if a.CreatedBy.Valid {
			set[a.CreatedBy.String] = struct{}{}
		}
	}

	uuids := make([]string, 0, len(set))
	for u := range set {
		if u != "" {
			uuids = append(uuids, u)
		}
	}
	sort.Strings(uuids)
	return uuids
}

func loadSourceActors(database *db.DB, uuids []string) ([]sourceActor, error) {
	if len(uuids) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(uuids))
	placeholders = strings.TrimSuffix(placeholders, ",")
	query := fmt.Sprintf(`
		SELECT uuid, id, slug, display_name, role, meta, created_at, updated_at
		FROM actors
		WHERE uuid IN (%s)
	`, placeholders)
	args := make([]any, len(uuids))
	for i, u := range uuids {
		args[i] = u
	}
	rows, err := database.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query source actors: %w", err)
	}
	defer rows.Close()
	var actors []sourceActor
	for rows.Next() {
		var a sourceActor
		if err := rows.Scan(&a.UUID, &a.ID, &a.Slug, &a.DisplayName, &a.Role, &a.Meta, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan source actor: %w", err)
		}
		actors = append(actors, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate source actors: %w", err)
	}
	return actors, nil
}

// -----------------------------------------------------------------------------
// Merge helpers
// -----------------------------------------------------------------------------

func mergeActors(exec *mergeExecutor, writer *events.Writer, actorUUID string, actors []sourceActor, report *mergeReport, dryRun bool) (map[string]string, error) {
	actorMap := make(map[string]string)
	sort.Slice(actors, func(i, j int) bool { return actors[i].Slug < actors[j].Slug })

	for _, a := range actors {
		report.Stats.Actors.Seen++
		var destUUID, destRole, destUpdated string
		var destDisplay, destMeta sql.NullString
		err := exec.QueryRow(`
			SELECT uuid, role, display_name, meta, updated_at
			FROM actors
			WHERE slug = ?
		`, a.Slug).Scan(&destUUID, &destRole, &destDisplay, &destMeta, &destUpdated)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("failed to lookup actor slug %s: %w", a.Slug, err)
		}

		if err == nil {
			actorMap[a.UUID] = destUUID
			if destUUID != a.UUID || destRole != a.Role {
				report.ActorMismatches = append(report.ActorMismatches, actorMismatch{
					Slug:       a.Slug,
					SourceUUID: a.UUID,
					DestUUID:   destUUID,
					SourceRole: a.Role,
					DestRole:   destRole,
				})
				report.Stats.Actors.Conflicts++
			}
			if sourceNewer(a.UpdatedAt, destUpdated, 0, 0) {
				if !dryRun {
					if _, err := exec.Exec(`
						UPDATE actors
						SET display_name = ?, meta = ?
						WHERE uuid = ?
					`, nullOrValue(a.DisplayName), nullOrValue(a.Meta), destUUID); err != nil {
						return nil, fmt.Errorf("failed to update actor %s: %w", a.Slug, err)
					}
					payload := buildActorPayload(a)
					if err := logMergeEvent(exec, writer, actorUUID, "actor", destUUID, "actor.updated", nil, payload); err != nil {
						return nil, err
					}
				}
				report.Stats.Actors.Updated++
			} else {
				report.Stats.Actors.Skipped++
			}
			continue
		}

		// slug not found, try uuid
		var existingSlug, existingRole, existingUpdated string
		var existingDisplay, existingMeta sql.NullString
		uuidErr := exec.QueryRow(`
			SELECT slug, role, display_name, meta, updated_at
			FROM actors
			WHERE uuid = ?
		`, a.UUID).Scan(&existingSlug, &existingRole, &existingDisplay, &existingMeta, &existingUpdated)
		if uuidErr != nil && !errors.Is(uuidErr, sql.ErrNoRows) {
			return nil, fmt.Errorf("failed to lookup actor uuid %s: %w", a.UUID, uuidErr)
		}
		if uuidErr == nil {
			actorMap[a.UUID] = a.UUID
			if existingSlug != a.Slug || existingRole != a.Role {
				report.ActorMismatches = append(report.ActorMismatches, actorMismatch{
					Slug:       a.Slug,
					SourceUUID: a.UUID,
					DestUUID:   a.UUID,
					SourceRole: a.Role,
					DestRole:   existingRole,
				})
				report.Stats.Actors.Conflicts++
			}
			if sourceNewer(a.UpdatedAt, existingUpdated, 0, 0) && !dryRun {
				if _, err := exec.Exec(`
					UPDATE actors
					SET slug = ?, display_name = ?, meta = ?
					WHERE uuid = ?
				`, a.Slug, nullOrValue(a.DisplayName), nullOrValue(a.Meta), a.UUID); err != nil {
					return nil, fmt.Errorf("failed to update actor %s: %w", a.UUID, err)
				}
				payload := buildActorPayload(a)
				if err := logMergeEvent(exec, writer, actorUUID, "actor", a.UUID, "actor.updated", nil, payload); err != nil {
					return nil, err
				}
				report.Stats.Actors.Updated++
			} else if sourceNewer(a.UpdatedAt, existingUpdated, 0, 0) {
				report.Stats.Actors.Updated++
			} else {
				report.Stats.Actors.Skipped++
			}
			continue
		}

		actorMap[a.UUID] = a.UUID
		if !dryRun {
			idValue := nullOrValue(a.ID)
			if idValue != nil {
				var count int
				if err := exec.QueryRow("SELECT COUNT(*) FROM actors WHERE id = ?", idValue).Scan(&count); err != nil {
					return nil, fmt.Errorf("failed to check actor id conflict: %w", err)
				}
				if count > 0 {
					idValue = nil
				}
			}
			if _, err := exec.Exec(`
				INSERT INTO actors (uuid, id, slug, display_name, role, meta, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`, a.UUID, idValue, a.Slug, nullOrValue(a.DisplayName), a.Role,
				nullOrValue(a.Meta), a.CreatedAt, a.UpdatedAt); err != nil {
				return nil, fmt.Errorf("failed to insert actor %s: %w", a.Slug, err)
			}
			payload := buildActorPayload(a)
			if err := logMergeEvent(exec, writer, actorUUID, "actor", a.UUID, "actor.created", nil, payload); err != nil {
				return nil, err
			}
		}
		report.Stats.Actors.Created++
	}
	return actorMap, nil
}

func buildActorPayload(a sourceActor) map[string]any {
	payload := map[string]any{
		"slug": a.Slug,
		"role": a.Role,
	}
	if a.DisplayName.Valid {
		payload["display_name"] = a.DisplayName.String
	}
	return payload
}

func ensurePrefixChain(exec *mergeExecutor, writer *events.Writer, actorUUID string, prefix string, report *mergeReport, dryRun bool) (*string, string, error) {
	segments := paths.SplitPath(prefix)
	if len(segments) == 0 {
		return nil, "", fmt.Errorf("invalid prefix")
	}
	var parentUUID *string
	parentPath := ""

	for idx, seg := range segments[:len(segments)-1] {
		var existingUUID string
		err := exec.QueryRow(`
			SELECT uuid FROM containers WHERE parent_uuid IS ? AND slug = ?
		`, parentUUID, seg).Scan(&existingUUID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, "", fmt.Errorf("failed to lookup prefix container: %w", err)
		}
		if err == sql.ErrNoRows {
			newUUID := uuid.New().String()
			title := seg
			if !dryRun {
				_, err := exec.Exec(`
					INSERT INTO containers (uuid, id, slug, title, description, parent_uuid, kind, sort_index,
						created_by_actor_uuid, updated_by_actor_uuid)
					VALUES (?, '', ?, ?, '', ?, 'project', 0, ?, ?)
				`, newUUID, seg, title, parentUUID, actorUUID, actorUUID)
				if err != nil {
					return nil, "", fmt.Errorf("failed to create prefix container: %w", err)
				}
				payload := map[string]any{"slug": seg, "title": title, "kind": "project"}
				if err := logMergeEvent(exec, writer, actorUUID, "container", newUUID, "container.created", nil, payload); err != nil {
					return nil, "", err
				}
			}
			report.Stats.Containers.Created++
			existingUUID = newUUID
		} else {
			report.Stats.Containers.Skipped++
		}
		parentUUID = &existingUUID
		if idx == 0 {
			parentPath = seg
		} else {
			parentPath = parentPath + "/" + seg
		}
	}

	return parentUUID, parentPath, nil
}

func mergeContainers(exec *mergeExecutor, writer *events.Writer, actorUUID string, data *sourceData, sourceRootPath, destPrefix string, prefixParentUUID *string, prefixParentPath string, actorMap map[string]string, containerMap map[string]string, containerPath map[string]string, report *mergeReport, dryRun bool) error {
	if len(data.Containers) == 0 {
		return nil
	}

	rootPath := sourceRootPath

	sort.Slice(data.Containers, func(i, j int) bool {
		return strings.Count(data.Containers[i].Path, "/") < strings.Count(data.Containers[j].Path, "/")
	})

	prefixSegments := paths.SplitPath(destPrefix)
	rootSlug := prefixSegments[len(prefixSegments)-1]

	for _, c := range data.Containers {
		report.Stats.Containers.Seen++
		isRoot := c.Path == rootPath
		var desiredParentUUID *string
		parentPath := ""
		if isRoot {
			desiredParentUUID = prefixParentUUID
			parentPath = prefixParentPath
		} else {
			if !c.ParentUUID.Valid {
				return fmt.Errorf("container %s missing parent", c.UUID)
			}
			parentDest, ok := containerMap[c.ParentUUID.String]
			if !ok {
				return fmt.Errorf("container parent not mapped for %s", c.UUID)
			}
			desiredParentUUID = &parentDest
			parentPath = containerPath[c.ParentUUID.String]
		}

		desiredSlug := c.Slug
		if isRoot {
			desiredSlug = rootSlug
		}

		actualUUID, actualSlug, actualPath, created, updated, renamed, err := mergeContainer(exec, writer, actorUUID, c, desiredParentUUID, parentPath, desiredSlug, actorMap, report, dryRun)
		if err != nil {
			return err
		}

		containerMap[c.UUID] = actualUUID
		containerPath[c.UUID] = actualPath
		if created {
			report.Stats.Containers.Created++
		} else if updated {
			report.Stats.Containers.Updated++
		} else {
			report.Stats.Containers.Skipped++
		}
		if renamed {
			report.Stats.Containers.Renamed++
			fromPath := buildPath(prefixParentPath, desiredSlug, c, containerPath)
			report.Renames = append(report.Renames, mergeRename{
				Entity: "container",
				UUID:   c.UUID,
				From:   fromPath,
				To:     actualPath,
				Reason: "slug_collision",
			})
		}
		if isRoot {
			_ = actualSlug
		}
	}

	return nil
}

func buildPath(prefixParentPath, desiredSlug string, c sourceContainer, containerPath map[string]string) string {
	if c.ParentUUID.Valid {
		if parentPath, ok := containerPath[c.ParentUUID.String]; ok && parentPath != "" {
			return parentPath + "/" + desiredSlug
		}
	}
	if prefixParentPath != "" {
		return prefixParentPath + "/" + desiredSlug
	}
	return desiredSlug
}

func mergeContainer(exec *mergeExecutor, writer *events.Writer, actorUUID string, c sourceContainer, desiredParentUUID *string, parentPath string, desiredSlug string, actorMap map[string]string, report *mergeReport, dryRun bool) (string, string, string, bool, bool, bool, error) {
	var destSlug, destParent sql.NullString
	var destTitle, destDescription, destKind sql.NullString
	var destSection sql.NullString
	var destSort int
	var destETag int64
	var destUpdated string
	var destUUID string
	err := exec.QueryRow(`
		SELECT uuid, slug, parent_uuid, title, description, kind, section_uuid, sort_index, etag, updated_at
		FROM containers WHERE uuid = ?
	`, c.UUID).Scan(&destUUID, &destSlug, &destParent, &destTitle, &destDescription, &destKind, &destSection, &destSort, &destETag, &destUpdated)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", "", "", false, false, false, fmt.Errorf("failed to lookup container %s: %w", c.UUID, err)
	}

	actualSlug := desiredSlug
	actualUUID := c.UUID
	created := false
	updated := false
	renamed := false

	resolveSlug := func() (string, bool, error) {
		return ensureUniqueContainerSlug(exec, desiredParentUUID, c.UUID, desiredSlug)
	}

	if err == sql.ErrNoRows {
		var err error
		actualSlug, renamed, err = resolveSlug()
		if err != nil {
			return "", "", "", false, false, false, err
		}
		if !dryRun {
			idValue := nullOrValue(c.ID)
			if idValue != nil {
				var count int
				if err := exec.QueryRow("SELECT COUNT(*) FROM containers WHERE id = ?", idValue).Scan(&count); err != nil {
					return "", "", "", false, false, false, fmt.Errorf("failed to check container id conflict: %w", err)
				}
				if count > 0 {
					idValue = nil
				}
			}
			_, err = exec.Exec(`
				INSERT INTO containers (uuid, id, slug, title, description, parent_uuid, kind, section_uuid, sort_index,
					etag, created_at, updated_at, archived_at, created_by_actor_uuid, updated_by_actor_uuid)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, c.UUID, idValue, actualSlug, c.Title, c.Description, desiredParentUUID, c.Kind,
				nullOrValue(c.SectionUUID), c.SortIndex, c.ETag, c.CreatedAt, c.UpdatedAt,
				nullOrValue(c.ArchivedAt), mapActor(actorMap, c.CreatedBy), mapActor(actorMap, c.UpdatedBy))
			if err != nil {
				return "", "", "", false, false, false, fmt.Errorf("failed to insert container %s: %w", c.UUID, err)
			}
			payload := map[string]any{"slug": actualSlug, "title": c.Title, "kind": c.Kind}
			if err := logMergeEvent(exec, writer, actorUUID, "container", c.UUID, "container.created", &c.ETag, payload); err != nil {
				return "", "", "", false, false, false, err
			}
		}
		created = true
	} else {
		actualSlug = destSlug.String
		if sourceNewer(c.UpdatedAt, destUpdated, c.ETag, destETag) {
			var err error
			actualSlug, renamed, err = resolveSlug()
			if err != nil {
				return "", "", "", false, false, false, err
			}
			if !dryRun {
				_, err = exec.Exec(`
					UPDATE containers
					SET slug = ?, parent_uuid = ?, title = ?, description = ?, kind = ?, section_uuid = ?, sort_index = ?,
						etag = ?, archived_at = ?, updated_by_actor_uuid = ?
					WHERE uuid = ?
				`, actualSlug, desiredParentUUID, c.Title, c.Description, c.Kind, nullOrValue(c.SectionUUID), c.SortIndex,
					c.ETag, nullOrValue(c.ArchivedAt), mapActor(actorMap, c.UpdatedBy), c.UUID)
				if err != nil {
					return "", "", "", false, false, false, fmt.Errorf("failed to update container %s: %w", c.UUID, err)
				}
				payload := map[string]any{"slug": actualSlug, "title": c.Title, "kind": c.Kind}
				if err := logMergeEvent(exec, writer, actorUUID, "container", c.UUID, "container.updated", &c.ETag, payload); err != nil {
					return "", "", "", false, false, false, err
				}
			}
			updated = true
		} else {
			report.Stats.Containers.Conflicts++
		}
	}

	var actualPath string
	if dryRun && created {
		if parentPath != "" {
			actualPath = parentPath + "/" + actualSlug
		} else {
			actualPath = actualSlug
		}
	} else {
		actualPath, err = resolveContainerPath(exec, actualUUID)
		if err != nil {
			return "", "", "", false, false, false, err
		}
	}

	return actualUUID, actualSlug, actualPath, created, updated, renamed, nil
}

func resolveContainerPath(exec *mergeExecutor, uuid string) (string, error) {
	var path string
	if err := exec.QueryRow("SELECT path FROM v_container_paths WHERE uuid = ?", uuid).Scan(&path); err != nil {
		return "", fmt.Errorf("failed to resolve container path for %s: %w", uuid, err)
	}
	return path, nil
}

func ensureUniqueContainerSlug(exec *mergeExecutor, parentUUID *string, uuid, desired string) (string, bool, error) {
	for idx := 0; idx < 1000; idx++ {
		candidate := desired
		renamed := idx > 0
		if idx > 0 {
			candidate = withDupSuffix(desired, idx+1)
		}
		var existing string
		err := exec.QueryRow(`
			SELECT uuid FROM containers WHERE parent_uuid IS ? AND slug = ?
		`, parentUUID, candidate).Scan(&existing)
		if err == sql.ErrNoRows {
			return candidate, renamed, nil
		}
		if err != nil {
			return "", false, err
		}
		if existing == uuid {
			return candidate, renamed, nil
		}
	}
	return "", false, fmt.Errorf("unable to resolve container slug collision for %s", desired)
}

func withDupSuffix(base string, idx int) string {
	suffix := fmt.Sprintf("--dup-%d", idx)
	if len(base)+len(suffix) <= 255 {
		return base + suffix
	}
	trim := 255 - len(suffix)
	if trim < 1 {
		trim = 1
	}
	return base[:trim] + suffix
}

func mergeSections(exec *mergeExecutor, writer *events.Writer, actorUUID string, sections []sourceSection, sourceProjectUUID, destProjectUUID string, actorMap map[string]string, report *mergeReport, dryRun bool) (map[string]string, error) {
	sectionMap := make(map[string]string)
	for _, s := range sections {
		report.Stats.Sections.Seen++
		if destProjectUUID == "" {
			return nil, fmt.Errorf("destination project uuid missing for sections")
		}
		var destSlug string
		var destUpdated string
		var destRole string
		err := exec.QueryRow(`
			SELECT slug, role, updated_at FROM sections WHERE uuid = ?
		`, s.UUID).Scan(&destSlug, &destRole, &destUpdated)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("failed to lookup section %s: %w", s.UUID, err)
		}

		sectionMap[s.UUID] = s.UUID
		if err == sql.ErrNoRows {
			slug, renamed, err := ensureUniqueSectionSlug(exec, destProjectUUID, s.UUID, s.Slug)
			if err != nil {
				return nil, err
			}
			if !dryRun {
				idValue := nullOrValue(s.ID)
				if idValue != nil {
					var count int
					if err := exec.QueryRow("SELECT COUNT(*) FROM sections WHERE id = ?", idValue).Scan(&count); err != nil {
						return nil, fmt.Errorf("failed to check section id conflict: %w", err)
					}
					if count > 0 {
						idValue = nil
					}
				}
				_, err := exec.Exec(`
					INSERT INTO sections (uuid, id, project_uuid, slug, title, order_index, role, is_default,
						wip_limit, meta, created_at, updated_at, archived_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				`, s.UUID, idValue, destProjectUUID, slug, s.Title, s.OrderIndex, s.Role, boolToInt(s.IsDefault),
					nullOrValue(s.WIPLimit), nullOrValue(s.Meta), s.CreatedAt, s.UpdatedAt, nullOrValue(s.ArchivedAt))
				if err != nil {
					return nil, fmt.Errorf("failed to insert section %s: %w", s.UUID, err)
				}
			}
			report.Stats.Sections.Created++
			if renamed {
				report.Stats.Sections.Renamed++
				report.Renames = append(report.Renames, mergeRename{
					Entity: "section",
					UUID:   s.UUID,
					From:   s.Slug,
					To:     slug,
					Reason: "slug_collision",
				})
			}
			continue
		}

		if sourceNewer(s.UpdatedAt, destUpdated, 0, 0) {
			slug, renamed, err := ensureUniqueSectionSlug(exec, destProjectUUID, s.UUID, s.Slug)
			if err != nil {
				return nil, err
			}
			if !dryRun {
				_, err := exec.Exec(`
					UPDATE sections
					SET slug = ?, title = ?, order_index = ?, role = ?, is_default = ?, wip_limit = ?, meta = ?, archived_at = ?
					WHERE uuid = ?
				`, slug, s.Title, s.OrderIndex, s.Role, boolToInt(s.IsDefault), nullOrValue(s.WIPLimit),
					nullOrValue(s.Meta), nullOrValue(s.ArchivedAt), s.UUID)
				if err != nil {
					return nil, fmt.Errorf("failed to update section %s: %w", s.UUID, err)
				}
			}
			report.Stats.Sections.Updated++
			if renamed {
				report.Stats.Sections.Renamed++
				report.Renames = append(report.Renames, mergeRename{
					Entity: "section",
					UUID:   s.UUID,
					From:   s.Slug,
					To:     slug,
					Reason: "slug_collision",
				})
			}
		} else {
			if destRole != s.Role {
				report.Stats.Sections.Conflicts++
			}
			report.Stats.Sections.Skipped++
		}
	}

	return sectionMap, nil
}

func ensureUniqueSectionSlug(exec *mergeExecutor, projectUUID, uuid, desired string) (string, bool, error) {
	for idx := 0; idx < 1000; idx++ {
		candidate := desired
		renamed := idx > 0
		if idx > 0 {
			candidate = withDupSuffix(desired, idx+1)
		}
		var existing string
		err := exec.QueryRow(`
			SELECT uuid FROM sections WHERE project_uuid = ? AND slug = ?
		`, projectUUID, candidate).Scan(&existing)
		if err == sql.ErrNoRows {
			return candidate, renamed, nil
		}
		if err != nil {
			return "", false, err
		}
		if existing == uuid {
			return candidate, renamed, nil
		}
	}
	return "", false, fmt.Errorf("unable to resolve section slug collision for %s", desired)
}

func applySectionRefs(exec *mergeExecutor, containers []sourceContainer, sectionMap map[string]string, containerMap map[string]string, report *mergeReport, dryRun bool) error {
	for _, c := range containers {
		if !c.SectionUUID.Valid {
			continue
		}
		destSection, ok := sectionMap[c.SectionUUID.String]
		if !ok {
			report.Warnings = append(report.Warnings, fmt.Sprintf("missing section %s for container %s", c.SectionUUID.String, c.UUID))
			continue
		}
		if !dryRun {
			if _, err := exec.Exec("UPDATE containers SET section_uuid = ? WHERE uuid = ?", destSection, containerMap[c.UUID]); err != nil {
				return fmt.Errorf("failed to update container section %s: %w", c.UUID, err)
			}
		}
	}
	return nil
}

func mergeTasks(exec *mergeExecutor, writer *events.Writer, actorUUID string, tasks []sourceTask, containerMap map[string]string, actorMap map[string]string, report *mergeReport, dryRun bool) (map[string]string, error) {
	taskMap := make(map[string]string)
	parents := make([]sourceTask, 0, len(tasks))
	subtasks := make([]sourceTask, 0, len(tasks))
	for _, t := range tasks {
		if t.ParentTaskUUID.Valid {
			subtasks = append(subtasks, t)
		} else {
			parents = append(parents, t)
		}
	}

	ordered := append(parents, subtasks...)
	for _, t := range ordered {
		report.Stats.Tasks.Seen++
		destProjectUUID, ok := containerMap[t.ProjectUUID]
		if !ok {
			return nil, fmt.Errorf("task %s references unknown container %s", t.UUID, t.ProjectUUID)
		}

		parentUUID := sql.NullString{}
		if t.ParentTaskUUID.Valid {
			if _, ok := taskMap[t.ParentTaskUUID.String]; ok {
				parentUUID = sql.NullString{String: t.ParentTaskUUID.String, Valid: true}
			} else {
				report.Warnings = append(report.Warnings, fmt.Sprintf("task %s parent %s missing; clearing parent_task_uuid", t.UUID, t.ParentTaskUUID.String))
			}
		}

		actualUUID := t.UUID
		actualSlug, created, updated, renamed, err := mergeTask(exec, writer, actorUUID, t, destProjectUUID, parentUUID, actorMap, report, dryRun)
		if err != nil {
			return nil, err
		}
		taskMap[t.UUID] = actualUUID

		if created {
			report.Stats.Tasks.Created++
		} else if updated {
			report.Stats.Tasks.Updated++
		} else {
			report.Stats.Tasks.Skipped++
		}
		if renamed {
			report.Stats.Tasks.Renamed++
			report.Renames = append(report.Renames, mergeRename{
				Entity: "task",
				UUID:   t.UUID,
				From:   t.Slug,
				To:     actualSlug,
				Reason: "slug_collision",
			})
		}
	}

	return taskMap, nil
}

func mergeTask(exec *mergeExecutor, writer *events.Writer, actorUUID string, t sourceTask, destProjectUUID string, parentUUID sql.NullString, actorMap map[string]string, report *mergeReport, dryRun bool) (string, bool, bool, bool, error) {
	var destSlug string
	var destETag int64
	var destUpdated string
	err := exec.QueryRow(`
		SELECT slug, etag, updated_at FROM tasks WHERE uuid = ?
	`, t.UUID).Scan(&destSlug, &destETag, &destUpdated)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, false, false, fmt.Errorf("failed to lookup task %s: %w", t.UUID, err)
	}

	resolveSlug := func() (string, bool, error) {
		return ensureUniqueTaskSlug(exec, destProjectUUID, t.UUID, t.Slug)
	}

	if err == sql.ErrNoRows {
		slug, renamed, err := resolveSlug()
		if err != nil {
			return "", false, false, false, err
		}
		if !dryRun {
			idValue := nullOrValue(t.ID)
			if idValue != nil {
				var count int
				if err := exec.QueryRow("SELECT COUNT(*) FROM tasks WHERE id = ?", idValue).Scan(&count); err != nil {
					return "", false, false, false, fmt.Errorf("failed to check task id conflict: %w", err)
				}
				if count > 0 {
					idValue = nil
				}
			}
			_, err = exec.Exec(`
				INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, kind, parent_task_uuid,
					assignee_actor_uuid, start_at, due_at, labels, description, etag, created_at, updated_at,
					completed_at, archived_at, deleted_at, created_by_actor_uuid, updated_by_actor_uuid)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, t.UUID, idValue, slug, t.Title, destProjectUUID, t.State, t.Priority, t.Kind,
				nullOrValue(parentUUID), mapActorNullable(actorMap, t.AssigneeUUID), nullOrValue(t.StartAt),
				nullOrValue(t.DueAt), nullOrValue(t.Labels), t.Description, t.ETag, t.CreatedAt, t.UpdatedAt,
				nullOrValue(t.CompletedAt), nullOrValue(t.ArchivedAt), nullOrValue(t.DeletedAt),
				mapActor(actorMap, t.CreatedBy), mapActor(actorMap, t.UpdatedBy))
			if err != nil {
				return "", false, false, false, fmt.Errorf("failed to insert task %s: %w", t.UUID, err)
			}
			payload := map[string]any{"slug": slug, "title": t.Title, "state": t.State}
			if err := logMergeEvent(exec, writer, actorUUID, "task", t.UUID, "task.created", &t.ETag, payload); err != nil {
				return "", false, false, false, err
			}
		}
		return slug, true, false, renamed, nil
	}

	if !sourceNewer(t.UpdatedAt, destUpdated, t.ETag, destETag) {
		report.Stats.Tasks.Conflicts++
		return destSlug, false, false, false, nil
	}

	slug, renamed, err := resolveSlug()
	if err != nil {
		return "", false, false, false, err
	}

	if !dryRun {
		_, err := exec.Exec(`
			UPDATE tasks
			SET slug = ?, title = ?, project_uuid = ?, state = ?, priority = ?, kind = ?, parent_task_uuid = ?,
				assignee_actor_uuid = ?, start_at = ?, due_at = ?, labels = ?, description = ?,
				completed_at = ?, archived_at = ?, deleted_at = ?, updated_by_actor_uuid = ?, updated_at = ?
			WHERE uuid = ?
		`, slug, t.Title, destProjectUUID, t.State, t.Priority, t.Kind, nullOrValue(parentUUID),
			mapActorNullable(actorMap, t.AssigneeUUID), nullOrValue(t.StartAt), nullOrValue(t.DueAt), nullOrValue(t.Labels),
			t.Description, nullOrValue(t.CompletedAt), nullOrValue(t.ArchivedAt), nullOrValue(t.DeletedAt),
			mapActor(actorMap, t.UpdatedBy), t.UpdatedAt, t.UUID)
		if err != nil {
			return "", false, false, false, fmt.Errorf("failed to update task %s: %w", t.UUID, err)
		}
		payload := map[string]any{"slug": slug, "title": t.Title, "state": t.State}
		if err := logMergeEvent(exec, writer, actorUUID, "task", t.UUID, "task.updated", nil, payload); err != nil {
			return "", false, false, false, err
		}
	}
	return slug, false, true, renamed, nil
}

func ensureUniqueTaskSlug(exec *mergeExecutor, projectUUID, uuid, desired string) (string, bool, error) {
	for idx := 0; idx < 1000; idx++ {
		candidate := desired
		renamed := idx > 0
		if idx > 0 {
			candidate = withDupSuffix(desired, idx+1)
		}
		var existing string
		err := exec.QueryRow(`
			SELECT uuid FROM tasks WHERE project_uuid = ? AND slug = ?
		`, projectUUID, candidate).Scan(&existing)
		if err == sql.ErrNoRows {
			return candidate, renamed, nil
		}
		if err != nil {
			return "", false, err
		}
		if existing == uuid {
			return candidate, renamed, nil
		}
	}
	return "", false, fmt.Errorf("unable to resolve task slug collision for %s", desired)
}

func mergeComments(exec *mergeExecutor, writer *events.Writer, actorUUID string, comments []sourceComment, taskMap map[string]string, actorMap map[string]string, report *mergeReport, dryRun bool) error {
	for _, c := range comments {
		report.Stats.Comments.Seen++
		destTask, ok := taskMap[c.TaskUUID]
		if !ok {
			report.Warnings = append(report.Warnings, fmt.Sprintf("comment %s references missing task %s", c.UUID, c.TaskUUID))
			report.Stats.Comments.Skipped++
			continue
		}
		var destETag int64
		var destUpdated sql.NullString
		err := exec.QueryRow(`
			SELECT etag, updated_at FROM comments WHERE uuid = ?
		`, c.UUID).Scan(&destETag, &destUpdated)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("failed to lookup comment %s: %w", c.UUID, err)
		}

		if err == sql.ErrNoRows {
			commentID := c.ID
			if !dryRun {
				if idConflict(exec, "comments", "id", commentID) {
					commentID, err = nextCommentID(exec)
					if err != nil {
						return err
					}
				}
				_, err := exec.Exec(`
					INSERT INTO comments (uuid, id, task_uuid, actor_uuid, body, meta, etag, created_at, updated_at, deleted_at, deleted_by_actor_uuid)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				`, c.UUID, commentID, destTask, mapActor(actorMap, c.ActorUUID), c.Body, nullOrValue(c.Meta), c.ETag,
					c.CreatedAt, nullOrValue(c.UpdatedAt), nullOrValue(c.DeletedAt), mapActorNullable(actorMap, c.DeletedByUUID))
				if err != nil {
					return fmt.Errorf("failed to insert comment %s: %w", c.UUID, err)
				}
				payload := map[string]any{"task_id": destTask, "comment_id": commentID, "actor_id": mapActor(actorMap, c.ActorUUID)}
				if err := logMergeEvent(exec, writer, actorUUID, "comment", c.UUID, "comment.created", &c.ETag, payload); err != nil {
					return err
				}
			}
			report.Stats.Comments.Created++
			continue
		}

		if destUpdated.Valid && !sourceNewer(nullableString(c.UpdatedAt, c.CreatedAt), destUpdated.String, c.ETag, destETag) {
			report.Stats.Comments.Skipped++
			continue
		}

		if !dryRun {
			_, err := exec.Exec(`
				UPDATE comments
				SET task_uuid = ?, actor_uuid = ?, body = ?, meta = ?, etag = ?, updated_at = ?, deleted_at = ?, deleted_by_actor_uuid = ?
				WHERE uuid = ?
			`, destTask, mapActor(actorMap, c.ActorUUID), c.Body, nullOrValue(c.Meta), c.ETag,
				nullOrValue(c.UpdatedAt), nullOrValue(c.DeletedAt), mapActorNullable(actorMap, c.DeletedByUUID), c.UUID)
			if err != nil {
				return fmt.Errorf("failed to update comment %s: %w", c.UUID, err)
			}
			payload := map[string]any{"task_id": destTask}
			if err := logMergeEvent(exec, writer, actorUUID, "comment", c.UUID, "comment.updated", nil, payload); err != nil {
				return err
			}
		}
		report.Stats.Comments.Updated++
	}
	return nil
}

func mergeRelations(exec *mergeExecutor, writer *events.Writer, actorUUID string, relations []sourceRelation, taskMap map[string]string, actorMap map[string]string, report *mergeReport, dryRun bool) error {
	for _, r := range relations {
		report.Stats.Relations.Seen++
		fromTask, okFrom := taskMap[r.FromTaskUUID]
		toTask, okTo := taskMap[r.ToTaskUUID]
		if !okFrom || !okTo {
			report.Warnings = append(report.Warnings, fmt.Sprintf("relation %s -> %s skipped (missing task)", r.FromTaskUUID, r.ToTaskUUID))
			report.Stats.Relations.Skipped++
			continue
		}
		var count int
		if err := exec.QueryRow(`
			SELECT COUNT(*) FROM task_relations WHERE from_task_uuid = ? AND to_task_uuid = ? AND kind = ?
		`, fromTask, toTask, r.Kind).Scan(&count); err != nil {
			return fmt.Errorf("failed to check relation: %w", err)
		}
		if count > 0 {
			report.Stats.Relations.Skipped++
			continue
		}
		if !dryRun {
			_, err := exec.Exec(`
				INSERT INTO task_relations (from_task_uuid, to_task_uuid, kind, meta, created_at, created_by_actor_uuid)
				VALUES (?, ?, ?, ?, ?, ?)
			`, fromTask, toTask, r.Kind, nullOrValue(r.Meta), r.CreatedAt, mapActor(actorMap, r.CreatedBy))
			if err != nil {
				return fmt.Errorf("failed to insert relation: %w", err)
			}
			payload := map[string]any{"from": fromTask, "to": toTask, "kind": r.Kind}
			if err := logMergeEvent(exec, writer, actorUUID, "task", fromTask, "task.relation.created", nil, payload); err != nil {
				return err
			}
		}
		report.Stats.Relations.Created++
	}
	return nil
}

type fileCopy struct {
	SourceRelPath string
	DestRelPath   string
}

func mergeAttachments(exec *mergeExecutor, writer *events.Writer, actorUUID string, attachments []sourceAttachment, taskMap map[string]string, actorMap map[string]string, report *mergeReport, dryRun bool) ([]fileCopy, error) {
	var files []fileCopy
	for _, a := range attachments {
		report.Stats.Attachments.Seen++
		destTask, ok := taskMap[a.TaskUUID]
		if !ok {
			report.Warnings = append(report.Warnings, fmt.Sprintf("attachment %s references missing task %s", a.UUID, a.TaskUUID))
			report.Stats.Attachments.Skipped++
			continue
		}

		var existingRelPath sql.NullString
		var existingChecksum sql.NullString
		err := exec.QueryRow(`
			SELECT relative_path, checksum FROM attachments WHERE uuid = ?
		`, a.UUID).Scan(&existingRelPath, &existingChecksum)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("failed to lookup attachment uuid %s: %w", a.UUID, err)
		}
		if err == nil {
			if existingRelPath.String != a.RelPath {
				report.Stats.Attachments.Conflicts++
				report.Warnings = append(report.Warnings, fmt.Sprintf("attachment uuid %s has different relative_path", a.UUID))
				report.Stats.Attachments.Skipped++
				continue
			}
			report.Stats.Attachments.Deduped++
			files = append(files, fileCopy{SourceRelPath: a.RelPath, DestRelPath: a.RelPath})
			continue
		}

		var existingUUID, existingChecksumByPath sql.NullString
		err = exec.QueryRow(`
			SELECT uuid, checksum FROM attachments WHERE relative_path = ?
		`, a.RelPath).Scan(&existingUUID, &existingChecksumByPath)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("failed to lookup attachment relpath %s: %w", a.RelPath, err)
		}

		if err == nil {
			if existingChecksumByPath.Valid && a.Checksum.Valid && existingChecksumByPath.String == a.Checksum.String {
				report.Stats.Attachments.Deduped++
				files = append(files, fileCopy{SourceRelPath: a.RelPath, DestRelPath: a.RelPath})
				continue
			}
			report.Stats.Attachments.Conflicts++
			report.Warnings = append(report.Warnings, fmt.Sprintf("attachment conflict at %s", a.RelPath))
			report.Stats.Attachments.Skipped++
			continue
		}

		if !dryRun {
			idValue := nullOrValue(a.ID)
			if idValue != nil {
				var count int
				if err := exec.QueryRow("SELECT COUNT(*) FROM attachments WHERE id = ?", idValue).Scan(&count); err != nil {
					return nil, fmt.Errorf("failed to check attachment id conflict: %w", err)
				}
				if count > 0 {
					idValue = nil
				}
			}
			_, err := exec.Exec(`
				INSERT INTO attachments (uuid, id, task_uuid, filename, relative_path, mime_type, size_bytes, checksum, created_at, created_by_actor_uuid)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, a.UUID, idValue, destTask, a.Filename, a.RelPath, nullOrValue(a.MimeType), a.SizeBytes,
				nullOrValue(a.Checksum), a.CreatedAt, mapActorNullable(actorMap, a.CreatedBy))
			if err != nil {
				return nil, fmt.Errorf("failed to insert attachment %s: %w", a.UUID, err)
			}
			payload := map[string]any{"attachment_id": a.ID.String, "filename": a.Filename}
			if err := logMergeEvent(exec, writer, actorUUID, "attachment", a.UUID, "attachment.created", nil, payload); err != nil {
				return nil, err
			}
		}
		report.Stats.Attachments.Created++
		files = append(files, fileCopy{SourceRelPath: a.RelPath, DestRelPath: a.RelPath})
	}
	return files, nil
}

func performFileCopies(files []fileCopy, srcAttach, destAttach string) (int, int, []string) {
	copied := 0
	missing := 0
	warnings := []string{}
	for _, f := range files {
		src := filepath.Join(srcAttach, f.SourceRelPath)
		dst := filepath.Join(destAttach, f.DestRelPath)
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) {
				missing++
				warnings = append(warnings, fmt.Sprintf("missing source attachment: %s", src))
				continue
			}
			missing++
			warnings = append(warnings, fmt.Sprintf("failed to stat source attachment: %s", src))
			continue
		}
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		parts := strings.Split(f.DestRelPath, "/")
		if len(parts) < 2 {
			warnings = append(warnings, fmt.Sprintf("invalid attachment path: %s", f.DestRelPath))
			continue
		}
		if err := attach.EnsureTaskDir(destAttach, parts[1]); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to ensure attachment dir: %s", err))
			continue
		}
		if _, _, err := attach.CopyFile(src, dst); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to copy attachment: %s", err))
			continue
		}
		copied++
	}
	return copied, missing, warnings
}

// -----------------------------------------------------------------------------
// Utility helpers
// -----------------------------------------------------------------------------

func nullOrValue(value any) any {
	switch v := value.(type) {
	case sql.NullString:
		if !v.Valid {
			return nil
		}
		return v.String
	case sql.NullInt64:
		if !v.Valid {
			return nil
		}
		return v.Int64
	case *sql.NullString:
		if v == nil || !v.Valid {
			return nil
		}
		return v.String
	case *sql.NullInt64:
		if v == nil || !v.Valid {
			return nil
		}
		return v.Int64
	case *sql.NullBool:
		if v == nil || !v.Valid {
			return nil
		}
		if v.Bool {
			return 1
		}
		return 0
	case sql.NullBool:
		if !v.Valid {
			return nil
		}
		if v.Bool {
			return 1
		}
		return 0
	case string:
		if v == "" {
			return nil
		}
		return v
	default:
		return value
	}
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func mapActor(mapping map[string]string, uuid string) string {
	if mapped, ok := mapping[uuid]; ok {
		return mapped
	}
	return uuid
}

func mapActorNullable(mapping map[string]string, uuid sql.NullString) any {
	if !uuid.Valid {
		return nil
	}
	return mapActor(mapping, uuid.String)
}

func sourceNewer(srcUpdated, destUpdated string, srcETag, destETag int64) bool {
	if srcUpdated != "" && destUpdated != "" {
		srcTime, err1 := time.Parse(time.RFC3339, srcUpdated)
		destTime, err2 := time.Parse(time.RFC3339, destUpdated)
		if err1 == nil && err2 == nil {
			return srcTime.After(destTime)
		}
	}
	return srcETag > destETag
}

func nullableString(primary sql.NullString, fallback string) string {
	if primary.Valid {
		return primary.String
	}
	return fallback
}

func idConflict(exec *mergeExecutor, table, column, value string) bool {
	var count int
	_ = exec.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = ?", table, column), value).Scan(&count)
	return count > 0
}

func nextCommentID(exec *mergeExecutor) (string, error) {
	var nextSeq int
	if err := exec.QueryRow("SELECT COALESCE(MAX(CAST(SUBSTR(id, 3) AS INTEGER)), 0) + 1 FROM comments").Scan(&nextSeq); err != nil {
		return "", fmt.Errorf("failed to compute next comment ID: %w", err)
	}
	return fmt.Sprintf("C-%05d", nextSeq), nil
}

func syncCommentSequence(exec *mergeExecutor) error {
	var nextSeq int
	if err := exec.QueryRow("SELECT COALESCE(MAX(CAST(SUBSTR(id, 3) AS INTEGER)), 0) FROM comments").Scan(&nextSeq); err != nil {
		return err
	}
	_, err := exec.Exec("UPDATE comment_sequences SET value = ? WHERE name = 'next_comment'", nextSeq)
	return err
}

func logMergeEvent(exec *mergeExecutor, writer *events.Writer, actorUUID string, resourceType, resourceUUID, eventType string, etag *int64, payload map[string]any) error {
	payloadJSON, _ := json.Marshal(payload)
	payloadStr := string(payloadJSON)
	event := &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: resourceType,
		ResourceUUID: &resourceUUID,
		EventType:    eventType,
		ETag:         etag,
		Payload:      &payloadStr,
	}
	var tx *sql.Tx
	if exec.tx != nil {
		tx = exec.tx
	}
	if err := writer.LogEvent(tx, event); err != nil {
		return fmt.Errorf("failed to log %s event: %w", eventType, err)
	}
	return nil
}
