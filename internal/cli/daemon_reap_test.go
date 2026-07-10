package cli

import (
	"context"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/workflow"
)

func TestActionReapSweepReapsExpiredLeaseAndStops(t *testing.T) {
	database, _ := setupTestEnv(t)
	const taskUUID = "00000000-0000-0000-0000-000000000003"
	if _, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES (?, 'T-00001', 'reap-sweep', 'Reap Sweep', '00000000-0000-0000-0000-000000000002', 'open', 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`, taskUUID); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	svc := workflow.NewService(database)
	run, err := svc.StartAction(workflow.StartActionParams{
		Task:         taskUUID,
		Action:       "triage",
		PrincipalRef: "agent:test-user",
		LeaseOwner:   "dead-runner",
		LeaseMs:      300000,
	})
	if err != nil {
		t.Fatalf("StartAction: %v", err)
	}
	if _, err := database.Exec(`UPDATE workflow_runs SET lease_expires_at = '2000-01-01T00:00:00Z' WHERE id = ?`, run.RunID); err != nil {
		t.Fatalf("expire action lease: %v", err)
	}

	stop := startActionReapSweep(context.Background(), svc, time.Millisecond)
	defer stop()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		reaped, err := svc.ShowAction(run.RunID)
		if err != nil {
			t.Fatalf("ShowAction: %v", err)
		}
		if reaped.Status == "failed" {
			stop()
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("expired action %s was not reaped by the scheduled sweep", run.RunID)
}
