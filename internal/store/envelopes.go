package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/taskfamily"
)

// EnvelopeNotFoundError identifies a missing envelope.
type EnvelopeNotFoundError struct{ Selector string }

func (e *EnvelopeNotFoundError) Error() string {
	return fmt.Sprintf("envelope not found: %s", e.Selector)
}

const envelopeColumns = `
	uuid, id, room_uuid, group_id, from_principal_ref, from_scope_ref,
	from_project_uuid, to_scope_ref, to_principal_ref, to_project_uuid, obligation, body, task_uuid, state,
	expires_at, delivery, failure_reason, retry_at, defer_reason, terminal_actor, terminal_at,
	materialization_intent, respond_to_principal_ref, retry_promise_uuid,
	idempotency_key, meta, etag, created_at, updated_at,
	created_by_principal_ref, created_by_scope_ref,
	updated_by_principal_ref, updated_by_scope_ref`

const envelopePresentationColumns = `
	uuid, envelope_uuid, room_uuid, member_ref, node, runtime_id,
	host_session_id, generation, run_id, drive_attempt_id, input_id, delivery_outcome,
	presented_at, presented_by_principal_ref`

// GetEnvelope resolves an envelope by UUID or EN- friendly ID.
func (rs *RoomStore) GetEnvelope(selector string) (*domain.Envelope, error) {
	return getEnvelope(rs.store.db, selector, "uuid = ? OR id = ?", selector, selector)
}

// EnvelopeListParams selects envelopes for `wrkc log`, `wrkc inbox`, and the
// HRC-facing wake set.
type EnvelopeListParams struct {
	RoomUUID         string
	GroupID          string
	FromScopeRef     string
	FromPrincipalRef string
	ToScopeRef       string
	// ToScopeRefs is the multi-scope form the kicker uses to ask for every
	// scope one node homes in a single call.
	ToScopeRefs      []string
	ToPrincipalRef   string
	States           []domain.EnvelopeState
	Obligations      []domain.EnvelopeObligation
	TaskUUID         string
	Limit            int
	NewestFirst      bool
	ExcludeObligNone bool
	// StillOwedSince narrows a sender-side listing to the envelopes that still
	// ask something of the sender (T-09880): reply_required only; one with a
	// task while that task is non-terminal; a taskless one while it reached
	// its current state (updated_at) at or after this cutoff. The room-stale
	// half of the taskless rule is a read-time projection the API applies to
	// the rows this bound already limits.
	StillOwedSince string
}

// stillOwedSQL is StillOwedSince's predicate over the unaliased envelopes
// table. The terminal set matches the API's isTerminalTaskState; a task row
// that no longer exists counts as terminal.
const stillOwedSQL = `obligation = 'reply_required' AND (
	(task_uuid IS NOT NULL AND EXISTS (SELECT 1 FROM tasks t WHERE t.uuid = envelopes.task_uuid
	  AND t.state NOT IN ('completed','cancelled','archived','deleted')))
	OR (task_uuid IS NULL AND updated_at >= ?))`

// ListEnvelopes returns envelopes ordered by insertion (id) unless the caller
// asked for the newest first.
func (rs *RoomStore) ListEnvelopes(ctx context.Context, params EnvelopeListParams) ([]domain.Envelope, error) {
	clauses := []string{"1 = 1"}
	args := []interface{}{}
	if params.RoomUUID != "" {
		clauses = append(clauses, "room_uuid = ?")
		args = append(args, params.RoomUUID)
	}
	if params.GroupID != "" {
		clauses = append(clauses, "group_id = ?")
		args = append(args, params.GroupID)
	}
	if params.FromScopeRef != "" {
		clauses = append(clauses, "from_scope_ref = ?")
		args = append(args, params.FromScopeRef)
	}
	if params.FromPrincipalRef != "" {
		clauses = append(clauses, "from_principal_ref = ?")
		args = append(args, params.FromPrincipalRef)
	}
	if params.ToScopeRef != "" {
		clauses = append(clauses, "to_scope_ref = ?")
		args = append(args, params.ToScopeRef)
	}
	if len(params.ToScopeRefs) > 0 {
		placeholders := make([]string, 0, len(params.ToScopeRefs))
		for _, ref := range params.ToScopeRefs {
			placeholders = append(placeholders, "?")
			args = append(args, ref)
		}
		clauses = append(clauses, "to_scope_ref IN ("+strings.Join(placeholders, ",")+")")
	}
	if params.ToPrincipalRef != "" {
		clauses = append(clauses, "to_principal_ref = ?")
		args = append(args, params.ToPrincipalRef)
	}
	if params.TaskUUID != "" {
		clauses = append(clauses, taskfamily.Filter("task_uuid"))
		args = append(args, params.TaskUUID, params.TaskUUID)
	}
	if len(params.States) > 0 {
		placeholders := make([]string, 0, len(params.States))
		for _, state := range params.States {
			if err := domain.ValidateEnvelopeState(state); err != nil {
				return nil, err
			}
			placeholders = append(placeholders, "?")
			args = append(args, state)
		}
		clauses = append(clauses, "state IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(params.Obligations) > 0 {
		placeholders := make([]string, 0, len(params.Obligations))
		for _, obligation := range params.Obligations {
			if err := domain.ValidateEnvelopeObligation(obligation); err != nil {
				return nil, err
			}
			placeholders = append(placeholders, "?")
			args = append(args, obligation)
		}
		clauses = append(clauses, "obligation IN ("+strings.Join(placeholders, ",")+")")
	}
	if params.ExcludeObligNone {
		clauses = append(clauses, "obligation <> 'none'")
	}
	if params.StillOwedSince != "" {
		clauses = append(clauses, stillOwedSQL)
		args = append(args, params.StillOwedSince)
	}

	order := "ORDER BY id"
	if params.NewestFirst {
		order = "ORDER BY id DESC"
	}
	query := "SELECT " + envelopeColumns + " FROM envelopes WHERE " + strings.Join(clauses, " AND ") + " " + order
	if params.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", params.Limit)
	}
	return rs.queryEnvelopes(ctx, query, args...)
}

// CountEnvelopes returns how many envelopes match, without materializing them.
// It is the shape the stop-hook predicate wants.
func (rs *RoomStore) CountEnvelopes(params EnvelopeListParams) (int, error) {
	rows, err := rs.ListEnvelopes(context.Background(), params)
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}

// BirthEnvelope returns the BIRTH ENVELOPE of a target scope: the envelope with
// the lowest ledger sequence addressed to that scope whose obligation is
// `reply_required`, in ANY state. fyi rows never summon and are outside the
// domain, and a log entry has no addressee at all, so neither can ever be the
// birth envelope. Nil means no firing obligation has ever been addressed to the
// scope.
//
// The read is state-INDEPENDENT on purpose (T-07655): HRC's registry host reads
// it to designate, once, the node a virgin scope is born on, and that answer has
// to stay re-derivable after the mail that caused the birth is disposed.
func (rs *RoomStore) BirthEnvelope(scopeRef string) (*domain.Envelope, error) {
	scopeRef = strings.TrimSpace(scopeRef)
	if scopeRef == "" {
		return nil, fmt.Errorf("birth envelope requires a target scope")
	}
	rows, err := rs.ListEnvelopes(context.Background(), EnvelopeListParams{
		ToScopeRef:  scopeRef,
		Obligations: []domain.EnvelopeObligation{domain.EnvelopeObligationReplyRequired},
		Limit:       1,
	})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return &rows[0], nil
}

// PresentedObligationsForReplier lists the PRESENTED reply_required envelopes in
// one room that this replier owes an answer to, most recently presented first.
// It is the evidence a bare `--to <name>` resolves against: the seat waiting on
// the replier is the seat the reply belongs to, whatever scope that seat holds
// (T-07638). The replier matches by SCOPE, exactly as reply-is-ack does; only a
// scope-less party — a human, who has no scope — matches by principal.
func (rs *RoomStore) PresentedObligationsForReplier(roomUUID, replierScopeRef, replierPrincipalRef string) ([]domain.Envelope, error) {
	clauses := []string{
		"room_uuid = ?",
		"obligation = 'reply_required'",
		"state = 'presented'",
	}
	args := []interface{}{roomUUID}
	clauses, args = appendEnvelopeParty(clauses, args, "to", replierScopeRef, replierPrincipalRef)
	// "Most recent" is the most recent PRESENTATION, not the most recent send: a
	// seat answers what it was last shown. An envelope in state presented always
	// has a presentation row; the id tiebreak keeps the order total anyway.
	return rs.queryEnvelopes(context.Background(), "SELECT "+envelopeColumns+" FROM envelopes e WHERE "+
		strings.Join(clauses, " AND ")+` ORDER BY (
			SELECT MAX(p.presented_at) FROM envelope_presentations p WHERE p.envelope_uuid = e.uuid
		) DESC, e.id DESC`, args...)
}

// StandingObligationFromSender returns the oldest non-terminal reply_required
// envelope one addressee still holds from one sender in a room. Pair-room say
// notices use this read before creating the next envelope so the envelope being
// written can never diagnose itself as a stack.
func (rs *RoomStore) StandingObligationFromSender(roomUUID, senderScopeRef, senderPrincipalRef, addresseeScopeRef, addresseePrincipalRef string) (*domain.Envelope, error) {
	clauses := []string{
		"room_uuid = ?",
		"obligation = 'reply_required'",
		"state NOT IN ('acked', 'failed', 'expired', 'withdrawn')",
	}
	args := []interface{}{roomUUID}
	clauses, args = appendEnvelopeParty(clauses, args, "from", senderScopeRef, senderPrincipalRef)
	clauses, args = appendEnvelopeParty(clauses, args, "to", addresseeScopeRef, addresseePrincipalRef)
	rows, err := rs.queryEnvelopes(context.Background(), "SELECT "+envelopeColumns+" FROM envelopes WHERE "+
		strings.Join(clauses, " AND ")+" ORDER BY id LIMIT 1", args...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

func envelopeEventPayload(envelope *domain.Envelope) map[string]interface{} {
	payload := map[string]interface{}{
		"id":                 envelope.ID,
		"room_uuid":          envelope.RoomUUID,
		"obligation":         string(envelope.Obligation),
		"state":              string(envelope.State),
		"from_principal_ref": envelope.FromPrincipalRef,
	}
	if envelope.GroupID != nil {
		payload["group_id"] = *envelope.GroupID
	}
	if envelope.FromScopeRef != nil {
		payload["from_scope_ref"] = *envelope.FromScopeRef
	}
	if envelope.FromProjectUUID != nil {
		payload["from_project_uuid"] = *envelope.FromProjectUUID
	}
	if envelope.ToScopeRef != nil {
		payload["to_scope_ref"] = *envelope.ToScopeRef
	}
	if envelope.ToPrincipalRef != nil {
		payload["to_principal_ref"] = *envelope.ToPrincipalRef
	}
	if envelope.ToProjectUUID != nil {
		payload["to_project_uuid"] = *envelope.ToProjectUUID
	}
	if envelope.TaskUUID != nil {
		payload["task_uuid"] = *envelope.TaskUUID
	}
	if envelope.MaterializationIntent != nil {
		payload["materialization_intent"] = *envelope.MaterializationIntent
	}
	return payload
}

func (rs *RoomStore) queryEnvelopes(ctx context.Context, query string, args ...interface{}) ([]domain.Envelope, error) {
	return queryEnvelopesWith(ctx, rs.store.db, query, args...)
}

type contextRowsQueryer interface {
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
}

func queryEnvelopesWith(ctx context.Context, q contextRowsQueryer, query string, args ...interface{}) ([]domain.Envelope, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query envelopes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := []domain.Envelope{}
	for rows.Next() {
		envelope, err := scanEnvelope(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan envelope: %w", err)
		}
		result = append(result, *envelope)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate envelopes: %w", err)
	}
	return result, nil
}

func scanEnvelope(scanner rowScanner) (*domain.Envelope, error) {
	envelope := &domain.Envelope{}
	err := scanner.Scan(envelopeScanDestinations(envelope)...)
	return envelope, err
}

func envelopeScanDestinations(envelope *domain.Envelope) []interface{} {
	return []interface{}{
		&envelope.UUID, &envelope.ID, &envelope.RoomUUID, &envelope.GroupID,
		&envelope.FromPrincipalRef, &envelope.FromScopeRef, &envelope.FromProjectUUID, &envelope.ToScopeRef,
		&envelope.ToPrincipalRef, &envelope.ToProjectUUID, &envelope.Obligation, &envelope.Body,
		&envelope.TaskUUID, &envelope.State, &envelope.ExpiresAt, &envelope.Delivery, &envelope.FailureReason,
		&envelope.RetryAt, &envelope.DeferReason, &envelope.TerminalActor,
		&envelope.TerminalAt, &envelope.MaterializationIntent,
		&envelope.RespondToPrincipalRef, &envelope.RetryPromiseUUID,
		&envelope.IdempotencyKey, &envelope.Meta, &envelope.ETag,
		&envelope.CreatedAt, &envelope.UpdatedAt,
		&envelope.CreatedByPrincipalRef, &envelope.CreatedByScopeRef,
		&envelope.UpdatedByPrincipalRef, &envelope.UpdatedByScopeRef,
	}
}

func scanEnvelopePresentation(scanner rowScanner) (*domain.EnvelopePresentation, error) {
	presentation := &domain.EnvelopePresentation{}
	err := scanner.Scan(
		&presentation.UUID, &presentation.EnvelopeUUID, &presentation.RoomUUID,
		&presentation.MemberRef, &presentation.Node, &presentation.RuntimeID,
		&presentation.HostSessionID, &presentation.Generation, &presentation.RunID,
		&presentation.DriveAttemptID, &presentation.InputID, &presentation.DeliveryOutcome,
		&presentation.PresentedAt, &presentation.PresentedByPrincipalRef,
	)
	return presentation, err
}

// getEnvelope reads the one envelope matching where, reporting selector when
// none does.
func getEnvelope(q rowQueryer, selector, where string, args ...interface{}) (*domain.Envelope, error) {
	envelope, err := scanEnvelope(q.QueryRow("SELECT "+envelopeColumns+" FROM envelopes WHERE "+where, args...))
	if err == sql.ErrNoRows {
		return nil, &EnvelopeNotFoundError{Selector: selector}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get envelope: %w", err)
	}
	return envelope, nil
}

func getEnvelopeTx(tx *sql.Tx, uuid string) (*domain.Envelope, error) {
	return getEnvelope(tx, uuid, "uuid = ?", uuid)
}

func getEnvelopeSelectorTx(tx *sql.Tx, selector string) (*domain.Envelope, error) {
	return getEnvelope(tx, selector, "uuid = ? OR id = ?", selector, selector)
}

func newestPresentationTx(tx *sql.Tx, envelopeUUID string) (*domain.EnvelopePresentation, error) {
	presentation, err := scanEnvelopePresentation(tx.QueryRow(`SELECT `+envelopePresentationColumns+`
		FROM envelope_presentations WHERE envelope_uuid = ? ORDER BY presented_at DESC, rowid DESC LIMIT 1`, envelopeUUID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read envelope presentation: %w", err)
	}
	return presentation, nil
}

func logEnvelopeEvent(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, envelopeUUID, eventType string, etag int64, payload map[string]interface{}) (events.EventMetadata, error) {
	return logCollaborationEvent(tx, ew, attr, "envelope", envelopeUUID, eventType, etag, payload)
}

// appendEnvelopeParty adds a clause matching one side ("from" or "to") of an
// envelope to a party: by scope when the party has one, else — a scope-less
// human — by principal on a scope-less row. A member IS a scope; its
// principal is attribution only (T-07628).
func appendEnvelopeParty(clauses []string, args []interface{}, side, scopeRef, principalRef string) ([]string, []interface{}) {
	if strings.TrimSpace(scopeRef) != "" {
		return append(clauses, side+"_scope_ref = ?"), append(args, scopeRef)
	}
	return append(clauses, side+"_scope_ref IS NULL AND "+side+"_principal_ref = ?"), append(args, principalRef)
}
