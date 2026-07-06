package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type cliExitError struct {
	code     int
	err      error
	reported bool
}

func (e cliExitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e cliExitError) Unwrap() error {
	return e.err
}

// exitError returns an error that causes the real CLI binary to exit with the
// given code. Tests can inspect the code with ExitCodeForError.
func exitError(code int, err error) error {
	return cliExitError{code: code, err: err}
}

func exitErrorReported(code int, err error) error {
	return cliExitError{code: code, err: err, reported: true}
}

// ExitCodeForError returns the process exit code represented by err.
func ExitCodeForError(err error) int {
	if err == nil {
		return 0
	}
	if e, ok := err.(cliExitError); ok {
		return e.code
	}
	return 1
}

// ErrorAlreadyReported reports whether a command already wrote its own
// structured error to stderr.
func ErrorAlreadyReported(err error) bool {
	if e, ok := err.(cliExitError); ok {
		return e.reported
	}
	return false
}

type stdinClaims struct {
	claimedBy string
}

func (c *stdinClaims) claim(label string) error {
	if c == nil {
		return nil
	}
	if c.claimedBy != "" {
		return fmt.Errorf("stdin already claimed by %s", c.claimedBy)
	}
	c.claimedBy = label
	return nil
}

// readTextValue reads free-form text from a literal value, @file, or stdin (-).
func readTextValue(value, label string, stdin io.Reader, claims *stdinClaims) (string, error) {
	if value == "-" {
		data, err := readStdinValue(label, stdin, claims)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	if strings.HasPrefix(value, "@") {
		filename := strings.TrimPrefix(value, "@")
		data, err := os.ReadFile(filename)
		if err != nil {
			return "", fmt.Errorf("failed to read file %s: %w", filename, err)
		}
		if len(data) == 0 {
			return "", fmt.Errorf("file %s is empty", filename)
		}
		return string(data), nil
	}

	return value, nil
}

func readFileValue(filename, label string, stdin io.Reader, claims *stdinClaims) ([]byte, error) {
	if filename == "-" {
		return readStdinValue(label, stdin, claims)
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s %s: %w", label, filename, err)
	}
	return data, nil
}

func readStdinValue(label string, stdin io.Reader, claims *stdinClaims) ([]byte, error) {
	if err := claims.claim(label); err != nil {
		return nil, err
	}
	if isReaderTTY(stdin) {
		return nil, fmt.Errorf("stdin is a terminal; pipe input or use a heredoc")
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return nil, fmt.Errorf("failed to read from stdin: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("stdin is empty")
	}
	return data, nil
}

func isReaderTTY(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func readMetaValue(value string, filename string, stdin io.Reader, claims *stdinClaims) (bool, *string, error) {
	if value == "" && filename == "" {
		return false, nil, nil
	}

	var raw string
	if filename != "" {
		data, err := readFileValue(filename, "--meta-file", stdin, claims)
		if err != nil {
			return true, nil, err
		}
		if len(data) == 0 {
			return true, nil, fmt.Errorf("meta file %s is empty", filename)
		}
		raw = string(data)
	} else {
		raw = value
	}

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return true, nil, fmt.Errorf("meta is empty")
	}
	if trimmed == "null" {
		return true, nil, nil
	}

	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &meta); err != nil {
		return true, nil, fmt.Errorf("invalid meta JSON: %w", err)
	}

	return true, &trimmed, nil
}
