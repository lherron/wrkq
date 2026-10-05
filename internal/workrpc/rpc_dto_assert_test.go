//go:build wrkq_local

package workrpc_test

// rpc_dto_assert_test.go — assertions over decoded RPC result DTOs: required
// camelCase fields, exact values, forbidden DB-column leaks, and list shapes.

import "testing"

// p2AssertStr fails if result[key] is missing or not a non-empty string.
func p2AssertStr(t *testing.T, result map[string]any, key string) {
	t.Helper()
	v, ok := result[key]
	if !ok {
		t.Errorf("DTO missing required field %q", key)
		return
	}
	s, ok := v.(string)
	if !ok || s == "" {
		t.Errorf("DTO field %q must be a non-empty string, got: %T=%v", key, v, v)
	}
}

// p2AssertFieldEq fails if result[key] != want.
func p2AssertFieldEq(t *testing.T, result map[string]any, key string, want any) {
	t.Helper()
	got := result[key]
	// JSON numbers come back as float64; compare canonically.
	if got != want {
		t.Errorf("DTO field %q: want %v, got %v", key, want, got)
	}
}

// p2AssertAbsent fails if result has the given key (DB column leak check).
func p2AssertAbsent(t *testing.T, result map[string]any, key string) {
	t.Helper()
	if _, ok := result[key]; ok {
		t.Errorf("DTO must not expose DB column name %q", key)
	}
}

// p2AssertEtag fails if result["etag"] is missing or not a positive number.
func p2AssertEtag(t *testing.T, result map[string]any) {
	t.Helper()
	v, ok := result["etag"]
	if !ok {
		t.Errorf("DTO missing required field \"etag\"")
		return
	}
	n, ok := v.(float64)
	if !ok || n <= 0 {
		t.Errorf("DTO field \"etag\" must be a positive number, got: %T=%v", v, v)
	}
}

// p2AssertHasItems fails if result["items"] is missing or not a non-nil array.
func p2AssertHasItems(t *testing.T, result map[string]any, label string) {
	t.Helper()
	v, ok := result["items"]
	if !ok {
		t.Errorf("%s: result missing \"items\" field", label)
		return
	}
	if v == nil {
		t.Errorf("%s: \"items\" must not be null", label)
	}
}

func p2TaskItemByID(t *testing.T, result map[string]any, id string) map[string]any {
	t.Helper()
	items, _ := result["items"].([]any)
	for _, item := range items {
		m, _ := item.(map[string]any)
		if m["id"] == id {
			return m
		}
	}
	t.Fatalf("task.list result did not include %s; items=%#v", id, items)
	return nil
}

// mapKeys returns the keys of a map as a slice (for error messages).
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// assertBlankOrAbsent fails if result[key] is present with a non-empty string.
func assertBlankOrAbsent(t *testing.T, result map[string]any, key, label string) {
	t.Helper()
	if v, ok := result[key]; ok {
		if s, _ := v.(string); s != "" {
			t.Errorf("%s: %s must be absent or empty, got %q", label, key, s)
		}
	}
}
