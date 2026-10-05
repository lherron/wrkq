package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/webhooks"
)

func TestUnblockWebhookSingleBlocker(t *testing.T) {
	f := newWebhookFixture(t)
	container := f.project("project")
	taskA := f.task(container, "blocker-task", "open")
	taskB := f.task(container, "blocked-task", "blocked")
	f.blocks(taskB, taskA)
	calls := f.captureWebhooks(container, "/hook/{ticket_id}")

	// Completing A fires its own webhook and B's unblock.
	f.update(taskA, map[string]interface{}{"state": "completed"})
	received := receiveWebhooksByTicket(t, calls, 2, 3*time.Second)

	if payload, ok := received[taskA.UUID]; !ok {
		t.Fatalf("did not receive webhook for completed task A")
	} else if payload.State != "completed" {
		t.Fatalf("task A webhook has wrong state: %s (expected completed)", payload.State)
	}
	payload, ok := received[taskB.UUID]
	switch {
	case !ok:
		t.Fatalf("did not receive webhook for unblocked task B")
	case payload.Event != "unblocked":
		t.Fatalf("unblocked task webhook has wrong event: %s", payload.Event)
	case payload.Transition != nil:
		t.Fatalf("unblocked task should not report a state transition: %+v", payload.Transition)
	case payload.EventID == "" || payload.EventSeq == 0:
		t.Fatalf("unblocked task missing event identity: %+v", payload)
	case len(payload.Changed) != 1 || payload.Changed[0] != "blocked_by":
		t.Fatalf("unblocked task changed fields = %+v", payload.Changed)
	}
}

func TestUnblockWebhookMultipleBlockers(t *testing.T) {
	f := newWebhookFixture(t)
	container := f.project("project")
	taskA1 := f.task(container, "blocker-1", "open")
	taskA2 := f.task(container, "blocker-2", "open")
	taskB := f.task(container, "blocked-task", "blocked")
	f.blocks(taskB, taskA1, taskA2)
	calls := f.captureWebhooks(container, "/hook/{ticket_id}")

	// Completing A1 leaves B blocked by A2: only A1's webhook fires.
	f.update(taskA1, map[string]interface{}{"state": "completed"})
	if got := receiveWebhook(t, calls, 2*time.Second); got.payload.TicketUUID != taskA1.UUID {
		t.Fatalf("expected webhook for A1, got %s", got.payload.TicketUUID)
	}
	expectNoWebhook(t, calls, 500*time.Millisecond, "B should still be blocked")

	// Completing A2 unblocks B.
	f.update(taskA2, map[string]interface{}{"state": "completed"})
	received := receiveWebhooksByTicket(t, calls, 2, 4*time.Second)
	if _, ok := received[taskA2.UUID]; !ok {
		t.Fatalf("did not receive webhook for completed task A2")
	}
	if _, ok := received[taskB.UUID]; !ok {
		t.Fatalf("did not receive webhook for unblocked task B")
	}
}

func TestUnblockWebhookCancelledState(t *testing.T) {
	f := newWebhookFixture(t)
	container := f.project("project")
	blocker := f.task(container, "blocker", "open")
	blocked := f.task(container, "blocked", "blocked")
	f.blocks(blocked, blocker)
	calls := f.captureWebhooks(container, "/hook")

	// Cancelling the blocker also unblocks the blocked task.
	f.update(blocker, map[string]interface{}{"state": "cancelled"})
	received := receiveWebhooksByTicket(t, calls, 2, 4*time.Second)
	if _, ok := received[blocker.UUID]; !ok {
		t.Fatalf("did not receive webhook for cancelled blocker")
	}
	if _, ok := received[blocked.UUID]; !ok {
		t.Fatalf("did not receive webhook for unblocked task")
	}
}

func TestNoUnblockWebhookWhenAlreadyCompleted(t *testing.T) {
	f := newWebhookFixture(t)
	container := f.project("project")
	blocker := f.task(container, "blocker", "completed")
	blocked := f.task(container, "blocked", "blocked")
	f.blocks(blocked, blocker)
	calls := f.captureWebhooks(container, "/hook")

	// Editing an already-completed blocker is not a transition to completion.
	f.update(blocker, map[string]interface{}{"title": "Updated Title"})
	if got := receiveWebhook(t, calls, 2*time.Second); got.payload.TicketUUID != blocker.UUID {
		t.Fatalf("expected webhook for blocker, got %s", got.payload.TicketUUID)
	}
	expectNoWebhook(t, calls, 500*time.Millisecond, "no unblock since blocker was already completed")
}

func TestWebhookPayloadIncludesBlockedBy(t *testing.T) {
	f := newWebhookFixture(t)
	container := f.project("project")
	taskA := f.task(container, "blocker-a", "open")
	taskB := f.task(container, "blocker-b", "in_progress")
	taskC := f.task(container, "blocked-task", "blocked")
	f.blocks(taskC, taskA, taskB)
	calls := f.captureWebhooks(container, "/hook")

	// Any update to C reports both incomplete blockers with their states.
	f.update(taskC, map[string]interface{}{"priority": 1})
	taskCPayload := receiveWebhook(t, calls, 2*time.Second).payload
	if taskCPayload.TicketUUID != taskC.UUID {
		t.Fatalf("expected webhook for task C, got %s", taskCPayload.TicketUUID)
	}
	if len(taskCPayload.BlockedBy) != 2 {
		t.Fatalf("expected 2 blockers, got %d: %+v", len(taskCPayload.BlockedBy), taskCPayload.BlockedBy)
	}
	blockerStates := make(map[string]string) // id -> state
	for _, blocker := range taskCPayload.BlockedBy {
		blockerStates[blocker.ID] = blocker.State
	}
	for _, want := range []struct{ id, state, name string }{{taskA.ID, "open", "A"}, {taskB.ID, "in_progress", "B"}} {
		if state, ok := blockerStates[want.id]; !ok {
			t.Fatalf("task %s (%s) not found in blocked_by", want.name, want.id)
		} else if state != want.state {
			t.Fatalf("task %s has wrong state in blocked_by: %s (expected %s)", want.name, state, want.state)
		}
	}

	// Completing A: A's webhook, and C's only if it is fully unblocked — it
	// is not, so if one arrives it must list B alone.
	f.update(taskA, map[string]interface{}{"state": "completed"})
	first := receiveWebhook(t, calls, 2*time.Second).payload
	afterA := map[string]webhooks.Payload{first.TicketUUID: first}
	select {
	case got := <-calls:
		afterA[got.payload.TicketUUID] = got.payload
	case <-time.After(2 * time.Second):
	}
	if payload, ok := afterA[taskC.UUID]; ok {
		if len(payload.BlockedBy) != 1 {
			t.Fatalf("after A completed, expected 1 blocker, got %d: %+v", len(payload.BlockedBy), payload.BlockedBy)
		}
		if payload.BlockedBy[0].ID != taskB.ID {
			t.Fatalf("expected remaining blocker to be B (%s), got %s", taskB.ID, payload.BlockedBy[0].ID)
		}
	}

	// Completing B fully unblocks C, whose webhook then carries no blockers.
	f.update(taskB, map[string]interface{}{"state": "completed"})
	received := receiveWebhooksByTicket(t, calls, 2, 4*time.Second)
	if payload, ok := received[taskC.UUID]; !ok {
		t.Fatalf("did not receive webhook for fully unblocked task C")
	} else if len(payload.BlockedBy) != 0 {
		t.Fatalf("expected empty blocked_by for fully unblocked task, got: %+v", payload.BlockedBy)
	}
}

func TestWebhookPayloadBlockedByOmittedWhenEmpty(t *testing.T) {
	f := newWebhookFixture(t)
	container := f.project("project")
	task := f.task(container, "unblocked-task", "open")
	calls := f.captureWebhooks(container, "/hook")

	f.update(task, map[string]interface{}{"state": "in_progress"})

	rawPayload := receiveWebhook(t, calls, 2*time.Second).raw
	var payloadMap map[string]interface{}
	if err := json.Unmarshal(rawPayload, &payloadMap); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if _, exists := payloadMap["blocked_by"]; exists {
		t.Fatalf("blocked_by should be omitted when empty, but found in payload: %s", string(rawPayload))
	}
}
