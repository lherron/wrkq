//go:build wrkq_local

package wrkqapi

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/id"
	"github.com/lherron/wrkq/internal/store"
)

// Envelope reads: one envelope, a seat's inbox and wake set, its birth
// envelope, and the cross-room member page.

// EnvelopeShow returns one envelope with its presentation receipts.
func (a *API) EnvelopeShow(ctx context.Context, p EnvelopeShowParams) (*WrkqEnvelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	if _, err := a.store.Rooms.ExpireDueEnvelopes(attr); err != nil {
		return nil, NewInternalError(err)
	}
	envelope, err := a.store.Rooms.GetEnvelope(strings.TrimSpace(p.Envelope))
	if err != nil {
		return nil, mapRoomStoreError(err, p.Envelope)
	}
	return a.envelopeView(ctx, envelope)
}

// EnvelopeBirthEnvelope returns the BIRTH ENVELOPE of one target scope: the
// lowest-seq `reply_required` envelope addressed to it, in any state, or nil
// when nothing has ever fired at it. fyi never summons and is outside the
// domain.
//
// This is HRC's tier-5 birth designation input (T-07655). The params carry the
// TARGET and nothing else — the sender comes off the ledger row, so a caller
// cannot steer which node a virgin scope is born on.
func (a *API) EnvelopeBirthEnvelope(ctx context.Context, p EnvelopeBirthEnvelopeParams) (*WrkqEnvelopeBirth, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := normalizeRoomScopeRef(p.ScopeRef)
	if err != nil {
		return nil, err
	}
	if target == "" {
		return nil, NewValidationError("birthEnvelope requires a target scope handle", map[string]any{"field": "scopeRef", "scopeRef": p.ScopeRef})
	}
	envelope, err := a.store.Rooms.BirthEnvelope(target)
	if err != nil {
		return nil, mapRoomStoreError(err, target)
	}
	if envelope == nil {
		return nil, nil
	}
	_, seq, err := id.Parse(envelope.ID)
	if err != nil {
		return nil, NewInternalError(fmt.Errorf("birth envelope %s has no ledger ordinal: %w", envelope.ID, err))
	}
	return &WrkqEnvelopeBirth{
		EnvelopeID: envelope.ID,
		Seq:        int64(seq),
		From:       WrkqEnvelopeParty{PrincipalRef: envelope.FromPrincipalRef, ScopeRef: envelope.FromScopeRef},
	}, nil
}

// EnvelopeInboxView lists the reply_required obligations standing against one
// scope, grouped by room. fyi is never listed: it carries no obligation. Every
// group is a real obligation that gates and wakes: there is no room projection
// that excuses one. A group whose room reads `work: terminal` is INFORMATION a
// renderer may lead with, not a different class of mail.
func (a *API) EnvelopeInboxView(ctx context.Context, p EnvelopeInboxViewParams) (*WrkqEnvelopeInboxView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	if _, err := a.store.Rooms.ExpireDueEnvelopes(attr); err != nil {
		return nil, NewInternalError(err)
	}
	if _, err := a.store.Rooms.RependDueDeferrals(attr); err != nil {
		return nil, NewInternalError(err)
	}
	target, err := normalizeRoomScopeRef(p.ScopeRef)
	if err != nil {
		return nil, err
	}

	base := store.EnvelopeListParams{Obligations: []domain.EnvelopeObligation{domain.EnvelopeObligationReplyRequired}}
	if target != "" {
		base.ToScopeRef = target
	} else {
		base.ToPrincipalRef = attr.PrincipalRef
	}

	view := &WrkqEnvelopeInboxView{
		PrincipalRef: attr.PrincipalRef, Groups: []WrkqEnvelopeInboxGroup{},
		Deferred: []WrkqEnvelope{}, Failed: []WrkqEnvelope{}, SentFailed: []WrkqEnvelope{}, SentExpired: []WrkqEnvelope{}, SentWithdrawn: []WrkqEnvelope{},
	}
	if target != "" {
		view.ScopeRef = &target
	}

	standing := base
	standing.States = []domain.EnvelopeState{domain.EnvelopeStatePending, domain.EnvelopeStatePresented}
	rows, err := a.store.Rooms.ListEnvelopes(ctx, standing)
	if err != nil {
		return nil, mapRoomStoreError(err, "")
	}
	order := []string{}
	grouped := map[string][]domain.Envelope{}
	for index := range rows {
		key := rows[index].RoomUUID
		if _, ok := grouped[key]; !ok {
			order = append(order, key)
		}
		grouped[key] = append(grouped[key], rows[index])
	}
	rooms := roomStates{}
	for _, roomUUID := range order {
		state, serr := a.roomStateFor(ctx, rooms, roomUUID)
		if serr != nil {
			return nil, serr
		}
		group := WrkqEnvelopeInboxGroup{Room: roomDTO(state)}
		for index := range grouped[roomUUID] {
			envelope, eerr := a.envelopeDTO(ctx, &grouped[roomUUID][index], state)
			if eerr != nil {
				return nil, eerr
			}
			group.Items = append(group.Items, *envelope)
		}
		view.Groups = append(view.Groups, group)
	}

	deferred := base
	deferred.States = []domain.EnvelopeState{domain.EnvelopeStateDeferred}
	view.Deferred, err = a.envelopeListDTO(ctx, rooms, deferred, nil)
	if err != nil {
		return nil, err
	}
	if p.IncludeFailed {
		failed := base
		failed.States = []domain.EnvelopeState{domain.EnvelopeStateFailed}
		view.Failed, err = a.envelopeListDTO(ctx, rooms, failed, nil)
		if err != nil {
			return nil, err
		}
	}
	sent := store.EnvelopeListParams{States: []domain.EnvelopeState{domain.EnvelopeStateFailed}}
	if target != "" {
		sent.FromScopeRef = target
	} else {
		sent.FromPrincipalRef = attr.PrincipalRef
	}
	view.SentFailed, err = a.sentStillOwedDTO(ctx, rooms, sent)
	if err != nil {
		return nil, err
	}
	sent.States = []domain.EnvelopeState{domain.EnvelopeStateExpired}
	view.SentExpired, err = a.sentStillOwedDTO(ctx, rooms, sent)
	if err != nil {
		return nil, err
	}
	sent.States = []domain.EnvelopeState{domain.EnvelopeStateWithdrawn}
	view.SentWithdrawn, err = a.sentStillOwedDTO(ctx, rooms, sent)
	if err != nil {
		return nil, err
	}
	return view, nil
}

// sentFailureWindow bounds how long a taskless sent failure stays listed. Set
// from the live ledger on 2026-09-29 (T-09880): of 204 failed reply_required
// envelopes, 126 were re-sent, p90 0.22h and max 8.66h after the failure, and
// none more than 24h after. A var so tests can shorten it.
var sentFailureWindow = 24 * time.Hour

// sentStillOwedDTO lists the sender's envelopes in one terminal state that
// still ask something of the sender (T-09880), evaluated at read time with
// nothing written. A fyi is never listed: it carries no obligation. A
// reply_required with a task is listed while the task is non-terminal. One
// without a task is listed while it is younger than sentFailureWindow and its
// room is not stale by the rule `wrkc ls` hides rooms by. The records stay in
// the ledger; show and log still read them.
func (a *API) sentStillOwedDTO(ctx context.Context, rooms roomStates, params store.EnvelopeListParams) ([]WrkqEnvelope, error) {
	params.StillOwedSince = time.Now().UTC().Add(-sentFailureWindow).Format("2006-01-02T15:04:05Z")
	return a.envelopeListDTO(ctx, rooms, params, func(envelope *domain.Envelope, room *roomState) bool {
		return envelope.TaskUUID != nil || room.activity != domain.RoomActivityStale
	})
}

// EnvelopePendingView is the HRC-facing read: the kicker's wake set AND the
// stop-hook predicate in one call. Its sweep re-pends due deferrals, which is
// the periodic-sweep half of §5's wake routing.
//
// The read is UNIFORM over rooms: an obligation wakes and gates whatever its
// room's work, activity, or hidden label says. T-07633 excluded a closed room's
// mail here, which was correct only while a closed room refused a say; with the
// gate gone the addressee always has a reply path, and excluding its mail would
// silently strand a supervisor's follow-up on completed work (T-07642).
func (a *API) EnvelopePendingView(ctx context.Context, p EnvelopePendingViewParams) (*WrkqEnvelopePendingView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	if _, err := a.store.Rooms.ExpireDueEnvelopes(attr); err != nil {
		return nil, NewInternalError(err)
	}
	repended, err := a.store.Rooms.RependDueDeferrals(attr)
	if err != nil {
		return nil, NewInternalError(err)
	}

	scopes := make([]string, 0, len(p.Scopes))
	for _, raw := range p.Scopes {
		normalized, nerr := normalizeRoomScopeRef(raw)
		if nerr != nil {
			return nil, nerr
		}
		if normalized != "" {
			scopes = append(scopes, normalized)
		}
	}
	addressee := store.EnvelopeListParams{}
	if len(scopes) > 0 {
		addressee.ToScopeRefs = scopes
	} else {
		own, oerr := normalizeRoomScopeRef(p.ScopeRef)
		if oerr != nil {
			return nil, oerr
		}
		if own != "" {
			addressee.ToScopeRef = own
		} else {
			addressee.ToPrincipalRef = attr.PrincipalRef
		}
	}

	params := addressee
	params.Obligations = []domain.EnvelopeObligation{domain.EnvelopeObligationReplyRequired}
	params.States = []domain.EnvelopeState{domain.EnvelopeStatePending, domain.EnvelopeStatePresented}

	view := &WrkqEnvelopePendingView{Items: []WrkqEnvelope{}, Blocking: []string{}, Repended: repended}
	rooms := roomStates{}
	collect := func(listParams store.EnvelopeListParams) error {
		items, lerr := a.envelopeListDTO(ctx, rooms, listParams, nil)
		if lerr != nil {
			return lerr
		}
		for _, item := range items {
			view.Items = append(view.Items, item)
			// The stop-hook refuses a turn end only for what was actually
			// PRESENTED, left neither replied nor deferred, and OBLIGED: a fyi
			// never reaches here presented, because presentation auto-acks it.
			if item.State == string(domain.EnvelopeStatePresented) &&
				item.Obligation == string(domain.EnvelopeObligationReplyRequired) {
				view.Blocking = append(view.Blocking, item.ID)
			}
		}
		return nil
	}

	if err := collect(params); err != nil {
		return nil, err
	}
	if p.IncludeFyi {
		// The opt-in half: a fyi carries no obligation, so it is only ever an
		// item. Its auto-ack at presentation leaves `pending` as the only live
		// fyi state, and it never blocks a turn end nor summons a runtime.
		fyiParams := addressee
		fyiParams.Obligations = []domain.EnvelopeObligation{domain.EnvelopeObligationFYI}
		fyiParams.States = []domain.EnvelopeState{domain.EnvelopeStatePending}
		if err := collect(fyiParams); err != nil {
			return nil, err
		}
	}
	return view, nil
}

// EnvelopeMemberPage returns one bounded chronological cross-room page for an
// exact active member. This is the authority-owned history/catch-up contract;
// consumers never list rooms or materialize all histories to reproduce it.
func (a *API) EnvelopeMemberPage(ctx context.Context, p EnvelopeMemberPageParams) (*WrkqEnvelopeMemberPage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if (p.BeforeMessageSeq == nil) == (p.AfterMessageSeq == nil) {
		return nil, NewValidationError("exactly one of beforeMessageSeq or afterMessageSeq is required", map[string]any{
			"fields": []string{"beforeMessageSeq", "afterMessageSeq"},
		})
	}
	if (p.BeforeMessageSeq != nil && *p.BeforeMessageSeq < 0) ||
		(p.AfterMessageSeq != nil && *p.AfterMessageSeq < 0) {
		return nil, NewValidationError("message sequence must be non-negative", map[string]any{
			"fields": []string{"beforeMessageSeq", "afterMessageSeq"},
		})
	}
	if p.Limit < 1 || p.Limit > 500 {
		return nil, NewValidationError("limit must be between 1 and 500", map[string]any{
			"field": "limit", "minimum": 1, "maximum": 500,
		})
	}
	memberRef, memberPrincipalRef, err := normalizeEnvelopePageMember(p.MemberRef)
	if err != nil {
		return nil, err
	}
	page, err := a.store.Rooms.MemberEnvelopePage(ctx, store.MemberEnvelopePageParams{
		MemberRef: memberRef, MemberPrincipalRef: memberPrincipalRef,
		BeforeMessageSeq: p.BeforeMessageSeq, AfterMessageSeq: p.AfterMessageSeq,
		Limit: p.Limit, ExpectedLedgerIncarnation: strings.TrimSpace(p.ExpectedLedgerIncarnation),
	})
	if err != nil {
		return nil, mapRoomStoreError(err, "")
	}
	result := &WrkqEnvelopeMemberPage{
		LedgerIncarnation: page.LedgerIncarnation,
		HeadMessageSeq:    page.HeadMessageSeq,
		HasMoreBefore:     page.HasMoreBefore,
		HasMoreAfter:      page.HasMoreAfter,
		Items:             make([]WrkqEnvelope, 0, len(page.Items)),
	}
	rooms := roomStates{}
	for index := range page.Items {
		item := &page.Items[index]
		state, err := a.roomStateFor(ctx, rooms, item.Envelope.RoomUUID)
		if err != nil {
			return nil, err
		}
		dto, err := a.envelopeDTO(ctx, &item.Envelope, state)
		if err != nil {
			return nil, err
		}
		// The repository owns cursor identity; asserting the same ordinal parsed
		// by the ordinary envelope DTO catches any future EN id divergence.
		if dto.MessageSeq != item.MessageSeq {
			return nil, NewInternalError(fmt.Errorf("envelope sequence mismatch for %s", dto.ID))
		}
		result.Items = append(result.Items, *dto)
	}
	return result, nil
}
