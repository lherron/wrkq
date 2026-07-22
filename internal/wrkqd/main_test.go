package wrkqd

import (
	"os"
	"testing"

	"github.com/lherron/wrkq/internal/testutil"
)

func TestMain(m *testing.M) {
	testutil.SanitizeAmbientLocator()
	os.Exit(m.Run())
}
