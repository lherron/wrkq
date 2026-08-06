//go:build wrkq_local

package workrpc_test

import (
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/store"
)

func TestContainerTaskCountsOverRealRPC(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "task-counts.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := store.New(database)
	const actor = "00000000-0000-4000-8000-0000000000b1"
	project, err := s.Containers.Create(actor, store.ContainerCreateParams{
		Slug: "rpc-project", Kind: "project",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	nested, err := s.Containers.Create(actor, store.ContainerCreateParams{
		Slug: "nested", Kind: "directory", ParentUUID: &project.UUID,
	})
	if err != nil {
		t.Fatalf("create nested container: %v", err)
	}
	for index, state := range []domain.State{domain.StateIdea, domain.StateOpen, domain.StateCompleted} {
		if _, err := s.Tasks.Create(actor, store.CreateParams{
			Slug:  "rpc-task-" + string(state),
			Title: "RPC task",
			ProjectUUID: func() string {
				if index == 0 {
					return project.UUID
				}
				return nested.UUID
			}(),
			State: state, Priority: 2,
		}); err != nil {
			t.Fatalf("create %s task: %v", state, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close seed database: %v", err)
	}

	frames := p2Run(t, dbPath,
		mkRPC("counts", "wrkq.container.taskCounts", map[string]any{}),
	)
	result := p2ResultOrFail(t, frames[1], "container.taskCounts")
	items, ok := result["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items = %#v, want project + nested rows", result["items"])
	}
	byPath := map[string]map[string]any{}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("item = %#v", raw)
		}
		path, _ := item["path"].(string)
		byPath[path] = item
		if _, exists := item["nextCursor"]; exists {
			t.Fatalf("aggregate row unexpectedly paginated: %#v", item)
		}
	}
	projectRow := byPath["rpc-project"]
	if projectRow["uuid"] != project.UUID ||
		projectRow["id"] != project.ID ||
		projectRow["projectUuid"] != project.UUID ||
		projectRow["projectId"] != project.ID ||
		projectRow["totalTaskCount"] != float64(3) ||
		projectRow["activeTaskCount"] != float64(2) {
		t.Fatalf("project row = %#v", projectRow)
	}
	nestedRow := byPath["rpc-project/nested"]
	if nestedRow["totalTaskCount"] != float64(2) ||
		nestedRow["activeTaskCount"] != float64(1) {
		t.Fatalf("nested row = %#v", nestedRow)
	}
}