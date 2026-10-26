package rpccli

import (
	"os"
	"testing"
)

// TestMain points PRAESIDIUM_HOME at a scratch directory for the whole
// package. `wrkq touch` creates <PRAESIDIUM_HOME>/var/wrkq-artifacts/<id> for
// every task it makes, and these tests run touch against scratch databases
// whose ids (T-00002.preview, ...) collide with real tasks. Without this they
// write into the operator's real artifact root, and where that root lives on
// an external volume (mini: /Volumes/Scratch) a CI job blocks in mkdir until
// the package timeout (T-11948). Tests that need their own root still set
// PRAESIDIUM_HOME themselves; the scratch root also hides the platform
// .env.local, which config loads from the same directory.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "rpccli-praesidium-home-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("PRAESIDIUM_HOME", home)
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}
