package workrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMethodCatalogMatchesRegistry(t *testing.T) {
	registered := NewRegistry(nil, RegistryOptions{}).RegisteredMethods()
	catalog := MethodCatalog()
	if len(registered) != len(catalog) {
		t.Fatalf("registered method count = %d, catalog count = %d", len(registered), len(catalog))
	}
	for i := range catalog {
		if registered[i] != catalog[i] {
			t.Fatalf("method %d = %q, catalog = %q", i, registered[i], catalog[i])
		}
	}
}

func TestCancellationNotificationAccepted(t *testing.T) {
	var output bytes.Buffer
	srv := NewServer(&output)
	srv.Register("rpc.initialize", HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"protocolVersion":"2026-06-30"}`), nil
	}))
	requests := "" +
		`{"jsonrpc":"2.0","method":"$/cancelRequest","params":{"id":"req_pending"}}` + "\n" +
		`{"jsonrpc":"2.0","id":"req_init","method":"rpc.initialize","params":{}}` + "\n"
	if err := srv.Serve(context.Background(), strings.NewReader(requests)); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"id":"req_init"`) {
		t.Fatalf("cancel notification emitted a response or blocked initialize: %q", output.String())
	}
}

func TestStdoutPurity_HandlerMustNotCorruptStream(t *testing.T) {
	origOut := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = origOut })

	srv := NewServer(writer)
	srv.Register("rpc.initialize", HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"protocolVersion":"2026-06-30"}`), nil
	}))
	srv.Register("wrkf.workflow.show", HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		fmt.Fprintln(os.Stdout, "handler diagnostic must not reach the protocol stream")
		return json.RawMessage(`{"workflow":null}`), nil
	}))
	requests := "" +
		`{"jsonrpc":"2.0","id":"req_1","method":"rpc.initialize","params":{}}` + "\n" +
		`{"jsonrpc":"2.0","id":"req_2","method":"wrkf.workflow.show","params":{}}` + "\n"
	if err := srv.Serve(context.Background(), strings.NewReader(requests)); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close protocol writer: %v", err)
	}
	var captured bytes.Buffer
	if _, err := io.Copy(&captured, reader); err != nil {
		t.Fatalf("read protocol stream: %v", err)
	}
	assertOnlyRPCFrames(t, captured.String())
	if !strings.Contains(captured.String(), `"protocolVersion"`) {
		t.Fatalf("initialize response missing: %q", captured.String())
	}
}

func TestStdoutPurity_OnlyRPCFrames_ShutdownNotification(t *testing.T) {
	var output bytes.Buffer
	srv := NewServer(&output)
	srv.Register("rpc.initialize", HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"protocolVersion":"2026-06-30"}`), nil
	}))
	requests := "" +
		`{"jsonrpc":"2.0","id":"req_1","method":"rpc.initialize","params":{}}` + "\n" +
		`{"jsonrpc":"2.0","id":"req_2","method":"rpc.shutdown","params":null}` + "\n" +
		`{"jsonrpc":"2.0","method":"rpc.exit"}` + "\n"
	if err := srv.Serve(context.Background(), strings.NewReader(requests)); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	assertOnlyRPCFrames(t, output.String())
}

func TestExternalExecutionDoesNotSerializeUnrelatedRPC(t *testing.T) {
	srv := NewServer(io.Discard)
	srv.Register("rpc.initialize", HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"protocolVersion":"2026-06-30"}`), nil
	}))
	started := make(chan struct{})
	release := make(chan struct{})
	srv.Register("wrkf.hook.run", HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		close(started)
		<-release
		return json.RawMessage(`{"verdict":"pass"}`), nil
	}))
	srv.Register("wrkf.workflow.list", HandlerFunc(func(context.Context, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"templates":[]}`), nil
	}))

	initResp, ok := srv.HandleRequest(t.Context(), Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "rpc.initialize"})
	if !ok || initResp.Error != nil {
		t.Fatalf("initialize = (%#v, %v)", initResp, ok)
	}
	hookDone := make(chan Response, 1)
	go func() {
		resp, _ := srv.HandleRequest(t.Context(), Request{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "wrkf.hook.run"})
		hookDone <- resp
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("hook handler did not start")
	}

	readDone := make(chan Response, 1)
	go func() {
		resp, _ := srv.HandleRequest(t.Context(), Request{JSONRPC: "2.0", ID: json.RawMessage(`3`), Method: "wrkf.workflow.list"})
		readDone <- resp
	}()
	select {
	case resp := <-readDone:
		if resp.Error != nil || !bytes.Contains(resp.Result, []byte(`"templates"`)) {
			t.Fatalf("unrelated response = %#v", resp)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("unrelated RPC blocked behind external hook execution")
	}
	close(release)
	select {
	case resp := <-hookDone:
		if resp.Error != nil {
			t.Fatalf("hook response = %#v", resp)
		}
	case <-time.After(time.Second):
		t.Fatal("hook request did not finish")
	}
}

func assertOnlyRPCFrames(t *testing.T, output string) {
	t.Helper()
	for i, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var frame struct {
			JSONRPC string `json:"jsonrpc"`
		}
		if err := json.Unmarshal([]byte(line), &frame); err != nil || frame.JSONRPC != "2.0" {
			t.Fatalf("protocol line %d is not a JSON-RPC frame: %q (%v)", i+1, line, err)
		}
	}
}
