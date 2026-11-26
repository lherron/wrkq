//go:build wrkq_local

package wrkqapi

import (
	"context"
	"testing"
)

// A single-segment move destination is top-level: for a project that means a
// rename under the root container. It must not write a null parent.
func TestContainerMoveRenamesTopLevelProject(t *testing.T) {
	api := newReadModelGoldenAPI(t)
	ctx := context.Background()
	for _, slug := range []string{"zed", "zedctl", "held"} {
		if _, err := api.ContainerCreate(ctx, ContainerCreateParams{Path: slug, Kind: "project"}); err != nil {
			t.Fatalf("create %s: %v", slug, err)
		}
	}
	if _, err := api.ContainerCreate(ctx, ContainerCreateParams{Path: "zedctl/inbox"}); err != nil {
		t.Fatalf("create zedctl/inbox: %v", err)
	}

	moved, err := api.ContainerMove(ctx, ContainerMoveParams{Container: "zed", Destination: "zed-retired"})
	if err != nil {
		t.Fatalf("rename top-level project: %v", err)
	}
	if moved.Slug != "zed-retired" {
		t.Fatalf("renamed slug = %q, want zed-retired", moved.Slug)
	}

	// The freed slug can now be a short; the renamed project is still a project.
	short := "zed"
	if _, err := api.ProjectSetShort(ctx, ProjectSetShortParams{Project: "zedctl", Short: &short}); err != nil {
		t.Fatalf("short zed after rename: %v", err)
	}
	hold := "hld"
	if _, err := api.ProjectSetShort(ctx, ProjectSetShortParams{Project: "zed-retired", Short: &hold}); err != nil {
		t.Fatalf("renamed project lost its project standing: %v", err)
	}

	// Renaming a project onto a held short is refused, as create and update are.
	if _, err := api.ContainerMove(ctx, ContainerMoveParams{Container: "held", Destination: "zed"}); err == nil {
		t.Fatalf("rename onto short zed succeeded; want conflict")
	}
	// A non-project container still cannot become top-level.
	if _, err := api.ContainerMove(ctx, ContainerMoveParams{Container: "zedctl/inbox", Destination: "loose"}); err == nil {
		t.Fatalf("directory moved to top level; want refusal")
	}
}
