package parse

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// TaskUpdate represents parsed task data with optional fields
type TaskUpdate struct {
	Title       *string `json:"title,omitempty" yaml:"title,omitempty"`
	State       *string `json:"state,omitempty" yaml:"state,omitempty"`
	Priority    *int    `json:"priority,omitempty" yaml:"priority,omitempty"`
	DueAt       *string `json:"due_at,omitempty" yaml:"due_at,omitempty"`
	Description *string `json:"description,omitempty" yaml:"description,omitempty"`
}

// Format represents supported input formats
type Format string

const (
	FormatJSON     Format = "json"
	FormatYAML     Format = "yaml"
	FormatMarkdown Format = "md"
)

// DetectFormat attempts to determine the format of the input data
// Returns an error if the format cannot be reliably determined
func DetectFormat(data []byte) (Format, error) {
	text := string(data)
	trimmed := strings.TrimSpace(text)

	// Check for markdown front matter (most specific pattern)
	if strings.HasPrefix(text, "---\n") {
		return FormatMarkdown, nil
	}

	// Check for JSON - validate it's actually valid JSON
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var js json.RawMessage
		if err := json.Unmarshal(data, &js); err == nil {
			return FormatJSON, nil
		}
		// If it starts with { but isn't valid JSON, that's an error
		return "", fmt.Errorf("input appears to be JSON but is invalid")
	}

	// Try parsing as YAML - if it's valid YAML with meaningful content, use it
	var yamlTest interface{}
	if err := yaml.Unmarshal(data, &yamlTest); err == nil {
		// YAML parser is very permissive - plain text is valid YAML
		// Only treat as YAML if it has structure (map or array)
		switch yamlTest.(type) {
		case map[string]interface{}, []interface{}:
			return FormatYAML, nil
		}
	}

	// If nothing else matches, treat as plain markdown (description only)
	return FormatMarkdown, nil
}

// ParseJSON parses JSON-formatted task data
func ParseJSON(data []byte) (*TaskUpdate, error) {
	var update TaskUpdate
	if err := json.Unmarshal(data, &update); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	return &update, nil
}

// ParseYAML parses YAML-formatted task data
func ParseYAML(data []byte) (*TaskUpdate, error) {
	var update TaskUpdate
	if err := yaml.Unmarshal(data, &update); err != nil {
		return nil, fmt.Errorf("invalid YAML: %w", err)
	}
	return &update, nil
}

// ParseMarkdown parses markdown with optional YAML front matter
// If no front matter is present, treats entire content as description
func ParseMarkdown(data []byte) (*TaskUpdate, error) {
	text := string(data)
	var update TaskUpdate

	// No front matter - treat entire content as description
	if !strings.HasPrefix(text, "---\n") {
		update.Description = &text
		return &update, nil
	}

	// Split front matter and description
	parts := strings.SplitN(text[4:], "\n---\n", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid markdown front matter format")
	}

	// Parse front matter as YAML
	if err := yaml.Unmarshal([]byte(parts[0]), &update); err != nil {
		return nil, fmt.Errorf("failed to parse front matter: %w", err)
	}

	// Set description from content after front matter
	description := strings.TrimSpace(parts[1])
	if description != "" {
		update.Description = &description
	}

	return &update, nil
}

// Parse parses task data in the specified format
// If format is empty, auto-detects the format
func Parse(data []byte, format string) (*TaskUpdate, error) {
	var detectedFormat Format
	var err error

	if format == "" {
		detectedFormat, err = DetectFormat(data)
		if err != nil {
			return nil, err
		}
	} else {
		detectedFormat = Format(format)
	}

	switch detectedFormat {
	case FormatJSON:
		return ParseJSON(data)
	case FormatYAML, "yml":
		return ParseYAML(data)
	case FormatMarkdown, "markdown":
		return ParseMarkdown(data)
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}
