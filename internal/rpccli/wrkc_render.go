package rpccli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

// Rendering for wrkc. The human modes are transcript-shaped rather than
// table-shaped: a room is a conversation, and a table of bodies is unreadable.
// Machine modes reuse the shared renderer so --json/--ndjson/--yaml behave
// exactly as they do in wrkq.

func renderWrkcSayResult(cmd *cobra.Command, result roomSayResultWire, flags promiseOutputFlags) error {
	mode, stable, err := resolvePromiseOutputMode(cmd, flags, false)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	renderer := render.NewRenderer(out, render.Options{Porcelain: stable})
	switch mode {
	case "json", "yaml":
		if mode == "yaml" {
			return renderer.RenderYAML(result)
		}
		return renderer.RenderJSON(result)
	case "ndjson":
		items := make([]interface{}, 0, len(result.Envelopes))
		for index := range result.Envelopes {
			items = append(items, result.Envelopes[index])
		}
		return renderer.RenderNDJSON(items)
	case "raw":
		ids := make([]string, 0, len(result.Envelopes))
		for _, envelope := range result.Envelopes {
			ids = append(ids, envelope.ID)
		}
		return renderer.RenderList(ids)
	}

	lines := []string{}
	for _, envelope := range result.Envelopes {
		target := "(log entry)"
		if envelope.To != nil {
			target = envelopePartyLabel(*envelope.To)
		}
		lines = append(lines, fmt.Sprintf("%s → %s in %s [%s]",
			envelope.ID, target, result.Room.Key, envelope.Obligation))
	}
	if len(result.Envelopes) > 1 {
		lines = append(lines, "group: "+result.GroupID)
	}
	if len(result.Acked) > 0 {
		lines = append(lines, "acked by this reply: "+strings.Join(result.Acked, ", "))
	}
	if result.RecordedCommentID != nil {
		lines = append(lines, "recorded as comment "+*result.RecordedCommentID)
	}
	return renderer.RenderList(lines)
}

func renderWrkcRoomSingleton(cmd *cobra.Command, raw json.RawMessage, flags promiseOutputFlags) error {
	var room roomWire
	if err := json.Unmarshal(raw, &room); err != nil {
		return err
	}
	mode, stable, err := resolvePromiseOutputMode(cmd, flags, false)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	renderer := render.NewRenderer(out, render.Options{Porcelain: stable})
	switch mode {
	case "json":
		return renderer.RenderJSON(room)
	case "yaml":
		return renderer.RenderYAML(room)
	case "ndjson":
		return renderer.RenderNDJSON([]interface{}{room})
	case "raw":
		return renderer.RenderList([]string{room.Key})
	case "tsv":
		headers, rows := wrkcRoomTable([]roomWire{room})
		return renderer.RenderTSV(headers, rows)
	}
	return renderer.RenderList(wrkcRoomDetailLines(room))
}

func wrkcRoomDetailLines(room roomWire) []string {
	lines := []string{
		"room: " + room.Key,
		"kind: " + room.Kind,
		"state: " + room.State,
	}
	if room.State != room.StoredState {
		// A derived closure is not the same fact as an explicit one, and the
		// difference decides whether `wrkc reopen` is the right move.
		lines = append(lines, "stored_state: "+room.StoredState+" (state is derived from the work)")
	}
	if room.ID != nil {
		lines = append(lines, "id: "+*room.ID)
	}
	if room.Subject != nil {
		lines = append(lines, "subject: "+*room.Subject)
	}
	if room.WorkRef != nil {
		lines = append(lines, "work: "+room.WorkRef.Type+":"+room.WorkRef.ID+" ("+room.WorkRef.Path+")")
	}
	for _, link := range room.Links {
		lines = append(lines, "linked ("+link.Relation+"): "+link.Key)
	}
	lines = append(lines,
		fmt.Sprintf("members: %d", room.MemberCount),
		fmt.Sprintf("messages: %d", room.MessageCount),
		"last_activity: "+room.LastActivityAt,
		"opened_by: "+room.OpenedByPrincipalRef,
		fmt.Sprintf("etag: %d", room.ETag),
	)
	return lines
}

func renderWrkcRooms(cmd *cobra.Command, rooms []roomWire, flags promiseOutputFlags) error {
	mode, stable, err := resolvePromiseOutputMode(cmd, flags, true)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	renderer := render.NewRenderer(out, render.Options{Porcelain: stable})
	switch mode {
	case "json":
		return renderer.RenderJSON(rooms)
	case "yaml":
		return renderer.RenderYAML(rooms)
	case "ndjson":
		items := make([]interface{}, 0, len(rooms))
		for index := range rooms {
			items = append(items, rooms[index])
		}
		return renderer.RenderNDJSON(items)
	case "raw":
		keys := make([]string, 0, len(rooms))
		for _, room := range rooms {
			keys = append(keys, room.Key)
		}
		return renderer.RenderList(keys)
	}
	headers, rows := wrkcRoomTable(rooms)
	if mode == "tsv" {
		return renderer.RenderTSV(headers, rows)
	}
	return renderer.RenderTable(headers, rows)
}

func wrkcRoomTable(rooms []roomWire) ([]string, [][]string) {
	headers := []string{"Room", "Kind", "State", "Subject", "Members", "Messages", "LastActivity"}
	rows := make([][]string, 0, len(rooms))
	for _, room := range rooms {
		subject := ""
		if room.Subject != nil {
			subject = *room.Subject
		}
		rows = append(rows, []string{
			room.Key, room.Kind, room.State, subject,
			fmt.Sprint(room.MemberCount), fmt.Sprint(room.MessageCount), room.LastActivityAt,
		})
	}
	return headers, rows
}

// renderWrkcEnvelopes renders a set of envelopes. `singleton` is the SHAPE
// decision, not a count: a singleton verb (show, defer) renders a bare object in
// JSON while a list verb (log, inbox, ack) stays array-shaped even when it
// happens to hold one row. Callers must not infer it from len().
func renderWrkcEnvelopes(cmd *cobra.Command, envelopes []envelopeWire, flags promiseOutputFlags, singleton bool) error {
	mode, stable, err := resolvePromiseOutputMode(cmd, flags, !singleton)
	if err != nil {
		return err
	}
	return renderWrkcEnvelopesMode(cmd, envelopes, mode, stable, singleton)
}

func renderWrkcEnvelopesMode(cmd *cobra.Command, envelopes []envelopeWire, mode string, stable, singleton bool) error {
	out := cmd.OutOrStdout()
	renderer := render.NewRenderer(out, render.Options{Porcelain: stable})
	switch mode {
	case "json":
		if singleton && len(envelopes) == 1 {
			return renderer.RenderJSON(envelopes[0])
		}
		return renderer.RenderJSON(envelopes)
	case "yaml":
		return renderer.RenderYAML(envelopes)
	case "ndjson":
		items := make([]interface{}, 0, len(envelopes))
		for index := range envelopes {
			items = append(items, envelopes[index])
		}
		return renderer.RenderNDJSON(items)
	case "raw":
		ids := make([]string, 0, len(envelopes))
		for _, envelope := range envelopes {
			ids = append(ids, envelope.ID)
		}
		return renderer.RenderList(ids)
	case "tsv":
		headers, rows := wrkcEnvelopeTable(envelopes)
		return renderer.RenderTSV(headers, rows)
	case "table":
		headers, rows := wrkcEnvelopeTable(envelopes)
		return renderer.RenderTable(headers, rows)
	}
	lines := []string{}
	for index, envelope := range envelopes {
		if index > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, wrkcEnvelopeTranscriptLines(envelope)...)
	}
	return renderer.RenderList(lines)
}

func wrkcEnvelopeTable(envelopes []envelopeWire) ([]string, [][]string) {
	headers := []string{"ID", "Room", "From", "To", "Obligation", "State", "Rounds", "Created"}
	rows := make([][]string, 0, len(envelopes))
	for _, envelope := range envelopes {
		target := ""
		if envelope.To != nil {
			target = envelopePartyLabel(*envelope.To)
		}
		rows = append(rows, []string{
			envelope.ID, envelope.RoomKey, envelopePartyLabel(envelope.From), target,
			envelope.Obligation, envelope.State, fmt.Sprint(envelope.RoundCount), envelope.CreatedAt,
		})
	}
	return headers, rows
}

func renderWrkcTranscript(cmd *cobra.Command, view roomLogViewWire) error {
	renderer := render.NewRenderer(cmd.OutOrStdout(), render.Options{})
	lines := []string{
		fmt.Sprintf("%s (%s · %s · %d messages)", view.Room.Key, view.Room.Kind, view.Room.State, view.Room.MessageCount),
	}
	if view.Room.Subject != nil {
		lines = append(lines, "subject: "+*view.Room.Subject)
	}
	for _, envelope := range view.Items {
		lines = append(lines, "")
		lines = append(lines, wrkcEnvelopeTranscriptLines(envelope)...)
	}
	return renderer.RenderList(lines)
}

func wrkcEnvelopeTranscriptLines(envelope envelopeWire) []string {
	header := fmt.Sprintf("[%s] %s", envelope.CreatedAt, envelopePartyLabel(envelope.From))
	if envelope.To == nil {
		header += " → (log entry)"
	} else {
		header += " → " + envelopePartyLabel(*envelope.To)
	}
	header += "  " + envelope.Obligation + "/" + envelope.State + "  " + envelope.ID
	if envelope.Urgent {
		header += " urgent"
	}
	lines := []string{header}
	if envelope.DeferReason != nil {
		deferred := "  deferred: " + *envelope.DeferReason
		if envelope.RetryAt != nil {
			deferred += " (retry " + *envelope.RetryAt + ")"
		}
		lines = append(lines, deferred)
	}
	for _, line := range strings.Split(strings.TrimRight(envelope.Body, "\n"), "\n") {
		lines = append(lines, "  "+line)
	}
	return lines
}

func renderWrkcEnvelopeDetail(cmd *cobra.Command, envelope envelopeWire) error {
	lines := []string{
		"envelope: " + envelope.ID,
		"room: " + envelope.RoomKey + " (" + envelope.RoomKind + ")",
		"from: " + envelopePartyLabel(envelope.From),
	}
	if envelope.To != nil {
		lines = append(lines, "to: "+envelopePartyLabel(*envelope.To))
	} else {
		lines = append(lines, "to: (log entry — nobody is presented)")
	}
	lines = append(lines,
		"obligation: "+envelope.Obligation,
		"state: "+envelope.State,
		fmt.Sprintf("rounds: %d", envelope.RoundCount),
	)
	if envelope.GroupID != nil {
		lines = append(lines, "group: "+*envelope.GroupID)
	}
	if envelope.TaskID != nil {
		lines = append(lines, "task: "+*envelope.TaskID)
	}
	if envelope.DeferReason != nil {
		lines = append(lines, "defer_reason: "+*envelope.DeferReason)
	}
	if envelope.RetryAt != nil {
		lines = append(lines, "retry_at: "+*envelope.RetryAt)
	}
	if envelope.RetryPromiseID != nil {
		lines = append(lines, "retry_promise: "+*envelope.RetryPromiseID)
	}
	if envelope.TerminalActor != nil {
		lines = append(lines, "terminal_actor: "+*envelope.TerminalActor)
	}
	if envelope.MaterializationIntent != nil {
		lines = append(lines, "materialization_intent: "+*envelope.MaterializationIntent)
	}
	if envelope.IdempotencyKey != nil {
		lines = append(lines, "idempotency_key: "+*envelope.IdempotencyKey)
	}
	for _, presentation := range envelope.PresentedTo {
		lines = append(lines, "presented: "+presentation.MemberRef+" at "+presentation.PresentedAt+
			presentationSuffix(presentation))
	}
	lines = append(lines, fmt.Sprintf("etag: %d", envelope.ETag), "", "body:")
	for _, line := range strings.Split(strings.TrimRight(envelope.Body, "\n"), "\n") {
		lines = append(lines, "  "+line)
	}
	return render.NewRenderer(cmd.OutOrStdout(), render.Options{}).RenderList(lines)
}

func presentationSuffix(presentation envelopePresentationWire) string {
	parts := []string{}
	if presentation.Node != nil {
		parts = append(parts, "node="+*presentation.Node)
	}
	if presentation.Generation != nil {
		parts = append(parts, "gen="+*presentation.Generation)
	}
	if presentation.RuntimeID != nil {
		parts = append(parts, "runtime="+*presentation.RuntimeID)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, " ") + ")"
}

func renderWrkcInbox(cmd *cobra.Command, view envelopeInboxViewWire) error {
	scope := view.PrincipalRef
	if view.ScopeRef != nil {
		scope = *view.ScopeRef
	}
	lines := []string{"inbox for " + scope}
	total := 0
	for _, group := range view.Groups {
		total += len(group.Items)
		lines = append(lines, "", group.Room.Key+" ("+group.Room.Kind+")")
		for _, envelope := range group.Items {
			lines = append(lines, "  "+envelope.ID+"  "+envelopePartyLabel(envelope.From)+
				"  ["+envelope.State+"]  "+wrkcFirstLine(envelope.Body))
		}
	}
	if total == 0 {
		lines = append(lines, "", "no standing obligations")
	}
	if len(view.Deferred) > 0 {
		lines = append(lines, "", "deferred")
		for _, envelope := range view.Deferred {
			retry := "no retry armed"
			if envelope.RetryAt != nil {
				retry = "retry " + *envelope.RetryAt
			}
			reason := ""
			if envelope.DeferReason != nil {
				reason = " — " + *envelope.DeferReason
			}
			lines = append(lines, "  "+envelope.ID+"  "+envelope.RoomKey+"  ("+retry+")"+reason)
		}
	}
	if len(view.Dead) > 0 {
		lines = append(lines, "", "dead")
		for _, envelope := range view.Dead {
			lines = append(lines, "  "+envelope.ID+"  "+envelope.RoomKey+"  "+wrkcFirstLine(envelope.Body))
		}
	}
	return render.NewRenderer(cmd.OutOrStdout(), render.Options{}).RenderList(lines)
}

func renderWrkcMembers(cmd *cobra.Command, view roomMembersViewWire, flags promiseOutputFlags) error {
	mode, stable, err := resolvePromiseOutputMode(cmd, flags, true)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	renderer := render.NewRenderer(out, render.Options{Porcelain: stable})
	switch mode {
	case "json":
		return renderer.RenderJSON(view)
	case "yaml":
		return renderer.RenderYAML(view)
	case "ndjson":
		items := make([]interface{}, 0, len(view.Items))
		for index := range view.Items {
			items = append(items, view.Items[index])
		}
		return renderer.RenderNDJSON(items)
	case "raw":
		refs := make([]string, 0, len(view.Items))
		for _, member := range view.Items {
			refs = append(refs, member.MemberRef)
		}
		return renderer.RenderList(refs)
	}
	headers := []string{"Member", "Principal", "Source", "Joined", "Left", "LastPresented"}
	rows := make([][]string, 0, len(view.Items))
	for _, member := range view.Items {
		left := ""
		if member.LeftAt != nil {
			left = *member.LeftAt
		}
		attendance := ""
		if member.Attendance != nil {
			attendance = member.Attendance.PresentedAt + presentationSuffix(*member.Attendance)
		} else if !member.Scoped {
			// Scope-less members are never presented through a runtime, so an
			// empty attendance here is a fact about them, not a gap.
			attendance = "(scope-less)"
		}
		rows = append(rows, []string{
			member.MemberRef, member.MemberPrincipalRef, member.Source,
			member.JoinedAt, left, attendance,
		})
	}
	if mode == "tsv" {
		return renderer.RenderTSV(headers, rows)
	}
	return renderer.RenderTable(headers, rows)
}

func envelopePartyLabel(party envelopePartyWire) string {
	if party.ScopeRef != nil && *party.ScopeRef != "" {
		return *party.ScopeRef
	}
	return party.PrincipalRef
}

func wrkcFirstLine(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			if len(trimmed) > 72 {
				return trimmed[:69] + "..."
			}
			return trimmed
		}
	}
	return ""
}
