//go:build wrkq_local

package workflow

import "testing"

func TestWatchWaitingUsesSemanticInstanceStatus(t *testing.T) {
	svc, taskUUID, database := setupCASFixture(t)

	snap, err := svc.WatchSnapshot(taskUUID, WatchUntilWaiting)
	if err != nil {
		t.Fatalf("WatchSnapshot waiting: %v", err)
	}
	if snap.Met {
		t.Fatalf("active/ready instance with no active runs satisfied waiting: %+v", snap)
	}

	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if _, err := database.Exec(`UPDATE workflow_instances SET status = 'waiting', phase = '', outcome = '' WHERE id = ?`, inst.ID); err != nil {
		t.Fatalf("set waiting status: %v", err)
	}

	snap, err = svc.WatchSnapshot(taskUUID, WatchUntilWaiting)
	if err != nil {
		t.Fatalf("WatchSnapshot semantic waiting: %v", err)
	}
	if !snap.Met || snap.Class != WatchClassWaiting || snap.ExitCode != 0 {
		t.Fatalf("waiting snapshot = met:%v class:%q exit:%d, want met waiting exit 0", snap.Met, snap.Class, snap.ExitCode)
	}
}

func TestWatchSuspendedUsesActiveSuspension(t *testing.T) {
	svc, taskUUID, _ := setupSuspendOutcomeFixture(t)
	snap, err := svc.WatchSnapshot(taskUUID, WatchUntilSuspended)
	if err != nil {
		t.Fatalf("WatchSnapshot suspended before park: %v", err)
	}
	if snap.Met {
		t.Fatalf("running instance satisfied suspended predicate: %+v", snap)
	}
	if _, err := svc.Transition(taskUUID, "park", TransitionOptions{PrincipalRef: "agent:watch", Role: "coordinator"}); err != nil {
		t.Fatalf("Transition park: %v", err)
	}
	snap, err = svc.WatchSnapshot(taskUUID, WatchUntilSuspended)
	if err != nil {
		t.Fatalf("WatchSnapshot suspended after park: %v", err)
	}
	if !snap.Met || snap.Class != WatchClassSuspended || snap.ExitCode != 0 || snap.Instance == nil || snap.Instance.Suspension == nil {
		t.Fatalf("suspended snapshot = %+v", snap)
	}
}

func TestWatchRunTerminalClassMapping(t *testing.T) {
	cases := []struct {
		status string
		code   int
		class  string
	}{
		{"completed", 0, WatchClassSuccess},
		{"failed", 10, WatchClassFailure},
		{"semantic_blocked", 10, WatchClassFailure},
		{"operational_failed", 10, WatchClassFailure},
		{"operator_required", 10, WatchClassFailure},
		{"cancelled", 11, WatchClassCancelled},
		{"canceled", 11, WatchClassCancelled},
		{"aborted", 11, WatchClassCancelled},
	}

	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			svc, taskUUID := setupRunFixture(t)
			run, err := svc.StartRun(taskUUID, "coordinator", "agent:watch", StartRunOptions{})
			if err != nil {
				t.Fatalf("StartRun: %v", err)
			}
			if _, err := svc.FinishRun(run.ID, tc.status, "terminal"); err != nil {
				t.Fatalf("FinishRun(%s): %v", tc.status, err)
			}

			snap, err := svc.WatchSnapshot(run.ID, WatchUntilTerminal)
			if err != nil {
				t.Fatalf("WatchSnapshot(%s): %v", tc.status, err)
			}
			if !snap.Met || snap.ExitCode != tc.code || snap.Class != tc.class {
				t.Fatalf("status %s snapshot = met:%v code:%d class:%s, want met code:%d class:%s", tc.status, snap.Met, snap.ExitCode, snap.Class, tc.code, tc.class)
			}
		})
	}
}

func TestWatchInstanceClosedClassMapping(t *testing.T) {
	svc, taskUUID, database := setupCASFixture(t)

	if _, err := svc.Transition(taskUUID, "complete", TransitionOptions{PrincipalRef: "agent:watch", Role: "coordinator"}); err != nil {
		t.Fatalf("Transition complete: %v", err)
	}
	success, err := svc.WatchSnapshot(taskUUID, WatchUntilClosed)
	if err != nil {
		t.Fatalf("WatchSnapshot success: %v", err)
	}
	if !success.Met || success.ExitCode != 0 || success.Class != WatchClassSuccess {
		t.Fatalf("closed/done snapshot = met:%v code:%d class:%s, want success", success.Met, success.ExitCode, success.Class)
	}

	if _, err := database.Exec(`UPDATE workflow_instances SET outcome = 'cancelled' WHERE id = ?`, success.Target.InstanceID); err != nil {
		t.Fatalf("set cancelled outcome: %v", err)
	}
	cancelled, err := svc.WatchSnapshot(taskUUID, WatchUntilClosed)
	if err != nil {
		t.Fatalf("WatchSnapshot cancelled: %v", err)
	}
	if !cancelled.Met || cancelled.ExitCode != 11 || cancelled.Class != WatchClassCancelled {
		t.Fatalf("closed/cancelled snapshot = met:%v code:%d class:%s, want cancelled exit 11", cancelled.Met, cancelled.ExitCode, cancelled.Class)
	}
}

func TestWatchEventsIncludeRunLifecycleInOrder(t *testing.T) {
	svc, taskUUID := setupRunFixture(t)
	run, err := svc.StartRun(taskUUID, "coordinator", "agent:watch", StartRunOptions{})
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if _, err := svc.FinishRun(run.ID, "completed", "done"); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	events, err := svc.WatchEvents(run.ID, 0, 10)
	if err != nil {
		t.Fatalf("WatchEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("run watch events len=%d, want 2: %+v", len(events), events)
	}
	if events[0].Type != "workflow.run_started" || events[1].Type != "workflow.run_finished" {
		t.Fatalf("event types = %q, %q; want run_started, run_finished", events[0].Type, events[1].Type)
	}
	if events[0].Seq >= events[1].Seq {
		t.Fatalf("events not ordered by increasing seq: %d >= %d", events[0].Seq, events[1].Seq)
	}
}