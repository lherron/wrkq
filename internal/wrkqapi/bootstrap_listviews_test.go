//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/workflow"
	"github.com/lherron/wrkq/internal/wrkfapi"
)

func TestPromiseListView(t *testing.T) {
	api, s := newMonitorAPI(t)
	ctx := context.Background()
	_, err := s.DB().Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1203)
 INSERT INTO promises(uuid,id,owner_principal_ref,subject,review_at,state,closed_at,created_at,created_by_principal_ref,updated_by_principal_ref)
 SELECT printf('promise-%05d',x),printf('PR-%05d',x), CASE WHEN x%2=0 THEN 'agent:mable' ELSE 'agent:cody' END,'page','2000-01-01T00:00:00Z',
 CASE x WHEN 1202 THEN 'resolved' WHEN 1203 THEN 'abandoned' ELSE 'open' END, CASE WHEN x>1201 THEN '2026-01-03T00:00:00Z' END,
 CASE WHEN x<=600 THEN '2026-01-01T00:00:00Z' ELSE '2026-01-02T00:00:00Z' END,'agent:cody','agent:cody' FROM n`)
	if err != nil {
		t.Fatal(err)
	}
	before := viewEventHead(t, s)
	p := PromiseListViewParams{}
	seen := map[string]bool{}
	last := ""
	owners := map[string]bool{}
	for i, want := range []int{500, 500, 201} {
		page, err := api.PromiseListView(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != want {
			t.Fatalf("page %d: %d want %d", i, len(page.Items), want)
		}
		for _, item := range page.Items {
			key := item.CreatedAt + item.UUID
			if seen[item.UUID] || key <= last {
				t.Fatalf("duplicate/unordered %s", key)
			}
			seen[item.UUID] = true
			last = key
			owners[item.OwnerPrincipalRef] = true
		}
		p.Cursor = page.NextCursor
		if (i < 2) != (p.Cursor != "") {
			t.Fatalf("page %d cursor %q", i, p.Cursor)
		}
	}
	if len(owners) != 2 {
		t.Fatalf("owners %v", owners)
	}
	page, err := api.PromiseListView(ctx, PromiseListViewParams{States: []string{"resolved", "abandoned"}})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("states: %+v %v", page, err)
	}
	page, err = api.PromiseListView(ctx, PromiseListViewParams{Limit: envelopeViewLimit(1)})
	if err != nil {
		t.Fatal(err)
	}
	shown, err := api.PromiseShow(ctx, PromiseShowParams{Promise: page.Items[0].UUID})
	if err != nil {
		t.Fatal(err)
	}
	assertViewJSON(t, page.Items[0], shown)
	if !page.Items[0].Ready || page.Items[0].ReadyFor == nil {
		t.Fatal("missing time-derived readiness")
	}
	empty, err := api.PromiseListView(ctx, PromiseListViewParams{States: []string{}})
	if err != nil || len(empty.Items) != 0 {
		t.Fatalf("empty states: %+v %v", empty, err)
	}
	for _, p := range []PromiseListViewParams{{States: []string{"bad"}}, {Limit: envelopeViewLimit(0)}, {Limit: envelopeViewLimit(501)}, {Cursor: "bad"}} {
		_, err := api.PromiseListView(ctx, p)
		_ = assertDomainCode(t, "WRKQ_VALIDATION", err)
	}
	if before != viewEventHead(t, s) {
		t.Fatal("read wrote events")
	}
}

func TestWorkflowInstancesView(t *testing.T) {
	api, s := newInstancesViewFixture(t)
	ctx := context.Background()
	before := viewEventHead(t, s)
	// Open-only paging exercises tied timestamps on all three pages.
	p := WorkflowInstancesViewParams{Statuses: []string{"open"}}
	seen := map[string]bool{}
	last := ""
	for i, want := range []int{500, 500, 201} {
		page, err := api.WorkflowInstancesView(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != want {
			t.Fatalf("page %d: %d want %d", i, len(page.Items), want)
		}
		for _, item := range page.Items {
			key := item.CreatedAt + item.ID
			if seen[item.ID] || key <= last {
				t.Fatalf("duplicate/unordered %s", key)
			}
			seen[item.ID] = true
			last = key
		}
		p.Cursor = page.NextCursor
		if (i < 2) != (p.Cursor != "") {
			t.Fatalf("page %d cursor %q", i, p.Cursor)
		}
	}
	// Default non-closed includes all three live statuses.
	def, err := api.WorkflowInstancesView(ctx, WorkflowInstancesViewParams{Tasks: []string{"T-01202", "task-01203", "T-01204"}})
	if err != nil || len(def.Items) != 2 {
		t.Fatalf("defaults/tasks %+v %v", def, err)
	}
	page, err := api.WorkflowInstancesView(ctx, WorkflowInstancesViewParams{Tasks: []string{"T-00001"}})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("tasks %+v %v", page, err)
	}
	shown, err := api.WorkflowInstances(ctx, WorkflowTaskParams{Task: "T-00001"})
	if err != nil {
		t.Fatal(err)
	}
	assertViewJSON(t, page.Items[0], shown.Instances[0])
	if page.Items[0].Suspension == nil {
		t.Fatal("missing suspension")
	}
	closed, err := api.WorkflowInstancesView(ctx, WorkflowInstancesViewParams{Statuses: []string{"closed"}})
	if err != nil || len(closed.Items) != 1 {
		t.Fatalf("closed %+v %v", closed, err)
	}
	for _, p := range []WorkflowInstancesViewParams{{Statuses: []string{}}, {Tasks: []string{}}} {
		empty, err := api.WorkflowInstancesView(ctx, p)
		if err != nil || len(empty.Items) != 0 {
			t.Fatalf("empty %+v %v", empty, err)
		}
	}
	for _, p := range []WorkflowInstancesViewParams{{Statuses: []string{"bad"}}, {Limit: envelopeViewLimit(0)}, {Limit: envelopeViewLimit(501)}, {Cursor: "bad"}, {Tasks: make([]string, 501)}} {
		_, err := api.WorkflowInstancesView(ctx, p)
		_ = assertDomainCode(t, "WRKQ_VALIDATION", err)
	}
	_, err = api.WorkflowInstancesView(ctx, WorkflowInstancesViewParams{Tasks: []string{"T-99999"}})
	_ = assertDomainCode(t, "WRKQ_NOT_FOUND", err)
	if before != viewEventHead(t, s) {
		t.Fatal("read wrote events")
	}
}

func assertViewJSON(t *testing.T, got, want any) {
	t.Helper()
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Fatalf("shape differs:\n%s\n%s", a, b)
	}
}
func viewEventHead(t *testing.T, s *store.Store) int64 {
	t.Helper()
	var n int64
	if err := s.DB().QueryRow("SELECT COALESCE(MAX(id),0) FROM event_log").Scan(&n); err != nil {
		t.Fatal(fmt.Errorf("event head: %w", err))
	}
	return n
}

func newInstancesViewFixture(t *testing.T) (*API, *store.Store) {
	t.Helper()
	api, s := newMonitorAPI(t)
	api.wf = wrkfapi.New(workflow.NewService(s.DB()))
	project := seedMonitorProject(t, s)
	// Unique tasks are required by the one-live-instance-per-task invariant.
	_, err := s.DB().Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1204)
 INSERT INTO tasks(uuid,id,slug,title,project_uuid,state,priority,created_by_principal_ref,updated_by_principal_ref)
 SELECT printf('task-%05d',x),printf('T-%05d',x),printf('task-%05d',x),'page',?,'open',2,'agent:cody','agent:cody' FROM n`, project)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB().Exec(`INSERT INTO workflow_templates(id,version,hash,definition_json) VALUES('view','1','hash','{}')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.DB().Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1204)
 INSERT INTO workflow_instances(id,task_uuid,task_ref,template_id,template_version,template_hash,status,task_doc_etag,task_doc_hash,created_at,suspension_id,suspension_reason,suspension_at,suspension_cause_ref)
 SELECT printf('wfi_%05d',x),printf('task-%05d',x),printf('wrkq:T-%05d',x),'view','1','hash',
 CASE x WHEN 1202 THEN 'active' WHEN 1203 THEN 'waiting' WHEN 1204 THEN 'closed' ELSE 'open' END,'1','hash',
 CASE WHEN x<=600 THEN '2026-01-01T00:00:00Z' ELSE '2026-01-02T00:00:00Z' END,
 CASE WHEN x=1 THEN 'sus_1' END,CASE WHEN x=1 THEN 'review' END,CASE WHEN x=1 THEN '2026-01-01T00:00:00Z' END,CASE WHEN x=1 THEN 'event_1' END FROM n`)
	if err != nil {
		t.Fatal(err)
	}
	return api, s
}
