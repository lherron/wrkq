package store

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/attribution"
)

func TestNamedSubtaskContainerArchiveAndPurge(t *testing.T) {
	database := setupTestDB(t)
	actor := setupTestActor(t, database)
	container := setupTestContainer(t, database, actor)
	s := New(database)
	owner, err := s.Tasks.Create(actor, CreateParams{Slug: "owner", Title: "Owner", ProjectUUID: container, State: "open", Priority: 3})
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO tasks(id,slug,title,project_uuid,state,priority,subtask_owner_uuid) VALUES(?, 'render','Render',?,'open',3,?)`, owner.ID+".render", container, owner.UUID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	rows, err := containerArchiveLiveTasks(tx, container)
	_ = tx.Rollback()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].UUID != owner.UUID {
		t.Fatalf("archive selected named subtask: %+v", rows)
	}
	impact, err := s.Containers.DeleteRecursiveImpact(container)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Containers.DeleteRecursiveWithAttribution(attribution.Attribution{PrincipalRef: "agent:test"}, container, 0, *impact)
	if err == nil || !strings.Contains(err.Error(), "subtasks") {
		t.Fatalf("purge owner container should be refused, got %v", err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM tasks WHERE project_uuid=?`, container).Scan(&count); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("refused purge altered tasks: %d", count)
	}
}
