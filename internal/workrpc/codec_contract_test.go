package workrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCodec_RequestRoundtrip(t *testing.T) {
	original := Request{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req_init_1"`),
		Method:  "rpc.initialize",
		Params:  json.RawMessage(`{"protocolVersion":"2026-06-30"}`),
	}
	frame, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	frame = append(frame, '\n')

	got, err := NewReader(bytes.NewReader(frame), DefaultMaxFrameBytes).ReadRequest()
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}
	if got.JSONRPC != original.JSONRPC || string(got.ID) != string(original.ID) || got.Method != original.Method {
		t.Fatalf("request roundtrip mismatch: got %+v want %+v", got, original)
	}
	var gotParams, wantParams map[string]any
	if err := json.Unmarshal(got.Params, &gotParams); err != nil {
		t.Fatalf("decode roundtrip params: %v", err)
	}
	if err := json.Unmarshal(original.Params, &wantParams); err != nil {
		t.Fatalf("decode original params: %v", err)
	}
	if gotParams["protocolVersion"] != wantParams["protocolVersion"] {
		t.Fatalf("protocolVersion = %v, want %v", gotParams["protocolVersion"], wantParams["protocolVersion"])
	}
}

func TestCodec_ResponseEncoding(t *testing.T) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req_init_1"`),
		Result:  json.RawMessage(`{"protocolVersion":"2026-06-30"}`),
	}
	var buf bytes.Buffer
	if err := NewWriter(&buf).WriteResponse(resp); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}
	frame := buf.String()
	if !strings.HasSuffix(frame, "\n") || strings.Count(frame, "\n") != 1 {
		t.Fatalf("response is not one newline-terminated frame: %q", frame)
	}
	var decoded Response
	if err := json.Unmarshal([]byte(strings.TrimSuffix(frame, "\n")), &decoded); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if decoded.JSONRPC != "2.0" || string(decoded.ID) != `"req_init_1"` {
		t.Fatalf("unexpected response envelope: %+v", decoded)
	}
}

func TestCodec_ErrorResponseEncoding(t *testing.T) {
	resp := Response{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`"req_2"`),
		Error: &RPCError{
			Code:    -32009,
			Message: "workflow revision mismatch",
			Data:    json.RawMessage(`{"code":"WRKF_STALE_REVISION","retryable":true}`),
		},
	}
	var buf bytes.Buffer
	if err := NewWriter(&buf).WriteResponse(resp); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}
	var decoded Response
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if decoded.Error == nil || decoded.Error.Code != -32009 {
		t.Fatalf("unexpected error envelope: %+v", decoded.Error)
	}
	var data map[string]any
	if err := json.Unmarshal(decoded.Error.Data, &data); err != nil {
		t.Fatalf("decode error data: %v", err)
	}
	if data["code"] != "WRKF_STALE_REVISION" || data["retryable"] != true {
		t.Fatalf("unexpected error data: %+v", data)
	}
}

func TestCodec_MaxFrameSizeRejected(t *testing.T) {
	const maxBytes = 64
	big, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "req_big",
		"method":  "wrkf.instance.next",
		"params":  map[string]any{"task": strings.Repeat("X", 256)},
	})
	if err != nil {
		t.Fatalf("marshal oversized request: %v", err)
	}
	small := []byte(`{"jsonrpc":"2.0","id":"req_small","method":"wrkf.instance.next"}`)
	input := append(append(big, '\n'), append(small, '\n')...)
	r := NewReader(bytes.NewReader(input), maxBytes)

	_, err = r.ReadRequest()
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("oversized frame error = %T %v, want ValidationError", err, err)
	}
	next, err := r.ReadRequest()
	if err != nil {
		t.Fatalf("ReadRequest after oversized frame: %v", err)
	}
	if next.Method != "wrkf.instance.next" {
		t.Fatalf("next method = %q", next.Method)
	}
}
