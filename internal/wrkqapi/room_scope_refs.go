//go:build wrkq_local

package wrkqapi

import (
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/scope"
)

// normalizeRoomScopeRef accepts a canonical ScopeRef or a scope handle and
// returns the handle form wrkq stores. wrkq parses the grammar and nothing more:
// what a scope MEANS at runtime is HRC's business.
func normalizeRoomScopeRef(raw string) (string, error) {
	raw = stripRuntimeLane(strings.TrimSpace(raw))
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "agent:") {
		parsed, err := scope.ParseScopeRef(raw)
		if err != nil {
			return "", NewValidationError("invalid scopeRef: "+err.Error(), map[string]any{"field": "scopeRef", "scopeRef": raw})
		}
		if parsed.ProjectID == "" {
			// A bare principal is not a scope: it is a scope-less identity.
			return "", nil
		}
		return scope.FormatScopeHandle(parsed), nil
	}
	parsed, err := scope.ParseScopeHandle(raw)
	if err != nil {
		return "", NewValidationError("invalid scope handle: "+err.Error(), map[string]any{"field": "scopeRef", "scopeRef": raw})
	}
	if parsed.ProjectID == "" {
		return "", nil
	}
	return scope.FormatScopeHandle(parsed), nil
}

func normalizeEnvelopePageMember(raw string) (string, string, error) {
	raw = stripRuntimeLane(strings.TrimSpace(raw))
	if strings.HasPrefix(raw, "agent:") {
		if err := attribution.ValidatePrincipalRef(raw); err == nil {
			return raw, raw, nil
		}
	}
	memberRef, err := normalizeRoomScopeRef(raw)
	if err != nil {
		return "", "", err
	}
	if memberRef == "" {
		return "", "", NewValidationError(
			"memberRef must be an exact scope handle or scope-less agent:<id> principal",
			map[string]any{"field": "memberRef", "memberRef": raw})
	}
	parsed, err := scope.ParseScopeHandle(memberRef)
	if err != nil {
		return "", "", NewValidationError("invalid memberRef: "+err.Error(), map[string]any{
			"field": "memberRef", "memberRef": raw,
		})
	}
	return memberRef, "agent:" + parsed.AgentID, nil
}

// stripRuntimeLane drops the runtime lane HRC appends to a live session ref
// (HRC_SESSION_REF is "agent:clod:project:wrkq:task:T-07613/lane:main"). A lane
// is execution vocabulary: which pane of a runtime is speaking. A room member is
// a SCOPE, so the lane is discarded here rather than modelled — the boundary
// rule cuts exactly here.
//
// A role suffix (".../reviewer") is part of the scope grammar and is kept: only
// a suffix carrying its own "key:value" shape is a runtime lane, because a role
// name cannot contain a colon.
func stripRuntimeLane(raw string) string {
	slash := strings.Index(raw, "/")
	if slash < 0 {
		return raw
	}
	if strings.Contains(raw[slash+1:], ":") {
		return raw[:slash]
	}
	return raw
}
