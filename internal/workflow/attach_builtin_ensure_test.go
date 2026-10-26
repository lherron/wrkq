//go:build wrkq_local

package workflow

import (
	"strings"
	"testing"
)

func TestAttachTaskUnknownNonBuiltinStillErrors(t *testing.T) {
	svc, taskUUID := actionFixture(t)

	if _, err := svc.AttachTask(taskUUID, "no-such-template@9", "agent:t"); err == nil || !strings.Contains(err.Error(), "template not found") {
		t.Fatalf("AttachTask(unknown) error = %v, want template not found", err)
	}
}

func TestAttachTaskEnsureDoesNotResurrectDiscontinuedBuiltin(t *testing.T) {
	svc, taskUUID := actionFixture(t)

	if _, _, err := svc.EnsureBuiltinTemplate(BuiltinSimpleTaskV2TemplateRef, "agent:t"); err != nil {
		t.Fatalf("EnsureBuiltinTemplate(v2): %v", err)
	}
	if err := svc.DiscontinueTemplate("wrkq-simple-task", "2", "agent:t"); err != nil {
		t.Fatalf("DiscontinueTemplate(v2): %v", err)
	}

	if _, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV2TemplateRef, "agent:t"); err == nil || !strings.Contains(err.Error(), "discontinued") {
		t.Fatalf("AttachTask(discontinued builtin) error = %v, want discontinued guard", err)
	}

	templates, err := svc.ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	for _, row := range templates {
		if row["id"] == "wrkq-simple-task" && row["version"] == "2" {
			if _, still := row["discontinuedAt"]; !still {
				t.Fatalf("wrkq-simple-task@2 lost discontinuedAt after attach attempt: %v", row)
			}
		}
	}
}
