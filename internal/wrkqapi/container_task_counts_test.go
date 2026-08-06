//go:build wrkq_local

package wrkqapi

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/store"
)

func TestContainerTaskCountsCanonicalInclusionAndIdentity(t *testing.T) {
	api, s := newMonitorAPI(t)
	projectUUID := seedMonitorProject(t, s)
	nested := createTimelineContainer(t, s, projectUUID, "nested")
	deep := createTimelineContainer(t, s, nested.UUID, "deep")
	empty := createTimelineContainer(t, s, nested.UUID, "empty")
	archivedContainer := createTimelineContainer(t, s, projectUUID, "archived-container")
	otherProject, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{
		Slug: "other", Kind: "project",
	})
	if err != nil {
		t.Fatalf("create other project: %v", err)
	}

	createTimelineTask(t, s, projectUUID, "idea", "idea", "")
	createTimelineTask(t, s, nested.UUID, "open", "open", "")
	createTimelineTask(t, s, deep.UUID, "blocked", "blocked", "")
	createTimelineTask(t, s, deep.UUID, "completed", "completed", "")
	createTimelineTask(t, s, deep.UUID, "cancelled", "cancelled", "")
	archivedTask := createTimelineTask(t, s, nested.UUID, "archived", "open", "")
	if _, err := s.Tasks.Archive(monitorSystemActor, archivedTask.UUID, 0); err != nil {
		t.Fatalf("archive task: %v", err)
	}
	createTimelineTask(t, s, archivedContainer.UUID, "active-under-archived", "draft", "")
	deleted := createTimelineTask(t, s, nested.UUID, "deleted", "open", "")
	if _, err := s.DB().Exec(`
		UPDATE tasks
		   SET state = 'deleted', deleted_at = '2026-07-24T00:00:00Z'
		 WHERE uuid = ?
	`, deleted.UUID); err != nil {
		t.Fatalf("mark task deleted: %v", err)
	}
	deletedMarkerOnly := createTimelineTask(t, s, deep.UUID, "deleted-marker", "open", "")
	if _, err := s.DB().Exec(`
		UPDATE tasks SET deleted_at = '2026-07-24T00:00:00Z' WHERE uuid = ?
	`, deletedMarkerOnly.UUID); err != nil {
		t.Fatalf("mark task deleted without state transition: %v", err)
	}
	if _, err := s.Containers.Archive(monitorSystemActor, archivedContainer.UUID, 0); err != nil {
		t.Fatalf("archive container: %v", err)
	}

	got, err := api.ContainerTaskCounts(context.Background(), ContainerTaskCountsParams{})
	if err != nil {
		t.Fatalf("ContainerTaskCounts: %v", err)
	}
	byPath := taskCountsByPath(got.Items)

	assertContainerCounts(t, byPath["proj"], 7, 4)
	assertContainerCounts(t, byPath["proj/nested"], 5, 2)
	assertContainerCounts(t, byPath["proj/nested/deep"], 3, 1)
	assertContainerCounts(t, byPath["proj/nested/empty"], 0, 0)
	assertContainerCounts(t, byPath["other"], 0, 0)
	if _, ok := byPath["proj/archived-container"]; ok {
		t.Fatal("default result included archived container row")
	}
	for _, path := range []string{"proj", "proj/nested", "proj/nested/deep", "proj/nested/empty"} {
		item := byPath[path]
		if item.ProjectUUID != projectUUID || item.ProjectID == "" || item.ProjectSlug != "proj" {
			t.Fatalf("%s project identity = %#v", path, item)
		}
		if item.UUID == "" || item.ID == "" || item.Kind == "" {
			t.Fatalf("%s missing stable container identity: %#v", path, item)
		}
	}
	if byPath["proj"].ProjectUUID != byPath["proj"].UUID ||
		byPath["proj"].ProjectID != byPath["proj"].ID {
		t.Fatalf("project root identity does not self-identify: %#v", byPath["proj"])
	}

	withArchived, err := api.ContainerTaskCounts(context.Background(), ContainerTaskCountsParams{
		IncludeArchived: true,
	})
	if err != nil {
		t.Fatalf("ContainerTaskCounts(includeArchived): %v", err)
	}
	archivedRow := taskCountsByPath(withArchived.Items)["proj/archived-container"]
	assertContainerCounts(t, archivedRow, 1, 1)
	if archivedRow.ArchivedAt == nil {
		t.Fatalf("archived row has no archivedAt: %#v", archivedRow)
	}

	// The existing ls projection uses the same residency/subtree/state rules.
	total, active, err := api.containerRollupCounts(context.Background(), projectUUID)
	if err != nil {
		t.Fatalf("containerRollupCounts: %v", err)
	}
	if total != byPath["proj"].TotalTaskCount || active != byPath["proj"].ActiveTaskCount {
		t.Fatalf("ls rollup = %d/%d, aggregate = %d/%d",
			total, active, byPath["proj"].TotalTaskCount, byPath["proj"].ActiveTaskCount)
	}
	_ = empty
	_ = otherProject
}

func TestContainerTaskCountsPaginationIndependentAndMatchesExhaustiveTaskList(t *testing.T) {
	api, s := newMonitorAPI(t)
	projectUUID := seedMonitorProject(t, s)
	nested := createTimelineContainer(t, s, projectUUID, "many")
	const taskCount = 503
	for index := 0; index < taskCount; index++ {
		state := domain.StateCompleted
		if index%4 == 0 {
			state = domain.StateOpen
		}
		if _, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
			Slug:        fmt.Sprintf("task-%04d", index),
			Title:       fmt.Sprintf("Task %04d", index),
			ProjectUUID: nested.UUID,
			State:       state,
			Priority:    2,
		}); err != nil {
			t.Fatalf("create task %d: %v", index, err)
		}
	}

	aggregate, err := api.ContainerTaskCounts(context.Background(), ContainerTaskCountsParams{})
	if err != nil {
		t.Fatalf("ContainerTaskCounts: %v", err)
	}
	projectCounts := taskCountsByPath(aggregate.Items)["proj"]

	var exhaustiveTotal, exhaustiveActive, pages int
	cursor := ""
	for {
		page, err := api.TaskList(context.Background(), TaskListParams{
			Path: "proj", Recursive: true, IncludeDeleted: false,
			Limit: 500, Cursor: cursor, Summary: true,
		})
		if err != nil {
			t.Fatalf("TaskList page %d: %v", pages+1, err)
		}
		pages++
		for _, task := range page.Items {
			exhaustiveTotal++
			if isContainerCountActiveTask(task.State, task.ArchivedAt, task.DeletedAt) {
				exhaustiveActive++
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if pages != 2 {
		t.Fatalf("exhaustive sweep pages = %d, want 2", pages)
	}
	if projectCounts.TotalTaskCount != exhaustiveTotal ||
		projectCounts.ActiveTaskCount != exhaustiveActive {
		t.Fatalf("aggregate = %d/%d, exhaustive = %d/%d",
			projectCounts.TotalTaskCount, projectCounts.ActiveTaskCount,
			exhaustiveTotal, exhaustiveActive)
	}
	if projectCounts.TotalTaskCount != taskCount {
		t.Fatalf("aggregate total = %d, want %d", projectCounts.TotalTaskCount, taskCount)
	}
}

func taskCountsByPath(items []WrkqContainerTaskCount) map[string]WrkqContainerTaskCount {
	out := make(map[string]WrkqContainerTaskCount, len(items))
	for _, item := range items {
		out[item.Path] = item
	}
	return out
}

func assertContainerCounts(t *testing.T, got WrkqContainerTaskCount, total, active int) {
	t.Helper()
	if got.TotalTaskCount != total || got.ActiveTaskCount != active {
		t.Fatalf("%s counts = %d/%d, want %d/%d",
			got.Path, got.TotalTaskCount, got.ActiveTaskCount, total, active)
	}
}

func isContainerCountActiveTask(
	state, archivedAt, deletedAt string,
) bool {
	if archivedAt != "" || deletedAt != "" {
		return false
	}
	switch domain.State(state) {
	case domain.StateIdea, domain.StateDraft, domain.StateOpen, domain.StateInProgress, domain.StateBlocked:
		return true
	default:
		return false
	}
}

func BenchmarkContainerTaskCountsVsProjectSweep(b *testing.B) {
	dbPath := filepath.Join(b.TempDir(), "benchmark.db")
	database, err := db.Open(dbPath)
	if err != nil {
		b.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		b.Fatalf("migrate: %v", err)
	}
	b.Cleanup(func() { _ = database.Close() })
	api := New(database, nil, "agent:wrkq-system", "", 0)
	s := store.New(database)

	const (
		projectCount       = 12
		containersPer      = 4
		tasksPerContainer  = 40
		expectedTotalTasks = projectCount * containersPer * tasksPerContainer
	)
	projects := make([]string, 0, projectCount)
	for projectIndex := 0; projectIndex < projectCount; projectIndex++ {
		projectSlug := fmt.Sprintf("bench-%02d", projectIndex)
		project, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{
			Slug: projectSlug, Kind: "project",
		})
		if err != nil {
			b.Fatalf("create project %d: %v", projectIndex, err)
		}
		projects = append(projects, projectSlug)
		for containerIndex := 0; containerIndex < containersPer; containerIndex++ {
			container, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{
				Slug:       fmt.Sprintf("area-%02d", containerIndex),
				Kind:       "directory",
				ParentUUID: &project.UUID,
			})
			if err != nil {
				b.Fatalf("create container %d/%d: %v", projectIndex, containerIndex, err)
			}
			for taskIndex := 0; taskIndex < tasksPerContainer; taskIndex++ {
				state := domain.StateCompleted
				if taskIndex%2 == 0 {
					state = domain.StateOpen
				}
				if _, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
					Slug:        fmt.Sprintf("task-%02d", taskIndex),
					Title:       "Benchmark task",
					ProjectUUID: container.UUID,
					State:       state,
					Priority:    2,
				}); err != nil {
					b.Fatalf("create task %d/%d/%d: %v", projectIndex, containerIndex, taskIndex, err)
				}
			}
		}
	}

	sweep := func() (int, int) {
		total, active := 0, 0
		for _, project := range projects {
			cursor := ""
			for {
				page, err := api.TaskList(context.Background(), TaskListParams{
					Path: project, Recursive: true, IncludeDeleted: false,
					Limit: 500, Cursor: cursor, Summary: true,
				})
				if err != nil {
					b.Fatalf("project sweep %s: %v", project, err)
				}
				for _, task := range page.Items {
					total++
					if isContainerCountActiveTask(task.State, task.ArchivedAt, task.DeletedAt) {
						active++
					}
				}
				if page.NextCursor == "" {
					break
				}
				cursor = page.NextCursor
			}
		}
		return total, active
	}
	aggregate := func() (int, int) {
		result, err := api.ContainerTaskCounts(context.Background(), ContainerTaskCountsParams{})
		if err != nil {
			b.Fatalf("aggregate: %v", err)
		}
		total, active := 0, 0
		for _, item := range result.Items {
			if item.Kind == "project" {
				total += item.TotalTaskCount
				active += item.ActiveTaskCount
			}
		}
		return total, active
	}
	sweepTotal, sweepActive := sweep()
	aggregateTotal, aggregateActive := aggregate()
	if sweepTotal != expectedTotalTasks ||
		aggregateTotal != sweepTotal ||
		aggregateActive != sweepActive {
		b.Fatalf("count drift: sweep=%d/%d aggregate=%d/%d expected total=%d",
			sweepTotal, sweepActive, aggregateTotal, aggregateActive, expectedTotalTasks)
	}

	b.Run("project-linear-sweep", func(b *testing.B) {
		for index := 0; index < b.N; index++ {
			sweep()
		}
	})
	b.Run("single-aggregate", func(b *testing.B) {
		for index := 0; index < b.N; index++ {
			aggregate()
		}
	})
}