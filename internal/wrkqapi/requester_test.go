//go:build wrkq_local

package wrkqapi

import (
	"context"
	"strings"
	"testing"
)

// Delegated work (T-09978): requester fields are first-class task facts, and
// the owner timeline shows a subtask's creation (with requester), claim and
// release by default while ordinary creation and edits stay quiet.

func TestRequesterFieldsValidateProjectAndEdit(t *testing.T) {
	api := newAttributionAPI(t, "agent:seed")
	ctx := context.Background()
	owner := claimTestTask(t, api, "owner")

	sub, err := api.TaskCreate(ctx, TaskCreateParams{
		SubtaskOwner: owner.ID, Slug: "diagram", Title: "Diagram", PrincipalRef: "agent:seed",
		RequesterPrincipalRef: "mable", RequesterScopeRef: "mable@agent-control-plane:primary",
		AssigneePrincipalRef: "agent:arris",
	})
	if err != nil {
		t.Fatalf("create subtask with requester: %v", err)
	}
	if sub.RequesterPrincipalRef != "agent:mable" {
		t.Fatalf("requesterPrincipalRef = %q", sub.RequesterPrincipalRef)
	}
	if !strings.HasPrefix(sub.RequesterScopeRef, "agent:mable:project:agent-control-plane") {
		t.Fatalf("requesterScopeRef not canonical: %q", sub.RequesterScopeRef)
	}

	// A scope alone derives the principal.
	derived, err := api.TaskCreate(ctx, TaskCreateParams{Title: "derived", PrincipalRef: "agent:seed", RequesterScopeRef: "agent:cody:project:wrkq"})
	if err != nil || derived.RequesterPrincipalRef != "agent:cody" || derived.RequesterScopeRef != "agent:cody:project:wrkq" {
		t.Fatalf("scope-only create = %+v, %v", derived, err)
	}

	for name, params := range map[string]TaskCreateParams{
		"agent mismatch": {Title: "bad1", RequesterPrincipalRef: "agent:cody", RequesterScopeRef: "mable@wrkq"},
		"bad scope":      {Title: "bad2", RequesterScopeRef: "not a scope"},
		"bad principal":  {Title: "bad3", RequesterPrincipalRef: "user:"},
	} {
		params.PrincipalRef = "agent:seed"
		if _, err := api.TaskCreate(ctx, params); err == nil || claimErrorCode(t, err) != CodeValidation {
			t.Fatalf("%s: err = %v, want validation", name, err)
		}
	}

	// Every task read projects the fields.
	list, err := api.TaskList(ctx, TaskListParams{Subtasks: true, SubtaskOwner: owner.ID})
	if err != nil || len(list.Items) != 1 || list.Items[0].RequesterScopeRef != sub.RequesterScopeRef {
		t.Fatalf("task.list(subtaskOwner) = %+v, %v", list, err)
	}
	cat, err := api.TaskCatView(ctx, TaskCatViewParams{Task: sub.ID})
	if err != nil || cat.RequesterPrincipalRef == nil || *cat.RequesterPrincipalRef != "agent:mable" ||
		cat.RequesterScopeRef == nil || *cat.RequesterScopeRef != sub.RequesterScopeRef {
		t.Fatalf("cat view requester = %+v, %v", cat, err)
	}
	found, err := api.FindListView(ctx, FindListViewParams{Subtasks: true, SlugGlob: "diagram"})
	if err != nil || len(found.Items) != 1 || found.Items[0].RequesterPrincipalRef == nil || *found.Items[0].RequesterPrincipalRef != "agent:mable" {
		t.Fatalf("find requester = %+v, %v", found, err)
	}

	// Editing: a new principal alone drops a scope naming another agent.
	updated, err := api.TaskUpdate(ctx, TaskUpdateParams{Task: sub.ID, Patch: TaskPatch{RequesterPrincipalRef: strp("cody")}})
	if err != nil || updated.RequesterPrincipalRef != "agent:cody" || updated.RequesterScopeRef != "" {
		t.Fatalf("principal-only patch = %+v, %v", updated, err)
	}
	updated, err = api.TaskUpdate(ctx, TaskUpdateParams{Task: sub.ID, Patch: TaskPatch{RequesterScopeRef: strp("mable@wrkq:primary")}})
	if err != nil || updated.RequesterPrincipalRef != "agent:mable" || !strings.HasPrefix(updated.RequesterScopeRef, "agent:mable:project:wrkq") {
		t.Fatalf("scope-only patch = %+v, %v", updated, err)
	}
	updated, err = api.TaskUpdate(ctx, TaskUpdateParams{Task: sub.ID, Patch: TaskPatch{RequesterScopeRef: strp("")}})
	if err != nil || updated.RequesterPrincipalRef != "agent:mable" || updated.RequesterScopeRef != "" {
		t.Fatalf("scope clear = %+v, %v", updated, err)
	}
	if _, err := api.TaskUpdate(ctx, TaskUpdateParams{Task: sub.ID, Patch: TaskPatch{
		RequesterPrincipalRef: strp("agent:cody"), RequesterScopeRef: strp("mable@wrkq"),
	}}); err == nil || claimErrorCode(t, err) != CodeValidation {
		t.Fatalf("mismatched patch err = %v, want validation", err)
	}
	updated, err = api.TaskUpdate(ctx, TaskUpdateParams{Task: sub.ID, Patch: TaskPatch{RequesterPrincipalRef: strp("")}})
	if err != nil || updated.RequesterPrincipalRef != "" || updated.RequesterScopeRef != "" {
		t.Fatalf("principal clear = %+v, %v", updated, err)
	}
}

func TestOwnerTimelineShowsDelegationFactsByDefault(t *testing.T) {
	api := newAttributionAPI(t, "agent:seed")
	ctx := context.Background()
	owner := claimTestTask(t, api, "owner")
	sub, err := api.TaskCreate(ctx, TaskCreateParams{
		SubtaskOwner: owner.ID, Slug: "diagram", Title: "Diagram", PrincipalRef: "agent:mable",
		RequesterScopeRef: "mable@agent-control-plane:primary",
	})
	if err != nil {
		t.Fatal(err)
	}
	bare, err := api.TaskCreate(ctx, TaskCreateParams{SubtaskOwner: owner.ID, Slug: "bare", Title: "Bare", PrincipalRef: "agent:mable"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := api.TaskCreate(ctx, TaskCreateParams{Title: "request", PrincipalRef: "agent:mable", RequesterPrincipalRef: "agent:mable"})
	if err != nil {
		t.Fatal(err)
	}
	scope := "agent:arris:project:wrkq:task:" + sub.ID
	claim, err := api.TaskClaim(nodeContext("max3"), TaskClaimParams{Task: sub.ID, PrincipalRef: "agent:arris", Scope: scope})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := api.TaskRelease(nodeContext("max3"), TaskReleaseParams{
		Task: sub.ID, PrincipalRef: "agent:arris", Scope: scope, ClaimGeneration: claim.ClaimGeneration, ClaimToken: claim.ClaimToken,
	}); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := api.TaskUpdate(ctx, TaskUpdateParams{Task: owner.ID, Patch: TaskPatch{Title: strp("Owner renamed")}}); err != nil {
		t.Fatal(err)
	}

	family, err := api.ContainerTimelineView(ctx, ContainerTimelineViewParams{Container: owner.ProjectUUID, Task: owner.ID, EntriesOnly: true})
	if err != nil {
		t.Fatalf("owner timeline: %v", err)
	}
	seen := map[string]WrkqTimelineEntry{}
	for _, entry := range family.Entries {
		seen[entry.Type+" "+entry.TaskID] = entry
	}
	created, ok := seen["task.created "+sub.ID]
	if !ok || created.Requester == nil || created.Requester.PrincipalRef != "agent:mable" ||
		!strings.HasPrefix(created.Requester.ScopeRef, "agent:mable:project:agent-control-plane") {
		t.Fatalf("subtask creation with requester missing: %+v", family.Entries)
	}
	if _, ok := seen["task.created "+bare.ID]; !ok {
		t.Fatalf("requester-less subtask creation should be visible: %+v", family.Entries)
	}
	claimed, ok := seen["task.claimed "+sub.ID]
	if !ok || claimed.Claim == nil || claimed.Claim.PrincipalRef != "agent:arris" || claimed.Claim.ScopeRef != scope ||
		claimed.Claim.Node != "max3" || claimed.Claim.Generation != claim.ClaimGeneration {
		t.Fatalf("claimed entry = %+v", claimed)
	}
	released, ok := seen["task.claim_released "+sub.ID]
	if !ok || released.Claim == nil || released.Claim.PrincipalRef != "agent:arris" || released.Claim.Generation != claim.ClaimGeneration {
		t.Fatalf("claim_released entry = %+v", released)
	}
	for key := range seen {
		if key == "task.created "+owner.ID || strings.HasPrefix(key, "task.edited ") {
			t.Fatalf("ordinary creation/edit leaked into the default timeline: %s", key)
		}
	}

	project, err := api.ContainerTimelineView(ctx, ContainerTimelineViewParams{Container: owner.ProjectUUID, EntriesOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	requestVisible := false
	for _, entry := range project.Entries {
		if entry.Type == "task.created" && entry.TaskID == request.ID {
			requestVisible = entry.Requester != nil
		}
		if entry.Type == "task.created" && entry.TaskID == owner.ID {
			t.Fatal("ordinary task.created is no longer quiet")
		}
	}
	if !requestVisible {
		t.Fatal("task created with requester fields is not default-visible")
	}

	// A filter naming the type still reaches ordinary creation.
	filtered, err := api.ContainerTimelineView(ctx, ContainerTimelineViewParams{Container: owner.ProjectUUID, Types: []string{"task.created"}, EntriesOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	ownerCreated := false
	for _, entry := range filtered.Entries {
		ownerCreated = ownerCreated || entry.TaskID == owner.ID
	}
	if !ownerCreated {
		t.Fatal("filtered read lost ordinary task.created")
	}
}

// task.create normalizes the assignee exactly as task.update does, so the bare
// agent slug the touch help advertises does not hit the CHECK constraint.
func TestTaskCreateNormalizesBareAssigneeSlug(t *testing.T) {
	api := newAttributionAPI(t, "agent:seed")
	ctx := context.Background()
	task, err := api.TaskCreate(ctx, TaskCreateParams{Title: "bare assignee", PrincipalRef: "agent:seed", AssigneePrincipalRef: "arris"})
	if err != nil || task.AssigneePrincipalRef != "agent:arris" {
		t.Fatalf("bare-slug create = %+v, %v", task, err)
	}
	if _, err := api.TaskCreate(ctx, TaskCreateParams{Title: "bad assignee", PrincipalRef: "agent:seed", AssigneePrincipalRef: "user:"}); err == nil || claimErrorCode(t, err) != CodeValidation {
		t.Fatalf("invalid assignee err = %v, want validation", err)
	}
}
