package attribution

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/spf13/cobra"
)

const (
	PrincipalEnv = "WRKQ_PRINCIPAL_REF"
)

// ErrNoPrincipalConfigured identifies the expected "no ambient principal"
// outcome for optional caller defaults. Mutating wrkq-core servers still make
// the final required-principal decision.
var ErrNoPrincipalConfigured = errors.New("no principal configured")

// Attribution is the canonical identity/provenance pair attached to a write.
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
		return fmt.Errorf("principal ref is required; store the normalized agent identity as agent:<id>")
	}
	parsed, err := scope.ParseScopeRef(ref)
	if err != nil {
		return fmt.Errorf("invalid principal ref %q: %w", ref, err)
	}
	if parsed.Kind != scope.KindAgent || parsed.ProjectID != "" || parsed.TaskID != "" || parsed.RoleName != "" {
		return fmt.Errorf("invalid stored principal ref %q: expected normalized agent:<id>; pass full ScopeRefs through NormalizeCanonical before storage", ref)
	}
	return nil
}

// NormalizeCanonical accepts an exact agent principal ref or a full agent
// ScopeRef and returns the principal identity portion. Bare compat slugs are
// intentionally not accepted for caller attribution.
func NormalizeCanonical(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("principal ref is required; use agent:<id> (for example agent:cody)")
	}
	parsed, err := scope.ParseScopeRef(ref)
	if err != nil {
		return "", principalInputError(ref, err)
	}
	return "agent:" + parsed.AgentID, nil
}

// NormalizeCompat accepts an exact/full agent principal input or a bare token
// and returns agent:<id>.
func NormalizeCompat(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("principal value is required; use agent:<id> (for example agent:cody)")
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

func principalInputError(ref string, err error) error {
	if !strings.Contains(ref, ":") && scope.TokenPattern.MatchString(ref) {
		return fmt.Errorf("invalid principal ref %q: missing agent: prefix; use agent:%s", ref, ref)
	}
	if strings.HasPrefix(ref, "agent:") {
		return fmt.Errorf("invalid principal ref %q: expected agent:<id> or a full agent ScopeRef such as agent:<id>:project:<projectId>; %w", ref, err)
	}
	return fmt.Errorf("invalid principal ref %q: principal attribution only supports agent identities; use agent:<id> (for example agent:cody)", ref)
}

// DeriveFromScope reduces a validated resolved scope to the agent identity
// prefix used as a principal ref.
func DeriveFromScope(resolved scope.ResolvedScope) (string, error) {
	if strings.TrimSpace(resolved.AgentID) == "" {
		return "", fmt.Errorf("resolved scope has no agent id")
	}
	return NormalizeCanonical("agent:" + resolved.AgentID)
}

// Resolve resolves wrkq acting attribution using principal-only precedence:
// --principal-ref, --as, WRKQ_PRINCIPAL_REF, scope-derived, config
// default_principal_ref. Legacy actor env/config values are intentionally not
// attribution sources.
func Resolve(opts ResolveOptions) (Attribution, error) {
	return ResolveWithPrincipalEnvs(opts, PrincipalEnv)
}

// ResolveWithPrincipalEnvs resolves acting attribution using the shared
// principal-only precedence: --principal-ref, --as, the supplied explicit
// product principal env vars in order, scope-derived, config
// default_principal_ref. It is used by wrkq and wrkf entrypoints so full runtime
// ScopeRefs are reduced consistently to stored agent:<id> principals.
func ResolveWithPrincipalEnvs(opts ResolveOptions, principalEnvNames ...string) (Attribution, error) {
	var scopeRef string
	if opts.ResolvedScope != nil {
		scopeRef = opts.ResolvedScope.FullRef()
	}

	if value := flagValue(opts.Command, "principal-ref"); value != "" {
		principal, err := NormalizeCanonical(value)
		if err != nil {
			return Attribution{}, err
		}
		if as := flagValue(opts.Command, "as"); as != "" {
			asPrincipal, err := NormalizeCanonical(as)
			if err != nil {
				return Attribution{}, err
			}
			if asPrincipal != principal {
				return Attribution{}, fmt.Errorf("--principal-ref resolves to %s but --as resolves to %s; use one flag or make both point to the same agent", principal, asPrincipal)
			}
		}
		return buildAttribution(principal, scopeRef)
	}
	if value := flagValue(opts.Command, "as"); value != "" {
		principal, err := NormalizeCanonical(value)
		if err != nil {
			return Attribution{}, err
		}
		return buildAttribution(principal, scopeRef)
	}
	for _, envName := range principalEnvNames {
		if envName == "" {
			continue
		}
		if value := os.Getenv(envName); value != "" {
			principal, err := NormalizeCanonical(value)
			if err != nil {
				return Attribution{}, fmt.Errorf("invalid %s: %w", envName, err)
			}
			return buildAttribution(principal, scopeRef)
		}
	}
	if opts.ResolvedScope != nil {
		principal, err := DeriveFromScope(*opts.ResolvedScope)
		if err == nil {
			return buildAttribution(principal, scopeRef)
		}
	}
	if opts.Config != nil && strings.TrimSpace(opts.Config.DefaultPrincipalRef) != "" {
		principal, err := NormalizeCanonical(opts.Config.DefaultPrincipalRef)
		if err != nil {
			return Attribution{}, err
		}
		return buildAttribution(principal, scopeRef)
	}

	return Attribution{}, fmt.Errorf("%w; set --principal-ref agent:<id>, --as agent:<id>, %s=agent:<id>, a valid runtime scope, or config default_principal_ref", ErrNoPrincipalConfigured, PrincipalEnv)
}

// IsNoPrincipalConfigured reports the optional-default miss from Resolve.
func IsNoPrincipalConfigured(err error) bool {
	return errors.Is(err, ErrNoPrincipalConfigured)
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

func buildAttribution(principalRef, scopeRef string) (Attribution, error) {
	if err := ValidatePrincipalRef(principalRef); err != nil {
		return Attribution{}, err
	}
	return Attribution{PrincipalRef: principalRef, ScopeRef: scopeRef}, nil
}

// PrincipalHandle returns the compact display token for an agent:<id> principal.
func PrincipalHandle(principalRef string) string {
	return strings.TrimPrefix(principalRef, "agent:")
}
