package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// directEvidenceTemplate exercises E1 (producibleBy) and E3 (linkageRefs).
// behavior_note is produced by the agent; review references it by id and is
// product_owner-only.
const directEvidenceTemplate = `{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "direct_evidence_test",
  "version": "1",
  "kind": "agent_first_workflow",
  "initial": { "status": "active", "phase": "draft" },
  "roles": {
    "agent": { "description": "Drafts the note" },
    "product_owner": { "description": "Reviews the note" }
  },
  "states": [
    { "status": "active", "phase": "draft" },
    { "status": "closed", "outcome": "done" }
  ],
  "evidenceKinds": {
    "behavior_note": {
      "description": "Agent-authored note"
    },
    "review": {
      "description": "Product-owner review of a specific behavior note",
      "producibleBy": ["product_owner"],
      "facts": {
        "required": ["verdict"],
        "properties": {
          "verdict": { "type": "string", "enum": ["ready", "needs_patch", "too_vague"] }
        }
      },
      "linkageRefs": [
        { "path": "/basedOnBehaviorNoteId", "resolvesToKind": "behavior_note", "required": true }
      ]
    }
  },
  "transitions": [
    {
      "id": "finish",
      "from": { "status": "active", "phase": "draft" },
      "by": ["product_owner"],
      "requires": [ { "evidence": { "kind": "review" } } ],
      "outcomes": [
        { "id": "done", "when": { "always": true }, "to": { "status": "closed", "outcome": "done" } }
      ]
    }
  ]
}`

func setupDirectEvidenceFixture(t *testing.T) (*Service, string) {
	t.Helper()
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "direct_evidence.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	svc := NewService(database)
	tplPath := filepath.Join(tmpDir, "direct_evidence_template.json")
	if err := os.WriteFile(tplPath, []byte(directEvidenceTemplate), 0644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	if _, err := svc.InstallTemplate(tplPath, "installer", nil); err != nil {
		t.Fatalf("InstallTemplate: %v", err)
	}

	actorUUID := "dddddddd-dddd-4ddd-8ddd-000000000011"
	if _, err := database.Exec(`INSERT INTO actors (uuid, slug, role) VALUES (?, 'de-actor', 'system')`, actorUUID); err != nil {
		t.Fatalf("insert actor: %v", err)
	}
	containerUUID := "eeeeeeee-eeee-4eee-8eee-000000000011"
	if _, err := database.Exec(
		`INSERT INTO containers (uuid, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'de-project', 'DE Project', (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		containerUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert container: %v", err)
	}
	taskUUID := "ffffffff-ffff-4fff-8fff-000000000011"
	if _, err := database.Exec(
		`INSERT INTO tasks (uuid, slug, title, description, project_uuid, state, priority, kind,
		                    created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'de-task', 'DE Task', 'body', ?, 'open', 2, 'task', ?, ?)`,
		taskUUID, containerUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, "direct_evidence_test@1", "installer"); err != nil {
		t.Fatalf("AttachTask: %v", err)
	}
	return svc, taskUUID
}

// E1 — producibleBy conformance.
func TestProducibleByRejectsNonProducerRole(t *testing.T) {
	svc, task := setupDirectEvidenceFixture(t)

	note, err := svc.AddEvidence(AddEvidenceParams{TaskSelector: task, Kind: "behavior_note", Ref: "urn:note:1", Actor: "agent-a", Role: "agent"})
	if err != nil {
		t.Fatalf("AddEvidence behavior_note: %v", err)
	}

	// Wrong role for a producibleBy-restricted kind.
	_, err = svc.AddEvidence(AddEvidenceParams{
		TaskSelector: task, Kind: "review", Ref: "urn:rev:1",
		Facts: `{"verdict":"ready"}`,
		Data:  `{"basedOnBehaviorNoteId":"` + note.ID + `"}`,
		Actor: "agent-a", Role: "agent",
	})
	if got := wrkfCode(err); got != wrkfCodeKindRoleDenied {
		t.Fatalf("non-producer role code = %q, want %s (err=%v)", got, wrkfCodeKindRoleDenied, err)
	}
	if !strings.Contains(err.Error(), "not producible by role agent") {
		t.Fatalf("message should name the supplied role: %v", err)
	}

	// Empty role is rejected when producers are declared.
	_, err = svc.AddEvidence(AddEvidenceParams{
		TaskSelector: task, Kind: "review", Ref: "urn:rev:2",
		Facts: `{"verdict":"ready"}`,
		Data:  `{"basedOnBehaviorNoteId":"` + note.ID + `"}`,
		Actor: "anon",
	})
	if got := wrkfCode(err); got != wrkfCodeKindRoleDenied {
		t.Fatalf("empty role code = %q, want %s", got, wrkfCodeKindRoleDenied)
	}

	// Authorized role succeeds.
	if _, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: task, Kind: "review", Ref: "urn:rev:3",
		Facts: `{"verdict":"ready"}`,
		Data:  `{"basedOnBehaviorNoteId":"` + note.ID + `"}`,
		Actor: "po-a", Role: "product_owner",
	}); err != nil {
		t.Fatalf("authorized product_owner add: %v", err)
	}
}

func TestProducibleByUnsetAllowsAllRoles(t *testing.T) {
	svc, task := setupDirectEvidenceFixture(t)
	// behavior_note declares no producibleBy → any role allowed.
	if _, err := svc.AddEvidence(AddEvidenceParams{TaskSelector: task, Kind: "behavior_note", Ref: "urn:note:x", Actor: "anyone", Role: "agent"}); err != nil {
		t.Fatalf("unrestricted kind add should succeed: %v", err)
	}
}

// E3 — data linkage resolution.
func TestLinkageRefRejectsDanglingReference(t *testing.T) {
	svc, task := setupDirectEvidenceFixture(t)

	_, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: task, Kind: "review", Ref: "urn:rev:dangling",
		Facts: `{"verdict":"ready"}`,
		Data:  `{"basedOnBehaviorNoteId":"ev-DOESNOTEXIST"}`,
		Actor: "po-a", Role: "product_owner",
	})
	if got := wrkfCode(err); got != wrkfCodeLinkageUnresolved {
		t.Fatalf("dangling ref code = %q, want %s (err=%v)", got, wrkfCodeLinkageUnresolved, err)
	}
	if !strings.Contains(err.Error(), "ev-DOESNOTEXIST") {
		t.Fatalf("message should name the bad ref: %v", err)
	}
}

func TestLinkageRefRequiredMissing(t *testing.T) {
	svc, task := setupDirectEvidenceFixture(t)
	_, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: task, Kind: "review", Ref: "urn:rev:missing",
		Facts: `{"verdict":"ready"}`,
		Data:  `{}`,
		Actor: "po-a", Role: "product_owner",
	})
	if got := wrkfCode(err); got != wrkfCodeLinkageUnresolved {
		t.Fatalf("missing required ref code = %q, want %s (err=%v)", got, wrkfCodeLinkageUnresolved, err)
	}
}

func TestLinkageRefWrongKind(t *testing.T) {
	svc, task := setupDirectEvidenceFixture(t)
	// Create a review first, then reference IT (wrong kind) from another review.
	note, err := svc.AddEvidence(AddEvidenceParams{TaskSelector: task, Kind: "behavior_note", Ref: "urn:note:k", Actor: "agent-a", Role: "agent"})
	if err != nil {
		t.Fatalf("seed note: %v", err)
	}
	rev, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: task, Kind: "review", Ref: "urn:rev:k1",
		Facts: `{"verdict":"ready"}`,
		Data:  `{"basedOnBehaviorNoteId":"` + note.ID + `"}`,
		Actor: "po-a", Role: "product_owner",
	})
	if err != nil {
		t.Fatalf("seed review: %v", err)
	}
	_, err = svc.AddEvidence(AddEvidenceParams{
		TaskSelector: task, Kind: "review", Ref: "urn:rev:k2",
		Facts: `{"verdict":"ready"}`,
		Data:  `{"basedOnBehaviorNoteId":"` + rev.ID + `"}`, // points at a review, not a behavior_note
		Actor: "po-a", Role: "product_owner",
	})
	if got := wrkfCode(err); got != wrkfCodeLinkageUnresolved {
		t.Fatalf("wrong-kind ref code = %q, want %s (err=%v)", got, wrkfCodeLinkageUnresolved, err)
	}
	if !strings.Contains(err.Error(), "expected behavior_note") {
		t.Fatalf("message should name expected kind: %v", err)
	}
}

func TestLinkageRefValidReferenceSucceeds(t *testing.T) {
	svc, task := setupDirectEvidenceFixture(t)
	note, err := svc.AddEvidence(AddEvidenceParams{TaskSelector: task, Kind: "behavior_note", Ref: "urn:note:ok", Actor: "agent-a", Role: "agent"})
	if err != nil {
		t.Fatalf("seed note: %v", err)
	}
	if _, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: task, Kind: "review", Ref: "urn:rev:ok",
		Facts: `{"verdict":"ready"}`,
		Data:  `{"basedOnBehaviorNoteId":"` + note.ID + `"}`,
		Actor: "po-a", Role: "product_owner",
	}); err != nil {
		t.Fatalf("valid linkage add should succeed: %v", err)
	}
}

// E3 — latest/staleness constraint. Uses a template where review links to the
// CURRENT behavior_note.
const stalenessTemplate = `{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "staleness_test",
  "version": "1",
  "kind": "agent_first_workflow",
  "initial": { "status": "active", "phase": "draft" },
  "roles": { "agent": {} },
  "states": [ { "status": "active", "phase": "draft" }, { "status": "closed", "outcome": "done" } ],
  "evidenceKinds": {
    "behavior_note": { "description": "agent note" },
    "review": {
      "description": "review of the LATEST note",
      "linkageRefs": [ { "path": "/noteId", "resolvesToKind": "behavior_note", "latest": true } ]
    }
  },
  "transitions": [
    { "id": "finish", "from": { "status": "active", "phase": "draft" }, "by": ["agent"],
      "outcomes": [ { "id": "done", "when": { "always": true }, "to": { "status": "closed", "outcome": "done" } } ] }
  ]
}`

func setupStalenessFixture(t *testing.T) (*Service, string) {
	t.Helper()
	tmpDir := t.TempDir()
	database, err := db.Open(filepath.Join(tmpDir, "staleness.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	svc := NewService(database)
	tplPath := filepath.Join(tmpDir, "staleness_template.json")
	if err := os.WriteFile(tplPath, []byte(stalenessTemplate), 0644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	if _, err := svc.InstallTemplate(tplPath, "installer", nil); err != nil {
		t.Fatalf("InstallTemplate: %v", err)
	}
	actorUUID := "dddddddd-dddd-4ddd-8ddd-000000000022"
	if _, err := database.Exec(`INSERT INTO actors (uuid, slug, role) VALUES (?, 'st-actor', 'system')`, actorUUID); err != nil {
		t.Fatalf("insert actor: %v", err)
	}
	containerUUID := "eeeeeeee-eeee-4eee-8eee-000000000022"
	if _, err := database.Exec(
		`INSERT INTO containers (uuid, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'st-project', 'ST Project', (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		containerUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert container: %v", err)
	}
	taskUUID := "ffffffff-ffff-4fff-8fff-000000000022"
	if _, err := database.Exec(
		`INSERT INTO tasks (uuid, slug, title, description, project_uuid, state, priority, kind,
		                    created_by_actor_uuid, updated_by_actor_uuid)
		 VALUES (?, 'st-task', 'ST Task', 'body', ?, 'open', 2, 'task', ?, ?)`,
		taskUUID, containerUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, "staleness_test@1", "installer"); err != nil {
		t.Fatalf("AttachTask: %v", err)
	}
	return svc, taskUUID
}

func TestLinkageLatestRejectsSupersededAndAcceptsCurrent(t *testing.T) {
	svc, task := setupStalenessFixture(t)
	first, err := svc.AddEvidence(AddEvidenceParams{TaskSelector: task, Kind: "behavior_note", Ref: "urn:n1", Actor: "ag", Role: "agent"})
	if err != nil {
		t.Fatalf("first note: %v", err)
	}
	second, err := svc.AddEvidence(AddEvidenceParams{TaskSelector: task, Kind: "behavior_note", Ref: "urn:n2", Actor: "ag", Role: "agent"})
	if err != nil {
		t.Fatalf("second note: %v", err)
	}

	// Referencing the superseded first note is rejected as stale.
	_, err = svc.AddEvidence(AddEvidenceParams{
		TaskSelector: task, Kind: "review", Ref: "urn:r:old",
		Data: `{"noteId":"` + first.ID + `"}`, Actor: "ag", Role: "agent",
	})
	if got := wrkfCode(err); got != wrkfCodeLinkageStale {
		t.Fatalf("superseded ref code = %q, want %s (err=%v)", got, wrkfCodeLinkageStale, err)
	}
	if !strings.Contains(err.Error(), second.ID) {
		t.Fatalf("stale message should name the current id %s: %v", second.ID, err)
	}

	// Referencing the current (latest) note succeeds.
	if _, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: task, Kind: "review", Ref: "urn:r:new",
		Data: `{"noteId":"` + second.ID + `"}`, Actor: "ag", Role: "agent",
	}); err != nil {
		t.Fatalf("current ref should succeed: %v", err)
	}
}

func TestInstallValidationLatestRequiresKind(t *testing.T) {
	bad := `{
	  "schemaVersion": "wrkf.workflow-template.v0",
	  "id": "bad_latest",
	  "version": "1",
	  "kind": "agent_first_workflow",
	  "initial": { "status": "active", "phase": "p" },
	  "roles": { "agent": {} },
	  "states": [ { "status": "active", "phase": "p" }, { "status": "closed", "outcome": "x" } ],
	  "evidenceKinds": { "k": { "linkageRefs": [ { "path": "/ref", "latest": true } ] } },
	  "transitions": [
	    { "id": "t", "from": { "status": "active", "phase": "p" }, "by": ["agent"],
	      "outcomes": [ { "id": "o", "when": { "always": true }, "to": { "status": "closed", "outcome": "x" } } ] }
	  ]
	}`
	var tpl Template
	if err := json.Unmarshal([]byte(bad), &tpl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	joined := strings.Join(ValidateTemplate(&tpl, []byte(bad), nil), "\n")
	if !strings.Contains(joined, "latest requires resolvesToKind") {
		t.Errorf("expected latest-requires-kind validation error, got:\n%s", joined)
	}
}

// F1/F2 — structured error detail with fix hint.
func TestStructuredErrorDetailOnMissingFact(t *testing.T) {
	svc, task := setupDirectEvidenceFixture(t)
	note, _ := svc.AddEvidence(AddEvidenceParams{TaskSelector: task, Kind: "behavior_note", Ref: "urn:note:f", Actor: "agent-a", Role: "agent"})

	_, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: task, Kind: "review", Ref: "urn:rev:f",
		Facts: `{}`, // missing required verdict
		Data:  `{"basedOnBehaviorNoteId":"` + note.ID + `"}`,
		Actor: "po-a", Role: "product_owner",
	})
	detail, ok := AsErrorDetail(err)
	if !ok {
		t.Fatalf("expected structured error detail, got %v", err)
	}
	if detail.Code != wrkfCodeValidation {
		t.Fatalf("detail.Code = %q, want %s", detail.Code, wrkfCodeValidation)
	}
	if detail.Field != "facts.verdict" {
		t.Fatalf("detail.Field = %q, want facts.verdict", detail.Field)
	}
	if len(detail.Allowed) != 3 || detail.Allowed[0] != "ready" {
		t.Fatalf("detail.Allowed = %v, want [ready needs_patch too_vague]", detail.Allowed)
	}
	if !strings.Contains(detail.Fix, "facts.verdict") {
		t.Fatalf("detail.Fix should mention the field+enum: %q", detail.Fix)
	}
}

// F4 — JSON parse errors carry a byte offset.
func TestFactsParseErrorIncludesOffset(t *testing.T) {
	svc, task := setupDirectEvidenceFixture(t)
	_, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: task, Kind: "behavior_note", Ref: "urn:note:bad",
		Facts: `{"x": }`, // malformed
		Actor: "agent-a", Role: "agent",
	})
	if err == nil || !strings.Contains(err.Error(), "offset") {
		t.Fatalf("malformed facts should report a byte offset: %v", err)
	}
}

// F3 — evidence schema query returns the contract.
func TestEvidenceSchemaReturnsContract(t *testing.T) {
	svc, task := setupDirectEvidenceFixture(t)
	schema, err := svc.EvidenceSchema(task, "review")
	if err != nil {
		t.Fatalf("EvidenceSchema: %v", err)
	}
	if len(schema.ProducibleBy) != 1 || schema.ProducibleBy[0] != "product_owner" {
		t.Fatalf("schema.ProducibleBy = %v", schema.ProducibleBy)
	}
	if len(schema.LinkageRefs) != 1 || schema.LinkageRefs[0].Path != "/basedOnBehaviorNoteId" {
		t.Fatalf("schema.LinkageRefs = %v", schema.LinkageRefs)
	}
	if schema.Facts == nil || len(schema.Facts.Required) != 1 {
		t.Fatalf("schema.Facts missing required: %+v", schema.Facts)
	}

	// Unknown kind reports a structured validation error listing declared kinds.
	_, err = svc.EvidenceSchema(task, "nope")
	detail, ok := AsErrorDetail(err)
	if !ok || detail.Code != wrkfCodeValidation {
		t.Fatalf("unknown kind should be a structured validation error: %v", err)
	}
	if len(detail.Allowed) == 0 {
		t.Fatalf("unknown kind error should list declared kinds")
	}
}

// Install-time validation rejects malformed producibleBy / linkageRefs.
func TestInstallValidationProducibleByAndLinkage(t *testing.T) {
	bad := `{
	  "schemaVersion": "wrkf.workflow-template.v0",
	  "id": "bad_template",
	  "version": "1",
	  "kind": "agent_first_workflow",
	  "initial": { "status": "active", "phase": "p" },
	  "roles": { "agent": {} },
	  "states": [ { "status": "active", "phase": "p" }, { "status": "closed", "outcome": "x" } ],
	  "evidenceKinds": {
	    "k": {
	      "producibleBy": ["ghost_role", ""],
	      "linkageRefs": [
	        { "path": "basedOnX" },
	        { "path": "/a/b" },
	        { "path": "/ok", "resolvesToKind": "missing_kind" }
	      ]
	    }
	  },
	  "transitions": [
	    { "id": "t", "from": { "status": "active", "phase": "p" }, "by": ["agent"],
	      "outcomes": [ { "id": "o", "when": { "always": true }, "to": { "status": "closed", "outcome": "x" } } ] }
	  ]
	}`
	var tpl Template
	if err := json.Unmarshal([]byte(bad), &tpl); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	errs := ValidateTemplate(&tpl, []byte(bad), nil)
	joined := strings.Join(errs, "\n")
	for _, want := range []string{
		"producibleBy references unknown role \"ghost_role\"",
		"producibleBy[1] is empty",
		"must be a JSON Pointer beginning with '/'",
		"must be a single top-level pointer",
		"resolvesToKind references unknown evidence kind \"missing_kind\"",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("install validation missing %q\nGot:\n%s", want, joined)
		}
	}
}
