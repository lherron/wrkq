//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"testing"
)

func TestNamedSubtaskReadSurfaces(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := seedMonitorProject(t, s)
	owner, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{Slug: "owner", Title: "Owner", ProjectUUID: project, State: "completed", Priority: 3})
	if err != nil {
		t.Fatal(err)
	}
	var create TaskCreateParams
	if err = json.Unmarshal([]byte(`{"subtaskOwner":"`+owner.ID+`","slug":"diagram","title":"Diagram"}`), &create); err != nil {
		t.Fatal(err)
	}
	child, err := api.TaskCreate(context.Background(), create)
	if err != nil {
		t.Fatal(err)
	}
	counts, err := api.ContainerTaskCounts(context.Background(), ContainerTaskCountsParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range counts.Items {
		if item.UUID == project && (item.TotalTaskCount != 1 || item.ActiveTaskCount != 0) {
			t.Fatalf("counts include named subtask: %+v", item)
		}
	}
	shown, err := api.TaskShow(context.Background(), TaskShowParams{Task: owner.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(shown.Subtasks) != 1 || shown.Subtasks[0].ID != child.ID || shown.OpenSubtaskCount != 1 {
		t.Fatalf("owner projection: %+v", shown)
	}
	for _, selector := range []string{child.ID, child.UUID, child.Path} {
		uuid, _, err := selectors.ResolveTask(api.db, selector)
		if err != nil || uuid != child.UUID {
			t.Fatalf("selector %q: %s %v", selector, uuid, err)
		}
	}
	if _, _, err := selectors.ResolveTask(api.db, "diagram"); err == nil {
		t.Fatal("bare subtask slug resolved")
	}
	listed, err := api.TaskList(context.Background(), TaskListParams{IncludeDeleted: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 || listed.Items[0].ID != owner.ID || listed.Items[0].OpenSubtaskCount != 1 {
		t.Fatalf("default list: %+v", listed)
	}
	var params TaskListParams
	_ = json.Unmarshal([]byte(`{"subtasks":true,"subtaskOwner":"`+owner.ID+`"}`), &params)
	listed, err = api.TaskList(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Items) != 1 || listed.Items[0].ID != child.ID {
		t.Fatalf("owner subtask list: %+v", listed)
	}
	var find FindListViewParams
	_ = json.Unmarshal([]byte(`{"subtasks":true,"ownerState":"terminal"}`), &find)
	found, err := api.FindListView(context.Background(), find)
	if err != nil {
		t.Fatal(err)
	}
	if len(found.Items) != 1 || found.Items[0].ID != child.ID {
		t.Fatalf("terminal-owner find: %+v", found)
	}
}

func TestNamedSubtaskViewExclusionAndOwnerTree(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := seedMonitorProject(t, s)
	owner, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{Slug: "owner", Title: "Owner", ProjectUUID: project, State: "completed", Priority: 3})
	if err != nil {
		t.Fatal(err)
	}
	var p TaskCreateParams
	_ = json.Unmarshal([]byte(`{"subtaskOwner":"`+owner.ID+`","slug":"preview","title":"Preview"}`), &p)
	sub, err := api.TaskCreate(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ls, err := api.LsListView(ctx, LsListViewParams{Path: "proj", States: flexString{"any"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ls.Items) != 1 || ls.Items[0].ID != owner.ID || ls.Items[0].OpenSubtaskCount != 1 {
		t.Fatalf("ls: %+v", ls)
	}
	ls, err = api.LsListView(ctx, LsListViewParams{Path: owner.ID, Subtasks: true, States: flexString{"any"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ls.Items) != 1 || ls.Items[0].ID != sub.ID {
		t.Fatalf("owner ls: %+v", ls)
	}
	tree, err := api.TreeView(ctx, TreeViewParams{Path: "proj", States: flexString{"any"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Children) != 1 || tree.Children[0].ID != owner.ID || tree.Children[0].OpenSubtaskCount != 1 || len(tree.Children[0].Children) != 1 || tree.Children[0].Children[0].ID != sub.ID {
		t.Fatalf("tree: %+v", tree)
	}
	cat, err := api.TaskCatView(ctx, TaskCatViewParams{Task: sub.ID})
	if err != nil {
		t.Fatal(err)
	}
	if cat.SubtaskOwnerID == nil || *cat.SubtaskOwnerID != owner.ID || cat.SubtaskOwnerUUID == nil || *cat.SubtaskOwnerUUID != owner.UUID {
		t.Fatalf("cat owner: %+v", cat)
	}
}

func TestNamedSubtaskOwnerLsPagination(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := seedMonitorProject(t, s)
	owner, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{Slug: "owner", Title: "Owner", ProjectUUID: project, State: "open", Priority: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"alpha", "beta", "gamma"} {
		if _, err := api.TaskCreate(context.Background(), TaskCreateParams{SubtaskOwner: owner.ID, Slug: slug, Title: slug}); err != nil {
			t.Fatal(err)
		}
	}
	for _, reverse := range []bool{false, true} {
		p := LsListViewParams{Path: owner.ID, Subtasks: true, States: flexString{"any"}, Limit: 1, Reverse: reverse}
		seen := map[string]bool{}
		for page := 0; page < 3; page++ {
			got, err := api.LsListView(context.Background(), p)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Items) != 1 || seen[got.Items[0].ID] {
				t.Fatalf("repeated or missing page: %+v", got)
			}
			seen[got.Items[0].ID] = true
			p.Cursor = got.NextCursor
			if (page < 2) != (p.Cursor != "") {
				t.Fatalf("unexpected cursor at page %d: %+v", page, got)
			}
		}
	}
}
