package webhooks_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/webhooks"
)

func TestNamedSubtaskWebhookOwnerFanoutAndIdentity(t *testing.T) {
	d := setupTestDB(t)
	actor := setupTestActor(t, d)
	s := store.New(d)
	container, err := s.Containers.Create(actor, store.ContainerCreateParams{Slug: "proj", Kind: "project"})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.Tasks.Create(actor, store.CreateParams{Slug: "owner", Title: "Owner", State: "open", Priority: 3, ProjectUUID: container.UUID})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.Tasks.Create(actor, store.CreateParams{Slug: "render", Title: "Render", State: "open", Priority: 3, SubtaskOwnerUUID: &owner.UUID})
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p map[string]any
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Error(err)
		}
		received <- p
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	urls, _ := json.Marshal([]string{server.URL})
	if _, err := s.Containers.UpdateFields(actor, container.UUID, map[string]interface{}{"webhook_urls": string(urls)}, 0); err != nil {
		t.Fatal(err)
	}
	webhooks.DispatchTaskEvent(d, sub.UUID, webhooks.EventContext{Event: "updated", Metadata: events.EventMetadata{ID: 42, Timestamp: "2026-09-30T00:00:00Z"}, PrincipalRef: "agent:test", Via: "cli"})
	select {
	case payload := <-received:
		if payload["ticket_id"] != sub.ID || payload["ticket_uuid"] != sub.UUID || payload["subtask_owner_id"] != owner.ID || payload["subtask_owner_uuid"] != owner.UUID {
			t.Fatalf("subtask/owner identity lost: %+v", payload)
		}
	default:
		t.Fatal("owner container subscription did not receive subtask event")
	}
}
