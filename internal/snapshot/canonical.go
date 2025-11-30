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
// - Keys sorted lexicographically
// - No insignificant whitespace
// - UTF-8 encoding
// - Consistent null/empty handling (omitted via omitempty tags)
func CanonicalJSON(s *Snapshot) ([]byte, error) {
	// Build a map with explicit key ordering
	ordered := buildOrderedSnapshot(s)

	// Use a custom encoder that doesn't escape HTML and uses no indentation
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(ordered); err != nil {
		return nil, fmt.Errorf("failed to encode snapshot: %w", err)
	}

	// Remove trailing newline added by Encode
	result := buf.Bytes()
	if len(result) > 0 && result[len(result)-1] == '\n' {
		result = result[:len(result)-1]
	}

	return result, nil
}

// ComputeSnapshotRev computes the sha256 hash of canonical JSON bytes.
// Returns "sha256:<hex>" format.
func ComputeSnapshotRev(data []byte) string {
	hash := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(hash[:])
}

// buildOrderedSnapshot creates an ordered map structure for canonical JSON.
// Order: meta, actors, containers, tasks, comments, links, events
func buildOrderedSnapshot(s *Snapshot) orderedMap {
	result := make(orderedMap, 0, 7)

	// meta (always present)
	result = append(result, keyValue{"meta", buildOrderedMeta(&s.Meta)})

	// actors (if non-empty)
	if len(s.Actors) > 0 {
		result = append(result, keyValue{"actors", buildOrderedActors(s.Actors)})
	}

	// containers (if non-empty)
	if len(s.Containers) > 0 {
		result = append(result, keyValue{"containers", buildOrderedContainers(s.Containers)})
	}

	// tasks (if non-empty)
	if len(s.Tasks) > 0 {
		result = append(result, keyValue{"tasks", buildOrderedTasks(s.Tasks)})
	}

	// comments (if non-empty)
	if len(s.Comments) > 0 {
		result = append(result, keyValue{"comments", buildOrderedComments(s.Comments)})
	}

	// links (if non-empty)
	if len(s.Links) > 0 {
		result = append(result, keyValue{"links", buildOrderedLinks(s.Links)})
	}

	// events (if non-empty)
	if len(s.Events) > 0 {
		result = append(result, keyValue{"events", buildOrderedEvents(s.Events)})
	}

	return result
}

// orderedMap is a slice of key-value pairs that marshals as a JSON object
// with keys in the order they appear in the slice.
type orderedMap []keyValue

type keyValue struct {
	Key   string
	Value interface{}
}

func (om orderedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')

	for i, kv := range om {
		if i > 0 {
			buf.WriteByte(',')
		}

		// Write key
		keyJSON, err := json.Marshal(kv.Key)
		if err != nil {
			return nil, err
		}
		buf.Write(keyJSON)
		buf.WriteByte(':')

		// Write value
		valJSON, err := json.Marshal(kv.Value)
		if err != nil {
			return nil, err
		}
		buf.Write(valJSON)
	}

	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func buildOrderedMeta(m *Meta) orderedMap {
	result := make(orderedMap, 0, 4)

	// Fields in lexicographic order per spec
	if m.GeneratedAt != "" {
		result = append(result, keyValue{"generated_at", m.GeneratedAt})
	}
	result = append(result, keyValue{"machine_interface_version", m.MachineInterfaceVersion})
	result = append(result, keyValue{"schema_version", m.SchemaVersion})
	if m.SnapshotRev != "" {
		result = append(result, keyValue{"snapshot_rev", m.SnapshotRev})
	}

	return result
}

func buildOrderedActors(actors map[string]ActorEntry) orderedMap {
	// Sort UUIDs lexicographically
	uuids := make([]string, 0, len(actors))
	for uuid := range actors {
		uuids = append(uuids, uuid)
	}
	sort.Strings(uuids)

	result := make(orderedMap, 0, len(actors))
	for _, uuid := range uuids {
		actor := actors[uuid]
		result = append(result, keyValue{uuid, buildOrderedActor(&actor)})
	}
	return result
}

func buildOrderedActor(a *ActorEntry) orderedMap {
	result := make(orderedMap, 0, 7)

	// Fields in lexicographic order
	result = append(result, keyValue{"created_at", a.CreatedAt})
	if a.DisplayName != "" {
		result = append(result, keyValue{"display_name", a.DisplayName})
	}
	result = append(result, keyValue{"id", a.ID})
	if a.Meta != "" {
		result = append(result, keyValue{"meta", a.Meta})
	}
	result = append(result, keyValue{"role", a.Role})
	result = append(result, keyValue{"slug", a.Slug})
	result = append(result, keyValue{"updated_at", a.UpdatedAt})

	return result
}

func buildOrderedContainers(containers map[string]ContainerEntry) orderedMap {
	uuids := make([]string, 0, len(containers))
	for uuid := range containers {
		uuids = append(uuids, uuid)
	}
	sort.Strings(uuids)

	result := make(orderedMap, 0, len(containers))
	for _, uuid := range uuids {
		container := containers[uuid]
		result = append(result, keyValue{uuid, buildOrderedContainer(&container)})
	}
	return result
}

func buildOrderedContainer(c *ContainerEntry) orderedMap {
	result := make(orderedMap, 0, 10)

	// Fields in lexicographic order
	if c.ArchivedAt != "" {
		result = append(result, keyValue{"archived_at", c.ArchivedAt})
	}
	result = append(result, keyValue{"created_at", c.CreatedAt})
	result = append(result, keyValue{"created_by", c.CreatedBy})
	result = append(result, keyValue{"etag", c.ETag})
	result = append(result, keyValue{"id", c.ID})
	if c.ParentUUID != "" {
		result = append(result, keyValue{"parent_uuid", c.ParentUUID})
	}
	result = append(result, keyValue{"slug", c.Slug})
	if c.Title != "" {
		result = append(result, keyValue{"title", c.Title})
	}
	result = append(result, keyValue{"updated_at", c.UpdatedAt})
	result = append(result, keyValue{"updated_by", c.UpdatedBy})

	return result
}

func buildOrderedTasks(tasks map[string]TaskEntry) orderedMap {
	uuids := make([]string, 0, len(tasks))
	for uuid := range tasks {
		uuids = append(uuids, uuid)
	}
	sort.Strings(uuids)

	result := make(orderedMap, 0, len(tasks))
	for _, uuid := range uuids {
		task := tasks[uuid]
		result = append(result, keyValue{uuid, buildOrderedTask(&task)})
	}
	return result
}

func buildOrderedTask(t *TaskEntry) orderedMap {
	result := make(orderedMap, 0, 16)

	// Fields in lexicographic order
	if t.ArchivedAt != "" {
		result = append(result, keyValue{"archived_at", t.ArchivedAt})
	}
	if t.CompletedAt != "" {
		result = append(result, keyValue{"completed_at", t.CompletedAt})
	}
	result = append(result, keyValue{"created_at", t.CreatedAt})
	result = append(result, keyValue{"created_by", t.CreatedBy})
	if t.Description != "" {
		result = append(result, keyValue{"description", t.Description})
	}
	if t.DueAt != "" {
		result = append(result, keyValue{"due_at", t.DueAt})
	}
	result = append(result, keyValue{"etag", t.ETag})
	result = append(result, keyValue{"id", t.ID})
	if len(t.Labels) > 0 {
		// Sort labels for determinism
		sortedLabels := make([]string, len(t.Labels))
		copy(sortedLabels, t.Labels)
		sort.Strings(sortedLabels)
		result = append(result, keyValue{"labels", sortedLabels})
	}
	result = append(result, keyValue{"priority", t.Priority})
	result = append(result, keyValue{"project_uuid", t.ProjectUUID})
	result = append(result, keyValue{"slug", t.Slug})
	if t.StartAt != "" {
		result = append(result, keyValue{"start_at", t.StartAt})
	}
	result = append(result, keyValue{"state", t.State})
	result = append(result, keyValue{"title", t.Title})
	result = append(result, keyValue{"updated_at", t.UpdatedAt})
	result = append(result, keyValue{"updated_by", t.UpdatedBy})

	return result
}

func buildOrderedComments(comments map[string]CommentEntry) orderedMap {
	uuids := make([]string, 0, len(comments))
	for uuid := range comments {
		uuids = append(uuids, uuid)
	}
	sort.Strings(uuids)

	result := make(orderedMap, 0, len(comments))
	for _, uuid := range uuids {
		comment := comments[uuid]
		result = append(result, keyValue{uuid, buildOrderedComment(&comment)})
	}
	return result
}

func buildOrderedComment(c *CommentEntry) orderedMap {
	result := make(orderedMap, 0, 10)

	// Fields in lexicographic order
	result = append(result, keyValue{"actor_uuid", c.ActorUUID})
	result = append(result, keyValue{"body", c.Body})
	result = append(result, keyValue{"created_at", c.CreatedAt})
	if c.DeletedAt != "" {
		result = append(result, keyValue{"deleted_at", c.DeletedAt})
	}
	if c.DeletedBy != "" {
		result = append(result, keyValue{"deleted_by", c.DeletedBy})
	}
	result = append(result, keyValue{"etag", c.ETag})
	result = append(result, keyValue{"id", c.ID})
	if c.Meta != "" {
		result = append(result, keyValue{"meta", c.Meta})
	}
	result = append(result, keyValue{"task_uuid", c.TaskUUID})
	if c.UpdatedAt != "" {
		result = append(result, keyValue{"updated_at", c.UpdatedAt})
	}

	return result
}

func buildOrderedLinks(links map[string]LinkEntry) orderedMap {
	uuids := make([]string, 0, len(links))
	for uuid := range links {
		uuids = append(uuids, uuid)
	}
	sort.Strings(uuids)

	result := make(orderedMap, 0, len(links))
	for _, uuid := range uuids {
		link := links[uuid]
		result = append(result, keyValue{uuid, buildOrderedLink(&link)})
	}
	return result
}

func buildOrderedLink(l *LinkEntry) orderedMap {
	result := make(orderedMap, 0, 5)

	// Fields in lexicographic order
	result = append(result, keyValue{"created_at", l.CreatedAt})
	result = append(result, keyValue{"created_by", l.CreatedBy})
	if l.ID != "" {
		result = append(result, keyValue{"id", l.ID})
	}
	result = append(result, keyValue{"link_type", l.LinkType})
	result = append(result, keyValue{"source_uuid", l.SourceUUID})
	result = append(result, keyValue{"target_uuid", l.TargetUUID})

	return result
}

func buildOrderedEvents(events map[string]EventEntry) orderedMap {
	// Sort by event ID (as string for consistency)
	ids := make([]string, 0, len(events))
	for id := range events {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	result := make(orderedMap, 0, len(events))
	for _, id := range ids {
		event := events[id]
		result = append(result, keyValue{id, buildOrderedEvent(&event)})
	}
	return result
}

func buildOrderedEvent(e *EventEntry) orderedMap {
	result := make(orderedMap, 0, 8)

	// Fields in lexicographic order
	if e.ActorUUID != "" {
		result = append(result, keyValue{"actor_uuid", e.ActorUUID})
	}
	if e.ETag != 0 {
		result = append(result, keyValue{"etag", e.ETag})
	}
	result = append(result, keyValue{"event_type", e.EventType})
	result = append(result, keyValue{"id", e.ID})
	if e.Payload != "" {
		result = append(result, keyValue{"payload", e.Payload})
	}
	result = append(result, keyValue{"resource_type", e.ResourceType})
	if e.ResourceUUID != "" {
		result = append(result, keyValue{"resource_uuid", e.ResourceUUID})
	}
	result = append(result, keyValue{"timestamp", e.Timestamp})

	return result
}

// PrettyJSON produces human-readable indented JSON (non-canonical).
// Useful for debugging but not for deterministic comparison.
func PrettyJSON(s *Snapshot) ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}
