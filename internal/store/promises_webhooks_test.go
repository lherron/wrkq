package store

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/webhooks"
)

func TestPromiseWebhookDeliveryFollowsSubjectContainerScope(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := New(database)

	container, err := s.Containers.Create(actorUUID, ContainerCreateParams{Slug: "promises", Kind: "project"})
	if err != nil {
		t.Fatalf("create promise container: %v", err)
	}
	task, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug: "subject", Title: "Promise subject", Description: "", ProjectUUID: container.UUID,
		State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create promise task: %v", err)
	}
	attached := createTestPromise(t, s, PromiseCreateParams{
		Subject: "Review attached subject", SubjectTaskUUID: &task.UUID,
		ReviewAt: "2000-01-01T00:00:00Z",
	})

	promiseCalls := make(chan webhooks.PromisePayload, 2)
	promiseServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() { _ = r.Body.Close() }()
		body, _ := io.ReadAll(r.Body)
		var payload webhooks.PromisePayload
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Errorf("decode promise webhook: %v", err)
		}
		promiseCalls <- payload
		w.WriteHeader(http.StatusNoContent)
	}))
	defer promiseServer.Close()

	taskCalls := make(chan struct{}, 2)
	taskServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		taskCalls <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer taskServer.Close()

	subscriptions, err := json.Marshal([]map[string]interface{}{
		{"url": promiseServer.URL + "/{promise_id}", "events": []string{"promise"}},
		{"url": taskServer.URL, "events": []string{"task"}},
	})
	if err != nil {
		t.Fatalf("encode subscriptions: %v", err)
	}
	if _, err := s.Containers.UpdateFields(actorUUID, container.UUID, map[string]interface{}{"webhook_urls": string(subscriptions)}, 0); err != nil {
		t.Fatalf("set subscriptions: %v", err)
	}

	note := "Still needs attention"
	renewed, err := s.Promises.RenewWithAttribution(promiseTestAttribution, attached.UUID, PromiseReviewParams{
		ReviewAt: "2099-01-01T00:00:00Z", Note: &note,
	}, attached.ETag)
	if err != nil {
		t.Fatalf("renew attached promise: %v", err)
	}

	select {
	case payload := <-promiseCalls:
		if payload.Event != "promise.renewed" || payload.Promise.ID != attached.ID || payload.Promise.ETag != renewed.ETag {
			t.Fatalf("unexpected promise projection: %+v", payload)
		}
		if payload.SubjectRef == nil || payload.SubjectRef.Type != "task" || payload.SubjectRef.ID != task.ID || payload.SubjectRef.Path != "promises/subject" {
			t.Fatalf("unexpected subject_ref: %+v", payload.SubjectRef)
		}
		if payload.Actor != promiseTestAttribution.PrincipalRef || payload.OccurredAt == "" {
			t.Fatalf("unexpected actor/time: %q/%q", payload.Actor, payload.OccurredAt)
		}
		if payload.Changes["previous_review_at"] != "2000-01-01T00:00:00Z" || payload.Changes["next_review_at"] != "2099-01-01T00:00:00Z" || payload.Changes["note"] != note {
			t.Fatalf("unexpected renewal changes: %#v", payload.Changes)
		}
	default:
		t.Fatal("promise-filtered subscription received no attached renewal")
	}
	select {
	case <-taskCalls:
		t.Fatal("task-filtered subscription received a promise event")
	default:
	}

	standalone := createTestPromise(t, s, PromiseCreateParams{
		Subject: "Standalone promise", ReviewAt: "2000-01-01T00:00:00Z",
	})
	if _, err := s.Promises.RenewWithAttribution(promiseTestAttribution, standalone.UUID, PromiseReviewParams{
		ReviewAt: "2099-02-01T00:00:00Z",
	}, standalone.ETag); err != nil {
		t.Fatalf("renew standalone promise: %v", err)
	}
	select {
	case payload := <-promiseCalls:
		t.Fatalf("standalone renewal unexpectedly delivered: %+v", payload)
	case <-time.After(100 * time.Millisecond):
	}
}
