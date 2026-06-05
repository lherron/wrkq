package wrkfrpc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/lherron/wrkq/internal/wrkfapi"
)

const DefaultMaxFrameBytes = 8 << 20

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type ValidationError struct {
	Message string
	Data    any
}

type ParseError struct {
	Err error
}

func (e *ParseError) Error() string {
	if e == nil || e.Err == nil {
		return "parse error"
	}
	return "parse error: " + e.Err.Error()
}

func (e *ParseError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *ValidationError) Error() string {
	if e == nil || e.Message == "" {
		return "validation failed"
	}
	return e.Message
}

func (e *ValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return wrkfapi.NewValidationError(e.Message, e.Data)
}

func isValidationError(err error, target **ValidationError) bool {
	return errors.As(err, target)
}

type Reader struct {
	r        *bufio.Reader
	maxBytes int
}

func NewReader(r io.Reader, maxBytes int) *Reader {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxFrameBytes
	}
	return &Reader{r: bufio.NewReader(r), maxBytes: maxBytes}
}

func (r *Reader) ReadRequest() (Request, error) {
	line, err := r.readFrame()
	if err != nil {
		return Request{}, err
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return Request{}, &ParseError{Err: err}
	}
	return req, nil
}

func (r *Reader) readFrame() ([]byte, error) {
	var frame []byte
	limit := r.effectiveMaxBytes()
	for {
		part, err := r.r.ReadSlice('\n')
		frame = append(frame, part...)
		if len(frame) > limit {
			if err == bufio.ErrBufferFull {
				_ = r.discardLine()
			}
			return nil, &ValidationError{
				Message: "frame too large",
				Data: map[string]any{
					"maxBytes": r.maxBytes,
				},
			}
		}
		switch {
		case err == nil:
			return bytes.TrimSuffix(frame, []byte{'\n'}), nil
		case err == bufio.ErrBufferFull:
			continue
		case errors.Is(err, io.EOF):
			if len(frame) == 0 {
				return nil, io.EOF
			}
			return frame, nil
		default:
			return nil, err
		}
	}
}

func (r *Reader) effectiveMaxBytes() int {
	if r.maxBytes < 1024 {
		return r.maxBytes + 128
	}
	return r.maxBytes
}

func (r *Reader) discardLine() error {
	for {
		_, err := r.r.ReadSlice('\n')
		if err == nil || errors.Is(err, io.EOF) {
			return nil
		}
		if err != bufio.ErrBufferFull {
			return err
		}
	}
}

type Writer struct {
	w io.Writer
}

func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

func (w *Writer) WriteRequest(req Request) error {
	return w.write(req)
}

func (w *Writer) WriteResponse(resp Response) error {
	return w.write(resp)
}

func (w *Writer) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.w.Write(b)
	return err
}
