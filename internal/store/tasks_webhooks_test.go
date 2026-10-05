package store

import (
	"os"
	"testing"
	"time"
)

func TestTaskStoreUpdateFieldsDispatchesWebhook(t *testing.T) {
	t.Setenv("WRKQ_CAUSATION_REF", "  jrun_parent_123  ")
	f := newWebhookFixture(t)
	container := f.project("project")
	meta := `{"triage_status":"queued"}`
	result := f.task(container, "task", "open", func(p *CreateParams) { p.Meta = &meta })
	calls := f.captureWebhooks(container, "/hook/{ticket_id}", "ftp://invalid")

	f.update(result, map[string]interface{}{"state": "in_progress"})

	got := receiveWebhook(t, calls, 2*time.Second)
	if expectedPath := "/hook/" + result.ID; got.path != expectedPath {
		t.Fatalf("unexpected path: %s (expected %s)", got.path, expectedPath)
	}
	payload := got.payload
	if payload.TicketID != result.ID {
		t.Fatalf("unexpected ticket_id: %s", payload.TicketID)
	}
	if payload.TicketUUID != result.UUID {
		t.Fatalf("unexpected ticket_uuid: %s", payload.TicketUUID)
	}
	if payload.ProjectID != container.ID {
		t.Fatalf("unexpected project_id: %s", payload.ProjectID)
	}
	if payload.ProjectUUID != container.UUID {
		t.Fatalf("unexpected project_uuid: %s", payload.ProjectUUID)
	}
	if payload.State != "in_progress" {
		t.Fatalf("unexpected state: %s", payload.State)
	}
	if payload.Priority != 2 {
		t.Fatalf("unexpected priority: %d", payload.Priority)
	}
	if payload.Kind != "task" {
		t.Fatalf("unexpected kind: %s", payload.Kind)
	}
	if string(payload.Meta) == "" || string(payload.Meta) == "null" {
		t.Fatalf("unexpected meta payload: %s", string(payload.Meta))
	}
	if payload.ETag != 3 {
		t.Fatalf("unexpected etag: %d", payload.ETag)
	}
	if payload.Resolution != nil {
		t.Fatalf("unexpected resolution: %s", *payload.Resolution)
	}
	if payload.SchemaVersion != 2 {
		t.Fatalf("unexpected schema_version: %d", payload.SchemaVersion)
	}
	if payload.Event != "updated" {
		t.Fatalf("unexpected event: %s", payload.Event)
	}
	if payload.EventID == "" || payload.EventSeq == 0 || payload.OccurredAt == "" {
		t.Fatalf("missing event identity fields: %+v", payload)
	}
	// Principal-only: origin.actor is now the bare principal ref (agent:<id>),
	// not the legacy role:slug derived from the actors table.
	if payload.Origin.Actor != "agent:"+f.actor || payload.Origin.Via != "cli" {
		t.Fatalf("unexpected origin: %+v", payload.Origin)
	}
	if payload.Origin.RunID != nil {
		t.Fatalf("origin.run_id must remain untouched, got %q", *payload.Origin.RunID)
	}
	if payload.Origin.CausationRef == nil || *payload.Origin.CausationRef != "jrun_parent_123" {
		t.Fatalf("origin.causation_ref: want jrun_parent_123, got %+v", payload.Origin.CausationRef)
	}
	if payload.ProjectScopeID != "project" || payload.ContainerPath != "project" {
		t.Fatalf("unexpected scope/container: %s / %s", payload.ProjectScopeID, payload.ContainerPath)
	}
	if payload.Transition == nil || payload.Transition.From == nil || *payload.Transition.From != "open" ||
		payload.Transition.To == nil || *payload.Transition.To != "in_progress" {
		t.Fatalf("unexpected transition: %+v", payload.Transition)
	}
	if len(payload.Changed) != 1 || payload.Changed[0] != "state" {
		t.Fatalf("unexpected changed: %+v", payload.Changed)
	}
	if change, ok := payload.Changes["state"]; !ok || change.From != "open" || change.To != "in_progress" {
		t.Fatalf("unexpected state change: %+v", payload.Changes["state"])
	}
}

func TestTaskStoreCreateDispatchesWebhookV2(t *testing.T) {
	t.Setenv("WRKQ_CAUSATION_REF", "") // restored after the test
	if err := os.Unsetenv("WRKQ_CAUSATION_REF"); err != nil {
		t.Fatalf("unset WRKQ_CAUSATION_REF: %v", err)
	}
	f := newWebhookFixture(t)
	container := f.project("project")
	calls := f.captureWebhooks(container, "/hook")

	result := f.task(container, "task", "open", func(p *CreateParams) { p.Labels = `["alpha","beta"]` })

	payload := receiveWebhook(t, calls, 2*time.Second).payload
	if payload.TicketUUID != result.UUID || payload.Event != "created" || payload.SchemaVersion != 2 {
		t.Fatalf("unexpected create payload: %+v", payload)
	}
	if payload.EventID == "" || payload.EventSeq == 0 || payload.OccurredAt == "" {
		t.Fatalf("missing event identity: %+v", payload)
	}
	if payload.Transition == nil || payload.Transition.From != nil || payload.Transition.To == nil || *payload.Transition.To != "open" {
		t.Fatalf("unexpected create transition: %+v", payload.Transition)
	}
	if len(payload.Labels) != 2 || payload.Labels[0] != "alpha" || payload.Labels[1] != "beta" {
		t.Fatalf("unexpected labels: %+v", payload.Labels)
	}
	if payload.Origin.CausationRef != nil {
		t.Fatalf("origin.causation_ref must be absent when WRKQ_CAUSATION_REF is unset, got %+v", payload.Origin.CausationRef)
	}
}

func TestTaskStoreCreateWebhookExposesNeedsSmoketestLabelEdge(t *testing.T) {
	f := newWebhookFixture(t)
	container := f.project("project")
	calls := f.captureWebhooks(container, "/hook")

	task := f.task(container, "created-needs-smoketest", "open", func(p *CreateParams) { p.Labels = `["needs_smoketest","ui"]` })

	payload := receiveWebhook(t, calls, 2*time.Second).payload
	if payload.TicketUUID != task.UUID || payload.Event != "created" {
		t.Fatalf("unexpected create payload: %+v", payload)
	}
	if !webhookLabelsContainStrings(payload.Labels, "needs_smoketest") {
		t.Fatalf("top-level labels missing needs_smoketest: %+v", payload.Labels)
	}
	change, ok := payload.Changes["labels"]
	if !ok {
		t.Fatalf("missing labels change: %+v", payload.Changes)
	}
	if change.From != nil {
		t.Fatalf("labels from = %+v, want nil for create", change.From)
	}
	if toLabels := webhookChangeLabels(t, change.To); !webhookLabelsContainInterfaces(toLabels, "needs_smoketest") {
		t.Fatalf("labels to missing needs_smoketest: %+v", toLabels)
	}
}

func TestTaskStoreUpdateWebhookExposesNeedsSmoketestLabelAdditionEdge(t *testing.T) {
	f := newWebhookFixture(t)
	container := f.project("project")
	task := f.task(container, "updated-needs-smoketest", "open", func(p *CreateParams) { p.Labels = `["ui"]` })
	calls := f.captureWebhooks(container, "/hook")

	f.update(task, map[string]interface{}{"labels": `["ui","needs_smoketest"]`})

	payload := receiveWebhook(t, calls, 2*time.Second).payload
	if payload.TicketUUID != task.UUID || payload.Event != "updated" {
		t.Fatalf("unexpected update payload: %+v", payload)
	}
	if len(payload.Changed) != 1 || payload.Changed[0] != "labels" {
		t.Fatalf("changed = %+v, want [labels]", payload.Changed)
	}
	if !webhookLabelsContainStrings(payload.Labels, "needs_smoketest") {
		t.Fatalf("top-level labels missing needs_smoketest: %+v", payload.Labels)
	}
	change, ok := payload.Changes["labels"]
	if !ok {
		t.Fatalf("missing labels change: %+v", payload.Changes)
	}
	if fromLabels := webhookChangeLabels(t, change.From); webhookLabelsContainInterfaces(fromLabels, "needs_smoketest") {
		t.Fatalf("labels from should not contain needs_smoketest: %+v", fromLabels)
	}
	if toLabels := webhookChangeLabels(t, change.To); !webhookLabelsContainInterfaces(toLabels, "needs_smoketest") {
		t.Fatalf("labels to missing needs_smoketest: %+v", toLabels)
	}
}

func TestTaskStoreMoveDispatchesWebhookV2(t *testing.T) {
	f := newWebhookFixture(t)
	source := f.project("source")
	dest := f.project("dest")
	task := f.task(source, "task", "open")
	calls := f.captureWebhooks(dest, "/hook")

	if _, err := f.s.Tasks.Move(f.actor, task.UUID, dest.UUID, 0); err != nil {
		t.Fatalf("failed to move task: %v", err)
	}

	payload := receiveWebhook(t, calls, 2*time.Second).payload
	if payload.Event != "moved" {
		t.Fatalf("unexpected move event: %s", payload.Event)
	}
	if payload.ProjectScopeID != "dest" || payload.ContainerPath != "dest" {
		t.Fatalf("unexpected moved scope/container: %s / %s", payload.ProjectScopeID, payload.ContainerPath)
	}
	if payload.Transition != nil {
		t.Fatalf("move should not report a state transition: %+v", payload.Transition)
	}
	if len(payload.Changed) != 2 || payload.Changed[0] != "container_path" || payload.Changed[1] != "project_uuid" {
		t.Fatalf("unexpected changed fields: %+v", payload.Changed)
	}
}

func webhookChangeLabels(t *testing.T, value interface{}) []interface{} {
	t.Helper()
	switch labels := value.(type) {
	case []interface{}:
		return labels
	case []string:
		out := make([]interface{}, 0, len(labels))
		for _, label := range labels {
			out = append(out, label)
		}
		return out
	default:
		t.Fatalf("labels change endpoint = %+v (%T), want JSON label array", value, value)
		return nil
	}
}

func webhookLabelsContainStrings(labels []string, needle string) bool {
	for _, label := range labels {
		if label == needle {
			return true
		}
	}
	return false
}

func webhookLabelsContainInterfaces(labels []interface{}, needle string) bool {
	for _, label := range labels {
		if label == needle {
			return true
		}
	}
	return false
}
