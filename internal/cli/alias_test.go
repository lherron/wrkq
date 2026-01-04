package cli

import (
	"testing"
)

// TestLsHasListAlias verifies that the ls command has the "list" alias configured
func TestLsHasListAlias(t *testing.T) {
	found := false
	for _, alias := range lsCmd.Aliases {
		if alias == "list" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ls command should have 'list' alias, but aliases are: %v", lsCmd.Aliases)
	}
}

// TestCatHasShowAlias verifies that the cat command has the "show" alias configured
func TestCatHasShowAlias(t *testing.T) {
	found := false
	for _, alias := range catCmd.Aliases {
		if alias == "show" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("cat command should have 'show' alias, but aliases are: %v", catCmd.Aliases)
	}
}

// TestSetHasEditAlias verifies that the set command has the "edit" alias configured
func TestSetHasEditAlias(t *testing.T) {
	found := false
	for _, alias := range setCmd.Aliases {
		if alias == "edit" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("set command should have 'edit' alias, but aliases are: %v", setCmd.Aliases)
	}
}
