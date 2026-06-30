package taskdocs_test

import (
	"encoding/json"
	"html"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPublicTaskDTOPageDocumentsDistinctTaskDTOSurfaces(t *testing.T) {
	root := repoRoot(t)
	page := readText(t, filepath.Join(root, "docs/html/WRKQ_TASK_PUBLIC_DTO.html"))
	text := normalizeHTMLText(page)

	// T-05264 red bar: the public page must document the public surfaces as
	// separate contracts, not collapse every caller onto domain.Task.
	required := []string{
		"internal/wrkqapi.WrkqTask",
		"WrkqTask",
		"wrkq.task.create",
		"wrkq.task.show",
		"wrkq.task.list",
		"camelCase",
		"internal/wrkqapi.WrkqTaskCatView",
		"WrkqTaskCatView",
		"wrkq cat --json",
		"wrkq.task.catView",
		"snake_case",
		"internal/domain.Task",
		"persisted",
		"sdk_session_id",
		"WRKQ_TASK_CONTRACT.html",
	}
	for _, needle := range required {
		if !strings.Contains(text, needle) {
			t.Errorf("WRKQ_TASK_PUBLIC_DTO.html must document %q", needle)
		}
	}

	forbidden := []string{
		"The canonical task object is the JSON projection of domain.Task",
		"domain.Task is the JSON DTO source of truth",
		"canonical public task-object contract",
	}
	for _, needle := range forbidden {
		if strings.Contains(text, needle) {
			t.Errorf("WRKQ_TASK_PUBLIC_DTO.html still presents domain.Task as the universal public DTO source of truth: %q", needle)
		}
	}
}

func TestTaskDataContractArtifactDocumentsCompositeOriginsAndProvenance(t *testing.T) {
	root := repoRoot(t)
	path := filepath.Join(root, "docs/archdoc/artifacts/data-contract/wrkq-task.v1.json")
	raw := readText(t, path)

	var artifact map[string]any
	if err := json.Unmarshal([]byte(raw), &artifact); err != nil {
		t.Fatalf("data-contract artifact must parse as JSON: %v", err)
	}
	if got := artifact["id"]; got != "artifact:wrkq/task-data-contract" {
		t.Fatalf("artifact id = %v, want artifact:wrkq/task-data-contract", got)
	}
	if got := artifact["profile"]; got != "data-contract.v1" {
		t.Fatalf("artifact profile = %v, want data-contract.v1", got)
	}

	allText := flattenJSONText(artifact)
	requiredProvenance := []string{
		"internal/wrkqapi/types.go",
		"internal/wrkqapi/catview.go",
		"internal/domain/types.go",
		"internal/domain/validation.go",
		"internal/domain/state.go",
		"internal/paths/slug.go",
		"internal/db/migrations",
		"WrkqTask",
		"WrkqTaskCatView",
		"domain.Task",
		"composite",
	}
	for _, needle := range requiredProvenance {
		if !strings.Contains(allText, needle) {
			t.Errorf("data-contract artifact must cite/document %q", needle)
		}
	}

	assertFieldOrigin(t, artifact, "path", "read-projection")
	assertFieldOrigin(t, artifact, "artifact_dir", "read-projection")
	assertFieldOrigin(t, artifact, "uuid", "persisted")
}

func assertFieldOrigin(t *testing.T, artifact map[string]any, fieldName, wantOrigin string) {
	t.Helper()

	field, ok := findObjectByName(artifact, fieldName)
	if !ok {
		t.Fatalf("data-contract artifact must include field %q", fieldName)
	}
	if got, _ := field["origin"].(string); got != wantOrigin {
		t.Errorf("field %q origin = %q, want %q", fieldName, got, wantOrigin)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root containing go.mod")
		}
		dir = parent
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func normalizeHTMLText(s string) string {
	s = html.UnescapeString(s)
	var b strings.Builder
	inTag := false
	lastSpace := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		case r == '>':
			inTag = false
		case inTag:
		case r == '\n' || r == '\r' || r == '\t' || r == ' ':
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		default:
			b.WriteRune(r)
			lastSpace = false
		}
	}
	return b.String()
}

func flattenJSONText(v any) string {
	var parts []string
	var walk func(any)
	walk = func(x any) {
		switch x := x.(type) {
		case map[string]any:
			for k, v := range x {
				parts = append(parts, k)
				walk(v)
			}
		case []any:
			for _, v := range x {
				walk(v)
			}
		case string:
			parts = append(parts, x)
		}
	}
	walk(v)
	return strings.Join(parts, "\n")
}

func findObjectByName(v any, name string) (map[string]any, bool) {
	switch x := v.(type) {
	case map[string]any:
		if got, _ := x["name"].(string); got == name {
			return x, true
		}
		for _, child := range x {
			if found, ok := findObjectByName(child, name); ok {
				return found, true
			}
		}
	case []any:
		for _, child := range x {
			if found, ok := findObjectByName(child, name); ok {
				return found, true
			}
		}
	}
	return nil, false
}
