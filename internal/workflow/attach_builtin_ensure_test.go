//go:build wrkq_local

package workflow

import (
	"strings"
	"testing"
)

func TestAttachTaskEnsuresBuiltinTemplate(t *testing.T) {
	svc, taskUUID := actionFixture(t)

	inst, err := svc.AttachTask(taskUUID, BuiltinRoom2BoxTemplateRef, "agent:t")
	if err != nil {
		t.Fatalf("AttachTask(%s) on fresh DB: %v", BuiltinRoom2BoxTemplateRef, err)
	}
	if inst.TemplateID != "room-2box" || inst.TemplateVersion != "1" {
		t.Fatalf("instance template = %s@%s, want room-2box@1", inst.TemplateID, inst.TemplateVersion)
	}

	templates, err := svc.ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	found := false
	for _, row := range templates {
		if row["id"] == "room-2box" && row["version"] == "1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("room-2box@1 not installed after attach; templates = %v", templates)
	}
}

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