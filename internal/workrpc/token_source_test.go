package workrpc

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// T-06976: the stdio-forwarding path must be as diagnosable as the direct client
// transport — a forwarded 401 names the credential source, never its bytes.

// TestRemoteStdioErrorFrameNamesTokenSourceOnlyOnAuthFailure drives the remote
// `rpc --stdio` forwarder (ServeRemoteStdio) against a wrkqd stand-in and reads
// the error frame the caller receives: a 401 names the token source and its
// shadowing without leaking either token value; a 500 carries the HTTP status
// and no credential detail at all.
func TestRemoteStdioErrorFrameNamesTokenSourceOnlyOnAuthFailure(t *testing.T) {
	const envToken = "stale-env-token"
	const fileToken = "valid-file-token"
	tokenPath := filepath.Join(t.TempDir(), "node-token")
	if err := os.WriteFile(tokenPath, []byte(fileToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name      string
		status    int
		env       map[string]string
		want      []string
		forbidden []string
	}{
		{
			name:      "401 names env source shadowing the file",
			status:    http.StatusUnauthorized,
			env:       map[string]string{"WRKQD_TOKEN": envToken, "WRKQD_TOKEN_FILE": tokenPath},
			want:      []string{"authentication failed (HTTP 401)", "WRKQD_TOKEN env", "shadowed", tokenPath},
			forbidden: []string{envToken, fileToken},
		},
		{
			name:      "500 carries no credential detail",
			status:    http.StatusInternalServerError,
			env:       map[string]string{"WRKQD_TOKEN": "some-token", "WRKQD_TOKEN_FILE": ""},
			want:      []string{"HTTP 500"},
			forbidden: []string{"token:", "WRKQD_TOKEN", "some-token"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			}))
			t.Cleanup(srv.Close)

			token, _ := ResolveToken()
			in := `{"jsonrpc":"2.0","id":"req_1","method":"wrkq.task.show","params":{"task":"T-00001"}}` + "\n"
			var out bytes.Buffer
			if err := ServeRemoteStdio(t.Context(), strings.NewReader(in), &out, strings.TrimPrefix(srv.URL, "http://"), token, ""); err != nil {
				t.Fatalf("ServeRemoteStdio: %v", err)
			}
			var frame Response
			if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &frame); err != nil {
				t.Fatalf("decode forwarded frame %q: %v", out.String(), err)
			}
			if string(frame.ID) != `"req_1"` || frame.Error == nil {
				t.Fatalf("want an error frame for req_1, got %s", out.String())
			}
			got := frame.Error.Message
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("forwarded error message missing %q:\n%s", want, got)
				}
			}
			for _, bad := range tc.forbidden {
				if strings.Contains(out.String(), bad) {
					t.Errorf("forwarded error frame carries %q:\n%s", bad, out.String())
				}
			}
		})
	}
}
