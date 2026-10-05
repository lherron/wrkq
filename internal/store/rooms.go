package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
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

// roomColumns omits the retained historical state and closed_at columns.
// Rooms have no lifecycle after the T-07612 rev 3 amendment. T-10158 removes
// reopened_at; neither retained history column may gate traffic.
const roomColumns = `
	uuid, id, kind, task_uuid, container_uuid,
	last_activity_at, opened_by_principal_ref, opened_at, meta,
	etag, created_at, updated_at,
	created_by_principal_ref, created_by_scope_ref,
	updated_by_principal_ref, updated_by_scope_ref`

const roomMemberColumns = `
	uuid, room_uuid, member_ref, member_principal_ref, scoped, source,
	joined_at, left_at`

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
	return rs.GetContext(context.Background(), selector)
}

// GetContext is Get under the caller's context, so a cut-off read releases
// its connection (T-09997).
func (rs *RoomStore) GetContext(ctx context.Context, selector string) (*domain.Room, error) {
	room, err := scanRoom(rs.store.db.QueryRowContext(ctx,
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

// TouchRoomActivity records that a room saw activity, keeping ad-hoc idle
// archival honest when membership changes without a say.
func (rs *RoomStore) TouchRoomActivity(roomUUID string) error {
	_, err := rs.store.db.Exec(touchRoomActivitySQL, roomUUID)
	return err
}

const touchRoomActivitySQL = `UPDATE rooms
	SET last_activity_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
	WHERE uuid = ?`

// touchRoomActivity stamps a room's last activity inside a write transaction.
func touchRoomActivity(tx *sql.Tx, roomUUID string) error {
	if _, err := tx.Exec(touchRoomActivitySQL, roomUUID); err != nil {
		return fmt.Errorf("failed to touch room activity: %w", err)
	}
	return nil
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

type collabScanner interface{ Scan(...interface{}) error }

// appendScan scans a row's leading columns through an existing scanner and
// its trailing extra columns into extra.
type appendScan struct {
	collabScanner
	extra interface{}
}

func (a appendScan) Scan(dest ...interface{}) error {
	return a.collabScanner.Scan(append(dest, a.extra)...)
}

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

func scanRoomMember(scanner collabScanner) (*domain.RoomMember, error) {
	member := &domain.RoomMember{}
	err := scanner.Scan(
		&member.UUID, &member.RoomUUID, &member.MemberRef,
		&member.MemberPrincipalRef, &member.Scoped, &member.Source,
		&member.JoinedAt, &member.LeftAt,
	)
	return member, err
}

func qualifySQLColumns(columns, alias string) string {
	parts := strings.Split(columns, ",")
	for index := range parts {
		parts[index] = alias + "." + strings.TrimSpace(parts[index])
	}
	return strings.Join(parts, ", ")
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
