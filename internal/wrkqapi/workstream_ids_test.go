//go:build wrkq_local

package wrkqapi

import (
	"context"
	"strconv"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/selectors"
)

func TestWorkstreamIDsAndFixedKind(t *testing.T) {
	a := newAttributionAPI(t, "agent:seed")
	ctx := context.Background()
	parent := claimTestTask(t, a, "parent")
	w, err := a.TaskCreate(ctx, TaskCreateParams{Title: "stream", Kind: "workstream", ParentTask: parent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if w.ID[:2] != "W-" || w.Kind != "workstream" {
		t.Fatalf("mint: %+v", w)
	}
	next := claimTestTask(t, a, "next")
	wn, _ := strconv.Atoi(w.ID[2:])
	tn, _ := strconv.Atoi(next.ID[2:])
	if tn != wn+1 {
		t.Fatalf("sequence: %s %s", w.ID, next.ID)
	}
	for _, pair := range []struct{ task, kind string }{{w.ID, "task"}, {next.ID, "workstream"}} {
		_, err = a.TaskUpdate(ctx, TaskUpdateParams{Task: pair.task, Patch: TaskPatch{Kind: strp(pair.kind)}})
		if err == nil || claimErrorCode(t, err) != "WRKQ_WORKSTREAM_KIND_FIXED" {
			t.Fatalf("kind change: %v", err)
		}
	}
	for _, p := range []string{"", parent.ID} {
		updated, e := a.TaskUpdate(ctx, TaskUpdateParams{Task: w.ID, Patch: TaskPatch{ParentTask: &p}})
		if e != nil || updated.Kind != "workstream" {
			t.Fatalf("parent: %+v %v", updated, e)
		}
	}
	if _, err = a.TaskUpdate(ctx, TaskUpdateParams{Task: w.ID, Patch: TaskPatch{Kind: strp("workstream")}}); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{w.ID[2:], strconv.Itoa(wn)} {
		got, _, e := selectors.ExpandBareTaskID(a.db, token)
		if e != nil || got != w.ID {
			t.Fatalf("bare %s: %s %v", token, got, e)
		}
	}
	if _, typ, e := a.resolveLogResource(ctx, w.ID); e != nil || typ != "task" {
		t.Fatalf("history: %s %v", typ, e)
	}
	sub, e := a.TaskCreate(ctx, TaskCreateParams{SubtaskOwner: parent.ID, Slug: "check", Title: "Check"})
	if e != nil {
		t.Fatal(e)
	}
	if _, typ, e := a.resolveLogResource(ctx, sub.ID); e != nil || typ != "task" {
		t.Fatalf("subtask history: %s %v", typ, e)
	}
	for _, owner := range []string{parent.ID, w.ID} {
		if _, e := a.TaskCreate(ctx, TaskCreateParams{SubtaskOwner: owner, Slug: "thread", Title: "Thread", Kind: "workstream"}); e == nil {
			t.Fatal("named workstream admitted")
		}
	}
	if _, e := a.TaskCreate(ctx, TaskCreateParams{SubtaskOwner: w.ID, Slug: "child", Title: "Child"}); e == nil {
		t.Fatal("W owner admitted")
	}
	// Imported IDs bypass minting; reconcile includes W numbers, skipping composites.
	if _, e := a.db.Exec("UPDATE tasks SET id='W-90000' WHERE uuid=?", w.UUID); e != nil {
		t.Fatal(e)
	}
	if _, e := db.FixSequenceDrifts(a.db, db.DefaultSequenceSpecs()); e != nil {
		t.Fatal(e)
	}
	after := claimTestTask(t, a, "after-reconcile")
	if after.ID != "T-90001" {
		t.Fatal(after.ID)
	}
}

func TestLegacyWorkstreamKeepsIdentity(t *testing.T) {
	a := newAttributionAPI(t, "agent:seed")
	legacy := claimTestTask(t, a, "legacy")
	if _, e := a.db.Exec("UPDATE tasks SET kind='workstream' WHERE uuid=?", legacy.UUID); e != nil {
		t.Fatal(e)
	}
	updated, e := a.TaskUpdate(context.Background(), TaskUpdateParams{Task: legacy.ID, Patch: TaskPatch{Title: strp("Legacy updated")}})
	if e != nil || updated.ID != legacy.ID || updated.Kind != "workstream" {
		t.Fatalf("legacy: %+v %v", updated, e)
	}
	_, e = a.TaskUpdate(context.Background(), TaskUpdateParams{Task: legacy.ID, Patch: TaskPatch{Kind: strp("task")}})
	if e == nil || claimErrorCode(t, e) != "WRKQ_WORKSTREAM_KIND_FIXED" {
		t.Fatalf("legacy conversion: %v", e)
	}
}
