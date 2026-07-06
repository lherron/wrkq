package workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAttachTaskSupersedeReplacesLiveInstanceWithLineage(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	for _, ref := range []string{BuiltinSimpleTaskV2TemplateRef, BuiltinSimpleTaskV3TemplateRef} {
		if _, _, err := svc.EnsureBuiltinTemplate(ref, "agent:t"); err != nil {
			t.Fatalf("EnsureBuiltinTemplate(%s): %v", ref, err)
		}
	}

	first, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV2TemplateRef, "agent:t")
	if err != nil {
		t.Fatalf("AttachTask(v2): %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV3TemplateRef, "agent:t"); err == nil {
		t.Fatalf("AttachTask over live instance without supersede succeeded")
	}

	staleRevision := first.Revision + 1
	if _, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV3TemplateRef, "agent:t", AttachTaskOptions{
		Supersede:             true,
		PredecessorInstanceID: first.ID,
		PredecessorRevision:   &staleRevision,
	}); err == nil || !strings.Contains(err.Error(), "revision mismatch") {
		t.Fatalf("stale predecessor guard error = %v, want revision mismatch", err)
	}

	revision := first.Revision
	second, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV3TemplateRef, "agent:t", AttachTaskOptions{
		Supersede:             true,
		PredecessorInstanceID: first.ID,
		PredecessorRevision:   &revision,
	})
	if err != nil {
		t.Fatalf("AttachTask supersede: %v", err)
	}
	if second.TemplateVersion != "3" || second.Status != "active" {
		t.Fatalf("successor = %+v, want active wrkq-simple-task@3", second)
	}
	if second.Supersedes == nil || second.Supersedes.InstanceID != first.ID || second.Supersedes.Phase != "superseded" {
		t.Fatalf("successor lineage = %+v, want superseded predecessor %s", second.Supersedes, first.ID)
	}
	if got := readTaskState(t, svc, taskUUID); got != "open" {
		t.Fatalf("task state after supersede = %q, want open", got)
	}

	old, err := svc.instanceByID(first.ID)
	if err != nil {
		t.Fatalf("instanceByID(first): %v", err)
	}
	if old.Status != "closed" || old.Phase != "superseded" {
		t.Fatalf("predecessor state = %+v, want closed/superseded", old.State())
	}
	if old.SupersededBy == nil || old.SupersededBy.InstanceID != second.ID {
		t.Fatalf("predecessor successor lineage = %+v, want %s", old.SupersededBy, second.ID)
	}

	latest, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if latest.ID != second.ID || latest.Supersedes == nil || latest.Supersedes.InstanceID != first.ID {
		t.Fatalf("latest inspect projection = %+v, want successor with predecessor lineage", latest)
	}

	snap, err := svc.WatchSnapshot(first.ID, WatchUntilClosed)
	if err != nil {
		t.Fatalf("WatchSnapshot predecessor: %v", err)
	}
	if !snap.Met || snap.Class != WatchClassFailure || snap.ExitCode == 0 {
		t.Fatalf("superseded watch snapshot = %+v, want non-success failure", snap)
	}

	if got := countLiveInstances(t, svc, taskUUID); got != 1 {
		t.Fatalf("live instance count = %d, want 1", got)
	}
	if got := countSupersedeEvents(t, svc, first.ID, second.ID); got != 2 {
		t.Fatalf("supersede lineage event count = %d, want 2", got)
	}
}

func TestAttachTaskSupersedeRejectsStalePredecessorAfterReplacement(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	for _, ref := range []string{BuiltinSimpleTaskV2TemplateRef, BuiltinSimpleTaskV3TemplateRef} {
		if _, _, err := svc.EnsureBuiltinTemplate(ref, "agent:t"); err != nil {
			t.Fatalf("EnsureBuiltinTemplate(%s): %v", ref, err)
		}
	}

	first, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV2TemplateRef, "agent:t")
	if err != nil {
		t.Fatalf("AttachTask(v2): %v", err)
	}
	revision := first.Revision
	if _, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV3TemplateRef, "agent:t", AttachTaskOptions{
		Supersede:             true,
		PredecessorInstanceID: first.ID,
		PredecessorRevision:   &revision,
	}); err != nil {
		t.Fatalf("AttachTask supersede: %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV3TemplateRef, "agent:t", AttachTaskOptions{
		Supersede:             true,
		PredecessorInstanceID: first.ID,
		PredecessorRevision:   &revision,
	}); err == nil || !strings.Contains(err.Error(), "current live workflow instance") {
		t.Fatalf("stale predecessor after replacement error = %v, want current live guard", err)
	}
	if got := countLiveInstances(t, svc, taskUUID); got != 1 {
		t.Fatalf("live instance count after stale attempt = %d, want 1", got)
	}
}

func TestWorkflowInstanceActiveUniqueIndexIntact(t *testing.T) {
	svc, _ := actionFixture(t)
	var sqlText string
	if err := svc.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = 'workflow_instances_one_active_per_task'`).Scan(&sqlText); err != nil {
		t.Fatalf("read unique index: %v", err)
	}
	if !strings.Contains(sqlText, "WHERE status != 'closed'") {
		t.Fatalf("unique index SQL = %q, want non-closed partial predicate", sqlText)
	}
}

func countLiveInstances(t *testing.T, svc *Service, taskUUID string) int {
	t.Helper()
	var got int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM workflow_instances WHERE task_uuid = ? AND status != 'closed'`, taskUUID).Scan(&got); err != nil {
		t.Fatalf("count live instances: %v", err)
	}
	return got
}

func countSupersedeEvents(t *testing.T, svc *Service, predecessorID, successorID string) int {
	t.Helper()
	rows, err := svc.db.Query(`
		SELECT type, payload_json
		FROM workflow_events
		WHERE (instance_id = ? AND type = 'workflow.superseded')
		   OR (instance_id = ? AND type = 'workflow.attached')
	`, predecessorID, successorID)
	if err != nil {
		t.Fatalf("query supersede events: %v", err)
	}
	defer func() { _ = rows.Close() }()

	count := 0
	for rows.Next() {
		var typ, raw string
		if err := rows.Scan(&typ, &raw); err != nil {
			t.Fatalf("scan event: %v", err)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("payload JSON: %v", err)
		}
		switch typ {
		case "workflow.superseded":
			if _, ok := payload["successor"]; ok {
				count++
			}
		case "workflow.attached":
			if _, ok := payload["supersededPredecessor"]; ok {
				count++
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read events: %v", err)
	}
	return count
}
