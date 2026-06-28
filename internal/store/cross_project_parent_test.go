package store

import (
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
)

func createCrossProjectTestContainer(t *testing.T, s *Store, actorUUID, slug string) string {
	t.Helper()
	result, err := s.Containers.Create(actorUUID, ContainerCreateParams{
		Slug: slug,
		Kind: "project",
	})
	if err != nil {
		t.Fatalf("create container %s: %v", slug, err)
	}
	return result.UUID
}

func createCrossProjectTestTask(t *testing.T, s *Store, actorUUID, projectUUID, slug string, parentUUID *string) string {
	t.Helper()
	kind := "task"
	if parentUUID != nil {
		kind = "subtask"
	}
	result, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:           slug,
		Title:          slug,
		Description:    slug,
		ProjectUUID:    projectUUID,
		State:          domain.StateOpen,
		Priority:       2,
		Kind:           kind,
		ParentTaskUUID: parentUUID,
	})
	if err != nil {
		t.Fatalf("create task %s: %v", slug, err)
	}
	return result.UUID
}

func testAttribution(actorUUID string) attribution.Attribution {
	return attribution.Attribution{
		PrincipalRef:    "agent:test-actor",
		LegacyActorUUID: &actorUUID,
	}
}

func TestTaskStoreCrossProjectParentAssignmentAndGlobalDepthGuards(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := New(database)
	projectA := createCrossProjectTestContainer(t, s, actorUUID, "xproj-a")
	projectB := createCrossProjectTestContainer(t, s, actorUUID, "xproj-b")

	parent := createCrossProjectTestTask(t, s, actorUUID, projectA, "xparent", nil)
	child := createCrossProjectTestTask(t, s, actorUUID, projectB, "xchild", nil)

	if _, err := s.Tasks.UpdateFields(actorUUID, child, map[string]interface{}{
		"parent_task_uuid": parent,
		"kind":             "subtask",
	}, 0); err != nil {
		t.Fatalf("cross-project parent assignment failed: %v", err)
	}
	gotChild, err := s.Tasks.GetByUUID(child)
	if err != nil {
		t.Fatalf("load child: %v", err)
	}
	if gotChild.ParentTaskUUID == nil || *gotChild.ParentTaskUUID != parent {
		t.Fatalf("child parent = %v, want %s", gotChild.ParentTaskUUID, parent)
	}
	if gotChild.ProjectUUID != projectB {
		t.Fatalf("child project moved to %s, want resident project %s", gotChild.ProjectUUID, projectB)
	}

	grandchild := createCrossProjectTestTask(t, s, actorUUID, projectA, "xgrandchild", nil)
	if _, err := s.Tasks.UpdateFields(actorUUID, grandchild, map[string]interface{}{"parent_task_uuid": child}, 0); err == nil || !strings.Contains(err.Error(), "parent task is itself a subtask") {
		t.Fatalf("parent-that-is-subtask guard err = %v, want max-depth rejection", err)
	}

	parentWithChild := createCrossProjectTestTask(t, s, actorUUID, projectA, "xhaschild", nil)
	residentChild := createCrossProjectTestTask(t, s, actorUUID, projectA, "xresident-child", &parentWithChild)
	otherParent := createCrossProjectTestTask(t, s, actorUUID, projectB, "xother-parent", nil)
	if _, err := s.Tasks.UpdateFields(actorUUID, parentWithChild, map[string]interface{}{"parent_task_uuid": otherParent}, 0); err == nil || !strings.Contains(err.Error(), "cannot reparent a task that already has subtasks") {
		t.Fatalf("child-with-children guard err = %v, want max-depth rejection", err)
	}
	if _, err := s.Tasks.GetByUUID(residentChild); err != nil {
		t.Fatalf("resident child should remain after guard check: %v", err)
	}
}

func TestTaskStoreCrossProjectParentSoftDeleteDetachesExternalChildren(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := New(database)
	projectA := createCrossProjectTestContainer(t, s, actorUUID, "xsoft-a")
	projectB := createCrossProjectTestContainer(t, s, actorUUID, "xsoft-b")

	parent := createCrossProjectTestTask(t, s, actorUUID, projectA, "xsoft-parent", nil)
	residentChild := createCrossProjectTestTask(t, s, actorUUID, projectA, "xsoft-resident", &parent)
	externalChild := createCrossProjectTestTask(t, s, actorUUID, projectB, "xsoft-external", &parent)

	if _, err := s.Tasks.UpdateFields(actorUUID, parent, map[string]interface{}{"state": "deleted"}, 0); err != nil {
		t.Fatalf("delete parent: %v", err)
	}

	gotResident, err := s.Tasks.GetByUUID(residentChild)
	if err != nil {
		t.Fatalf("load resident child: %v", err)
	}
	if gotResident.State != domain.StateDeleted {
		t.Fatalf("resident child state = %s, want deleted", gotResident.State)
	}
	gotExternal, err := s.Tasks.GetByUUID(externalChild)
	if err != nil {
		t.Fatalf("external child should survive: %v", err)
	}
	if gotExternal.State != domain.StateOpen || gotExternal.ParentTaskUUID != nil || gotExternal.Kind != domain.TaskKindTask {
		t.Fatalf("external child = state %s parent %v kind %s, want open detached task", gotExternal.State, gotExternal.ParentTaskUUID, gotExternal.Kind)
	}
}

func TestTaskStoreCrossProjectParentPurgeAndContainerDeleteDetachExternalChildren(t *testing.T) {
	t.Run("task purge", func(t *testing.T) {
		database := setupTestDB(t)
		actorUUID := setupTestActor(t, database)
		s := New(database)
		projectA := createCrossProjectTestContainer(t, s, actorUUID, "xpurge-a")
		projectB := createCrossProjectTestContainer(t, s, actorUUID, "xpurge-b")
		parent := createCrossProjectTestTask(t, s, actorUUID, projectA, "xpurge-parent", nil)
		externalChild := createCrossProjectTestTask(t, s, actorUUID, projectB, "xpurge-external", &parent)

		if _, err := s.Tasks.Purge(actorUUID, parent, 0); err != nil {
			t.Fatalf("purge parent: %v", err)
		}
		gotExternal, err := s.Tasks.GetByUUID(externalChild)
		if err != nil {
			t.Fatalf("external child should survive purge: %v", err)
		}
		if gotExternal.ParentTaskUUID != nil || gotExternal.Kind != domain.TaskKindTask {
			t.Fatalf("external child after purge parent = %v kind %s, want detached task", gotExternal.ParentTaskUUID, gotExternal.Kind)
		}
	})

	t.Run("container delete recursive", func(t *testing.T) {
		database := setupTestDB(t)
		actorUUID := setupTestActor(t, database)
		s := New(database)
		projectA := createCrossProjectTestContainer(t, s, actorUUID, "xrecursive-a")
		projectB := createCrossProjectTestContainer(t, s, actorUUID, "xrecursive-b")
		parent := createCrossProjectTestTask(t, s, actorUUID, projectA, "xrecursive-parent", nil)
		externalChild := createCrossProjectTestTask(t, s, actorUUID, projectB, "xrecursive-external", &parent)

		impact, err := s.Containers.DeleteRecursiveImpact(projectA)
		if err != nil {
			t.Fatalf("recursive impact: %v", err)
		}
		if impact.Tasks != 1 {
			t.Fatalf("recursive impact tasks = %d, want only resident parent counted", impact.Tasks)
		}
		if _, err := s.Containers.DeleteRecursiveWithAttribution(testAttribution(actorUUID), projectA, 0, *impact); err != nil {
			t.Fatalf("delete recursive: %v", err)
		}
		gotExternal, err := s.Tasks.GetByUUID(externalChild)
		if err != nil {
			t.Fatalf("external child should survive container delete: %v", err)
		}
		if gotExternal.ParentTaskUUID != nil || gotExternal.Kind != domain.TaskKindTask {
			t.Fatalf("external child after container delete = %v kind %s, want detached task", gotExternal.ParentTaskUUID, gotExternal.Kind)
		}
	})
}

func TestTaskStoreCrossProjectParentMoveDoesNotMoveExternalChildren(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := New(database)
	projectA := createCrossProjectTestContainer(t, s, actorUUID, "xmove-a")
	projectB := createCrossProjectTestContainer(t, s, actorUUID, "xmove-b")
	projectC := createCrossProjectTestContainer(t, s, actorUUID, "xmove-c")

	parent := createCrossProjectTestTask(t, s, actorUUID, projectA, "xmove-parent", nil)
	residentChild := createCrossProjectTestTask(t, s, actorUUID, projectA, "xmove-resident", &parent)
	externalChild := createCrossProjectTestTask(t, s, actorUUID, projectB, "xmove-external", &parent)

	if _, err := s.Tasks.Move(actorUUID, parent, projectC, 0); err != nil {
		t.Fatalf("move parent: %v", err)
	}
	gotParent, _ := s.Tasks.GetByUUID(parent)
	gotResident, _ := s.Tasks.GetByUUID(residentChild)
	gotExternal, _ := s.Tasks.GetByUUID(externalChild)
	if gotParent.ProjectUUID != projectC {
		t.Fatalf("parent project = %s, want %s", gotParent.ProjectUUID, projectC)
	}
	if gotResident.ProjectUUID != projectC {
		t.Fatalf("resident child project = %s, want %s", gotResident.ProjectUUID, projectC)
	}
	if gotExternal.ProjectUUID != projectB {
		t.Fatalf("external child project = %s, want %s", gotExternal.ProjectUUID, projectB)
	}
	if gotExternal.ParentTaskUUID == nil || *gotExternal.ParentTaskUUID != parent {
		t.Fatalf("external child parent = %v, want backlink to moved parent %s", gotExternal.ParentTaskUUID, parent)
	}
}
