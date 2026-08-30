package workrpc

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

// Credential diagnostics (T-06976). A remote auth rejection used to surface as a
// bare "remote workrpc HTTP 401", naming neither the credential source nor its
// precedence. Diagnosing it meant bisecting the environment by hand — which is
// exactly what a stale WRKQD_TOKEN injected by a repo-local .env.local cost on
// 2026-07-25, while a valid WRKQD_TOKEN_FILE sat unused.
//
// This lives in package workrpc, not pkg/client, so the direct client
// transport and the stdio-forwarding path share one source of truth (client
// imports workrpc, so the reverse would cycle).
//
// These helpers report SOURCE and LENGTH only. Token bytes must never appear in
// an error, a log line, or a warning.

// TokenDiagnostic describes which credential source produced the token and what
// else was available.
type TokenDiagnostic struct {
	Source         string // "WRKQD_TOKEN env", "WRKQD_TOKEN_FILE", or "" when none
	Length         int
	FilePath       string
	FileReadable   bool
	FileShadowed   bool // env token won while the token file was readable
	FileUnreadable bool // WRKQD_TOKEN_FILE set but unreadable
}

// String renders the parenthetical detail for an auth error, reporting only
// source and character count.
func (d TokenDiagnostic) String() string {
	if d.Source == "" {
		if d.FileUnreadable {
			return fmt.Sprintf("no token: WRKQD_TOKEN unset; WRKQD_TOKEN_FILE %s unreadable", d.FilePath)
		}
		return "no token: neither WRKQD_TOKEN nor WRKQD_TOKEN_FILE provided one"
	}
	detail := fmt.Sprintf("token: %s, %d chars", d.Source, d.Length)
	switch {
	case d.FileShadowed:
		detail += fmt.Sprintf("; WRKQD_TOKEN_FILE %s present but shadowed", d.FilePath)
	case d.FileUnreadable:
		detail += fmt.Sprintf("; WRKQD_TOKEN_FILE %s unreadable", d.FilePath)
	}
	return detail
}

// ResolveToken applies the unchanged precedence — an explicit WRKQD_TOKEN wins
// over WRKQD_TOKEN_FILE — and reports how it got there. Resolving also emits the
// shadow warning, so the surprise is named once per process even when auth
// subsequently succeeds.
func ResolveToken() (string, TokenDiagnostic) {
	diag := TokenDiagnostic{}
	envToken := strings.TrimSpace(os.Getenv("WRKQD_TOKEN"))
	path := strings.TrimSpace(os.Getenv("WRKQD_TOKEN_FILE"))

	var fileToken string
	if path != "" {
		diag.FilePath = path
		if b, err := os.ReadFile(path); err == nil {
			diag.FileReadable = true
			fileToken = strings.TrimSpace(string(b))
		} else {
			diag.FileUnreadable = true
		}
	}

	if envToken != "" {
		diag.Source = "WRKQD_TOKEN env"
		diag.Length = len(envToken)
		diag.FileShadowed = diag.FileReadable
		if diag.FileShadowed {
			warnEnvTokenShadowsFile(path)
		}
		return envToken, diag
	}
	if fileToken != "" {
		diag.Source = "WRKQD_TOKEN_FILE"
		diag.Length = len(fileToken)
		return fileToken, diag
	}
	return "", diag
}

// DescribeTokenSource classifies the credential a transport actually sent.
// Production callers resolve from the environment, but tests and embedders may
// pass an explicit token, so classify by comparison rather than assumption.
func DescribeTokenSource(token string) string {
	resolved, diag := ResolveToken()
	if token == resolved {
		return diag.String()
	}
	if token == "" {
		return "no token: transport constructed without credentials"
	}
	return fmt.Sprintf("token: explicit (not from environment), %d chars", len(token))
}

var shadowWarnOnce sync.Once

// warnEnvTokenShadowsFile emits one unconditional stderr notice per process when
// an env token overrides a readable token file. It fires at resolution time, not
// on failure, because a shadowed credential is worth naming even when the call
// succeeds.
func warnEnvTokenShadowsFile(path string) {
	shadowWarnOnce.Do(func() {
		fmt.Fprintf(os.Stderr,
			"warning: WRKQD_TOKEN (env) is shadowing readable WRKQD_TOKEN_FILE %s; the env token takes precedence\n",
			path)
	})
}
