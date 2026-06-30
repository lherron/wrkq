package snapshot

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	dbsync "github.com/lherron/wrkq/internal/db"
)

// Import loads a snapshot file and hydrates the database.
func Import(db *sql.DB, opts ImportOptions) (*ImportResult, error) {
	if opts.InputPath == "" {
		opts.InputPath = DefaultOutputPath
	}

	// Read snapshot file
	data, err := os.ReadFile(opts.InputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot: %w", err)
	}

	// Reject legacy actor-bearing snapshots outright. wrkq is principal-only;
	// importing actor scaffolding would be lossy, so we refuse rather than convert.
	if err := rejectLegacyActorData(data); err != nil {
		return nil, err
	}

	// Parse snapshot
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("failed to parse snapshot: %w", err)
	}

	// Validate snapshot structure
	if err := validateSnapshot(&snap); err != nil {
		return nil, fmt.Errorf("invalid snapshot: %w", err)
	}

	// Check if database is empty (if --if-empty)
	if opts.IfEmpty {
		empty, err := isDatabaseEmpty(db)
		if err != nil {
			return nil, fmt.Errorf("failed to check database: %w", err)
		}
		if !empty {
			return nil, fmt.Errorf("database is not empty (use --force to override)")
		}
	}

	// If dry run, just validate and return
	if opts.DryRun {
		return &ImportResult{
			InputPath:      opts.InputPath,
			SnapshotRev:    snap.Meta.SnapshotRev,
			ContainerCount: len(snap.Containers),
			TaskCount:      len(snap.Tasks),
			CommentCount:   len(snap.Comments),
			DryRun:         true,
		}, nil
	}

	// Start transaction
	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// If force, truncate tables
	if opts.Force {
		if err := truncateTables(tx); err != nil {
			return nil, fmt.Errorf("failed to truncate tables: %w", err)
		}
	}

	// Import in dependency order: containers -> tasks -> comments
	if err := importContainers(tx, &snap); err != nil {
		return nil, fmt.Errorf("failed to import containers: %w", err)
	}

	if err := importTasks(tx, &snap); err != nil {
		return nil, fmt.Errorf("failed to import tasks: %w", err)
	}

	if err := importComments(tx, &snap); err != nil {
		return nil, fmt.Errorf("failed to import comments: %w", err)
	}

	// Ensure sqlite_sequence matches max friendly IDs after import.
	if _, err := dbsync.FixSequenceDrifts(tx, dbsync.DefaultSequenceSpecs()); err != nil {
		return nil, fmt.Errorf("failed to sync sqlite_sequence: %w", err)
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &ImportResult{
		InputPath:      opts.InputPath,
		SnapshotRev:    snap.Meta.SnapshotRev,
		ContainerCount: len(snap.Containers),
		TaskCount:      len(snap.Tasks),
		CommentCount:   len(snap.Comments),
		DryRun:         false,
	}, nil
}

// rejectLegacyActorData refuses any snapshot that still carries actor
// scaffolding (a top-level "actors" map, actor UUID/slug/role fields, or the
// legacy bare created_by/updated_by/deleted_by attribution keys). wrkq is
// principal-only; converting such snapshots would be lossy, so we hard-gate
// them rather than import partial data.
func rejectLegacyActorData(data []byte) error {
	var raw interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		// Let the normal parse path report the malformed-JSON error.
		return nil
	}
	if key := findLegacyActorKey(raw); key != "" {
		return fmt.Errorf("legacy actor data movement is no longer supported: snapshot contains %q; wrkq is principal-only", key)
	}
	return nil
}

// legacyActorKeys are object keys whose presence anywhere in a snapshot marks
// it as a pre-principal (actor-bearing) snapshot.
// These are JSON denylist keys, not DB columns — wrkq is principal-only, so any
// snapshot still carrying actor scaffolding is hard-rejected rather than imported.
var legacyActorKeys = map[string]bool{
	"actors":                true,
	"actor_uuid":            true, // principal-only: legacy-rejection denylist key, not a live column read
	"actor_slug":            true,
	"actor_role":            true,
	"created_by_actor_uuid": true, // principal-only: legacy-rejection denylist key, not a live column read
	"updated_by_actor_uuid": true, // principal-only: legacy-rejection denylist key, not a live column read
	"deleted_by_actor_uuid": true, // principal-only: legacy-rejection denylist key, not a live column read
	"assignee_actor_uuid":   true, // principal-only: legacy-rejection denylist key, not a live column read
	// Legacy bare attribution fields (now superseded by *_principal_ref).
	"created_by": true,
	"updated_by": true,
	"deleted_by": true,
}

func findLegacyActorKey(v interface{}) string {
	switch t := v.(type) {
	case map[string]interface{}:
		for k, child := range t {
			if legacyActorKeys[k] {
				return k
			}
			if found := findLegacyActorKey(child); found != "" {
				return found
			}
		}
	case []interface{}:
		for _, child := range t {
			if found := findLegacyActorKey(child); found != "" {
				return found
			}
		}
	}
	return ""
}

// LoadSnapshot reads and parses a snapshot file.
func LoadSnapshot(path string) (*Snapshot, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read snapshot: %w", err)
	}

	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, nil, fmt.Errorf("failed to parse snapshot: %w", err)
	}

	return &snap, data, nil
}

func validateSnapshot(snap *Snapshot) error {
	// Validate meta
	if snap.Meta.SchemaVersion < 1 {
		return fmt.Errorf("invalid schema_version: %d", snap.Meta.SchemaVersion)
	}
	if snap.Meta.MachineInterfaceVersion < 1 {
		return fmt.Errorf("invalid machine_interface_version: %d", snap.Meta.MachineInterfaceVersion)
	}

	// Validate FK references
	// Tasks must reference valid containers
	for uuid, task := range snap.Tasks {
		if _, ok := snap.Containers[task.ProjectUUID]; !ok {
			return fmt.Errorf("task %s references unknown container %s", uuid, task.ProjectUUID)
		}
	}

	// Comments must reference valid tasks.
	for uuid, comment := range snap.Comments {
		if _, ok := snap.Tasks[comment.TaskUUID]; !ok {
			return fmt.Errorf("comment %s references unknown task %s", uuid, comment.TaskUUID)
		}
	}

	// Containers with parent_uuid must reference valid containers
	for uuid, container := range snap.Containers {
		if container.ParentUUID != "" {
			if _, ok := snap.Containers[container.ParentUUID]; !ok {
				return fmt.Errorf("container %s references unknown parent %s", uuid, container.ParentUUID)
			}
		}
	}

	return nil
}

func isDatabaseEmpty(db *sql.DB) (bool, error) {
	var count int

	// Check containers
	if err := db.QueryRow("SELECT COUNT(*) FROM containers").Scan(&count); err != nil {
		return false, err
	}
	if count > 1 { // Allow inbox
		return false, nil
	}

	// Check tasks
	if err := db.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&count); err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}

	return true, nil
}

func truncateTables(tx *sql.Tx) error {
	// Delete in reverse dependency order
	tables := []string{"comments", "attachments", "tasks", "containers"}

	for _, table := range tables {
		if _, err := tx.Exec(fmt.Sprintf("DELETE FROM %s", table)); err != nil {
			return fmt.Errorf("failed to truncate %s: %w", table, err)
		}
	}

	// Reset sequences
	seqTables := []string{"container_seq", "task_seq", "attachment_seq"}
	for _, seq := range seqTables {
		if _, err := tx.Exec(fmt.Sprintf("DELETE FROM %s", seq)); err != nil {
			return fmt.Errorf("failed to reset %s: %w", seq, err)
		}
	}

	// Reset comment sequence
	if _, err := tx.Exec("UPDATE comment_sequences SET value = 0 WHERE name = 'next_comment'"); err != nil {
		return fmt.Errorf("failed to reset comment sequence: %w", err)
	}

	return nil
}

func importContainers(tx *sql.Tx, snap *Snapshot) error {
	// Build dependency graph and import in topological order
	// (parents before children)
	ordered := topologicalSortContainers(snap.Containers)

	stmt, err := tx.Prepare(`
		INSERT INTO containers (uuid, id, slug, title, parent_uuid, etag,
		                        created_at, updated_at, archived_at,
		                        created_by_principal_ref, updated_by_principal_ref)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uuid) DO UPDATE SET
			id = excluded.id,
			slug = excluded.slug,
			title = excluded.title,
			parent_uuid = excluded.parent_uuid,
			etag = excluded.etag,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at,
			archived_at = excluded.archived_at,
			created_by_principal_ref = excluded.created_by_principal_ref,
			updated_by_principal_ref = excluded.updated_by_principal_ref
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, uuid := range ordered {
		container := snap.Containers[uuid]

		var parentUUID, archivedAt, createdByPrincipal, updatedByPrincipal interface{}
		if container.ParentUUID != "" {
			parentUUID = container.ParentUUID
		}
		if container.ArchivedAt != "" {
			archivedAt = container.ArchivedAt
		}
		if container.CreatedByPrincipalRef != "" {
			createdByPrincipal = container.CreatedByPrincipalRef
		}
		if container.UpdatedByPrincipalRef != "" {
			updatedByPrincipal = container.UpdatedByPrincipalRef
		}

		if _, err := stmt.Exec(uuid, container.ID, container.Slug, container.Title,
			parentUUID, container.ETag, container.CreatedAt, container.UpdatedAt,
			archivedAt, createdByPrincipal, updatedByPrincipal); err != nil {
			return fmt.Errorf("failed to import container %s: %w", uuid, err)
		}
	}

	return nil
}

func topologicalSortContainers(containers map[string]ContainerEntry) []string {
	// Build adjacency list
	children := make(map[string][]string)
	roots := make([]string, 0)

	for uuid, container := range containers {
		if container.ParentUUID == "" {
			roots = append(roots, uuid)
		} else {
			children[container.ParentUUID] = append(children[container.ParentUUID], uuid)
		}
	}

	// Sort roots for determinism
	sort.Strings(roots)

	// BFS to get topological order
	result := make([]string, 0, len(containers))
	queue := roots

	for len(queue) > 0 {
		uuid := queue[0]
		queue = queue[1:]
		result = append(result, uuid)

		// Sort children for determinism
		childList := children[uuid]
		sort.Strings(childList)
		queue = append(queue, childList...)
	}

	return result
}

func importTasks(tx *sql.Tx, snap *Snapshot) error {
	uuids := make([]string, 0, len(snap.Tasks))
	for uuid := range snap.Tasks {
		uuids = append(uuids, uuid)
	}
	sort.Strings(uuids)

	stmt, err := tx.Prepare(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, requested_by_project_id,
		                   assigned_project_id, acknowledged_at, resolution,
		                   workflow_preset, preset_version, phase, risk_class,
		                   state, priority,
		                   start_at, due_at, labels, description, specification, etag,
		                   created_at, updated_at, completed_at, archived_at,
		                   created_by_principal_ref, updated_by_principal_ref)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uuid) DO UPDATE SET
			id = excluded.id,
			slug = excluded.slug,
			title = excluded.title,
			project_uuid = excluded.project_uuid,
			requested_by_project_id = excluded.requested_by_project_id,
			assigned_project_id = excluded.assigned_project_id,
			acknowledged_at = excluded.acknowledged_at,
			resolution = excluded.resolution,
			workflow_preset = excluded.workflow_preset,
			preset_version = excluded.preset_version,
			phase = excluded.phase,
			risk_class = excluded.risk_class,
			state = excluded.state,
			priority = excluded.priority,
			start_at = excluded.start_at,
			due_at = excluded.due_at,
			labels = excluded.labels,
			description = excluded.description,
			specification = excluded.specification,
			etag = excluded.etag,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at,
			completed_at = excluded.completed_at,
			archived_at = excluded.archived_at,
			created_by_principal_ref = excluded.created_by_principal_ref,
			updated_by_principal_ref = excluded.updated_by_principal_ref
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, uuid := range uuids {
		task := snap.Tasks[uuid]

		var startAt, dueAt, labels, completedAt, archivedAt interface{}
		var requestedBy, assignedProject, acknowledgedAt, resolution interface{}
		var workflowPreset, presetVersion, phase, riskClass interface{}
		var createdByPrincipal, updatedByPrincipal interface{}
		if task.CreatedByPrincipalRef != "" {
			createdByPrincipal = task.CreatedByPrincipalRef
		}
		if task.UpdatedByPrincipalRef != "" {
			updatedByPrincipal = task.UpdatedByPrincipalRef
		}
		if task.RequestedByProjectID != "" {
			requestedBy = task.RequestedByProjectID
		}
		if task.AssignedProjectID != "" {
			assignedProject = task.AssignedProjectID
		}
		if task.AcknowledgedAt != "" {
			acknowledgedAt = task.AcknowledgedAt
		}
		if task.Resolution != "" {
			resolution = task.Resolution
		}
		if task.WorkflowPreset != "" {
			workflowPreset = task.WorkflowPreset
		}
		if task.PresetVersion > 0 {
			presetVersion = task.PresetVersion
		}
		if task.Phase != "" {
			phase = task.Phase
		}
		if task.RiskClass != "" {
			riskClass = task.RiskClass
		}
		if task.StartAt != "" {
			startAt = task.StartAt
		}
		if task.DueAt != "" {
			dueAt = task.DueAt
		}
		if len(task.Labels) > 0 {
			// Sort labels for determinism
			sortedLabels := make([]string, len(task.Labels))
			copy(sortedLabels, task.Labels)
			sort.Strings(sortedLabels)
			labelsJSON, _ := json.Marshal(sortedLabels)
			labels = string(labelsJSON)
		}
		if task.CompletedAt != "" {
			completedAt = task.CompletedAt
		}
		if task.ArchivedAt != "" {
			archivedAt = task.ArchivedAt
		}

		description := task.Description
		if description == "" {
			description = "" // Ensure empty string, not NULL
		}
		specification := task.Specification
		if specification == "" {
			specification = "" // Ensure empty string, not NULL
		}

		if _, err := stmt.Exec(uuid, task.ID, task.Slug, task.Title, task.ProjectUUID,
			requestedBy, assignedProject, acknowledgedAt, resolution,
			workflowPreset, presetVersion, phase, riskClass,
			task.State, task.Priority,
			startAt, dueAt, labels, description, specification, task.ETag,
			task.CreatedAt, task.UpdatedAt, completedAt, archivedAt,
			createdByPrincipal, updatedByPrincipal); err != nil {
			return fmt.Errorf("failed to import task %s: %w", uuid, err)
		}
	}

	return nil
}

func importComments(tx *sql.Tx, snap *Snapshot) error {
	uuids := make([]string, 0, len(snap.Comments))
	for uuid := range snap.Comments {
		uuids = append(uuids, uuid)
	}
	sort.Strings(uuids)

	stmt, err := tx.Prepare(`
		INSERT INTO comments (uuid, id, task_uuid, created_by_principal_ref, body, meta, etag,
		                      created_at, updated_at, deleted_at, deleted_by_principal_ref)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uuid) DO UPDATE SET
			id = excluded.id,
			task_uuid = excluded.task_uuid,
			created_by_principal_ref = excluded.created_by_principal_ref,
			body = excluded.body,
			meta = excluded.meta,
			etag = excluded.etag,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at,
			deleted_at = excluded.deleted_at,
			deleted_by_principal_ref = excluded.deleted_by_principal_ref
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, uuid := range uuids {
		comment := snap.Comments[uuid]

		var createdByPrincipal, meta, updatedAt, deletedAt, deletedByPrincipal interface{}
		if comment.CreatedByPrincipalRef != "" {
			createdByPrincipal = comment.CreatedByPrincipalRef
		}
		if comment.Meta != "" {
			meta = comment.Meta
		}
		if comment.UpdatedAt != "" {
			updatedAt = comment.UpdatedAt
		}
		if comment.DeletedAt != "" {
			deletedAt = comment.DeletedAt
		}
		if comment.DeletedByPrincipalRef != "" {
			deletedByPrincipal = comment.DeletedByPrincipalRef
		}

		if _, err := stmt.Exec(uuid, comment.ID, comment.TaskUUID, createdByPrincipal,
			comment.Body, meta, comment.ETag, comment.CreatedAt, updatedAt,
			deletedAt, deletedByPrincipal); err != nil {
			return fmt.Errorf("failed to import comment %s: %w", uuid, err)
		}
	}

	// Update comment_sequences to stay in sync with MAX(id)
	// This prevents UNIQUE constraint violations on subsequent comment add operations
	var maxSeq int
	err = tx.QueryRow("SELECT COALESCE(MAX(CAST(SUBSTR(id, 3) AS INTEGER)), 0) FROM comments").Scan(&maxSeq)
	if err != nil {
		return fmt.Errorf("failed to get max comment ID: %w", err)
	}
	_, err = tx.Exec("UPDATE comment_sequences SET value = ? WHERE name = 'next_comment'", maxSeq)
	if err != nil {
		return fmt.Errorf("failed to update comment sequence: %w", err)
	}

	return nil
}

// Verify checks that a snapshot file is canonical (round-trip deterministic).
func Verify(db *sql.DB, inputPath string) (*VerifyResult, error) {
	// Load original snapshot
	origData, err := os.ReadFile(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read snapshot: %w", err)
	}

	var origSnap Snapshot
	if err := json.Unmarshal(origData, &origSnap); err != nil {
		return nil, fmt.Errorf("failed to parse snapshot: %w", err)
	}

	// Store original snapshot_rev for comparison
	origRev := origSnap.Meta.SnapshotRev

	// Clear snapshot_rev and generated_at for comparison
	// (these change on re-export)
	origSnap.Meta.SnapshotRev = ""
	origSnap.Meta.GeneratedAt = ""

	// Re-export to canonical JSON
	canonicalOrig, err := CanonicalJSON(&origSnap)
	if err != nil {
		return nil, fmt.Errorf("failed to canonicalize original: %w", err)
	}

	// Parse the canonical version
	var reloadedSnap Snapshot
	if err := json.Unmarshal(canonicalOrig, &reloadedSnap); err != nil {
		return nil, fmt.Errorf("failed to parse canonicalized snapshot: %w", err)
	}

	// Re-export the reloaded snapshot
	canonicalReloaded, err := CanonicalJSON(&reloadedSnap)
	if err != nil {
		return nil, fmt.Errorf("failed to re-canonicalize: %w", err)
	}

	// Compare bytes
	if string(canonicalOrig) != string(canonicalReloaded) {
		// Find first difference for diagnostics
		diff := findFirstDiff(string(canonicalOrig), string(canonicalReloaded))
		return &VerifyResult{
			InputPath:   inputPath,
			Valid:       false,
			SnapshotRev: origRev,
			Message:     fmt.Sprintf("round-trip failed: %s", diff),
		}, nil
	}

	return &VerifyResult{
		InputPath:   inputPath,
		Valid:       true,
		SnapshotRev: origRev,
		Message:     "snapshot is canonical",
	}, nil
}

func findFirstDiff(a, b string) string {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}

	for i := 0; i < minLen; i++ {
		if a[i] != b[i] {
			start := i - 20
			if start < 0 {
				start = 0
			}
			end := i + 20
			if end > minLen {
				end = minLen
			}
			return fmt.Sprintf("difference at byte %d: ...%s... vs ...%s...",
				i, strings.ReplaceAll(a[start:end], "\n", "\\n"),
				strings.ReplaceAll(b[start:end], "\n", "\\n"))
		}
	}

	if len(a) != len(b) {
		return fmt.Sprintf("length mismatch: %d vs %d", len(a), len(b))
	}

	return "unknown difference"
}
