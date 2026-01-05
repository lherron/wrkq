package webhooks

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lherron/wrkq/internal/db"
)

const (
	defaultTimeout     = 500 * time.Millisecond
	defaultConcurrency = 4
)

// Payload is the webhook payload for task updates.
type Payload struct {
	TicketID     string          `json:"ticket_id"`
	TicketUUID   string          `json:"ticket_uuid"`
	ProjectID    string          `json:"project_id"`
	ProjectUUID  string          `json:"project_uuid"`
	State        string          `json:"state"`
	Priority     int             `json:"priority"`
	Kind         string          `json:"kind"`
	RunStatus    *string         `json:"run_status"`
	Resolution   *string         `json:"resolution"`
	Meta         json.RawMessage `json:"meta"`
	ETag         int64           `json:"etag"`
	CPProjectID  *string         `json:"cp_project_id"`
	CPRunID      *string         `json:"cp_run_id"`
	CPSessionID  *string         `json:"cp_session_id"`
	SDKSessionID *string         `json:"sdk_session_id"`
}

// TaskInfo carries task metadata needed for webhook dispatch.
type TaskInfo struct {
	TaskID       string
	TaskUUID     string
	ProjectID    string
	ProjectUUID  string
	State        string
	Priority     int
	Kind         string
	RunStatus    *string
	Resolution   *string
	Meta         *string
	ETag         int64
	CPProjectID  *string
	CPRunID      *string
	CPSessionID  *string
	SDKSessionID *string
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
	meta := json.RawMessage(`{}`)
	if info.Meta != nil && *info.Meta != "" {
		if json.Valid([]byte(*info.Meta)) {
			meta = json.RawMessage(*info.Meta)
		}
	}
	payload := Payload{
		TicketID:     info.TaskID,
		TicketUUID:   info.TaskUUID,
		ProjectID:    info.ProjectID,
		ProjectUUID:  info.ProjectUUID,
		State:        info.State,
		Priority:     info.Priority,
		Kind:         info.Kind,
		RunStatus:    info.RunStatus,
		Resolution:   info.Resolution,
		Meta:         meta,
		ETag:         info.ETag,
		CPProjectID:  info.CPProjectID,
		CPRunID:      info.CPRunID,
		CPSessionID:  info.CPSessionID,
		SDKSessionID: info.SDKSessionID,
	}
	urls, err := ResolveWebhookTargets(database, info.ProjectUUID, payload)
	if err != nil {
		log.Printf("webhooks: resolve targets for task %s failed: %v", info.TaskID, err)
		return
	}
	dispatchURLs(urls, payload)
}

// LookupTaskInfo fetches the task and project friendly IDs for dispatch.
func LookupTaskInfo(database *db.DB, taskUUID string) (TaskInfo, error) {
	var info TaskInfo
	var runStatus sql.NullString
	var resolution sql.NullString
	var meta sql.NullString
	var cpProjectID sql.NullString
	var cpRunID sql.NullString
	var cpSessionID sql.NullString
	var sdkSessionID sql.NullString
	err := database.QueryRow(`
		SELECT t.id, t.uuid, t.project_uuid, c.id,
		       t.state, t.priority, t.kind, t.run_status, t.resolution, t.meta, t.etag,
		       t.cp_project_id, t.cp_run_id, t.cp_session_id, t.sdk_session_id
		FROM tasks t
		JOIN containers c ON c.uuid = t.project_uuid
		WHERE t.uuid = ?
	`, taskUUID).Scan(
		&info.TaskID,
		&info.TaskUUID,
		&info.ProjectUUID,
		&info.ProjectID,
		&info.State,
		&info.Priority,
		&info.Kind,
		&runStatus,
		&resolution,
		&meta,
		&info.ETag,
		&cpProjectID,
		&cpRunID,
		&cpSessionID,
		&sdkSessionID,
	)
	if err != nil {
		return TaskInfo{}, fmt.Errorf("lookup task info: %w", err)
	}
	if runStatus.Valid {
		info.RunStatus = &runStatus.String
	}
	if resolution.Valid {
		info.Resolution = &resolution.String
	}
	if meta.Valid {
		info.Meta = &meta.String
	}
	if cpProjectID.Valid {
		info.CPProjectID = &cpProjectID.String
	}
	if cpRunID.Valid {
		info.CPRunID = &cpRunID.String
	}
	if cpSessionID.Valid {
		info.CPSessionID = &cpSessionID.String
	}
	if sdkSessionID.Valid {
		info.SDKSessionID = &sdkSessionID.String
	}
	return info, nil
}

// ResolveWebhookTargets collects, templates, normalizes, and de-dupes webhook URLs.
func ResolveWebhookTargets(database *db.DB, containerUUID string, payload Payload) ([]string, error) {
	raw, err := collectWebhookURLs(database, containerUUID)
	if err != nil {
		return nil, err
	}
	return normalizeWebhookURLs(raw, payload), nil
}

func collectWebhookURLs(database *db.DB, containerUUID string) ([]string, error) {
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
	defer rows.Close()

	var collected []string
	for rows.Next() {
		var jsonStr string
		if err := rows.Scan(&jsonStr); err != nil {
			return nil, fmt.Errorf("scan webhook urls: %w", err)
		}
		var urls []string
		if err := json.Unmarshal([]byte(jsonStr), &urls); err != nil {
			return nil, fmt.Errorf("parse webhook urls: %w", err)
		}
		collected = append(collected, urls...)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating webhook urls: %w", err)
	}
	return collected, nil
}

func normalizeWebhookURLs(urls []string, payload Payload) []string {
	if len(urls) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(urls))
	var normalized []string

	for _, raw := range urls {
		trimmed := strings.TrimSpace(raw)
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
