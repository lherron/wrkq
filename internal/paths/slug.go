package paths

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	maxSlugLen  = 255
)

// NormalizeSlug normalizes a string to a valid slug
// Rules:
// - Always lower-case
// - Allowed characters: a-z, 0-9, -
// - Must start with [a-z0-9]
// - Regex: ^[a-z0-9][a-z0-9-]*$
// - Max length: 255 bytes
func NormalizeSlug(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("slug cannot be empty")
	}

	// Convert to lowercase
	s = strings.ToLower(s)

	// Replace spaces and underscores with hyphens
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")

	// Remove invalid characters
	var result strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			result.WriteRune(r)
		}
	}
	s = result.String()

	// Remove leading/trailing hyphens
	s = strings.Trim(s, "-")

	// Ensure it starts with alphanumeric
	if len(s) == 0 || !((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= '0' && s[0] <= '9')) {
		return "", fmt.Errorf("slug must start with alphanumeric character")
	}

	// Check length
	if len(s) > maxSlugLen {
		return "", fmt.Errorf("slug exceeds maximum length of %d bytes", maxSlugLen)
	}

	// Validate pattern
	if !slugPattern.MatchString(s) {
		return "", fmt.Errorf("invalid slug format: %s", s)
	}

	return s, nil
}

// ValidateSlug checks if a string is a valid slug without normalization
func ValidateSlug(s string) error {
	if s == "" {
		return fmt.Errorf("slug cannot be empty")
	}

	if len(s) > maxSlugLen {
		return fmt.Errorf("slug exceeds maximum length of %d bytes", maxSlugLen)
	}

	if !slugPattern.MatchString(s) {
		return fmt.Errorf("invalid slug format: must be lowercase, start with alphanumeric, and contain only [a-z0-9-]")
	}

	return nil
}

// SplitPath splits a path into segments
func SplitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

// JoinPath joins path segments
func JoinPath(segments ...string) string {
	return strings.Join(segments, "/")
}
