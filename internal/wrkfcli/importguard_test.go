package wrkfcli

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var forbiddenAdapterImports = []string{
	"database/sql",
	"github.com/lherron/wrkq/internal/admincli",
	"github.com/lherron/wrkq/internal/db",
	"github.com/lherron/wrkq/internal/store",
	"github.com/lherron/wrkq/internal/workrpc/bootstrap",
	"github.com/lherron/wrkq/internal/workflow",
	"github.com/lherron/wrkq/internal/wrkqd",
	"github.com/mattn/go-sqlite3",
}

// TestLegacyDirectDBBootstrapCannotRegrow pins S8: command adapters may not
// restore the retired withApp/db.Open/workflow.NewService fork or the old
// remote-locator rejection. Local commands must use InProcess transport.
func TestLegacyDirectDBBootstrapCannotRegrow(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"func withApp(", "db.Open(", "workflow.NewService(", "requires a local database path"}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, needle := range forbidden {
			if strings.Contains(string(body), needle) {
				t.Errorf("%s contains retired direct-DB bootstrap marker %q", name, needle)
			}
		}
	}
}

// TestCommandAdapterImportGuard freezes the remote migration boundary: durable
// command behavior crosses only internal/workrpc/client. watch.go retains the
// ratified workflow import because its bounded poll loop evaluates local watch
// predicates; evidence exec's local os/exec path needs no durable package.
func TestCommandAdapterImportGuard(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var violations []string
	foundClient := false
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		imports, err := importsInFile(filepath.Join(".", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, importPath := range imports {
			if importPath == "github.com/lherron/wrkq/internal/workrpc/client" {
				foundClient = true
			}
			if name == "watch.go" && importPath == "github.com/lherron/wrkq/internal/workflow" {
				continue
			}
			if isForbiddenAdapterImport(importPath) {
				violations = append(violations, fmt.Sprintf("%s imports %q", name, importPath))
			}
		}
	}
	if !foundClient {
		t.Fatal("wrkf command adapters no longer import internal/workrpc/client; durable behavior must retain the shared transport boundary")
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("wrkf command adapter import guard failed:\n%s\nroute durable behavior through internal/workrpc/client; only watch.go may import workflow for client-owned predicate evaluation", strings.Join(violations, "\n"))
	}
}

// TestCommandAdapterImportGuardRejectsDirectStoreImport is the permanent red
// control for the guard: a deliberately injected store import must be caught.
func TestCommandAdapterImportGuardRejectsDirectStoreImport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.go")
	if err := os.WriteFile(path, []byte(`package bad
import "github.com/lherron/wrkq/internal/store"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	imports, err := importsInFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) != 1 || !isForbiddenAdapterImport(imports[0]) {
		t.Fatalf("deliberate direct-store import escaped guard: %#v", imports)
	}
}

func importsInFile(path string) ([]string, error) {
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	imports := make([]string, 0, len(parsed.Imports))
	for _, spec := range parsed.Imports {
		imports = append(imports, strings.Trim(spec.Path.Value, `"`))
	}
	return imports, nil
}

func isForbiddenAdapterImport(importPath string) bool {
	for _, forbidden := range forbiddenAdapterImports {
		if importPath == forbidden || strings.HasPrefix(importPath, forbidden+"/") {
			return true
		}
	}
	return false
}
