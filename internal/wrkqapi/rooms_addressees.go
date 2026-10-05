//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/store"
)

// Addressee resolution: which seat or principal each --to token of a say
// addresses in its room.

// addresseeScope is the resolution context one say carries: the room, its
// members, and the replier's own address. Bare-name resolution reads the
// replier's standing obligations, so it needs to know who is speaking.
type addresseeScope struct {
	room             *roomState
	members          []domain.RoomMember
	replierScope     string
	replierPrincipal string

	obligations       []domain.Envelope
	obligationsLoaded bool
}

// presentedObligations loads the replier's standing obligations in this room
// once per say, and only when a bare name actually needs them.
func (s *addresseeScope) presentedObligations(a *API) ([]domain.Envelope, error) {
	if s.obligationsLoaded {
		return s.obligations, nil
	}
	rows, err := a.store.Rooms.PresentedObligationsForReplier(s.room.row.UUID, s.replierScope, s.replierPrincipal)
	if err != nil {
		return nil, NewInternalError(err)
	}
	s.obligations, s.obligationsLoaded = rows, true
	return s.obligations, nil
}

// resolveAddressees resolves each --to token against the room per §4. A full
// handle is taken verbatim; a bare name resolves against the replier's standing
// obligations first and by room kind last; HRC birth directives ride along
// verbatim in materialization_intent and are never parsed.
func (a *API) resolveAddressees(ctx context.Context, room *roomState, to []string, implied, replierScope, replierPrincipal string) ([]store.EnvelopeAddressee, error) {
	tokens := make([]string, 0, len(to)+1)
	for _, raw := range to {
		for _, part := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				tokens = append(tokens, trimmed)
			}
		}
	}
	if len(tokens) == 0 {
		if implied == "" {
			return nil, nil
		}
		tokens = append(tokens, implied)
	}

	members, err := a.store.Rooms.ListMembers(room.row.UUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	resolution := &addresseeScope{
		room: room, members: members,
		replierScope: replierScope, replierPrincipal: replierPrincipal,
	}
	seen := map[string]bool{}
	result := make([]store.EnvelopeAddressee, 0, len(tokens))
	for _, token := range tokens {
		addressee, rerr := a.resolveAddressee(ctx, resolution, token)
		if rerr != nil {
			return nil, rerr
		}
		key := addressee.ScopeRef + "|" + addressee.PrincipalRef
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, *addressee)
	}
	return result, nil
}

func (a *API) resolveAddressee(ctx context.Context, resolution *addresseeScope, token string) (*store.EnvelopeAddressee, error) {
	room := resolution.room
	// HRC birth directives (+node=, +model=) are stored verbatim and never
	// parsed by wrkq: they are HRC's vocabulary, applied at kick.
	handle := token
	var intent *string
	if index := strings.Index(token, "+"); index > 0 {
		handle = strings.TrimSpace(token[:index])
		directives := strings.TrimSpace(token[index:])
		if directives != "" {
			intent = &directives
		}
	}

	// An explicit principal (agent:lance) addresses a scope-less principal
	// directly. It is never kicked or summoned.
	if strings.HasPrefix(handle, "agent:") {
		principal, err := attribution.NormalizeCompat(handle)
		if err != nil {
			return nil, NewValidationError("invalid addressee: "+err.Error(), map[string]any{"field": "to", "to": token})
		}
		if principal != handle {
			// A full ScopeRef was supplied; keep the scope as the address.
			parsed, perr := scope.ParseScopeRef(handle)
			if perr == nil && parsed.ProjectID != "" {
				return &store.EnvelopeAddressee{
					ScopeRef: scope.FormatScopeHandle(parsed), PrincipalRef: principal,
					MaterializationIntent: intent,
				}, nil
			}
		}
		return &store.EnvelopeAddressee{PrincipalRef: principal, MaterializationIntent: intent}, nil
	}

	// A full handle is always accepted verbatim.
	if strings.Contains(handle, "@") {
		parsed, err := scope.ParseScopeHandle(handle)
		if err != nil {
			return nil, NewValidationError("invalid addressee handle: "+err.Error(), map[string]any{"field": "to", "to": token})
		}
		principal, perr := attribution.NormalizeCompat(parsed.AgentID)
		if perr != nil {
			return nil, NewValidationError("invalid addressee: "+perr.Error(), map[string]any{"field": "to", "to": token})
		}
		return &store.EnvelopeAddressee{
			ScopeRef: scope.FormatScopeHandle(parsed), PrincipalRef: principal,
			MaterializationIntent: intent,
		}, nil
	}

	// A bare agent name resolves against the room.
	if err := validateBareAgentName(handle); err != nil {
		return nil, NewValidationError(err.Error(), map[string]any{"field": "to", "to": token})
	}

	// The obligation wins over the room's shape. HRC's §7 reply line prints a
	// bare name, and reply-is-ack keys on SCOPES (T-07628): if a bare reply
	// resolved by room kind it would address a seat that never asked, leaving
	// the real obligation to fail while a correct answer sits in the room
	// (T-07638). A supervisor at :primary and a coordinator at any other seat
	// are both answered where they actually stand.
	obligated, err := a.addresseeFromObligation(resolution, handle, intent)
	if err != nil {
		return nil, err
	}
	if obligated != nil {
		return obligated, nil
	}

	matches := make([]domain.RoomMember, 0, 2)
	for _, member := range resolution.members {
		if member.LeftAt != nil {
			continue
		}
		if memberAgentName(member) == handle {
			matches = append(matches, member)
		}
	}
	if len(matches) > 1 {
		refs := make([]string, 0, len(matches))
		for _, match := range matches {
			refs = append(refs, match.MemberRef)
		}
		// Ambiguity ALWAYS refuses, and it refuses with the candidates IN the
		// message: a refusal costs the caller one retry with a full handle, while
		// a silently chosen seat costs a failed obligation nobody notices
		// (T-07638).
		return nil, NewValidationError("ambiguous addressee "+handle+" in this room: "+
			strings.Join(refs, ", ")+" — address one of them by its full handle", map[string]any{
			"field": "to", "to": token, "candidates": refs,
		})
	}
	if len(matches) == 1 {
		match := matches[0]
		addressee := &store.EnvelopeAddressee{PrincipalRef: match.MemberPrincipalRef, MaterializationIntent: intent}
		if match.Scoped {
			addressee.ScopeRef = match.MemberRef
			// A member IS a scope. The row's principal only records who last
			// spoke from the seat, so the address — and the attribution written
			// onto the envelope — derives from the seat itself (T-07628).
			if principal, ok := seatPrincipal(match.MemberRef); ok {
				addressee.PrincipalRef = principal
			}
		}
		return addressee, nil
	}

	// Not a member here: a principal already known to the ledger as scope-less
	// is addressed directly rather than given a scope it does not have.
	principal, err := attribution.NormalizeCompat(handle)
	if err != nil {
		return nil, NewValidationError("invalid addressee: "+err.Error(), map[string]any{"field": "to", "to": token})
	}
	scopeless, err := a.principalIsKnownScopeless(ctx, principal)
	if err != nil {
		return nil, err
	}
	if scopeless {
		return &store.EnvelopeAddressee{PrincipalRef: principal, MaterializationIntent: intent}, nil
	}

	// Otherwise derive the scope from the room: a task room addresses the
	// task-scoped seat, a campaign or project room addresses :primary.
	derived, err := a.deriveAddresseeScope(ctx, room, handle)
	if err != nil {
		return nil, err
	}
	return &store.EnvelopeAddressee{ScopeRef: derived, PrincipalRef: principal, MaterializationIntent: intent}, nil
}

// addresseeFromObligation resolves a bare name to the seat that is waiting on
// this replier: the most recently PRESENTED reply_required envelope in this room
// sent by an agent of that name. Nil means no such obligation stands and the
// caller falls through to membership, then to the room-derived default.
func (a *API) addresseeFromObligation(resolution *addresseeScope, agentName string, intent *string) (*store.EnvelopeAddressee, error) {
	obligations, err := resolution.presentedObligations(a)
	if err != nil {
		return nil, err
	}
	for index := range obligations {
		envelope := &obligations[index]
		if envelopeSenderAgentName(envelope) != agentName {
			continue
		}
		if envelope.FromScopeRef == nil || strings.TrimSpace(*envelope.FromScopeRef) == "" {
			// A scope-less sender (a human) is addressed as the principal it is.
			return &store.EnvelopeAddressee{
				PrincipalRef: envelope.FromPrincipalRef, MaterializationIntent: intent,
			}, nil
		}
		addressee := &store.EnvelopeAddressee{
			ScopeRef: *envelope.FromScopeRef, PrincipalRef: envelope.FromPrincipalRef,
			MaterializationIntent: intent,
		}
		// The seat is the address, and the attribution derives from it: the
		// principal a say was attributed to is never what the reply targets
		// (T-07628).
		if principal, ok := seatPrincipal(*envelope.FromScopeRef); ok {
			addressee.PrincipalRef = principal
		}
		return addressee, nil
	}
	return nil, nil
}

// envelopeSenderAgentName is the bare name an envelope's sender answers to: the
// agent of its SEAT when it has one, else its principal.
func envelopeSenderAgentName(envelope *domain.Envelope) string {
	if envelope.FromScopeRef != nil && strings.TrimSpace(*envelope.FromScopeRef) != "" {
		if parsed, err := scope.ParseScopeHandle(*envelope.FromScopeRef); err == nil {
			return parsed.AgentID
		}
		return *envelope.FromScopeRef
	}
	return strings.TrimPrefix(envelope.FromPrincipalRef, "agent:")
}

func (a *API) deriveAddresseeScope(ctx context.Context, room *roomState, agentName string) (string, error) {
	switch room.row.Kind {
	case domain.RoomKindAdhoc:
		return "", NewValidationError(
			"unknown addressee "+agentName+" in this ad-hoc room; use a full handle (agent@project:task)",
			map[string]any{"field": "to", "to": agentName, "room": room.key})
	case domain.RoomKindTask:
		project, taskID, err := a.taskRoomHandleParts(ctx, room)
		if err != nil {
			return "", err
		}
		return agentName + "@" + project + ":" + taskID, nil
	default:
		project, err := a.containerProjectSlug(ctx, *room.row.ContainerUUID)
		if err != nil {
			return "", err
		}
		return agentName + "@" + project + ":primary", nil
	}
}

func (a *API) taskRoomHandleParts(ctx context.Context, room *roomState) (string, string, error) {
	var taskID, containerUUID string
	if err := a.db.QueryRowContext(ctx, "SELECT id, project_uuid FROM tasks WHERE uuid = ?", *room.row.TaskUUID).
		Scan(&taskID, &containerUUID); err != nil {
		return "", "", NewInternalError(err)
	}
	project, err := a.containerProjectSlug(ctx, containerUUID)
	if err != nil {
		return "", "", err
	}
	return project, taskID, nil
}

// containerProjectSlug walks a container to its top-level project and returns
// that project's slug: the project segment of an agent scope handle.
func (a *API) containerProjectSlug(ctx context.Context, containerUUID string) (string, error) {
	var slug string
	err := a.db.QueryRowContext(ctx, `WITH RECURSIVE ancestry(uuid, parent_uuid, slug, kind) AS (
		SELECT c.uuid, c.parent_uuid, c.slug, c.kind FROM containers c WHERE c.uuid = ?
		UNION ALL
		SELECT p.uuid, p.parent_uuid, p.slug, p.kind
		  FROM containers p JOIN ancestry a ON p.uuid = a.parent_uuid
	)
	SELECT slug FROM ancestry WHERE kind = 'project' LIMIT 1`, containerUUID).Scan(&slug)
	if err == sql.ErrNoRows {
		return "", NewValidationError("container has no owning project", map[string]any{"container": containerUUID})
	}
	if err != nil {
		return "", NewInternalError(err)
	}
	return slug, nil
}

// principalIsKnownScopeless reports whether the ledger has already seen this
// principal participate WITHOUT an HRC scope. wrkq keeps no registry of humans;
// this is derived from the membership it already holds.
func (a *API) principalIsKnownScopeless(ctx context.Context, principalRef string) (bool, error) {
	var known int
	err := a.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM room_members
		WHERE member_principal_ref = ? AND scoped = 0)`, principalRef).Scan(&known)
	if err != nil {
		return false, NewInternalError(err)
	}
	return known == 1, nil
}

// seatPrincipal is the principal a seat speaks as: the agent of its handle.
func seatPrincipal(seat string) (string, bool) {
	parsed, err := scope.ParseScopeHandle(seat)
	if err != nil {
		return "", false
	}
	principal, err := attribution.NormalizeCompat(parsed.AgentID)
	if err != nil {
		return "", false
	}
	return principal, true
}

func memberAgentName(member domain.RoomMember) string {
	if !member.Scoped {
		return strings.TrimPrefix(member.MemberPrincipalRef, "agent:")
	}
	if parsed, err := scope.ParseScopeHandle(member.MemberRef); err == nil {
		return parsed.AgentID
	}
	return member.MemberRef
}

func validateBareAgentName(name string) error {
	if !scope.TokenPattern.MatchString(name) {
		return fmt.Errorf("invalid addressee %q: expected an agent name, agent@project[:task], or agent:<id>", name)
	}
	return nil
}
