package attribution

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/spf13/cobra"
)

const (
	PrincipalEnv = "WRKQ_PRINCIPAL_REF"
	ActorEnv     = "WRKQ_ACTOR"
	ActorIDEnv   = "WRKQ_ACTOR_ID"
)

// Attribution is the canonical identity/provenance pair attached to a write.
// LegacyActorUUID is optional compatibility/display metadata and must never gate
// acceptance of a write.
type Attribution struct {
	PrincipalRef    string
	ScopeRef        string
	LegacyActorUUID *string
	LegacyActorID   string
}

// ResolveOptions contains the caller inputs for principal resolution.
type ResolveOptions struct {
	DB            *sql.DB
	Config        *config.Config
	Command       *cobra.Command
	ResolvedScope *scope.ResolvedScope
}

// ValidatePrincipalRef accepts only exact Praesidium-v1 identity-prefix refs:
// agent:<id>. Full scope refs belong in scope_ref, not principal_ref.
func ValidatePrincipalRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return fmt.Errorf("principal ref is required")
	}
	parsed, err := scope.ParseScopeRef(ref)
	if err != nil {
		return fmt.Errorf("invalid principal ref %q: %w", ref, err)
	}
	if parsed.Kind != scope.KindAgent || parsed.ProjectID != "" || parsed.TaskID != "" || parsed.RoleName != "" {
		return fmt.Errorf("invalid principal ref %q: must be exactly agent:<id>; full scope refs belong in scope_ref", ref)
	}
	return nil
}

// NormalizeCanonical validates a canonical principal ref without accepting bare
// compat slugs.
func NormalizeCanonical(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if err := ValidatePrincipalRef(ref); err != nil {
		return "", err
	}
	return ref, nil
}

// NormalizeCompat accepts either an exact canonical principal ref or a bare
// token and returns agent:<token>. It rejects full scope refs.
func NormalizeCompat(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("principal value is required")
	}
	if strings.HasPrefix(value, "agent:") {
		return NormalizeCanonical(value)
	}
	ref := "agent:" + value
	if err := ValidatePrincipalRef(ref); err != nil {
		return "", err
	}
	return ref, nil
}

// DeriveFromScope reduces a validated resolved scope to the agent identity
// prefix used as a principal ref.
func DeriveFromScope(resolved scope.ResolvedScope) (string, error) {
	if strings.TrimSpace(resolved.AgentID) == "" {
		return "", fmt.Errorf("resolved scope has no agent id")
	}
	return NormalizeCanonical("agent:" + resolved.AgentID)
}

// Resolve resolves acting attribution using the approved CLI precedence:
// --as, WRKQ_PRINCIPAL_REF, WRKQ_ACTOR, WRKQ_ACTOR_ID, scope-derived, config
// default_actor. Actor table lookups are optional compatibility translations.
func Resolve(opts ResolveOptions) (Attribution, error) {
	var scopeRef string
	if opts.ResolvedScope != nil {
		scopeRef = opts.ResolvedScope.FullRef()
	}

	if value := flagValue(opts.Command, "as"); value != "" {
		principal, err := NormalizeCompat(value)
		if err != nil {
			return Attribution{}, err
		}
		return buildAttribution(opts.DB, principal, scopeRef)
	}
	if value := os.Getenv(PrincipalEnv); value != "" {
		principal, err := NormalizeCanonical(value)
		if err != nil {
			return Attribution{}, err
		}
		return buildAttribution(opts.DB, principal, scopeRef)
	}
	if value := os.Getenv(ActorEnv); value != "" {
		principal, err := NormalizeCompat(value)
		if err != nil {
			return Attribution{}, err
		}
		return buildAttribution(opts.DB, principal, scopeRef)
	}
	if value := os.Getenv(ActorIDEnv); value != "" {
		principal, err := normalizeActorID(opts.DB, value)
		if err != nil {
			return Attribution{}, err
		}
		return buildAttribution(opts.DB, principal, scopeRef)
	}
	if opts.ResolvedScope != nil {
		principal, err := DeriveFromScope(*opts.ResolvedScope)
		if err == nil {
			return buildAttribution(opts.DB, principal, scopeRef)
		}
	}
	if opts.Config != nil && strings.TrimSpace(opts.Config.DefaultActor) != "" {
		principal, err := NormalizeCompat(opts.Config.DefaultActor)
		if err != nil {
			return Attribution{}, err
		}
		return buildAttribution(opts.DB, principal, scopeRef)
	}

	return Attribution{}, fmt.Errorf("no principal configured (set --as, %s, %s, %s, a valid ASP scope, or config default_actor)", PrincipalEnv, ActorEnv, ActorIDEnv)
}

func flagValue(cmd *cobra.Command, name string) string {
	if cmd == nil {
		return ""
	}
	flag := cmd.Flag(name)
	if flag == nil {
		return ""
	}
	return strings.TrimSpace(flag.Value.String())
}

func normalizeActorID(database *sql.DB, value string) (string, error) {
	value = strings.TrimSpace(value)
	if database != nil {
		var slug string
		err := database.QueryRow("SELECT slug FROM actors WHERE id = ? LIMIT 1", value).Scan(&slug)
		if err == nil && strings.TrimSpace(slug) != "" {
			return NormalizeCompat(slug)
		}
		if err != nil && err != sql.ErrNoRows {
			return "", fmt.Errorf("translate %s: %w", ActorIDEnv, err)
		}
	}
	return NormalizeCompat(value)
}

func buildAttribution(database *sql.DB, principalRef, scopeRef string) (Attribution, error) {
	if err := ValidatePrincipalRef(principalRef); err != nil {
		return Attribution{}, err
	}
	attr := Attribution{PrincipalRef: principalRef, ScopeRef: scopeRef}
	if database == nil {
		return attr, nil
	}
	slug := PrincipalHandle(principalRef)
	var uuid, id string
	err := database.QueryRow("SELECT uuid, id FROM actors WHERE slug = ? LIMIT 1", slug).Scan(&uuid, &id)
	if err == nil {
		attr.LegacyActorUUID = &uuid
		attr.LegacyActorID = id
		return attr, nil
	}
	if err != nil && err != sql.ErrNoRows {
		return Attribution{}, fmt.Errorf("read legacy actor display cache: %w", err)
	}
	return attr, nil
}

// PrincipalHandle returns the compact display token for an agent:<id> principal.
func PrincipalHandle(principalRef string) string {
	return strings.TrimPrefix(principalRef, "agent:")
}
