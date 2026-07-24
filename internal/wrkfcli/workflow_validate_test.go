package wrkfcli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workflow"
)

func TestWorkflowValidateCLIEnforcesSuspensionReasonReferences(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "workflow-validate.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	oldDB, oldJSON, oldHook := flagDB, flagJSON, flagHookCatalog
	t.Cleanup(func() {
		flagDB, flagJSON, flagHookCatalog = oldDB, oldJSON, oldHook
	})
	flagDB = dbPath
	flagJSON = true
	flagHookCatalog = ""
	emptyCatalog := filepath.Join(t.TempDir(), "empty-hook-catalog.json")
	if err := os.WriteFile(emptyCatalog, []byte(`{"hooks":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WRKF_HOOK_CATALOG", emptyCatalog)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ASP_PROJECT", "")
	t.Setenv("WRKQ_PROJECT", "")

	validBody, err := os.ReadFile(filepath.Join("..", "workflow", "builtins", "wrkq-simple-task-v5.workflow.json"))
	if err != nil {
		t.Fatal(err)
	}
	unusedBody := addSuspensionReason(t, validBody, "never_used")

	tests := []struct {
		name       string
		body       []byte
		wantValid  bool
		wantErrMsg string
	}{
		{name: "used declaration", body: validBody, wantValid: true},
		{
			name:       "unused declaration",
			body:       unusedBody,
			wantValid:  false,
			wantErrMsg: `suspension reason "never_used" is declared but not referenced by any outcome`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			templatePath := filepath.Join(t.TempDir(), "workflow.json")
			if err := os.WriteFile(templatePath, tc.body, 0o600); err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			cmd := workflowCmd()
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			cmd.SetArgs([]string{"validate", templatePath})
			executeErr := cmd.Execute()
			if tc.wantValid && executeErr != nil {
				t.Fatalf("workflow validate returned %v; stderr=%q", executeErr, stderr.String())
			}
			if !tc.wantValid && executeErr == nil {
				t.Fatalf("workflow validate error = %v, want validation failure; stderr=%q", executeErr, stderr.String())
			}

			var result workflow.ValidateResult
			if err := json.NewDecoder(bytes.NewReader(stdout.Bytes())).Decode(&result); err != nil {
				t.Fatalf("decode workflow validate output %q: %v", stdout.String(), err)
			}
			if result.Valid != tc.wantValid {
				t.Fatalf("workflow validate result = %+v, want valid=%t", result, tc.wantValid)
			}
			if tc.wantErrMsg != "" && !containsString(result.Errors, tc.wantErrMsg) {
				t.Fatalf("workflow validate errors = %v, want %q", result.Errors, tc.wantErrMsg)
			}
		})
	}
}

func addSuspensionReason(t *testing.T, body []byte, reason string) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	suspension := document["suspension"].(map[string]any)
	suspension["reasons"] = append(suspension["reasons"].([]any), reason)
	mutated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return mutated
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
