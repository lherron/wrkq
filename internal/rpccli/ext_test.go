package rpccli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/clifunnel"
)

func TestExtUsageErrorsAreStructured(t *testing.T) {
	for _, args := range [][]string{
		{"put", "T-00001", "acp", "request", "main"},
		{"put", "T-00001", "acp", "request", "main", "--body", "{}", "--create", "--if-match", "1"},
		{"put", "T-00001", "acp", "request", "main", "--body", "[]"},
		{"get", "invalid", "acp", "request", "main"},
		{"delete", "T-00001", "acp", "request", "main", "--if-match", "-1"},
		{"list", "--limit", "501"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			cmd := NewRootCmd()
			cmd.SetArgs(append([]string{"ext"}, args...))
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			err := cmd.Execute()
			if err == nil {
				t.Fatal("expected usage error")
			}
			if code := clifunnel.ExitCode(err, 1); code != 2 {
				t.Fatalf("exit code %d: %v", code, err)
			}
			var output bytes.Buffer
			clifunnel.Report(&output, err, clifunnel.ErrorJSON, "WRKQ")
			if !strings.Contains(output.String(), `"code": "WRKQ_USAGE"`) {
				t.Fatalf("unstructured error: %s", &output)
			}
		})
	}
}

func TestExtRPCErrorKeepsCode(t *testing.T) {
	err := &Error{DomainID: "WRKQ_CONFLICT", Message: "extension etag mismatch"}
	var output bytes.Buffer
	clifunnel.Report(&output, err, clifunnel.ErrorJSON, "WRKQ")
	if !strings.Contains(output.String(), `"code": "WRKQ_CONFLICT"`) {
		t.Fatalf("lost RPC code: %s", &output)
	}
}

func TestCoversInputConventions(t *testing.T) {
	for _, raw := range []string{`{"tasks":["T-00001"]}`, `null`} {
		cmd := NewRootCmd()
		value, err := readJSONObjectValue(cmd, raw, "--covers", &stdinClaims{}, true)
		if err != nil || string(value) != raw {
			t.Fatalf("covers %s: %s, %v", raw, value, err)
		}
	}
	cmd := NewRootCmd()
	cmd.SetIn(strings.NewReader(`{"container":"P-00001"}`))
	claims := &stdinClaims{}
	if _, err := readJSONObjectValue(cmd, "-", "--covers", claims, true); err != nil {
		t.Fatal(err)
	}
	if _, err := readTextValue("-", "--description", cmd.InOrStdin(), claims); err == nil {
		t.Fatal("stdin consumed twice")
	}
}
