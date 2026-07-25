package wrkqapi

import (
	"context"
	"testing"
)

func createLabelFilterTask(t *testing.T, api *API, title string, labels []string) *WrkqTask {
	t.Helper()
	task, err := api.TaskCreate(context.Background(), TaskCreateParams{
		Title:        title,
		Description:  "shared filterneedle prose",
		State:        "open",
		Labels:       labels,
		PrincipalRef: "agent:seed",
	})
	if err != nil {
		t.Fatalf("TaskCreate(%q): %v", title, err)
	}
	return task
}

func findIDs(view *WrkqFindListView) []string {
	ids := make([]string, 0, len(view.Items))
	for _, item := range view.Items {
		ids = append(ids, item.ID)
	}
	return ids
}

func assertIDs(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

func assertIDSet(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want set %v", got, want)
	}
	set := make(map[string]struct{}, len(got))
	for _, id := range got {
		set[id] = struct{}{}
	}
	for _, id := range want {
		if _, ok := set[id]; !ok {
			t.Fatalf("ids = %v, want set %v", got, want)
		}
	}
}

func TestFindListViewExactLabelMembershipAndPagination(t *testing.T) {
	api := newAttributionAPI(t, "agent:seed")
	both := createLabelFilterTask(t, api, "Label both", []string{"alpha", "beta"})
	_ = createLabelFilterTask(t, api, "Label none", nil)
	alpha := createLabelFilterTask(t, api, "Label alpha", []string{"alpha"})
	_ = createLabelFilterTask(t, api, "Label substring", []string{"alphabet"})
	_ = createLabelFilterTask(t, api, "Label case", []string{"Alpha"})

	ctx := context.Background()
	one, err := api.FindListView(ctx, FindListViewParams{
		Type: "t", State: "all", Labels: []string{"alpha"}, Sort: "id",
	})
	if err != nil {
		t.Fatalf("FindListView one label: %v", err)
	}
	assertIDs(t, findIDs(one), both.ID, alpha.ID)

	and, err := api.FindListView(ctx, FindListViewParams{
		Type: "t", State: "all", Labels: []string{"alpha", "beta"}, Sort: "id",
	})
	if err != nil {
		t.Fatalf("FindListView repeated labels: %v", err)
	}
	assertIDs(t, findIDs(and), both.ID)

	duplicate, err := api.FindListView(ctx, FindListViewParams{
		State: "all", Labels: []string{"alpha", "alpha"}, Sort: "id",
	})
	if err != nil {
		t.Fatalf("FindListView duplicate labels: %v", err)
	}
	assertIDs(t, findIDs(duplicate), both.ID, alpha.ID)
	for _, item := range duplicate.Items {
		if item.Type != "task" {
			t.Fatalf("label-filtered untyped find returned %q row: %#v", item.Type, item)
		}
	}

	first, err := api.FindListView(ctx, FindListViewParams{
		Type: "t", State: "all", Labels: []string{"alpha"}, Sort: "id", Limit: 1,
	})
	if err != nil {
		t.Fatalf("FindListView first page: %v", err)
	}
	assertIDs(t, findIDs(first), both.ID)
	if first.NextCursor == "" {
		t.Fatal("first filtered page has no next cursor")
	}
	second, err := api.FindListView(ctx, FindListViewParams{
		Type: "t", State: "all", Labels: []string{"alpha"}, Sort: "id",
		Limit: 1, Cursor: first.NextCursor,
	})
	if err != nil {
		t.Fatalf("FindListView second page: %v", err)
	}
	assertIDs(t, findIDs(second), alpha.ID)
	if second.NextCursor != "" {
		t.Fatalf("second filtered page next cursor = %q, want empty", second.NextCursor)
	}
}

func searchTaskIDs(view *WrkqSearchListView) []string {
	ids := make([]string, 0, len(view.Results))
	for _, result := range view.Results {
		if result.ResourceType == "task" {
			ids = append(ids, result.TaskID)
		}
	}
	return ids
}

func TestSearchListViewExactCanonicalLabelMembership(t *testing.T) {
	api, _ := newSearchAPI(t, "none")
	both := createLabelFilterTask(t, api, "Search label both", []string{"alpha", "beta"})
	alpha := createLabelFilterTask(t, api, "Search label alpha", []string{"alpha"})
	_ = createLabelFilterTask(t, api, "Search label substring", []string{"alphabet"})
	_ = createLabelFilterTask(t, api, "Search label case", []string{"Alpha"})
	_ = createLabelFilterTask(t, api, "Search alpha prose only", nil)

	ctx := context.Background()
	if _, err := api.IndexRebuild(ctx, IndexLifecycleParams{}); err != nil {
		t.Fatalf("IndexRebuild: %v", err)
	}

	one, err := api.SearchListView(ctx, SearchListViewParams{
		Query: "filterneedle", State: "all", Labels: []string{"alpha"}, Sort: "created_at",
	})
	if err != nil {
		t.Fatalf("SearchListView one label: %v", err)
	}
	assertIDSet(t, searchTaskIDs(one), both.ID, alpha.ID)

	and, err := api.SearchListView(ctx, SearchListViewParams{
		Query: "filterneedle", State: "all", Labels: []string{"alpha", "beta"},
	})
	if err != nil {
		t.Fatalf("SearchListView repeated labels: %v", err)
	}
	assertIDs(t, searchTaskIDs(and), both.ID)

	duplicate, err := api.SearchListView(ctx, SearchListViewParams{
		Query: "filterneedle", State: "all", Labels: []string{"alpha", "alpha"},
		Limit: 1,
	})
	if err != nil {
		t.Fatalf("SearchListView duplicate labels: %v", err)
	}
	if duplicate.TotalMatches != 2 || len(duplicate.Results) != 1 {
		t.Fatalf("duplicate filtered search total/page = %d/%d, want 2/1", duplicate.TotalMatches, len(duplicate.Results))
	}

	prose, err := api.SearchListView(ctx, SearchListViewParams{
		Query: "alpha", State: "all", Labels: []string{"alpha"},
	})
	if err != nil {
		t.Fatalf("SearchListView label/prose separation: %v", err)
	}
	assertIDSet(t, searchTaskIDs(prose), both.ID, alpha.ID)
}
