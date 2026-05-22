package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/id"
)

const (
	HandoffStatusPending      = "pending"
	HandoffStatusAcknowledged = "acknowledged"
)

// Handoff is the store representation of an agent session handoff.
type Handoff struct {
	UUID, ID                             string
	ScopeRef, ScopeKind                  string
	AgentID, ProjectID                   string
	AgentActorUUID, ProjectContainerUUID *string
	CreatedByAgentID                     string
	CreatedByActorUUID                   *string
	Title, Body                          string
	Status                               string
	IdempotencyKey                       *string
	AcknowledgedAt                       *time.Time
	AcknowledgedByAgentID                *string
	AcknowledgedByActorUUID              *string
	AcknowledgementNote                  *string
	Meta                                 *string
	ETag                                 int64
	CreatedAt, UpdatedAt                 time.Time
}

// CreateHandoffArgs contains the pre-resolved values needed to create a handoff.
type CreateHandoffArgs struct {
	ScopeRef, ScopeKind, AgentID, ProjectID string
	CreatedByAgentID                        string
	Title, Body                             string
	IdempotencyKey                          *string
	Meta                                    *string
	AgentActorUUID                          *string
	ProjectContainerUUID                    *string
	CreatedByActorUUID                      *string
}

// CreateHandoffResult reports whether CreateHandoff inserted or replayed an idempotent request.
type CreateHandoffResult struct {
	Handoff          Handoff
	IdempotentReplay bool
}

// ListHandoffsOpts configures handoff listing.
type ListHandoffsOpts struct {
	ScopeRef string
	Status   string
	Limit    int
	Cursor   string
}

// AcknowledgeHandoffArgs configures a pending->acknowledged transition.
type AcknowledgeHandoffArgs struct {
	Note         *string
	ActorAgentID string
	ActorUUID    *string
	DryRun       bool
	IfMatch      int64
}

// SearchHandoffsOpts configures LIKE-based handoff search.
type SearchHandoffsOpts struct {
	Query    string
	ScopeRef string
	Status   string
	Limit    int
	Cursor   string
}

// HandoffIdempotencyPayloadMismatchError is returned when an idempotency key
// replays in the same scope with different handoff content.
type HandoffIdempotencyPayloadMismatchError struct {
	ScopeRef       string
	IdempotencyKey string
	ExistingID     string
	ExistingUUID   string
}

func (e *HandoffIdempotencyPayloadMismatchError) Error() string {
	return fmt.Sprintf("idempotency key %q already created handoff %s in scope %q with different title or body", e.IdempotencyKey, e.ExistingID, e.ScopeRef)
}

// CreateHandoff inserts a pending handoff and logs handoff.created.
func CreateHandoff(ctx context.Context, tx *sql.Tx, args CreateHandoffArgs) (CreateHandoffResult, error) {
	if tx == nil {
		return CreateHandoffResult{}, fmt.Errorf("transaction is required")
	}
	if err := validateCreateHandoffArgs(args); err != nil {
		return CreateHandoffResult{}, err
	}

	if args.IdempotencyKey != nil {
		existing, found, err := getHandoffByIdempotencyKey(ctx, tx, args.ScopeRef, *args.IdempotencyKey)
		if err != nil {
			return CreateHandoffResult{}, err
		}
		if found {
			if existing.Title != args.Title || existing.Body != args.Body {
				return CreateHandoffResult{}, &HandoffIdempotencyPayloadMismatchError{
					ScopeRef:       args.ScopeRef,
					IdempotencyKey: *args.IdempotencyKey,
					ExistingID:     existing.ID,
					ExistingUUID:   existing.UUID,
				}
			}
			return CreateHandoffResult{Handoff: existing, IdempotentReplay: true}, nil
		}
	}

	var nextSeq int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(CAST(SUBSTR(id, 3) AS INTEGER)), 0) + 1 FROM handoffs`).Scan(&nextSeq); err != nil {
		return CreateHandoffResult{}, fmt.Errorf("failed to calculate next handoff ID: %w", err)
	}
	if err := syncHandoffSequence(ctx, tx, nextSeq); err != nil {
		return CreateHandoffResult{}, err
	}

	handoffUUID := uuid.New().String()
	handoffID := id.FormatHandoff(nextSeq)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO handoffs (
			uuid, id, scope_ref, scope_kind, agent_id, project_id,
			agent_actor_uuid, project_container_uuid, created_by_agent_id, created_by_actor_uuid,
			title, body, status, idempotency_key, meta, etag
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, 1)
	`, handoffUUID, handoffID, args.ScopeRef, args.ScopeKind, args.AgentID, args.ProjectID,
		args.AgentActorUUID, args.ProjectContainerUUID, args.CreatedByAgentID, args.CreatedByActorUUID,
		args.Title, args.Body, args.IdempotencyKey, args.Meta); err != nil {
		return CreateHandoffResult{}, fmt.Errorf("failed to insert handoff: %w", err)
	}

	handoff, err := getHandoffByUUIDTx(ctx, tx, handoffUUID)
	if err != nil {
		return CreateHandoffResult{}, err
	}
	if err := logHandoffEvent(ctx, tx, args.CreatedByActorUUID, handoff.UUID, "handoff.created", handoff.ETag, map[string]interface{}{
		"id":                  handoff.ID,
		"scope_ref":           handoff.ScopeRef,
		"scope_kind":          handoff.ScopeKind,
		"agent_id":            handoff.AgentID,
		"project_id":          handoff.ProjectID,
		"created_by_agent_id": handoff.CreatedByAgentID,
		"title":               handoff.Title,
		"status":              handoff.Status,
	}); err != nil {
		return CreateHandoffResult{}, err
	}

	return CreateHandoffResult{Handoff: handoff}, nil
}

// GetHandoff retrieves a handoff by friendly ID or UUID.
func GetHandoff(ctx context.Context, database *db.DB, idOrUUID string) (Handoff, error) {
	if database == nil {
		return Handoff{}, fmt.Errorf("database is required")
	}
	idOrUUID = strings.TrimSpace(idOrUUID)
	if idOrUUID == "" {
		return Handoff{}, fmt.Errorf("handoff id or uuid is required")
	}

	handoff, err := scanHandoff(database.QueryRowContext(ctx, handoffSelectSQL()+` WHERE id = ? OR uuid = ?`, idOrUUID, idOrUUID))
	if err != nil {
		if err == sql.ErrNoRows {
			return Handoff{}, fmt.Errorf("handoff not found: %s", idOrUUID)
		}
		return Handoff{}, err
	}
	return handoff, nil
}

// ListHandoffs returns handoffs ordered by created_at DESC, id DESC.
func ListHandoffs(ctx context.Context, database *db.DB, opts ListHandoffsOpts) ([]Handoff, string, error) {
	status, err := normalizeListHandoffStatus(opts.Status)
	if err != nil {
		return nil, "", err
	}
	return queryHandoffs(ctx, database, handoffQueryOptions{
		ScopeRef: opts.ScopeRef,
		Status:   status,
		Limit:    opts.Limit,
		Cursor:   opts.Cursor,
	})
}

// AcknowledgeHandoff marks a pending handoff as acknowledged and logs handoff.acknowledged.
func AcknowledgeHandoff(ctx context.Context, database *db.DB, idOrUUID string, args AcknowledgeHandoffArgs) (Handoff, error) {
	if database == nil {
		return Handoff{}, fmt.Errorf("database is required")
	}
	if strings.TrimSpace(idOrUUID) == "" {
		return Handoff{}, fmt.Errorf("handoff id or uuid is required")
	}
	if strings.TrimSpace(args.ActorAgentID) == "" {
		return Handoff{}, fmt.Errorf("actor agent id is required")
	}
	if args.Note != nil && strings.TrimSpace(*args.Note) == "" {
		return Handoff{}, fmt.Errorf("acknowledgement note cannot be empty")
	}

	tx, err := database.Begin()
	if err != nil {
		return Handoff{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	handoff, err := getHandoffByIDOrUUIDTx(ctx, tx, idOrUUID)
	if err != nil {
		return Handoff{}, err
	}
	if err := checkETag(handoff.ETag, args.IfMatch); err != nil {
		return Handoff{}, err
	}
	if handoff.Status == HandoffStatusAcknowledged {
		return Handoff{}, fmt.Errorf("handoff %s is already acknowledged", handoff.ID)
	}
	if handoff.Status != HandoffStatusPending {
		return Handoff{}, fmt.Errorf("handoff %s has unsupported status %q", handoff.ID, handoff.Status)
	}

	now := time.Now().UTC().Truncate(time.Second)
	if args.DryRun {
		handoff.Status = HandoffStatusAcknowledged
		handoff.AcknowledgedAt = &now
		handoff.AcknowledgedByAgentID = &args.ActorAgentID
		handoff.AcknowledgedByActorUUID = args.ActorUUID
		handoff.AcknowledgementNote = args.Note
		handoff.ETag++
		handoff.UpdatedAt = now
		return handoff, nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE handoffs
		SET status = 'acknowledged',
			acknowledged_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
			acknowledged_by_agent_id = ?,
			acknowledged_by_actor_uuid = ?,
			acknowledgement_note = ?,
			updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
			etag = etag + 1
		WHERE uuid = ?
	`, args.ActorAgentID, args.ActorUUID, args.Note, handoff.UUID); err != nil {
		return Handoff{}, fmt.Errorf("failed to acknowledge handoff: %w", err)
	}

	acknowledged, err := getHandoffByUUIDTx(ctx, tx, handoff.UUID)
	if err != nil {
		return Handoff{}, err
	}
	if err := logHandoffEvent(ctx, tx, args.ActorUUID, acknowledged.UUID, "handoff.acknowledged", acknowledged.ETag, map[string]interface{}{
		"id":                       acknowledged.ID,
		"acknowledged_by_agent_id": args.ActorAgentID,
		"acknowledgement_note":     args.Note,
		"previous_status":          HandoffStatusPending,
		"status":                   acknowledged.Status,
	}); err != nil {
		return Handoff{}, err
	}

	if err := tx.Commit(); err != nil {
		return Handoff{}, fmt.Errorf("failed to commit transaction: %w", err)
	}
	return acknowledged, nil
}

// SearchHandoffs performs a case-insensitive LIKE search over title, body, and scope_ref.
func SearchHandoffs(ctx context.Context, database *db.DB, opts SearchHandoffsOpts) ([]Handoff, string, error) {
	status, err := normalizeOptionalHandoffStatus(opts.Status)
	if err != nil {
		return nil, "", err
	}
	return queryHandoffs(ctx, database, handoffQueryOptions{
		Query:    opts.Query,
		ScopeRef: opts.ScopeRef,
		Status:   status,
		Limit:    opts.Limit,
		Cursor:   opts.Cursor,
	})
}

type handoffQueryOptions struct {
	Query    string
	ScopeRef string
	Status   string
	Limit    int
	Cursor   string
}

func queryHandoffs(ctx context.Context, database *db.DB, opts handoffQueryOptions) ([]Handoff, string, error) {
	if database == nil {
		return nil, "", fmt.Errorf("database is required")
	}
	if opts.Limit < 0 {
		return nil, "", fmt.Errorf("limit cannot be negative")
	}

	pag, err := cursor.Apply(opts.Cursor, cursor.ApplyOptions{
		SortFields: []string{"created_at"},
		Descending: []bool{true},
		IDField:    "id",
		Limit:      opts.Limit,
	})
	if err != nil {
		return nil, "", err
	}

	query := handoffSelectSQL() + ` WHERE 1=1`
	queryArgs := []interface{}{}
	if opts.ScopeRef != "" {
		query += ` AND scope_ref = ?`
		queryArgs = append(queryArgs, opts.ScopeRef)
	}
	if opts.Status != "" {
		query += ` AND status = ?`
		queryArgs = append(queryArgs, opts.Status)
	}
	if strings.TrimSpace(opts.Query) != "" {
		like := "%" + strings.ToLower(strings.TrimSpace(opts.Query)) + "%"
		query += ` AND (LOWER(title) LIKE ? OR LOWER(body) LIKE ? OR LOWER(scope_ref) LIKE ?)`
		queryArgs = append(queryArgs, like, like, like)
	}
	if pag.WhereClause != "" {
		query += ` AND ` + pag.WhereClause
		queryArgs = append(queryArgs, pag.Params...)
	}
	query += ` ` + pag.OrderByClause
	if pag.LimitClause != "" {
		query += ` ` + pag.LimitClause
		queryArgs = append(queryArgs, *pag.LimitParam)
	}

	rows, err := database.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, "", fmt.Errorf("failed to query handoffs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	handoffs := []Handoff{}
	for rows.Next() {
		handoff, err := scanHandoff(rows)
		if err != nil {
			return nil, "", err
		}
		handoffs = append(handoffs, handoff)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("error iterating handoffs: %w", err)
	}

	hasMore := false
	if opts.Limit > 0 && len(handoffs) > opts.Limit {
		hasMore = true
		handoffs = handoffs[:opts.Limit]
	}

	nextCursor := ""
	if hasMore && len(handoffs) > 0 {
		last := handoffs[len(handoffs)-1]
		nextCursor, err = cursor.BuildNextCursor(
			[]string{"created_at"},
			[]interface{}{last.CreatedAt.Format(time.RFC3339)},
			last.ID,
		)
		if err != nil {
			return nil, "", err
		}
	}
	return handoffs, nextCursor, nil
}

func validateCreateHandoffArgs(args CreateHandoffArgs) error {
	required := map[string]string{
		"scope_ref":           args.ScopeRef,
		"scope_kind":          args.ScopeKind,
		"agent_id":            args.AgentID,
		"project_id":          args.ProjectID,
		"created_by_agent_id": args.CreatedByAgentID,
		"title":               args.Title,
		"body":                args.Body,
	}
	for name, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}
	return nil
}

func normalizeListHandoffStatus(status string) (string, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		return HandoffStatusPending, nil
	}
	return normalizeOptionalHandoffStatus(status)
}

func normalizeOptionalHandoffStatus(status string) (string, error) {
	switch strings.TrimSpace(status) {
	case "", "all":
		return "", nil
	case HandoffStatusPending:
		return HandoffStatusPending, nil
	case HandoffStatusAcknowledged:
		return HandoffStatusAcknowledged, nil
	default:
		return "", fmt.Errorf("invalid handoff status %q: must be pending, acknowledged, or all", status)
	}
}

func syncHandoffSequence(ctx context.Context, tx *sql.Tx, seq int) error {
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO sqlite_sequence(name, seq) VALUES ('handoff_seq', 0)`); err != nil {
		return fmt.Errorf("failed to initialize handoff sequence: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE sqlite_sequence
		SET seq = CASE WHEN seq < ? THEN ? ELSE seq END
		WHERE name = 'handoff_seq'
	`, seq, seq); err != nil {
		return fmt.Errorf("failed to update handoff sequence: %w", err)
	}
	return nil
}

func getHandoffByIdempotencyKey(ctx context.Context, tx *sql.Tx, scopeRef, key string) (Handoff, bool, error) {
	handoff, err := scanHandoff(tx.QueryRowContext(ctx, handoffSelectSQL()+` WHERE scope_ref = ? AND idempotency_key = ?`, scopeRef, key))
	if err != nil {
		if err == sql.ErrNoRows {
			return Handoff{}, false, nil
		}
		return Handoff{}, false, err
	}
	return handoff, true, nil
}

func getHandoffByIDOrUUIDTx(ctx context.Context, tx *sql.Tx, idOrUUID string) (Handoff, error) {
	idOrUUID = strings.TrimSpace(idOrUUID)
	handoff, err := scanHandoff(tx.QueryRowContext(ctx, handoffSelectSQL()+` WHERE id = ? OR uuid = ?`, idOrUUID, idOrUUID))
	if err != nil {
		if err == sql.ErrNoRows {
			return Handoff{}, fmt.Errorf("handoff not found: %s", idOrUUID)
		}
		return Handoff{}, err
	}
	return handoff, nil
}

func getHandoffByUUIDTx(ctx context.Context, tx *sql.Tx, handoffUUID string) (Handoff, error) {
	handoff, err := scanHandoff(tx.QueryRowContext(ctx, handoffSelectSQL()+` WHERE uuid = ?`, handoffUUID))
	if err != nil {
		if err == sql.ErrNoRows {
			return Handoff{}, fmt.Errorf("handoff not found: %s", handoffUUID)
		}
		return Handoff{}, err
	}
	return handoff, nil
}

func handoffSelectSQL() string {
	return `
		SELECT uuid, id, scope_ref, scope_kind, agent_id, project_id,
		       agent_actor_uuid, project_container_uuid, created_by_agent_id, created_by_actor_uuid,
		       title, body, status, idempotency_key,
		       acknowledged_at, acknowledged_by_agent_id, acknowledged_by_actor_uuid, acknowledgement_note,
		       meta, etag, created_at, updated_at
		FROM handoffs`
}

type handoffScanner interface {
	Scan(dest ...interface{}) error
}

func scanHandoff(scanner handoffScanner) (Handoff, error) {
	var handoff Handoff
	var agentActorUUID, projectContainerUUID, createdByActorUUID sql.NullString
	var idempotencyKey, acknowledgedAt, acknowledgedByAgentID, acknowledgedByActorUUID, acknowledgementNote sql.NullString
	var meta sql.NullString
	var createdAt, updatedAt string

	err := scanner.Scan(
		&handoff.UUID, &handoff.ID, &handoff.ScopeRef, &handoff.ScopeKind, &handoff.AgentID, &handoff.ProjectID,
		&agentActorUUID, &projectContainerUUID, &handoff.CreatedByAgentID, &createdByActorUUID,
		&handoff.Title, &handoff.Body, &handoff.Status, &idempotencyKey,
		&acknowledgedAt, &acknowledgedByAgentID, &acknowledgedByActorUUID, &acknowledgementNote,
		&meta, &handoff.ETag, &createdAt, &updatedAt,
	)
	if err != nil {
		return Handoff{}, err
	}

	handoff.AgentActorUUID = nullStringPtr(agentActorUUID)
	handoff.ProjectContainerUUID = nullStringPtr(projectContainerUUID)
	handoff.CreatedByActorUUID = nullStringPtr(createdByActorUUID)
	handoff.IdempotencyKey = nullStringPtr(idempotencyKey)
	handoff.AcknowledgedByAgentID = nullStringPtr(acknowledgedByAgentID)
	handoff.AcknowledgedByActorUUID = nullStringPtr(acknowledgedByActorUUID)
	handoff.AcknowledgementNote = nullStringPtr(acknowledgementNote)
	handoff.Meta = nullStringPtr(meta)
	handoff.AcknowledgedAt = parseTimeNullString(acknowledgedAt)

	parsedCreatedAt, err := parseStoreTimestamp(createdAt)
	if err != nil {
		return Handoff{}, fmt.Errorf("failed to parse created_at for handoff %s: %w", handoff.ID, err)
	}
	parsedUpdatedAt, err := parseStoreTimestamp(updatedAt)
	if err != nil {
		return Handoff{}, fmt.Errorf("failed to parse updated_at for handoff %s: %w", handoff.ID, err)
	}
	handoff.CreatedAt = parsedCreatedAt
	handoff.UpdatedAt = parsedUpdatedAt

	return handoff, nil
}

func nullStringPtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func parseTimeNullString(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed, err := parseStoreTimestamp(value.String)
	if err != nil {
		return nil
	}
	return &parsed
}

func parseStoreTimestamp(value string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, format := range formats {
		parsed, err := time.Parse(format, value)
		if err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unable to parse timestamp: %s", value)
}

func logHandoffEvent(ctx context.Context, tx *sql.Tx, actorUUID *string, resourceUUID, eventType string, etag int64, payload map[string]interface{}) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal handoff event payload: %w", err)
	}
	payloadStr := string(payloadJSON)
	event := &domain.Event{
		ActorUUID:    actorUUID,
		ResourceType: "handoff",
		ResourceUUID: &resourceUUID,
		EventType:    eventType,
		ETag:         &etag,
		Payload:      &payloadStr,
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO event_log (actor_uuid, resource_type, resource_uuid, event_type, etag, payload)
		VALUES (?, ?, ?, ?, ?, ?)
	`, event.ActorUUID, event.ResourceType, event.ResourceUUID, event.EventType, event.ETag, event.Payload); err != nil {
		return fmt.Errorf("failed to log handoff event: %w", err)
	}
	return nil
}
