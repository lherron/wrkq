package attribution

import (
	"context"
	"strings"
)

type callerPrincipalKey struct{}

// WithCallerPrincipal records the per-request caller principal a transport
// supplied (the X-Wrkq-Principal-Ref header on wrkqd /v1/rpc). Request handlers
// default unattributed writes from it before the configured default principal.
func WithCallerPrincipal(ctx context.Context, principalRef string) context.Context {
	principalRef = strings.TrimSpace(principalRef)
	if principalRef == "" {
		return ctx
	}
	return context.WithValue(ctx, callerPrincipalKey{}, principalRef)
}

// DefaultPrincipal returns the request's caller principal when a transport set
// one, else the configured fallback. An explicit per-frame principal still wins
// at each call site; this only replaces what an empty one defaults to.
func DefaultPrincipal(ctx context.Context, fallback string) string {
	if ctx != nil {
		if ref, ok := ctx.Value(callerPrincipalKey{}).(string); ok && ref != "" {
			return ref
		}
	}
	return fallback
}
