package appctx

import (
	"os"
	"testing"

	"github.com/lherron/wrkq/internal/testutil"
)

// TestMain keeps this package's tests hermetic. See internal/cli/main_test.go.
func TestMain(m *testing.M) {
	testutil.SanitizeAmbientLocator()
	os.Exit(m.Run())
}
