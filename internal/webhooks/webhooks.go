package webhooks

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/events"
)

const (
	defaultTimeout     = 500 * time.Millisecond
	defaultConcurrency = 4

	EventWorkflowAttached     = "workflow_attached"
	EventWorkflowTransitioned = "workflow_transitioned"
)

// BlockerInfo represents an incomplete blocking task.
// This matches the format used in wrkq cat --json output.
type BlockerInfo struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

// Origin describes the source of the mutation that produced the webhook.
type Origin struct {
	Actor        string  `json:"actor"`
	RunID        *string `json:"run_id"`
	Via          string  `json:"via"`
	CausationRef *string `json:"causation_ref,omitempty"`
}

// Transition describes a task state transition. Nil endpoints encode JSON null.
type Transition struct {
	From *string `json:"from"`
	To   *string `json:"to"`
}

// Change describes a single changed field.
type Change struct {
	From interface{} `json:"from,omitempty"`
	To   interface{} `json:"to,omitempty"`
}

// Subject identifies the resource pair a webhook event is about.
type Subject struct {
	TicketID           string `json:"ticket_id"`
	TicketUUID         string `json:"ticket_uuid"`
	WorkflowInstanceID string `json:"workflow_instance_id,omitempty"`
}

// WorkflowPayload carries wrkf-specific event details inside the existing
// schema_version=2 webhook body.
type WorkflowPayload struct {
	SchemaVersion    string      `json:"schema_version,omitempty"`
	Type             string      `json:"type,omitempty"`
	EventID          string      `json:"event_id,omitempty"`
	EventSeq         int64       `json:"event_seq,omitempty"`
	InstanceID       string      `json:"instance_id"`
	Template         string      `json:"template,omitempty"`
	State            interface{} `json:"state,omitempty"`
	Transition       string      `json:"transition,omitempty"`
	Outcome          string      `json:"outcome,omitempty"`
	From             interface{} `json:"from,omitempty"`
	To               interface{} `json:"to,omitempty"`
	PrincipalRef     string      `json:"principal_ref,omitempty"`
	Role             string      `json:"role,omitempty"`
	RunID            *string     `json:"run_id,omitempty"`
	ObservedRevision *int64      `json:"observed_revision,omitempty"`
	NextRevision     *int64      `json:"next_revision,omitempty"`
	TaskDocETag      string      `json:"task_doc_etag,omitempty"`
	TaskDocHash      string      `json:"task_doc_hash,omitempty"`
	ContextHash      string      `json:"context_hash,omitempty"`
	IdempotencyKey   *string     `json:"idempotency_key,omitempty"`
	Payload          interface{} `json:"payload,omitempty"`
}

// EventContext carries event-specific data that cannot be reconstructed from
// the post-mutation task row.
type EventContext struct {
	Metadata     events.EventMetadata
	Event        string
	EventID      string
	EventSeq     int64
	OccurredAt   string
	ActorUUID    *string
	PrincipalRef string
	Via          string
	Transition   *Transition
	Changed      []string
	Changes      map[string]Change
	Subject      *Subject
	Workflow     *WorkflowPayload
}

// Payload is the webhook payload for task updates.
type Payload struct {
	SchemaVersion  int               `json:"schema_version"`
	EventID        string            `json:"event_id"`
	EventSeq       int64             `json:"event_seq"`
	Event          string            `json:"event"`
	OccurredAt     string            `json:"occurred_at"`
	Origin         Origin            `json:"origin"`
	TicketID       string            `json:"ticket_id"`
	TicketUUID     string            `json:"ticket_uuid"`
	ProjectID      string            `json:"project_id"`
	ProjectUUID    string            `json:"project_uuid"`
	ProjectScopeID string            `json:"project_scope_id"`
	Transition     *Transition       `json:"transition"`
	Changed        []string          `json:"changed"`
	Changes        map[string]Change `json:"changes"`
	Title          string            `json:"title"`
	Slug           string            `json:"slug"`
	ContainerPath  string            `json:"container_path"`
	Labels         []string          `json:"labels"`
	State          string            `json:"state"`
	Priority       int               `json:"priority"`
	Kind           string            `json:"kind"`
	Resolution     *string           `json:"resolution"`
	Meta           json.RawMessage   `json:"meta"`
	ETag           int64             `json:"etag"`
	BlockedBy      []BlockerInfo     `json:"blocked_by,omitempty"`
	Subject        *Subject          `json:"subject,omitempty"`
	Workflow       *WorkflowPayload  `json:"workflow,omitempty"`
}

// TaskInfo carries task metadata needed for webhook dispatch.
type TaskInfo struct {
	TaskID         string
	TaskUUID       string
	ProjectID      string
	ProjectUUID    string
	ProjectScopeID string
	Title          string
	Slug           string
	ContainerPath  string
	Labels         *string
	State          string
	Priority       int
	Kind           string
	Resolution     *string
	Meta           *string
	ETag           int64
	BlockedBy      []BlockerInfo
}

// DispatchTask resolves task info then dispatches webhooks.
func DispatchTask(database *db.DB, taskUUID string) {
	info, err := LookupTaskInfo(database, taskUUID)
	if err != nil {
		log.Printf("webhooks: lookup task %s failed: %v", taskUUID, err)
		return
	}
	DispatchTaskInfo(database, info)
}

// DispatchTaskInfo dispatches webhooks using pre-fetched task info.
func DispatchTaskInfo(database *db.DB, info TaskInfo) {
	DispatchTaskInfoEvent(database, info, EventContext{
		Metadata: events.EventMetadata{ID: 0, Timestamp: time.Now().UTC().Format(time.RFC3339)},
		Event:    "updated",
		Via:      "unknown",
	})
}

// DispatchTaskEvent resolves task info then dispatches an event-aware webhook.
func DispatchTaskEvent(database *db.DB, taskUUID string, ctx EventContext) {
	info, err := LookupTaskInfo(database, taskUUID)
	if err != nil {
		log.Printf("webhooks: lookup task %s failed: %v", taskUUID, err)
		return
	}
	DispatchTaskInfoEvent(database, info, ctx)
}

// DispatchTaskInfoEvent dispatches webhooks using pre-fetched task info and event context.
func DispatchTaskInfoEvent(database *db.DB, info TaskInfo, ctx EventContext) {
	meta := json.RawMessage(`{}`)
	if info.Meta != nil && *info.Meta != "" {
		if json.Valid([]byte(*info.Meta)) {
			meta = json.RawMessage(*info.Meta)
		}
	}
	labels := parseLabels(info.Labels)
	origin := resolveOrigin(ctx.PrincipalRef, ctx.Via, nil)
	eventID := ""
	if ctx.Metadata.ID > 0 {
		eventID = fmt.Sprintf("evt_%d", ctx.Metadata.ID)
	}
	if ctx.EventID != "" {
		eventID = ctx.EventID
	}
	eventSeq := ctx.Metadata.ID
	if ctx.EventSeq != 0 {
		eventSeq = ctx.EventSeq
	}
	occurredAt := ctx.Metadata.Timestamp
	if ctx.OccurredAt != "" {
		occurredAt = ctx.OccurredAt
	}
	if ctx.Workflow != nil && ctx.Workflow.RunID != nil {
		origin.RunID = ctx.Workflow.RunID
	}
	changed := ctx.Changed
	if changed == nil {
		changed = []string{}
	}
	changes := ctx.Changes
	if changes == nil {
		changes = map[string]Change{}
	}
	payload := Payload{
		SchemaVersion:  2,
		EventID:        eventID,
		EventSeq:       eventSeq,
		Event:          ctx.Event,
		OccurredAt:     occurredAt,
		Origin:         origin,
		TicketID:       info.TaskID,
		TicketUUID:     info.TaskUUID,
		ProjectID:      info.ProjectID,
		ProjectUUID:    info.ProjectUUID,
		ProjectScopeID: info.ProjectScopeID,
		Transition:     ctx.Transition,
		Changed:        changed,
		Changes:        changes,
		Title:          info.Title,
		Slug:           info.Slug,
		ContainerPath:  info.ContainerPath,
		Labels:         labels,
		State:          info.State,
		Priority:       info.Priority,
		Kind:           info.Kind,
		Resolution:     info.Resolution,
		Meta:           meta,
		ETag:           info.ETag,
		BlockedBy:      info.BlockedBy,
		Workflow:       ctx.Workflow,
	}
	if ctx.Subject != nil {
		subject := *ctx.Subject
		if subject.TicketID == "" {
			subject.TicketID = info.TaskID
		}
		if subject.TicketUUID == "" {
			subject.TicketUUID = info.TaskUUID
		}
		payload.Subject = &subject
	}
	urls, err := ResolveWebhookTargets(database, info.ProjectUUID, payload)
	if err != nil {
		log.Printf("webhooks: resolve targets for task %s failed: %v", info.TaskID, err)
		return
	}
	dispatchURLs(urls, payload)
}

func parseLabels(raw *string) []string {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return []string{}
	}
	var labels []string
	if err := json.Unmarshal([]byte(*raw), &labels); err != nil {
		return []string{}
	}
	return labels
}

// resolveOrigin derives the webhook origin actor from the canonical caller
// principal ref (agent:<id>). It never reads the legacy actors table; a missing
// principal yields the "system" sentinel.
func resolveOrigin(principalRef string, via string, runID *string) Origin {
	if via == "" {
		via = "unknown"
	}
	causationRef := readCausationRefFromEnv()
	principalRef = strings.TrimSpace(principalRef)
	if principalRef == "" {
		return Origin{Actor: "system", RunID: runID, Via: via, CausationRef: causationRef}
	}
	return Origin{Actor: principalRef, RunID: runID, Via: via, CausationRef: causationRef}
}

func readCausationRefFromEnv() *string {
	value := strings.TrimSpace(os.Getenv("WRKQ_CAUSATION_REF"))
	if value == "" {
		return nil
	}
	return &value
}

// nullStringToPtr converts sql.NullString to *string.
func nullStringToPtr(ns sql.NullString) *string {
	if ns.Valid {
		return &ns.String
	}
	return nil
}

// LookupTaskInfo fetches the task and project friendly IDs for dispatch.
func LookupTaskInfo(database *db.DB, taskUUID string) (TaskInfo, error) {
	return LookupTaskInfoWith(database, taskUUID)
}

type taskInfoQueryer interface {
	QueryRow(query string, args ...interface{}) *sql.Row
	Query(query string, args ...interface{}) (*sql.Rows, error)
}

// LookupTaskInfoWith fetches task info using either a DB or an open transaction.
func LookupTaskInfoWith(database taskInfoQueryer, taskUUID string) (TaskInfo, error) {
	var info TaskInfo
	var resolution, meta, labels sql.NullString

	err := database.QueryRow(`
		SELECT t.id, t.uuid, t.project_uuid, c.id, t.slug, t.title, cp.path,
		       t.state, t.priority, t.kind, t.resolution, t.meta, t.labels, t.etag
		FROM tasks t
		JOIN containers c ON c.uuid = t.project_uuid
		JOIN v_container_paths cp ON cp.uuid = t.project_uuid
		WHERE t.uuid = ?
	`, taskUUID).Scan(
		&info.TaskID, &info.TaskUUID, &info.ProjectUUID, &info.ProjectID, &info.Slug, &info.Title, &info.ContainerPath,
		&info.State, &info.Priority, &info.Kind,
		&resolution, &meta, &labels, &info.ETag,
	)
	if err != nil {
		return TaskInfo{}, fmt.Errorf("lookup task info: %w", err)
	}

	info.ProjectScopeID = strings.Split(strings.Trim(info.ContainerPath, "/"), "/")[0]
	info.Resolution = nullStringToPtr(resolution)
	info.Meta = nullStringToPtr(meta)
	info.Labels = nullStringToPtr(labels)

	// Query incomplete blockers for this task
	blockerRows, err := database.Query(`
		SELECT t.id, t.state
		FROM task_relations r
		JOIN tasks t ON r.from_task_uuid = t.uuid
		WHERE r.to_task_uuid = ?
		  AND r.kind = 'blocks'
		  AND t.state NOT IN ('completed', 'archived', 'deleted', 'cancelled', 'idea')
		ORDER BY t.id
	`, taskUUID)
	if err != nil {
		return TaskInfo{}, fmt.Errorf("query blockers: %w", err)
	}
	defer func() { _ = blockerRows.Close() }()

	for blockerRows.Next() {
		var blocker BlockerInfo
		if err := blockerRows.Scan(&blocker.ID, &blocker.State); err != nil {
			return TaskInfo{}, fmt.Errorf("scan blocker: %w", err)
		}
		info.BlockedBy = append(info.BlockedBy, blocker)
	}
	if err := blockerRows.Err(); err != nil {
		return TaskInfo{}, fmt.Errorf("iterate blockers: %w", err)
	}

	return info, nil
}

type webhookSubscription struct {
	URL    string
	Events []string
}

// ResolveWebhookTargets collects, templates, filters, normalizes, and de-dupes webhook URLs.
func ResolveWebhookTargets(database *db.DB, containerUUID string, payload Payload) ([]string, error) {
	raw, err := collectWebhookSubscriptions(database, containerUUID)
	if err != nil {
		return nil, err
	}
	return normalizeWebhookURLs(raw, payload), nil
}

func collectWebhookSubscriptions(database *db.DB, containerUUID string) ([]webhookSubscription, error) {
	rows, err := database.Query(`
		WITH RECURSIVE container_chain(uuid, parent_uuid, webhook_urls) AS (
			SELECT uuid, parent_uuid, webhook_urls FROM containers WHERE uuid = ?
			UNION ALL
			SELECT c.uuid, c.parent_uuid, c.webhook_urls
			FROM containers c
			JOIN container_chain cc ON c.uuid = cc.parent_uuid
		)
		SELECT webhook_urls FROM container_chain
		WHERE webhook_urls IS NOT NULL AND webhook_urls != ''
	`, containerUUID)
	if err != nil {
		return nil, fmt.Errorf("query webhook urls: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var collected []webhookSubscription
	for rows.Next() {
		var jsonStr string
		if err := rows.Scan(&jsonStr); err != nil {
			return nil, fmt.Errorf("scan webhook urls: %w", err)
		}
		urls, err := decodeWebhookSubscriptions(jsonStr)
		if err != nil {
			return nil, fmt.Errorf("parse webhook urls: %w", err)
		}
		collected = append(collected, urls...)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating webhook urls: %w", err)
	}
	return collected, nil
}

func decodeWebhookSubscriptions(jsonStr string) ([]webhookSubscription, error) {
	var entries []json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &entries); err != nil {
		return nil, err
	}
	out := make([]webhookSubscription, 0, len(entries))
	for _, entry := range entries {
		var urlOnly string
		if err := json.Unmarshal(entry, &urlOnly); err == nil {
			// A bare URL string with no explicit events defaults to ALL event
			// families (task + workflow). Subscribers narrow via the object
			// form {"url":...,"events":[...]}.
			out = append(out, webhookSubscription{URL: urlOnly, Events: []string{"*"}})
			continue
		}
		var structured struct {
			URL    string   `json:"url"`
			Events []string `json:"events"`
		}
		if err := json.Unmarshal(entry, &structured); err != nil {
			return nil, err
		}
		if len(structured.Events) == 0 {
			structured.Events = []string{"*"}
		}
		out = append(out, webhookSubscription{URL: structured.URL, Events: structured.Events})
	}
	return out, nil
}

func normalizeWebhookURLs(urls []webhookSubscription, payload Payload) []string {
	if len(urls) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(urls))
	var normalized []string

	for _, sub := range urls {
		if !subscriptionMatchesEvent(sub, payload.Event) {
			continue
		}
		trimmed := strings.TrimSpace(sub.URL)
		if trimmed == "" {
			continue
		}
		templated := applyTemplate(trimmed, payload)
		templated = strings.TrimSpace(templated)
		if templated == "" {
			continue
		}
		templated = strings.TrimRight(templated, "/")
		if templated == "" {
			continue
		}
		if !isValidWebhookURL(templated) {
			log.Printf("webhooks: skipping invalid url %q", templated)
			continue
		}
		if _, ok := seen[templated]; ok {
			continue
		}
		seen[templated] = struct{}{}
		normalized = append(normalized, templated)
	}

	return normalized
}

func subscriptionMatchesEvent(sub webhookSubscription, event string) bool {
	if len(sub.Events) == 0 {
		// No explicit events => receive everything (task + workflow).
		return true
	}
	for _, allowed := range sub.Events {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		switch allowed {
		case "", "task", "task.*":
			if isTaskWebhookEvent(event) {
				return true
			}
		case "*", "all":
			return true
		case "workflow", "workflow.*":
			if isWorkflowWebhookEvent(event) {
				return true
			}
		default:
			if strings.EqualFold(allowed, event) {
				return true
			}
		}
	}
	return false
}

func isWorkflowWebhookEvent(event string) bool {
	return event == EventWorkflowAttached || event == EventWorkflowTransitioned || strings.HasPrefix(event, "workflow.")
}

func isTaskWebhookEvent(event string) bool {
	return !isWorkflowWebhookEvent(event)
}

func applyTemplate(raw string, payload Payload) string {
	result := strings.ReplaceAll(raw, "{ticket_id}", payload.TicketID)
	result = strings.ReplaceAll(result, "{project_id}", payload.ProjectID)
	return result
}

func isValidWebhookURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if parsed.Host == "" {
		return false
	}
	return true
}

func dispatchURLs(urls []string, payload Payload) {
	if len(urls) == 0 {
		return
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("webhooks: failed to encode payload: %v", err)
		return
	}

	client := &http.Client{Timeout: defaultTimeout}
	workers := defaultConcurrency
	if len(urls) < workers {
		workers = len(urls)
	}

	jobs := make(chan string)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for endpoint := range jobs {
				sendWebhook(client, endpoint, body)
			}
		}()
	}

	for _, endpoint := range urls {
		jobs <- endpoint
	}
	close(jobs)
	wg.Wait()
}

func sendWebhook(client *http.Client, endpoint string, body []byte) {
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		log.Printf("webhooks: build request %q failed: %v", endpoint, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("webhooks: request to %q failed: %v", endpoint, err)
		return
	}
	_ = resp.Body.Close()
}
