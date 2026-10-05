package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/webhooks"
)

type EnvelopeDischargeInvalidError struct {
	Envelope string
	Reason   string
}

func (e *EnvelopeDischargeInvalidError) Error() string {
	return fmt.Sprintf("invalid scoped discharge %s: %s", e.Envelope, e.Reason)
}

// EnvelopeAddressee is one resolved recipient of a say. ScopeRef is empty for a
// scope-less principal (a human), which is never kicked or summoned.
type EnvelopeAddressee struct {
	ScopeRef              string
	PrincipalRef          string
	ProjectUUID           *string
	MaterializationIntent *string
}

// EnvelopeCreateParams carries one say. Addressees empty means obligation none:
// a log entry that fires nothing.
type EnvelopeCreateParams struct {
	RoomUUID              string
	FromPrincipalRef      string
	FromScopeRef          *string
	FromProjectUUID       *string
	SenderMemberRef       string
	SenderScoped          bool
	Addressees            []EnvelopeAddressee
	Obligation            domain.EnvelopeObligation
	Body                  string
	TaskUUID              *string
	RespondToPrincipalRef *string
	IdempotencyKey        *string
	Meta                  *string
	ExpiresAt             *string
	Delivery              domain.EnvelopeDelivery
	// DischargeEnvelopeIDs nil selects the legacy wide reply-is-ack rule in
	// the API. A non-nil set is validated and acked atomically with creation.
	DischargeEnvelopeIDs []string
}

// CreateEnvelopesWithAttribution writes one envelope per addressee in ONE
// transaction sharing a group id, records `addressed` membership for each, and
// emits envelope.created per row. fyi envelopes are auto-acked at their own
// presentation; a `none` envelope is acked at write because nothing will ever
// present it.
func (rs *RoomStore) CreateEnvelopesWithAttribution(attr attribution.Attribution, params EnvelopeCreateParams) ([]domain.Envelope, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	if err := domain.ValidateEnvelopeObligation(params.Obligation); err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.Body) == "" {
		return nil, fmt.Errorf("envelope body is required")
	}
	if params.Obligation == domain.EnvelopeObligationNone && len(params.Addressees) > 0 {
		return nil, fmt.Errorf("obligation none does not accept an addressee")
	}
	if params.Obligation != domain.EnvelopeObligationNone && len(params.Addressees) == 0 {
		return nil, fmt.Errorf("obligation %s requires at least one addressee", params.Obligation)
	}
	if params.Delivery == "" {
		params.Delivery = domain.EnvelopeDeliveryQueue
	}
	if err := domain.ValidateEnvelopeDelivery(params.Delivery); err != nil {
		return nil, err
	}
	if params.Obligation == domain.EnvelopeObligationNone && (params.ExpiresAt != nil || params.Delivery != domain.EnvelopeDeliveryQueue) {
		return nil, fmt.Errorf("obligation none does not accept ttl or delivery intent")
	}

	var created []domain.Envelope
	err := rs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		var err error
		created, err = rs.createEnvelopesTx(tx, ew, attr, params)
		return err
	})
	if err != nil {
		return nil, err
	}
	rs.dispatchCreated(created, attr)
	return created, nil
}

// CreateEnvelopesAndDischargeWithAttribution validates the complete explicit
// discharge set, creates the reply, and acks exactly that set in one
// transaction. A rejected id leaves both reply and obligations untouched.
func (rs *RoomStore) CreateEnvelopesAndDischargeWithAttribution(attr attribution.Attribution, params EnvelopeCreateParams) ([]domain.Envelope, []domain.Envelope, error) {
	if params.DischargeEnvelopeIDs == nil {
		return nil, nil, fmt.Errorf("explicit discharge set is required")
	}
	if err := requireAttribution(attr); err != nil {
		return nil, nil, err
	}
	var created, acked []domain.Envelope
	err := rs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		candidates, err := rs.validateScopedDischargesTx(tx, params)
		if err != nil {
			return err
		}
		created, err = rs.createEnvelopesTx(tx, ew, attr, params)
		if err != nil {
			return err
		}
		for i := range candidates {
			updated, derr := disposeEnvelopeTx(tx, ew, attr, candidates[i], EnvelopeDisposition{State: domain.EnvelopeStateAcked, Reason: "reply_scoped"}, 0)
			if derr != nil {
				return derr
			}
			acked = append(acked, *updated)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	rs.dispatchCreated(created, attr)
	return created, acked, nil
}

func (rs *RoomStore) createEnvelopesTx(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, params EnvelopeCreateParams) ([]domain.Envelope, error) {
	created := []domain.Envelope{}
	if strings.TrimSpace(params.SenderMemberRef) != "" {
		if _, err := upsertRoomMemberTx(tx, ew, attr, params.RoomUUID, RoomMemberSeed{
			MemberRef: params.SenderMemberRef, MemberPrincipalRef: params.FromPrincipalRef,
			Scoped: params.SenderScoped, Source: domain.RoomMemberSourceSpoke,
		}); err != nil {
			return nil, err
		}
	}
	rows := params.Addressees
	if params.Obligation == domain.EnvelopeObligationNone {
		rows = []EnvelopeAddressee{{}}
	}
	groupID := ""
	for _, addressee := range rows {
		var toScope, toPrincipal, intent interface{}
		if params.Obligation != domain.EnvelopeObligationNone {
			toPrincipal = addressee.PrincipalRef
			if strings.TrimSpace(addressee.ScopeRef) != "" {
				toScope = addressee.ScopeRef
			}
			if addressee.MaterializationIntent != nil {
				intent = *addressee.MaterializationIntent
			}
		}
		// A `none` envelope never fires, so it is disposed at write; every
		// addressed envelope opens pending and is disposed by delivery.
		state := domain.EnvelopeStatePending
		var terminalActor, terminalAt interface{}
		if params.Obligation == domain.EnvelopeObligationNone {
			state = domain.EnvelopeStateAcked
			terminalActor = attr.PrincipalRef
		}

		// The idempotency key belongs to the SAY, so EVERY envelope it fanned
		// out to carries it: a consumer dual-writing into another system can
		// correlate on any addressee's row, and the per-(key, addressee)
		// unique index still collides a retried say into a rollback.
		var idempotencyKey interface{}
		if params.IdempotencyKey != nil && strings.TrimSpace(*params.IdempotencyKey) != "" {
			idempotencyKey = *params.IdempotencyKey
		}

		res, err := tx.Exec(`INSERT INTO envelopes (
				id, room_uuid, from_principal_ref, from_scope_ref, from_project_uuid, to_scope_ref,
				to_principal_ref, to_project_uuid, obligation, body, task_uuid, state,
				expires_at, delivery, materialization_intent, respond_to_principal_ref, idempotency_key,
				meta, terminal_actor, terminal_at,
				created_by_principal_ref, created_by_scope_ref,
				updated_by_principal_ref, updated_by_scope_ref
			) VALUES ('', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			params.RoomUUID, params.FromPrincipalRef, params.FromScopeRef, params.FromProjectUUID, toScope,
			toPrincipal, addressee.ProjectUUID, params.Obligation, params.Body, params.TaskUUID, state,
			params.ExpiresAt, params.Delivery, intent, params.RespondToPrincipalRef, idempotencyKey, params.Meta,
			terminalActor, terminalAt,
			attr.PrincipalRef, scopeSQL(attr), attr.PrincipalRef, scopeSQL(attr))
		if err != nil {
			return nil, fmt.Errorf("failed to create envelope: %w", err)
		}
		rowID, err := res.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("failed to read envelope row id: %w", err)
		}
		envelope, err := scanEnvelope(tx.QueryRow("SELECT "+envelopeColumns+" FROM envelopes WHERE rowid = ?", rowID))
		if err != nil {
			return nil, fmt.Errorf("failed to read created envelope: %w", err)
		}
		// group_id equals the FIRST envelope's own id, so a single addressee
		// groups with itself and a fan-out shares one waitable handle.
		if groupID == "" {
			groupID = envelope.ID
		}
		if _, err := tx.Exec("UPDATE envelopes SET group_id = ? WHERE uuid = ?", groupID, envelope.UUID); err != nil {
			return nil, fmt.Errorf("failed to stamp envelope group: %w", err)
		}
		envelope.GroupID = &groupID

		if params.Obligation != domain.EnvelopeObligationNone {
			if _, err := upsertRoomMemberTx(tx, ew, attr, params.RoomUUID, RoomMemberSeed{
				MemberRef:          addresseeMemberRef(addressee),
				MemberPrincipalRef: addressee.PrincipalRef,
				Scoped:             strings.TrimSpace(addressee.ScopeRef) != "",
				Source:             domain.RoomMemberSourceAddressed,
			}); err != nil {
				return nil, err
			}
		}

		payload := envelopeEventPayload(envelope)
		if _, err := logEnvelopeEvent(tx, ew, attr, envelope.UUID, "envelope.created", envelope.ETag, payload); err != nil {
			return nil, err
		}
		created = append(created, *envelope)
	}

	if err := touchRoomActivity(tx, params.RoomUUID); err != nil {
		return nil, err
	}
	return created, nil
}

func (rs *RoomStore) dispatchCreated(created []domain.Envelope, attr attribution.Attribution) {
	for index := range created {
		webhooks.DispatchEnvelopeEvent(rs.store.db, created[index], "envelope.created", envelopeEventPayload(&created[index]), attr.PrincipalRef)
	}
}

func (rs *RoomStore) validateScopedDischargesTx(tx *sql.Tx, params EnvelopeCreateParams) ([]*domain.Envelope, error) {
	if len(params.DischargeEnvelopeIDs) == 0 {
		return nil, &EnvelopeDischargeInvalidError{Reason: "explicit set must not be empty"}
	}
	allowedCounterparties := map[string]bool{}
	for _, addressee := range params.Addressees {
		key := addressee.ScopeRef
		if key == "" {
			key = addressee.PrincipalRef
		}
		allowedCounterparties[key] = true
	}
	seen := map[string]bool{}
	result := make([]*domain.Envelope, 0, len(params.DischargeEnvelopeIDs))
	for _, raw := range params.DischargeEnvelopeIDs {
		selector := strings.TrimSpace(raw)
		if selector == "" || seen[selector] {
			return nil, &EnvelopeDischargeInvalidError{Envelope: selector, Reason: "empty or duplicate envelope id"}
		}
		seen[selector] = true
		envelope, err := getEnvelopeSelectorTx(tx, selector)
		if err != nil {
			return nil, err
		}
		if envelope.RoomUUID != params.RoomUUID {
			return nil, &EnvelopeDischargeInvalidError{Envelope: envelope.ID, Reason: "foreign room"}
		}
		// A deferred envelope is admitted: naming it is the addressee's own
		// explicit reply, the one way they can finish a paused obligation
		// without waiting for a retry that may never be armed.
		if envelope.Obligation != domain.EnvelopeObligationReplyRequired ||
			(envelope.State != domain.EnvelopeStatePending && envelope.State != domain.EnvelopeStatePresented &&
				envelope.State != domain.EnvelopeStateDeferred) {
			return nil, &EnvelopeDischargeInvalidError{Envelope: envelope.ID, Reason: "must be pending, presented, or deferred reply_required"}
		}
		if params.FromScopeRef != nil {
			if envelope.ToScopeRef == nil || *envelope.ToScopeRef != *params.FromScopeRef {
				return nil, &EnvelopeDischargeInvalidError{Envelope: envelope.ID, Reason: "addressed to another scope"}
			}
		} else if envelope.ToScopeRef != nil || envelope.ToPrincipalRef == nil || *envelope.ToPrincipalRef != params.FromPrincipalRef {
			return nil, &EnvelopeDischargeInvalidError{Envelope: envelope.ID, Reason: "addressed to another principal"}
		}
		counterparty := envelope.FromPrincipalRef
		if envelope.FromScopeRef != nil {
			counterparty = *envelope.FromScopeRef
		}
		if !allowedCounterparties[counterparty] {
			return nil, &EnvelopeDischargeInvalidError{Envelope: envelope.ID, Reason: "sender is not a reply addressee"}
		}
		result = append(result, envelope)
	}
	return result, nil
}

// AckSenderObligationsWithAttribution is the reply-is-ack rule: saying into a
// room with --to X acks every PENDING or PRESENTED reply_required envelope in
// that room addressed to the replier's own scope and sent from X's scope. Both
// sides match on the SCOPE, never on a principal: a member IS a scope and its
// principal is attribution only, so a say that carried an --as disagreeing with
// the seat can never silently break the ack (T-07628). Only a scope-less party
// — a human such as agent:lance, who has no scope — matches by principal.
// Sibling envelopes in a fan-out group addressed to OTHER scopes are untouched,
// and a deferred envelope is deliberately excluded so `defer` before replying
// really does exclude it.
func (rs *RoomStore) AckSenderObligationsWithAttribution(attr attribution.Attribution, roomUUID, replierScopeRef, replierPrincipalRef, counterpartyScopeRef, counterpartyPrincipalRef string) ([]domain.Envelope, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	clauses := []string{
		"room_uuid = ?",
		"obligation = 'reply_required'",
		"state IN ('pending', 'presented')",
	}
	args := []interface{}{roomUUID}
	clauses, args = appendEnvelopeParty(clauses, args, "from", counterpartyScopeRef, counterpartyPrincipalRef)
	clauses, args = appendEnvelopeParty(clauses, args, "to", replierScopeRef, replierPrincipalRef)

	candidates, err := rs.queryEnvelopes(context.Background(), "SELECT "+envelopeColumns+" FROM envelopes WHERE "+
		strings.Join(clauses, " AND ")+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	acked := make([]domain.Envelope, 0, len(candidates))
	for index := range candidates {
		updated, err := rs.DisposeEnvelopeWithAttribution(attr, candidates[index].UUID, EnvelopeDisposition{
			State:  domain.EnvelopeStateAcked,
			Reason: "reply",
		}, 0)
		if err != nil {
			return nil, err
		}
		if updated != nil {
			acked = append(acked, *updated)
		}
	}
	return acked, nil
}

func addresseeMemberRef(addressee EnvelopeAddressee) string {
	if ref := strings.TrimSpace(addressee.ScopeRef); ref != "" {
		return ref
	}
	return addressee.PrincipalRef
}
