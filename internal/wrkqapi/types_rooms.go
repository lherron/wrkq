package wrkqapi

// ─── resource DTOs ────────────────────────────────────────────────────────────

// WrkqRoomWorkRef identifies the live wrkq resource a derived room is keyed by.
// Ad-hoc rooms carry null: they have no work identity, which is why they mint an
// R- id instead.
type WrkqRoomWorkRef struct {
	Type string `json:"type"`
	UUID string `json:"uuid"`
	ID   string `json:"id"`
	Path string `json:"path"`
}

// WrkqRoomLink records the other room a task/campaign pair holds. A task that
// later joins a campaign routes new says to the campaign room while its own
// room stays readable — linked both ways, never merged.
type WrkqRoomLink struct {
	Relation string `json:"relation"`
	Key      string `json:"key"`
	UUID     string `json:"uuid"`
	Kind     string `json:"kind"`
}

// WrkqRoom is the stable room resource DTO. A room carries NO lifecycle state:
// Work and Activity are READ-TIME PROJECTIONS a consumer may render and must
// never gate on, because a say into any room a caller can resolve always
// writes. Labels holds the operator discovery labels (`hidden` today), which
// change the default listing and nothing else.
type WrkqRoom struct {
	UUID    string  `json:"uuid"`
	ID      *string `json:"id,omitempty"`
	Key     string  `json:"key"`
	Kind    string  `json:"kind"`
	Subject *string `json:"subject,omitempty"`
	// Work is "open" or "terminal", derived from the task/campaign state. An
	// ad-hoc room anchors on no work and is always "open".
	Work string `json:"work"`
	// Activity is "stale", "active", or "quiet", by FIRST MATCH over
	// LastActivityAt: stale iff Work is terminal and the last activity is older
	// than 4h, else active under 24h, else quiet. Exactly one value, always.
	Activity             string           `json:"activity"`
	Labels               []string         `json:"labels"`
	WorkRef              *WrkqRoomWorkRef `json:"workRef"`
	Links                []WrkqRoomLink   `json:"links"`
	OpenedByPrincipalRef string           `json:"openedByPrincipalRef"`
	OpenedAt             string           `json:"openedAt"`
	// LastActivityAt is the activity clock: max(openedAt, newest envelope,
	// newest member join). openedAt always exists, so it is defined for every
	// room including one that has never carried a message.
	LastActivityAt string `json:"lastActivityAt"`
	MemberCount    int    `json:"memberCount"`
	MessageCount   int    `json:"messageCount"`
	ETag           int64  `json:"etag"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

// WrkqEnvelopeParty is one end of an envelope. ScopeRef is absent for a
// scope-less principal (a human), which is never kicked or summoned.
type WrkqEnvelopeParty struct {
	PrincipalRef string  `json:"principalRef"`
	ScopeRef     *string `json:"scopeRef,omitempty"`
}

// WrkqEnvelopePresentation is one presentation receipt. Every HRC identifier
// here is an opaque string wrkq stores and never interprets.
type WrkqEnvelopePresentation struct {
	MemberRef      string  `json:"memberRef"`
	Node           *string `json:"node,omitempty"`
	RuntimeID      *string `json:"runtimeId,omitempty"`
	HostSessionID  *string `json:"hostSessionId,omitempty"`
	Generation     *string `json:"generation,omitempty"`
	RunID          *string `json:"runId,omitempty"`
	DriveAttemptID *string `json:"driveAttemptId,omitempty"`
	// DeliveryOutcome is HRC's own class for HOW this delivery landed —
	// admitted_into_active_turn, presented_to_live_harness, started_fresh_turn,
	// kicker today. wrkq stores and returns it and never interprets it, so HRC
	// can add a class without a wrkq change (T-07638).
	DeliveryOutcome *string `json:"deliveryOutcome,omitempty"`
	PresentedAt     string  `json:"presentedAt"`
}

// WrkqEnvelope is the stable envelope resource DTO. EN-xxxxx is an INTERNAL row
// id: inbox/show/log surface it, the injected presentation never does.
type WrkqEnvelope struct {
	UUID     string             `json:"uuid"`
	ID       string             `json:"id"`
	RoomUUID string             `json:"roomUuid"`
	RoomKey  string             `json:"roomKey"`
	RoomKind string             `json:"roomKind"`
	GroupID  *string            `json:"groupId,omitempty"`
	From     WrkqEnvelopeParty  `json:"from"`
	To       *WrkqEnvelopeParty `json:"to"`
	// ReplyTo is the exact --to token that answers THIS envelope: the sender's
	// scope handle when it has one, else its scope-less principal. HRC's §7
	// reply line prints it verbatim. A bare name must never be printed in its
	// place: bare names resolve per room, and reply-is-ack keys on scopes, so a
	// bare reply line can address a seat that never asked (T-07638).
	ReplyTo               string  `json:"replyTo"`
	Obligation            string  `json:"obligation"`
	Body                  string  `json:"body"`
	TaskID                *string `json:"taskId,omitempty"`
	State                 string  `json:"state"`
	Terminal              bool    `json:"terminal"`
	RoundCount            int64   `json:"roundCount"`
	RetryAt               *string `json:"retryAt,omitempty"`
	DeferReason           *string `json:"deferReason,omitempty"`
	TerminalActor         *string `json:"terminalActor,omitempty"`
	Urgent                bool    `json:"urgent"`
	MaterializationIntent *string `json:"materializationIntent,omitempty"`
	RespondToPrincipalRef *string `json:"respondToPrincipalRef,omitempty"`
	RetryPromiseID        *string `json:"retryPromiseId,omitempty"`
	// IdempotencyKey is the SAY's key, carried by every envelope of a fan-out.
	// Consumers dual-writing into another system correlate on it.
	IdempotencyKey *string                    `json:"idempotencyKey,omitempty"`
	Meta           map[string]any             `json:"meta"`
	PresentedTo    []WrkqEnvelopePresentation `json:"presentedTo"`
	ETag           int64                      `json:"etag"`
	CreatedAt      string                     `json:"createdAt"`
	UpdatedAt      string                     `json:"updatedAt"`
}

// WrkqRoomMember is identity + attendance, not delivery and not an ACL.
type WrkqRoomMember struct {
	MemberRef          string                    `json:"memberRef"`
	MemberPrincipalRef string                    `json:"memberPrincipalRef"`
	Scoped             bool                      `json:"scoped"`
	Source             string                    `json:"source"`
	JoinedAt           string                    `json:"joinedAt"`
	LeftAt             *string                   `json:"leftAt,omitempty"`
	Attendance         *WrkqEnvelopePresentation `json:"attendance"`
}

// ─── result DTOs ──────────────────────────────────────────────────────────────

// WrkqRoomSayResult is the receipt for one say. Envelopes holds one row per
// addressee; GroupID is the waitable handle shared by the fan-out and equals the
// single envelope's own id when there is exactly one addressee.
type WrkqRoomSayResult struct {
	Room      WrkqRoom       `json:"room"`
	GroupID   string         `json:"groupId"`
	Envelopes []WrkqEnvelope `json:"envelopes"`
	// Acked lists the envelope ids this say discharged under reply-is-ack. Only
	// the replier's OWN obligations from the same counterparty seat appear;
	// matching is by scope, never by the principal a say was attributed to.
	Acked []string `json:"acked"`
	// RecordedCommentID is set when --record also wrote the body as a wrkq
	// comment on the room's task. Rooms are talk; comments are record.
	RecordedCommentID *string `json:"recordedCommentId,omitempty"`
	// Notice is §5's advisory: set when the say landed in a room whose activity
	// read `stale`. It is never an error and there is no override flag, because
	// there is nothing to override — the say already wrote. A CLI prints it to
	// stderr; a programmatic consumer may ignore it.
	Notice *string `json:"notice,omitempty"`
}

type WrkqRoomListResult struct {
	Items []WrkqRoom `json:"items"`
}

// WrkqRoomLogView is a room's history — the hrcchat `messages`/`thread`
// equivalent. Room history is NEVER injected; an agent pulls it with wrkc log.
type WrkqRoomLogView struct {
	Room  WrkqRoom       `json:"room"`
	Items []WrkqEnvelope `json:"items"`
}

type WrkqRoomMembersView struct {
	Room  WrkqRoom         `json:"room"`
	Items []WrkqRoomMember `json:"items"`
}

// WrkqEnvelopeInboxGroup is one room's worth of standing obligations.
type WrkqEnvelopeInboxGroup struct {
	Room  WrkqRoom       `json:"room"`
	Items []WrkqEnvelope `json:"items"`
}

// WrkqEnvelopeInboxView lists reply_required envelopes addressed to one scope.
// fyi is never listed: it carries no obligation. Deferred envelopes get their
// own heading with their retry time.
type WrkqEnvelopeInboxView struct {
	ScopeRef     *string                  `json:"scopeRef,omitempty"`
	PrincipalRef string                   `json:"principalRef"`
	Groups       []WrkqEnvelopeInboxGroup `json:"groups"`
	Deferred     []WrkqEnvelope           `json:"deferred"`
	Dead         []WrkqEnvelope           `json:"dead"`
}

// WrkqEnvelopePresentResult is what HRC gets back after recording a
// presentation. HistoryHint carries the §7 `history:` cue decision, which is
// keyed to the RUNTIME and not the generation: /quit clears continuation without
// rotating the generation, so every post-quit runtime is cold and gets the cue.
type WrkqEnvelopePresentResult struct {
	Envelope     WrkqEnvelope `json:"envelope"`
	Recorded     bool         `json:"recorded"`
	HistoryHint  bool         `json:"historyHint"`
	MessageCount int          `json:"messageCount"`
	LastMessage  *string      `json:"lastMessageAt,omitempty"`
}

// WrkqEnvelopePendingView is the HRC-facing wake set AND stop-hook predicate in
// one read model. Blocking counts only what refuses a turn end: presented
// reply_required envelopes neither replied nor deferred.
type WrkqEnvelopePendingView struct {
	// Items is the wake set: standing reply_required envelopes, plus pending fyi
	// envelopes when the caller asked for IncludeFyi.
	Items []WrkqEnvelope `json:"items"`
	// Blocking is the stop-hook predicate: envelope ids that must be replied or
	// deferred before this scope's turn may end.
	Blocking []string `json:"blocking"`
	// Repended reports how many due deferrals the read's sweep returned to
	// pending. The transition is derived and emits no event by design.
	Repended int `json:"repended"`
}

// ─── params ───────────────────────────────────────────────────────────────────

// RoomSayParams is the single write verb of the collaboration ledger. Ref is
// resolved by the §4 routing table; To is the addressee list that fans out.
type RoomSayParams struct {
	Ref            string         `json:"ref,omitempty"`
	Body           string         `json:"body"`
	To             []string       `json:"to,omitempty"`
	FYI            bool           `json:"fyi,omitempty"`
	Subject        string         `json:"subject,omitempty"`
	New            bool           `json:"new,omitempty"`
	Urgent         bool           `json:"urgent,omitempty"`
	RespondTo      string         `json:"respondTo,omitempty"`
	Record         bool           `json:"record,omitempty"`
	IdempotencyKey string         `json:"idempotencyKey,omitempty"`
	Meta           map[string]any `json:"meta,omitempty"`
	PrincipalRef   string         `json:"principalRef,omitempty"`
	// ScopeRef is the caller's HRC session handle when it has one. wrkq parses it
	// only as a scope handle and is otherwise opaque to it.
	ScopeRef string `json:"scopeRef,omitempty"`
}

// RoomOpenParams opens an explicit ad-hoc or group room.
type RoomOpenParams struct {
	Members      []string `json:"members"`
	Subject      string   `json:"subject"`
	Task         string   `json:"task,omitempty"`
	PrincipalRef string   `json:"principalRef,omitempty"`
	ScopeRef     string   `json:"scopeRef,omitempty"`
}

type RoomShowParams struct {
	Room         string `json:"room"`
	PrincipalRef string `json:"principalRef,omitempty"`
	ScopeRef     string `json:"scopeRef,omitempty"`
}

// RoomListParams selects rooms. Scope "me" restricts to rooms the caller's own
// scope is a member of; rooms are otherwise readable by any principal.
type RoomListParams struct {
	// All includes rooms the default listing omits — activity `stale` and rooms
	// carrying the `hidden` label. Listing is DISCOVERY: what it omits is still
	// fully addressable, and its obligations still gate and wake.
	All          bool   `json:"all,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Scope        string `json:"scope,omitempty"`
	PrincipalRef string `json:"principalRef,omitempty"`
	ScopeRef     string `json:"scopeRef,omitempty"`
}

type RoomLogViewParams struct {
	Room         string `json:"room"`
	Task         string `json:"task,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	PrincipalRef string `json:"principalRef,omitempty"`
	ScopeRef     string `json:"scopeRef,omitempty"`
}

// RoomLifecycleParams is retained ONLY by the close/reopen burn-in shims, which
// accept it and refuse with room_lifecycle_removed. Wave 5 deletes both.
type RoomLifecycleParams struct {
	Room         string `json:"room"`
	IfMatch      int64  `json:"ifMatch,omitempty"`
	PrincipalRef string `json:"principalRef,omitempty"`
	ScopeRef     string `json:"scopeRef,omitempty"`
}

// RoomLabelParams sets or clears the `hidden` discovery label. Any principal
// may call it: what a listing shows is not an ownership boundary.
type RoomLabelParams struct {
	Room         string `json:"room"`
	PrincipalRef string `json:"principalRef,omitempty"`
	ScopeRef     string `json:"scopeRef,omitempty"`
}

// RoomMemberParams powers join, leave, and invite. Member empty on join/leave
// means the caller's own scope.
type RoomMemberParams struct {
	Room         string `json:"room"`
	Member       string `json:"member,omitempty"`
	PrincipalRef string `json:"principalRef,omitempty"`
	ScopeRef     string `json:"scopeRef,omitempty"`
}

type RoomMembersViewParams struct {
	Room         string `json:"room"`
	PrincipalRef string `json:"principalRef,omitempty"`
	ScopeRef     string `json:"scopeRef,omitempty"`
}

type EnvelopeShowParams struct {
	Envelope     string `json:"envelope"`
	PrincipalRef string `json:"principalRef,omitempty"`
	ScopeRef     string `json:"scopeRef,omitempty"`
}

// EnvelopeInboxViewParams defaults to the caller's own scope.
type EnvelopeInboxViewParams struct {
	ScopeRef     string `json:"scopeRef,omitempty"`
	IncludeDead  bool   `json:"includeDead,omitempty"`
	PrincipalRef string `json:"principalRef,omitempty"`
}

// EnvelopeDeferParams pauses one obligation. RetryAfter is a relative duration
// the API resolves against server time; RetryAt is the absolute form. A retry
// time is backed by a wrkq promise owned by the deferring principal.
type EnvelopeDeferParams struct {
	Envelope     string `json:"envelope"`
	Reason       string `json:"reason"`
	RetryAfter   string `json:"retryAfter,omitempty"`
	RetryAt      string `json:"retryAt,omitempty"`
	IfMatch      int64  `json:"ifMatch,omitempty"`
	PrincipalRef string `json:"principalRef,omitempty"`
	ScopeRef     string `json:"scopeRef,omitempty"`
}

// EnvelopeAckParams is the OPERATOR-only ack used to clear dead mail. There is
// no agent-facing ack: for an agent, the reply IS the ack.
type EnvelopeAckParams struct {
	Envelopes    []string `json:"envelopes"`
	Note         string   `json:"note,omitempty"`
	PrincipalRef string   `json:"principalRef,omitempty"`
	ScopeRef     string   `json:"scopeRef,omitempty"`
}

// EnvelopePresentParams is written by HRC at presentation. wrkq holds every
// identifier here as an opaque string and never resolves it back to a runtime.
type EnvelopePresentParams struct {
	Envelope       string `json:"envelope"`
	MemberRef      string `json:"memberRef,omitempty"`
	Node           string `json:"node,omitempty"`
	RuntimeID      string `json:"runtimeId,omitempty"`
	HostSessionID  string `json:"hostSessionId,omitempty"`
	Generation     string `json:"generation,omitempty"`
	RunID          string `json:"runId,omitempty"`
	DriveAttemptID string `json:"driveAttemptId,omitempty"`
	// DeliveryOutcome is optional: HRC's class for how this delivery landed.
	// Absent stays null on the receipt; wrkq never validates the vocabulary.
	DeliveryOutcome string `json:"deliveryOutcome,omitempty"`
	PrincipalRef    string `json:"principalRef,omitempty"`
	ScopeRef        string `json:"scopeRef,omitempty"`
}

// EnvelopePendingViewParams asks for the obligations standing against one or
// more scopes. Scopes empty defaults to the caller's own scope.
type EnvelopePendingViewParams struct {
	Scopes []string `json:"scopes,omitempty"`
	// IncludeFyi additionally reports pending fyi envelopes in Items. It is a
	// request parameter, not a feature flag: the default read is the wake set
	// and stays obligation-only. A fyi never enters Blocking and never summons;
	// gating presentation to a live generation is the consumer's half of §5.
	IncludeFyi   bool   `json:"includeFyi,omitempty"`
	PrincipalRef string `json:"principalRef,omitempty"`
	ScopeRef     string `json:"scopeRef,omitempty"`
}

// EnvelopeRoundParams records that a completed kicker turn presented an
// envelope and ended without disposition. MaxRounds overrides the default bound.
type EnvelopeRoundParams struct {
	Envelope     string `json:"envelope"`
	MaxRounds    int64  `json:"maxRounds,omitempty"`
	PrincipalRef string `json:"principalRef,omitempty"`
	ScopeRef     string `json:"scopeRef,omitempty"`
}

// EnvelopeBirthEnvelopeParams asks for the birth envelope of ONE target scope.
// It carries the target and nothing else: the sender is read from the ledger,
// never supplied by the caller (T-07655).
type EnvelopeBirthEnvelopeParams struct {
	ScopeRef string `json:"scopeRef"`
}

// WrkqEnvelopeBirth is the birth envelope of a target scope — the lowest-seq
// `reply_required` envelope addressed to it, in any state. A null result means
// nothing has ever fired at the scope.
type WrkqEnvelopeBirth struct {
	EnvelopeID string `json:"envelopeId"`
	// Seq is the envelope's ledger ordinal, the number its EN- id is minted from.
	Seq  int64             `json:"seq"`
	From WrkqEnvelopeParty `json:"from"`
}
