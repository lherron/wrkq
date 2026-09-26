//go:build wrkq_local

package workflow

import (
	"encoding/json"
	"testing"
)

func TestWorkflowProjectionEmitsOneMetaOnlyTaskUpdate(t *testing.T) {
	svc, taskUUID, _ := setupCASFixture(t)
	const principal = "agent:timeline-test"

	var beforeCount int
	var beforeETag int64
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM event_log WHERE resource_uuid = ? AND event_type = 'task.updated'`, taskUUID).Scan(&beforeCount); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`SELECT etag FROM tasks WHERE uuid = ?`, taskUUID).Scan(&beforeETag); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Transition(taskUUID, "complete", TransitionOptions{PrincipalRef: principal, Role: "coordinator"}); err != nil {
		t.Fatalf("transition: %v", err)
	}
	var count int
	var gotPrincipal string
	var payloadText string
	var eventETag int64
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM event_log WHERE resource_uuid = ? AND event_type = 'task.updated'`, taskUUID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := svc.db.QueryRow(`
		SELECT principal_ref, payload, etag FROM event_log
		WHERE resource_uuid = ? AND event_type = 'task.updated' ORDER BY id DESC LIMIT 1
	`, taskUUID).Scan(&gotPrincipal, &payloadText, &eventETag); err != nil {
		t.Fatal(err)
	}
	if count != beforeCount+1 || gotPrincipal != principal || eventETag != beforeETag {
		t.Fatalf("task updates=%d (before %d), principal=%q, etag=%d (want %d)", count, beforeCount, gotPrincipal, eventETag, beforeETag)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"meta", "container_uuid", "campaign_uuid"} {
		if _, ok := payload[field]; !ok {
			t.Errorf("event payload lacks %s: %s", field, payloadText)
		}
	}
	if _, ok := payload["state"]; ok {
		t.Fatalf("meta event spuriously carries state: %s", payloadText)
	}
	if _, err := svc.SyncMeta(taskUUID, principal); err != nil {
		t.Fatalf("repeat sync-meta: %v", err)
	}
	var afterCount int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM event_log WHERE resource_uuid = ? AND event_type = 'task.updated'`, taskUUID).Scan(&afterCount); err != nil {
		t.Fatal(err)
	}
	if afterCount != count {
		t.Fatalf("no-op sync-meta added an event: %d -> %d", count, afterCount)
	}
}
