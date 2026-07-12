package workflow

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/webhooks"
)

func setupWorkflowWebhookFixture(t *testing.T, webhookURLs string) (*Service, string, *db.DB) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "workflow_webhook_test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	svc := NewService(database)
	tplPath := filepath.Join(tmpDir, "cas_test.json")
	if err := os.WriteFile(tplPath, []byte(casTestTemplate), 0644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	if _, err := svc.InstallTemplate(tplPath, "test-actor", nil); err != nil {
		t.Fatalf("InstallTemplate: %v", err)
	}

	actorUUID := "cccccccc-cccc-4ccc-cccc-000000000101"
	if _, err := database.Exec(
		`INSERT INTO actors (uuid, slug, role) VALUES (?, 'workflow-webhook-actor', 'system')`,
		actorUUID,
	); err != nil {
		t.Fatalf("insert actor: %v", err)
	}

	containerUUID := "aaaaaaaa-aaaa-4aaa-aaaa-000000000101"
	if _, err := database.Exec(
		`INSERT INTO containers (uuid, slug, title, parent_uuid, kind, webhook_urls, created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'workflow-webhook-project', 'Workflow Webhook Project', (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?, ?)`,
		containerUUID, webhookURLs, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert container: %v", err)
	}

	taskUUID := "bbbbbbbb-bbbb-4bbb-bbbb-000000000101"
	if _, err := database.Exec(
		`INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, kind,
		                    created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'workflow-webhook-task', 'Workflow Webhook Test Task', ?, 'open', 2, 'task', ?, ?)`,
		taskUUID, containerUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	return svc, taskUUID, database
}

func webhookCaptureServer(t *testing.T, calls chan<- webhooks.Payload) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		body, _ := io.ReadAll(r.Body)
		var payload webhooks.Payload
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode webhook payload: %v", err)
		}
		calls <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
}

func TestWorkflowAttachDispatchesWebhook(t *testing.T) {
	calls := make(chan webhooks.Payload, 2)
	server := webhookCaptureServer(t, calls)
	defer server.Close()

	webhookURLs, _ := json.Marshal([]interface{}{
		map[string]interface{}{"url": server.URL + "/hook/{ticket_id}", "events": []string{"workflow_attached"}},
	})
	svc, taskUUID, _ := setupWorkflowWebhookFixture(t, string(webhookURLs))

	inst, err := svc.AttachTask(taskUUID, "cas_test@1", "attach-actor")
	if err != nil {
		t.Fatalf("AttachTask: %v", err)
	}

	select {
	case payload := <-calls:
		if payload.SchemaVersion != 2 || payload.Event != webhooks.EventWorkflowAttached {
			t.Fatalf("unexpected attach payload header: %+v", payload)
		}
		if payload.TicketID == "" || payload.TicketUUID != taskUUID {
			t.Fatalf("attach payload missing task identity: %+v", payload)
		}
		if payload.Subject == nil || payload.Subject.TicketID != payload.TicketID || payload.Subject.TicketUUID != taskUUID || payload.Subject.WorkflowInstanceID != inst.ID {
			t.Fatalf("attach payload missing subject identity: %+v", payload.Subject)
		}
		if payload.Workflow == nil || payload.Workflow.InstanceID != inst.ID || payload.Workflow.Template != "cas_test@1" {
			t.Fatalf("attach payload missing workflow identity: %+v", payload.Workflow)
		}
		if payload.Workflow.Type != "workflow.attached" || payload.Workflow.SchemaVersion != "wrkf.workflow-event.v0" || payload.Workflow.EventID == "" || payload.Workflow.EventSeq != 1 {
			t.Fatalf("attach payload missing workflow event identity: %+v", payload.Workflow)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for attach webhook")
	}
}

func TestWorkflowTransitionDispatchesWebhookAndSkipsIdempotentReplay(t *testing.T) {
	calls := make(chan webhooks.Payload, 4)
	server := webhookCaptureServer(t, calls)
	defer server.Close()

	webhookURLs, _ := json.Marshal([]interface{}{
		map[string]interface{}{"url": server.URL + "/hook", "events": []string{"workflow.*"}},
	})
	svc, taskUUID, _ := setupWorkflowWebhookFixture(t, string(webhookURLs))
	if _, err := svc.AttachTask(taskUUID, "cas_test@1", "attach-actor"); err != nil {
		t.Fatalf("AttachTask: %v", err)
	}
	<-calls // workflow_attached

	expectRev := int64(0)
	result, err := svc.Transition(taskUUID, "complete", TransitionOptions{
		PrincipalRef:   "transition-actor",
		Role:           "coordinator",
		ExpectRevision: &expectRev,
		IdempotencyKey: "transition-once",
		RunID:          "run_123",
	})
	if err != nil {
		t.Fatalf("Transition: %v", err)
	}

	select {
	case payload := <-calls:
		if payload.Event != webhooks.EventWorkflowTransitioned || payload.SchemaVersion != 2 {
			t.Fatalf("unexpected transition payload header: %+v", payload)
		}
		if payload.Workflow == nil {
			t.Fatalf("transition payload missing workflow body: %+v", payload)
		}
		if payload.Workflow.InstanceID == "" || payload.Workflow.Transition != "complete" || payload.Workflow.Outcome != "done" {
			t.Fatalf("transition payload missing transition identity: %+v", payload.Workflow)
		}
		if payload.Workflow.ObservedRevision == nil || *payload.Workflow.ObservedRevision != 0 || payload.Workflow.NextRevision == nil || *payload.Workflow.NextRevision != 1 {
			t.Fatalf("transition payload missing revisions: %+v", payload.Workflow)
		}
		if payload.Workflow.RunID == nil || *payload.Workflow.RunID != "run_123" || payload.Origin.RunID == nil || *payload.Origin.RunID != "run_123" {
			t.Fatalf("transition payload missing run id: origin=%+v workflow=%+v", payload.Origin, payload.Workflow)
		}
		if payload.Subject == nil || payload.Subject.TicketID == "" || payload.Subject.TicketUUID != taskUUID || payload.Subject.WorkflowInstanceID != payload.Workflow.InstanceID {
			t.Fatalf("transition payload missing subject identity: %+v", payload.Subject)
		}
		if payload.Transition == nil || payload.Transition.From == nil || *payload.Transition.From != "active:ready" || payload.Transition.To == nil || *payload.Transition.To != "closed:done" {
			t.Fatalf("transition payload missing state transition: %+v", payload.Transition)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for transition webhook")
	}

	replayed, err := svc.Transition(taskUUID, "complete", TransitionOptions{
		PrincipalRef:   "transition-actor",
		Role:           "coordinator",
		ExpectRevision: &expectRev,
		IdempotencyKey: "transition-once",
		RunID:          "run_123",
	})
	if err != nil {
		t.Fatalf("Transition replay: %v", err)
	}
	if result["eventId"] != replayed["eventId"] {
		t.Fatalf("replay returned different event id: first=%v replay=%v", result["eventId"], replayed["eventId"])
	}
	select {
	case payload := <-calls:
		t.Fatalf("unexpected webhook for idempotent replay: %+v", payload)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestWorkflowSuspensionLifecycleDispatchesCoherentWebhooks(t *testing.T) {
	calls := make(chan webhooks.Payload, 4)
	server := webhookCaptureServer(t, calls)
	defer server.Close()

	webhookURLs, _ := json.Marshal([]interface{}{
		map[string]interface{}{"url": server.URL + "/hook", "events": []string{"workflow.*"}},
	})
	svc, taskUUID, _ := setupWorkflowWebhookFixture(t, string(webhookURLs))
	templatePath := filepath.Join(t.TempDir(), "suspend-webhook.json")
	if err := os.WriteFile(templatePath, []byte(suspendOutcomeTemplate), 0o644); err != nil {
		t.Fatalf("write suspension template: %v", err)
	}
	if _, err := svc.InstallTemplate(templatePath, "test-installer", nil); err != nil {
		t.Fatalf("InstallTemplate: %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, "suspend-outcome-test@1", "attach-actor"); err != nil {
		t.Fatalf("AttachTask: %v", err)
	}
	<-calls
	if _, err := svc.Transition(taskUUID, "park", TransitionOptions{PrincipalRef: "agent:op", Role: "coordinator"}); err != nil {
		t.Fatalf("Transition park: %v", err)
	}

	parked := <-calls
	if parked.Event != webhooks.EventWorkflowSuspended || parked.Workflow == nil || parked.Workflow.Type != "workflow.suspended" || parked.Workflow.Suspension == nil {
		t.Fatalf("suspension webhook = %+v", parked)
	}
	if parked.Workflow.BeforeRevision == nil || *parked.Workflow.BeforeRevision != 0 || parked.Workflow.AfterRevision == nil || *parked.Workflow.AfterRevision != 1 {
		t.Fatalf("suspension webhook revisions = %+v", parked.Workflow)
	}
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if _, err := svc.ResolveSuspension(ResolveSuspensionParams{SuspensionID: inst.Suspension.ID, Disposition: DispositionResume, PrincipalRef: "human:op"}); err != nil {
		t.Fatalf("ResolveSuspension: %v", err)
	}
	resolved := <-calls
	if resolved.Event != webhooks.EventWorkflowSuspensionResolved || resolved.Workflow == nil || resolved.Workflow.Type != "workflow.suspension_resolved" || resolved.Workflow.Suspension != nil || resolved.Workflow.Disposition != "resume" {
		t.Fatalf("resolution webhook = %+v", resolved)
	}
	resolvedEventPayload, err := json.Marshal(resolved.Workflow.Payload)
	if err != nil || !strings.Contains(string(resolvedEventPayload), `"suspension"`) {
		t.Fatalf("resolution webhook event payload does not carry suspension record: %s (err=%v)", resolvedEventPayload, err)
	}
	if resolved.Workflow.BeforeRevision == nil || *resolved.Workflow.BeforeRevision != 1 || resolved.Workflow.AfterRevision == nil || *resolved.Workflow.AfterRevision != 2 {
		t.Fatalf("resolution webhook revisions = %+v", resolved.Workflow)
	}
}

// TestWorkflowWebhookLegacyStringDefaultsToAllTaskOnlyNarrows asserts the
// post-T-05272 contract: a legacy/bare URL string with no explicit events
// receives workflow events by default, while an object-form subscriber
// narrowed to task-only is still excluded from workflow events.
func TestWorkflowWebhookLegacyStringDefaultsToAllTaskOnlyNarrows(t *testing.T) {
	legacyAllCalls := make(chan webhooks.Payload, 2)
	legacyAllServer := webhookCaptureServer(t, legacyAllCalls)
	defer legacyAllServer.Close()

	taskOnlyCalls := make(chan webhooks.Payload, 2)
	taskOnlyServer := webhookCaptureServer(t, taskOnlyCalls)
	defer taskOnlyServer.Close()

	webhookURLs, _ := json.Marshal([]interface{}{
		legacyAllServer.URL + "/legacy",
		map[string]interface{}{"url": taskOnlyServer.URL + "/task-only", "events": []string{"task.*"}},
	})
	svc, taskUUID, _ := setupWorkflowWebhookFixture(t, string(webhookURLs))
	if _, err := svc.AttachTask(taskUUID, "cas_test@1", "attach-actor"); err != nil {
		t.Fatalf("AttachTask: %v", err)
	}

	// Legacy bare-string subscriber receives the workflow_attached event.
	select {
	case payload := <-legacyAllCalls:
		if payload.Event != webhooks.EventWorkflowAttached {
			t.Fatalf("legacy subscriber received wrong event on attach: %+v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for legacy-string workflow attach webhook")
	}
	// Task-only object subscriber is excluded from workflow attach.
	select {
	case payload := <-taskOnlyCalls:
		t.Fatalf("task-only object webhook should not receive workflow attach: %+v", payload)
	case <-time.After(300 * time.Millisecond):
	}

	expectRev := int64(0)
	if _, err := svc.Transition(taskUUID, "complete", TransitionOptions{PrincipalRef: "transition-actor", Role: "coordinator", ExpectRevision: &expectRev}); err != nil {
		t.Fatalf("Transition: %v", err)
	}

	// Legacy bare-string subscriber also receives the workflow_transitioned event.
	select {
	case payload := <-legacyAllCalls:
		if payload.Event != webhooks.EventWorkflowTransitioned {
			t.Fatalf("legacy subscriber received wrong event on transition: %+v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for legacy-string workflow transition webhook")
	}
	// Task-only object subscriber is still excluded from workflow transition.
	select {
	case payload := <-taskOnlyCalls:
		t.Fatalf("task-only object webhook should not receive workflow transition: %+v", payload)
	case <-time.After(300 * time.Millisecond):
	}
}
