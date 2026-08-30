package client

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/workrpc"
)

// T-06976 regression bars. A remote auth rejection used to surface as a bare
// "remote workrpc HTTP 401", naming neither the credential source nor its
// precedence, which cost ~20 minutes of env bisection when a repo-local
// .env.local injected a stale WRKQD_TOKEN over a valid token file.

const (
	envTokenValue  = "env-token-secret"
	fileTokenValue = "file-token-secret"
)

// unauthorizedEndpoint serves a bare 401 with an empty JSON-RPC body, matching
// what wrkqd returns when the bearer token is rejected.
func unauthorizedEndpoint(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1}`))
	}))
	t.Cleanup(srv.Close)
	// NewRemote builds the scheme and path itself, so hand it bare host:port.
	return strings.TrimPrefix(srv.URL, "http://")
}

// authFailure drives a real remote transport against a 401 endpoint and returns
// the resulting error text.
func authFailure(t *testing.T) string {
	t.Helper()
	tr, err := NewRemote(unauthorizedEndpoint(t), TokenFromEnv(), WrkqProfile)
	if err == nil {
		if _, callErr := tr.Call(t.Context(), "wrkq.task.show", nil); callErr != nil {
			return callErr.Error()
		}
		t.Fatal("expected an auth failure from the 401 endpoint")
	}
	return err.Error()
}

func writeTokenFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node-token")
	if err := os.WriteFile(path, []byte(fileTokenValue+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAuthErrorNamesEnvTokenSource(t *testing.T) {
	t.Setenv("WRKQD_TOKEN", envTokenValue)
	t.Setenv("WRKQD_TOKEN_FILE", "")

	got := authFailure(t)
	for _, want := range []string{"HTTP 401", "WRKQD_TOKEN env", "16 chars"} {
		if !strings.Contains(got, want) {
			t.Errorf("auth error missing %q:\n%s", want, got)
		}
	}
}

func TestAuthErrorNamesTokenFileSource(t *testing.T) {
	t.Setenv("WRKQD_TOKEN", "")
	t.Setenv("WRKQD_TOKEN_FILE", writeTokenFile(t))

	got := authFailure(t)
	for _, want := range []string{"HTTP 401", "WRKQD_TOKEN_FILE", "17 chars"} {
		if !strings.Contains(got, want) {
			t.Errorf("auth error missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "shadowed") {
		t.Errorf("file-only resolution must not claim shadowing:\n%s", got)
	}
}

// The live defect: env token wins while a perfectly good token file sits unused.
func TestAuthErrorReportsShadowedTokenFile(t *testing.T) {
	path := writeTokenFile(t)
	t.Setenv("WRKQD_TOKEN", envTokenValue)
	t.Setenv("WRKQD_TOKEN_FILE", path)

	got := authFailure(t)
	for _, want := range []string{"HTTP 401", "WRKQD_TOKEN env", "WRKQD_TOKEN_FILE", "shadowed", path} {
		if !strings.Contains(got, want) {
			t.Errorf("auth error missing %q:\n%s", want, got)
		}
	}
}

func TestAuthErrorNamesMissingToken(t *testing.T) {
	t.Setenv("WRKQD_TOKEN", "")
	t.Setenv("WRKQD_TOKEN_FILE", "")

	got := authFailure(t)
	for _, want := range []string{"HTTP 401", "no token"} {
		if !strings.Contains(got, want) {
			t.Errorf("auth error missing %q:\n%s", want, got)
		}
	}
}

func TestAuthErrorNamesUnreadableTokenFile(t *testing.T) {
	t.Setenv("WRKQD_TOKEN", "")
	t.Setenv("WRKQD_TOKEN_FILE", filepath.Join(t.TempDir(), "absent"))

	got := authFailure(t)
	for _, want := range []string{"no token", "unreadable"} {
		if !strings.Contains(got, want) {
			t.Errorf("auth error missing %q:\n%s", want, got)
		}
	}
}

// Redaction bar: no diagnostic path may leak token bytes.
func TestAuthDiagnosticsNeverLeakTokenBytes(t *testing.T) {
	path := writeTokenFile(t)
	t.Setenv("WRKQD_TOKEN", envTokenValue)
	t.Setenv("WRKQD_TOKEN_FILE", path)

	got := authFailure(t)
	for _, secret := range []string{envTokenValue, fileTokenValue} {
		if strings.Contains(got, secret) {
			t.Fatalf("auth error leaked a token value %q:\n%s", secret, got)
		}
	}

	// The env-only and file-only shapes must be redacted too.
	t.Setenv("WRKQD_TOKEN_FILE", "")
	if got := authFailure(t); strings.Contains(got, envTokenValue) {
		t.Fatalf("env-only auth error leaked the token:\n%s", got)
	}
	t.Setenv("WRKQD_TOKEN", "")
	t.Setenv("WRKQD_TOKEN_FILE", path)
	if got := authFailure(t); strings.Contains(got, fileTokenValue) {
		t.Fatalf("file-only auth error leaked the token:\n%s", got)
	}
}

// Non-auth failures stay unchanged; credential detail is noise for them.
func TestNonAuthErrorOmitsTokenDiagnostics(t *testing.T) {
	t.Setenv("WRKQD_TOKEN", envTokenValue)
	t.Setenv("WRKQD_TOKEN_FILE", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1}`))
	}))
	t.Cleanup(srv.Close)

	tr, err := NewRemote(strings.TrimPrefix(srv.URL, "http://"), TokenFromEnv(), WrkqProfile)
	got := ""
	if err != nil {
		got = err.Error()
	} else if _, callErr := tr.Call(t.Context(), "wrkq.task.show", nil); callErr != nil {
		got = callErr.Error()
	}
	if !strings.Contains(got, "HTTP 500") {
		t.Fatalf("expected an HTTP 500 error, got: %s", got)
	}
	if strings.Contains(got, "token:") || strings.Contains(got, "WRKQD_TOKEN") {
		t.Errorf("non-auth error should not carry credential detail:\n%s", got)
	}
}

// Precedence is diagnostics-only; env must still win over the file.
func TestTokenPrecedenceUnchangedByDiagnostics(t *testing.T) {
	path := writeTokenFile(t)
	t.Setenv("WRKQD_TOKEN_FILE", path)
	t.Setenv("WRKQD_TOKEN", envTokenValue)
	if got := TokenFromEnv(); got != envTokenValue {
		t.Fatalf("env token must win, got %q", got)
	}
	t.Setenv("WRKQD_TOKEN", "")
	if got := TokenFromEnv(); got != fileTokenValue {
		t.Fatalf("file token must be used when env is empty, got %q", got)
	}
}

func TestShadowDiagnosticNamesBothSources(t *testing.T) {
	path := writeTokenFile(t)
	t.Setenv("WRKQD_TOKEN", envTokenValue)
	t.Setenv("WRKQD_TOKEN_FILE", path)

	_, diag := workrpc.ResolveToken()
	if !diag.FileShadowed {
		t.Fatal("expected fileShadowed when env token overrides a readable file")
	}
	rendered := diag.String()
	for _, want := range []string{"WRKQD_TOKEN env", "WRKQD_TOKEN_FILE", "shadowed", path} {
		if !strings.Contains(rendered, want) {
			t.Errorf("diagnostic missing %q: %s", want, rendered)
		}
	}
	if strings.Contains(rendered, envTokenValue) || strings.Contains(rendered, fileTokenValue) {
		t.Fatalf("diagnostic leaked a token value: %s", rendered)
	}
}
