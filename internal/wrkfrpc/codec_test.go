package wrkfrpc

// codec_test.go — NDJSON frame encode/decode roundtrip (§1 contract).
//
// References Request, Response, RPCError, NewWriter, NewReader, and ValidationError
// which DO NOT EXIST YET → compile failure = TDD red state.
//
// When green: every frame written by the server is a single compact JSON object
// terminated by exactly one '\n'.  Pretty-printing on stdout is forbidden.
// Over-limit inbound frames must yield a WRKF_VALIDATION error and leave the
// connection usable for the next frame.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestCodec_RequestRoundtrip verifies that encoding a Request as NDJSON and
// decoding it back yields the original struct unchanged.
// Contract §1: "One compact JSON object per line on stdout, terminated by '\n'."
func TestCodec_RequestRoundtrip(t *testing.T) {
	const (
		reqID  = `"req_init_1"`
		method = "wrkf.initialize"
	)
	params, _ := json.Marshal(map[string]any{
		"protocolVersion": "2026-06-01",
		"client":          map[string]any{"name": "test-client", "version": "0.0.1"},
	})

	original := Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(reqID),
		Method:  method,
		Params:  json.RawMessage(params),
	}

	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteRequest(original); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	line := buf.String()

	// Frame must be terminated by exactly one newline.
	if !strings.HasSuffix(line, "\n") {
		t.Errorf("NDJSON frame must end with '\\n'; got: %q", line)
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("compact frame must have exactly one '\\n' (at end); got %d newlines in: %q",
			strings.Count(line, "\n"), line)
	}
	// Pretty-printing is forbidden: no leading spaces or tabs.
	if strings.Contains(line, "  ") || strings.Contains(line, "\t") {
		t.Errorf("pretty JSON is forbidden on the wire; got: %q", line)
	}

	// Decode and assert roundtrip equality.
	r := NewReader(&buf, 8<<20)
	got, err := r.ReadRequest()
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}
	if got.JSONRPC != original.JSONRPC {
		t.Errorf("JSONRPC = %q, want %q", got.JSONRPC, original.JSONRPC)
	}
	if string(got.ID) != reqID {
		t.Errorf("ID = %s, want %s", got.ID, reqID)
	}
	if got.Method != method {
		t.Errorf("Method = %q, want %q", got.Method, method)
	}
	// Params must decode back to equivalent JSON.
	var gotParams, wantParams map[string]any
	if err := json.Unmarshal(got.Params, &gotParams); err != nil {
		t.Fatalf("Params not valid JSON: %v", err)
	}
	if err := json.Unmarshal(params, &wantParams); err != nil {
		t.Fatalf("original Params not valid JSON: %v", err)
	}
	if gotParams["protocolVersion"] != wantParams["protocolVersion"] {
		t.Errorf("Params.protocolVersion = %v, want %v",
			gotParams["protocolVersion"], wantParams["protocolVersion"])
	}
}

// TestCodec_ResponseEncoding verifies that a Response is written as a single
// compact JSON line terminated by '\n', and that the line is valid JSON containing
// the required "jsonrpc" field.
func TestCodec_ResponseEncoding(t *testing.T) {
	result, _ := json.Marshal(map[string]any{
		"protocolVersion": "2026-06-01",
		"server":          map[string]any{"name": "wrkf", "version": "test", "pid": 99},
		"capabilities":    map[string]any{"cancel": true, "effectClaimLease": true},
		"schemaHash":      "sha256:deadbeef",
		"database":        map[string]any{"path": "/tmp/test.db"},
	})

	resp := Response{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req_init_1"`),
		Result:  json.RawMessage(result),
	}

	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteResponse(resp); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}

	line := buf.String()

	if !strings.HasSuffix(line, "\n") {
		t.Errorf("response frame must end with '\\n'")
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("response must be a single compact line; got %d newlines in: %q",
			strings.Count(line, "\n"), line)
	}

	// Must be parseable as a JSON-RPC 2.0 envelope.
	var frame struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
	}
	stripped := strings.TrimSuffix(line, "\n")
	if err := json.Unmarshal([]byte(stripped), &frame); err != nil {
		t.Fatalf("response frame is not valid JSON: %v — frame: %q", err, line)
	}
	if frame.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q, want \"2.0\"", frame.JSONRPC)
	}
}

// TestCodec_ErrorResponseEncoding verifies that an error Response round-trips
// through the codec and preserves the RPCError code and message.
func TestCodec_ErrorResponseEncoding(t *testing.T) {
	errData, _ := json.Marshal(map[string]any{
		"code":             "WRKF_STALE_REVISION",
		"instanceId":       "wfi_abc",
		"expectedRevision": 3,
		"actualRevision":   4,
		"retryable":        true,
	})

	resp := Response{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req_2"`),
		Error: &RPCError{
			Code:    -32009,
			Message: "workflow revision mismatch",
			Data:    json.RawMessage(errData),
		},
	}

	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteResponse(resp); err != nil {
		t.Fatalf("WriteResponse (error): %v", err)
	}

	line := strings.TrimSuffix(buf.String(), "\n")
	var envelope struct {
		JSONRPC string `json:"jsonrpc"`
		Error   *struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &envelope); err != nil {
		t.Fatalf("error response not valid JSON: %v", err)
	}
	if envelope.Error == nil {
		t.Fatal("error field missing in error response")
	}
	if envelope.Error.Code != -32009 {
		t.Errorf("error.code = %d, want -32009", envelope.Error.Code)
	}

	var data map[string]any
	if err := json.Unmarshal(envelope.Error.Data, &data); err != nil {
		t.Fatalf("error.data not valid JSON: %v", err)
	}
	if data["code"] != "WRKF_STALE_REVISION" {
		t.Errorf("error.data.code = %v, want \"WRKF_STALE_REVISION\"", data["code"])
	}
}

// TestCodec_MaxFrameSizeRejected verifies that a frame exceeding maxBytes is
// rejected with a ValidationError and the reader remains usable for the next frame.
// Contract §1: "oversize inbound frame → WRKF_VALIDATION, connection stays open."
func TestCodec_MaxFrameSizeRejected(t *testing.T) {
	const maxBytes = 64 // tiny limit so a normal request exceeds it

	big, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "req_big",
		"method":  "wrkf.task.inspect",
		"params":  map[string]any{"task": strings.Repeat("X", 256)},
	})
	big = append(big, '\n')

	// A valid small frame to confirm the connection survives the oversize rejection.
	small, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "req_small",
		"method":  "wrkf.next",
		"params":  map[string]any{"task": "T-00001"},
	})
	small = append(small, '\n')

	combined := append(big, small...)
	r := NewReader(bytes.NewReader(combined), maxBytes)

	_, err := r.ReadRequest()
	if err == nil {
		t.Fatal("expected error for oversized frame, got nil")
	}
	var ve *ValidationError
	if !isValidationError(err, &ve) {
		t.Errorf("expected *ValidationError (WRKF_VALIDATION) for oversized frame, got %T: %v", err, err)
	}

	// Connection must stay open — next read should succeed.
	next, err := r.ReadRequest()
	if err != nil {
		t.Fatalf("ReadRequest after oversized frame rejection: %v", err)
	}
	if next.Method != "wrkf.next" {
		t.Errorf("next.Method = %q, want \"wrkf.next\"", next.Method)
	}
}
