package search

import (
	"github.com/lherron/wrkq/internal/store"
	"testing"
)

func TestNamedSubtaskSearchMembership(t *testing.T) {
	database, actor, container := setupSearchDB(t)
	owner, err := store.New(database).Tasks.Create(actor, store.CreateParams{Slug: "owner", Title: "Owner", ProjectUUID: container, State: "open", Priority: 3})
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO tasks(id,slug,title,project_uuid,state,priority,subtask_owner_uuid) VALUES(?, 'render','Render',?,'open',3,?)`, owner.ID+".render", container, owner.UUID)
	if err != nil {
		t.Fatal(err)
	}
	var subtask string
	if err = database.QueryRow(`SELECT uuid FROM tasks WHERE id=?`, owner.ID+".render").Scan(&subtask); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Canonical: database}
	if svc.canonicalTaskMatches(subtask, Options{State: "all"}) {
		t.Fatal("default search includes named subtask")
	}
	if !svc.canonicalTaskMatches(subtask, Options{State: "all", Subtasks: true}) {
		t.Fatal("explicit search excludes named subtask")
	}
}
