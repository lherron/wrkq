package workflow

import (
	"sort"
	"testing"
)

func TestInstancesReturnsAllGenerationsInInspectCompatibleOrder(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	for _, ref := range []string{BuiltinSimpleTaskV2TemplateRef, BuiltinSimpleTaskV3TemplateRef} {
		if _, _, err := svc.EnsureBuiltinTemplate(ref, "agent:t"); err != nil {
			t.Fatalf("EnsureBuiltinTemplate(%s): %v", ref, err)
		}
	}

	empty, err := svc.Instances(taskUUID)
	if err != nil {
		t.Fatalf("Instances(empty): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("Instances(empty) = %#v, want non-nil empty slice", empty)
	}

	first, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV2TemplateRef, "agent:t")
	if err != nil {
		t.Fatalf("AttachTask(first): %v", err)
	}
	revision := first.Revision
	second, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV3TemplateRef, "agent:t", AttachTaskOptions{
		Supersede:             true,
		PredecessorInstanceID: first.ID,
		PredecessorRevision:   &revision,
	})
	if err != nil {
		t.Fatalf("AttachTask(second): %v", err)
	}
	if _, err := svc.db.Exec(`
		UPDATE workflow_instances
		SET status = 'closed', phase = 'done', closed_at = '2026-07-24T03:00:00Z'
		WHERE id = ?
	`, second.ID); err != nil {
		t.Fatalf("close second: %v", err)
	}
	third, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV2TemplateRef, "agent:t")
	if err != nil {
		t.Fatalf("AttachTask(third): %v", err)
	}

	// Make both closed generations newer than the live generation and tie their
	// timestamps. This proves the live-first partition and id DESC tie-break.
	if _, err := svc.db.Exec(`
		UPDATE workflow_instances
		SET created_at = CASE WHEN id = ? THEN '2026-07-24T01:00:00Z' ELSE '2026-07-24T03:00:00Z' END
		WHERE id IN (?, ?, ?)
	`, third.ID, first.ID, second.ID, third.ID); err != nil {
		t.Fatalf("set deterministic creation times: %v", err)
	}

	instances, err := svc.Instances(taskUUID)
	if err != nil {
		t.Fatalf("Instances(populated): %v", err)
	}
	if len(instances) != 3 {
		t.Fatalf("Instances(populated) length = %d, want 3: %#v", len(instances), instances)
	}
	closedIDs := []string{first.ID, second.ID}
	sort.Sort(sort.Reverse(sort.StringSlice(closedIDs)))
	wantIDs := []string{third.ID, closedIDs[0], closedIDs[1]}
	for i, want := range wantIDs {
		if instances[i].ID != want {
			t.Fatalf("instances[%d].ID = %s, want %s; all=%#v", i, instances[i].ID, want, instances)
		}
	}
	if instances[0].Status == "closed" || instances[1].Status != "closed" || instances[2].Status != "closed" {
		t.Fatalf("status partition = [%s %s %s], want live then closed history",
			instances[0].Status, instances[1].Status, instances[2].Status)
	}

	inspect, err := svc.InspectTask(taskUUID)
	if err != nil {
		t.Fatalf("InspectTask: %v", err)
	}
	if inspect.ID != instances[0].ID {
		t.Fatalf("inspect selected %s, instances[0] = %s", inspect.ID, instances[0].ID)
	}

	byID := map[string]*Instance{}
	for _, inst := range instances {
		byID[inst.ID] = inst
	}
	if byID[first.ID].SupersededBy == nil || byID[first.ID].SupersededBy.InstanceID != second.ID {
		t.Fatalf("first supersededBy = %#v, want %s", byID[first.ID].SupersededBy, second.ID)
	}
	if byID[second.ID].Supersedes == nil || byID[second.ID].Supersedes.InstanceID != first.ID {
		t.Fatalf("second supersedes = %#v, want %s", byID[second.ID].Supersedes, first.ID)
	}
}
