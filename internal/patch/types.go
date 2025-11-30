// Package patch implements RFC 6902 JSON Patch for wrkq snapshots.
//
// It provides functionality to:
// - Create patches between two snapshots (diff)
// - Validate patches against domain invariants
// - Apply patches to snapshots or databases
package patch

import (
	"encoding/json"
	"fmt"
	"os"
)

// Operation represents a single RFC 6902 JSON Patch operation.
type Operation struct {
	Op    string      `json:"op"`              // add, remove, replace, move, copy, test
	Path  string      `json:"path"`            // JSON Pointer path
	Value interface{} `json:"value,omitempty"` // Value for add/replace/test
	From  string      `json:"from,omitempty"`  // Source path for move/copy
}

// Patch is a sequence of RFC 6902 operations.
type Patch []Operation

// CreateOptions configures patch creation behavior.
type CreateOptions struct {
	// FromPath is the base snapshot file
	FromPath string
	// ToPath is the target snapshot file
	ToPath string
	// OutputPath is where to write the patch
	OutputPath string
	// AllowNonCanonical skips canonicalization check
	AllowNonCanonical bool
}

// ValidateOptions configures patch validation behavior.
type ValidateOptions struct {
	// PatchPath is the patch file
	PatchPath string
	// BasePath is the base snapshot file
	BasePath string
	// Strict enables strict mode (exit 4 on any violation)
	Strict bool
}

// ApplyOptions configures patch application behavior.
type ApplyOptions struct {
	// PatchPath is the patch file
	PatchPath string
	// IfMatch requires snapshot_rev to match before apply
	IfMatch string
	// DryRun validates without writing
	DryRun bool
	// Strict enables strict validation
	Strict bool
}

// CreateResult contains the result of a patch create operation.
type CreateResult struct {
	OutputPath  string `json:"out"`
	OpCount     int    `json:"ops"`
	AddCount    int    `json:"adds"`
	ReplaceCount int   `json:"replaces"`
	RemoveCount int    `json:"removes"`
}

// ValidateResult contains the result of a patch validate operation.
type ValidateResult struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// ApplyResult contains the result of a patch apply operation.
type ApplyResult struct {
	Applied     bool   `json:"applied"`
	DryRun      bool   `json:"dry_run,omitempty"`
	SnapshotRev string `json:"snapshot_rev,omitempty"`
	OpCount     int    `json:"ops"`
	AddCount    int    `json:"adds"`
	ReplaceCount int   `json:"replaces"`
	RemoveCount int    `json:"removes"`
}

// LoadPatch reads and parses a patch file.
func LoadPatch(path string) (Patch, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read patch: %w", err)
	}

	var patch Patch
	if err := json.Unmarshal(data, &patch); err != nil {
		return nil, fmt.Errorf("failed to parse patch: %w", err)
	}

	return patch, nil
}

// Save writes a patch to a file.
func (p Patch) Save(path string) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal patch: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write patch: %w", err)
	}

	return nil
}

// CountOps returns counts of operations by type.
func (p Patch) CountOps() (adds, replaces, removes int) {
	for _, op := range p {
		switch op.Op {
		case "add":
			adds++
		case "replace":
			replaces++
		case "remove":
			removes++
		}
	}
	return
}

// ValidationError represents a domain validation error.
type ValidationError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}
