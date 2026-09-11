package store

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/domain"
)

// ProjectEventStore persists foreign facts without producing event_log rows,
// webhooks, room envelopes, or any other side effect.
type ProjectEventStore struct {
	store *Store
}

type ProjectEventCreateParams struct {
	ProjectUUID    string
	ContainerUUID  string
	CampaignUUID   *string
	TaskUUID       *string
	Type           string
	Summary        string
	Attributes     string
	PrincipalRef   *string
	ScopeRef       *string
	IdempotencyKey *string
	OccurredAt     string
}

// CreateWithAttribution inserts exactly one foreign fact, or returns the
// existing fact for a project-scoped idempotent replay.
func (ps *ProjectEventStore) Create(p ProjectEventCreateParams) (*domain.ProjectEvent, bool, error) {
	tx, err := ps.store.db.Begin()
	if err != nil {
		return nil, false, fmt.Errorf("begin project event post: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if p.IdempotencyKey != nil {
		existing, err := scanProjectEvent(tx.QueryRow(`SELECT `+projectEventColumns+` FROM project_events WHERE project_uuid = ? AND idempotency_key = ?`, p.ProjectUUID, *p.IdempotencyKey))
		if err == nil {
			return existing, false, nil
		}
		if err != sql.ErrNoRows {
			return nil, false, fmt.Errorf("look up project event idempotency key: %w", err)
		}
	}

	result, err := tx.Exec(`INSERT INTO project_events (
		uuid, project_uuid, container_uuid, campaign_uuid, task_uuid, type,
		summary, attributes, principal_ref, scope_ref,
		idempotency_key, occurred_at
	) VALUES (lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-' || lower(hex(randomblob(2))) || '-' || lower(hex(randomblob(2))) || '-' || lower(hex(randomblob(6))), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ProjectUUID, p.ContainerUUID, p.CampaignUUID, p.TaskUUID, p.Type,
		p.Summary, p.Attributes, p.PrincipalRef, p.ScopeRef,
		p.IdempotencyKey, p.OccurredAt)
	if err != nil {
		return nil, false, fmt.Errorf("insert project event: %w", err)
	}
	rowID, err := result.LastInsertId()
	if err != nil {
		return nil, false, fmt.Errorf("read project event row id: %w", err)
	}
	event, err := scanProjectEvent(tx.QueryRow(`SELECT `+projectEventColumns+` FROM project_events WHERE id = ?`, rowID))
	if err != nil {
		return nil, false, fmt.Errorf("read inserted project event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit project event: %w", err)
	}
	return event, true, nil
}

// Get resolves an addressable project event independently of tree membership.
func (ps *ProjectEventStore) Get(uuid string) (*domain.ProjectEvent, error) {
	return scanProjectEvent(ps.store.db.QueryRow(`SELECT `+projectEventColumns+` FROM project_events WHERE uuid = ?`, uuid))
}

// TypesForContainers groups facts by type using only current subtree container
// identities. ProjectUUID is deliberately absent from this read predicate.
func (ps *ProjectEventStore) TypesForContainers(containerUUIDs []string) ([]domain.ProjectEventTypeCount, error) {
	query := `SELECT type, COUNT(*), MAX(created_at) FROM project_events`
	args := make([]any, 0, len(containerUUIDs))
	if containerUUIDs != nil {
		if len(containerUUIDs) == 0 {
			return []domain.ProjectEventTypeCount{}, nil
		}
		query += ` WHERE container_uuid IN (` + strings.TrimRight(strings.Repeat("?,", len(containerUUIDs)), ",") + `)`
		for _, value := range containerUUIDs {
			args = append(args, value)
		}
	}
	query += ` GROUP BY type ORDER BY type`
	rows, err := ps.store.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []domain.ProjectEventTypeCount{}
	for rows.Next() {
		var item domain.ProjectEventTypeCount
		if err := rows.Scan(&item.Type, &item.Count, &item.LastCreatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

const projectEventColumns = `
	id, uuid, project_uuid, container_uuid, campaign_uuid, task_uuid, type,
	summary, attributes, principal_ref, scope_ref, idempotency_key,
	occurred_at, created_at`

type projectEventScanner interface {
	Scan(dest ...any) error
}

func scanProjectEvent(row projectEventScanner) (*domain.ProjectEvent, error) {
	var event domain.ProjectEvent
	var campaign, task, principal, scope, key sql.NullString
	err := row.Scan(
		&event.ID, &event.UUID, &event.ProjectUUID, &event.ContainerUUID,
		&campaign, &task, &event.Type, &event.Summary, &event.Attributes,
		&principal, &scope, &key,
		&event.OccurredAt, &event.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	event.CampaignUUID = nullStringPtr(campaign)
	event.TaskUUID = nullStringPtr(task)
	event.PrincipalRef = nullStringPtr(principal)
	event.ScopeRef = nullStringPtr(scope)
	event.IdempotencyKey = nullStringPtr(key)
	return &event, nil
}
