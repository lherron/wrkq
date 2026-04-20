package store

import (
	"encoding/json"
	"testing"
)

func TestTaskWorkflowRoundTrip(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	containerUUID := setupTestContainer(t, database, actorUUID)
	s := New(database)

	taskResult, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "workflow-roundtrip",
		Title:       "Workflow Round Trip",
		ProjectUUID: containerUUID,
		State:       "open",
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	newETag, err := s.Tasks.UpdateFields(actorUUID, taskResult.UUID, map[string]interface{}{
		"workflow_preset": "code_defect_fastlane",
		"preset_version":  1,
		"phase":           "open",
		"risk_class":      "medium",
	}, 0)
	if err != nil {
		t.Fatalf("UpdateFields workflow metadata failed: %v", err)
	}
	if newETag <= taskResult.ETag {
		t.Fatalf("expected etag to advance, create=%d update=%d", taskResult.ETag, newETag)
	}

	task, err := s.Tasks.GetByUUID(taskResult.UUID)
	if err != nil {
		t.Fatalf("GetByUUID failed: %v", err)
	}
	if task.WorkflowPreset == nil || *task.WorkflowPreset != "code_defect_fastlane" {
		t.Fatalf("expected workflow_preset to round-trip, got %v", task.WorkflowPreset)
	}
	if task.PresetVersion == nil || *task.PresetVersion != 1 {
		t.Fatalf("expected preset_version 1, got %v", task.PresetVersion)
	}
	if task.Phase == nil || *task.Phase != "open" {
		t.Fatalf("expected phase open, got %v", task.Phase)
	}
	if task.RiskClass == nil || *task.RiskClass != "medium" {
		t.Fatalf("expected risk_class medium, got %v", task.RiskClass)
	}

	assignment, err := s.Tasks.CreateRoleAssignment(CreateRoleAssignmentParams{
		TaskUUID:  taskResult.UUID,
		Role:      "triager",
		ActorUUID: actorUUID,
	})
	if err != nil {
		t.Fatalf("CreateRoleAssignment failed: %v", err)
	}
	if assignment.Role != "triager" || assignment.ActorUUID != actorUUID {
		t.Fatalf("unexpected role assignment: %+v", assignment)
	}

	evidence, err := s.Tasks.CreateEvidenceItem(CreateEvidenceItemParams{
		TaskUUID:            taskResult.UUID,
		Kind:                "failing_test",
		Ref:                 "git:tests/failing#TestWorkflowRoundTrip",
		ProducedByActorUUID: actorUUID,
		ProducedByRole:      "implementer",
		BuildID:             strPtr("build-42"),
		BuildVersion:        strPtr("1.2.3"),
		BuildEnv:            strPtr("ci"),
		Meta:                strPtr(`{"suite":"workflow"}`),
	})
	if err != nil {
		t.Fatalf("CreateEvidenceItem failed: %v", err)
	}
	if evidence.ID == "" || evidence.Kind != "failing_test" {
		t.Fatalf("unexpected evidence item: %+v", evidence)
	}

	evidenceUUIDsJSON, err := json.Marshal([]string{evidence.UUID})
	if err != nil {
		t.Fatalf("failed to marshal evidence uuids: %v", err)
	}

	transition, err := s.Tasks.CreateTaskTransition(CreateTaskTransitionParams{
		TaskUUID:           taskResult.UUID,
		FromPhase:          strPtr("open"),
		ToPhase:            "red",
		FromLifecycleState: strPtr("open"),
		ToLifecycleState:   strPtr("in_progress"),
		ActorUUID:          actorUUID,
		ActorRole:          "implementer",
		EvidenceItemUUIDs:  strPtr(string(evidenceUUIDsJSON)),
		Meta:               strPtr(`{"reason":"begin red phase"}`),
	})
	if err != nil {
		t.Fatalf("CreateTaskTransition failed: %v", err)
	}
	if transition.ID == "" || transition.ToPhase != "red" {
		t.Fatalf("unexpected transition: %+v", transition)
	}

	assignments, err := s.Tasks.ListRoleAssignments(taskResult.UUID)
	if err != nil {
		t.Fatalf("ListRoleAssignments failed: %v", err)
	}
	if len(assignments) != 1 || assignments[0].UUID != assignment.UUID {
		t.Fatalf("unexpected assignments: %+v", assignments)
	}

	evidenceItems, err := s.Tasks.ListEvidenceItems(taskResult.UUID)
	if err != nil {
		t.Fatalf("ListEvidenceItems failed: %v", err)
	}
	if len(evidenceItems) != 1 || evidenceItems[0].UUID != evidence.UUID {
		t.Fatalf("unexpected evidence items: %+v", evidenceItems)
	}

	transitions, err := s.Tasks.ListTaskTransitions(taskResult.UUID)
	if err != nil {
		t.Fatalf("ListTaskTransitions failed: %v", err)
	}
	if len(transitions) != 1 || transitions[0].UUID != transition.UUID {
		t.Fatalf("unexpected transitions: %+v", transitions)
	}
	if transitions[0].EvidenceItemUUIDs == nil || *transitions[0].EvidenceItemUUIDs != string(evidenceUUIDsJSON) {
		t.Fatalf("expected evidence_item_uuids to round-trip, got %v", transitions[0].EvidenceItemUUIDs)
	}
}
