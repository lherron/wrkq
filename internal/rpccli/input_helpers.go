package rpccli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

func readTextValue(value, label string, stdin io.Reader) (string, error) {
	if value == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("failed to read from stdin: %w", err)
		}
		if len(data) == 0 {
			return "", fmt.Errorf("stdin is empty")
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

func readMetaValue(value, filename string) (bool, *string, map[string]any, error) {
	if value == "" && filename == "" {
		return false, nil, nil, nil
	}

	raw := value
	if filename != "" {
		data, err := os.ReadFile(filename)
		if err != nil {
			return true, nil, nil, fmt.Errorf("failed to read meta file %s: %w", filename, err)
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
