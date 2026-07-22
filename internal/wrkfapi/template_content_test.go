package wrkfapi_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/wrkfapi"
)

func TestWorkflowContentBodyLimitIsServerAuthoritative(t *testing.T) {
	api := newTestAPI(t)
	_, err := api.WorkflowInstall(context.Background(), wrkfapi.WorkflowInstallParams{
		Body:       strings.Repeat("x", wrkfapi.MaxTemplateBodyBytes+1),
		SourceName: "oversize.workflow.json",
	})
	if err == nil || !strings.Contains(err.Error(), "1048576-byte template body limit") {
		t.Fatalf("oversize install error=%v, want body-limit refusal", err)
	}
}
