package webhooks_test

import (
	"encoding/json"
	"sort"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/webhooks"
)

// setWebhooks writes a container's webhook_urls as a JSON array.
func setWebhooks(t *testing.T, database *db.DB, actorUUID, containerUUID string, urls []string) {
	t.Helper()
	raw, _ := json.Marshal(urls)
	if _, err := store.New(database).Containers.UpdateFields(actorUUID, containerUUID, map[string]interface{}{"webhook_urls": string(raw)}, 0); err != nil {
		t.Fatalf("set webhook urls on %s: %v", containerUUID, err)
	}
}

// resolveForProject creates a task in the given project and resolves its webhook targets.
func resolveForProject(t *testing.T, database *db.DB, projectUUID string) []string {
	t.Helper()
	urls, err := webhooks.ResolveWebhookTargets(database, projectUUID, webhooks.Payload{TicketID: "T-00001", ProjectID: "P-00001"})
	if err != nil {
		t.Fatalf("resolve webhook targets: %v", err)
	}
	sort.Strings(urls)
	return urls
}

// TestPayloadProjectScopeUnaffectedByRoot proves the webhook payload's
// project_scope_id and container_path carry no root prefix after the root model.
func TestPayloadProjectScopeUnaffectedByRoot(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := store.New(database)

	proj, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "alpha", Kind: "project"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, err := s.Tasks.Create(actorUUID, store.CreateParams{Slug: "task-one", Title: "Task One", ProjectUUID: proj.UUID, State: "open", Priority: 2})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	info, err := webhooks.LookupTaskInfo(database, task.UUID)
	if err != nil {
		t.Fatalf("lookup task info: %v", err)
	}
	if info.ProjectScopeID != "alpha" {
		t.Fatalf("ProjectScopeID = %q, want alpha (no root prefix)", info.ProjectScopeID)
	}
	if info.ContainerPath != "alpha" {
		t.Fatalf("ContainerPath = %q, want alpha (no root prefix)", info.ContainerPath)
	}
}

// TestRootWebhookInheritedByEveryProject proves direction A: a webhook registered
// ONLY on the root is inherited by a project that has no webhook of its own.
func TestRootWebhookInheritedByEveryProject(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := store.New(database)

	rootUUID, err := store.RootContainerUUID(database)
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	// Two projects, neither carrying a webhook of its own.
	p1, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "alpha", Kind: "project"})
	if err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	p2, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "beta", Kind: "project"})
	if err != nil {
		t.Fatalf("create beta: %v", err)
	}

	const sink = "http://127.0.0.1:18451/api/webhooks/wrkq"
	setWebhooks(t, database, actorUUID, rootUUID, []string{sink})

	for _, p := range []*store.ContainerCreateResult{p1, p2} {
		urls := resolveForProject(t, database, p.UUID)
		if len(urls) != 1 || urls[0] != sink {
			t.Fatalf("project %s: got %v, want [%s]", p.UUID, urls, sink)
		}
	}
}

// TestProjectAndRootWebhooksCombineAndDedupe proves direction B: a project-local
// webhook unions with the root webhook, and a URL present on both collapses once.
func TestProjectAndRootWebhooksCombineAndDedupe(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := store.New(database)

	rootUUID, err := store.RootContainerUUID(database)
	if err != nil {
		t.Fatalf("resolve root: %v", err)
	}

	const (
		rootSink    = "http://127.0.0.1:18470/v1/webhooks/wrkq"
		sharedSink  = "http://127.0.0.1:18451/api/webhooks/wrkq"
		projectSink = "http://127.0.0.1:19999/project-only"
	)

	// Root carries the shared sink + an ACP-style sink.
	setWebhooks(t, database, actorUUID, rootUUID, []string{rootSink, sharedSink})

	// Project carries a project-only sink AND a duplicate of the shared sink.
	proj, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "gamma", Kind: "project"})
	if err != nil {
		t.Fatalf("create gamma: %v", err)
	}
	setWebhooks(t, database, actorUUID, proj.UUID, []string{projectSink, sharedSink})

	got := resolveForProject(t, database, proj.UUID)
	want := []string{rootSink, sharedSink, projectSink}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("expected %d deduped urls, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("resolved urls mismatch\n got:  %v\n want: %v", got, want)
		}
	}
}
