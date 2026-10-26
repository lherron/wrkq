package workrpc

import (
	"testing"
)

// Wire-identity pins for the portable/server package cut (T-07090).
//
// These tests carry NO build tag on purpose: they must produce the same result
// in the canonical tagged build and in the portable CGO-free build, because the
// portable client verifies this hash against the server before dispatch.
//
// ProtocolSchemaHash reflects over live DTO types, and writeSchemaType emits
// typ.String() — the PACKAGE-QUALIFIED name — for every internal struct and
// recursively for every field. Relocating a schema-reachable defined type to a
// different package therefore changes the wire identity even when its fields and
// JSON tags are untouched. That is why the cut moved implementations out from
// behind internal/wrkqapi and internal/wrkfapi instead of moving the DTOs out.

// pinnedProtocolSchemaHash is the intentional wire identity after T-08048 added
// project-event post/get/types methods and widened timeline parameters, entries,
// task-state origin, cursor fences, and project-event DTOs. That sits on top of
// T-07889, which added the caller-scoped consumed_by_wait ack reason and rendered
// envelope reason.
// That sits on top of T-07871, which added
// immutable envelope expiresAt/delivery projections, expired/withdrawn states,
// scoped discharge input, the envelope.withdraw method and result DTOs, and the
// WRKQ_ALREADY_PRESENTED refusal code. That sits on top of T-07723, which added
// wrkq.envelope.memberPage, its request/result DTOs, envelope.messageSeq, and
// WRKQ_CURSOR_INVALID for incarnation-fenced collaboration history/catch-up.
// That sits on top of T-07703, which cut
// rooms to rev 5.1: failed{reason} replaces dead, envelope.fail replaces
// roundEnded, roundCount is removed, and inboxView gains sentFailed. That sits
// on top of T-07700, which added
// the advisory `notices` array to WrkqRoomSayResult while retaining `notice` as
// the first-entry compatibility field. That sits on top of T-07701, which made
// cross-project campaign enrolment visible and reachable: WrkqTaskCatView gains
// the optional `campaign` object (CatViewCampaign: id/path/membership) and
// WrkqTaskCreateParams gains `campaign`, so create is a full campaign admission
// path. Both are ADDITIVE fields on existing DTOs — no method joins or leaves
// the catalog, so the cardinality pins below are unchanged by it. That sits on
// top of T-07699, which
// removed room subjects, wrkq.room.open, and WrkqRoomOpenParams. That sits on
// top of T-07673's side-effect-free `preview` plus opaque `inputId` on envelope
// presentation.
// That sits on top of T-07655's birth-envelope read model — `wrkq.envelope.birthEnvelope` with
// WrkqEnvelopeBirthEnvelopeParams and WrkqEnvelopeBirth — which HRC's registry
// host reads to designate a virgin scope's birth node. That sits on top of the
// T-07612 rev 3 amendment which removed the room lifecycle (T-07642): WrkqRoom drops `state`,
// `storedState`, and `closedAt` for the `work`/`activity`/`labels` projections,
// WrkqRoomSayResult gains the stale `notice`, WrkqRoomListParams trades `state`
// for `all`, and wrkq.room.hide/unhide join the catalog with
// WrkqRoomLabelParams. That sits on top of `replyTo` and `deliveryOutcome`
// (T-07638), `includeFyi` (T-07627), and the wave-1 ledger (T-07612). Update it
// only alongside an explicit protocol change; an incidental mismatch remains a
// test failure.
//
// T-08216 (caller state selection): WrkqTreeViewParams and WrkqLsListViewParams
// join the catalog, so the two tree/ls REQUEST shapes now sit inside the hash.
// That is the whole compatibility mechanism for the change — a client built with
// `states`/`lifecycle`/`pruneEmpty` fails initialize against a daemon that would
// silently ignore them, instead of rendering a tree it believes is filtered.
// dtos 150 -> 152.
//
// T-08358 (room messages in the project timeline): WrkqTimelineMessage joins the
// catalog and WrkqTimelineEntry gains the optional `message` variant, so a room
// message is a timeline entry like a comment. The field is additive and
// omitempty, so every entry type that existed before is byte-identical on the
// wire; what the hash change buys is the handshake refusal — a client built
// without the `message` variant would silently drop every room message rather
// than render a log it believes is complete. dtos 152 -> 153.
// T-08925: timeline entries expose the writer's optional scopeRef; scoped
// task.update and comment.add request DTOs join the catalog. dtos 153 -> 155.
// T-09657: WrkqEnvelopeFailParams gains the optional, omitempty `detail` (why an
// envelope failed, e.g. HRC's birth-refusal reason) carried onto the
// envelope.failed event. Additive; cardinality unchanged.
// T-09634 adds the optional timeline before bound; old requests keep their shape.
// T-09886 adds optional session attribution to comment.add and comment read DTOs.
// T-09897 catalogs task create/list requests and named-subtask read/write fields.
// T-09978 adds requesterPrincipalRef/requesterScopeRef to task create, patch,
// task, cat and find DTOs, and the timeline entry's claim and requester details.
// T-10087 adds allTypes to timeline reads to include quiet turn.* facts.
// T-10158 removes retired room verbs, section data and inert workflow fields.
// T-11305 adds say note, and inbox includeNotes/notesSince/recentNotes/notes.
const pinnedProtocolSchemaHash = "sha256:3d3ffa73071c88551ed31d12a12342afc2ce40b6f64d9bca295d2d213041907d"

func TestProtocolSchemaHashPinned(t *testing.T) {
	if got := ProtocolSchemaHash(); got != pinnedProtocolSchemaHash {
		t.Fatalf("ProtocolSchemaHash changed:\n  want %s\n  got  %s\n"+
			"A schema-reachable defined type changed package, name, field set, or json tag.",
			pinnedProtocolSchemaHash, got)
	}
}

// TestProtocolCatalogCardinality pins the catalog sizes that feed the hash, so a
// silently dropped method/error/DTO is reported as itself rather than only as an
// opaque hash mismatch.
func TestProtocolCatalogCardinality(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  int
		want int
	}{
		{"methods", len(MethodCatalog()), 182},
		{"errorCodes", len(ErrorCodeCatalog()), 27},
		{"dtos", len(dtoCatalog), 157},
	} {
		if tc.got != tc.want {
			t.Errorf("%s catalog: want %d, got %d", tc.name, tc.want, tc.got)
		}
	}
}
