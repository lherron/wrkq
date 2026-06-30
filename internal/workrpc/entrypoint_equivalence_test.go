package workrpc_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/db"
)

func TestEntrypointEquivalence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping go run integration in short mode")
	}

	wrkqDB := migratedDB(t)
	wrkfDB := migratedDB(t)

	wrkqInit, wrkqList, wrkqDomain := runRPCSequence(t, "wrkq", wrkqDB)
	wrkfInit, wrkfList, wrkfDomain := runRPCSequence(t, "wrkf", wrkfDB)

	if wrkqInit["protocolVersion"] != "2026-06-30" || wrkfInit["protocolVersion"] != "2026-06-30" {
		t.Fatalf("protocolVersion mismatch: wrkq=%v wrkf=%v", wrkqInit["protocolVersion"], wrkfInit["protocolVersion"])
	}
	if _, ok := wrkqInit["schemaHash"]; ok {
		t.Fatalf("wrkq initialize returned legacy schemaHash")
	}
	if _, ok := wrkfInit["schemaHash"]; ok {
		t.Fatalf("wrkf initialize returned legacy schemaHash")
	}

	wrkqComparable := normalizeInitialize(wrkqInit)
	wrkfComparable := normalizeInitialize(wrkfInit)
	if !reflect.DeepEqual(wrkqComparable, wrkfComparable) {
		t.Fatalf("initialize results differ beyond entrypoint/db diagnostics\nwrkq=%#v\nwrkf=%#v", wrkqComparable, wrkfComparable)
	}
	if entrypoint(t, wrkqInit) != "wrkq" || entrypoint(t, wrkfInit) != "wrkf" {
		t.Fatalf("unexpected entrypoints: wrkq=%q wrkf=%q", entrypoint(t, wrkqInit), entrypoint(t, wrkfInit))
	}
	if !reflect.DeepEqual(wrkqList, wrkfList) {
		t.Fatalf("sample success differs\nwrkq=%#v\nwrkf=%#v", wrkqList, wrkfList)
	}
	if !reflect.DeepEqual(wrkqDomain, wrkfDomain) {
		t.Fatalf("sample domain error differs\nwrkq=%#v\nwrkf=%#v", wrkqDomain, wrkfDomain)
	}

	wrkqPreinit := runPreinitValidation(t, "wrkq", migratedDB(t))
	wrkfPreinit := runPreinitValidation(t, "wrkf", migratedDB(t))
	if !reflect.DeepEqual(wrkqPreinit, wrkfPreinit) {
		t.Fatalf("pre-initialize validation differs\nwrkq=%#v\nwrkf=%#v", wrkqPreinit, wrkfPreinit)
	}
}

func runRPCSequence(t *testing.T, entrypoint, dbPath string) (map[string]any, map[string]any, map[string]any) {
	t.Helper()
	frames := runRPC(t, entrypoint, dbPath, []string{
		`{"jsonrpc":"2.0","id":"init","method":"rpc.initialize","params":{"protocolVersion":"2026-06-30","client":{"name":"test","version":"0.0.1"}}}`,
		`{"jsonrpc":"2.0","id":"list","method":"wrkf.workflow.list","params":{}}`,
		`{"jsonrpc":"2.0","id":"domain","method":"wrkq.task.create","params":{}}`,
		`{"jsonrpc":"2.0","id":"shutdown","method":"rpc.shutdown","params":{}}`,
		`{"jsonrpc":"2.0","method":"rpc.exit"}`,
	})
	if len(frames) != 4 {
		t.Fatalf("%s: got %d response frames, want 4: %#v", entrypoint, len(frames), frames)
	}
	return resultMap(t, frames[0]), resultMap(t, frames[1]), errorShape(t, frames[2])
}

func runPreinitValidation(t *testing.T, entrypoint, dbPath string) map[string]any {
	t.Helper()
	frames := runRPC(t, entrypoint, dbPath, []string{
		`{"jsonrpc":"2.0","id":"pre","method":"wrkq.task.create","params":{"title":"before init"}}`,
		`{"jsonrpc":"2.0","method":"rpc.exit"}`,
	})
	if len(frames) != 1 {
		t.Fatalf("%s preinit: got %d response frames, want 1: %#v", entrypoint, len(frames), frames)
	}
	return errorShape(t, frames[0])
}

func runRPC(t *testing.T, entrypoint, dbPath string, requests []string) []map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	args := []string{"run", "./cmd/" + entrypoint, "--db", dbPath, "rpc", "--stdio"}
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = repoRoot(t)
	// Principal-only attribution: supply a default caller principal for the rpc
	// server (mirrors a configured agent session). Mutation steps that pass an
	// explicit `actor: agent:<id>` override this; seed steps fall back to it.
	cmd.Env = append(os.Environ(), "WRKQ_PRINCIPAL_REF=agent:smokey")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", entrypoint, err)
	}
	for _, req := range requests {
		if _, err := stdin.Write([]byte(req + "\n")); err != nil {
			t.Fatalf("%s write request: %v", entrypoint, err)
		}
	}
	_ = stdin.Close()

	var frames []map[string]any
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		var frame map[string]any
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("%s stdout contained non-JSON-RPC line %q: %v; stderr=%s", entrypoint, line, err, stderr.String())
		}
		if frame["jsonrpc"] != "2.0" {
			t.Fatalf("%s stdout frame missing jsonrpc=2.0: %s", entrypoint, line)
		}
		frames = append(frames, frame)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("%s stdout scan: %v", entrypoint, err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("%s failed: %v; stderr=%s", entrypoint, err, stderr.String())
	}
	return frames
}

func resultMap(t *testing.T, frame map[string]any) map[string]any {
	t.Helper()
	result, ok := frame["result"].(map[string]any)
	if !ok {
		t.Fatalf("frame has no object result: %#v", frame)
	}
	return result
}

func errorShape(t *testing.T, frame map[string]any) map[string]any {
	t.Helper()
	errObj, ok := frame["error"].(map[string]any)
	if !ok {
		t.Fatalf("frame has no error object: %#v", frame)
	}
	data, _ := errObj["data"].(map[string]any)
	return map[string]any{
		"code":      errObj["code"],
		"dataCode":  data["code"],
		"retryable": data["retryable"],
	}
}

func normalizeInitialize(init map[string]any) map[string]any {
	out := cloneMap(init)
	server := cloneMap(out["server"].(map[string]any))
	delete(server, "pid")
	delete(server, "entrypoint")
	out["server"] = server
	database := cloneMap(out["database"].(map[string]any))
	delete(database, "path")
	out["database"] = database
	return out
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func entrypoint(t *testing.T, init map[string]any) string {
	t.Helper()
	server, ok := init["server"].(map[string]any)
	if !ok {
		t.Fatalf("initialize result has no server object: %#v", init)
	}
	value, _ := server["entrypoint"].(string)
	return value
}

func migratedDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wrkq.db")
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open temp db: %v", err)
	}
	defer func() { _ = database.Close() }()
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate temp db: %v", err)
	}
	return path
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}
