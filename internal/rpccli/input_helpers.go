package rpccli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

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

// readNullableTextValue is the content-field input convention for nullable text:
// unlike required bodies, an empty file/stdin is meaningful and clears the field.
func readNullableTextValue(value, label string, stdin io.Reader, claims *stdinClaims) (string, error) {
	if value == "-" {
		if err := claims.claim(label); err != nil {
			return "", err
		}
		if isReaderTTY(stdin) {
			return "", fmt.Errorf("stdin is a terminal; pipe input or use a heredoc")
		}
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("failed to read from stdin: %w", err)
		}
		return string(data), nil
	}
	if strings.HasPrefix(value, "@") {
		filename := strings.TrimPrefix(value, "@")
		data, err := os.ReadFile(filename)
		if err != nil {
			return "", fmt.Errorf("failed to read file %s: %w", filename, err)
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

func readMetaValue(value, filename string, stdin io.Reader, claims *stdinClaims) (bool, *string, map[string]any, error) {
	if value == "" && filename == "" {
		return false, nil, nil, nil
	}

	raw := value
	if filename != "" {
		data, err := readFileValue(filename, "--meta-file", stdin, claims)
		if err != nil {
			return true, nil, nil, err
		}
		if len(data) == 0 {
			return true, nil, nil, fmt.Errorf("meta file %s is empty", filename)
		}
		raw = string(data)
	}

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return true, nil, nil, fmt.Errorf("meta is empty")
	}
	if trimmed == "null" {
		return true, &trimmed, nil, nil
	}

	var meta map[string]any
	if err := json.Unmarshal([]byte(trimmed), &meta); err != nil {
		return true, nil, nil, fmt.Errorf("invalid meta JSON: %w", err)
	}
	return true, &trimmed, meta, nil
}
