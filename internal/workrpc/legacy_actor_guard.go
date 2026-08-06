//go:build wrkq_local

package workrpc

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/lherron/wrkq/internal/wrkfapi"
)

// legacyActorParamFields are the actor-shaped wrkf participant-identity param
// keys retired by the T-05372 cutover. wrkf participant identity is now
// canonical `principal_ref`; a wrkf request that still carries any of these
// top-level keys is rejected rather than silently ignored by json.Unmarshal.
//
// The scan is top-level only so free-form nested blobs (evidence
// data/facts/source) that legitimately contain an "actor" key are not
// affected, and it is applied ONLY to wrkf.* methods (see isWrkfDomainMethod) so unrelated wrkq core/admin surfaces that still accept
// `actor` are untouched.
var legacyActorParamFields = []string{
	"actor", "actorRole", "actor_role",
	"ownerActor", "owner_actor",
	"obligeeActor", "obligee_actor",
	"waiveActor", "waive_actor",
	"resolvedByActor", "resolved_by_actor",
}

// isWrkfDomainMethod reports whether an RPC method carries wrkf workflow
// participant identity and must therefore reject legacy actor-shaped params.
//
// Scope is the wrkf.* namespace ONLY. The wrkq.workflow.* methods
// (attach/refresh/inspect/timeline) are core caller/installer attribution and
// still read canonical camelCase `actor` per the wrkq core convention — they
// are NOT wrkf participant identity and must not be rejected here.
func isWrkfDomainMethod(method string) bool {
	return strings.HasPrefix(method, "wrkf.")
}

// guardLegacyActorParams wraps a wrkf-domain handler so a request carrying a
// retired actor-shaped identity key fails with a validation error.
func guardLegacyActorParams(handler Handler) Handler {
	return HandlerFunc(func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
		if err := rejectLegacyActorFields(params); err != nil {
			return nil, err
		}
		return handler.HandleRPC(ctx, params)
	})
}

func rejectLegacyActorFields(raw json.RawMessage) error {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed[0] != '{' {
		return nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil // malformed object is surfaced by the real param decode
	}
	for _, key := range legacyActorParamFields {
		if _, ok := top[key]; ok {
			return wrkfapi.NewValidationError("legacy actor-shaped wrkf param rejected", map[string]any{
				"field":    key,
				"expected": "principal_ref",
				"hint":     "wrkf participant identity is canonical principal_ref (agent:<id>); the actor field was removed in the T-05372 protocol break",
			})
		}
	}
	return nil
}
