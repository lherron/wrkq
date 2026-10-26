//go:build wrkq_local

package wrkqapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskPatchRejectsLegacyLinkageFields(t *testing.T) {
	fields := []string{"cpProjectId", "cpWorkItemId", "cpRunId", "sessionId", "runStatus"}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			var patch TaskPatch
			err := json.Unmarshal([]byte(`{"`+field+`":"legacy"}`), &patch)
			if err == nil {
				t.Fatalf("expected unsupported field error")
			}
			if !strings.Contains(err.Error(), `unsupported task patch field "`+field+`"`) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
