package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	adornmentsRootUUID     = "00000000-0000-4000-8000-000000000001"
	adornmentsProjectUUID  = "10000000-0000-4000-8000-000000000001"
	adornmentsCampaignUUID = "10000000-0000-4000-8000-000000000002"
	adornmentsTaskUUID     = "20000000-0000-4000-8000-000000000001"
	adornmentsCommentUUID  = "30000000-0000-4000-8000-000000000001"
)

func openPreAdornmentsFixture(t *testing.T) *DB {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "pre-adornments.db"))
	if err != nil {
		t.Fatalf("open isolated database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if _, err := database.Exec(`
		CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		)
	`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	appliedClaimMigration := false
	for _, name := range names {
		if strings.HasPrefix(name, "000049_") {
			break
		}
		content, err := migrationsFS.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := database.applyMigration(name, content); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
		if name == "000048_task_claim_authority.sql" {
			appliedClaimMigration = true
		}
	}
	if !appliedClaimMigration {
		t.Fatal("fixture did not reach 000048_task_claim_authority.sql")
	}

	if _, err := database.Exec(`
		INSERT INTO containers (
			uuid, id, parent_uuid, slug, title, description, kind,
			created_by_principal_ref, updated_by_principal_ref
		) VALUES
			(?, 'P-91001', ?, 'legacy-project', 'Legacy Project', 'legacy container body', 'project',
			 'agent:legacy', 'agent:legacy'),
			(?, 'P-91002', ?, 'campaign-target', 'Campaign Target', 'campaign body', 'project',
			 'agent:legacy', 'agent:legacy');

		INSERT INTO tasks (
			uuid, id, slug, title, project_uuid, state, priority, kind,
			description, specification, created_by_principal_ref, updated_by_principal_ref
		) VALUES (
			?, 'T-91001', 'legacy-task', 'Legacy Task', ?, 'open', 2, 'task',
			'legacy task body', 'legacy task spec', 'agent:legacy', 'agent:legacy'
		);

		INSERT INTO comments (
			uuid, id, task_uuid, created_by_principal_ref, created_by_scope_ref,
			body, meta, etag, created_at, updated_at, deleted_at,
			deleted_by_principal_ref, deleted_by_scope_ref
		) VALUES (
			?, 'C-91001', ?, 'agent:legacy-author', 'agent:legacy-author:project:wrkq',
			'legacy comment body', '{"legacy":true}', 7, '2026-07-20 10:00:00',
			'2026-07-20 11:00:00', '2026-07-20 12:00:00',
			'agent:legacy-deleter', 'agent:legacy-deleter:project:wrkq'
		)
	`, adornmentsProjectUUID, adornmentsRootUUID,
		adornmentsCampaignUUID, adornmentsRootUUID,
		adornmentsTaskUUID, adornmentsProjectUUID,
		adornmentsCommentUUID, adornmentsTaskUUID); err != nil {
		t.Fatalf("seed prod-shaped legacy rows: %v", err)
	}

	return database
}

func applyAdornmentsMigration(t *testing.T, database *DB) string {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	var matches []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "000049_") && strings.HasSuffix(entry.Name(), ".sql") {
			matches = append(matches, entry.Name())
		}
	}
	if len(matches) != 1 {
		t.Fatalf("000049 migration count=%d, want exactly 1", len(matches))
	}
	content, err := migrationsFS.ReadFile(filepath.Join("migrations", matches[0]))
	if err != nil {
		t.Fatalf("read %s: %v", matches[0], err)
	}
	if err := database.applyMigration(matches[0], content); err != nil {
		t.Fatalf("apply %s: %v", matches[0], err)
	}
	return matches[0]
}

func tableCounts(t *testing.T, database *DB) map[string]int {
	t.Helper()
	counts := make(map[string]int, 3)
	for _, table := range []string{"comments", "tasks", "containers"} {
		var count int
		if err := database.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = count
	}
	return counts
}

func TestAdornmentsMigrationPreservesLegacyRowsAndAddsNullableColumns(t *testing.T) {
	database := openPreAdornmentsFixture(t)
	before := tableCounts(t, database)
	applyAdornmentsMigration(t, database)

	after := tableCounts(t, database)
	for _, table := range []string{"comments", "tasks", "containers"} {
		if after[table] != before[table] {
			t.Errorf("%s row count after migration=%d, want %d", table, after[table], before[table])
		}
	}

	var taskTitle, taskDescription, taskSpecification string
	var outcome, campaignUUID sql.NullString
	if err := database.QueryRow(`
		SELECT title, description, specification, outcome, campaign_uuid
		FROM tasks WHERE uuid = ?
	`, adornmentsTaskUUID).Scan(&taskTitle, &taskDescription, &taskSpecification, &outcome, &campaignUUID); err != nil {
		t.Fatalf("read migrated task: %v", err)
	}
	if taskTitle != "Legacy Task" || taskDescription != "legacy task body" || taskSpecification != "legacy task spec" {
		t.Errorf("migrated task content=%q/%q/%q, want legacy values", taskTitle, taskDescription, taskSpecification)
	}
	if outcome.Valid || campaignUUID.Valid {
		t.Errorf("migrated task adornments outcome/campaign=%v/%v, want NULL/NULL", outcome, campaignUUID)
	}

	var containerTitle, containerDescription, containerKind string
	var archivedAt, campaignState, containerSpecification sql.NullString
	if err := database.QueryRow(`
		SELECT title, description, kind, archived_at, campaign_state, specification
		FROM containers WHERE uuid = ?
	`, adornmentsProjectUUID).Scan(
		&containerTitle, &containerDescription, &containerKind, &archivedAt, &campaignState, &containerSpecification,
	); err != nil {
		t.Fatalf("read migrated container: %v", err)
	}
	if containerTitle != "Legacy Project" || containerDescription != "legacy container body" || containerKind != "project" {
		t.Errorf("migrated container content=%q/%q/%q, want legacy values", containerTitle, containerDescription, containerKind)
	}
	if archivedAt.Valid || campaignState.Valid || containerSpecification.Valid {
		t.Errorf("migrated container archived/campaign/specification=%v/%v/%v, want NULL/NULL/NULL", archivedAt, campaignState, containerSpecification)
	}

	var taskUUID, containerUUID, kind sql.NullString
	var body, meta, createdPrincipal, createdScope, createdAt, updatedAt, deletedAt, deletedPrincipal, deletedScope string
	var etag int64
	if err := database.QueryRow(`
		SELECT task_uuid, container_uuid, kind, body, meta, etag, created_at, updated_at, deleted_at,
		       created_by_principal_ref, created_by_scope_ref, deleted_by_principal_ref, deleted_by_scope_ref
		FROM comments WHERE uuid = ?
	`, adornmentsCommentUUID).Scan(
		&taskUUID, &containerUUID, &kind, &body, &meta, &etag, &createdAt, &updatedAt, &deletedAt,
		&createdPrincipal, &createdScope, &deletedPrincipal, &deletedScope,
	); err != nil {
		t.Fatalf("read migrated comment: %v", err)
	}
	if !taskUUID.Valid || taskUUID.String != adornmentsTaskUUID || containerUUID.Valid || kind.Valid {
		t.Errorf("migrated comment parents/kind=%v/%v/%v, want task/NULL/NULL", taskUUID, containerUUID, kind)
	}
	if body != "legacy comment body" || meta != `{"legacy":true}` || etag != 7 ||
		createdAt != "2026-07-20 10:00:00" || updatedAt != "2026-07-20 11:00:00" || deletedAt != "2026-07-20 12:00:00" ||
		createdPrincipal != "agent:legacy-author" || createdScope != "agent:legacy-author:project:wrkq" ||
		deletedPrincipal != "agent:legacy-deleter" || deletedScope != "agent:legacy-deleter:project:wrkq" {
		t.Errorf("migrated comment fields were not preserved")
	}
}

func TestAdornmentsMigrationEnforcesParentsRestrictAndTouchAttribution(t *testing.T) {
	database := openPreAdornmentsFixture(t)
	applyAdornmentsMigration(t, database)

	if _, err := database.Exec(`INSERT INTO comments (uuid, id, body) VALUES ('30000000-0000-4000-8000-000000000010', 'C-91010', 'no parent')`); err == nil {
		t.Fatal("comment with neither parent inserted; want exactly-one CHECK failure")
	}
	if _, err := database.Exec(`
		INSERT INTO comments (uuid, id, task_uuid, container_uuid, body)
		VALUES ('30000000-0000-4000-8000-000000000011', 'C-91011', ?, ?, 'two parents')
	`, adornmentsTaskUUID, adornmentsProjectUUID); err == nil {
		t.Fatal("comment with both parents inserted; want exactly-one CHECK failure")
	}

	assertParentTouchLifecycle(t, database, "task", adornmentsTaskUUID, "task_uuid", "C-91012")
	assertParentTouchLifecycle(t, database, "container", adornmentsProjectUUID, "container_uuid", "C-91013")

	if !hasIndexColumns(t, database, "comments", []string{"task_uuid", "created_at", "id"}) {
		t.Error("comments missing (task_uuid, created_at, id) index")
	}
	if !hasIndexColumns(t, database, "comments", []string{"container_uuid", "created_at", "id"}) {
		t.Error("comments missing (container_uuid, created_at, id) index")
	}
	if !hasIndexColumns(t, database, "tasks", []string{"campaign_uuid"}) {
		t.Error("tasks missing campaign_uuid index")
	}

	for _, state := range []interface{}{nil, "active", "completed", "cancelled"} {
		if _, err := database.Exec(`UPDATE containers SET campaign_state = ? WHERE uuid = ?`, state, adornmentsCampaignUUID); err != nil {
			t.Errorf("campaign_state %v rejected: %v", state, err)
		}
	}
	if _, err := database.Exec(`UPDATE containers SET campaign_state = 'paused' WHERE uuid = ?`, adornmentsCampaignUUID); err == nil {
		t.Fatal("invalid campaign_state inserted; want CHECK failure")
	}

	if _, err := database.Exec(`UPDATE tasks SET outcome = 'durable result', campaign_uuid = ? WHERE uuid = ?`, adornmentsCampaignUUID, adornmentsTaskUUID); err != nil {
		t.Fatalf("set task adornments: %v", err)
	}
	if _, err := database.Exec(`DELETE FROM containers WHERE uuid = ?`, adornmentsCampaignUUID); err == nil {
		t.Fatal("deleted enrolled campaign container; want campaign_uuid ON DELETE RESTRICT failure")
	}
	var campaignCount int
	if err := database.QueryRow(`SELECT COUNT(*) FROM containers WHERE uuid = ?`, adornmentsCampaignUUID).Scan(&campaignCount); err != nil {
		t.Fatalf("count restricted campaign: %v", err)
	}
	if campaignCount != 1 {
		t.Fatalf("campaign count after restricted delete=%d, want 1", campaignCount)
	}
}

func assertParentTouchLifecycle(t *testing.T, database *DB, parentTable, parentUUID, parentColumn, commentID string) {
	t.Helper()
	commentUUID := "touch-" + commentID
	insertPrincipal := "agent:" + parentTable + "-insert"
	insertScope := insertPrincipal + ":project:wrkq"
	updatePrincipal := "agent:" + parentTable + "-update"
	updateScope := updatePrincipal + ":project:wrkq"

	query := fmt.Sprintf(`
		INSERT INTO comments (uuid, id, %s, created_by_principal_ref, created_by_scope_ref, body)
		VALUES (?, ?, ?, ?, ?, 'touch lifecycle')
	`, parentColumn)
	if _, err := database.Exec(query, commentUUID, commentID, parentUUID, insertPrincipal, insertScope); err != nil {
		t.Fatalf("insert %s comment: %v", parentTable, err)
	}
	assertParentAttribution(t, database, parentTable, parentUUID, insertPrincipal, insertScope, "insert")

	if _, err := database.Exec(`
		UPDATE comments
		SET deleted_at = '2026-07-22 12:00:00', deleted_by_principal_ref = ?, deleted_by_scope_ref = ?
		WHERE uuid = ?
	`, updatePrincipal, updateScope, commentUUID); err != nil {
		t.Fatalf("update %s comment: %v", parentTable, err)
	}
	assertParentAttribution(t, database, parentTable, parentUUID, updatePrincipal, updateScope, "update")

	if _, err := database.Exec(`DELETE FROM comments WHERE uuid = ?`, commentUUID); err != nil {
		t.Fatalf("delete %s comment: %v", parentTable, err)
	}
	assertParentAttribution(t, database, parentTable, parentUUID, insertPrincipal, insertScope, "delete")
}

func assertParentAttribution(t *testing.T, database *DB, table, uuid, wantPrincipal, wantScope, operation string) {
	t.Helper()
	var principal, scope sql.NullString
	query := fmt.Sprintf("SELECT updated_by_principal_ref, updated_by_scope_ref FROM %s WHERE uuid = ?", table+"s")
	if err := database.QueryRow(query, uuid).Scan(&principal, &scope); err != nil {
		t.Fatalf("read %s attribution after comment %s: %v", table, operation, err)
	}
	if !principal.Valid || principal.String != wantPrincipal || !scope.Valid || scope.String != wantScope {
		t.Errorf("%s attribution after comment %s=%v/%v, want %q/%q", table, operation, principal, scope, wantPrincipal, wantScope)
	}
}

func hasIndexColumns(t *testing.T, database *DB, table string, want []string) bool {
	t.Helper()
	rows, err := database.Query(fmt.Sprintf("PRAGMA index_list('%s')", table))
	if err != nil {
		t.Fatalf("list %s indexes: %v", table, err)
	}
	defer func() { _ = rows.Close() }()

	var indexNames []string
	for rows.Next() {
		var seq, unique, partial int
		var name, origin string
		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan %s index: %v", table, err)
		}
		indexNames = append(indexNames, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s indexes: %v", table, err)
	}

	for _, name := range indexNames {
		columnRows, err := database.Query(fmt.Sprintf("PRAGMA index_info('%s')", strings.ReplaceAll(name, "'", "''")))
		if err != nil {
			t.Fatalf("inspect index %s: %v", name, err)
		}
		var got []string
		for columnRows.Next() {
			var seq, cid int
			var column string
			if err := columnRows.Scan(&seq, &cid, &column); err != nil {
				_ = columnRows.Close()
				t.Fatalf("scan index %s column: %v", name, err)
			}
			got = append(got, column)
		}
		if err := columnRows.Close(); err != nil {
			t.Fatalf("close index %s columns: %v", name, err)
		}
		if strings.Join(got, ",") == strings.Join(want, ",") {
			return true
		}
	}
	return false
}

func TestAdornmentsMigrationGuardsTypedCommentVocabulary(t *testing.T) {
	database := openPreAdornmentsFixture(t)
	applyAdornmentsMigration(t, database)

	var legacyKind sql.NullString
	if err := database.QueryRow(`SELECT kind FROM comments WHERE uuid = ?`, adornmentsCommentUUID).Scan(&legacyKind); err != nil {
		t.Fatalf("read migrated legacy kind: %v", err)
	}
	if legacyKind.Valid {
		t.Fatalf("migrated legacy kind=%q, want NULL", legacyKind.String)
	}

	allowedKinds := []interface{}{nil, "blocker", "decision", "postmortem", "digest"}
	for i, kind := range allowedKinds {
		if _, err := database.Exec(`
			INSERT INTO comments (uuid, id, task_uuid, kind, body)
			VALUES (?, ?, ?, ?, 'allowed typed comment')
		`, fmt.Sprintf("allowed-kind-%d", i), fmt.Sprintf("C-9200%d", i), adornmentsTaskUUID, kind); err != nil {
			t.Errorf("allowed comment kind %v rejected: %v", kind, err)
		}
	}
	if _, err := database.Exec(`
		INSERT INTO comments (uuid, id, task_uuid, kind, body)
		VALUES ('arbitrary-kind', 'C-92009', ?, 'heartbeat', 'forbidden typed comment')
	`, adornmentsTaskUUID); err == nil {
		t.Fatal("arbitrary comment kind inserted; want DB vocabulary CHECK failure")
	}
}
