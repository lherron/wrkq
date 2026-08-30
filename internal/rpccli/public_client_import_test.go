package rpccli

import (
	"os"
	"strings"
	"testing"
)

// TestCLITransportConsumesPublicClient pins T-07732's single-copy boundary:
// wrkq and wrkc share rpccli, whose transport must come from pkg/client rather
// than regrowing an internal transport implementation.
func TestCLITransportConsumesPublicClient(t *testing.T) {
	body, err := os.ReadFile("transport.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)
	if !strings.Contains(source, `"github.com/lherron/wrkq/pkg/client"`) {
		t.Fatal("rpccli transport does not consume the public pkg/client package")
	}
	if strings.Contains(source, `"github.com/lherron/wrkq/internal/workrpc/client"`) {
		t.Fatal("rpccli transport still consumes the retired internal client package")
	}
}
