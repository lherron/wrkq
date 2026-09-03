package client

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/workrpc"
)

func healthyInitializeResult(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"protocolVersion":    workrpc.ProtocolVersion,
		"protocolSchemaHash": workrpc.ProtocolSchemaHash(),
		"server":             map[string]string{"version": "test", "revision": "abc123"},
		"capabilities":       map[string]bool{"wrkq": true, "wrkf": true},
		"methods":            []string{"wrkq.task.show", "wrkf.workflow.list"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const testServerRevision = "0123456789abcdef0123456789abcdef01234567"

func expectedProtocolMismatch(serverProtocol, serverHash, revision string) string {
	if serverHash == "" {
		serverHash = "unknown"
	}
	revisionDetail := revision
	updateTarget := revision
	if revision == "" || revision == "unknown" {
		revisionDetail = "unknown — run `wrkqd version` on the wrkqd host"
		updateTarget = "the revision reported by `wrkqd version`"
	}
	return "wrkq client is incompatible with wrkqd (rpc protocol mismatch)\n" +
		"  client: protocol " + workrpc.ProtocolVersion + ", schema " + workrpc.ProtocolSchemaHash() + "\n" +
		"  server: protocol " + serverProtocol + ", schema " + serverHash + ", wrkqd revision " + revisionDetail + "\n" +
		"  Update this host's wrkq checkout to at least " + updateTarget + " and run `just install`.\n" +
		"  If this host is deliberately ahead of the canonical wrkqd, redeploy wrkqd from your revision instead — host binaries never lead wrkqd (conventions-full.md)."
}

func TestInitializeProtocolMismatchMessages(t *testing.T) {
	serverProtocol := "2026-09-30"
	serverHash := "sha256:" + strings.Repeat("a", 64)
	cases := []struct {
		name string
		err  func(t *testing.T) error
		want string
	}{
		{
			name: "server rejects protocol version with identity",
			err: func(t *testing.T) error {
				data, err := json.Marshal(map[string]any{
					"code":                     "WRKF_VALIDATION",
					"retryable":                false,
					"expected":                 serverProtocol,
					"actual":                   workrpc.ProtocolVersion,
					"serverProtocolSchemaHash": serverHash,
					"serverRevision":           testServerRevision,
				})
				if err != nil {
					t.Fatal(err)
				}
				return validateInitializeResponse(nil, errorFromRPC(&workrpc.RPCError{Data: data}), WrkqProfile)
			},
			want: expectedProtocolMismatch(serverProtocol, serverHash, testServerRevision),
		},
		{
			name: "old server rejects protocol version without identity",
			err: func(t *testing.T) error {
				data := json.RawMessage(`{"code":"WRKF_VALIDATION","retryable":false,"expected":"2026-09-30","actual":"2026-06-30"}`)
				return validateInitializeResponse(nil, errorFromRPC(&workrpc.RPCError{Data: data}), WrkqProfile)
			},
			want: expectedProtocolMismatch(serverProtocol, "", ""),
		},
		{
			name: "server returns mismatched schema",
			err: func(t *testing.T) error {
				var init map[string]any
				if err := json.Unmarshal(healthyInitializeResult(t), &init); err != nil {
					t.Fatal(err)
				}
				init["protocolSchemaHash"] = serverHash
				init["server"] = map[string]string{"version": "test", "revision": testServerRevision}
				raw, err := json.Marshal(init)
				if err != nil {
					t.Fatal(err)
				}
				return validateInitializeResult(raw, WrkqProfile)
			},
			want: expectedProtocolMismatch(workrpc.ProtocolVersion, serverHash, testServerRevision),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.err(t)
			if err == nil {
				t.Fatal("expected protocol mismatch")
			}
			var mismatch *ProtocolMismatchError
			if !errors.As(err, &mismatch) {
				t.Fatalf("error type = %T, want *ProtocolMismatchError", err)
			}
			if got := err.Error(); got != tc.want {
				t.Fatalf("protocol mismatch message:\n--- got ---\n%s\n--- want ---\n%s", got, tc.want)
			}
		})
	}
}

func TestInitializeMatchingServerSucceeds(t *testing.T) {
	if err := validateInitializeResult(healthyInitializeResult(t), WrkqProfile); err != nil {
		t.Fatalf("matching initialize result: %v", err)
	}
}

func TestInitializeTransportsReturnTypedProtocolMismatch(t *testing.T) {
	serverProtocol := "2026-09-30"
	serverHash := "sha256:" + strings.Repeat("a", 64)
	data, err := json.Marshal(map[string]any{
		"code":                     "WRKF_VALIDATION",
		"retryable":                false,
		"expected":                 serverProtocol,
		"actual":                   workrpc.ProtocolVersion,
		"serverProtocolSchemaHash": serverHash,
		"serverRevision":           testServerRevision,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := json.Marshal(workrpc.Response{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Error:   &workrpc.RPCError{Code: -32602, Message: "invalid protocolVersion", Data: data},
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("local stdio", func(t *testing.T) {
		c := &conn{w: io.Discard, br: bufio.NewReader(bytes.NewReader(append(response, '\n'))), profile: WrkqProfile}
		assertTypedProtocolMismatch(t, c.initialize())
	})

	t.Run("remote http", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(response)
		}))
		defer srv.Close()
		_, err := NewRemote(strings.TrimPrefix(srv.URL, "http://"), "", WrkqProfile)
		assertTypedProtocolMismatch(t, err)
	})
}

func assertTypedProtocolMismatch(t *testing.T, err error) {
	t.Helper()
	var mismatch *ProtocolMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("error = %v (%T), want *ProtocolMismatchError", err, err)
	}
}

func TestInitializeSchemaMismatchNamesBothHashesAndServerRevision(t *testing.T) {
	var init map[string]any
	if err := json.Unmarshal(healthyInitializeResult(t), &init); err != nil {
		t.Fatal(err)
	}
	serverHash := "sha256:" + strings.Repeat("f", 64)
	init["protocolSchemaHash"] = serverHash
	raw, err := json.Marshal(init)
	if err != nil {
		t.Fatal(err)
	}

	err = validateInitializeResult(raw, WrkqProfile)
	if err == nil {
		t.Fatal("expected schema mismatch")
	}
	for _, want := range []string{workrpc.ProtocolSchemaHash(), serverHash, "abc123"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("schema mismatch error %q missing %q", err, want)
		}
	}
}

func TestErrorCodePreservesRemoteDomainIdentifier(t *testing.T) {
	err := errorFromRPC(&workrpc.RPCError{Data: json.RawMessage(`{"code":"WRKF_VALIDATION","retryable":false}`)})
	if err.Code() != "WRKF_VALIDATION" {
		t.Fatalf("Code()=%q want WRKF_VALIDATION", err.Code())
	}
}

func TestInitializeProfilesProbeTheirOwnSurface(t *testing.T) {
	base := healthyInitializeResult(t)
	for _, profile := range []Profile{WrkqProfile, WrkfProfile} {
		if err := validateInitializeResult(base, profile); err != nil {
			t.Fatalf("healthy %s profile: %v", profile.Capability, err)
		}
	}

	var init map[string]any
	if err := json.Unmarshal(base, &init); err != nil {
		t.Fatal(err)
	}
	init["methods"] = []string{"wrkq.task.show"}
	missingMethod, _ := json.Marshal(init)
	if err := validateInitializeResult(missingMethod, WrkfProfile); err == nil || !strings.Contains(err.Error(), "wrkf.workflow.list") {
		t.Fatalf("wrkf method probe error = %v", err)
	}

	init["methods"] = []string{"wrkq.task.show", "wrkf.workflow.list"}
	init["capabilities"] = map[string]bool{"wrkq": true, "wrkf": false}
	missingCapability, _ := json.Marshal(init)
	if err := validateInitializeResult(missingCapability, WrkfProfile); err == nil || !strings.Contains(err.Error(), "wrkf capability") {
		t.Fatalf("wrkf capability probe error = %v", err)
	}
}

func TestTokenFromEnvPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WRKQD_TOKEN_FILE", path)
	t.Setenv("WRKQD_TOKEN", "")
	if got := TokenFromEnv(); got != "file-token" {
		t.Fatalf("file token = %q", got)
	}
	t.Setenv("WRKQD_TOKEN", "explicit-inline")
	if got := TokenFromEnv(); got != "explicit-inline" {
		t.Fatalf("explicit inline token = %q", got)
	}
}
