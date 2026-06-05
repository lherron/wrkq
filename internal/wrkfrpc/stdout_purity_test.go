package wrkfrpc

// stdout_purity_test.go — stdout MUST contain ONLY valid JSON-RPC frames (§1 contract).
//
// References NewServer, HandlerFunc, and (*Server).Register / (*Server).Serve
// which DO NOT EXIST YET → compile failure = TDD red state.
//
// When green: the server must ensure that if a handler (or hook) writes a plain
// text line directly to os.Stdout, that line does NOT appear on the protocol
// stream — the test FAILS if any non-JSON line appears on stdout.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// TestStdoutPurity_HandlerMustNotCorruptStream verifies that a misbehaving
// handler writing a plain-text log line to os.Stdout does NOT corrupt the
// JSON-RPC protocol stream.
//
// The test captures everything written to os.Stdout and asserts that every
// non-empty line is a valid JSON-RPC 2.0 frame.  If "log: ..." appears, the
// test FAILS — which is the intended behaviour: the server must redirect any
// accidental os.Stdout writes away from the protocol stream.
func TestStdoutPurity_HandlerMustNotCorruptStream(t *testing.T) {
	// Replace os.Stdout with a pipe so we capture everything written to it.
	// This simulates the production scenario where wrkf rpc --stdio writes to
	// real stdout and any hook that accidentally calls fmt.Println corrupts it.
	origOut := os.Stdout
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = pw
	t.Cleanup(func() {
		os.Stdout = origOut
	})

	// Create a server that writes its protocol frames to the pipe (= os.Stdout).
	srv := NewServer(pw)

	// Initialize the server first (lifecycle contract §2).
	srv.Register("wrkf.initialize", HandlerFunc(func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
		result, _ := json.Marshal(map[string]any{
			"protocolVersion": "2026-06-01",
			"server":          map[string]any{"name": "wrkf", "version": "test", "pid": os.Getpid()},
			"database":        map[string]any{"path": "/tmp/test.db"},
			"capabilities":    map[string]any{"cancel": true, "effectClaimLease": true, "runExternalBinding": true},
			"schemaHash":      "sha256:testonly",
		})
		return json.RawMessage(result), nil
	}))

	// Register a misbehaving handler that writes a plain-text line to os.Stdout.
	// This simulates a hook (e.g. a pre-transition check) calling fmt.Println.
	// The server MUST prevent this from reaching the protocol stream.
	srv.Register("wrkf.task.inspect", HandlerFunc(func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
		// BAD: a hook logs to stdout instead of stderr.
		fmt.Fprintln(os.Stdout, "log: hook debug output — this must NOT appear on the protocol stream")
		result, _ := json.Marshal(map[string]any{"instance": nil})
		return json.RawMessage(result), nil
	}))

	// Feed two requests: initialize then task.inspect.
	const requests = "" +
		`{"jsonrpc":"2.0","id":"req_1","method":"wrkf.initialize","params":{"protocolVersion":"2026-06-01","client":{"name":"test","version":"0.0.1"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":"req_2","method":"wrkf.task.inspect","params":{"task":"T-00001"}}` + "\n"

	ctx := context.Background()
	serveErr := srv.Serve(ctx, strings.NewReader(requests))
	// Serve returns after stdin is exhausted; transport errors are expected.
	_ = serveErr

	// Close the write end so io.Copy can finish.
	_ = pw.Close()

	var captured bytes.Buffer
	if _, err := io.Copy(&captured, pr); err != nil {
		t.Fatalf("io.Copy from pipe: %v", err)
	}

	// Every non-empty line written to stdout must be a valid JSON-RPC 2.0 frame.
	output := captured.String()
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		if !json.Valid([]byte(line)) {
			t.Errorf("stdout purity VIOLATION at line %d — non-JSON line on protocol stream: %q", i+1, line)
			continue
		}
		var frame struct {
			JSONRPC string `json:"jsonrpc"`
		}
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Errorf("line %d: valid JSON but not a JSON object: %q", i+1, line)
			continue
		}
		if frame.JSONRPC != "2.0" {
			t.Errorf("line %d: jsonrpc field = %q, want \"2.0\": %q", i+1, frame.JSONRPC, line)
		}
	}

	// Confirm we actually received at least the initialize response.
	if !strings.Contains(output, `"protocolVersion"`) {
		t.Error("expected initialize response on stdout (containing protocolVersion), got none")
	}
}

// TestStdoutPurity_OnlyRPCFrames_ShutdownNotification verifies that a shutdown
// request and the subsequent exit notification do not cause non-JSON output.
func TestStdoutPurity_OnlyRPCFrames_ShutdownNotification(t *testing.T) {
	origOut := os.Stdout
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = pw
	t.Cleanup(func() {
		os.Stdout = origOut
	})

	srv := NewServer(pw)

	// Minimal initialize handler.
	srv.Register("wrkf.initialize", HandlerFunc(func(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
		r, _ := json.Marshal(map[string]any{
			"protocolVersion": "2026-06-01",
			"server":          map[string]any{"name": "wrkf", "version": "test", "pid": 0},
			"database":        map[string]any{"path": "/tmp/test.db"},
			"capabilities":    map[string]any{},
			"schemaHash":      "sha256:0",
		})
		return json.RawMessage(r), nil
	}))

	// initialize → shutdown → exit (notification, no id)
	const requests = "" +
		`{"jsonrpc":"2.0","id":"req_1","method":"wrkf.initialize","params":{"protocolVersion":"2026-06-01","client":{"name":"test","version":"0"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":"req_2","method":"wrkf.shutdown","params":null}` + "\n" +
		`{"jsonrpc":"2.0","method":"wrkf.exit"}` + "\n"

	ctx := context.Background()
	_ = srv.Serve(ctx, strings.NewReader(requests))
	_ = pw.Close()

	var captured bytes.Buffer
	_, _ = io.Copy(&captured, pr)

	output := captured.String()
	for i, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if line == "" {
			continue
		}
		if !json.Valid([]byte(line)) {
			t.Errorf("line %d is not JSON (purity violation): %q", i+1, line)
		}
	}
}
