package rpccli

// wrkp_render.go — how wrkp shows what it read: timeline entries in the
// human/json/ndjson modes, and the styled view models for timeline rows and a
// single project event.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/style"
	"github.com/spf13/cobra"
)

func renderWrkpEntries(cmd *cobra.Command, entries []timelineEntry, mode string, styled *style.TimelineWriter) error {
	if mode == "human" {
		styled.Write(styledTimelineEntries(entries))
		return nil
	}
	if mode == "json" {
		return encodeJSONIndent(cmd, entries)
	}
	if mode == "ndjson" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		for _, entry := range entries {
			if err := enc.Encode(entry); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}

// styledTimelineEntries flattens merged-timeline entries into the presentation
// view model. Each kind is given the weight it carries: prose entries hand their
// body to the renderer to flow, ticks resolve to a single label. An entry whose
// type is not recognized still renders as itself — project-event types are
// free-form dotted names validated only against a reserved-namespace list, so a
// renderer that only knew today's names would start dropping tomorrow's facts.
func styledTimelineEntries(entries []timelineEntry) []style.StyledEntry {
	styledEntries := make([]style.StyledEntry, 0, len(entries))
	for _, entry := range entries {
		styled := style.StyledEntry{
			Timestamp: entry.Timestamp,
			Principal: entry.PrincipalRef,
			TaskID:    entry.TaskID,
			TaskPath:  entry.TaskPath,
			Label:     entry.Type,
			Accent:    style.ColDim,
		}
		switch {
		case entry.Comment != nil:
			// A comment is an author writing ON a task, so the row is drawn with
			// the same grammar as a message: the authoring seat leads, an arrow
			// names what it wrote on, and the comment id rides dim beside them.
			// The seat is the entry's principal — which IS the scope ref — so it
			// moves out of the right-hand actor column rather than being repeated
			// there. A comment attached to no task keeps the older shape: there is
			// no target to point at.
			styled.ID = entry.Comment.ID
			styled.Label = entry.Comment.ID
			styled.Body = entry.Comment.Body
			if entry.Comment.Kind != nil && *entry.Comment.Kind != "" {
				styled.ID = entry.Comment.ID + " " + *entry.Comment.Kind
			}
			if entry.TaskID != "" {
				styled.Label = "→ " + entry.TaskID
				if styled.Principal != "" {
					styled.From = styled.Principal
					styled.Principal = ""
				}
			}
		case entry.Message != nil:
			// A message is prose and carries the same weight as a comment: the
			// body flows. The label answers the question a room message raises
			// and a comment does not — who handed what to whom — so it names
			// BOTH seats: "sender → addressee   EN-xxxxx". Addressee alone, with
			// the sender pushed to the right-hand actor column, made the reader
			// pair two columns to recover a direction the arrow can just state.
			// The id is carried in ID, which the renderer prints beside the
			// label, so the label must not repeat it.
			styled.ID = entry.Message.EnvelopeID
			styled.Label = "→ " + strings.Join(entry.Message.To, ", ")
			if entry.Message.From != "" {
				// The sender leads the row in From, which the renderer paints in
				// the actor color it wore at the right margin. Leaving it in the
				// actor column too would print the same handle twice on one row.
				styled.From = entry.Message.From
				styled.Principal = ""
			}
			if len(entry.Message.To) == 0 {
				// A log entry (obligation "none") addresses nobody: there is no
				// direction to draw, so the sender goes back to the actor column.
				styled.Label = "logged"
				styled.From = ""
				if entry.Message.From != "" {
					styled.Principal = entry.Message.From
				}
			}
			styled.Accent = style.ColMarker
			styled.Body = entry.Message.Body
		case entry.Outcome != nil:
			styled.Label = "outcome"
			styled.Accent = style.ColDone
			if entry.Outcome.Text != nil {
				styled.Body = *entry.Outcome.Text
			}
		case entry.TaskState != nil:
			styled.Label = "→ " + entry.TaskState.State
			if entry.TaskState.From != nil && *entry.TaskState.From != "" {
				styled.Label = *entry.TaskState.From + " → " + entry.TaskState.State
			}
			if entry.Type == "task.created" {
				styled.Label = "created → " + entry.TaskState.State
				if entry.Requester != nil {
					styled.Label += "  requester=" + wrkpRequesterLabel(entry.Requester.PrincipalRef, entry.Requester.ScopeRef)
				}
			}
			styled.Accent = style.StateColor(entry.TaskState.State)
			styled.TaskState = entry.TaskState.State
		case entry.Claim != nil:
			// The actor column already names who claimed or released; the label
			// carries the generation and, for a release by someone else, whose
			// claim it was.
			styled.Accent = style.ColMarker
			generation := fmt.Sprintf(" gen %d", entry.Claim.Generation)
			if entry.Type == "task.claimed" {
				styled.Label = "claimed" + generation
				if entry.Claim.ScopeRef != "" {
					styled.Label += "  " + entry.Claim.ScopeRef
				}
				if entry.Claim.TakeOver {
					styled.Label += " (take-over)"
				}
			} else {
				styled.Label = "claim released" + generation
				if entry.Claim.PrincipalRef != "" && entry.Claim.PrincipalRef != entry.PrincipalRef {
					styled.Label += "  holder=" + entry.Claim.PrincipalRef
				}
				if entry.Claim.Force {
					styled.Label += " (forced)"
				}
			}
		case entry.ContainerState != nil:
			styled.Label = "campaign → " + entry.ContainerState.To
			if entry.ContainerState.From != nil && *entry.ContainerState.From != "" {
				styled.Label = "campaign " + *entry.ContainerState.From + " → " + entry.ContainerState.To
			}
			styled.Accent = style.ColMarker
		case entry.ProjectEvent != nil:
			styled.Label = entry.ProjectEvent.Type
			styled.Accent = style.ColMarker
			styled.Body = entry.ProjectEvent.Summary
			styled.Attributes = entry.ProjectEvent.Attributes
			if styled.Principal == "" && entry.ProjectEvent.PrincipalRef != nil {
				styled.Principal = *entry.ProjectEvent.PrincipalRef
			}
		}
		styledEntries = append(styledEntries, styled)
	}
	return styledEntries
}

// wrkpRequesterLabel prefers the requester's scope, which is the address a
// worker replies to, and falls back to the principal.
func wrkpRequesterLabel(principalRef, scopeRef string) string {
	if scopeRef != "" {
		return scopeRef
	}
	return principalRef
}

// styledProjectEvent flattens the wire event into the presentation view model.
func styledProjectEvent(event wrkpProjectEvent, project string) style.StyledEvent {
	styled := style.StyledEvent{
		ID:         event.UUID,
		Type:       event.Type,
		Summary:    event.Summary,
		Attributes: event.Attributes,
		Project:    project,
		OccurredAt: event.OccurredAt,
		CreatedAt:  event.CreatedAt,
	}
	if event.PrincipalRef != nil {
		styled.Principal = *event.PrincipalRef
	}
	if event.ScopeRef != nil {
		styled.ScopeRef = *event.ScopeRef
	}
	if event.IdempotencyKey != nil {
		styled.Idempotency = *event.IdempotencyKey
	}
	return styled
}
