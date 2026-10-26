//go:build wrkq_local

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
// admincli/wrkqd adapters. Construction of the server lives behind the
// neutral internal/workrpc/bootstrap helper, which is allowed.
func TestCoreRuleImportGuard(t *testing.T) {
	forbidden := []string{
		"github.com/lherron/wrkq/internal/store",
		"github.com/lherron/wrkq/internal/wrkqapi",
		"github.com/lherron/wrkq/internal/wrkfapi",
		"github.com/lherron/wrkq/internal/db",
		"github.com/lherron/wrkq/internal/admincli",
		"github.com/lherron/wrkq/internal/wrkqd",
		"github.com/mattn/go-sqlite3",
		// Search/index family (T-05114): the server owns the derived sidecar +
		// dense embedder. The mirror MUST NOT open the sidecar (internal/search,
		// internal/search/indexdb, internal/search/indexer) or call EnsureLlamaReady
		// (internal/search/embed). Forbidding these import paths is the structural
		// proof of "rpccli must NOT open the sidecar or call EnsureLlamaReady".
		"github.com/lherron/wrkq/internal/search",
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

func TestProductionWrkqEntrypointUsesRPCCLI(t *testing.T) {
	root := repoRootFromTest(t)
	src := readTextFile(t, filepath.Join(root, "cmd", "wrkq", "main.go"))
	for _, forbidden := range []string{"internal/admincli", "internal/wrkqd"} {
		if strings.Contains(src, forbidden) {
			t.Fatalf("cmd/wrkq imports %s; production wrkq must use only the RPC-backed entrypoint", forbidden)
		}
	}
	if !strings.Contains(src, `"github.com/lherron/wrkq/internal/rpccli"`) {
		t.Fatal("cmd/wrkq does not import internal/rpccli")
	}
}

func TestLegacyCLIPackageRemoved(t *testing.T) {
	root := repoRootFromTest(t)
	_, err := os.Stat(filepath.Join(root, "internal", "cli"))
	if !os.IsNotExist(err) {
		t.Fatalf("legacy internal/cli command package regrew: err=%v", err)
	}
}

func readTextFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}
