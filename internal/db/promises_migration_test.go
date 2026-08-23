package db

import (
	"path/filepath"
	"strings"
	"testing"
)

func applyPromisesMigration(t *testing.T, database *DB) {
	t.Helper()
	content, err := migrationsFS.ReadFile(filepath.Join("migrations", "000051_promises.sql"))
	if err != nil {
		t.Fatalf("read promises migration: %v", err)
	}
	if err := database.applyMigration("000051_promises.sql", content); err != nil {
		t.Fatalf("apply promises migration: %v", err)
	}
}

func TestPromisesMigrationPreservesEventsAndWidensResourceType(t *testing.T) {
	database := openPreAdornmentsFixture(t)
	applyAdornmentsMigration(t, database)
	applyCampaignPortfolioMigration(t, database)

	result, err := database.Exec(`
		INSERT INTO event_log (
			timestamp, resource_type, resource_uuid, event_type, etag, payload,
			principal_ref, scope_ref
		) VALUES (
			'2026-08-23T12:00:00Z', 'task', ?, 'task.fixture', 7, '{"kept":true}',
			'agent:legacy', 'agent:legacy:project:wrkq'
		)
	`, adornmentsTaskUUID)
	if err != nil {
		t.Fatalf("seed event: %v", err)
	}
	eventID, _ := result.LastInsertId()

	applyPromisesMigration(t, database)

	var timestamp, resourceType, resourceUUID, eventType, payload, principal, scope string
	var etag int64
	if err := database.QueryRow(`
		SELECT timestamp, resource_type, resource_uuid, event_type, etag, payload,
		       principal_ref, scope_ref
		  FROM event_log WHERE id = ?
	`, eventID).Scan(&timestamp, &resourceType, &resourceUUID, &eventType, &etag, &payload, &principal, &scope); err != nil {
		t.Fatalf("read preserved event: %v", err)
	}
	if timestamp != "2026-08-23T12:00:00Z" || resourceType != "task" || resourceUUID != adornmentsTaskUUID ||
		eventType != "task.fixture" || etag != 7 || payload != `{"kept":true}` ||
		principal != "agent:legacy" || scope != "agent:legacy:project:wrkq" {
		t.Fatalf("event rebuild did not preserve all columns")
	}

	if _, err := database.Exec(`
		INSERT INTO event_log (resource_type, event_type) VALUES ('promise', 'promise.fixture')
	`); err != nil {
		t.Fatalf("promise event resource rejected: %v", err)
	}
	if _, err := database.Exec(`
		INSERT INTO event_log (resource_type, event_type) VALUES ('unknown', 'unknown.fixture')
	`); err == nil {
		t.Fatal("unknown event resource accepted")
	}
	for _, index := range []string{"event_log_resource_idx", "event_log_principal_idx", "event_log_scope_idx"} {
		var sqlText string
		if err := database.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&sqlText); err != nil {
			t.Fatalf("read rebuilt event index %s: %v", index, err)
		}
		if index != "event_log_resource_idx" && !strings.Contains(strings.ToUpper(sqlText), " WHERE ") {
			t.Errorf("rebuilt event index %s lost its partial predicate: %s", index, sqlText)
		}
	}
}

func TestPromisesFreshSchemaContract(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "fresh-promises.db"))
	if err != nil {
		t.Fatalf("open isolated database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate isolated database: %v", err)
	}

	var applied int
	if err := database.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = '000051_promises.sql'`).Scan(&applied); err != nil {
		t.Fatalf("read migration ledger: %v", err)
	}
	if applied != 1 {
		t.Fatalf("000051 migration count = %d, want 1", applied)
	}

	var tableSQL string
	if err := database.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'promises'`).Scan(&tableSQL); err != nil {
		t.Fatalf("read promises schema: %v", err)
	}
	for _, fragment := range []string{
		"owner_principal_ref TEXT NOT NULL",
		"subject_task_uuid TEXT",
		"subject_container_uuid TEXT",
		"review_at GLOB",
		"'resolved'",
		"'abandoned'",
		"closed_at IS NOT NULL",
	} {
		if !strings.Contains(tableSQL, fragment) {
			t.Errorf("promises schema missing %q: %s", fragment, tableSQL)
		}
	}

	for _, index := range []string{"promises_owner_ready_idx", "promises_task_idx", "promises_container_idx"} {
		var sqlText string
		if err := database.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&sqlText); err != nil {
			t.Fatalf("read index %s: %v", index, err)
		}
		if !strings.Contains(strings.ToUpper(sqlText), " WHERE ") {
			t.Errorf("index %s is not partial: %s", index, sqlText)
		}
	}

	insert := func(subject, reviewAt, state string, closedAt interface{}, taskUUID, containerUUID interface{}) error {
		_, err := database.Exec(`
			INSERT INTO promises (
				id, owner_principal_ref, subject, review_at, state, closed_at,
				subject_task_uuid, subject_container_uuid,
				created_by_principal_ref, updated_by_principal_ref
			) VALUES ('', 'agent:cody', ?, ?, ?, ?, ?, ?, 'agent:cody', 'agent:cody')
		`, subject, reviewAt, state, closedAt, taskUUID, containerUUID)
		return err
	}
	if err := insert("Canonical promise", "2026-08-23T23:30:00Z", "open", nil, nil, nil); err != nil {
		t.Fatalf("insert valid promise: %v", err)
	}
	var friendlyID string
	if err := database.QueryRow(`SELECT id FROM promises WHERE subject = 'Canonical promise'`).Scan(&friendlyID); err != nil {
		t.Fatalf("read friendly id: %v", err)
	}
	if friendlyID != "PR-00001" {
		t.Fatalf("friendly id = %q, want PR-00001", friendlyID)
	}
	foundPromiseSequence := false
	for _, spec := range DefaultSequenceSpecs() {
		if spec.SeqTable == "promise_seq" && spec.EntityTable == "promises" && spec.Prefix == "PR-" {
			foundPromiseSequence = true
		}
	}
	if !foundPromiseSequence {
		t.Fatal("default sequence diagnostics omit promise_seq")
	}
	if _, err := database.Exec(`
		INSERT INTO containers (
			id, parent_uuid, slug, title, kind,
			created_by_principal_ref, updated_by_principal_ref
		) VALUES (
			'', ?, 'promise-check-project', 'Promise Check Project', 'project',
			'agent:cody', 'agent:cody'
		)
	`, adornmentsRootUUID); err != nil {
		t.Fatalf("seed target project: %v", err)
	}
	var targetProjectUUID string
	if err := database.QueryRow(`SELECT uuid FROM containers WHERE slug = 'promise-check-project'`).Scan(&targetProjectUUID); err != nil {
		t.Fatalf("read target project: %v", err)
	}
	if _, err := database.Exec(`
		INSERT INTO tasks (
			id, slug, title, project_uuid, state, priority, kind,
			description, specification, created_by_principal_ref, updated_by_principal_ref
		) VALUES (
			'', 'promise-check-target', 'Promise Check Target', ?, 'open', 3, 'task',
			'', '', 'agent:cody', 'agent:cody'
		)
	`, targetProjectUUID); err != nil {
		t.Fatalf("seed target task: %v", err)
	}
	var targetTaskUUID string
	if err := database.QueryRow(`SELECT uuid FROM tasks WHERE slug = 'promise-check-target'`).Scan(&targetTaskUUID); err != nil {
		t.Fatalf("read target task: %v", err)
	}

	invalid := []struct {
		name                              string
		subject, reviewAt, state          string
		closedAt, taskUUID, containerUUID interface{}
	}{
		{name: "offset review_at", subject: "bad offset", reviewAt: "2026-08-24T00:30:00+01:00", state: "open"},
		{name: "blank subject", subject: "  ", reviewAt: "2026-08-23T23:30:00Z", state: "open"},
		{name: "unknown state", subject: "bad state", reviewAt: "2026-08-23T23:30:00Z", state: "ready"},
		{name: "open with closed_at", subject: "open closed", reviewAt: "2026-08-23T23:30:00Z", state: "open", closedAt: "2026-08-23T23:30:00Z"},
		{name: "closed without closed_at", subject: "closed open", reviewAt: "2026-08-23T23:30:00Z", state: "resolved"},
		{name: "two targets", subject: "two targets", reviewAt: "2026-08-23T23:30:00Z", state: "open", taskUUID: targetTaskUUID, containerUUID: adornmentsRootUUID},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			if err := insert(tt.subject, tt.reviewAt, tt.state, tt.closedAt, tt.taskUUID, tt.containerUUID); err == nil {
				t.Fatal("invalid promise inserted")
			}
		})
	}

	var taskDelete, containerDelete string
	for column, target := range map[string]*string{"subject_task_uuid": &taskDelete, "subject_container_uuid": &containerDelete} {
		rows, err := database.Query(`PRAGMA foreign_key_list(promises)`)
		if err != nil {
			t.Fatalf("list promise foreign keys: %v", err)
		}
		for rows.Next() {
			var seqID, seq int
			var table, from, to, onUpdate, onDelete, match string
			if err := rows.Scan(&seqID, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
				t.Fatalf("scan promise foreign key: %v", err)
			}
			if from == column {
				*target = onDelete
			}
		}
		_ = rows.Close()
	}
	if taskDelete != "SET NULL" || containerDelete != "SET NULL" {
		t.Fatalf("promise target delete actions = task:%q container:%q, want SET NULL", taskDelete, containerDelete)
	}
}
