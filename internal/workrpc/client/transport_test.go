package client

import (
	"encoding/json"
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
		"capabilities":       map[string]bool{"wrkq": true, "wrkf": true},
		"methods":            []string{"wrkq.task.show", "wrkf.workflow.list"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
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
