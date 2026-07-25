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

type remoteForwardError struct {
	kind       string
	httpStatus int
	retryable  bool
	// tokenDetail names the credential source for auth rejections (T-06976).
	// Source and length only — never token bytes.
	tokenDetail string
}

// credentialSuffix appends the token-source detail when one was captured, so a
// forwarded auth rejection is as diagnosable as a direct one.
func (e *remoteForwardError) credentialSuffix() string {
	if e.tokenDetail == "" {
		return ""
	}
	return " (" + e.tokenDetail + ")"
}

func (e *remoteForwardError) Error() string {
	if e == nil {
		return "remote workrpc request failed"
	}
	switch e.kind {
	case "authentication":
		return fmt.Sprintf("remote workrpc authentication failed (HTTP %d)%s", e.httpStatus, e.credentialSuffix())
	case "authorization":
		return fmt.Sprintf("remote workrpc authorization failed (HTTP %d)%s", e.httpStatus, e.credentialSuffix())
	case "http":
		return fmt.Sprintf("remote workrpc HTTP %d", e.httpStatus)
	case "protocol":
		return "remote workrpc protocol failure"
	default:
		return "remote workrpc transport failure"
	}
}

func mapRemoteForwardError(err error) *RPCError {
	var remoteErr *remoteForwardError
	if !errors.As(err, &remoteErr) {
		return protocolError(codeInternal, "remote workrpc request failed", map[string]any{
			"kind":      "internal",
			"retryable": false,
		})
	}
	data := map[string]any{
		"kind":      remoteErr.kind,
		"retryable": remoteErr.retryable,
	}
	if remoteErr.httpStatus != 0 {
		data["httpStatus"] = remoteErr.httpStatus
	}
	return protocolError(codeInternal, remoteErr.Error(), data)
}

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
				Error:   mapRemoteForwardError(err),
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
		return Response{}, &remoteForwardError{kind: "transport", retryable: true}
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		kind := "http"
		tokenDetail := ""
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			kind = "authentication"
			tokenDetail = DescribeTokenSource(token)
		case http.StatusForbidden:
			kind = "authorization"
			tokenDetail = DescribeTokenSource(token)
		}
		return Response{}, &remoteForwardError{
			kind:        kind,
			tokenDetail: tokenDetail,
			httpStatus:  resp.StatusCode,
			retryable:   resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError,
		}
	}
	var rpcResp Response
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return Response{}, &remoteForwardError{kind: "protocol", retryable: false}
	}
	if rpcResp.JSONRPC == "" {
		rpcResp.JSONRPC = "2.0"
	}
	if len(rpcResp.ID) == 0 {
		rpcResp.ID = frame.ID
	}
	return rpcResp, nil
}
