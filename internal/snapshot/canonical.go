package snapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// CanonicalJSON produces a deterministic JSON encoding following JCS-like rules:
// - Top-level sections in a fixed order (meta first), omitted when empty
// - Every object key below the top level sorted lexicographically
// - No insignificant whitespace, no HTML escaping, UTF-8
// - Absent values omitted via the entry structs' omitempty tags
//
// Entries are encoded generically from their tagged structs, so a column
// added to an entry struct can never be silently left out of the canonical
// bytes (T-07498: hand-listed per-field builders dropped containers.kind).
func CanonicalJSON(s *Snapshot) ([]byte, error) {
	tasks := s.Tasks
	if len(tasks) > 0 {
		tasks = make(map[string]TaskEntry, len(s.Tasks))
		for uuid, task := range s.Tasks {
			if len(task.Labels) > 0 {
				sorted := append([]string(nil), task.Labels...)
				sort.Strings(sorted)
				task.Labels = sorted
			}
			tasks[uuid] = task
		}
	}

	sections := []struct {
		key     string
		value   interface{}
		present bool
	}{
		{"meta", s.Meta, true},
		{"containers", s.Containers, len(s.Containers) > 0},
		{"tasks", tasks, len(tasks) > 0},
		{"promises", s.Promises, len(s.Promises) > 0},
		{"comments", s.Comments, len(s.Comments) > 0},
		{"links", s.Links, len(s.Links) > 0},
		{"events", s.Events, len(s.Events) > 0},
		{"project_events", s.ProjectEvents, len(s.ProjectEvents) > 0},
	}

	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	for _, section := range sections {
		if !section.present {
			continue
		}
		if !first {
			buf.WriteByte(',')
		}
		first = false
		value, err := canonicalValue(section.value)
		if err != nil {
			return nil, fmt.Errorf("failed to encode snapshot %s: %w", section.key, err)
		}
		buf.WriteByte('"')
		buf.WriteString(section.key)
		buf.WriteString(`":`)
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// canonicalValue encodes v with every object key sorted: struct fields are
// marshalled through their tags, decoded to generic maps (numbers kept
// verbatim) and re-encoded, which sorts map keys.
func canonicalValue(v interface{}) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var generic interface{}
	if err := decoder.Decode(&generic); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(generic); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

// ComputeSnapshotRev computes the sha256 hash of canonical JSON bytes.
// Returns "sha256:<hex>" format.
func ComputeSnapshotRev(data []byte) string {
	hash := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(hash[:])
}

// PrettyJSON produces human-readable indented JSON (non-canonical).
// Useful for debugging but not for deterministic comparison.
func PrettyJSON(s *Snapshot) ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}
