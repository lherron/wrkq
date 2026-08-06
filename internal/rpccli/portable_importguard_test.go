package rpccli

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Portable-build import guard (T-07090).
//
// This test carries no build tag, so it runs in the portable lane where every
// wrkq_local file is excluded. What remains is exactly the set of files the
// CGO-free client compiles, and none of them may reach durable local state.
// Without this guard a single innocuous import would silently reintroduce the
// cgo dependency and break `CGO_ENABLED=0 go build ./cmd/wrkq` for every target.
var forbiddenPortableImports = []string{
	"database/sql",
	"github.com/asg017/sqlite-vec-go-bindings/cgo",
	"github.com/lherron/wrkq/internal/db",
	"github.com/lherron/wrkq/internal/search",
	"github.com/lherron/wrkq/internal/store",
	"github.com/lherron/wrkq/internal/workflow",
	"github.com/lherron/wrkq/internal/workrpc/bootstrap",
	"github.com/lherron/wrkq/internal/wrkqd",
	"github.com/mattn/go-sqlite3",
}

func TestPortableClientLinksNoDurableLocalState(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := map[string]bool{}
	for _, imp := range forbiddenPortableImports {
		forbidden[imp] = true
	}

	fset := token.NewFileSet()
	violations := map[string][]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// The portable lane excludes wrkq_local files; skip them the same way
		// the compiler does rather than reimplementing constraint evaluation.
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(string(body), "//go:build wrkq_local") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Clean(name), body, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if forbidden[path] {
				violations[name] = append(violations[name], path)
			}
		}
	}
	if len(violations) == 0 {
		return
	}
	names := make([]string, 0, len(violations))
	for name := range violations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Errorf("%s imports %s in the portable build; move that code behind //go:build wrkq_local",
			name, strings.Join(violations[name], ", "))
	}
}
