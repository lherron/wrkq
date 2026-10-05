//go:build wrkq_local

package workrpc_test

// rpc_workflow_fixture_test.go — workflow fixtures for RPC tests: the canonical
// wrkq-code-change@1 template, the install/attach requests that put a task
// under it, and the evidence payloads its red phase accepts.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// p2WorkflowTemplatePath returns the path to the canonical wrkq-code-change template.
func p2WorkflowTemplatePath(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	path := filepath.Join(root, "wrkf", "templates", "wrkq-code-change.workflow.json")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("workflow template not found at %s: %v", path, err)
	}
	return path
}

func templateBody(t *testing.T, path string) string {
	t.Helper()
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot(t), path)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read workflow template %s: %v", path, err)
	}
	return string(body)
}

// installCodeChangeReq installs the canonical wrkq-code-change@1 template.
func installCodeChangeReq(t *testing.T) string {
	t.Helper()
	return mkRPC("i1", "wrkf.workflow.install", map[string]any{"body": templateBody(t, p2WorkflowTemplatePath(t))})
}

// attachCodeChangeReq attaches wrkq-code-change@1 to taskID; an empty
// idempotencyKey is omitted.
func attachCodeChangeReq(id, taskID, idempotencyKey string) string {
	params := map[string]any{"task": taskID, "workflow": "wrkq-code-change@1"}
	if idempotencyKey != "" {
		params["idempotencyKey"] = idempotencyKey
	}
	return mkRPC(id, "wrkq.workflow.attach", params)
}

// p3InstallAndAttach installs wrkq-code-change@1 and attaches it to taskID in
// one wrkf session, returning the new instance id.
func p3InstallAndAttach(t *testing.T, dbPath, tplPath, taskID string) string {
	t.Helper()
	frames := p3Run(t, dbPath,
		mkRPC("i1", "wrkf.workflow.install", map[string]any{"body": templateBody(t, tplPath)}),
		attachCodeChangeReq("a1", taskID, ""),
	)
	p2ResultOrFail(t, frames[1], "wrkf.workflow.install")
	return attachedInstanceID(t, frames[2], "wrkq.workflow.attach")
}

// attachedInstanceID returns instance.id from a wrkq.workflow.attach frame.
func attachedInstanceID(t *testing.T, frame map[string]any, label string) string {
	t.Helper()
	attachResult := p2ResultOrFail(t, frame, label)
	inst, ok := attachResult["instance"].(map[string]any)
	if !ok || inst == nil {
		t.Fatalf("%s: attach returned no instance object; result keys: %v", label, mapKeys(attachResult))
	}
	instanceID, _ := inst["id"].(string)
	if instanceID == "" {
		t.Fatalf("%s: attach returned empty instance id", label)
	}
	return instanceID
}

// p3RedFacts is a red_test facts object; the schema requires verdict ∈
// ["red","fails_expected"].
func p3RedFacts() json.RawMessage { return json.RawMessage(`{"verdict":"red"}`) }

// redEvidenceParams is a wrkf.evidence.add request for red_test evidence by
// the tester role on taskID, with extra fields merged over it.
func redEvidenceParams(taskID, ref string, extra map[string]any) map[string]any {
	params := map[string]any{"task": taskID, "kind": "red_test", "ref": ref, "role": "tester", "facts": p3RedFacts()}
	for k, v := range extra {
		params[k] = v
	}
	return params
}

// p3EvidenceItems extracts the evidence items from a wrkf.evidence.list result
// frame, accepting a bare array or a map with "items" (or "evidence").
func p3EvidenceItems(t *testing.T, frame map[string]any, label string) []map[string]any {
	t.Helper()
	if errObj, ok := frame["error"]; ok {
		t.Fatalf("%s: unexpected error: %v", label, errObj)
	}
	var raw []any
	switch v := frame["result"].(type) {
	case []any:
		raw = v
	case map[string]any:
		raw, _ = v["items"].([]any)
		if raw == nil {
			raw, _ = v["evidence"].([]any)
		}
	}
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m, ok := item.(map[string]any); ok {
			items = append(items, m)
		}
	}
	return items
}
