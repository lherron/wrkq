package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// exitError returns an error that will cause the CLI to exit with the given code
func exitError(code int, err error) error {
	// For now, just return the error. We'll enhance this with proper exit codes later
	return err
}

// readDescriptionValue reads description from string, file (@file.md), or stdin (-)
func readDescriptionValue(value string) (string, error) {
	// Handle stdin
	if value == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("failed to read from stdin: %w", err)
		}
		if len(data) == 0 {
			return "", fmt.Errorf("stdin is empty")
		}
		return string(data), nil
	}

	// Handle file (starts with @)
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

	// Handle string literal
	return value, nil
}

func readMetaValue(value string, filename string) (bool, *string, error) {
	if value == "" && filename == "" {
		return false, nil, nil
	}

	var raw string
	if filename != "" {
		data, err := os.ReadFile(filename)
		if err != nil {
			return true, nil, fmt.Errorf("failed to read meta file %s: %w", filename, err)
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
