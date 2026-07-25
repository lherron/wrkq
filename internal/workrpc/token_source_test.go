package workrpc

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T-06976: the stdio-forwarding path must be as diagnosable as the direct client
// transport — a forwarded 401 names the credential source, never its bytes.

func TestForwardedAuthErrorNamesTokenSourceAndRedacts(t *testing.T) {
	const envToken = "stale-env-token"
	const fileToken = "valid-file-token"
	path := filepath.Join(t.TempDir(), "node-token")
	if err := os.WriteFile(path, []byte(fileToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WRKQD_TOKEN", envToken)
	t.Setenv("WRKQD_TOKEN_FILE", path)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	token, _ := ResolveToken()
	_, err := forwardRemoteFrame(t.Context(), http.DefaultClient,
		srv.URL+"/v1/rpc", token, Request{JSONRPC: "2.0", Method: "wrkq.task.show"})
	if err == nil {
		t.Fatal("expected a forwarded auth failure")
	}

	got := err.Error()
	for _, want := range []string{"authentication failed (HTTP 401)", "WRKQD_TOKEN env", "shadowed", path} {
		if !strings.Contains(got, want) {
			t.Errorf("forwarded auth error missing %q:\n%s", want, got)
		}
	}
	for _, secret := range []string{envToken, fileToken} {
		if strings.Contains(got, secret) {
			t.Fatalf("forwarded auth error leaked a token value %q:\n%s", secret, got)
		}
	}
}

// A non-auth forwarded failure carries no credential detail.
func TestForwardedNonAuthErrorOmitsCredentialDetail(t *testing.T) {
	t.Setenv("WRKQD_TOKEN", "some-token")
	t.Setenv("WRKQD_TOKEN_FILE", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	_, err := forwardRemoteFrame(t.Context(), http.DefaultClient,
		srv.URL+"/v1/rpc", "some-token", Request{JSONRPC: "2.0", Method: "wrkq.task.show"})
	if err == nil {
		t.Fatal("expected a forwarded failure")
	}
	got := err.Error()
	if !strings.Contains(got, "HTTP 500") {
		t.Fatalf("expected HTTP 500, got: %s", got)
	}
	if strings.Contains(got, "token:") || strings.Contains(got, "WRKQD_TOKEN") {
		t.Errorf("non-auth forwarded error should not carry credential detail:\n%s", got)
	}
}

func TestResolveTokenPrecedenceAndDiagnostics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(path, []byte("file-side\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("WRKQD_TOKEN_FILE", path)
	t.Setenv("WRKQD_TOKEN", "env-side")
	token, diag := ResolveToken()
	if token != "env-side" {
		t.Fatalf("env token must win, got %q", token)
	}
	if diag.Source != "WRKQD_TOKEN env" || !diag.FileShadowed {
		t.Fatalf("expected shadowed env-source diagnostic, got %+v", diag)
	}

	t.Setenv("WRKQD_TOKEN", "")
	token, diag = ResolveToken()
	if token != "file-side" {
		t.Fatalf("file token must be used, got %q", token)
	}
	if diag.Source != "WRKQD_TOKEN_FILE" || diag.FileShadowed {
		t.Fatalf("expected unshadowed file-source diagnostic, got %+v", diag)
	}

	t.Setenv("WRKQD_TOKEN_FILE", "")
	if token, diag = ResolveToken(); token != "" || diag.Source != "" {
		t.Fatalf("expected no token, got %q / %+v", token, diag)
	}
	if !strings.Contains(diag.String(), "no token") {
		t.Errorf("empty diagnostic should say 'no token': %s", diag.String())
	}
}
