package snapshot

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	dbsync "github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
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

	// If dry run, just validate and return
	if opts.DryRun {
		return &ImportResult{
			InputPath:      opts.InputPath,
			SnapshotRev:    snap.Meta.SnapshotRev,
			ContainerCount: len(snap.Containers),
			TaskCount:      len(snap.Tasks),
			PromiseCount:   len(snap.Promises),
			CommentCount:   len(snap.Comments),
			LinkCount:      len(snap.Links),
			DryRun:         true,
		}, nil
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := checkImportTarget(tx, opts); err != nil {
		return nil, err
	}

	if err := restoreSnapshot(tx, &snap); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return &ImportResult{
		InputPath:      opts.InputPath,
		SnapshotRev:    snap.Meta.SnapshotRev,
		ContainerCount: len(snap.Containers),
		TaskCount:      len(snap.Tasks),
		PromiseCount:   len(snap.Promises),
		CommentCount:   len(snap.Comments),
		LinkCount:      len(snap.Links),
		DryRun:         false,
	}, nil
}

// restoreSnapshot replaces the modelled tables with the snapshot's rows inside
// tx. Rows are written verbatim: foreign keys are deferred to commit so
// insertion order never matters, and comments go in before the tasks and
// containers they touch, so the comment touch triggers match no row and
// cannot bump a restored task's etag/updated_at.
func restoreSnapshot(tx *sql.Tx, snap *Snapshot) error {
	if _, err := tx.Exec("PRAGMA defer_foreign_keys = ON"); err != nil {
		return fmt.Errorf("failed to defer foreign keys: %w", err)
	}
	if err := clearModelledTables(tx); err != nil {
		return fmt.Errorf("failed to clear modelled tables: %w", err)
	}
	if err := importComments(tx, snap); err != nil {
		return fmt.Errorf("failed to import comments: %w", err)
	}
	if err := importContainers(tx, snap); err != nil {
		return fmt.Errorf("failed to import containers: %w", err)
	}
	if err := importTasks(tx, snap); err != nil {
		return fmt.Errorf("failed to import tasks: %w", err)
	}
	if err := importPromises(tx, snap); err != nil {
		return fmt.Errorf("failed to import promises: %w", err)
	}
	if err := importLinks(tx, snap); err != nil {
		return fmt.Errorf("failed to import links: %w", err)
	}
	if err := restoreSequences(tx, snap); err != nil {
		return fmt.Errorf("failed to restore sequences: %w", err)
	}
	return nil
}

func snapshotSequenceSpecs(tx *sql.Tx) ([]dbsync.SequenceSpec, error) {
	var projectEventsExists int
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'project_events')`).Scan(&projectEventsExists); err != nil {
		return nil, err
	}
	result := dbsync.DefaultSequenceSpecs()
	if projectEventsExists == 0 {
		for i, spec := range result {
			if spec.EntityTable == "project_events" {
				result = append(result[:i], result[i+1:]...)
				break
			}
		}
	}
	return result, nil
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

	// The singleton root is part of every ledger; import restores it in place.
	root, ok := snap.Containers[domain.RootContainerUUID]
	if !ok {
		return fmt.Errorf("snapshot has no root container %s", domain.RootContainerUUID)
	}
	if root.Kind != "root" {
		return fmt.Errorf("container %s must have kind root, has %q", domain.RootContainerUUID, root.Kind)
	}

	// Every cross-entity reference must resolve inside the snapshot.
	for uuid, container := range snap.Containers {
		if container.Kind == "" {
			return fmt.Errorf("container %s has no kind", uuid)
		}
		if container.Kind == "root" && uuid != domain.RootContainerUUID {
			return fmt.Errorf("container %s has kind root but is not the root container", uuid)
		}
		if container.ParentUUID != "" {
			if _, ok := snap.Containers[container.ParentUUID]; !ok {
				return fmt.Errorf("container %s references unknown parent %s", uuid, container.ParentUUID)
			}
		}
	}

	for uuid, task := range snap.Tasks {
		if _, ok := snap.Containers[task.ProjectUUID]; !ok {
			return fmt.Errorf("task %s references unknown container %s", uuid, task.ProjectUUID)
		}
		if task.CampaignUUID != "" {
			if _, ok := snap.Containers[task.CampaignUUID]; !ok {
				return fmt.Errorf("task %s references unknown campaign container %s", uuid, task.CampaignUUID)
			}
		}
		if task.SubtaskOwnerUUID != nil {
			owner, ok := snap.Tasks[*task.SubtaskOwnerUUID]
			if !ok || owner.SubtaskOwnerUUID != nil {
				return fmt.Errorf("task %s has invalid subtask owner", uuid)
			}
			if task.ProjectUUID != owner.ProjectUUID {
				return fmt.Errorf("task %s subtask residency differs from owner", uuid)
			}
		}
		if task.ParentTaskUUID != nil {
			if parent, ok := snap.Tasks[*task.ParentTaskUUID]; !ok || parent.SubtaskOwnerUUID != nil {
				return fmt.Errorf("task %s references unknown parent task %s", uuid, *task.ParentTaskUUID)
			}
		}
	}

	// Attached promises must reference a subject present in the same snapshot.
	for uuid, promise := range snap.Promises {
		if promise.SubjectTaskUUID != nil {
			if _, ok := snap.Tasks[*promise.SubjectTaskUUID]; !ok {
				return fmt.Errorf("promise %s references unknown task %s", uuid, *promise.SubjectTaskUUID)
			}
		}
		if promise.SubjectContainerUUID != nil {
			if _, ok := snap.Containers[*promise.SubjectContainerUUID]; !ok {
				return fmt.Errorf("promise %s references unknown container %s", uuid, *promise.SubjectContainerUUID)
			}
		}
		if promise.SubjectTaskUUID != nil && promise.SubjectContainerUUID != nil {
			return fmt.Errorf("promise %s references both a task and container", uuid)
		}
	}

	// Comments must reference exactly one valid task or container.
	for uuid, comment := range snap.Comments {
		hasTask := comment.TaskUUID != ""
		hasContainer := comment.ContainerUUID != ""
		if hasTask == hasContainer {
			return fmt.Errorf("comment %s must reference exactly one task or container", uuid)
		}
		if hasTask {
			if _, ok := snap.Tasks[comment.TaskUUID]; !ok {
				return fmt.Errorf("comment %s references unknown task %s", uuid, comment.TaskUUID)
			}
		}
		if hasContainer {
			if _, ok := snap.Containers[comment.ContainerUUID]; !ok {
				return fmt.Errorf("comment %s references unknown container %s", uuid, comment.ContainerUUID)
			}
		}
	}

	for key, link := range snap.Links {
		if key != LinkKey(link.SourceUUID, link.TargetUUID, link.LinkType) {
			return fmt.Errorf("link %s does not match its source|target|link_type", key)
		}
		if _, ok := snap.Tasks[link.SourceUUID]; !ok {
			return fmt.Errorf("link %s references unknown task %s", key, link.SourceUUID)
		}
		if _, ok := snap.Tasks[link.TargetUUID]; !ok {
			return fmt.Errorf("link %s references unknown task %s", key, link.TargetUUID)
		}
	}

	return nil
}

// modelledTables are the tables a snapshot represents in full.
var modelledTables = map[string]bool{
	"containers": true, "tasks": true, "comments": true, "promises": true, "task_relations": true,
}

// checkImportTarget refuses to import over existing ledger data unless Force
// is set, and refuses Force when rows outside the snapshot model reference
// the modelled rows it would delete, unless AllowCascade is also set.
func checkImportTarget(tx *sql.Tx, opts ImportOptions) error {
	if !opts.Force {
		occupied, err := ledgerOccupancy(tx)
		if err != nil {
			return fmt.Errorf("failed to check database: %w", err)
		}
		if occupied != "" {
			return fmt.Errorf("database is not empty (%s); use --force to replace the task ledger", occupied)
		}
	}
	if opts.AllowCascade {
		return nil
	}
	dependents, err := outOfModelDependents(tx)
	if err != nil {
		return fmt.Errorf("failed to check out-of-model rows: %w", err)
	}
	if len(dependents) > 0 {
		return fmt.Errorf("refusing to replace the task ledger: rows outside the snapshot model reference it and would be cascade-deleted or nulled (%s); pass --allow-cascade to proceed, never against a live ledger", strings.Join(dependents, ", "))
	}
	return nil
}

// ledgerOccupancy returns a description of the first ledger data found, or ""
// when the database holds only seeded defaults: the root plus at most one
// other container (the inbox `wrkqadm init` seeds).
func ledgerOccupancy(tx *sql.Tx) (string, error) {
	for _, table := range []string{"tasks", "comments", "promises", "task_relations"} {
		var count int
		if err := tx.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&count); err != nil {
			return "", err
		}
		if count > 0 {
			return fmt.Sprintf("%d %s", count, table), nil
		}
	}
	var containers int
	if err := tx.QueryRow("SELECT COUNT(*) FROM containers WHERE kind != 'root'").Scan(&containers); err != nil {
		return "", err
	}
	if containers > 1 {
		return fmt.Sprintf("%d non-root containers", containers), nil
	}
	return "", nil
}

// outOfModelDependents lists "table.column (n)" for every non-null foreign
// key from a table outside the snapshot model into a modelled table. The
// schema is read at run time, so a new referencing table is never missed.
func outOfModelDependents(tx *sql.Tx) ([]string, error) {
	rows, err := tx.Query(`
		SELECT m.name, fk."from", fk."table"
		  FROM sqlite_master m, pragma_foreign_key_list(m.name) fk
		 WHERE m.type = 'table'
		 ORDER BY m.name, fk."from"`)
	if err != nil {
		return nil, err
	}
	type ref struct{ table, column string }
	var refs []ref
	for rows.Next() {
		var table, column, target string
		if err := rows.Scan(&table, &column, &target); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if !modelledTables[table] && modelledTables[target] {
			refs = append(refs, ref{table, column})
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	var dependents []string
	for _, r := range refs {
		var count int
		query := fmt.Sprintf(`SELECT COUNT(*) FROM "%s" WHERE "%s" IS NOT NULL`, r.table, r.column)
		if err := tx.QueryRow(query).Scan(&count); err != nil {
			return nil, err
		}
		if count > 0 {
			dependents = append(dependents, fmt.Sprintf("%s.%s (%d)", r.table, r.column, count))
		}
	}
	return dependents, nil
}

// clearModelledTables deletes every modelled row except the singleton root,
// which cannot be deleted and is restored in place.
func clearModelledTables(tx *sql.Tx) error {
	for _, stmt := range []string{
		"DELETE FROM task_relations",
		"DELETE FROM comments",
		"DELETE FROM promises",
		"DELETE FROM tasks",
		"DELETE FROM containers WHERE kind != 'root'",
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return nil
}

// restoreSequences raises each friendly-id sequence to the larger of the
// snapshot's high-water mark and the largest restored canonical id; it never
// lowers a sequence.
func restoreSequences(tx *sql.Tx, snap *Snapshot) error {
	specs, err := snapshotSequenceSpecs(tx)
	if err != nil {
		return err
	}
	if _, err := dbsync.FixSequenceDrifts(tx, specs); err != nil {
		return err
	}
	for seqTable, name := range snapshotSequenceNames {
		hw := snap.Meta.Sequences[name]
		if hw <= 0 {
			continue
		}
		res, err := tx.Exec("UPDATE sqlite_sequence SET seq = MAX(seq, ?) WHERE name = ?", hw, seqTable)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			if _, err := tx.Exec("INSERT INTO sqlite_sequence (name, seq) VALUES (?, ?)", seqTable, hw); err != nil {
				return err
			}
		}
	}
	comment, err := commentHighWater(tx)
	if err != nil {
		return err
	}
	if hw := snap.Meta.Sequences["comment"]; hw > comment {
		comment = hw
	}
	if _, err := tx.Exec(`UPDATE comment_sequences SET value = ? WHERE name = 'next_comment'`, comment); err != nil {
		return err
	}
	return nil
}

func importContainers(tx *sql.Tx, snap *Snapshot) error {
	uuids := make([]string, 0, len(snap.Containers))
	for uuid := range snap.Containers {
		if uuid != domain.RootContainerUUID {
			uuids = append(uuids, uuid)
		}
	}
	sort.Strings(uuids)

	stmt, err := tx.Prepare(`
		INSERT INTO containers (uuid, id, slug, title, kind, description, parent_uuid, sort_index,
		                        section_uuid, webhook_urls, root, specification, labels, campaign_state,
		                        etag, created_at, updated_at, archived_at,
		                        created_by_principal_ref, created_by_scope_ref,
		                        updated_by_principal_ref, updated_by_scope_ref)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, uuid := range uuids {
		c := snap.Containers[uuid]
		if _, err := stmt.Exec(uuid, c.ID, c.Slug, c.Title, c.Kind, c.Description,
			nullableSnapshotString(c.ParentUUID), c.SortIndex,
			nullableSnapshotPointer(c.SectionUUID), nullableSnapshotPointer(c.WebhookURLs),
			nullableSnapshotPointer(c.Root), nullableSnapshotPointer(c.Specification),
			nullableSnapshotPointer(c.Labels), nullableSnapshotPointer(c.CampaignState),
			c.ETag, c.CreatedAt, c.UpdatedAt, nullableSnapshotString(c.ArchivedAt),
			nullableSnapshotString(c.CreatedByPrincipalRef), nullableSnapshotPointer(c.CreatedByScopeRef),
			nullableSnapshotString(c.UpdatedByPrincipalRef), nullableSnapshotPointer(c.UpdatedByScopeRef)); err != nil {
			return fmt.Errorf("failed to import container %s: %w", uuid, err)
		}
	}

	return restoreRootContainer(tx, snap.Containers[domain.RootContainerUUID])
}

// restoreRootContainer overwrites the mutable columns of the migration-seeded
// root in place. Its identity columns (parent, slug, kind, archived_at) are
// immutable by trigger and fixed by migration. containers_au_touch would
// stamp updated_at with the import time, so it is suspended for this one
// UPDATE; the DROP and CREATE both live in tx and roll back with it.
func restoreRootContainer(tx *sql.Tx, root ContainerEntry) error {
	const touchTrigger = "containers_au_touch"
	var triggerSQL string
	err := tx.QueryRow("SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = ?", touchTrigger).Scan(&triggerSQL)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if triggerSQL != "" {
		if _, err := tx.Exec("DROP TRIGGER " + touchTrigger); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`
		UPDATE containers
		   SET id = ?, title = ?, description = ?, sort_index = ?, section_uuid = ?,
		       webhook_urls = ?, root = ?, specification = ?, labels = ?, campaign_state = ?,
		       etag = ?, created_at = ?, updated_at = ?,
		       created_by_principal_ref = ?, created_by_scope_ref = ?,
		       updated_by_principal_ref = ?, updated_by_scope_ref = ?
		 WHERE uuid = ?`,
		root.ID, root.Title, root.Description, root.SortIndex, nullableSnapshotPointer(root.SectionUUID),
		nullableSnapshotPointer(root.WebhookURLs), nullableSnapshotPointer(root.Root),
		nullableSnapshotPointer(root.Specification), nullableSnapshotPointer(root.Labels),
		nullableSnapshotPointer(root.CampaignState),
		root.ETag, root.CreatedAt, root.UpdatedAt,
		nullableSnapshotString(root.CreatedByPrincipalRef), nullableSnapshotPointer(root.CreatedByScopeRef),
		nullableSnapshotString(root.UpdatedByPrincipalRef), nullableSnapshotPointer(root.UpdatedByScopeRef),
		domain.RootContainerUUID); err != nil {
		return fmt.Errorf("failed to restore root container: %w", err)
	}
	if triggerSQL != "" {
		if _, err := tx.Exec(triggerSQL); err != nil {
			return fmt.Errorf("failed to recreate %s: %w", touchTrigger, err)
		}
	}
	return nil
}

func importTasks(tx *sql.Tx, snap *Snapshot) error {
	uuids := make([]string, 0, len(snap.Tasks))
	for uuid := range snap.Tasks {
		uuids = append(uuids, uuid)
	}
	sort.Slice(uuids, func(i, j int) bool {
		a, b := snap.Tasks[uuids[i]], snap.Tasks[uuids[j]]
		if (a.SubtaskOwnerUUID == nil) != (b.SubtaskOwnerUUID == nil) {
			return a.SubtaskOwnerUUID == nil
		}
		return uuids[i] < uuids[j]
	})

	stmt, err := tx.Prepare(`
		INSERT INTO tasks (uuid, id, slug, title, kind, project_uuid, campaign_uuid, parent_task_uuid, subtask_owner_uuid,
		                   requested_by_project_id, assigned_project_id, acknowledged_at, resolution,
		                   workflow_preset, preset_version, phase, risk_class,
		                   state, priority, assignee_principal_ref,
		                   start_at, due_at, labels, meta, outcome, description, specification, etag,
		                   created_at, updated_at, completed_at, archived_at,
		                   deleted_at, deleted_by_principal_ref, deleted_by_scope_ref,
		                   created_by_principal_ref, created_by_scope_ref,
		                   updated_by_principal_ref, updated_by_scope_ref,
		                   cp_project_id, cp_run_id, cp_session_id, cp_work_item_id,
		                   sdk_session_id, run_status,
		                   claimed_by_principal_ref, claimed_scope_ref, claimed_node, claimed_at,
		                   claim_token_hash, claim_generation,
		                   requester_principal_ref, requester_scope_ref)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		        ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, uuid := range uuids {
		t := snap.Tasks[uuid]

		var presetVersion, labels interface{}
		if t.PresetVersion > 0 {
			presetVersion = t.PresetVersion
		}
		if len(t.Labels) > 0 {
			sortedLabels := append([]string(nil), t.Labels...)
			sort.Strings(sortedLabels)
			labelsJSON, _ := json.Marshal(sortedLabels)
			labels = string(labelsJSON)
		}

		if _, err := stmt.Exec(uuid, t.ID, t.Slug, t.Title, t.Kind, t.ProjectUUID,
			nullableSnapshotString(t.CampaignUUID), nullableSnapshotPointer(t.ParentTaskUUID), nullableSnapshotPointer(t.SubtaskOwnerUUID),
			nullableSnapshotString(t.RequestedByProjectID), nullableSnapshotString(t.AssignedProjectID),
			nullableSnapshotString(t.AcknowledgedAt), nullableSnapshotString(t.Resolution),
			nullableSnapshotString(t.WorkflowPreset), presetVersion,
			nullableSnapshotString(t.Phase), nullableSnapshotString(t.RiskClass),
			t.State, t.Priority, nullableSnapshotPointer(t.AssigneePrincipalRef),
			nullableSnapshotString(t.StartAt), nullableSnapshotString(t.DueAt), labels,
			nullableSnapshotPointer(t.Meta), nullableSnapshotPointer(t.Outcome),
			t.Description, t.Specification, t.ETag,
			t.CreatedAt, t.UpdatedAt, nullableSnapshotString(t.CompletedAt), nullableSnapshotString(t.ArchivedAt),
			nullableSnapshotPointer(t.DeletedAt), nullableSnapshotPointer(t.DeletedByPrincipalRef),
			nullableSnapshotPointer(t.DeletedByScopeRef),
			nullableSnapshotString(t.CreatedByPrincipalRef), nullableSnapshotPointer(t.CreatedByScopeRef),
			nullableSnapshotString(t.UpdatedByPrincipalRef), nullableSnapshotPointer(t.UpdatedByScopeRef),
			nullableSnapshotPointer(t.CPProjectID), nullableSnapshotPointer(t.CPRunID),
			nullableSnapshotPointer(t.CPSessionID), nullableSnapshotPointer(t.CPWorkItemID),
			nullableSnapshotPointer(t.SDKSessionID), nullableSnapshotPointer(t.RunStatus),
			nullableSnapshotPointer(t.ClaimedByPrincipalRef), nullableSnapshotPointer(t.ClaimedScopeRef),
			nullableSnapshotPointer(t.ClaimedNode), nullableSnapshotPointer(t.ClaimedAt),
			nullableSnapshotPointer(t.ClaimTokenHash), t.ClaimGeneration,
			nullableSnapshotPointer(t.RequesterPrincipalRef), nullableSnapshotPointer(t.RequesterScopeRef)); err != nil {
			return fmt.Errorf("failed to import task %s: %w", uuid, err)
		}
	}

	return nil
}

func importLinks(tx *sql.Tx, snap *Snapshot) error {
	keys := make([]string, 0, len(snap.Links))
	for key := range snap.Links {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	stmt, err := tx.Prepare(`
		INSERT INTO task_relations (from_task_uuid, to_task_uuid, kind, meta, created_at,
		                            created_by_principal_ref, created_by_scope_ref)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, key := range keys {
		l := snap.Links[key]
		if _, err := stmt.Exec(l.SourceUUID, l.TargetUUID, l.LinkType, nullableSnapshotPointer(l.Meta),
			l.CreatedAt, nullableSnapshotString(l.CreatedByPrincipalRef),
			nullableSnapshotPointer(l.CreatedByScopeRef)); err != nil {
			return fmt.Errorf("failed to import link %s: %w", key, err)
		}
	}
	return nil
}

func importPromises(tx *sql.Tx, snap *Snapshot) error {
	uuids := make([]string, 0, len(snap.Promises))
	for uuid := range snap.Promises {
		uuids = append(uuids, uuid)
	}
	sort.Strings(uuids)

	stmt, err := tx.Prepare(`
		INSERT INTO promises (
			uuid, id, owner_principal_ref, subject, review_question,
			subject_task_uuid, subject_container_uuid, review_at, state,
			closed_at, last_reviewed_at, last_review_note, meta, etag,
			created_at, updated_at, created_by_principal_ref,
			created_by_scope_ref, updated_by_principal_ref, updated_by_scope_ref
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(uuid) DO UPDATE SET
			id = excluded.id,
			owner_principal_ref = excluded.owner_principal_ref,
			subject = excluded.subject,
			review_question = excluded.review_question,
			subject_task_uuid = excluded.subject_task_uuid,
			subject_container_uuid = excluded.subject_container_uuid,
			review_at = excluded.review_at,
			state = excluded.state,
			closed_at = excluded.closed_at,
			last_reviewed_at = excluded.last_reviewed_at,
			last_review_note = excluded.last_review_note,
			meta = excluded.meta,
			etag = excluded.etag,
			created_at = excluded.created_at,
			updated_at = excluded.updated_at,
			created_by_principal_ref = excluded.created_by_principal_ref,
			created_by_scope_ref = excluded.created_by_scope_ref,
			updated_by_principal_ref = excluded.updated_by_principal_ref,
			updated_by_scope_ref = excluded.updated_by_scope_ref
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, uuid := range uuids {
		promise := snap.Promises[uuid]
		if _, err := stmt.Exec(
			uuid, promise.ID, promise.OwnerPrincipalRef, promise.Subject,
			nullableSnapshotPointer(promise.ReviewQuestion),
			nullableSnapshotPointer(promise.SubjectTaskUUID),
			nullableSnapshotPointer(promise.SubjectContainerUUID),
			promise.ReviewAt, promise.State, nullableSnapshotPointer(promise.ClosedAt),
			nullableSnapshotPointer(promise.LastReviewedAt),
			nullableSnapshotPointer(promise.LastReviewNote), nullableSnapshotPointer(promise.Meta),
			promise.ETag, promise.CreatedAt, promise.UpdatedAt,
			promise.CreatedByPrincipalRef, nullableSnapshotPointer(promise.CreatedByScopeRef),
			promise.UpdatedByPrincipalRef, nullableSnapshotPointer(promise.UpdatedByScopeRef),
		); err != nil {
			return fmt.Errorf("failed to import promise %s: %w", uuid, err)
		}
	}
	return nil
}

func nullableSnapshotPointer(value *string) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func importComments(tx *sql.Tx, snap *Snapshot) error {
	uuids := make([]string, 0, len(snap.Comments))
	for uuid := range snap.Comments {
		uuids = append(uuids, uuid)
	}
	sort.Strings(uuids)

	stmt, err := tx.Prepare(`
		INSERT INTO comments (uuid, id, task_uuid, container_uuid, kind,
		                      created_by_principal_ref, created_by_scope_ref, created_by_host_session_id, created_by_generation, body, meta, etag,
		                      created_at, updated_at, deleted_at,
		                      deleted_by_principal_ref, deleted_by_scope_ref)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, uuid := range uuids {
		c := snap.Comments[uuid]
		if _, err := stmt.Exec(uuid, c.ID, nullableSnapshotString(c.TaskUUID),
			nullableSnapshotString(c.ContainerUUID), nullableSnapshotPointer(c.Kind),
			nullableSnapshotString(c.CreatedByPrincipalRef), nullableSnapshotPointer(c.CreatedByScopeRef),
			nullableSnapshotPointer(c.CreatedByHostSessionID), nullableSnapshotPointerInt64(c.CreatedByGeneration),
			c.Body, nullableSnapshotString(c.Meta), c.ETag, c.CreatedAt,
			nullableSnapshotString(c.UpdatedAt), nullableSnapshotString(c.DeletedAt),
			nullableSnapshotString(c.DeletedByPrincipalRef), nullableSnapshotPointer(c.DeletedByScopeRef)); err != nil {
			return fmt.Errorf("failed to import comment %s: %w", uuid, err)
		}
	}
	return nil
}

func nullableSnapshotString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
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

func nullableSnapshotPointerInt64(value *int64) interface{} {
	if value == nil {
		return nil
	}
	return *value
}
