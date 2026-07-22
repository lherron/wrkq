package admincli

import (
	"os"
	"testing"

	"github.com/lherron/wrkq/internal/testutil"
)

// TestMain keeps this package's tests hermetic: the checkout's .env.local
// carries the production rpc:// locator, and these tests drive real CLI
// commands, so an inherited locator would both fail them and point them at the
// canonical database.
func TestMain(m *testing.M) {
	testutil.SanitizeAmbientLocator()
	os.Exit(m.Run())
}
