package workrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ServeRemoteStdio serves the stdio JSON-RPC protocol by forwarding request
// frames to a remote wrkqd /v1/rpc endpoint. It preserves caller IDs and forwards
// rpc.initialize to the canonical server; it does not synthesize protocol
// metadata or open local durable state.
func ServeRemoteStdio(ctx context.Context, in io.Reader, out io.Writer, endpoint, token string) error {
	reader := NewReader(in, DefaultMaxFrameBytes)
	writer := NewWriter(out)
	client := http.DefaultClient
	url := "http://" + endpoint + "/v1/rpc"
	shutdown := false
	for {
		req, err := reader.ReadRequest()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			resp := Response{
				JSONRPC: "2.0",
				ID:      json.RawMessage(`null`),
				Error:   MapError(err),
			}
			_ = writer.WriteResponse(resp)
			continue
		}
		if len(req.ID) == 0 {
			if req.Method == "rpc.exit" {
				return nil
			}
			continue
		}
		if req.Method == "rpc.shutdown" {
			shutdown = true
			if err := writer.WriteResponse(Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result:  json.RawMessage(`{}`),
			}); err != nil {
				return err
			}
			continue
		}
		if shutdown {
			if err := writer.WriteResponse(Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   MapError(NewValidationError("server is shutting down", nil)),
			}); err != nil {
				return err
			}
			continue
		}
		resp, err := forwardRemoteFrame(ctx, client, url, token, req)
		if err != nil {
			resp = Response{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   MapError(err),
			}
		}
		if err := writer.WriteResponse(resp); err != nil {
			return err
		}
	}
}

func forwardRemoteFrame(ctx context.Context, client *http.Client, url, token string, frame Request) (Response, error) {
	body, err := json.Marshal(frame)
	if err != nil {
		return Response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("remote workrpc request %s: %w", frame.Method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var rpcResp Response
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return Response{}, fmt.Errorf("decode remote workrpc response %s: %w", frame.Method, err)
	}
	if rpcResp.JSONRPC == "" {
		rpcResp.JSONRPC = "2.0"
	}
	if len(rpcResp.ID) == 0 {
		rpcResp.ID = frame.ID
	}
	return rpcResp, nil
}
