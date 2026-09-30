package wrkqd

import (
	"github.com/lherron/wrkq/internal/store"
	"testing"
)

func TestLegacyHTTPContainerViewsExcludeNamedSubtasks(t *testing.T) {
	database, _ := setupTestEnv(t)
	s := store.New(database)
	owner, err := s.Tasks.Create("00000000-0000-0000-0000-000000000001", store.CreateParams{ProjectUUID: "00000000-0000-0000-0000-000000000002", Slug: "owner", Title: "Owner", State: "open", Priority: 3})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Tasks.Create("00000000-0000-0000-0000-000000000001", store.CreateParams{SubtaskOwnerUUID: &owner.UUID, Slug: "preview", Title: "Preview", State: "open", Priority: 3})
	if err != nil {
		t.Fatal(err)
	}
	rows, _, err := findTasks(database, findOptions{paths: []string{"inbox"}, state: "all", sortField: "id"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != owner.ID || rows[0].OpenSubtaskCount != 1 {
		t.Fatalf("list: %+v", rows)
	}
	tree, err := buildTree(database, "inbox", 0, true, false, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Children) != 1 || tree.Children[0].ID != owner.ID || tree.Children[0].OpenSubtaskCount != 1 {
		t.Fatalf("tree: %+v", tree)
	}
}
