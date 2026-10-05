package rpccli

// wrkc_wire.go — the wire DTOs wrkc decodes from the wrkq.room.* and
// wrkq.envelope.* RPC methods, and envelope-party identity.

type roomWire struct {
	OpenSubtaskCount     int            `json:"openSubtaskCount"`
	UUID                 string         `json:"uuid"`
	ID                   *string        `json:"id,omitempty"`
	Key                  string         `json:"key"`
	Kind                 string         `json:"kind"`
	Work                 string         `json:"work"`
	Activity             string         `json:"activity"`
	Labels               []string       `json:"labels"`
	WorkRef              *roomWorkRef   `json:"workRef"`
	Links                []roomLinkWire `json:"links"`
	OpenedByPrincipalRef string         `json:"openedByPrincipalRef"`
	OpenedAt             string         `json:"openedAt"`
	LastActivityAt       string         `json:"lastActivityAt"`
	MemberCount          int            `json:"memberCount"`
	MessageCount         int            `json:"messageCount"`
	ETag                 int64          `json:"etag"`
	CreatedAt            string         `json:"createdAt"`
	UpdatedAt            string         `json:"updatedAt"`
}

type roomWorkRef struct {
	Type string `json:"type"`
	UUID string `json:"uuid"`
	ID   string `json:"id"`
	Path string `json:"path"`
}

type roomLinkWire struct {
	Relation string `json:"relation"`
	Key      string `json:"key"`
	UUID     string `json:"uuid"`
	Kind     string `json:"kind"`
}

type envelopePartyWire struct {
	PrincipalRef string  `json:"principalRef"`
	ScopeRef     *string `json:"scopeRef,omitempty"`
}

type envelopePresentationWire struct {
	MemberRef       string  `json:"memberRef"`
	Node            *string `json:"node,omitempty"`
	RuntimeID       *string `json:"runtimeId,omitempty"`
	HostSessionID   *string `json:"hostSessionId,omitempty"`
	Generation      *string `json:"generation,omitempty"`
	RunID           *string `json:"runId,omitempty"`
	DriveAttemptID  *string `json:"driveAttemptId,omitempty"`
	InputID         *string `json:"inputId,omitempty"`
	DeliveryOutcome *string `json:"deliveryOutcome,omitempty"`
	PresentedAt     string  `json:"presentedAt"`
}

type envelopeWire struct {
	UUID                  string                     `json:"uuid"`
	ID                    string                     `json:"id"`
	RoomUUID              string                     `json:"roomUuid"`
	RoomKey               string                     `json:"roomKey"`
	RoomKind              string                     `json:"roomKind"`
	GroupID               *string                    `json:"groupId,omitempty"`
	From                  envelopePartyWire          `json:"from"`
	To                    *envelopePartyWire         `json:"to"`
	ReplyTo               string                     `json:"replyTo"`
	Obligation            string                     `json:"obligation"`
	Body                  string                     `json:"body"`
	TaskID                *string                    `json:"taskId,omitempty"`
	State                 string                     `json:"state"`
	Terminal              bool                       `json:"terminal"`
	ExpiresAt             *string                    `json:"expiresAt,omitempty"`
	Delivery              string                     `json:"delivery"`
	FailureReason         *string                    `json:"failureReason,omitempty"`
	RetryAt               *string                    `json:"retryAt,omitempty"`
	DeferReason           *string                    `json:"deferReason,omitempty"`
	Reason                *string                    `json:"reason,omitempty"`
	TerminalActor         *string                    `json:"terminalActor,omitempty"`
	MaterializationIntent *string                    `json:"materializationIntent,omitempty"`
	RespondToPrincipalRef *string                    `json:"respondToPrincipalRef,omitempty"`
	RetryPromiseID        *string                    `json:"retryPromiseId,omitempty"`
	IdempotencyKey        *string                    `json:"idempotencyKey,omitempty"`
	Meta                  map[string]any             `json:"meta"`
	PresentedTo           []envelopePresentationWire `json:"presentedTo"`
	ETag                  int64                      `json:"etag"`
	CreatedAt             string                     `json:"createdAt"`
	UpdatedAt             string                     `json:"updatedAt"`
}

type roomMemberWire struct {
	MemberRef          string                    `json:"memberRef"`
	MemberPrincipalRef string                    `json:"memberPrincipalRef"`
	Scoped             bool                      `json:"scoped"`
	Source             string                    `json:"source"`
	JoinedAt           string                    `json:"joinedAt"`
	LeftAt             *string                   `json:"leftAt,omitempty"`
	Attendance         *envelopePresentationWire `json:"attendance"`
}

type roomSayResultWire struct {
	Room              roomWire       `json:"room"`
	GroupID           string         `json:"groupId"`
	Envelopes         []envelopeWire `json:"envelopes"`
	Acked             []string       `json:"acked"`
	RecordedCommentID *string        `json:"recordedCommentId,omitempty"`
	Notices           []string       `json:"notices,omitempty"`
	Notice            *string        `json:"notice,omitempty"`
}

type envelopeWithdrawRefusalWire struct {
	EnvelopeID   string                    `json:"envelopeId"`
	Reason       string                    `json:"reason"`
	State        string                    `json:"state,omitempty"`
	Presentation *envelopePresentationWire `json:"presentation,omitempty"`
}

type envelopeWithdrawResultWire struct {
	Withdrawn []envelopeWire                `json:"withdrawn"`
	Refused   []envelopeWithdrawRefusalWire `json:"refused"`
}

type roomLogViewWire struct {
	Room  roomWire       `json:"room"`
	Items []envelopeWire `json:"items"`
}

type roomMembersViewWire struct {
	Room  roomWire         `json:"room"`
	Items []roomMemberWire `json:"items"`
}

type envelopeInboxGroupWire struct {
	Room  roomWire       `json:"room"`
	Items []envelopeWire `json:"items"`
}

type envelopeInboxViewWire struct {
	ScopeRef      *string                  `json:"scopeRef,omitempty"`
	PrincipalRef  string                   `json:"principalRef"`
	Groups        []envelopeInboxGroupWire `json:"groups"`
	Deferred      []envelopeWire           `json:"deferred"`
	Failed        []envelopeWire           `json:"failed"`
	SentFailed    []envelopeWire           `json:"sentFailed"`
	SentExpired   []envelopeWire           `json:"sentExpired"`
	SentWithdrawn []envelopeWire           `json:"sentWithdrawn"`
}

func envelopePartyKey(party envelopePartyWire) string {
	scopeRef := ""
	if party.ScopeRef != nil {
		scopeRef = *party.ScopeRef
	}
	return party.PrincipalRef + "\x00" + scopeRef
}

func sameEnvelopeParty(left, right envelopePartyWire) bool {
	return envelopePartyKey(left) == envelopePartyKey(right)
}
