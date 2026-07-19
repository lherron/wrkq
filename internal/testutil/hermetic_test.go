package testutil

import (
	"os"
	"testing"
)

// TestSanitizeAmbientLocatorLeavesVarsPresentButEmpty pins the subtle half of
// the contract: the vars must survive as empty-but-present. If a future edit
// switches to os.Unsetenv, config loading falls back to the checkout's
// .env.local and the production rpc:// locator leaks back into tests.
func TestSanitizeAmbientLocatorLeavesVarsPresentButEmpty(t *testing.T) {
	for _, key := range ambientLocatorVars {
		t.Setenv(key, "rpc://mini:7171")
	}

	SanitizeAmbientLocator()

	for _, key := range ambientLocatorVars {
		value, present := os.LookupEnv(key)
		if !present {
			t.Errorf("%s was unset; it must stay present so .env.local cannot supply it", key)
			continue
		}
		if value != "" {
			t.Errorf("%s = %q, want empty", key, value)
		}
	}
}
