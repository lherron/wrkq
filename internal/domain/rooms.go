package domain

import (
	"fmt"
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

// RoomWork is the read-time projection of the work a derived room is keyed by.
// It is NEVER stored and NEVER gates: a say into a terminal room writes.
type RoomWork string

const (
	RoomWorkOpen     RoomWork = "open"
	RoomWorkTerminal RoomWork = "terminal"
)

// RoomActivity is the read-time liveness projection. It is single-valued and
// total: every room has exactly one value at every instant, and every consumer
// — default listing, pair-room reuse, the stale notice — reads that one value.
type RoomActivity string

const (
	RoomActivityActive RoomActivity = "active"
	RoomActivityQuiet  RoomActivity = "quiet"
	RoomActivityStale  RoomActivity = "stale"
)

// RoomStaleAfter and RoomActiveWithin are the two clocks of the activity
// projection (T-07612 rev 3 §3.1).
const (
	RoomStaleAfter   = 4 * time.Hour
	RoomActiveWithin = 24 * time.Hour
)

// RoomLabelHidden removes a room from the DEFAULT listing and nothing else. It
// is a label, not a state: a hidden room accepts says, and its obligations gate
// and wake exactly like any other room's.
const RoomLabelHidden = "hidden"

// EnvelopeObligation is what a say asks of its addressee. The axis is
// summon-vs-inject, not wake-vs-no-wake: only reply_required BIRTHS an unborn
// seat and gates a turn end; fyi is still injected into a seated addressee
// (it drives a turn on a live seat, auto-acked at presentation) but never
// births one and never gates; none is a log entry with no addressee.
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
	EnvelopeStateFailed    EnvelopeState = "failed"
	EnvelopeStateExpired   EnvelopeState = "expired"
	EnvelopeStateWithdrawn EnvelopeState = "withdrawn"
)

// EnvelopeDelivery is immutable delivery intent. wrkq stores and projects it;
// HRC alone decides how that intent maps to execution admission.
type EnvelopeDelivery string

const (
	EnvelopeDeliveryQueue EnvelopeDelivery = "queue"
	EnvelopeDeliveryHold  EnvelopeDelivery = "hold"
)

// EnvelopeFailureReason classifies why an obligation ended without a reply,
// defer, or operator ack. legacy is migration-only; live callers never write it.
type EnvelopeFailureReason string

const (
	EnvelopeFailureRuntimeTerminated EnvelopeFailureReason = "runtime_terminated"
	EnvelopeFailureIgnored           EnvelopeFailureReason = "ignored"
	EnvelopeFailureUndeliverable     EnvelopeFailureReason = "undeliverable"
	EnvelopeFailureLegacy            EnvelopeFailureReason = "legacy"
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

// Room is a durable conversation. Derived rooms anchor on exactly one live
// wrkq resource; ad-hoc rooms anchor on nothing and mint an R- id.
//
// A room carries NO lifecycle state. `work` and `activity` are computed at read
// time from the work and from LastActivity, and neither can refuse a say. The
// `state`, `closed_at`, and `reopened_at` columns survive the rev-3 amendment
// unread until wave 5 drops them, which is why they are not scanned here.
type Room struct {
	UUID                  string   `json:"uuid" db:"uuid"`
	ID                    *string  `json:"id,omitempty" db:"id"`
	Kind                  RoomKind `json:"kind" db:"kind"`
	TaskUUID              *string  `json:"task_uuid,omitempty" db:"task_uuid"`
	ContainerUUID         *string  `json:"container_uuid,omitempty" db:"container_uuid"`
	LastActivityAt        string   `json:"last_activity_at" db:"last_activity_at"`
	OpenedByPrincipalRef  string   `json:"opened_by_principal_ref" db:"opened_by_principal_ref"`
	OpenedAt              string   `json:"opened_at" db:"opened_at"`
	Labels                []string `json:"labels" db:"-"`
	Meta                  *string  `json:"meta,omitempty" db:"meta"`
	ETag                  int64    `json:"etag" db:"etag"`
	CreatedAt             string   `json:"created_at" db:"created_at"`
	UpdatedAt             string   `json:"updated_at" db:"updated_at"`
	CreatedByPrincipalRef string   `json:"created_by_principal_ref" db:"created_by_principal_ref"`
	CreatedByScopeRef     *string  `json:"created_by_scope_ref,omitempty" db:"created_by_scope_ref"`
	UpdatedByPrincipalRef string   `json:"updated_by_principal_ref" db:"updated_by_principal_ref"`
	UpdatedByScopeRef     *string  `json:"updated_by_scope_ref,omitempty" db:"updated_by_scope_ref"`
}

// Envelope is one object for chat and obligation, addressed to exactly one
// recipient. `--to a,b` fans out to one envelope per addressee sharing GroupID.
type Envelope struct {
	UUID                  string                 `json:"uuid" db:"uuid"`
	ID                    string                 `json:"id" db:"id"`
	RoomUUID              string                 `json:"room_uuid" db:"room_uuid"`
	GroupID               *string                `json:"group_id,omitempty" db:"group_id"`
	FromPrincipalRef      string                 `json:"from_principal_ref" db:"from_principal_ref"`
	FromScopeRef          *string                `json:"from_scope_ref,omitempty" db:"from_scope_ref"`
	ToScopeRef            *string                `json:"to_scope_ref,omitempty" db:"to_scope_ref"`
	ToPrincipalRef        *string                `json:"to_principal_ref,omitempty" db:"to_principal_ref"`
	Obligation            EnvelopeObligation     `json:"obligation" db:"obligation"`
	Body                  string                 `json:"body" db:"body"`
	TaskUUID              *string                `json:"task_uuid,omitempty" db:"task_uuid"`
	State                 EnvelopeState          `json:"state" db:"state"`
	ExpiresAt             *string                `json:"expires_at,omitempty" db:"expires_at"`
	Delivery              EnvelopeDelivery       `json:"delivery" db:"delivery"`
	FailureReason         *EnvelopeFailureReason `json:"failure_reason,omitempty" db:"failure_reason"`
	RetryAt               *string                `json:"retry_at,omitempty" db:"retry_at"`
	DeferReason           *string                `json:"defer_reason,omitempty" db:"defer_reason"`
	TerminalActor         *string                `json:"terminal_actor,omitempty" db:"terminal_actor"`
	TerminalAt            *string                `json:"terminal_at,omitempty" db:"terminal_at"`
	MaterializationIntent *string                `json:"materialization_intent,omitempty" db:"materialization_intent"`
	RespondToPrincipalRef *string                `json:"respond_to_principal_ref,omitempty" db:"respond_to_principal_ref"`
	RetryPromiseUUID      *string                `json:"retry_promise_uuid,omitempty" db:"retry_promise_uuid"`
	IdempotencyKey        *string                `json:"idempotency_key,omitempty" db:"idempotency_key"`
	Meta                  *string                `json:"meta,omitempty" db:"meta"`
	ETag                  int64                  `json:"etag" db:"etag"`
	CreatedAt             string                 `json:"created_at" db:"created_at"`
	UpdatedAt             string                 `json:"updated_at" db:"updated_at"`
	CreatedByPrincipalRef string                 `json:"created_by_principal_ref" db:"created_by_principal_ref"`
	CreatedByScopeRef     *string                `json:"created_by_scope_ref,omitempty" db:"created_by_scope_ref"`
	UpdatedByPrincipalRef string                 `json:"updated_by_principal_ref" db:"updated_by_principal_ref"`
	UpdatedByScopeRef     *string                `json:"updated_by_scope_ref,omitempty" db:"updated_by_scope_ref"`
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
	// InputID is the broker input that accepted this presentation. It is opaque
	// execution-world join data, just like the runtime and drive-attempt ids.
	InputID *string `json:"input_id,omitempty" db:"input_id"`
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
		EnvelopeStateDeferred, EnvelopeStateFailed, EnvelopeStateExpired,
		EnvelopeStateWithdrawn:
		return nil
	default:
		return fmt.Errorf("invalid envelope state %q: must be one of: pending, presented, acked, deferred, failed, expired, withdrawn", state)
	}
}

// ValidateEnvelopeDelivery validates the stored delivery-intent vocabulary.
func ValidateEnvelopeDelivery(delivery EnvelopeDelivery) error {
	switch delivery {
	case EnvelopeDeliveryQueue, EnvelopeDeliveryHold:
		return nil
	default:
		return fmt.Errorf("invalid envelope delivery %q: must be one of: queue, hold", delivery)
	}
}

// ValidateEnvelopeFailureReason validates the stored failure classification.
func ValidateEnvelopeFailureReason(reason EnvelopeFailureReason) error {
	switch reason {
	case EnvelopeFailureRuntimeTerminated, EnvelopeFailureIgnored,
		EnvelopeFailureUndeliverable, EnvelopeFailureLegacy:
		return nil
	default:
		return fmt.Errorf("invalid envelope failure reason %q: must be one of: runtime_terminated, ignored, undeliverable, legacy", reason)
	}
}

// IsEnvelopeTerminal reports whether an envelope has reached a disposition no
// further delivery can change. deferred is paused, never terminal.
func IsEnvelopeTerminal(state EnvelopeState) bool {
	return state == EnvelopeStateAcked || state == EnvelopeStateFailed ||
		state == EnvelopeStateExpired || state == EnvelopeStateWithdrawn
}

// RoomActivityFor classifies a room by FIRST MATCH, so the three labels are
// mutually exclusive by construction and every room has exactly one:
//
//  1. stale  — terminal work whose last activity is older than RoomStaleAfter
//  2. active — anything whose last activity is younger than RoomActiveWithin
//  3. quiet  — everything else
//
// It is total because lastActivity is total: RoomLastActivity folds opened_at,
// which every room has, so a room with no envelope still classifies.
func RoomActivityFor(work RoomWork, lastActivity, now time.Time) RoomActivity {
	age := now.Sub(lastActivity)
	switch {
	case work == RoomWorkTerminal && age > RoomStaleAfter:
		return RoomActivityStale
	case age < RoomActiveWithin:
		return RoomActivityActive
	default:
		return RoomActivityQuiet
	}
}

// RoomLastActivity is the room's activity clock: the maximum of when it was
// opened, when its newest envelope was written, and when its newest member
// joined. opened_at always exists, so the value is defined for every room
// including a store-created room that has never carried a message.
func RoomLastActivity(openedAt, newestEnvelopeAt, newestJoinAt string) string {
	latest := openedAt
	for _, candidate := range []string{newestEnvelopeAt, newestJoinAt} {
		if candidate > latest {
			latest = candidate
		}
	}
	return latest
}

// RoomHasLabel reports whether a room carries an operator label.
func RoomHasLabel(labels []string, label string) bool {
	for _, candidate := range labels {
		if candidate == label {
			return true
		}
	}
	return false
}
