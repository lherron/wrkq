package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/webhooks"
)

// PromiseStore persists principal-owned attention promises. API-layer
// ownership checks remain outside this package; every write here is fenced by
// etag and records the supplied canonical attribution in the same transaction.
type PromiseStore struct {
	store *Store
}

// PromiseCreateParams contains the durable fields accepted at creation.
type PromiseCreateParams struct {
	UUID                 string
	OwnerPrincipalRef    string
	Subject              string
	ReviewQuestion       *string
	SubjectTaskUUID      *string
	SubjectContainerUUID *string
	ReviewAt             string
	Meta                 *string
	OnBehalfAssertedBy   *string
}

// PromiseListParams selects promises without imposing owner read restrictions.
// Empty fields are omitted; State accepts the durable lifecycle vocabulary.
type PromiseListParams struct {
	OwnerPrincipalRef    string
	State                domain.PromiseState
	SubjectTaskUUID      string
	SubjectContainerUUID string
}

// PromiseReviewParams is shared by renew, resolve, and abandon. ReviewAt is
// required only by Renew; Note nil records a completed review without a note.
type PromiseReviewParams struct {
	ReviewAt string
	Note     *string
}

// PromiseNotFoundError identifies a missing promise without coupling callers
// to store error strings.
type PromiseNotFoundError struct{ Selector string }

func (e *PromiseNotFoundError) Error() string {
	return fmt.Sprintf("promise not found: %s", e.Selector)
}

// PromiseWrongStateError identifies lifecycle verbs attempted on a closed
// promise.
type PromiseWrongStateError struct {
	State domain.PromiseState
	Verb  string
}

func (e *PromiseWrongStateError) Error() string {
	return fmt.Sprintf("cannot %s promise in state %s", e.Verb, e.State)
}

const promiseColumns = `
	uuid, id, owner_principal_ref, subject, review_question,
	subject_task_uuid, subject_container_uuid, review_at, state, closed_at,
	last_reviewed_at, last_review_note, meta, etag, created_at, updated_at,
	created_by_principal_ref, created_by_scope_ref,
	updated_by_principal_ref, updated_by_scope_ref`

// CreateWithAttribution creates an open promise and emits promise.created.
func (ps *PromiseStore) CreateWithAttribution(attr attribution.Attribution, params PromiseCreateParams) (*domain.Promise, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	reviewAt, err := domain.ValidatePromiseFields(
		params.OwnerPrincipalRef, params.Subject, params.ReviewAt,
		domain.PromiseStateOpen, params.SubjectTaskUUID, params.SubjectContainerUUID,
	)
	if err != nil {
		return nil, err
	}

	var created *domain.Promise
	var webhookMetadata events.EventMetadata
	var webhookChanges map[string]interface{}
	err = ps.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		columns := "id, owner_principal_ref, subject, review_question, subject_task_uuid, subject_container_uuid, review_at, meta, created_by_principal_ref, created_by_scope_ref, updated_by_principal_ref, updated_by_scope_ref"
		placeholders := "?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?"
		args := []interface{}{"", params.OwnerPrincipalRef, strings.TrimSpace(params.Subject), params.ReviewQuestion, params.SubjectTaskUUID, params.SubjectContainerUUID, reviewAt, params.Meta, attr.PrincipalRef, scopeSQL(attr), attr.PrincipalRef, scopeSQL(attr)}
		if params.UUID != "" {
			columns = "uuid, " + columns
			placeholders = "?, " + placeholders
			args = append([]interface{}{params.UUID}, args...)
		}
		res, err := tx.Exec("INSERT INTO promises ("+columns+") VALUES ("+placeholders+")", args...)
		if err != nil {
			return fmt.Errorf("failed to create promise: %w", err)
		}
		rowID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("failed to read promise row id: %w", err)
		}
		created, err = scanPromise(tx.QueryRow("SELECT "+promiseColumns+" FROM promises WHERE rowid = ?", rowID))
		if err != nil {
			return fmt.Errorf("failed to read created promise: %w", err)
		}

		payload := map[string]interface{}{
			"owner_principal_ref": created.OwnerPrincipalRef,
			"subject":             created.Subject,
			"review_at":           created.ReviewAt,
			"state":               created.State,
		}
		if created.ReviewQuestion != nil {
			payload["review_question"] = *created.ReviewQuestion
		}
		if created.SubjectTaskUUID != nil {
			payload["subject_task_uuid"] = *created.SubjectTaskUUID
		}
		if created.SubjectContainerUUID != nil {
			payload["subject_container_uuid"] = *created.SubjectContainerUUID
		}
		if created.Meta != nil {
			payload["meta"] = *created.Meta
		}
		if params.OnBehalfAssertedBy != nil {
			payload["on_behalf_asserted_by"] = *params.OnBehalfAssertedBy
		}
		webhookChanges = payload
		webhookMetadata, err = logPromiseEvent(tx, ew, attr, created.UUID, "promise.created", created.ETag, payload)
		return err
	})
	if err == nil && created != nil {
		webhooks.DispatchPromiseEvent(ps.store.db, *created, "promise.created", webhookChanges, webhookMetadata, attr.PrincipalRef)
	}
	return created, err
}

// Get resolves a promise by UUID or PR-friendly ID.
func (ps *PromiseStore) Get(selector string) (*domain.Promise, error) {
	promise, err := scanPromise(ps.store.db.QueryRow(
		"SELECT "+promiseColumns+" FROM promises WHERE uuid = ? OR id = ?", selector, selector,
	))
	if err == sql.ErrNoRows {
		return nil, &PromiseNotFoundError{Selector: selector}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get promise: %w", err)
	}
	return promise, nil
}

// GetByUUID retrieves a promise by UUID.
func (ps *PromiseStore) GetByUUID(uuid string) (*domain.Promise, error) { return ps.Get(uuid) }

// List returns promises ordered by review time and friendly ID.
func (ps *PromiseStore) List(params PromiseListParams) ([]domain.Promise, error) {
	clauses := []string{"1 = 1"}
	args := []interface{}{}
	if params.OwnerPrincipalRef != "" {
		clauses = append(clauses, "owner_principal_ref = ?")
		args = append(args, params.OwnerPrincipalRef)
	}
	if params.State != "" {
		if err := domain.ValidatePromiseState(params.State); err != nil {
			return nil, err
		}
		clauses = append(clauses, "state = ?")
		args = append(args, params.State)
	}
	if params.SubjectTaskUUID != "" {
		clauses = append(clauses, "subject_task_uuid = ?")
		args = append(args, params.SubjectTaskUUID)
	}
	if params.SubjectContainerUUID != "" {
		clauses = append(clauses, "subject_container_uuid = ?")
		args = append(args, params.SubjectContainerUUID)
	}
	return ps.query("SELECT "+promiseColumns+" FROM promises WHERE "+strings.Join(clauses, " AND ")+" ORDER BY review_at, id", args...)
}

// Ready returns open promises due for the owner at the database server's UTC
// clock. The comparison instant is deliberately not accepted from callers.
func (ps *PromiseStore) Ready(ownerPrincipalRef string) ([]domain.Promise, error) {
	if strings.TrimSpace(ownerPrincipalRef) == "" {
		return nil, fmt.Errorf("owner_principal_ref is required")
	}
	return ps.query(`SELECT `+promiseColumns+` FROM promises
		WHERE owner_principal_ref = ? AND state = 'open'
		  AND review_at <= strftime('%Y-%m-%dT%H:%M:%SZ','now')
		ORDER BY review_at, id`, ownerPrincipalRef)
}

// ReadyScoped returns ready promises for one owner within a session's project
// attention scope. Attached promises derive their project through the subject's
// container ancestry. Standalone promises are included only when requested.
func (ps *PromiseStore) ReadyScoped(ownerPrincipalRef, projectUUID string, includeGlobal bool) ([]domain.Promise, error) {
	if strings.TrimSpace(ownerPrincipalRef) == "" {
		return nil, fmt.Errorf("owner_principal_ref is required")
	}
	includeGlobalValue := 0
	if includeGlobal {
		includeGlobalValue = 1
	}
	return ps.query(`WITH RECURSIVE container_projects(container_uuid, project_uuid) AS (
		SELECT c.uuid, c.uuid
		  FROM containers c
		 WHERE c.kind = 'project'
		   AND c.parent_uuid = (SELECT uuid FROM containers WHERE kind = 'root')
		UNION ALL
		SELECT child.uuid, parent.project_uuid
		  FROM containers child
		  JOIN container_projects parent ON child.parent_uuid = parent.container_uuid
	)
	SELECT `+promiseColumns+` FROM promises
	 WHERE owner_principal_ref = ? AND state = 'open'
	   AND review_at <= strftime('%Y-%m-%dT%H:%M:%SZ','now')
	   AND (
		(? = 1 AND subject_task_uuid IS NULL AND subject_container_uuid IS NULL)
		OR subject_task_uuid IN (
			SELECT t.uuid
			  FROM tasks t
			  JOIN container_projects cp ON cp.container_uuid = t.project_uuid
			 WHERE cp.project_uuid = ?
		)
		OR subject_container_uuid IN (
			SELECT cp.container_uuid
			  FROM container_projects cp
			 WHERE cp.project_uuid = ?
		)
	   )
	 ORDER BY review_at, id`, ownerPrincipalRef, includeGlobalValue, projectUUID, projectUUID)
}

// readyAt is the deterministic seam used only by store tests. Production
// callers use Ready so a client can never supply the authority clock.
func (ps *PromiseStore) readyAt(ownerPrincipalRef, canonicalNow string) ([]domain.Promise, error) {
	now, err := domain.NormalizePromiseReviewAt(canonicalNow)
	if err != nil {
		return nil, err
	}
	return ps.query(`SELECT `+promiseColumns+` FROM promises
		WHERE owner_principal_ref = ? AND state = 'open' AND review_at <= ?
		ORDER BY review_at, id`, ownerPrincipalRef, now)
}

// UpdateFieldsWithAttribution edits mutable content fields and emits
// promise.updated. nil values clear nullable fields. Lifecycle and target
// changes must use their explicit verbs.
func (ps *PromiseStore) UpdateFieldsWithAttribution(attr attribution.Attribution, promiseUUID string, fields map[string]interface{}, ifMatch int64) (int64, error) {
	if err := requireAttribution(attr); err != nil {
		return 0, err
	}
	if len(fields) == 0 {
		return 0, fmt.Errorf("at least one promise field is required")
	}
	allowed := map[string]bool{"subject": true, "review_question": true, "review_at": true, "meta": true}
	normalized := make(map[string]interface{}, len(fields))
	for key, value := range fields {
		if !allowed[key] {
			return 0, fmt.Errorf("promise field %q is not editable", key)
		}
		if key == "subject" {
			subject, ok := value.(string)
			if !ok || strings.TrimSpace(subject) == "" {
				return 0, fmt.Errorf("promise subject is required")
			}
			value = strings.TrimSpace(subject)
		}
		if key == "review_at" {
			raw, ok := value.(string)
			if !ok {
				return 0, fmt.Errorf("review_at must be an RFC3339 string")
			}
			canonical, err := domain.NormalizePromiseReviewAt(raw)
			if err != nil {
				return 0, err
			}
			value = canonical
		}
		if key == "review_question" || key == "meta" {
			if value != nil {
				if _, ok := value.(string); !ok {
					return 0, fmt.Errorf("%s must be a string or null", key)
				}
			}
		}
		normalized[key] = value
	}

	var newETag int64
	var webhookMetadata events.EventMetadata
	err := ps.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		current, err := getPromiseTx(tx, promiseUUID)
		if err != nil {
			return err
		}
		if current.State != domain.PromiseStateOpen {
			return &PromiseWrongStateError{State: current.State, Verb: "edit"}
		}
		if err := checkETag(current.ETag, ifMatch); err != nil {
			return err
		}
		keys := make([]string, 0, len(normalized))
		for key := range normalized {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		sets := make([]string, 0, len(keys)+4)
		args := make([]interface{}, 0, len(keys)+4)
		for _, key := range keys {
			sets = append(sets, key+" = ?")
			args = append(args, normalized[key])
		}
		sets = append(sets, "etag = etag + 1", "updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')", "updated_by_principal_ref = ?", "updated_by_scope_ref = ?")
		args = append(args, attr.PrincipalRef, scopeSQL(attr), promiseUUID)
		if _, err := tx.Exec("UPDATE promises SET "+strings.Join(sets, ", ")+" WHERE uuid = ?", args...); err != nil {
			return fmt.Errorf("failed to update promise: %w", err)
		}
		newETag = current.ETag + 1
		webhookMetadata, err = logPromiseEvent(tx, ew, attr, promiseUUID, "promise.updated", newETag, normalized)
		return err
	})
	if err == nil {
		ps.dispatchPromiseWebhook(promiseUUID, "promise.updated", normalized, webhookMetadata, attr.PrincipalRef)
	}
	return newETag, err
}

// Renew records a completed review and chooses the next review instant.
func (ps *PromiseStore) RenewWithAttribution(attr attribution.Attribution, promiseUUID string, params PromiseReviewParams, ifMatch int64) (*domain.Promise, error) {
	next, err := domain.NormalizePromiseReviewAt(params.ReviewAt)
	if err != nil {
		return nil, err
	}
	return ps.reviewWithAttribution(attr, promiseUUID, "renew", next, params.Note, ifMatch)
}

// Resolve records a completed review and closes the promise as satisfied.
func (ps *PromiseStore) ResolveWithAttribution(attr attribution.Attribution, promiseUUID string, note *string, ifMatch int64) (*domain.Promise, error) {
	return ps.reviewWithAttribution(attr, promiseUUID, "resolve", "", note, ifMatch)
}

// Abandon records a completed review and deliberately stops carrying it.
func (ps *PromiseStore) AbandonWithAttribution(attr attribution.Attribution, promiseUUID string, note *string, ifMatch int64) (*domain.Promise, error) {
	return ps.reviewWithAttribution(attr, promiseUUID, "abandon", "", note, ifMatch)
}

func (ps *PromiseStore) reviewWithAttribution(attr attribution.Attribution, promiseUUID, verb, nextReviewAt string, note *string, ifMatch int64) (*domain.Promise, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	var reviewed *domain.Promise
	var webhookMetadata events.EventMetadata
	var webhookChanges map[string]interface{}
	var webhookEvent string
	err := ps.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		current, err := getPromiseTx(tx, promiseUUID)
		if err != nil {
			return err
		}
		if current.State != domain.PromiseStateOpen {
			return &PromiseWrongStateError{State: current.State, Verb: verb}
		}
		if err := checkETag(current.ETag, ifMatch); err != nil {
			return err
		}
		var reviewedAt string
		if err := tx.QueryRow("SELECT strftime('%Y-%m-%dT%H:%M:%SZ','now')").Scan(&reviewedAt); err != nil {
			return fmt.Errorf("failed to read server review time: %w", err)
		}
		state := domain.PromiseStateOpen
		var closedAt interface{}
		eventType := "promise.renewed"
		switch verb {
		case "renew":
		case "resolve":
			state, closedAt, eventType = domain.PromiseStateResolved, reviewedAt, "promise.resolved"
		case "abandon":
			state, closedAt, eventType = domain.PromiseStateAbandoned, reviewedAt, "promise.abandoned"
		default:
			return fmt.Errorf("unknown promise review verb %q", verb)
		}
		reviewAt := current.ReviewAt
		if verb == "renew" {
			reviewAt = nextReviewAt
		}
		if _, err := tx.Exec(`UPDATE promises
			SET review_at = ?, state = ?, closed_at = ?, last_reviewed_at = ?,
			    last_review_note = ?, etag = etag + 1,
			    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
			    updated_by_principal_ref = ?, updated_by_scope_ref = ?
			WHERE uuid = ?`, reviewAt, state, closedAt, reviewedAt, note, attr.PrincipalRef, scopeSQL(attr), promiseUUID); err != nil {
			return fmt.Errorf("failed to %s promise: %w", verb, err)
		}
		payload := map[string]interface{}{
			"state":              state,
			"last_reviewed_at":   reviewedAt,
			"last_review_note":   nullableStringValue(note),
			"previous_review_at": current.ReviewAt,
			"note":               nullableStringValue(note),
		}
		if verb == "renew" {
			payload["review_at"] = nextReviewAt
			payload["next_review_at"] = nextReviewAt
		} else {
			payload["closed_at"] = reviewedAt
		}
		newETag := current.ETag + 1
		webhookMetadata, err = logPromiseEvent(tx, ew, attr, promiseUUID, eventType, newETag, payload)
		if err != nil {
			return err
		}
		webhookChanges = payload
		webhookEvent = eventType
		reviewed, err = getPromiseTx(tx, promiseUUID)
		return err
	})
	if err == nil && reviewed != nil {
		webhooks.DispatchPromiseEvent(ps.store.db, *reviewed, webhookEvent, webhookChanges, webhookMetadata, attr.PrincipalRef)
	}
	return reviewed, err
}

// AttachTaskWithAttribution retargets a promise to one task.
func (ps *PromiseStore) AttachTaskWithAttribution(attr attribution.Attribution, promiseUUID, taskUUID string, ifMatch int64) (*domain.Promise, error) {
	return ps.retargetWithAttribution(attr, promiseUUID, &taskUUID, nil, ifMatch)
}

// AttachContainerWithAttribution retargets a promise to one container/campaign.
func (ps *PromiseStore) AttachContainerWithAttribution(attr attribution.Attribution, promiseUUID, containerUUID string, ifMatch int64) (*domain.Promise, error) {
	return ps.retargetWithAttribution(attr, promiseUUID, nil, &containerUUID, ifMatch)
}

// DetachWithAttribution makes a promise standalone without changing its text
// snapshot or review history.
func (ps *PromiseStore) DetachWithAttribution(attr attribution.Attribution, promiseUUID string, ifMatch int64) (*domain.Promise, error) {
	return ps.retargetWithAttribution(attr, promiseUUID, nil, nil, ifMatch)
}

func (ps *PromiseStore) retargetWithAttribution(attr attribution.Attribution, promiseUUID string, taskUUID, containerUUID *string, ifMatch int64) (*domain.Promise, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	if taskUUID != nil && containerUUID != nil {
		return nil, fmt.Errorf("promise may reference at most one task or container")
	}
	var updated *domain.Promise
	var webhookMetadata events.EventMetadata
	var webhookChanges map[string]interface{}
	err := ps.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		current, err := getPromiseTx(tx, promiseUUID)
		if err != nil {
			return err
		}
		if current.State != domain.PromiseStateOpen {
			return &PromiseWrongStateError{State: current.State, Verb: "retarget"}
		}
		if err := checkETag(current.ETag, ifMatch); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE promises
			SET subject_task_uuid = ?, subject_container_uuid = ?, etag = etag + 1,
			    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
			    updated_by_principal_ref = ?, updated_by_scope_ref = ?
			WHERE uuid = ?`, taskUUID, containerUUID, attr.PrincipalRef, scopeSQL(attr), promiseUUID); err != nil {
			return fmt.Errorf("failed to retarget promise: %w", err)
		}
		payload := map[string]interface{}{
			"subject_task_uuid":      nullableStringValue(taskUUID),
			"subject_container_uuid": nullableStringValue(containerUUID),
			"previous_subject_ref":   promiseSubjectRef(current),
		}
		newETag := current.ETag + 1
		webhookMetadata, err = logPromiseEvent(tx, ew, attr, promiseUUID, "promise.retargeted", newETag, payload)
		if err != nil {
			return err
		}
		webhookChanges = payload
		updated, err = getPromiseTx(tx, promiseUUID)
		return err
	})
	if err == nil && updated != nil {
		webhooks.DispatchPromiseEvent(ps.store.db, *updated, "promise.retargeted", webhookChanges, webhookMetadata, attr.PrincipalRef)
	}
	return updated, err
}

// PurgeWithAttribution hard-deletes a promise after recording the terminal
// audit event. Owner authority is enforced by the API before this store call.
func (ps *PromiseStore) PurgeWithAttribution(attr attribution.Attribution, promiseUUID string, ifMatch int64) error {
	if err := requireAttribution(attr); err != nil {
		return err
	}
	var purged *domain.Promise
	var webhookMetadata events.EventMetadata
	changes := map[string]interface{}{}
	err := ps.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		current, err := getPromiseTx(tx, promiseUUID)
		if err != nil {
			return err
		}
		if err := checkETag(current.ETag, ifMatch); err != nil {
			return err
		}
		purged = current
		changes = map[string]interface{}{"id": current.ID}
		webhookMetadata, err = logPromiseEvent(tx, ew, attr, promiseUUID, "promise.purged", current.ETag, changes)
		if err != nil {
			return err
		}
		_, err = tx.Exec("DELETE FROM promises WHERE uuid = ?", promiseUUID)
		return err
	})
	if err == nil && purged != nil {
		webhooks.DispatchPromiseEvent(ps.store.db, *purged, "promise.purged", changes, webhookMetadata, attr.PrincipalRef)
	}
	return err
}

func (ps *PromiseStore) query(query string, args ...interface{}) ([]domain.Promise, error) {
	rows, err := ps.store.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query promises: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := []domain.Promise{}
	for rows.Next() {
		promise, err := scanPromise(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan promise: %w", err)
		}
		result = append(result, *promise)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate promises: %w", err)
	}
	return result, nil
}

type promiseScanner interface{ Scan(...interface{}) error }

func scanPromise(scanner promiseScanner) (*domain.Promise, error) {
	promise := &domain.Promise{}
	err := scanner.Scan(
		&promise.UUID, &promise.ID, &promise.OwnerPrincipalRef, &promise.Subject, &promise.ReviewQuestion,
		&promise.SubjectTaskUUID, &promise.SubjectContainerUUID, &promise.ReviewAt, &promise.State, &promise.ClosedAt,
		&promise.LastReviewedAt, &promise.LastReviewNote, &promise.Meta, &promise.ETag, &promise.CreatedAt, &promise.UpdatedAt,
		&promise.CreatedByPrincipalRef, &promise.CreatedByScopeRef,
		&promise.UpdatedByPrincipalRef, &promise.UpdatedByScopeRef,
	)
	return promise, err
}

func getPromiseTx(tx *sql.Tx, uuid string) (*domain.Promise, error) {
	promise, err := scanPromise(tx.QueryRow("SELECT "+promiseColumns+" FROM promises WHERE uuid = ?", uuid))
	if err == sql.ErrNoRows {
		return nil, &PromiseNotFoundError{Selector: uuid}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get promise: %w", err)
	}
	return promise, nil
}

func logPromiseEvent(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, promiseUUID, eventType string, etag int64, payload map[string]interface{}) (events.EventMetadata, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return events.EventMetadata{}, fmt.Errorf("failed to marshal %s payload: %w", eventType, err)
	}
	text := string(encoded)
	metadata, err := ew.LogEventReturning(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "promise",
		ResourceUUID: &promiseUUID,
		EventType:    eventType,
		ETag:         &etag,
		Payload:      &text,
	})
	if err != nil {
		return events.EventMetadata{}, fmt.Errorf("failed to log %s event: %w", eventType, err)
	}
	return metadata, nil
}

func (ps *PromiseStore) dispatchPromiseWebhook(promiseUUID, event string, changes map[string]interface{}, metadata events.EventMetadata, principalRef string) {
	promise, err := ps.GetByUUID(promiseUUID)
	if err != nil {
		log.Printf("webhooks: read committed promise %s failed: %v", promiseUUID, err)
		return
	}
	webhooks.DispatchPromiseEvent(ps.store.db, *promise, event, changes, metadata, principalRef)
}

func nullableStringValue(value *string) interface{} {
	if value == nil {
		return nil
	}
	return *value
}

func promiseSubjectRef(promise *domain.Promise) interface{} {
	if promise.SubjectTaskUUID != nil {
		return map[string]interface{}{"type": "task", "uuid": *promise.SubjectTaskUUID}
	}
	if promise.SubjectContainerUUID != nil {
		return map[string]interface{}{"type": "container", "uuid": *promise.SubjectContainerUUID}
	}
	return nil
}

// retargetPromisesForPurgedTask explicitly detaches promise references before
// the task is deleted so audit history records the exact lost target. Relying
// on ON DELETE SET NULL would silently violate the promise event contract.
func retargetPromisesForPurgedTask(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, taskUUID, id, slug string) error {
	return retargetPromisesForPurge(tx, ew, attr, "task", taskUUID, id, slug)
}

// retargetPromisesForPurgedContainer is the container counterpart of the task
// purge hook above.
func retargetPromisesForPurgedContainer(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, containerUUID, id, slug string) error {
	return retargetPromisesForPurge(tx, ew, attr, "container", containerUUID, id, slug)
}

func retargetPromisesForPurge(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, kind, resourceUUID, id, slug string) error {
	column := "subject_task_uuid"
	if kind == "container" {
		column = "subject_container_uuid"
	}
	rows, err := tx.Query("SELECT uuid, etag FROM promises WHERE "+column+" = ? ORDER BY id", resourceUUID)
	if err != nil {
		return fmt.Errorf("failed to find promises attached to purged %s: %w", kind, err)
	}
	type attached struct {
		uuid string
		etag int64
	}
	var promises []attached
	for rows.Next() {
		var item attached
		if err := rows.Scan(&item.uuid, &item.etag); err != nil {
			_ = rows.Close()
			return fmt.Errorf("failed to scan attached promise: %w", err)
		}
		promises = append(promises, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, item := range promises {
		if _, err := tx.Exec("UPDATE promises SET "+column+" = NULL, etag = etag + 1, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'), updated_by_principal_ref = ?, updated_by_scope_ref = ? WHERE uuid = ?", attr.PrincipalRef, scopeSQL(attr), item.uuid); err != nil {
			return fmt.Errorf("failed to detach promise from purged %s: %w", kind, err)
		}
		payload := map[string]interface{}{
			column: nil,
			"lost_ref": map[string]interface{}{
				"type": kind,
				"uuid": resourceUUID,
				"id":   id,
				"slug": slug,
			},
		}
		if _, err := logPromiseEvent(tx, ew, attr, item.uuid, "promise.retargeted", item.etag+1, payload); err != nil {
			return err
		}
	}
	return nil
}
