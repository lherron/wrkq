//go:build wrkq_local

package wrkqapi

import (
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/scope"
)

// Requester fields name who asked for a task or subtask. They are distinct from
// creator attribution (audit history) and from requestedBy (project routing).
// The scope is stored canonical and its agent must be the principal, so a
// reader can always address the requester by scope or, scope-less, by principal.

// parseRequesterScope accepts a canonical ScopeRef (agent:<id>:project:...) or
// a scope handle (<id>@<project>[:<lane>]) and returns the canonical ScopeRef
// and the principal it implies.
func parseRequesterScope(raw string) (string, string, error) {
	var (
		parsed scope.ParsedScopeRef
		err    error
	)
	if strings.HasPrefix(raw, "agent:") {
		parsed, err = scope.ParseScopeRef(raw)
	} else {
		parsed, err = scope.ParseScopeHandle(raw)
	}
	if err != nil {
		return "", "", NewValidationError("invalid requester scope: "+err.Error(), map[string]any{
			"field": "requesterScopeRef", "expected": "agent:<id>:project:<project>[:...] or <id>@<project>[:<lane>]",
		})
	}
	return parsed.ScopeRef, "agent:" + parsed.AgentID, nil
}

func normalizeRequesterPrincipal(raw string) (string, error) {
	ref, err := attribution.NormalizeCompat(raw)
	if err != nil {
		return "", NewValidationError("invalid requester principal: "+err.Error(), map[string]any{"field": "requesterPrincipalRef"})
	}
	return ref, nil
}

// resolveRequester validates a requester pair. A scope alone derives the
// principal; when both are given the scope's agent must be the principal.
// Blank inputs yield nil.
func resolveRequester(principalRaw, scopeRaw string) (*string, *string, error) {
	principalRaw = strings.TrimSpace(principalRaw)
	scopeRaw = strings.TrimSpace(scopeRaw)
	var principal, scopeRef *string
	if principalRaw != "" {
		ref, err := normalizeRequesterPrincipal(principalRaw)
		if err != nil {
			return nil, nil, err
		}
		principal = &ref
	}
	if scopeRaw != "" {
		canonical, implied, err := parseRequesterScope(scopeRaw)
		if err != nil {
			return nil, nil, err
		}
		if principal != nil && *principal != implied {
			return nil, nil, NewValidationError("requester principal must match requester scope agent", map[string]any{
				"field": "requesterScopeRef", "requesterPrincipalRef": *principal, "requesterScopeRef": canonical,
			})
		}
		principal = &implied
		scopeRef = &canonical
	}
	return principal, scopeRef, nil
}

// requesterPatchFields maps a TaskPatch's requester pointers onto update
// fields. Principal "" clears both fields; scope "" clears only the scope.
func requesterPatchFields(patch TaskPatch, fields map[string]any) error {
	if patch.RequesterPrincipalRef == nil && patch.RequesterScopeRef == nil {
		return nil
	}
	principalRaw, scopeRaw := "", ""
	if patch.RequesterPrincipalRef != nil {
		principalRaw = strings.TrimSpace(*patch.RequesterPrincipalRef)
	}
	if patch.RequesterScopeRef != nil {
		scopeRaw = strings.TrimSpace(*patch.RequesterScopeRef)
	}
	if patch.RequesterPrincipalRef != nil && principalRaw == "" {
		if scopeRaw != "" {
			return NewValidationError("requester scope requires a requester principal", map[string]any{"field": "requesterScopeRef"})
		}
		fields["requester_principal_ref"] = nil
		fields["requester_scope_ref"] = nil
		return nil
	}
	principal, scopeRef, err := resolveRequester(principalRaw, scopeRaw)
	if err != nil {
		return err
	}
	if principal != nil {
		fields["requester_principal_ref"] = *principal
	}
	if scopeRef != nil {
		fields["requester_scope_ref"] = *scopeRef
	} else if patch.RequesterScopeRef != nil {
		fields["requester_scope_ref"] = nil
	}
	return nil
}

// requesterScopeAgent returns the principal a stored requester scope implies,
// or "" when the scope is blank or unparseable.
func requesterScopeAgent(scopeRef string) string {
	if strings.TrimSpace(scopeRef) == "" {
		return ""
	}
	_, implied, err := parseRequesterScope(scopeRef)
	if err != nil {
		return ""
	}
	return implied
}
