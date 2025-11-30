package patch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/lherron/wrkq/internal/snapshot"
)

// Create computes a patch from base snapshot to target snapshot.
func Create(opts CreateOptions) (*CreateResult, error) {
	// Load base snapshot
	baseData, err := os.ReadFile(opts.FromPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read base snapshot: %w", err)
	}

	var base snapshot.Snapshot
	if err := json.Unmarshal(baseData, &base); err != nil {
		return nil, fmt.Errorf("failed to parse base snapshot: %w", err)
	}

	// Load target snapshot
	targetData, err := os.ReadFile(opts.ToPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read target snapshot: %w", err)
	}

	var target snapshot.Snapshot
	if err := json.Unmarshal(targetData, &target); err != nil {
		return nil, fmt.Errorf("failed to parse target snapshot: %w", err)
	}

	// Compute diff
	patch := computeDiff(&base, &target)

	// Normalize patch (remove redundant ops)
	patch = normalizePatch(patch)

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(opts.OutputPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	// Write patch
	if err := patch.Save(opts.OutputPath); err != nil {
		return nil, err
	}

	adds, replaces, removes := patch.CountOps()

	return &CreateResult{
		OutputPath:   opts.OutputPath,
		OpCount:      len(patch),
		AddCount:     adds,
		ReplaceCount: replaces,
		RemoveCount:  removes,
	}, nil
}

// computeDiff generates RFC 6902 operations to transform base into target.
func computeDiff(base, target *snapshot.Snapshot) Patch {
	var ops Patch

	// Diff actors
	ops = append(ops, diffMap("/actors", base.Actors, target.Actors,
		func(v interface{}) interface{} { return v })...)

	// Diff containers
	ops = append(ops, diffMap("/containers", base.Containers, target.Containers,
		func(v interface{}) interface{} { return v })...)

	// Diff tasks
	ops = append(ops, diffMap("/tasks", base.Tasks, target.Tasks,
		func(v interface{}) interface{} { return v })...)

	// Diff comments
	ops = append(ops, diffMap("/comments", base.Comments, target.Comments,
		func(v interface{}) interface{} { return v })...)

	// Diff links
	ops = append(ops, diffMap("/links", base.Links, target.Links,
		func(v interface{}) interface{} { return v })...)

	// Note: we don't diff meta or events - those are regenerated

	return ops
}

// diffMap computes operations for a map-based collection (keyed by UUID).
func diffMap[T any](basePath string, base, target map[string]T, normalize func(interface{}) interface{}) Patch {
	var ops Patch

	// Collect all keys
	allKeys := make(map[string]bool)
	for k := range base {
		allKeys[k] = true
	}
	for k := range target {
		allKeys[k] = true
	}

	// Sort keys for deterministic output
	keys := make([]string, 0, len(allKeys))
	for k := range allKeys {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		path := basePath + "/" + escapeJSONPointer(key)
		baseVal, inBase := base[key]
		targetVal, inTarget := target[key]

		if !inBase && inTarget {
			// Add
			ops = append(ops, Operation{
				Op:    "add",
				Path:  path,
				Value: normalize(targetVal),
			})
		} else if inBase && !inTarget {
			// Remove
			ops = append(ops, Operation{
				Op:   "remove",
				Path: path,
			})
		} else if inBase && inTarget {
			// Check for changes - compare as JSON for deep equality
			if !deepEqual(baseVal, targetVal) {
				// Replace entire entity (simpler than field-level patches)
				ops = append(ops, Operation{
					Op:    "replace",
					Path:  path,
					Value: normalize(targetVal),
				})
			}
		}
	}

	return ops
}

// deepEqual compares two values by JSON encoding.
func deepEqual(a, b interface{}) bool {
	aJSON, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bJSON, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(aJSON) == string(bJSON)
}

// escapeJSONPointer escapes special characters in JSON Pointer per RFC 6901.
func escapeJSONPointer(s string) string {
	// ~ must be escaped first, then /
	result := ""
	for _, c := range s {
		switch c {
		case '~':
			result += "~0"
		case '/':
			result += "~1"
		default:
			result += string(c)
		}
	}
	return result
}

// unescapeJSONPointer unescapes a JSON Pointer token.
func unescapeJSONPointer(s string) string {
	// ~1 -> /, ~0 -> ~
	result := ""
	i := 0
	for i < len(s) {
		if i+1 < len(s) && s[i] == '~' {
			switch s[i+1] {
			case '1':
				result += "/"
				i += 2
				continue
			case '0':
				result += "~"
				i += 2
				continue
			}
		}
		result += string(s[i])
		i++
	}
	return result
}

// normalizePatch removes redundant operations.
func normalizePatch(patch Patch) Patch {
	// For now, just return as-is. Future: remove no-op replaces, etc.
	return patch
}

// DiffSnapshots computes a patch directly from snapshot structs.
func DiffSnapshots(base, target *snapshot.Snapshot) Patch {
	patch := computeDiff(base, target)
	return normalizePatch(patch)
}

// parseJSONPointer splits a JSON Pointer into tokens.
func parseJSONPointer(path string) []string {
	if path == "" || path == "/" {
		return []string{}
	}
	if path[0] != '/' {
		return []string{path}
	}
	parts := []string{}
	for _, p := range splitPath(path[1:]) {
		parts = append(parts, unescapeJSONPointer(p))
	}
	return parts
}

func splitPath(path string) []string {
	if path == "" {
		return []string{}
	}
	result := []string{}
	current := ""
	for i := 0; i < len(path); i++ {
		if path[i] == '/' {
			result = append(result, current)
			current = ""
		} else {
			current += string(path[i])
		}
	}
	result = append(result, current)
	return result
}

// getValueAtPath retrieves a value from a snapshot at the given JSON Pointer path.
func getValueAtPath(snap *snapshot.Snapshot, path string) (interface{}, bool) {
	parts := parseJSONPointer(path)
	if len(parts) == 0 {
		return nil, false
	}

	switch parts[0] {
	case "actors":
		if len(parts) < 2 {
			return snap.Actors, true
		}
		if v, ok := snap.Actors[parts[1]]; ok {
			if len(parts) == 2 {
				return v, true
			}
			return getFieldValue(v, parts[2:])
		}
	case "containers":
		if len(parts) < 2 {
			return snap.Containers, true
		}
		if v, ok := snap.Containers[parts[1]]; ok {
			if len(parts) == 2 {
				return v, true
			}
			return getFieldValue(v, parts[2:])
		}
	case "tasks":
		if len(parts) < 2 {
			return snap.Tasks, true
		}
		if v, ok := snap.Tasks[parts[1]]; ok {
			if len(parts) == 2 {
				return v, true
			}
			return getFieldValue(v, parts[2:])
		}
	case "comments":
		if len(parts) < 2 {
			return snap.Comments, true
		}
		if v, ok := snap.Comments[parts[1]]; ok {
			if len(parts) == 2 {
				return v, true
			}
			return getFieldValue(v, parts[2:])
		}
	case "links":
		if len(parts) < 2 {
			return snap.Links, true
		}
		if v, ok := snap.Links[parts[1]]; ok {
			if len(parts) == 2 {
				return v, true
			}
			return getFieldValue(v, parts[2:])
		}
	case "meta":
		if len(parts) == 1 {
			return snap.Meta, true
		}
		return getFieldValue(snap.Meta, parts[1:])
	}

	return nil, false
}

// getFieldValue uses reflection to get a struct field by JSON tag name.
func getFieldValue(v interface{}, path []string) (interface{}, bool) {
	if len(path) == 0 {
		return v, true
	}

	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return nil, false
	}

	fieldName := path[0]
	typ := val.Type()

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		jsonTag := field.Tag.Get("json")
		// Parse json tag (could be "name,omitempty")
		tagName := jsonTag
		if idx := len(jsonTag); idx > 0 {
			for j := 0; j < len(jsonTag); j++ {
				if jsonTag[j] == ',' {
					tagName = jsonTag[:j]
					break
				}
			}
		}

		if tagName == fieldName || field.Name == fieldName {
			fieldVal := val.Field(i)
			if len(path) == 1 {
				return fieldVal.Interface(), true
			}
			return getFieldValue(fieldVal.Interface(), path[1:])
		}
	}

	return nil, false
}
