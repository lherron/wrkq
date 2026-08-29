package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/webhooks"
)

// RoomStore persists the wrkq collaboration ledger: rooms, envelopes, members,
// and presentation receipts. It has NO dependency on hrc — every HRC identifier
// it stores is an opaque string — so the whole surface works with every HRC
// daemon down. (T-07612 §2, invariant wrkq.collaboration-ledger.authority.)
type RoomStore struct {
	store *Store
}

// RoomNotFoundError identifies a missing room without coupling callers to
// store error strings.
type RoomNotFoundError struct{ Selector string }

func (e *RoomNotFoundError) Error() string {
	return fmt.Sprintf("room not found: %s", e.Selector)
}

// EnvelopeNotFoundError identifies a missing envelope.
type EnvelopeNotFoundError struct{ Selector string }

func (e *EnvelopeNotFoundError) Error() string {
	return fmt.Sprintf("envelope not found: %s", e.Selector)
}

// EnvelopeWrongStateError identifies a disposition verb attempted on an
// envelope that has already reached a terminal state.
type EnvelopeWrongStateError struct {
	Envelope string
	State    domain.EnvelopeState
	Verb     string
}

func (e *EnvelopeWrongStateError) Error() string {
	return fmt.Sprintf("cannot %s envelope %s in state %s", e.Verb, e.Envelope, e.State)
}

// roomColumns deliberately omits state / closed_at / reopened_at: rooms have no
// lifecycle after the T-07612 rev 3 amendment, and reading a column nothing may
// act on is how a dropped gate grows back. Wave 5 drops them from the schema.
const roomColumns = `
	uuid, id, kind, task_uuid, container_uuid,
	last_activity_at, opened_by_principal_ref, opened_at, meta,
	etag, created_at, updated_at,
	created_by_principal_ref, created_by_scope_ref,
	updated_by_principal_ref, updated_by_scope_ref`

const envelopeColumns = `
	uuid, id, room_uuid, group_id, from_principal_ref, from_scope_ref,
	to_scope_ref, to_principal_ref, obligation, body, task_uuid, state,
	round_count, retry_at, defer_reason, terminal_actor, terminal_at,
	materialization_intent, respond_to_principal_ref, retry_promise_uuid,
	idempotency_key, meta, etag, created_at, updated_at,
	created_by_principal_ref, created_by_scope_ref,
	updated_by_principal_ref, updated_by_scope_ref`

const roomMemberColumns = `
	uuid, room_uuid, member_ref, member_principal_ref, scoped, source,
	joined_at, left_at`

const envelopePresentationColumns = `
	uuid, envelope_uuid, room_uuid, member_ref, node, runtime_id,
	host_session_id, generation, run_id, drive_attempt_id, input_id, delivery_outcome,
	presented_at, presented_by_principal_ref`

// ─── rooms ────────────────────────────────────────────────────────────────────

// RoomCreateParams carries the durable fields accepted at room creation. The
// caller has already resolved the work anchor and enforced the kind rules.
type RoomCreateParams struct {
	Kind          domain.RoomKind
	TaskUUID      *string
	ContainerUUID *string
	Members       []RoomMemberSeed
}

// RoomMemberSeed is one membership recorded alongside a room mutation.
type RoomMemberSeed struct {
	MemberRef          string
	MemberPrincipalRef string
	Scoped             bool
	Source             domain.RoomMemberSource
}

// CreateWithAttribution opens a room and emits room.opened plus one
// member.joined per seeded member.
func (rs *RoomStore) CreateWithAttribution(attr attribution.Attribution, params RoomCreateParams) (*domain.Room, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	if err := domain.ValidateRoomKind(params.Kind); err != nil {
		return nil, err
	}

	var created *domain.Room
	err := rs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// id stays NULL: only ad-hoc rooms mint an R- id, and the friendly-id
		// trigger fires on NULL. A derived room's key IS its work identity, and
		// SQLite lets the UNIQUE index hold as many NULLs as there are of them.
		res, err := tx.Exec(`INSERT INTO rooms (
			kind, task_uuid, container_uuid, opened_by_principal_ref,
			created_by_principal_ref, created_by_scope_ref,
			updated_by_principal_ref, updated_by_scope_ref
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			params.Kind, params.TaskUUID, params.ContainerUUID,
			attr.PrincipalRef, attr.PrincipalRef, scopeSQL(attr), attr.PrincipalRef, scopeSQL(attr))
		if err != nil {
			return fmt.Errorf("failed to create room: %w", err)
		}
		rowID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("failed to read room row id: %w", err)
		}
		created, err = scanRoom(tx.QueryRow("SELECT "+roomColumns+" FROM rooms WHERE rowid = ?", rowID))
		if err != nil {
			return fmt.Errorf("failed to read created room: %w", err)
		}

		payload := map[string]interface{}{"kind": string(created.Kind)}
		if created.ID != nil {
			payload["id"] = *created.ID
		}
		if created.TaskUUID != nil {
			payload["task_uuid"] = *created.TaskUUID
		}
		if created.ContainerUUID != nil {
			payload["container_uuid"] = *created.ContainerUUID
		}
		if _, err := logRoomEvent(tx, ew, attr, created.UUID, "room.opened", created.ETag, payload); err != nil {
			return err
		}
		for _, member := range params.Members {
			if _, err := upsertRoomMemberTx(tx, ew, attr, created.UUID, member); err != nil {
				return err
			}
		}
		return nil
	})
	return created, err
}

// Get resolves a room by UUID or R- friendly ID.
func (rs *RoomStore) Get(selector string) (*domain.Room, error) {
	room, err := scanRoom(rs.store.db.QueryRow(
		"SELECT "+roomColumns+" FROM rooms WHERE uuid = ? OR id = ?", selector, selector,
	))
	if err == sql.ErrNoRows {
		return nil, &RoomNotFoundError{Selector: selector}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get room: %w", err)
	}
	return room, nil
}

// GetByTask returns the room anchored on one task, or nil when none exists.
func (rs *RoomStore) GetByTask(taskUUID string) (*domain.Room, error) {
	return rs.getByAnchor("task_uuid", taskUUID)
}

// GetByContainer returns the room anchored on one container, or nil when none
// exists.
func (rs *RoomStore) GetByContainer(containerUUID string) (*domain.Room, error) {
	return rs.getByAnchor("container_uuid", containerUUID)
}

func (rs *RoomStore) getByAnchor(column, value string) (*domain.Room, error) {
	room, err := scanRoom(rs.store.db.QueryRow(
		"SELECT "+roomColumns+" FROM rooms WHERE "+column+" = ?", value,
	))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get room by %s: %w", column, err)
	}
	return room, nil
}

// FindAdhocPairRoom returns the ad-hoc room whose ACTIVE member set is exactly
// the supplied pair and whose activity reads `active`. A third member joining
// makes it a group room, so it stops matching and the next unsolicited pair say
// opens a fresh one; a pair room that has gone quiet does the same, which is
// what replaced the idle auto-archive the rev-3 amendment removed.
//
// activeSince is the `active` cutoff (now − RoomActiveWithin). The clock is the
// SAME three-way max the read-time projection uses — opened_at folded with the
// newest envelope and the newest join — so a room this reuses is a room
// `wrkc show` calls active, including a store-created room with no envelope.
func (rs *RoomStore) FindAdhocPairRoom(memberA, memberB, activeSince string) (*domain.Room, error) {
	room, err := scanRoom(rs.store.db.QueryRow(`
		SELECT `+roomColumns+` FROM rooms r
		 WHERE r.kind = 'adhoc'
		   AND (SELECT COUNT(*) FROM room_members m
		         WHERE m.room_uuid = r.uuid AND m.left_at IS NULL) = 2
		   AND EXISTS (SELECT 1 FROM room_members m
		                WHERE m.room_uuid = r.uuid AND m.left_at IS NULL AND m.member_ref = ?)
		   AND EXISTS (SELECT 1 FROM room_members m
		                WHERE m.room_uuid = r.uuid AND m.left_at IS NULL AND m.member_ref = ?)
		   AND MAX(
		         r.opened_at,
		         COALESCE((SELECT MAX(e.created_at) FROM envelopes e WHERE e.room_uuid = r.uuid), ''),
		         COALESCE((SELECT MAX(m.joined_at) FROM room_members m WHERE m.room_uuid = r.uuid), '')
		       ) > ?
		 ORDER BY r.last_activity_at DESC, r.id DESC
		 LIMIT 1`, memberA, memberB, activeSince))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to find ad-hoc pair room: %w", err)
	}
	return room, nil
}

// RoomListParams selects rooms without imposing read restrictions: rooms are
// readable by any principal and membership is not an ACL.
type RoomListParams struct {
	Kind      domain.RoomKind
	MemberRef string
}

// List returns rooms ordered by most recent activity.
func (rs *RoomStore) List(params RoomListParams) ([]domain.Room, error) {
	clauses := []string{"1 = 1"}
	args := []interface{}{}
	if params.Kind != "" {
		if err := domain.ValidateRoomKind(params.Kind); err != nil {
			return nil, err
		}
		clauses = append(clauses, "r.kind = ?")
		args = append(args, params.Kind)
	}
	if strings.TrimSpace(params.MemberRef) != "" {
		clauses = append(clauses, `EXISTS (SELECT 1 FROM room_members m
			WHERE m.room_uuid = r.uuid AND m.left_at IS NULL AND m.member_ref = ?)`)
		args = append(args, params.MemberRef)
	}
	return rs.queryRooms("SELECT "+roomColumns+" FROM rooms r WHERE "+
		strings.Join(clauses, " AND ")+" ORDER BY r.last_activity_at DESC, r.uuid", args...)
}

// SetRoomLabelWithAttribution sets or clears an operator label on a room and
// emits room.hidden / room.unhidden. A label is not a state: it changes what the
// DEFAULT listing shows and nothing else — the room still accepts says, and its
// obligations gate and wake unchanged. Any principal may set it; discovery is
// not an ownership boundary.
func (rs *RoomStore) SetRoomLabelWithAttribution(attr attribution.Attribution, roomUUID, label string, on bool) (*domain.Room, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	eventType := "room.unhidden"
	if on {
		eventType = "room.hidden"
	}

	var updated *domain.Room
	err := rs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		current, err := getRoomTx(tx, roomUUID)
		if err != nil {
			return err
		}
		if domain.RoomHasLabel(current.Labels, label) == on {
			updated = current
			return nil
		}
		meta, err := roomMetaWithLabel(current.Meta, label, on)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE rooms
			SET meta = ?, etag = etag + 1,
			    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
			    updated_by_principal_ref = ?, updated_by_scope_ref = ?
			WHERE uuid = ?`, meta, attr.PrincipalRef, scopeSQL(attr), roomUUID); err != nil {
			return fmt.Errorf("failed to set room label: %w", err)
		}
		payload := map[string]interface{}{"label": label}
		if _, err := logRoomEvent(tx, ew, attr, roomUUID, eventType, current.ETag+1, payload); err != nil {
			return err
		}
		updated, err = getRoomTx(tx, roomUUID)
		return err
	})
	return updated, err
}

// roomLabelsFromMeta reads the operator labels out of a room's meta blob. Room
// labels ride meta rather than a column of their own: `hidden` is the only one
// the rev-3 amendment mints, and it changes no query plan worth an index.
func roomLabelsFromMeta(meta *string) []string {
	if meta == nil || strings.TrimSpace(*meta) == "" {
		return nil
	}
	var decoded struct {
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(*meta), &decoded); err != nil {
		return nil
	}
	return decoded.Labels
}

func roomMetaWithLabel(meta *string, label string, on bool) (string, error) {
	decoded := map[string]interface{}{}
	if meta != nil && strings.TrimSpace(*meta) != "" {
		if err := json.Unmarshal([]byte(*meta), &decoded); err != nil {
			return "", fmt.Errorf("failed to parse room meta: %w", err)
		}
	}
	labels := []string{}
	for _, existing := range roomLabelsFromMeta(meta) {
		if existing != label {
			labels = append(labels, existing)
		}
	}
	if on {
		labels = append(labels, label)
	}
	if len(labels) == 0 {
		delete(decoded, "labels")
	} else {
		decoded["labels"] = labels
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return "", fmt.Errorf("failed to encode room meta: %w", err)
	}
	return string(encoded), nil
}

// ─── members ──────────────────────────────────────────────────────────────────

// AddMemberWithAttribution records a membership and emits member.joined the
// first time that member appears. Re-adding an active member is idempotent and
// keeps the original source: attendance is the live signal, not the source.
func (rs *RoomStore) AddMemberWithAttribution(attr attribution.Attribution, roomUUID string, seed RoomMemberSeed) (*domain.RoomMember, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	var member *domain.RoomMember
	err := rs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		var err error
		member, err = upsertRoomMemberTx(tx, ew, attr, roomUUID, seed)
		return err
	})
	return member, err
}

// RemoveMemberWithAttribution marks a member as having left and emits
// member.left. Leaving is not a delete: the attendance record stays readable.
func (rs *RoomStore) RemoveMemberWithAttribution(attr attribution.Attribution, roomUUID, memberRef string) error {
	if err := requireAttribution(attr); err != nil {
		return err
	}
	return rs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		res, err := tx.Exec(`UPDATE room_members
			SET left_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
			WHERE room_uuid = ? AND member_ref = ? AND left_at IS NULL`, roomUUID, memberRef)
		if err != nil {
			return fmt.Errorf("failed to remove room member: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return nil
		}
		_, err = logRoomEvent(tx, ew, attr, roomUUID, "member.left", 0, map[string]interface{}{
			"member_ref": memberRef,
		})
		return err
	})
}

// ListMembers returns every membership of a room, departed members included,
// ordered by join time.
func (rs *RoomStore) ListMembers(roomUUID string) ([]domain.RoomMember, error) {
	rows, err := rs.store.db.Query("SELECT "+roomMemberColumns+
		" FROM room_members WHERE room_uuid = ? ORDER BY joined_at, member_ref", roomUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query room members: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := []domain.RoomMember{}
	for rows.Next() {
		member, err := scanRoomMember(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan room member: %w", err)
		}
		result = append(result, *member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate room members: %w", err)
	}
	return result, nil
}

// LatestAttendance returns the most recent presentation receipt per member of a
// room. Scope-less members never appear: they have no attendance.
func (rs *RoomStore) LatestAttendance(roomUUID string) (map[string]domain.EnvelopePresentation, error) {
	rows, err := rs.store.db.Query(`SELECT `+envelopePresentationColumns+`
		FROM envelope_presentations p
		WHERE p.room_uuid = ?
		  AND p.presented_at = (SELECT MAX(q.presented_at) FROM envelope_presentations q
		                         WHERE q.room_uuid = p.room_uuid AND q.member_ref = p.member_ref)
		ORDER BY p.member_ref, p.uuid`, roomUUID)
	if err != nil {
		return nil, fmt.Errorf("failed to query room attendance: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := map[string]domain.EnvelopePresentation{}
	for rows.Next() {
		presentation, err := scanEnvelopePresentation(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan attendance: %w", err)
		}
		result[presentation.MemberRef] = *presentation
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate attendance: %w", err)
	}
	return result, nil
}

// HasRuntimeSeenRoom reports whether one HRC runtime has already been presented
// anything in this room. It backs the §7 `history:` cue, which is keyed to the
// RUNTIME and not the generation: /quit clears continuation without rotating
// the generation, so every post-quit runtime is cold and gets the cue.
func (rs *RoomStore) HasRuntimeSeenRoom(roomUUID, runtimeID string) (bool, error) {
	var seen int
	err := rs.store.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM envelope_presentations
		WHERE room_uuid = ? AND runtime_id = ?)`, roomUUID, runtimeID).Scan(&seen)
	if err != nil {
		return false, fmt.Errorf("failed to check runtime room history: %w", err)
	}
	return seen == 1, nil
}

// ─── envelopes ────────────────────────────────────────────────────────────────

// EnvelopeAddressee is one resolved recipient of a say. ScopeRef is empty for a
// scope-less principal (a human), which is never kicked or summoned.
type EnvelopeAddressee struct {
	ScopeRef              string
	PrincipalRef          string
	MaterializationIntent *string
}

// EnvelopeCreateParams carries one say. Addressees empty means obligation none:
// a log entry that fires nothing.
type EnvelopeCreateParams struct {
	RoomUUID              string
	FromPrincipalRef      string
	FromScopeRef          *string
	Addressees            []EnvelopeAddressee
	Obligation            domain.EnvelopeObligation
	Body                  string
	TaskUUID              *string
	RespondToPrincipalRef *string
	IdempotencyKey        *string
	Meta                  *string
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

	var created []domain.Envelope
	err := rs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		created = nil
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
				id, room_uuid, from_principal_ref, from_scope_ref, to_scope_ref,
				to_principal_ref, obligation, body, task_uuid, state,
				materialization_intent, respond_to_principal_ref, idempotency_key,
				meta, terminal_actor, terminal_at,
				created_by_principal_ref, created_by_scope_ref,
				updated_by_principal_ref, updated_by_scope_ref
			) VALUES ('', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				params.RoomUUID, params.FromPrincipalRef, params.FromScopeRef, toScope,
				toPrincipal, params.Obligation, params.Body, params.TaskUUID, state,
				intent, params.RespondToPrincipalRef, idempotencyKey, params.Meta,
				terminalActor, terminalAt,
				attr.PrincipalRef, scopeSQL(attr), attr.PrincipalRef, scopeSQL(attr))
			if err != nil {
				return fmt.Errorf("failed to create envelope: %w", err)
			}
			rowID, err := res.LastInsertId()
			if err != nil {
				return fmt.Errorf("failed to read envelope row id: %w", err)
			}
			envelope, err := scanEnvelope(tx.QueryRow("SELECT "+envelopeColumns+" FROM envelopes WHERE rowid = ?", rowID))
			if err != nil {
				return fmt.Errorf("failed to read created envelope: %w", err)
			}
			// group_id equals the FIRST envelope's own id, so a single addressee
			// groups with itself and a fan-out shares one waitable handle.
			if groupID == "" {
				groupID = envelope.ID
			}
			if _, err := tx.Exec("UPDATE envelopes SET group_id = ? WHERE uuid = ?", groupID, envelope.UUID); err != nil {
				return fmt.Errorf("failed to stamp envelope group: %w", err)
			}
			envelope.GroupID = &groupID

			if params.Obligation != domain.EnvelopeObligationNone {
				if _, err := upsertRoomMemberTx(tx, ew, attr, params.RoomUUID, RoomMemberSeed{
					MemberRef:          addresseeMemberRef(addressee),
					MemberPrincipalRef: addressee.PrincipalRef,
					Scoped:             strings.TrimSpace(addressee.ScopeRef) != "",
					Source:             domain.RoomMemberSourceAddressed,
				}); err != nil {
					return err
				}
			}

			payload := envelopeEventPayload(envelope)
			if _, err := logEnvelopeEvent(tx, ew, attr, envelope.UUID, "envelope.created", envelope.ETag, payload); err != nil {
				return err
			}
			created = append(created, *envelope)
		}

		if _, err := tx.Exec(`UPDATE rooms
			SET last_activity_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
			WHERE uuid = ?`, params.RoomUUID); err != nil {
			return fmt.Errorf("failed to touch room activity: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for index := range created {
		webhooks.DispatchEnvelopeEvent(rs.store.db, created[index], "envelope.created", envelopeEventPayload(&created[index]), attr.PrincipalRef)
	}
	return created, nil
}

// GetEnvelope resolves an envelope by UUID or EN- friendly ID.
func (rs *RoomStore) GetEnvelope(selector string) (*domain.Envelope, error) {
	envelope, err := scanEnvelope(rs.store.db.QueryRow(
		"SELECT "+envelopeColumns+" FROM envelopes WHERE uuid = ? OR id = ?", selector, selector,
	))
	if err == sql.ErrNoRows {
		return nil, &EnvelopeNotFoundError{Selector: selector}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get envelope: %w", err)
	}
	return envelope, nil
}

// EnvelopeListParams selects envelopes for `wrkc log`, `wrkc inbox`, and the
// HRC-facing wake set.
type EnvelopeListParams struct {
	RoomUUID   string
	GroupID    string
	ToScopeRef string
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
}

// ListEnvelopes returns envelopes ordered by insertion (id) unless the caller
// asked for the newest first.
func (rs *RoomStore) ListEnvelopes(params EnvelopeListParams) ([]domain.Envelope, error) {
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
		clauses = append(clauses, "task_uuid = ?")
		args = append(args, params.TaskUUID)
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

	order := "ORDER BY id"
	if params.NewestFirst {
		order = "ORDER BY id DESC"
	}
	query := "SELECT " + envelopeColumns + " FROM envelopes WHERE " + strings.Join(clauses, " AND ") + " " + order
	if params.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", params.Limit)
	}
	return rs.queryEnvelopes(query, args...)
}

// CountEnvelopes returns how many envelopes match, without materializing them.
// It is the shape the stop-hook predicate wants.
func (rs *RoomStore) CountEnvelopes(params EnvelopeListParams) (int, error) {
	rows, err := rs.ListEnvelopes(params)
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
	rows, err := rs.ListEnvelopes(EnvelopeListParams{
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

// RependDueDeferrals returns deferred envelopes whose retry time has arrived to
// pending so the kicker's next sweep re-drives them, and resolves the promise
// that was carrying the deferral. This is a DERIVED transition back to the
// pre-defer state, so it emits no event: the kicker wakes on its periodic
// sweep, exactly as the spec routes it.
func (rs *RoomStore) RependDueDeferrals(attr attribution.Attribution) (int, error) {
	if err := requireAttribution(attr); err != nil {
		return 0, err
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
				if _, err := tx.Exec(`UPDATE promises
					SET state = 'resolved',
					    closed_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
					    last_reviewed_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
					    last_review_note = 'deferred envelope re-pended',
					    etag = etag + 1,
					    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
					    updated_by_principal_ref = ?, updated_by_scope_ref = ?
					WHERE uuid = ? AND state = 'open'`, attr.PrincipalRef, scopeSQL(attr), item.promise.String); err != nil {
					return fmt.Errorf("failed to resolve deferral promise: %w", err)
				}
			}
			repended++
		}
		return nil
	})
	return repended, err
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
	eventType := map[domain.EnvelopeState]string{
		domain.EnvelopeStateAcked:    "envelope.acked",
		domain.EnvelopeStateDeferred: "envelope.deferred",
		domain.EnvelopeStateDead:     "envelope.dead",
	}[disposition.State]
	if eventType == "" {
		return nil, fmt.Errorf("envelope disposition %q is not a terminal or paused state", disposition.State)
	}

	var updated *domain.Envelope
	err := rs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		current, err := getEnvelopeTx(tx, envelopeUUID)
		if err != nil {
			return err
		}
		if err := checkETag(current.ETag, ifMatch); err != nil {
			return err
		}
		if domain.IsEnvelopeTerminal(current.State) {
			// First terminal disposition wins; an identical retry is a no-op.
			if current.State == disposition.State {
				updated = current
				return nil
			}
			return &EnvelopeWrongStateError{Envelope: current.ID, State: current.State, Verb: string(disposition.State)}
		}
		var terminalActor, terminalAt interface{}
		if domain.IsEnvelopeTerminal(disposition.State) {
			terminalActor = attr.PrincipalRef
			now, nerr := serverNowTx(tx)
			if nerr != nil {
				return nerr
			}
			terminalAt = now
		}
		if _, err := tx.Exec(`UPDATE envelopes
			SET state = ?, defer_reason = ?, retry_at = ?, retry_promise_uuid = ?,
			    terminal_actor = ?, terminal_at = ?, etag = etag + 1,
			    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
			    updated_by_principal_ref = ?, updated_by_scope_ref = ?
			WHERE uuid = ?`,
			disposition.State, disposition.DeferReason, disposition.RetryAt,
			disposition.RetryPromiseUUID, terminalActor, terminalAt,
			attr.PrincipalRef, scopeSQL(attr), envelopeUUID); err != nil {
			return fmt.Errorf("failed to dispose envelope: %w", err)
		}
		payload := map[string]interface{}{
			"state":          string(disposition.State),
			"previous_state": string(current.State),
			"room_uuid":      current.RoomUUID,
		}
		if disposition.DeferReason != nil {
			payload["defer_reason"] = *disposition.DeferReason
		}
		if disposition.RetryAt != nil {
			payload["retry_at"] = *disposition.RetryAt
		}
		if disposition.Reason != "" {
			payload["reason"] = disposition.Reason
		}
		if _, err := logEnvelopeEvent(tx, ew, attr, envelopeUUID, eventType, current.ETag+1, payload); err != nil {
			return err
		}
		updated, err = getEnvelopeTx(tx, envelopeUUID)
		return err
	})
	return updated, err
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
	if strings.TrimSpace(replierScopeRef) != "" {
		clauses = append(clauses, "to_scope_ref = ?")
		args = append(args, replierScopeRef)
	} else {
		clauses = append(clauses, "to_scope_ref IS NULL AND to_principal_ref = ?")
		args = append(args, replierPrincipalRef)
	}
	// "Most recent" is the most recent PRESENTATION, not the most recent send: a
	// seat answers what it was last shown. An envelope in state presented always
	// has a presentation row; the id tiebreak keeps the order total anyway.
	return rs.queryEnvelopes("SELECT "+envelopeColumns+" FROM envelopes e WHERE "+
		strings.Join(clauses, " AND ")+` ORDER BY (
			SELECT MAX(p.presented_at) FROM envelope_presentations p WHERE p.envelope_uuid = e.uuid
		) DESC, e.id DESC`, args...)
}

// AckSenderObligationsWithAttribution is the reply-is-ack rule: saying into a
// room with --to X acks every PRESENTED reply_required envelope in that room
// addressed to the replier's own scope and sent from X's scope. Both sides
// match on the SCOPE, never on a principal: a member IS a scope and its
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
		"state = 'presented'",
	}
	args := []interface{}{roomUUID}
	if strings.TrimSpace(counterpartyScopeRef) != "" {
		clauses = append(clauses, "from_scope_ref = ?")
		args = append(args, counterpartyScopeRef)
	} else {
		clauses = append(clauses, "from_scope_ref IS NULL AND from_principal_ref = ?")
		args = append(args, counterpartyPrincipalRef)
	}
	if strings.TrimSpace(replierScopeRef) != "" {
		clauses = append(clauses, "to_scope_ref = ?")
		args = append(args, replierScopeRef)
	} else {
		clauses = append(clauses, "to_scope_ref IS NULL AND to_principal_ref = ?")
		args = append(args, replierPrincipalRef)
	}

	candidates, err := rs.queryEnvelopes("SELECT "+envelopeColumns+" FROM envelopes WHERE "+
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
		if _, err := tx.Exec(`UPDATE rooms
			SET last_activity_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
			WHERE uuid = ?`, current.RoomUUID); err != nil {
			return fmt.Errorf("failed to touch room activity: %w", err)
		}
		updated, err = getEnvelopeTx(tx, envelopeUUID)
		return err
	})
	return updated, recorded, err
}

// RecordRoundWithAttribution advances the round counter for an envelope a
// completed kicker turn presented but left undisposed, dead-lettering it
// visibly at the bound. Only a still-presented envelope advances: a clear-inbox
// no-op turn and an acked or deferred envelope never do.
func (rs *RoomStore) RecordRoundWithAttribution(attr attribution.Attribution, envelopeUUID string, maxRounds int64) (*domain.Envelope, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	if maxRounds <= 0 {
		maxRounds = domain.DefaultEnvelopeMaxRounds
	}

	var updated *domain.Envelope
	err := rs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		current, err := getEnvelopeTx(tx, envelopeUUID)
		if err != nil {
			return err
		}
		if current.State != domain.EnvelopeStatePresented {
			updated = current
			return nil
		}
		nextRound := current.RoundCount + 1
		nextState := domain.EnvelopeStatePresented
		eventType := ""
		var terminalActor, terminalAt interface{}
		if nextRound >= maxRounds {
			nextState = domain.EnvelopeStateDead
			eventType = "envelope.dead"
			terminalActor = attr.PrincipalRef
			now, nerr := serverNowTx(tx)
			if nerr != nil {
				return nerr
			}
			terminalAt = now
		}
		if _, err := tx.Exec(`UPDATE envelopes
			SET round_count = ?, state = ?, terminal_actor = ?, terminal_at = ?,
			    etag = etag + 1,
			    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
			    updated_by_principal_ref = ?, updated_by_scope_ref = ?
			WHERE uuid = ?`, nextRound, nextState, terminalActor, terminalAt,
			attr.PrincipalRef, scopeSQL(attr), envelopeUUID); err != nil {
			return fmt.Errorf("failed to advance envelope round: %w", err)
		}
		if eventType != "" {
			if _, err := logEnvelopeEvent(tx, ew, attr, envelopeUUID, eventType, current.ETag+1, map[string]interface{}{
				"state": string(nextState), "round_count": nextRound,
				"max_rounds": maxRounds, "room_uuid": current.RoomUUID,
			}); err != nil {
				return err
			}
		}
		updated, err = getEnvelopeTx(tx, envelopeUUID)
		return err
	})
	return updated, err
}

// TouchRoomActivity records that a room saw activity, keeping ad-hoc idle
// archival honest when membership changes without a say.
func (rs *RoomStore) TouchRoomActivity(roomUUID string) error {
	_, err := rs.store.db.Exec(`UPDATE rooms
		SET last_activity_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE uuid = ?`, roomUUID)
	return err
}

// ─── internals ────────────────────────────────────────────────────────────────

func addresseeMemberRef(addressee EnvelopeAddressee) string {
	if ref := strings.TrimSpace(addressee.ScopeRef); ref != "" {
		return ref
	}
	return addressee.PrincipalRef
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
	if envelope.ToScopeRef != nil {
		payload["to_scope_ref"] = *envelope.ToScopeRef
	}
	if envelope.ToPrincipalRef != nil {
		payload["to_principal_ref"] = *envelope.ToPrincipalRef
	}
	if envelope.TaskUUID != nil {
		payload["task_uuid"] = *envelope.TaskUUID
	}
	if envelope.MaterializationIntent != nil {
		payload["materialization_intent"] = *envelope.MaterializationIntent
	}
	return payload
}

func upsertRoomMemberTx(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, roomUUID string, seed RoomMemberSeed) (*domain.RoomMember, error) {
	if strings.TrimSpace(seed.MemberRef) == "" {
		return nil, fmt.Errorf("room member ref is required")
	}
	existing, err := scanRoomMember(tx.QueryRow("SELECT "+roomMemberColumns+
		" FROM room_members WHERE room_uuid = ? AND member_ref = ?", roomUUID, seed.MemberRef))
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to read room member: %w", err)
	}
	if err == nil {
		// Attendance stays current: the row's principal is whoever last SPOKE
		// from this seat. It is attribution only — no address resolves through
		// it — so refreshing it can never move an obligation (T-07628).
		if seed.Source == domain.RoomMemberSourceSpoke &&
			strings.TrimSpace(seed.MemberPrincipalRef) != "" &&
			seed.MemberPrincipalRef != existing.MemberPrincipalRef {
			if _, err := tx.Exec(`UPDATE room_members SET member_principal_ref = ?
				WHERE room_uuid = ? AND member_ref = ?`,
				seed.MemberPrincipalRef, roomUUID, seed.MemberRef); err != nil {
				return nil, fmt.Errorf("failed to refresh room member principal: %w", err)
			}
			existing.MemberPrincipalRef = seed.MemberPrincipalRef
		}
		if existing.LeftAt == nil {
			return existing, nil
		}
		// Rejoining is a fresh join: attendance resumes and the ledger says so.
		if _, err := tx.Exec(`UPDATE room_members
			SET left_at = NULL, source = ?,
			    joined_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
			WHERE room_uuid = ? AND member_ref = ?`, seed.Source, roomUUID, seed.MemberRef); err != nil {
			return nil, fmt.Errorf("failed to rejoin room member: %w", err)
		}
	} else {
		scoped := 0
		if seed.Scoped {
			scoped = 1
		}
		if _, err := tx.Exec(`INSERT INTO room_members (
			room_uuid, member_ref, member_principal_ref, scoped, source
		) VALUES (?, ?, ?, ?, ?)`, roomUUID, seed.MemberRef, seed.MemberPrincipalRef, scoped, seed.Source); err != nil {
			return nil, fmt.Errorf("failed to add room member: %w", err)
		}
	}

	if _, err := logRoomEvent(tx, ew, attr, roomUUID, "member.joined", 0, map[string]interface{}{
		"member_ref":           seed.MemberRef,
		"member_principal_ref": seed.MemberPrincipalRef,
		"scoped":               seed.Scoped,
		"source":               string(seed.Source),
	}); err != nil {
		return nil, err
	}
	return scanRoomMember(tx.QueryRow("SELECT "+roomMemberColumns+
		" FROM room_members WHERE room_uuid = ? AND member_ref = ?", roomUUID, seed.MemberRef))
}

func (rs *RoomStore) queryRooms(query string, args ...interface{}) ([]domain.Room, error) {
	rows, err := rs.store.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query rooms: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := []domain.Room{}
	for rows.Next() {
		room, err := scanRoom(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan room: %w", err)
		}
		result = append(result, *room)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate rooms: %w", err)
	}
	return result, nil
}

func (rs *RoomStore) queryEnvelopes(query string, args ...interface{}) ([]domain.Envelope, error) {
	rows, err := rs.store.db.Query(query, args...)
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

type collabScanner interface{ Scan(...interface{}) error }

func scanRoom(scanner collabScanner) (*domain.Room, error) {
	room := &domain.Room{}
	err := scanner.Scan(
		&room.UUID, &room.ID, &room.Kind, &room.TaskUUID, &room.ContainerUUID,
		&room.LastActivityAt, &room.OpenedByPrincipalRef,
		&room.OpenedAt, &room.Meta, &room.ETag, &room.CreatedAt, &room.UpdatedAt,
		&room.CreatedByPrincipalRef, &room.CreatedByScopeRef,
		&room.UpdatedByPrincipalRef, &room.UpdatedByScopeRef,
	)
	if err != nil {
		return room, err
	}
	room.Labels = roomLabelsFromMeta(room.Meta)
	return room, nil
}

func scanEnvelope(scanner collabScanner) (*domain.Envelope, error) {
	envelope := &domain.Envelope{}
	err := scanner.Scan(
		&envelope.UUID, &envelope.ID, &envelope.RoomUUID, &envelope.GroupID,
		&envelope.FromPrincipalRef, &envelope.FromScopeRef, &envelope.ToScopeRef,
		&envelope.ToPrincipalRef, &envelope.Obligation, &envelope.Body,
		&envelope.TaskUUID, &envelope.State, &envelope.RoundCount,
		&envelope.RetryAt, &envelope.DeferReason, &envelope.TerminalActor,
		&envelope.TerminalAt, &envelope.MaterializationIntent,
		&envelope.RespondToPrincipalRef, &envelope.RetryPromiseUUID,
		&envelope.IdempotencyKey, &envelope.Meta, &envelope.ETag,
		&envelope.CreatedAt, &envelope.UpdatedAt,
		&envelope.CreatedByPrincipalRef, &envelope.CreatedByScopeRef,
		&envelope.UpdatedByPrincipalRef, &envelope.UpdatedByScopeRef,
	)
	return envelope, err
}

func scanRoomMember(scanner collabScanner) (*domain.RoomMember, error) {
	member := &domain.RoomMember{}
	err := scanner.Scan(
		&member.UUID, &member.RoomUUID, &member.MemberRef,
		&member.MemberPrincipalRef, &member.Scoped, &member.Source,
		&member.JoinedAt, &member.LeftAt,
	)
	return member, err
}

func scanEnvelopePresentation(scanner collabScanner) (*domain.EnvelopePresentation, error) {
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

func getRoomTx(tx *sql.Tx, uuid string) (*domain.Room, error) {
	room, err := scanRoom(tx.QueryRow("SELECT "+roomColumns+" FROM rooms WHERE uuid = ?", uuid))
	if err == sql.ErrNoRows {
		return nil, &RoomNotFoundError{Selector: uuid}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get room: %w", err)
	}
	return room, nil
}

func getEnvelopeTx(tx *sql.Tx, uuid string) (*domain.Envelope, error) {
	envelope, err := scanEnvelope(tx.QueryRow("SELECT "+envelopeColumns+" FROM envelopes WHERE uuid = ?", uuid))
	if err == sql.ErrNoRows {
		return nil, &EnvelopeNotFoundError{Selector: uuid}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get envelope: %w", err)
	}
	return envelope, nil
}

func serverNowTx(tx *sql.Tx) (string, error) {
	var now string
	if err := tx.QueryRow("SELECT strftime('%Y-%m-%dT%H:%M:%SZ','now')").Scan(&now); err != nil {
		return "", fmt.Errorf("failed to read server time: %w", err)
	}
	return now, nil
}

func logRoomEvent(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, roomUUID, eventType string, etag int64, payload map[string]interface{}) (events.EventMetadata, error) {
	return logCollaborationEvent(tx, ew, attr, "room", roomUUID, eventType, etag, payload)
}

func logEnvelopeEvent(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, envelopeUUID, eventType string, etag int64, payload map[string]interface{}) (events.EventMetadata, error) {
	return logCollaborationEvent(tx, ew, attr, "envelope", envelopeUUID, eventType, etag, payload)
}

func logCollaborationEvent(tx *sql.Tx, ew *events.Writer, attr attribution.Attribution, resourceType, resourceUUID, eventType string, etag int64, payload map[string]interface{}) (events.EventMetadata, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return events.EventMetadata{}, fmt.Errorf("failed to marshal %s payload: %w", eventType, err)
	}
	text := string(encoded)
	event := &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: resourceType,
		ResourceUUID: &resourceUUID,
		EventType:    eventType,
		Payload:      &text,
	}
	if etag > 0 {
		event.ETag = &etag
	}
	metadata, err := ew.LogEventReturning(tx, event)
	if err != nil {
		return events.EventMetadata{}, fmt.Errorf("failed to log %s event: %w", eventType, err)
	}
	return metadata, nil
}
