package patch

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"

	"github.com/lherron/wrkq/internal/snapshot"
)

// RebaseOptions configures patch rebase behavior.
type RebaseOptions struct {
	// PatchPath is the patch file to rebase
	PatchPath string
	// OldBasePath is the snapshot the patch was originally based on
	OldBasePath string
	// NewBasePath is the new snapshot to rebase onto
	NewBasePath string
	// OutputPath is where to write the rebased patch
	OutputPath string
	// StrictIDs fails on malformed friendly IDs
	StrictIDs bool
}

// RebaseResult contains the result of a patch rebase operation.
type RebaseResult struct {
	OutputPath   string                       `json:"out"`
	OpCount      int                          `json:"ops"`
	AddCount     int                          `json:"adds"`
	ReplaceCount int                          `json:"replaces"`
	RemoveCount  int                          `json:"removes"`
	CodeRewrites map[string]map[string]IDRewrite `json:"code_rewrites,omitempty"`
}

// IDRewrite records a friendly ID rename.
type IDRewrite struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// friendlyIDPattern matches friendly IDs like T-00001, P-00123, etc.
var friendlyIDPattern = regexp.MustCompile(`^([A-Z])-(\d+)$`)

// Rebase rebases a patch from old-base to new-base, auto-renumbering colliding IDs.
func Rebase(opts RebaseOptions) (*RebaseResult, error) {
	// Load patch
	p, err := LoadPatch(opts.PatchPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load patch: %w", err)
	}

	// Load old-base snapshot
	oldBaseData, err := os.ReadFile(opts.OldBasePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read old base: %w", err)
	}
	var oldBase snapshot.Snapshot
	if err := json.Unmarshal(oldBaseData, &oldBase); err != nil {
		return nil, fmt.Errorf("failed to parse old base: %w", err)
	}

	// Load new-base snapshot
	newBaseData, err := os.ReadFile(opts.NewBasePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read new base: %w", err)
	}
	var newBase snapshot.Snapshot
	if err := json.Unmarshal(newBaseData, &newBase); err != nil {
		return nil, fmt.Errorf("failed to parse new base: %w", err)
	}

	// Compute branch state: apply(patch, old-base)
	branchState, err := ApplyToSnapshot(&oldBase, p)
	if err != nil {
		return nil, fmt.Errorf("failed to apply patch to old base: %w", err)
	}

	// Detect new entities and collect all rewrites
	codeRewrites := make(map[string]map[string]IDRewrite)

	// Auto-renumber each resource type
	if rewrites, err := renumberActors(branchState, &oldBase, &newBase, opts.StrictIDs); err != nil {
		return nil, err
	} else if len(rewrites) > 0 {
		codeRewrites["actors"] = rewrites
	}

	if rewrites, err := renumberContainers(branchState, &oldBase, &newBase, opts.StrictIDs); err != nil {
		return nil, err
	} else if len(rewrites) > 0 {
		codeRewrites["containers"] = rewrites
	}

	if rewrites, err := renumberTasks(branchState, &oldBase, &newBase, opts.StrictIDs); err != nil {
		return nil, err
	} else if len(rewrites) > 0 {
		codeRewrites["tasks"] = rewrites
	}

	if rewrites, err := renumberComments(branchState, &oldBase, &newBase, opts.StrictIDs); err != nil {
		return nil, err
	} else if len(rewrites) > 0 {
		codeRewrites["comments"] = rewrites
	}

	// Compute rebased patch: diff(new-base, rebased branch state)
	rebasedPatch := DiffSnapshots(&newBase, branchState)

	// Save rebased patch
	if err := rebasedPatch.Save(opts.OutputPath); err != nil {
		return nil, err
	}

	adds, replaces, removes := rebasedPatch.CountOps()

	result := &RebaseResult{
		OutputPath:   opts.OutputPath,
		OpCount:      len(rebasedPatch),
		AddCount:     adds,
		ReplaceCount: replaces,
		RemoveCount:  removes,
	}

	if len(codeRewrites) > 0 {
		result.CodeRewrites = codeRewrites
	}

	return result, nil
}

// renumberActors renumbers colliding actor friendly IDs.
func renumberActors(branch, oldBase, newBase *snapshot.Snapshot, strict bool) (map[string]IDRewrite, error) {
	// Find new actors (in branch but not in old-base)
	newUUIDs := findNewEntities(
		keysOf(branch.Actors),
		keysOf(oldBase.Actors),
	)

	if len(newUUIDs) == 0 {
		return nil, nil
	}

	// Collect existing IDs on new-base
	existingIDs := make(map[string]bool)
	for _, actor := range newBase.Actors {
		existingIDs[actor.ID] = true
	}

	// Renumber collisions
	rewrites := make(map[string]IDRewrite)
	for _, uuid := range newUUIDs {
		actor := branch.Actors[uuid]
		if existingIDs[actor.ID] {
			newID, err := incrementFriendlyID(actor.ID, existingIDs, strict)
			if err != nil {
				return nil, fmt.Errorf("actor %s: %w", uuid, err)
			}
			rewrites[uuid] = IDRewrite{From: actor.ID, To: newID}
			actor.ID = newID
			branch.Actors[uuid] = actor
		}
		existingIDs[actor.ID] = true
	}

	return rewrites, nil
}

// renumberContainers renumbers colliding container friendly IDs.
func renumberContainers(branch, oldBase, newBase *snapshot.Snapshot, strict bool) (map[string]IDRewrite, error) {
	newUUIDs := findNewEntities(
		keysOf(branch.Containers),
		keysOf(oldBase.Containers),
	)

	if len(newUUIDs) == 0 {
		return nil, nil
	}

	existingIDs := make(map[string]bool)
	for _, container := range newBase.Containers {
		existingIDs[container.ID] = true
	}

	rewrites := make(map[string]IDRewrite)
	for _, uuid := range newUUIDs {
		container := branch.Containers[uuid]
		if existingIDs[container.ID] {
			newID, err := incrementFriendlyID(container.ID, existingIDs, strict)
			if err != nil {
				return nil, fmt.Errorf("container %s: %w", uuid, err)
			}
			rewrites[uuid] = IDRewrite{From: container.ID, To: newID}
			container.ID = newID
			branch.Containers[uuid] = container
		}
		existingIDs[container.ID] = true
	}

	return rewrites, nil
}

// renumberTasks renumbers colliding task friendly IDs.
func renumberTasks(branch, oldBase, newBase *snapshot.Snapshot, strict bool) (map[string]IDRewrite, error) {
	newUUIDs := findNewEntities(
		keysOf(branch.Tasks),
		keysOf(oldBase.Tasks),
	)

	if len(newUUIDs) == 0 {
		return nil, nil
	}

	existingIDs := make(map[string]bool)
	for _, task := range newBase.Tasks {
		existingIDs[task.ID] = true
	}

	rewrites := make(map[string]IDRewrite)
	for _, uuid := range newUUIDs {
		task := branch.Tasks[uuid]
		if existingIDs[task.ID] {
			newID, err := incrementFriendlyID(task.ID, existingIDs, strict)
			if err != nil {
				return nil, fmt.Errorf("task %s: %w", uuid, err)
			}
			rewrites[uuid] = IDRewrite{From: task.ID, To: newID}
			task.ID = newID
			branch.Tasks[uuid] = task
		}
		existingIDs[task.ID] = true
	}

	return rewrites, nil
}

// renumberComments renumbers colliding comment friendly IDs.
func renumberComments(branch, oldBase, newBase *snapshot.Snapshot, strict bool) (map[string]IDRewrite, error) {
	newUUIDs := findNewEntities(
		keysOf(branch.Comments),
		keysOf(oldBase.Comments),
	)

	if len(newUUIDs) == 0 {
		return nil, nil
	}

	existingIDs := make(map[string]bool)
	for _, comment := range newBase.Comments {
		existingIDs[comment.ID] = true
	}

	rewrites := make(map[string]IDRewrite)
	for _, uuid := range newUUIDs {
		comment := branch.Comments[uuid]
		if existingIDs[comment.ID] {
			newID, err := incrementFriendlyID(comment.ID, existingIDs, strict)
			if err != nil {
				return nil, fmt.Errorf("comment %s: %w", uuid, err)
			}
			rewrites[uuid] = IDRewrite{From: comment.ID, To: newID}
			comment.ID = newID
			branch.Comments[uuid] = comment
		}
		existingIDs[comment.ID] = true
	}

	return rewrites, nil
}

// findNewEntities returns UUIDs that are in branch but not in base, sorted.
func findNewEntities(branchUUIDs, baseUUIDs []string) []string {
	baseSet := make(map[string]bool)
	for _, uuid := range baseUUIDs {
		baseSet[uuid] = true
	}

	var newUUIDs []string
	for _, uuid := range branchUUIDs {
		if !baseSet[uuid] {
			newUUIDs = append(newUUIDs, uuid)
		}
	}

	// Sort for deterministic order
	sort.Strings(newUUIDs)
	return newUUIDs
}

// keysOf returns the keys of a map as a slice.
func keysOf[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// incrementFriendlyID finds the next available friendly ID.
// It parses the ID format (e.g., T-00050), finds max among existing,
// and returns the next available (e.g., T-00051).
func incrementFriendlyID(id string, existing map[string]bool, strict bool) (string, error) {
	match := friendlyIDPattern.FindStringSubmatch(id)
	if match == nil {
		if strict {
			return "", fmt.Errorf("malformed friendly ID: %s", id)
		}
		// Non-strict: append suffix
		suffix := 2
		newID := fmt.Sprintf("%s-%d", id, suffix)
		for existing[newID] {
			suffix++
			newID = fmt.Sprintf("%s-%d", id, suffix)
		}
		return newID, nil
	}

	prefix := match[1]
	numStr := match[2]
	width := len(numStr)

	// Find max number among existing IDs with same prefix
	maxNum := 0
	for existingID := range existing {
		existingMatch := friendlyIDPattern.FindStringSubmatch(existingID)
		if existingMatch != nil && existingMatch[1] == prefix {
			num, err := strconv.Atoi(existingMatch[2])
			if err == nil && num > maxNum {
				maxNum = num
			}
		}
	}

	// Also check the original ID's number
	origNum, _ := strconv.Atoi(numStr)
	if origNum > maxNum {
		maxNum = origNum
	}

	// Generate new ID with incremented number
	newNum := maxNum + 1
	newID := fmt.Sprintf("%s-%0*d", prefix, width, newNum)

	// Safety check - ensure it's not taken
	for existing[newID] {
		newNum++
		newID = fmt.Sprintf("%s-%0*d", prefix, width, newNum)
	}

	return newID, nil
}
