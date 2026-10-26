package search

import (
	"context"
	"testing"

	"github.com/lherron/wrkq/internal/search/embed"
	"github.com/lherron/wrkq/internal/search/indexer"
	"github.com/lherron/wrkq/internal/store"
)

// TestSearchExcludesNamedSubtasksUnlessAsked: a named subtask matching the
// query is left out of search results by default and returned when the
// caller sets Subtasks.
func TestSearchExcludesNamedSubtasksUnlessAsked(t *testing.T) {
	database, actor, container := setupSearchDB(t)
	owner, err := store.New(database).Tasks.Create(actor, store.CreateParams{Slug: "owner", Title: "Owner", ProjectUUID: container, State: "open", Priority: 3})
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`INSERT INTO tasks(id,slug,title,project_uuid,state,priority,subtask_owner_uuid) VALUES(?, 'render','Render quokka frames',?,'open',3,?)`, owner.ID+".render", container, owner.UUID)
	if err != nil {
		t.Fatal(err)
	}
	var subtask string
	if err = database.QueryRow(`SELECT uuid FROM tasks WHERE id=?`, owner.ID+".render").Scan(&subtask); err != nil {
		t.Fatal(err)
	}

	idx := setupSearchIndex(t)
	emb := embed.HashEmbedder{Dims: 16}
	if err := indexer.New(database, idx, emb).Rebuild(context.Background()); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	svc := NewService(database, idx, emb)

	found := func(opts Options) bool {
		t.Helper()
		resp, err := svc.Search(context.Background(), opts)
		if err != nil {
			t.Fatalf("search %+v: %v", opts, err)
		}
		for _, r := range resp.Results {
			if r.TaskUUID == subtask {
				return true
			}
		}
		return false
	}
	if found(Options{Query: "quokka", State: "all", Limit: 10}) {
		t.Fatal("default search includes the named subtask")
	}
	if !found(Options{Query: "quokka", State: "all", Limit: 10, Subtasks: true}) {
		t.Fatal("search with Subtasks excludes the named subtask")
	}
}
