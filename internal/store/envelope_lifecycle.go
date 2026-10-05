package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
)

// EnvelopeWrongStateError identifies a disposition verb attempted on an
// envelope that has already reached a terminal state.
type EnvelopeWrongStateError struct {
	Envelope string
	State    domain.EnvelopeState
	Verb     string
}

// EnvelopeAlreadyPresentedError carries the receipt that makes sender
// withdrawal illegal.
type EnvelopeAlreadyPresentedError struct {
	Envelope     string
	Presentation domain.EnvelopePresentation
}

func (e *EnvelopeAlreadyPresentedError) Error() string {
	return fmt.Sprintf("already_presented: envelope %s", e.Envelope)
}

func (e *EnvelopeWrongStateError) Error() string {
	return fmt.Sprintf("cannot %s envelope %s in state %s", e.Verb, e.Envelope, e.State)
}

// EnvelopeRuntimeMismatchError identifies a fail call whose runtime does not
// own the envelope's newest presentation receipt.
type EnvelopeRuntimeMismatchError struct {
	Envelope string
	Runtime  string
}

func (e *EnvelopeRuntimeMismatchError) Error() string {
	return fmt.Sprintf("cannot fail envelope %s from runtime %s: newest presentation belongs to another runtime", e.Envelope, e.Runtime)
}

// envelopeExpiryDueSQL selects, over alias e, the addressed live envelopes
// that no presentation receipt has ever reached: the only rows either deadline
// can end. A committed receipt permanently exempts an envelope from both.
const envelopeExpiryDueSQL = `e.state IN ('pending','deferred') AND e.to_principal_ref IS NOT NULL
	AND NOT EXISTS (SELECT 1 FROM envelope_presentations p WHERE p.envelope_uuid = e.uuid)`

// envelopeExpiryKindSQL classifies a candidate by server time. An explicit
// expires_at strictly earlier than the implicit undelivered horizon
// (created_at + 24h) materializes expired/ttl; otherwise the horizon
// materializes failed/undeliverable, so a later explicit deadline never extends
// it and an equal one yields to it. An unparseable created_at has no horizon.
const envelopeExpiryKindSQL = `CASE
	WHEN e.expires_at IS NOT NULL AND e.expires_at <= strftime('%Y-%m-%dT%H:%M:%SZ','now')
	  AND (strftime('%Y-%m-%dT%H:%M:%SZ', e.created_at, '+24 hours') IS NULL
	    OR e.expires_at < strftime('%Y-%m-%dT%H:%M:%SZ', e.created_at, '+24 hours'))
	  THEN 'expired'
	WHEN strftime('%Y-%m-%dT%H:%M:%SZ', e.created_at, '+24 hours') <= strftime('%Y-%m-%dT%H:%M:%SZ','now')
	  THEN 'undeliverable'
	END`

// EnvelopeTTLExpiredDetail is the envelope.failed detail that marks a failure
// written by the implicit undelivered horizon rather than by a reader.
const EnvelopeTTLExpiredDetail = "envelope_ttl_expired"

// envelopeExpiryDueTx re-reads, inside the caller's transaction, whether one
// envelope is due and which terminal it takes ("" when not due).
func envelopeExpiryDueTx(tx *sql.Tx, envelopeUUID string) (string, error) {
	var kind sql.NullString
	err := tx.QueryRow(`SELECT `+envelopeExpiryKindSQL+` FROM envelopes e
		WHERE e.uuid = ? AND `+envelopeExpiryDueSQL, envelopeUUID).Scan(&kind)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to check envelope expiry: %w", err)
	}
	return kind.String, nil
}

// materializeEnvelopeExpiryTx writes the terminal a due envelope takes. It is
// the single terminal-write path for both deadlines, shared by the global
// sweep and presentation recording.
func materializeEnvelopeExpiryTx(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, current *domain.Envelope, kind string) (*domain.Envelope, error) {
	switch kind {
	case "expired":
		return disposeEnvelopeTx(tx, ew, attr, current, EnvelopeDisposition{State: domain.EnvelopeStateExpired, Reason: "ttl"}, 0)
	case "undeliverable":
		return failEnvelopeTx(tx, ew, attr, current, domain.EnvelopeFailureUndeliverable, "wrkq", "", EnvelopeTTLExpiredDetail)
	}
	return nil, fmt.Errorf("unknown envelope expiry kind %q", kind)
}

// ExpireDueEnvelopes materializes server-clock expiry exactly once on the
// authoritative observation paths: an earlier explicit expires_at as
// expired/ttl, and the implicit created_at + 24h undelivered horizon as
// failed/undeliverable. Presentation receipts permanently exclude an envelope
// from both. A read-only probe runs first so an observation with nothing due
// never takes the writer lock; the transaction re-evaluates every row.
func (rs *RoomStore) ExpireDueEnvelopes(attr attribution.Attribution) (int, error) {
	if err := requireAttribution(attr); err != nil {
		return 0, err
	}
	var anyDue int
	if err := rs.store.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM envelopes e WHERE ` +
		envelopeExpiryDueSQL + ` AND (` + envelopeExpiryKindSQL + `) IS NOT NULL)`).Scan(&anyDue); err != nil {
		return 0, fmt.Errorf("failed to probe expired envelopes: %w", err)
	}
	if anyDue == 0 {
		return 0, nil
	}
	expired := 0
	err := rs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		rows, err := tx.Query(`SELECT ` + envelopeColumns + `, ` + envelopeExpiryKindSQL + ` FROM envelopes e
			WHERE ` + envelopeExpiryDueSQL + ` AND (` + envelopeExpiryKindSQL + `) IS NOT NULL
			ORDER BY e.id`)
		if err != nil {
			return fmt.Errorf("failed to find expired envelopes: %w", err)
		}
		type dueEnvelope struct {
			envelope *domain.Envelope
			kind     string
		}
		var due []dueEnvelope
		for rows.Next() {
			var kind string
			envelope, serr := scanEnvelope(appendScan{rows, &kind})
			if serr != nil {
				_ = rows.Close()
				return serr
			}
			due = append(due, dueEnvelope{envelope, kind})
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for _, d := range due {
			if _, err := materializeEnvelopeExpiryTx(tx, ew, attr, d.envelope, d.kind); err != nil {
				return err
			}
			expired++
		}
		return nil
	})
	return expired, err
}

type EnvelopeWithdrawRefusal struct {
	Envelope     domain.Envelope
	Reason       string
	Presentation *domain.EnvelopePresentation
}

type EnvelopeWithdrawResult struct {
	Withdrawn []domain.Envelope
	Refused   []EnvelopeWithdrawRefusal
}

// WithdrawEnvelopesWithAttribution withdraws one envelope, or every sibling
// in its fan-out group, in one transaction. Presented siblings are reported
// and left unchanged.
func (rs *RoomStore) WithdrawEnvelopesWithAttribution(attr attribution.Attribution, selector string, group bool, reason string) (*EnvelopeWithdrawResult, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	result := &EnvelopeWithdrawResult{Withdrawn: []domain.Envelope{}, Refused: []EnvelopeWithdrawRefusal{}}
	err := rs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		target, err := getEnvelopeSelectorTx(tx, selector)
		if err != nil {
			return err
		}
		candidates := []*domain.Envelope{target}
		if group && target.GroupID != nil {
			siblings, qerr := queryEnvelopesWith(context.Background(), tx, "SELECT "+envelopeColumns+" FROM envelopes WHERE group_id = ? ORDER BY id", *target.GroupID)
			if qerr != nil {
				return qerr
			}
			candidates = nil
			for i := range siblings {
				candidates = append(candidates, &siblings[i])
			}
		}
		for _, envelope := range candidates {
			presentation, perr := newestPresentationTx(tx, envelope.UUID)
			if perr != nil {
				return perr
			}
			if presentation != nil {
				if !group {
					return &EnvelopeAlreadyPresentedError{Envelope: envelope.ID, Presentation: *presentation}
				}
				result.Refused = append(result.Refused, EnvelopeWithdrawRefusal{Envelope: *envelope, Reason: "already_presented", Presentation: presentation})
				continue
			}
			if envelope.State == domain.EnvelopeStateWithdrawn {
				result.Withdrawn = append(result.Withdrawn, *envelope)
				continue
			}
			if envelope.State != domain.EnvelopeStatePending && envelope.State != domain.EnvelopeStateDeferred {
				if !group {
					return &EnvelopeWrongStateError{Envelope: envelope.ID, State: envelope.State, Verb: "withdraw"}
				}
				result.Refused = append(result.Refused, EnvelopeWithdrawRefusal{Envelope: *envelope, Reason: "wrong_state"})
				continue
			}
			updated, derr := disposeEnvelopeTx(tx, ew, attr, envelope, EnvelopeDisposition{State: domain.EnvelopeStateWithdrawn, Reason: reason}, 0)
			if derr != nil {
				return derr
			}
			result.Withdrawn = append(result.Withdrawn, *updated)
		}
		return nil
	})
	return result, err
}

// RependDueDeferrals returns deferred envelopes whose retry time has arrived to
// pending so the kicker's next sweep re-drives them, and resolves the promise
// that was carrying the deferral. This is a DERIVED transition back to the
// pre-defer state, so it emits no event: the kicker wakes on its periodic
// sweep, exactly as the spec routes it.
func (rs *RoomStore) RependDueDeferrals(attr attribution.Attribution) (int, error) {
	if err := requireAttribution(attr); err != nil {
		return 0, err
	}
	// Probe on a plain read first, as ExpireDueEnvelopes does: every inbox
	// read calls this, and an unconditional write transaction would queue
	// concurrent reads on the SQLite writer lock (T-09997). The transaction
	// re-selects, so a racing caller that loses finds nothing to do.
	var anyDue int
	if err := rs.store.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM envelopes
		 WHERE state = 'deferred' AND retry_at IS NOT NULL
		   AND retry_at <= strftime('%Y-%m-%dT%H:%M:%SZ','now'))`).Scan(&anyDue); err != nil {
		return 0, fmt.Errorf("failed to probe due deferrals: %w", err)
	}
	if anyDue == 0 {
		return 0, nil
	}
	repended := 0
	err := rs.store.withTx(func(tx *sql.Tx, _ *events.Writer) error {
		rows, err := tx.Query(`SELECT uuid, retry_promise_uuid FROM envelopes
			 WHERE state = 'deferred' AND retry_at IS NOT NULL
			   AND retry_at <= strftime('%Y-%m-%dT%H:%M:%SZ','now')
			 ORDER BY id`)
		if err != nil {
			return fmt.Errorf("failed to find due deferrals: %w", err)
		}
		type due struct {
			uuid    string
			promise sql.NullString
		}
		var items []due
		for rows.Next() {
			var item due
			if err := rows.Scan(&item.uuid, &item.promise); err != nil {
				_ = rows.Close()
				return fmt.Errorf("failed to scan due deferral: %w", err)
			}
			items = append(items, item)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}

		for _, item := range items {
			if _, err := tx.Exec(`UPDATE envelopes
				SET state = 'pending', retry_at = NULL, etag = etag + 1,
				    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
				    updated_by_principal_ref = ?, updated_by_scope_ref = ?
				WHERE uuid = ? AND state = 'deferred'`, attr.PrincipalRef, scopeSQL(attr), item.uuid); err != nil {
				return fmt.Errorf("failed to re-pend deferred envelope: %w", err)
			}
			if item.promise.Valid {
				if err := resolveDeferralPromiseTx(tx, attr, item.promise.String, "deferred envelope re-pended"); err != nil {
					return err
				}
			}
			repended++
		}
		return nil
	})
	return repended, err
}

// resolveDeferralPromiseTx closes the open promise that carried a deferral,
// once the envelope leaves deferred (re-pended or terminal).
func resolveDeferralPromiseTx(tx *sql.Tx, attr attribution.Attribution, promiseUUID, note string) error {
	if _, err := tx.Exec(`UPDATE promises
		SET state = 'resolved',
		    closed_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
		    last_reviewed_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
		    last_review_note = ?,
		    etag = etag + 1,
		    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
		    updated_by_principal_ref = ?, updated_by_scope_ref = ?
		WHERE uuid = ? AND state = 'open'`, note, attr.PrincipalRef, scopeSQL(attr), promiseUUID); err != nil {
		return fmt.Errorf("failed to resolve deferral promise: %w", err)
	}
	return nil
}

// EnvelopeDisposition is one terminal or paused transition applied to a single
// envelope.
type EnvelopeDisposition struct {
	State            domain.EnvelopeState
	DeferReason      *string
	RetryAt          *string
	RetryPromiseUUID *string
	Reason           string
}

// DisposeEnvelopeWithAttribution applies one disposition under a
// first-terminal-wins CAS and emits the matching envelope event. An identical
// repeat of a disposition already recorded is idempotent; a conflicting one is
// refused visibly.
func (rs *RoomStore) DisposeEnvelopeWithAttribution(attr attribution.Attribution, envelopeUUID string, disposition EnvelopeDisposition, ifMatch int64) (*domain.Envelope, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	if err := domain.ValidateEnvelopeState(disposition.State); err != nil {
		return nil, err
	}
	var updated *domain.Envelope
	err := rs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		current, err := getEnvelopeTx(tx, envelopeUUID)
		if err != nil {
			return err
		}
		updated, err = disposeEnvelopeTx(tx, ew, attr, current, disposition, ifMatch)
		return err
	})
	return updated, err
}

func disposeEnvelopeTx(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, current *domain.Envelope, disposition EnvelopeDisposition, ifMatch int64) (*domain.Envelope, error) {
	eventType := map[domain.EnvelopeState]string{
		domain.EnvelopeStateAcked: "envelope.acked", domain.EnvelopeStateDeferred: "envelope.deferred",
		domain.EnvelopeStateExpired: "envelope.expired", domain.EnvelopeStateWithdrawn: "envelope.withdrawn",
	}[disposition.State]
	if eventType == "" {
		return nil, fmt.Errorf("envelope disposition %q is not a terminal or paused state", disposition.State)
	}
	if err := checkETag(current.ETag, ifMatch); err != nil {
		return nil, err
	}
	if domain.IsEnvelopeTerminal(current.State) {
		if current.State == disposition.State {
			return current, nil
		}
		return nil, &EnvelopeWrongStateError{Envelope: current.ID, State: current.State, Verb: string(disposition.State)}
	}
	var terminalActor, terminalAt interface{}
	if domain.IsEnvelopeTerminal(disposition.State) {
		terminalActor = attr.PrincipalRef
		if disposition.State == domain.EnvelopeStateExpired {
			terminalActor = "wrkq"
		}
		now, err := serverNowTx(tx)
		if err != nil {
			return nil, err
		}
		terminalAt = now
	}
	if _, err := tx.Exec(`UPDATE envelopes SET state = ?, defer_reason = ?, retry_at = ?, retry_promise_uuid = ?, terminal_actor = ?, terminal_at = ?, etag = etag + 1, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'), updated_by_principal_ref = ?, updated_by_scope_ref = ? WHERE uuid = ?`, disposition.State, disposition.DeferReason, disposition.RetryAt, disposition.RetryPromiseUUID, terminalActor, terminalAt, attr.PrincipalRef, scopeSQL(attr), current.UUID); err != nil {
		return nil, fmt.Errorf("failed to dispose envelope: %w", err)
	}
	// Leaving deferred for a terminal state retires the retry that carried
	// the deferral; otherwise its promise stays open with nothing to re-pend.
	if current.State == domain.EnvelopeStateDeferred && domain.IsEnvelopeTerminal(disposition.State) &&
		current.RetryPromiseUUID != nil {
		if err := resolveDeferralPromiseTx(tx, attr, *current.RetryPromiseUUID, "deferred envelope "+string(disposition.State)); err != nil {
			return nil, err
		}
	}
	payload := map[string]interface{}{"state": string(disposition.State), "previous_state": string(current.State), "room_uuid": current.RoomUUID}
	if disposition.DeferReason != nil {
		payload["defer_reason"] = *disposition.DeferReason
	}
	if disposition.RetryAt != nil {
		payload["retry_at"] = *disposition.RetryAt
	}
	if disposition.Reason != "" {
		payload["reason"] = disposition.Reason
	}
	if _, err := logEnvelopeEvent(tx, ew, attr, current.UUID, eventType, current.ETag+1, payload); err != nil {
		return nil, err
	}
	return getEnvelopeTx(tx, current.UUID)
}

// PresentationRecord is the HRC-written receipt of one presentation.
type PresentationRecord struct {
	MemberRef      string
	Node           *string
	RuntimeID      *string
	HostSessionID  *string
	Generation     *string
	RunID          *string
	DriveAttemptID *string
	// InputID is the broker input that accepted this presentation, held opaquely.
	InputID *string
	// DeliveryOutcome is HRC's steer class for this delivery, held opaquely.
	DeliveryOutcome *string
}

// RecordPresentationWithAttribution writes presented_to, advances the envelope
// to presented, and emits envelope.presented. A fyi envelope is auto-acked at
// its OWN presentation, so a fyi presented to one recipient stays pending for
// the others. One driveAttemptId presents an envelope exactly once: a repeat
// returns the envelope unchanged rather than double-presenting.
func (rs *RoomStore) RecordPresentationWithAttribution(attr attribution.Attribution, envelopeUUID string, record PresentationRecord) (*domain.Envelope, bool, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, false, err
	}
	var updated *domain.Envelope
	recorded := false
	var afterCommit error
	err := rs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		current, err := getEnvelopeTx(tx, envelopeUUID)
		if err != nil {
			return err
		}
		if record.DriveAttemptID != nil && strings.TrimSpace(*record.DriveAttemptID) != "" {
			var exists int
			if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM envelope_presentations
				WHERE envelope_uuid = ? AND drive_attempt_id = ?)`,
				envelopeUUID, *record.DriveAttemptID).Scan(&exists); err != nil {
				return fmt.Errorf("failed to check drive attempt: %w", err)
			}
			if exists == 1 {
				updated = current
				return nil
			}
		}
		if domain.IsEnvelopeTerminal(current.State) {
			return &EnvelopeWrongStateError{Envelope: current.ID, State: current.State, Verb: "present"}
		}
		// Expiry that is due now commits first and refuses this receipt; a
		// receipt that committed earlier already exempts the envelope here.
		kind, err := envelopeExpiryDueTx(tx, current.UUID)
		if err != nil {
			return err
		}
		if kind != "" {
			updated, err = materializeEnvelopeExpiryTx(tx, ew, attr, current, kind)
			if err != nil {
				return err
			}
			afterCommit = &EnvelopeWrongStateError{Envelope: current.ID, State: updated.State, Verb: "present"}
			return nil
		}
		if _, err := tx.Exec(`INSERT INTO envelope_presentations (
			envelope_uuid, room_uuid, member_ref, node, runtime_id, host_session_id,
			generation, run_id, drive_attempt_id, input_id, delivery_outcome, presented_by_principal_ref
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			envelopeUUID, current.RoomUUID, record.MemberRef, record.Node, record.RuntimeID,
			record.HostSessionID, record.Generation, record.RunID, record.DriveAttemptID,
			record.InputID, record.DeliveryOutcome, attr.PrincipalRef); err != nil {
			return fmt.Errorf("failed to record presentation: %w", err)
		}
		recorded = true

		nextState := domain.EnvelopeStatePresented
		var terminalActor, terminalAt interface{}
		if current.Obligation == domain.EnvelopeObligationFYI {
			nextState = domain.EnvelopeStateAcked
			terminalActor = attr.PrincipalRef
			now, nerr := serverNowTx(tx)
			if nerr != nil {
				return nerr
			}
			terminalAt = now
		}
		if domain.IsEnvelopeTerminal(current.State) {
			nextState = current.State
			terminalActor = current.TerminalActor
			terminalAt = current.TerminalAt
		}
		if _, err := tx.Exec(`UPDATE envelopes
			SET state = ?, terminal_actor = ?, terminal_at = ?, etag = etag + 1,
			    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
			    updated_by_principal_ref = ?, updated_by_scope_ref = ?
			WHERE uuid = ?`, nextState, terminalActor, terminalAt,
			attr.PrincipalRef, scopeSQL(attr), envelopeUUID); err != nil {
			return fmt.Errorf("failed to advance presented envelope: %w", err)
		}

		payload := map[string]interface{}{
			"member_ref": record.MemberRef,
			"room_uuid":  current.RoomUUID,
			"state":      string(nextState),
		}
		for key, value := range map[string]*string{
			"node": record.Node, "runtime_id": record.RuntimeID,
			"host_session_id": record.HostSessionID, "generation": record.Generation,
			"run_id": record.RunID, "drive_attempt_id": record.DriveAttemptID,
		} {
			if value != nil {
				payload[key] = *value
			}
		}
		if _, err := logEnvelopeEvent(tx, ew, attr, envelopeUUID, "envelope.presented", current.ETag+1, payload); err != nil {
			return err
		}
		if nextState == domain.EnvelopeStateAcked && current.State != domain.EnvelopeStateAcked {
			if _, err := logEnvelopeEvent(tx, ew, attr, envelopeUUID, "envelope.acked", current.ETag+1, map[string]interface{}{
				"state": string(domain.EnvelopeStateAcked), "reason": "fyi_presented",
				"room_uuid": current.RoomUUID,
			}); err != nil {
				return err
			}
		}
		if err := touchRoomActivity(tx, current.RoomUUID); err != nil {
			return err
		}
		updated, err = getEnvelopeTx(tx, envelopeUUID)
		return err
	})
	if err == nil && afterCommit != nil {
		err = afterCommit
	}
	return updated, recorded, err
}

// MaxEnvelopeFailureDetailBytes bounds the optional failure detail carried on
// the envelope.failed event. Longer detail is truncated, never rejected.
const MaxEnvelopeFailureDetailBytes = 2048

// FailEnvelopeWithAttribution ends one obligation unsuccessfully. When runtime
// is supplied it must own the newest presentation receipt; repeating the same
// (envelope, runtime) failure is an idempotent read of the terminal row. A
// non-empty detail rides the envelope.failed event payload as `detail`.
func (rs *RoomStore) FailEnvelopeWithAttribution(attr attribution.Attribution, envelopeUUID string, reason domain.EnvelopeFailureReason, runtime string, detail string) (*domain.Envelope, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	if err := domain.ValidateEnvelopeFailureReason(reason); err != nil {
		return nil, err
	}

	var updated *domain.Envelope
	err := rs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		current, err := getEnvelopeTx(tx, envelopeUUID)
		if err != nil {
			return err
		}
		runtime = strings.TrimSpace(runtime)
		if runtime != "" {
			var newest sql.NullString
			err := tx.QueryRow(`SELECT runtime_id FROM envelope_presentations
				WHERE envelope_uuid = ? ORDER BY presented_at DESC, rowid DESC LIMIT 1`, envelopeUUID).Scan(&newest)
			if err == sql.ErrNoRows {
				return &EnvelopeRuntimeMismatchError{Envelope: current.ID, Runtime: runtime}
			}
			if err != nil {
				return fmt.Errorf("failed to read newest envelope presentation: %w", err)
			}
			if !newest.Valid || newest.String != runtime {
				return &EnvelopeRuntimeMismatchError{Envelope: current.ID, Runtime: runtime}
			}
		}
		if current.State == domain.EnvelopeStateFailed {
			updated = current
			return nil
		}
		if domain.IsEnvelopeTerminal(current.State) {
			return &EnvelopeWrongStateError{Envelope: current.ID, State: current.State, Verb: "fail"}
		}
		if current.State != domain.EnvelopeStatePending && current.State != domain.EnvelopeStatePresented {
			return &EnvelopeWrongStateError{Envelope: current.ID, State: current.State, Verb: "fail"}
		}
		updated, err = failEnvelopeTx(tx, ew, attr, current, reason, attr.PrincipalRef, runtime, detail)
		return err
	})
	return updated, err
}

// failEnvelopeTx writes failed with its reason and terminal metadata and emits
// exactly one envelope.failed. Callers own the state guard.
func failEnvelopeTx(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, current *domain.Envelope, reason domain.EnvelopeFailureReason, terminalActor, runtime, detail string) (*domain.Envelope, error) {
	now, err := serverNowTx(tx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE envelopes
		SET state = 'failed', failure_reason = ?, terminal_actor = ?, terminal_at = ?,
		    etag = etag + 1,
		    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
		    updated_by_principal_ref = ?, updated_by_scope_ref = ?
		WHERE uuid = ?`, reason, terminalActor, now,
		attr.PrincipalRef, scopeSQL(attr), current.UUID); err != nil {
		return nil, fmt.Errorf("failed to fail envelope: %w", err)
	}
	payload := map[string]interface{}{
		"state": "failed", "reason": string(reason), "room_uuid": current.RoomUUID,
	}
	if runtime != "" {
		payload["runtime_id"] = runtime
	}
	if detail = truncateUTF8(strings.TrimSpace(detail), MaxEnvelopeFailureDetailBytes); detail != "" {
		payload["detail"] = detail
	}
	if _, err := logEnvelopeEvent(tx, ew, attr, current.UUID, "envelope.failed", current.ETag+1, payload); err != nil {
		return nil, err
	}
	return getEnvelopeTx(tx, current.UUID)
}

// truncateUTF8 cuts s to at most max bytes without splitting a rune.
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
