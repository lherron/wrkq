package rpccli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

const labelValueForms = "comma-separated labels or a JSON array of strings"

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

// parseLabelValue owns CLI acquisition for every task/campaign --labels write
// flag. JSON arrays retain their element values and ordering exactly; shorthand
// trims comma-separated segments and drops empty ones.
func parseLabelValue(raw string) ([]string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []string{}, nil
	}

	if strings.HasPrefix(trimmed, "[") {
		var elements []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &elements); err != nil {
			return nil, fmt.Errorf("invalid --labels value: expected %s: %w", labelValueForms, err)
		}
		if elements == nil {
			return nil, fmt.Errorf("invalid --labels value: expected %s: null is not allowed", labelValueForms)
		}
		labels := make([]string, 0, len(elements))
		for i, element := range elements {
			value := strings.TrimSpace(string(element))
			if !strings.HasPrefix(value, `"`) {
				return nil, fmt.Errorf("invalid --labels value: expected %s: element %d is not a string", labelValueForms, i)
			}
			var label string
			if err := json.Unmarshal(element, &label); err != nil {
				return nil, fmt.Errorf("invalid --labels value: expected %s: element %d: %w", labelValueForms, i, err)
			}
			labels = append(labels, label)
		}
		return labels, nil
	}

	// Valid JSON values outside the accepted array form are almost certainly an
	// attempted JSON label value. Refuse them instead of silently storing their
	// JSON punctuation as shorthand label bytes.
	if json.Valid([]byte(trimmed)) {
		return nil, fmt.Errorf("invalid --labels value: expected %s: JSON value must be an array of strings", labelValueForms)
	}

	parts := strings.Split(trimmed, ",")
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		if label := strings.TrimSpace(part); label != "" {
			labels = append(labels, label)
		}
	}
	return labels, nil
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
