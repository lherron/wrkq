package patch

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/lherron/wrkq/internal/snapshot"
)

// Validate checks a patch against a base snapshot and domain invariants.
func Validate(opts ValidateOptions) (*ValidateResult, error) {
	// Load patch
	p, err := LoadPatch(opts.PatchPath)
	if err != nil {
		return nil, err
	}

	// Load base snapshot
	baseData, err := os.ReadFile(opts.BasePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read base snapshot: %w", err)
	}

	var base snapshot.Snapshot
	if err := json.Unmarshal(baseData, &base); err != nil {
		return nil, fmt.Errorf("failed to parse base snapshot: %w", err)
	}

	// Apply patch to get result snapshot
	result, err := ApplyToSnapshot(&base, p)
	if err != nil {
		return &ValidateResult{
			Valid:  false,
			Errors: []string{err.Error()},
		}, nil
	}

	// Validate the resulting snapshot
	errors := validateSnapshotInvariants(result)

	return &ValidateResult{
		Valid:  len(errors) == 0,
		Errors: errors,
	}, nil
}

// ValidateSnapshot checks domain invariants on a snapshot.
func ValidateSnapshot(snap *snapshot.Snapshot) error {
	errors := validateSnapshotInvariants(snap)
	if len(errors) > 0 {
		return fmt.Errorf("validation failed: %v", errors)
	}
	return nil
}

// validateSnapshotInvariants checks all domain invariants per PATCH-MODE.md §3.5.
func validateSnapshotInvariants(snap *snapshot.Snapshot) []string {
	var errors []string

	// 1. FK constraints - tasks must reference valid containers
	for uuid, task := range snap.Tasks {
		if _, ok := snap.Containers[task.ProjectUUID]; !ok {
			errors = append(errors, fmt.Sprintf("task %s references unknown container %s", uuid, task.ProjectUUID))
		}
	}

	// 2. FK constraints - comments must reference valid tasks and actors
	for uuid, comment := range snap.Comments {
		if _, ok := snap.Tasks[comment.TaskUUID]; !ok {
			errors = append(errors, fmt.Sprintf("comment %s references unknown task %s", uuid, comment.TaskUUID))
		}
		if _, ok := snap.Actors[comment.ActorUUID]; !ok {
			errors = append(errors, fmt.Sprintf("comment %s references unknown actor %s", uuid, comment.ActorUUID))
		}
	}

	// 3. FK constraints - containers with parent must reference valid parent
	for uuid, container := range snap.Containers {
		if container.ParentUUID != "" {
			if _, ok := snap.Containers[container.ParentUUID]; !ok {
				errors = append(errors, fmt.Sprintf("container %s references unknown parent %s", uuid, container.ParentUUID))
			}
		}
	}

	// 4. Slug uniqueness among siblings (containers)
	containerSiblings := make(map[string]map[string]string) // parentUUID -> slug -> uuid
	for uuid, container := range snap.Containers {
		parentKey := container.ParentUUID
		if parentKey == "" {
			parentKey = "__root__"
		}
		if containerSiblings[parentKey] == nil {
			containerSiblings[parentKey] = make(map[string]string)
		}
		if existing, ok := containerSiblings[parentKey][container.Slug]; ok {
			errors = append(errors, fmt.Sprintf("duplicate container slug '%s' in same parent: %s and %s", container.Slug, existing, uuid))
		}
		containerSiblings[parentKey][container.Slug] = uuid
	}

	// 5. Slug uniqueness among siblings (tasks in same container)
	taskSiblings := make(map[string]map[string]string) // containerUUID -> slug -> uuid
	for uuid, task := range snap.Tasks {
		if taskSiblings[task.ProjectUUID] == nil {
			taskSiblings[task.ProjectUUID] = make(map[string]string)
		}
		if existing, ok := taskSiblings[task.ProjectUUID][task.Slug]; ok {
			errors = append(errors, fmt.Sprintf("duplicate task slug '%s' in container %s: %s and %s", task.Slug, task.ProjectUUID, existing, uuid))
		}
		taskSiblings[task.ProjectUUID][task.Slug] = uuid
	}

	// 6. Friendly ID uniqueness per resource type
	actorIDs := make(map[string]string) // id -> uuid
	for uuid, actor := range snap.Actors {
		if existing, ok := actorIDs[actor.ID]; ok {
			errors = append(errors, fmt.Sprintf("duplicate actor ID '%s': %s and %s", actor.ID, existing, uuid))
		}
		actorIDs[actor.ID] = uuid
	}

	containerIDs := make(map[string]string)
	for uuid, container := range snap.Containers {
		if existing, ok := containerIDs[container.ID]; ok {
			errors = append(errors, fmt.Sprintf("duplicate container ID '%s': %s and %s", container.ID, existing, uuid))
		}
		containerIDs[container.ID] = uuid
	}

	taskIDs := make(map[string]string)
	for uuid, task := range snap.Tasks {
		if existing, ok := taskIDs[task.ID]; ok {
			errors = append(errors, fmt.Sprintf("duplicate task ID '%s': %s and %s", task.ID, existing, uuid))
		}
		taskIDs[task.ID] = uuid
	}

	commentIDs := make(map[string]string)
	for uuid, comment := range snap.Comments {
		if existing, ok := commentIDs[comment.ID]; ok {
			errors = append(errors, fmt.Sprintf("duplicate comment ID '%s': %s and %s", comment.ID, existing, uuid))
		}
		commentIDs[comment.ID] = uuid
	}

	// 7. Container hierarchy is acyclic (no container can be its own ancestor)
	errors = append(errors, checkContainerCycles(snap)...)

	// 8. Actors referenced by tasks must exist
	for uuid, task := range snap.Tasks {
		if _, ok := snap.Actors[task.CreatedBy]; !ok {
			errors = append(errors, fmt.Sprintf("task %s references unknown actor %s (created_by)", uuid, task.CreatedBy))
		}
		if _, ok := snap.Actors[task.UpdatedBy]; !ok {
			errors = append(errors, fmt.Sprintf("task %s references unknown actor %s (updated_by)", uuid, task.UpdatedBy))
		}
	}

	for uuid, container := range snap.Containers {
		if _, ok := snap.Actors[container.CreatedBy]; !ok {
			errors = append(errors, fmt.Sprintf("container %s references unknown actor %s (created_by)", uuid, container.CreatedBy))
		}
		if _, ok := snap.Actors[container.UpdatedBy]; !ok {
			errors = append(errors, fmt.Sprintf("container %s references unknown actor %s (updated_by)", uuid, container.UpdatedBy))
		}
	}

	return errors
}

// checkContainerCycles detects cycles in the container hierarchy.
func checkContainerCycles(snap *snapshot.Snapshot) []string {
	var errors []string

	// Build parent map
	parents := make(map[string]string) // uuid -> parent_uuid
	for uuid, container := range snap.Containers {
		if container.ParentUUID != "" {
			parents[uuid] = container.ParentUUID
		}
	}

	// Check each container for cycles
	for uuid := range snap.Containers {
		visited := make(map[string]bool)
		current := uuid

		for current != "" {
			if visited[current] {
				errors = append(errors, fmt.Sprintf("cycle detected in container hierarchy at %s", uuid))
				break
			}
			visited[current] = true
			current = parents[current]
		}
	}

	return errors
}

// ValidatePatchOps checks that all operations in a patch are valid RFC 6902.
func ValidatePatchOps(p Patch) []string {
	var errors []string

	for i, op := range p {
		// Check op type
		switch op.Op {
		case "add", "replace", "test":
			if op.Value == nil {
				errors = append(errors, fmt.Sprintf("operation %d (%s): missing value", i, op.Op))
			}
		case "remove":
			// remove doesn't need value
		case "move", "copy":
			if op.From == "" {
				errors = append(errors, fmt.Sprintf("operation %d (%s): missing from", i, op.Op))
			}
		default:
			errors = append(errors, fmt.Sprintf("operation %d: unknown op '%s'", i, op.Op))
		}

		// Check path format
		if op.Path == "" {
			errors = append(errors, fmt.Sprintf("operation %d (%s): empty path", i, op.Op))
		} else if op.Path[0] != '/' {
			errors = append(errors, fmt.Sprintf("operation %d (%s): path must start with /", i, op.Op))
		}
	}

	return errors
}
