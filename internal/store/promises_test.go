package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
)

var promiseTestAttribution = attribution.Attribution{
	PrincipalRef: "agent:cody",
	ScopeRef:     "agent:cody:project:wrkq:task:T-07486",
}

func createTestPromise(t *testing.T, s *Store, params PromiseCreateParams) *domain.Promise {
	t.Helper()
	if params.OwnerPrincipalRef == "" {
		params.OwnerPrincipalRef = promiseTestAttribution.PrincipalRef
	}
	if params.Subject == "" {
		params.Subject = "Review promise behavior"
	}
	if params.ReviewAt == "" {
		params.ReviewAt = "2026-08-23T23:30:00Z"
	}
	promise, err := s.Promises.CreateWithAttribution(promiseTestAttribution, params)
	if err != nil {
		t.Fatalf("create promise: %v", err)
	}
	return promise
}

func TestPromiseReadyDerivedWithoutSchedulerAtExactBoundary(t *testing.T) {
	database := setupTestDB(t)
	s := New(database)
	promise := createTestPromise(t, s, PromiseCreateParams{
		Subject:  "Offset normalization boundary",
		ReviewAt: "2026-08-24T00:30:00+01:00",
	})
	if promise.ReviewAt != "2026-08-23T23:30:00Z" {
		t.Fatalf("stored review_at = %q, want 2026-08-23T23:30:00Z", promise.ReviewAt)
	}

	before, err := s.Promises.readyAt(promise.OwnerPrincipalRef, "2026-08-23T23:29:59Z")
	if err != nil {
		t.Fatalf("ready before boundary: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("ready before boundary = %d, want 0", len(before))
	}
	atBoundary, err := s.Promises.readyAt(promise.OwnerPrincipalRef, "2026-08-23T23:30:00Z")
	if err != nil {
		t.Fatalf("ready at boundary: %v", err)
	}
	if len(atBoundary) != 1 || atBoundary[0].UUID != promise.UUID {
		t.Fatalf("ready at boundary = %#v, want promise %s", atBoundary, promise.UUID)
	}

	past := createTestPromise(t, s, PromiseCreateParams{
		Subject:  "Already ready",
		ReviewAt: "2000-01-01T00:00:00Z",
	})
	ready, err := s.Promises.Ready(past.OwnerPrincipalRef)
	if err != nil {
		t.Fatalf("server ready query: %v", err)
	}
	found := false
	for _, candidate := range ready {
		if candidate.UUID == past.UUID {
			found = true
		}
	}
	if !found {
		t.Fatalf("server ready query omitted past promise: %#v", ready)
	}
	after, err := s.Promises.GetByUUID(past.UUID)
	if err != nil {
		t.Fatalf("read after ready query: %v", err)
	}
	if after.State != domain.PromiseStateOpen || after.ETag != past.ETag {
		t.Fatalf("ready query mutated row state/etag = %s/%d, want open/%d", after.State, after.ETag, past.ETag)
	}
}

func TestPromiseRenewRecordsReviewAndLeavesReadyQueue(t *testing.T) {
	database := setupTestDB(t)
	s := New(database)
	promise := createTestPromise(t, s, PromiseCreateParams{
		Subject:  "Renew review",
		ReviewAt: "2000-01-01T00:00:00Z",
	})
	note := "Still matters"
	renewed, err := s.Promises.RenewWithAttribution(promiseTestAttribution, promise.UUID, PromiseReviewParams{
		ReviewAt: "2099-01-01T00:00:00Z",
		Note:     &note,
	}, promise.ETag)
	if err != nil {
		t.Fatalf("renew promise: %v", err)
	}
	if renewed.State != domain.PromiseStateOpen || renewed.ReviewAt != "2099-01-01T00:00:00Z" || renewed.ETag != promise.ETag+1 {
		t.Fatalf("renewed row state/review/etag = %s/%s/%d", renewed.State, renewed.ReviewAt, renewed.ETag)
	}
	if renewed.LastReviewedAt == nil || renewed.LastReviewNote == nil || *renewed.LastReviewNote != note {
		t.Fatalf("renewed last review fields = %v/%v", renewed.LastReviewedAt, renewed.LastReviewNote)
	}
	ready, err := s.Promises.Ready(promise.OwnerPrincipalRef)
	if err != nil {
		t.Fatalf("ready after renew: %v", err)
	}
	for _, candidate := range ready {
		if candidate.UUID == promise.UUID {
			t.Fatal("renewed promise remained ready")
		}
	}

	payload := promiseEventPayload(t, database, promise.UUID, "promise.renewed")
	if payload["previous_review_at"] != "2000-01-01T00:00:00Z" ||
		payload["next_review_at"] != "2099-01-01T00:00:00Z" || payload["note"] != note {
		t.Fatalf("renewed payload = %#v", payload)
	}

	_, err = s.Promises.RenewWithAttribution(promiseTestAttribution, promise.UUID, PromiseReviewParams{ReviewAt: "2099-02-01T00:00:00Z"}, promise.ETag)
	var mismatch *domain.ETagMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("stale renewal error = %v, want ETagMismatchError", err)
	}
}

func TestPromiseCRUDRetargetAndLifecycleEvents(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	projectUUID := setupTestContainer(t, database, actorUUID)
	s := New(database)
	task, err := s.Tasks.CreateWithAttribution(promiseTestAttribution, CreateParams{
		Slug: "promise-crud-target", Title: "Promise CRUD Target", ProjectUUID: projectUUID,
		State: domain.StateOpen, Priority: 3,
	})
	if err != nil {
		t.Fatalf("create task target: %v", err)
	}
	onBehalf := "agent:cody"
	promise := createTestPromise(t, s, PromiseCreateParams{
		OwnerPrincipalRef:  "agent:lance",
		Subject:            "CRUD promise",
		ReviewAt:           "2099-01-01T00:00:00Z",
		OnBehalfAssertedBy: &onBehalf,
	})
	createdPayload := promiseEventPayload(t, database, promise.UUID, "promise.created")
	if createdPayload["on_behalf_asserted_by"] != onBehalf {
		t.Fatalf("created payload = %#v", createdPayload)
	}

	question := "What changed?"
	newETag, err := s.Promises.UpdateFieldsWithAttribution(promiseTestAttribution, promise.UUID, map[string]interface{}{
		"subject":         "Updated CRUD promise",
		"review_question": question,
		"review_at":       "2099-01-01T01:00:00+01:00",
	}, promise.ETag)
	if err != nil {
		t.Fatalf("update promise: %v", err)
	}
	updated, err := s.Promises.Get(promise.ID)
	if err != nil {
		t.Fatalf("get by friendly id: %v", err)
	}
	if updated.Subject != "Updated CRUD promise" || updated.ReviewQuestion == nil || *updated.ReviewQuestion != question || updated.ReviewAt != "2099-01-01T00:00:00Z" {
		t.Fatalf("updated promise = %#v", updated)
	}

	attached, err := s.Promises.AttachTaskWithAttribution(promiseTestAttribution, promise.UUID, task.UUID, newETag)
	if err != nil {
		t.Fatalf("attach task: %v", err)
	}
	if attached.SubjectTaskUUID == nil || *attached.SubjectTaskUUID != task.UUID || attached.ID != promise.ID {
		t.Fatalf("attached promise = %#v", attached)
	}
	listed, err := s.Promises.List(PromiseListParams{SubjectTaskUUID: task.UUID, State: domain.PromiseStateOpen})
	if err != nil || len(listed) != 1 || listed[0].UUID != promise.UUID {
		t.Fatalf("list attached promises = %#v, err=%v", listed, err)
	}
	detached, err := s.Promises.DetachWithAttribution(promiseTestAttribution, promise.UUID, attached.ETag)
	if err != nil {
		t.Fatalf("detach promise: %v", err)
	}
	if detached.SubjectTaskUUID != nil || detached.SubjectContainerUUID != nil {
		t.Fatalf("detached promise retained target: %#v", detached)
	}

	note := "Satisfied"
	resolved, err := s.Promises.ResolveWithAttribution(promiseTestAttribution, promise.UUID, &note, detached.ETag)
	if err != nil {
		t.Fatalf("resolve promise: %v", err)
	}
	if resolved.State != domain.PromiseStateResolved || resolved.ClosedAt == nil || resolved.LastReviewedAt == nil {
		t.Fatalf("resolved promise = %#v", resolved)
	}
	if _, err := s.Promises.AbandonWithAttribution(promiseTestAttribution, promise.UUID, nil, resolved.ETag); err == nil {
		t.Fatal("closed promise was abandoned again")
	}
	_ = promiseEventPayload(t, database, promise.UUID, "promise.updated")
	_ = promiseEventPayload(t, database, promise.UUID, "promise.retargeted")
	_ = promiseEventPayload(t, database, promise.UUID, "promise.resolved")

	abandoned := createTestPromise(t, s, PromiseCreateParams{Subject: "Abandon me", ReviewAt: "2000-01-01T00:00:00Z"})
	closed, err := s.Promises.AbandonWithAttribution(promiseTestAttribution, abandoned.UUID, nil, abandoned.ETag)
	if err != nil || closed.State != domain.PromiseStateAbandoned {
		t.Fatalf("abandon promise = %#v, err=%v", closed, err)
	}
	_ = promiseEventPayload(t, database, abandoned.UUID, "promise.abandoned")

	if err := s.Promises.PurgeWithAttribution(promiseTestAttribution, abandoned.UUID, closed.ETag); err != nil {
		t.Fatalf("purge promise: %v", err)
	}
	if _, err := s.Promises.GetByUUID(abandoned.UUID); err == nil {
		t.Fatal("purged promise still exists")
	}
}

func TestPromiseSubjectLifecyclePreservesAttentionAndAuditsLostRefs(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	projectUUID := setupTestContainer(t, database, actorUUID)
	s := New(database)
	task, err := s.Tasks.CreateWithAttribution(promiseTestAttribution, CreateParams{
		Slug: "promise-purge-target", Title: "Promise Purge Target", ProjectUUID: projectUUID,
		State: domain.StateOpen, Priority: 3,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	promise := createTestPromise(t, s, PromiseCreateParams{
		Subject:         "Keep task attention",
		ReviewAt:        "2099-01-01T00:00:00Z",
		SubjectTaskUUID: &task.UUID,
	})
	if _, err := s.Tasks.UpdateFieldsWithAttribution(promiseTestAttribution, task.UUID, map[string]interface{}{"state": domain.StateCompleted}, 0); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	stillOpen, err := s.Promises.GetByUUID(promise.UUID)
	if err != nil {
		t.Fatalf("read after completion: %v", err)
	}
	if stillOpen.State != domain.PromiseStateOpen || stillOpen.SubjectTaskUUID == nil {
		t.Fatalf("task completion changed promise attention: %#v", stillOpen)
	}
	if _, err := s.Tasks.PurgeWithAttribution(promiseTestAttribution, task.UUID, 0); err != nil {
		t.Fatalf("purge task: %v", err)
	}
	detached, err := s.Promises.GetByUUID(promise.UUID)
	if err != nil {
		t.Fatalf("read after task purge: %v", err)
	}
	if detached.SubjectTaskUUID != nil || detached.State != domain.PromiseStateOpen || detached.Subject != promise.Subject || detached.ETag != promise.ETag+1 {
		t.Fatalf("promise after task purge = %#v", detached)
	}
	payload := promiseEventPayload(t, database, promise.UUID, "promise.retargeted")
	lost, ok := payload["lost_ref"].(map[string]interface{})
	if !ok || lost["type"] != "task" || lost["uuid"] != task.UUID || lost["id"] != task.ID || lost["slug"] != "promise-purge-target" {
		t.Fatalf("task purge lost_ref = %#v", payload["lost_ref"])
	}

	container, err := s.Containers.CreateWithAttribution(promiseTestAttribution, ContainerCreateParams{
		Slug: "promise-container-purge", Kind: string(domain.ContainerKindProject),
	})
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	containerPromise := createTestPromise(t, s, PromiseCreateParams{
		Subject:              "Keep container attention",
		ReviewAt:             "2099-01-01T00:00:00Z",
		SubjectContainerUUID: &container.UUID,
	})
	if err := s.Containers.DeleteWithAttribution(promiseTestAttribution, container.UUID, container.ETag); err != nil {
		t.Fatalf("purge container: %v", err)
	}
	detachedContainer, err := s.Promises.GetByUUID(containerPromise.UUID)
	if err != nil {
		t.Fatalf("read after container purge: %v", err)
	}
	if detachedContainer.SubjectContainerUUID != nil || detachedContainer.State != domain.PromiseStateOpen {
		t.Fatalf("promise after container purge = %#v", detachedContainer)
	}
	containerPayload := promiseEventPayload(t, database, containerPromise.UUID, "promise.retargeted")
	containerLost, ok := containerPayload["lost_ref"].(map[string]interface{})
	if !ok || containerLost["type"] != "container" || containerLost["uuid"] != container.UUID || containerLost["id"] != container.ID || containerLost["slug"] != "promise-container-purge" {
		t.Fatalf("container purge lost_ref = %#v", containerPayload["lost_ref"])
	}
}

func promiseEventPayload(t *testing.T, database interface {
	QueryRow(string, ...interface{}) *sql.Row
}, promiseUUID, eventType string) map[string]interface{} {
	t.Helper()
	var raw string
	if err := database.QueryRow(`
		SELECT payload FROM event_log
		 WHERE resource_type = 'promise' AND resource_uuid = ? AND event_type = ?
		 ORDER BY id DESC LIMIT 1
	`, promiseUUID, eventType).Scan(&raw); err != nil {
		t.Fatalf("read %s event: %v", eventType, err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("decode %s payload: %v", eventType, err)
	}
	return payload
}
