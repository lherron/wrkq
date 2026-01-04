package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestUsageCommandExists(t *testing.T) {
	// Verify the command is registered
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "usage" {
			found = true
			break
		}
	}
	if !found {
		t.Error("usage command should be registered with rootCmd")
	}
}

func TestUsageContentEmbedded(t *testing.T) {
	// Verify content is embedded and non-empty
	if len(wrkqUsageContent) == 0 {
		t.Error("wrkqUsageContent should not be empty")
	}

	// Verify it contains expected content
	if !strings.Contains(wrkqUsageContent, "wrkq") {
		t.Error("wrkqUsageContent should contain 'wrkq'")
	}
}

func TestUsageCommandOutput(t *testing.T) {
	// Test plain text output by calling runUsage directly
	var buf bytes.Buffer
	usageCmd.SetOut(&buf)

	// Reset the flag
	usageJSON = false

	if err := runUsage(usageCmd, []string{}); err != nil {
		t.Fatalf("usage command failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "wrkq") {
		t.Error("usage output should contain 'wrkq'")
	}
	if !strings.Contains(output, "Task Lifecycle") {
		t.Error("usage output should contain 'Task Lifecycle'")
	}
}

func TestUsageCommandJSONOutput(t *testing.T) {
	var buf bytes.Buffer
	usageCmd.SetOut(&buf)

	// Set the flag
	usageJSON = true
	defer func() { usageJSON = false }()

	if err := runUsage(usageCmd, []string{}); err != nil {
		t.Fatalf("usage --json command failed: %v", err)
	}

	output := buf.String()

	// Verify it's valid JSON
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("usage --json output is not valid JSON: %v", err)
	}

	// Verify it has content key
	content, ok := result["content"].(string)
	if !ok {
		t.Error("usage --json output should have 'content' key with string value")
	}

	if !strings.Contains(content, "wrkq") {
		t.Error("usage --json content should contain 'wrkq'")
	}
}
