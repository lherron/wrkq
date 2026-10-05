//go:build wrkq_local

package workrpc_test

// rpc_session_test.go — the package's JSON-RPC session harness: request
// encoding, the initialize/shutdown/exit framing around a test's business
// requests, and response-frame decoding. The subprocess itself is started by
// runRPCProcess (entrypoint_equivalence_test.go).

import (
	"encoding/json"
	"strings"
	"testing"
)

// mkRPC encodes a JSON-RPC 2.0 request line.
// Pass id="" for notifications (no id field → no response frame).
func mkRPC(id, method string, params any) string {
	req := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if id != "" {
		req["id"] = id
	}
	b, _ := json.Marshal(req)
	return string(b)
}

// rpcSession sandwiches reqs between rpc.initialize and rpc.shutdown + rpc.exit.
// A session yields 2+len(reqs) frames: init, one per request, shutdown (exit is
// a notification).
func rpcSession(clientName string, reqs ...string) []string {
	seq := []string{
		mkRPC("_init", "rpc.initialize", map[string]any{
			"protocolVersion": "2026-06-30",
			"client":          map[string]any{"name": clientName, "version": "0.0.1"},
		}),
	}
	seq = append(seq, reqs...)
	return append(seq,
		mkRPC("_sd", "rpc.shutdown", map[string]any{}),
		mkRPC("", "rpc.exit", nil),
	)
}

// p2Run runs one wrkq RPC session around reqs and returns all response frames
// (init + business requests + shutdown).
func p2Run(t *testing.T, dbPath string, reqs ...string) []map[string]any {
	t.Helper()
	frames := runRPC(t, "wrkq", dbPath, rpcSession("p2-smokey", reqs...))
	if want := 2 + len(reqs); len(frames) != want {
		t.Fatalf("p2Run: expected %d frames, got %d\nframes: %#v", want, len(frames), frames)
	}
	return frames
}

// p3Run runs one wrkf RPC session around reqs and returns all response frames
// (init + business requests + shutdown).
func p3Run(t *testing.T, dbPath string, reqs ...string) []map[string]any {
	t.Helper()
	frames := runRPC(t, "wrkf", dbPath, rpcSession("p3-smokey", reqs...))
	if want := 2 + len(reqs); len(frames) != want {
		t.Fatalf("p3Run: expected %d frames, got %d\nframes: %#v", want, len(frames), frames)
	}
	return frames
}

// runRPCWithEnv is like runRPC but pins the entrypoint's caller principal to
// agent:smokey and appends extraEnv last, so a test may override either.
func runRPCWithEnv(t *testing.T, entrypoint, dbPath string, requests []string, extraEnv []string) []map[string]any {
	t.Helper()
	principalEnv := "WRKQ_PRINCIPAL_REF=agent:smokey"
	if entrypoint == "wrkf" {
		principalEnv = "WRKF_PRINCIPAL_REF=agent:smokey"
	}
	env := append(append(scopeFreeAuthorityEnv(t), principalEnv), extraEnv...)
	return runRPCProcess(t, entrypoint, dbPath, requests, env)
}

// pdRunEnv is p2Run parameterized over the entrypoint (wrkq or wrkf) and extra env.
func pdRunEnv(t *testing.T, entrypoint, dbPath string, extraEnv []string, reqs ...string) []map[string]any {
	t.Helper()
	frames := runRPCWithEnv(t, entrypoint, dbPath, rpcSession("p4-smokey", reqs...), extraEnv)
	if want := 2 + len(reqs); len(frames) != want {
		t.Fatalf("pdRunEnv(%s): expected %d frames, got %d\nframes: %#v", entrypoint, want, len(frames), frames)
	}
	return frames
}

// filterEnv returns a copy of env without entries for key.
func filterEnv(env []string, key string) []string {
	prefix := key + "="
	var out []string
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return out
}

// createTaskRPC creates a task via wrkq.task.create and returns (id, uuid).
func createTaskRPC(t *testing.T, dbPath, title string) (id, uuid string) {
	t.Helper()
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": title, "kind": "task"}),
	)
	return createdTaskIDs(t, frames[1])
}

// createdTaskIDs returns (id, uuid) from a wrkq.task.create response frame.
func createdTaskIDs(t *testing.T, frame map[string]any) (id, uuid string) {
	t.Helper()
	result := p2ResultOrFail(t, frame, "wrkq.task.create")
	id, _ = result["id"].(string)
	uuid, _ = result["uuid"].(string)
	if id == "" || uuid == "" {
		t.Fatalf("createTaskRPC: empty id/uuid: %#v", result)
	}
	return id, uuid
}

// p2ResultOrFail returns the result map from a frame, failing the test if the
// frame carries an error.
func p2ResultOrFail(t *testing.T, frame map[string]any, label string) map[string]any {
	t.Helper()
	if errObj, ok := frame["error"]; ok {
		t.Fatalf("%s: expected result, got error: %v", label, errObj)
	}
	return resultMap(t, frame)
}

// p2ErrCode extracts error.data.code from a response frame.
func p2ErrCode(frame map[string]any) string {
	code, _ := p2ErrDataField(frame, "code").(string)
	return code
}

// p2ErrDataField extracts a field from error.data.
func p2ErrDataField(frame map[string]any, field string) any {
	errObj, _ := frame["error"].(map[string]any)
	data, _ := errObj["data"].(map[string]any)
	return data[field]
}

// p4IsStubError reports whether the frame carries the P1 placeholder error
// ("method is registered but not implemented in P1").
func p4IsStubError(frame map[string]any) bool {
	errObj, _ := frame["error"].(map[string]any)
	if errObj == nil {
		return false
	}
	msg, _ := errObj["message"].(string)
	return strings.Contains(msg, "not implemented in P1")
}

// p4AssertNotStub fails if the frame carries the P1 placeholder error.
func p4AssertNotStub(t *testing.T, frame map[string]any, label string) {
	t.Helper()
	if p4IsStubError(frame) {
		t.Errorf("%s: got stub error 'not implemented in P1'; method must be fully implemented", label)
	}
}

// p4AssertDomainError fails unless the frame is a real (non-stub) error whose
// error.data.code is wantCode.
func p4AssertDomainError(t *testing.T, frame map[string]any, wantCode, label string) {
	t.Helper()
	if code := p2ErrCode(frame); code != wantCode {
		t.Errorf("%s: want %s, got %q", label, wantCode, code)
	}
	p4AssertNotStub(t, frame, label+" must be a real domain error")
}
