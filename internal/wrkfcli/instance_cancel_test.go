//go:build wrkq_local

package wrkfcli

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workflow"
)

// `wrkf instance cancel` forwards --expect-revision as the CAS precondition and
// --explanation into the durable cancel event: a stale revision is refused with
// nothing changed, the current one closes the instance cancelled.
func TestInstanceCancelForwardsCASAndRecordsExplanation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "wrkq.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	actorUUID := "00000000-0000-4000-8000-0000000000a0"
	projectUUID := "a3000000-0000-4000-8000-000000000011"
	taskUUID := "b3000000-0000-4000-8000-000000000011"
	if _, err := database.Exec(`
		INSERT INTO containers (uuid, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, 'instance-cancel-cli', 'Instance Cancel CLI',
		        (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)
	`, projectUUID, actorUUID, actorUUID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, 'instance-cancel-cli-task', 'Instance Cancel CLI Task', ?, 'open', 2, 'task', ?, ?)
	`, taskUUID, projectUUID, actorUUID, actorUUID); err != nil {
		t.Fatal(err)
	}
	var taskID string
	if err := database.QueryRow(`SELECT id FROM tasks WHERE uuid = ?`, taskUUID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	instance, err := workflow.NewService(database).AttachTask(taskID, workflow.BuiltinSimpleTaskV2TemplateRef, "agent:cli-cancel")
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"WRKF_PRINCIPAL_REF=agent:cli-cancel", "WRKF_HOOK_CATALOG=" + explicitEmptyHookCatalog(t)}
	cancel := func(revision string) wrkfResult {
		return runWrkf(t, "", env, "--db", dbPath, "--json", "--role", "coordinator",
			"instance", "cancel", taskID, "--instance", instance.ID, "--expect-revision", revision, "--explanation", "operator stopped never-cranked work")
	}

	requireWrkfRefusal(t, cancel("99"), "WRKF_STALE_REVISION")
	var status string
	if err := database.QueryRow(`SELECT status FROM workflow_instances WHERE id = ?`, instance.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "active" {
		t.Fatalf("stale cancel changed instance status to %q", status)
	}

	res := cancel("0")
	if res.exitCode != 0 {
		t.Fatalf("cancel at current revision exit %d: %s", res.exitCode, res.stderr)
	}
	var out struct {
		InstanceID string `json:"instanceId"`
		EventID    string `json:"eventId"`
		Revision   int64  `json:"revision"`
		State      struct {
			Status  string `json:"status"`
			Outcome string `json:"outcome"`
		} `json:"state"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &out); err != nil {
		t.Fatalf("cancel JSON: %v\n%s", err, res.stdout)
	}
	if out.InstanceID != instance.ID || out.Revision != 1 || out.State.Status != "closed" || out.State.Outcome != "cancelled" {
		t.Fatalf("cancel result = %+v, want %s closed/cancelled at revision 1", out, instance.ID)
	}
	var explanation string
	if err := database.QueryRow(`SELECT json_extract(payload_json, '$.explanation') FROM workflow_events WHERE id = ?`, out.EventID).Scan(&explanation); err != nil {
		t.Fatalf("read cancel event %s: %v", out.EventID, err)
	}
	if explanation != "operator stopped never-cranked work" {
		t.Fatalf("cancel event explanation = %q", explanation)
	}
}
