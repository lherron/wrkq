package rpccli

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCoreRuleImportGuard enforces the migration's Core Rule: rpccli command
// implementations must obtain durable wrkq behavior ONLY through the JSON-RPC
// protocol boundary, never by importing store, wrkqapi, direct db/SQL, or
// internal/cli command handlers. Construction of the server lives behind the
// neutral internal/workrpc/bootstrap helper, which is allowed.
func TestCoreRuleImportGuard(t *testing.T) {
	forbidden := []string{
		"github.com/lherron/wrkq/internal/store",
		"github.com/lherron/wrkq/internal/wrkqapi",
		"github.com/lherron/wrkq/internal/wrkfapi",
		"github.com/lherron/wrkq/internal/db",
		"github.com/lherron/wrkq/internal/cli", // includes internal/cli/appctx
		"github.com/mattn/go-sqlite3",
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				if path == bad || strings.HasPrefix(path, bad+"/") {
					t.Errorf("%s imports forbidden package %q (Core Rule: route durable behavior through the RPC boundary / bootstrap helper)", name, path)
				}
			}
		}
	}
}
