package workrpc

import (
	"strings"
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

// pinnedProtocolSchemaHash is the intentional wire identity after T-07673 added
// side-effect-free `preview` plus opaque `inputId` to envelope presentation.
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
const pinnedProtocolSchemaHash = "sha256:3cb63e1e5c56043a02eb7b65557a356a8306417847b109426ee75a319501cc2f"

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
		{"methods", len(MethodCatalog()), 180},
		{"errorCodes", len(ErrorCodeCatalog()), 25},
		{"dtos", len(dtoCatalog), 138},
	} {
		if tc.got != tc.want {
			t.Errorf("%s catalog: want %d, got %d", tc.name, tc.want, tc.got)
		}
	}
}

// TestSchemaFingerprintsKeepPackageIdentity asserts the package-qualified names
// the cut could plausibly have shifted: a top-level DTO, a nested struct reached
// through a field, and a type owned by a package whose implementation moved
// behind a build tag. Cardinality and the aggregate hash would both catch a
// change here, but neither would say WHICH identity moved.
func TestSchemaFingerprintsKeepPackageIdentity(t *testing.T) {
	for _, tc := range []struct {
		dto      string
		contains []string
	}{
		{
			// Top-level wrkq DTO; must still be owned by package wrkqapi.
			dto:      "WrkqTask",
			contains: []string{"wrkqapi.WrkqTask{"},
		},
		{
			// Nested reach: a wrkqapi DTO whose field type is owned by
			// internal/workflow, whose service implementation is now tagged.
			dto:      "WrkqWorkflowInstancesResult",
			contains: []string{"wrkqapi.WrkqWorkflowInstancesResult{", "workflow.Instance{"},
		},
		{
			// The initialize handshake the portable client validates on connect.
			dto:      "RPCInitializeResult",
			contains: []string{"workrpc.initializeResult{", "workrpc.Capabilities{"},
		},
	} {
		typ, ok := dtoSchemaTypes[tc.dto]
		if !ok {
			t.Errorf("%s: absent from dtoSchemaTypes", tc.dto)
			continue
		}
		fingerprint := dtoSchemaFingerprint(tc.dto, typ)
		for _, want := range tc.contains {
			if !strings.Contains(fingerprint, want) {
				t.Errorf("%s fingerprint lost %q\n  got: %s", tc.dto, want, fingerprint)
			}
		}
	}
}
