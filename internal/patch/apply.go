package patch

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/snapshot"
)

// ApplyToSnapshot applies a patch to a snapshot in memory.
// Returns the modified snapshot or an error if the patch cannot be applied.
func ApplyToSnapshot(base *snapshot.Snapshot, patch Patch) (*snapshot.Snapshot, error) {
	// Deep copy the base snapshot
	result, err := copySnapshot(base)
	if err != nil {
		return nil, fmt.Errorf("failed to copy snapshot: %w", err)
	}

	// Apply each operation
	for i, op := range patch {
		if err := applyOperation(result, op); err != nil {
			return nil, fmt.Errorf("failed to apply operation %d (%s %s): %w", i, op.Op, op.Path, err)
		}
	}

	return result, nil
}

// copySnapshot creates a deep copy of a snapshot via JSON round-trip.
func copySnapshot(snap *snapshot.Snapshot) (*snapshot.Snapshot, error) {
	data, err := json.Marshal(snap)
	if err != nil {
		return nil, err
	}

	var result snapshot.Snapshot
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	// Ensure maps are initialized
	if result.Actors == nil {
		result.Actors = make(map[string]snapshot.ActorEntry)
	}
	if result.Containers == nil {
		result.Containers = make(map[string]snapshot.ContainerEntry)
	}
	if result.Tasks == nil {
		result.Tasks = make(map[string]snapshot.TaskEntry)
	}
	if result.Comments == nil {
		result.Comments = make(map[string]snapshot.CommentEntry)
	}
	if result.Links == nil {
		result.Links = make(map[string]snapshot.LinkEntry)
	}
	if result.Events == nil {
		result.Events = make(map[string]snapshot.EventEntry)
	}

	return &result, nil
}

// applyOperation applies a single RFC 6902 operation to a snapshot.
func applyOperation(snap *snapshot.Snapshot, op Operation) error {
	parts := parseJSONPointer(op.Path)
	if len(parts) < 1 {
		return fmt.Errorf("invalid path: %s", op.Path)
	}

	switch op.Op {
	case "add":
		return applyAdd(snap, parts, op.Value)
	case "remove":
		return applyRemove(snap, parts)
	case "replace":
		return applyReplace(snap, parts, op.Value)
	case "test":
		return applyTest(snap, parts, op.Value)
	default:
		return fmt.Errorf("unsupported operation: %s", op.Op)
	}
}

func applyAdd(snap *snapshot.Snapshot, path []string, value interface{}) error {
	if len(path) < 2 {
		return fmt.Errorf("path too short for add: %v", path)
	}

	collection := path[0]
	key := path[1]

	switch collection {
	case "actors":
		entry, err := convertToActorEntry(value)
		if err != nil {
			return err
		}
		snap.Actors[key] = entry
	case "containers":
		entry, err := convertToContainerEntry(value)
		if err != nil {
			return err
		}
		snap.Containers[key] = entry
	case "tasks":
		entry, err := convertToTaskEntry(value)
		if err != nil {
			return err
		}
		snap.Tasks[key] = entry
	case "comments":
		entry, err := convertToCommentEntry(value)
		if err != nil {
			return err
		}
		snap.Comments[key] = entry
	case "links":
		entry, err := convertToLinkEntry(value)
		if err != nil {
			return err
		}
		snap.Links[key] = entry
	default:
		return fmt.Errorf("unsupported collection for add: %s", collection)
	}

	return nil
}

func applyRemove(snap *snapshot.Snapshot, path []string) error {
	if len(path) < 2 {
		return fmt.Errorf("path too short for remove: %v", path)
	}

	collection := path[0]
	key := path[1]

	switch collection {
	case "actors":
		if _, ok := snap.Actors[key]; !ok {
			return fmt.Errorf("actor not found: %s", key)
		}
		delete(snap.Actors, key)
	case "containers":
		if _, ok := snap.Containers[key]; !ok {
			return fmt.Errorf("container not found: %s", key)
		}
		delete(snap.Containers, key)
	case "tasks":
		if _, ok := snap.Tasks[key]; !ok {
			return fmt.Errorf("task not found: %s", key)
		}
		delete(snap.Tasks, key)
	case "comments":
		if _, ok := snap.Comments[key]; !ok {
			return fmt.Errorf("comment not found: %s", key)
		}
		delete(snap.Comments, key)
	case "links":
		if _, ok := snap.Links[key]; !ok {
			return fmt.Errorf("link not found: %s", key)
		}
		delete(snap.Links, key)
	default:
		return fmt.Errorf("unsupported collection for remove: %s", collection)
	}

	return nil
}

func applyReplace(snap *snapshot.Snapshot, path []string, value interface{}) error {
	if len(path) < 2 {
		return fmt.Errorf("path too short for replace: %v", path)
	}

	collection := path[0]
	key := path[1]

	switch collection {
	case "actors":
		if _, ok := snap.Actors[key]; !ok {
			return fmt.Errorf("actor not found for replace: %s", key)
		}
		entry, err := convertToActorEntry(value)
		if err != nil {
			return err
		}
		snap.Actors[key] = entry
	case "containers":
		if _, ok := snap.Containers[key]; !ok {
			return fmt.Errorf("container not found for replace: %s", key)
		}
		entry, err := convertToContainerEntry(value)
		if err != nil {
			return err
		}
		snap.Containers[key] = entry
	case "tasks":
		if _, ok := snap.Tasks[key]; !ok {
			return fmt.Errorf("task not found for replace: %s", key)
		}
		entry, err := convertToTaskEntry(value)
		if err != nil {
			return err
		}
		snap.Tasks[key] = entry
	case "comments":
		if _, ok := snap.Comments[key]; !ok {
			return fmt.Errorf("comment not found for replace: %s", key)
		}
		entry, err := convertToCommentEntry(value)
		if err != nil {
			return err
		}
		snap.Comments[key] = entry
	case "links":
		if _, ok := snap.Links[key]; !ok {
			return fmt.Errorf("link not found for replace: %s", key)
		}
		entry, err := convertToLinkEntry(value)
		if err != nil {
			return err
		}
		snap.Links[key] = entry
	default:
		return fmt.Errorf("unsupported collection for replace: %s", collection)
	}

	return nil
}

func applyTest(snap *snapshot.Snapshot, path []string, value interface{}) error {
	current, found := getValueAtPath(snap, "/"+strings.Join(path, "/"))
	if !found {
		return fmt.Errorf("path not found for test: %v", path)
	}

	if !deepEqual(current, value) {
		return fmt.Errorf("test failed at %v: values differ", path)
	}

	return nil
}

// Type conversion functions
func convertToActorEntry(v interface{}) (snapshot.ActorEntry, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return snapshot.ActorEntry{}, err
	}
	var entry snapshot.ActorEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return snapshot.ActorEntry{}, err
	}
	return entry, nil
}

func convertToContainerEntry(v interface{}) (snapshot.ContainerEntry, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return snapshot.ContainerEntry{}, err
	}
	var entry snapshot.ContainerEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return snapshot.ContainerEntry{}, err
	}
	return entry, nil
}

func convertToTaskEntry(v interface{}) (snapshot.TaskEntry, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return snapshot.TaskEntry{}, err
	}
	var entry snapshot.TaskEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return snapshot.TaskEntry{}, err
	}
	return entry, nil
}

func convertToCommentEntry(v interface{}) (snapshot.CommentEntry, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return snapshot.CommentEntry{}, err
	}
	var entry snapshot.CommentEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return snapshot.CommentEntry{}, err
	}
	return entry, nil
}

func convertToLinkEntry(v interface{}) (snapshot.LinkEntry, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return snapshot.LinkEntry{}, err
	}
	var entry snapshot.LinkEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return snapshot.LinkEntry{}, err
	}
	return entry, nil
}

// Apply applies a patch to the database.
func Apply(db *sql.DB, opts ApplyOptions) (*ApplyResult, error) {
	// Load patch
	p, err := LoadPatch(opts.PatchPath)
	if err != nil {
		return nil, err
	}

	// Export current DB state
	snap, data, err := snapshot.ExportToSnapshot(db, snapshot.ExportOptions{Canonical: true})
	if err != nil {
		return nil, fmt.Errorf("failed to export current state: %w", err)
	}

	// Check if-match guard
	if opts.IfMatch != "" {
		currentRev := snapshot.ComputeSnapshotRev(data)
		if currentRev != opts.IfMatch {
			return nil, fmt.Errorf("snapshot_rev mismatch: expected %s, got %s", opts.IfMatch, currentRev)
		}
	}

	// Apply patch to snapshot
	newSnap, err := ApplyToSnapshot(snap, p)
	if err != nil {
		return nil, fmt.Errorf("failed to apply patch: %w", err)
	}

	// Validate resulting snapshot
	if opts.Strict {
		if err := ValidateSnapshot(newSnap); err != nil {
			return nil, fmt.Errorf("validation failed: %w", err)
		}
	}

	adds, replaces, removes := p.CountOps()

	// If dry run, just return success
	if opts.DryRun {
		return &ApplyResult{
			Applied:      false,
			DryRun:       true,
			OpCount:      len(p),
			AddCount:     adds,
			ReplaceCount: replaces,
			RemoveCount:  removes,
		}, nil
	}

	// Import the new snapshot
	importOpts := snapshot.ImportOptions{
		Force: true, // We're replacing the DB state
	}

	// Write snapshot to temp file
	tmpFile, err := os.CreateTemp("", "wrkq-patch-*.json")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	// Generate canonical JSON with updated snapshot_rev
	newData, err := snapshot.CanonicalJSON(newSnap)
	if err != nil {
		return nil, fmt.Errorf("failed to generate snapshot: %w", err)
	}
	newRev := snapshot.ComputeSnapshotRev(newData)
	newSnap.Meta.SnapshotRev = newRev

	// Regenerate with updated rev
	newData, err = snapshot.CanonicalJSON(newSnap)
	if err != nil {
		return nil, fmt.Errorf("failed to regenerate snapshot: %w", err)
	}

	if err := os.WriteFile(tmpFile.Name(), newData, 0644); err != nil {
		return nil, fmt.Errorf("failed to write snapshot: %w", err)
	}

	importOpts.InputPath = tmpFile.Name()
	_, err = snapshot.Import(db, importOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to import patched state: %w", err)
	}

	return &ApplyResult{
		Applied:      true,
		DryRun:       false,
		SnapshotRev:  newRev,
		OpCount:      len(p),
		AddCount:     adds,
		ReplaceCount: replaces,
		RemoveCount:  removes,
	}, nil
}
