package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/webhooks"
)

type pendingWebhook struct {
	taskUUID string
	ctx      webhooks.EventContext
}

func sortedFieldNames(fields map[string]interface{}) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func summarizeWebhookValue(field string, value interface{}) interface{} {
	if value == nil {
		if field == "labels" {
			return []string{}
		}
		return nil
	}
	switch field {
	case "description", "specification":
		text, ok := value.(string)
		if !ok {
			return value
		}
		sum := sha256.Sum256([]byte(text))
		return map[string]interface{}{
			"length": len(text),
			"sha256": hex.EncodeToString(sum[:]),
		}
	case "labels":
		return summarizeWebhookLabels(value)
	}
	return value
}

func summarizeWebhookLabels(value interface{}) []string {
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case string:
		text := strings.TrimSpace(v)
		if text == "" || text == "null" {
			return []string{}
		}
		var labels []string
		if err := json.Unmarshal([]byte(text), &labels); err == nil && labels != nil {
			return labels
		}
	case []interface{}:
		labels := make([]string, 0, len(v))
		for _, item := range v {
			label, ok := item.(string)
			if !ok {
				return []string{}
			}
			labels = append(labels, label)
		}
		return labels
	}
	return []string{}
}

func loadTaskFieldValues(tx *sql.Tx, taskUUID string, fields []string) (map[string]interface{}, error) {
	values := make(map[string]interface{}, len(fields))
	for _, field := range fields {
		switch field {
		case "priority", "etag":
			var value int64
			if err := tx.QueryRow("SELECT "+field+" FROM tasks WHERE uuid = ?", taskUUID).Scan(&value); err != nil {
				return nil, err
			}
			if field == "priority" {
				values[field] = int(value)
			} else {
				values[field] = value
			}
		case "state", "slug", "title", "project_uuid", "kind", "resolution", "outcome", "meta", "labels", "due_at", "start_at", "archived_at", "deleted_at", "parent_task_uuid", "assignee_principal_ref", "requester_principal_ref", "requester_scope_ref", "requested_by_project_id", "assigned_project_id", "created_by_principal_ref", "updated_by_principal_ref", "deleted_by_principal_ref", "created_by_scope_ref", "updated_by_scope_ref", "deleted_by_scope_ref":
			var value sql.NullString
			if err := tx.QueryRow("SELECT "+field+" FROM tasks WHERE uuid = ?", taskUUID).Scan(&value); err != nil {
				return nil, err
			}
			if value.Valid {
				values[field] = value.String
			} else {
				values[field] = nil
			}
		case "description", "specification":
			var value string
			if err := tx.QueryRow("SELECT "+field+" FROM tasks WHERE uuid = ?", taskUUID).Scan(&value); err != nil {
				return nil, err
			}
			values[field] = summarizeWebhookValue(field, value)
		}
	}
	return values, nil
}

func buildWebhookChanges(oldValues map[string]interface{}, fields map[string]interface{}) map[string]webhooks.Change {
	changes := make(map[string]webhooks.Change, len(fields))
	for _, field := range sortedFieldNames(fields) {
		changes[field] = webhooks.Change{
			From: summarizeWebhookValue(field, oldValues[field]),
			To:   summarizeWebhookValue(field, fields[field]),
		}
	}
	return changes
}

// dispatchTaskWebhooks fires the webhooks a committed transaction queued.
func dispatchTaskWebhooks(database *db.DB, pending []pendingWebhook) {
	for _, p := range pending {
		webhooks.DispatchTaskEvent(database, p.taskUUID, p.ctx)
	}
}

// logTaskEvent stamps payload with the task's campaign affiliation (in place)
// and appends one task event; etag is nil for events that carry none.
func logTaskEvent(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, taskUUID, eventType string, etag *int64, payload map[string]any) (events.EventMetadata, error) {
	payloadStr, err := stampTaskCampaignJSON(tx, taskUUID, payload)
	if err != nil {
		return events.EventMetadata{}, err
	}
	meta, err := ew.LogEventReturning(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "task",
		ResourceUUID: &taskUUID,
		EventType:    eventType,
		ETag:         etag,
		Payload:      &payloadStr,
	})
	if err != nil {
		return events.EventMetadata{}, fmt.Errorf("failed to log %s event: %w", eventType, err)
	}
	return meta, nil
}
