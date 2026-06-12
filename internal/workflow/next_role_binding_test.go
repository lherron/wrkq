package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

const nextRoleBindingTemplate = `{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "next_role_binding_test",
  "version": "1",
  "kind": "agent_first_workflow",
  "initial": { "status": "active", "phase": "pressure" },
  "roles": {
    "agent": { "description": "Writer", "actors": ["agent:pbc-writer"] },
    "pressure_reviewer": { "description": "Reviewer" }
  },
  "states": [
    { "status": "active", "phase": "pressure" },
    { "status": "closed", "phase": "finalized" }
  ],
  "obligationKinds": {
    "patch_decision": { "description": "Patch decision" }
  },
  "transitions": [
    {
      "id": "finalize_ready_pbc",
      "from": { "status": "active", "phase": "pressure" },
      "by": ["agent", "pressure_reviewer"],
      "responsibility": { "role": "agent", "scope": "task", "lane": "pbc-refinement" },
      "requires": [
        { "obligation": { "kind": "patch_decision", "status": "satisfied" } }
      ],
      "outcomes": [
        {
          "id": "finalized",
          "when": { "always": true },
          "to": { "status": "closed", "phase": "finalized" }
        }
      ]
    }
  ]
}`

func setupNextRoleBindingFixture(t *testing.T) (*Service, string, *db.DB) {
	t.Helper()
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "next_role_binding.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	svc := NewService(database)
	tplPath := filepath.Join(tmpDir, "next_role_binding_template.json")
	if err := os.WriteFile(tplPath, []byte(nextRoleBindingTemplate), 0644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	if _, err := svc.InstallTemplate(tplPath, "test-installer", nil); err != nil {
		t.Fatalf("InstallTemplate: %v", err)
	}

	actorUUID := "12121212-1212-4212-9212-000000000001"
	if _, err := database.Exec(`INSERT INTO actors (uuid, slug, role) VALUES (?, 'next-binding-actor', 'system')`, actorUUID); err != nil {
		t.Fatalf("insert actor: %v", err)
	}
	containerUUID := "34343434-3434-4434-9434-000000000001"
	if _, err := database.Exec(
		`INSERT INTO containers (uuid, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'next-binding-project', 'Next Binding Project', (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		containerUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert container: %v", err)
	}
	taskUUID := "56565656-5656-4565-9565-000000000001"
	if _, err := database.Exec(
		`INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, kind, created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'next-binding-task', 'Next Binding Task', ?, 'open', 2, 'task', ?, ?)`,
		taskUUID, containerUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, "next_role_binding_test@1", "test-installer"); err != nil {
		t.Fatalf("AttachTask: %v", err)
	}
	return svc, taskUUID, database
}

func TestNextAnnotatesBoundTransitionOwners(t *testing.T) {
	svc, taskUUID, database := setupNextRoleBindingFixture(t)
	if _, err := svc.StartRun(taskUUID, "pressure_reviewer", "agent:pbc-reviewer", StartRunOptions{DeliveryRef: "acp:pbc-reviewer"}); err != nil {
		t.Fatalf("StartRun pressure_reviewer: %v", err)
	}
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if _, err := database.Exec(`
		INSERT INTO workflow_obligations (id, instance_id, kind, owner_role, blocking, status, reason)
		VALUES ('obl_next_patch_decision', ?, 'patch_decision', 'product_owner', 1, 'satisfied', 'test satisfied obligation')
	`, inst.ID); err != nil {
		t.Fatalf("insert satisfied obligation: %v", err)
	}

	next, err := svc.Next(taskUUID, "")
	if err != nil {
		t.Fatalf("Next: %v", err)
	}

	var writer, reviewer bool
	for _, action := range next.Actions {
		if action.Kind != "transition" || action.ID == "" {
			continue
		}
		switch action.Owner.Role {
		case "agent":
			if action.Owner.Actor == "agent:pbc-writer" &&
				strings.Contains(action.Command, " finalize_ready_pbc --role agent --actor agent:pbc-writer --expect-revision 0") {
				writer = true
			}
		case "pressure_reviewer":
			if action.Owner.Actor == "agent:pbc-reviewer" && action.Owner.DeliveryRef == "acp:pbc-reviewer" &&
				strings.Contains(action.Command, " finalize_ready_pbc --role pressure_reviewer --actor agent:pbc-reviewer --expect-revision 0") {
				reviewer = true
			}
		}
	}
	if !writer {
		t.Fatalf("Next did not advertise statically allowed writer owner: %#v", next.Actions)
	}
	if !reviewer {
		t.Fatalf("Next did not advertise bound reviewer owner: %#v", next.Actions)
	}
}

func TestNextBlocksRoleOutsideTransitionByList(t *testing.T) {
	svc, taskUUID, _ := setupNextRoleBindingFixture(t)
	next, err := svc.Next(taskUUID, "product_owner")
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(next.Actions) != 0 {
		t.Fatalf("Next advertised actions for disallowed role: %#v", next.Actions)
	}
	for _, blocked := range next.BlockedTransitions {
		for _, blocker := range blocked.BlocksOn {
			if blocker.Kind == "role" && blocker.Ref == "product_owner" {
				return
			}
		}
	}
	t.Fatalf("Next did not report role blocker for product_owner: %#v", next.BlockedTransitions)
}
