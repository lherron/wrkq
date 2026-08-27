package domain

import (
	"fmt"
	"strings"
	"time"
)

// RoomKind is the work identity a room is keyed by. Every kind but adhoc
// derives its key from a live wrkq resource, so `wrkq monitor watch T-07613`
// shows task state changes and the conversation on one selector.
type RoomKind string

const (
	RoomKindCampaign RoomKind = "campaign"
	RoomKindTask     RoomKind = "task"
	RoomKindProject  RoomKind = "project"
	RoomKindAdhoc    RoomKind = "adhoc"
)

// RoomState is the durable room lifecycle. A derived room may also read as
// closed without a stored transition when its work goes terminal; see
// EffectiveRoomState.
type RoomState string

const (
	RoomStateOpen     RoomState = "open"
	RoomStateClosed   RoomState = "closed"
	RoomStateArchived RoomState = "archived"
)

// EnvelopeObligation is what a say asks of its addressee. Only reply_required
// fires the kicker; fyi presents into a live generation or waits for the next
// attend; none is a log entry with no addressee.
type EnvelopeObligation string

const (
	EnvelopeObligationReplyRequired EnvelopeObligation = "reply_required"
	EnvelopeObligationFYI           EnvelopeObligation = "fyi"
	EnvelopeObligationNone          EnvelopeObligation = "none"
)

// EnvelopeState is the per-envelope obligation lifecycle. Every lifecycle field
// is per envelope, so one recipient of a fan-out never disposes another's.
type EnvelopeState string

const (
	EnvelopeStatePending   EnvelopeState = "pending"
	EnvelopeStatePresented EnvelopeState = "presented"
	EnvelopeStateAcked     EnvelopeState = "acked"
	EnvelopeStateDeferred  EnvelopeState = "deferred"
	EnvelopeStateDead      EnvelopeState = "dead"
)

// RoomMemberSource records how a member came to be in the room. There is no
// derived membership from wrkq fields (assignee, owner): nothing fires from
// membership, so derivation would be dead weight and a drift source.
type RoomMemberSource string

const (
	RoomMemberSourceSpoke     RoomMemberSource = "spoke"
	RoomMemberSourceAddressed RoomMemberSource = "addressed"
	RoomMemberSourceJoined    RoomMemberSource = "joined"
)

// AdhocRoomIdleTTL is the idle window after which an open ad-hoc room
// auto-archives (T-07612 §3.1, open item closed at 24h).
const AdhocRoomIdleTTL = 24 * time.Hour

// DefaultEnvelopeMaxRounds bounds kicker-driven redelivery before an envelope
// dead-letters visibly. Carried from T-06810's wave-2 freeze ruling: only a
// still-presented envelope advanced by a completed kicker turn counts, and the
// fifth round lands in dead. Clear-inbox no-op turns never advance it.
const DefaultEnvelopeMaxRounds = 5

// Room is a durable conversation. Derived rooms anchor on exactly one live
// wrkq resource; ad-hoc rooms anchor on nothing and mint an R- id.
type Room struct {
	UUID                  string    `json:"uuid" db:"uuid"`
	ID                    *string   `json:"id,omitempty" db:"id"`
	Kind                  RoomKind  `json:"kind" db:"kind"`
	TaskUUID              *string   `json:"task_uuid,omitempty" db:"task_uuid"`
	ContainerUUID         *string   `json:"container_uuid,omitempty" db:"container_uuid"`
	Subject               *string   `json:"subject,omitempty" db:"subject"`
	State                 RoomState `json:"state" db:"state"`
	ClosedAt              *string   `json:"closed_at,omitempty" db:"closed_at"`
	ReopenedAt            *string   `json:"reopened_at,omitempty" db:"reopened_at"`
	LastActivityAt        string    `json:"last_activity_at" db:"last_activity_at"`
	OpenedByPrincipalRef  string    `json:"opened_by_principal_ref" db:"opened_by_principal_ref"`
	OpenedAt              string    `json:"opened_at" db:"opened_at"`
	Meta                  *string   `json:"meta,omitempty" db:"meta"`
	ETag                  int64     `json:"etag" db:"etag"`
	CreatedAt             string    `json:"created_at" db:"created_at"`
	UpdatedAt             string    `json:"updated_at" db:"updated_at"`
	CreatedByPrincipalRef string    `json:"created_by_principal_ref" db:"created_by_principal_ref"`
	CreatedByScopeRef     *string   `json:"created_by_scope_ref,omitempty" db:"created_by_scope_ref"`
	UpdatedByPrincipalRef string    `json:"updated_by_principal_ref" db:"updated_by_principal_ref"`
	UpdatedByScopeRef     *string   `json:"updated_by_scope_ref,omitempty" db:"updated_by_scope_ref"`
}

// Envelope is one object for chat and obligation, addressed to exactly one
// recipient. `--to a,b` fans out to one envelope per addressee sharing GroupID.
type Envelope struct {
	UUID                  string             `json:"uuid" db:"uuid"`
	ID                    string             `json:"id" db:"id"`
	RoomUUID              string             `json:"room_uuid" db:"room_uuid"`
	GroupID               *string            `json:"group_id,omitempty" db:"group_id"`
	FromPrincipalRef      string             `json:"from_principal_ref" db:"from_principal_ref"`
	FromScopeRef          *string            `json:"from_scope_ref,omitempty" db:"from_scope_ref"`
	ToScopeRef            *string            `json:"to_scope_ref,omitempty" db:"to_scope_ref"`
	ToPrincipalRef        *string            `json:"to_principal_ref,omitempty" db:"to_principal_ref"`
	Obligation            EnvelopeObligation `json:"obligation" db:"obligation"`
	Body                  string             `json:"body" db:"body"`
	TaskUUID              *string            `json:"task_uuid,omitempty" db:"task_uuid"`
	State                 EnvelopeState      `json:"state" db:"state"`
	RoundCount            int64              `json:"round_count" db:"round_count"`
	RetryAt               *string            `json:"retry_at,omitempty" db:"retry_at"`
	DeferReason           *string            `json:"defer_reason,omitempty" db:"defer_reason"`
	TerminalActor         *string            `json:"terminal_actor,omitempty" db:"terminal_actor"`
	TerminalAt            *string            `json:"terminal_at,omitempty" db:"terminal_at"`
	Urgent                bool               `json:"urgent" db:"urgent"`
	MaterializationIntent *string            `json:"materialization_intent,omitempty" db:"materialization_intent"`
	RespondToPrincipalRef *string            `json:"respond_to_principal_ref,omitempty" db:"respond_to_principal_ref"`
	RetryPromiseUUID      *string            `json:"retry_promise_uuid,omitempty" db:"retry_promise_uuid"`
	IdempotencyKey        *string            `json:"idempotency_key,omitempty" db:"idempotency_key"`
	Meta                  *string            `json:"meta,omitempty" db:"meta"`
	ETag                  int64              `json:"etag" db:"etag"`
	CreatedAt             string             `json:"created_at" db:"created_at"`
	UpdatedAt             string             `json:"updated_at" db:"updated_at"`
	CreatedByPrincipalRef string             `json:"created_by_principal_ref" db:"created_by_principal_ref"`
	CreatedByScopeRef     *string            `json:"created_by_scope_ref,omitempty" db:"created_by_scope_ref"`
	UpdatedByPrincipalRef string             `json:"updated_by_principal_ref" db:"updated_by_principal_ref"`
	UpdatedByScopeRef     *string            `json:"updated_by_scope_ref,omitempty" db:"updated_by_scope_ref"`
}

// RoomMember is identity + attendance in a room, never delivery and never an
// ACL. Scope-less members (humans) have no attendance.
type RoomMember struct {
	UUID               string           `json:"uuid" db:"uuid"`
	RoomUUID           string           `json:"room_uuid" db:"room_uuid"`
	MemberRef          string           `json:"member_ref" db:"member_ref"`
	MemberPrincipalRef string           `json:"member_principal_ref" db:"member_principal_ref"`
	Scoped             bool             `json:"scoped" db:"scoped"`
	Source             RoomMemberSource `json:"source" db:"source"`
	JoinedAt           string           `json:"joined_at" db:"joined_at"`
	LeftAt             *string          `json:"left_at,omitempty" db:"left_at"`
}

// EnvelopePresentation is the join between wrkq's collaboration ledger and
// HRC's execution world. Every HRC identifier here is opaque to wrkq.
type EnvelopePresentation struct {
	UUID           string  `json:"uuid" db:"uuid"`
	EnvelopeUUID   string  `json:"envelope_uuid" db:"envelope_uuid"`
	RoomUUID       string  `json:"room_uuid" db:"room_uuid"`
	MemberRef      string  `json:"member_ref" db:"member_ref"`
	Node           *string `json:"node,omitempty" db:"node"`
	RuntimeID      *string `json:"runtime_id,omitempty" db:"runtime_id"`
	HostSessionID  *string `json:"host_session_id,omitempty" db:"host_session_id"`
	Generation     *string `json:"generation,omitempty" db:"generation"`
	RunID          *string `json:"run_id,omitempty" db:"run_id"`
	DriveAttemptID *string `json:"drive_attempt_id,omitempty" db:"drive_attempt_id"`
	// DeliveryOutcome is HRC's own classification of HOW this presentation was
	// delivered. Like every other identifier on the receipt it is opaque: wrkq
	// stores and returns it and never interprets or validates it (T-07638).
	DeliveryOutcome         *string `json:"delivery_outcome,omitempty" db:"delivery_outcome"`
	PresentedAt             string  `json:"presented_at" db:"presented_at"`
	PresentedByPrincipalRef string  `json:"presented_by_principal_ref" db:"presented_by_principal_ref"`
}

// ValidateRoomKind validates the stored room kind vocabulary.
func ValidateRoomKind(kind RoomKind) error {
	switch kind {
	case RoomKindCampaign, RoomKindTask, RoomKindProject, RoomKindAdhoc:
		return nil
	default:
		return fmt.Errorf("invalid room kind %q: must be one of: campaign, task, project, adhoc", kind)
	}
}

// ValidateRoomState validates the stored room lifecycle vocabulary.
func ValidateRoomState(state RoomState) error {
	switch state {
	case RoomStateOpen, RoomStateClosed, RoomStateArchived:
		return nil
	default:
		return fmt.Errorf("invalid room state %q: must be one of: open, closed, archived", state)
	}
}

// ValidateEnvelopeObligation validates the stored obligation vocabulary.
func ValidateEnvelopeObligation(obligation EnvelopeObligation) error {
	switch obligation {
	case EnvelopeObligationReplyRequired, EnvelopeObligationFYI, EnvelopeObligationNone:
		return nil
	default:
		return fmt.Errorf("invalid envelope obligation %q: must be one of: reply_required, fyi, none", obligation)
	}
}

// ValidateEnvelopeState validates the stored envelope lifecycle vocabulary.
func ValidateEnvelopeState(state EnvelopeState) error {
	switch state {
	case EnvelopeStatePending, EnvelopeStatePresented, EnvelopeStateAcked,
		EnvelopeStateDeferred, EnvelopeStateDead:
		return nil
	default:
		return fmt.Errorf("invalid envelope state %q: must be one of: pending, presented, acked, deferred, dead", state)
	}
}

// IsEnvelopeTerminal reports whether an envelope has reached a disposition no
// further delivery can change. deferred is paused, never terminal.
func IsEnvelopeTerminal(state EnvelopeState) bool {
	return state == EnvelopeStateAcked || state == EnvelopeStateDead
}

// EffectiveRoomState resolves the room state a caller sees. A stored non-open
// state always wins. Otherwise a derived room reads as closed once its work is
// terminal — a task room's task completed/cancelled/archived/deleted, a
// campaign room's campaign completed/cancelled — unless an explicit reopen has
// overridden that closure. The task room's key IS the task id, so the closure
// signal already rides the same monitor selector as the conversation.
func EffectiveRoomState(stored RoomState, kind RoomKind, reopenedAt *string, workTerminal bool) RoomState {
	if stored != RoomStateOpen {
		return stored
	}
	if kind == RoomKindAdhoc || kind == RoomKindProject {
		return RoomStateOpen
	}
	if workTerminal && reopenedAt == nil {
		return RoomStateClosed
	}
	return RoomStateOpen
}

// NormalizeRoomSubject trims an ad-hoc room subject and derives one from the
// first body line when the caller supplied none.
func NormalizeRoomSubject(subject, body string) string {
	if trimmed := strings.TrimSpace(subject); trimmed != "" {
		return trimmed
	}
	for _, line := range strings.Split(body, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			if len(trimmed) > 120 {
				return trimmed[:120]
			}
			return trimmed
		}
	}
	return ""
}
