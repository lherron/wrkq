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

func TestPrimaryCutoverInventoryGate(t *testing.T) {
	if len(topLevelCommands) != 0 {
		t.Fatalf("top-level mirror stubs remain: %#v", topLevelCommands)
	}
	for _, cmd := range NewRootCmd().Commands() {
		if strings.Contains(cmd.Short, "mirror stub") {
			t.Fatalf("top-level command %q is still a mirror stub", cmd.Name())
		}
	}

	root := repoRootFromTest(t)
	migrationDoc := readTextFile(t, filepath.Join(root, "docs", "rpc-cli-migration.md"))
	for _, line := range strings.Split(migrationDoc, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		fields := strings.Split(line, "|")
		if len(fields) < 4 {
			continue
		}
		status := strings.TrimSpace(fields[2])
		switch {
		case strings.Contains(status, "partial"):
			t.Errorf("partial coverage row remains: %s", line)
		case strings.Contains(status, "not-started"), strings.Contains(status, "rpc-gap"), strings.Contains(status, "seam-smoke"):
			t.Errorf("unclassified/non-parity coverage row remains: %s", line)
		}
	}

	requiredRows := []string{
		"| `set` | `rpc-backed` |",
		"| `rm` | `rpc-backed` |",
		"| `cp` | `rpc-backed` |",
		"| `search` | `rpc-backed` |",
		"| `monitor` | `rpc-backed` |",
		"| `watch` | `rpc-backed` |",
	}
	for _, row := range requiredRows {
		if !strings.Contains(migrationDoc, row) {
			t.Errorf("missing finalized coverage row %q", row)
		}
	}
	requiredEvidence := []string{
		"bulk-flags",
		"`--jobs` multi-target archive",
		"`--recursive` task source accepted-and-ignored",
		"TestSearchTTYHumanParity",
		"TestMonitorWatchFollowNDJSONParity",
		"TestWatchFollowNDJSONParity",
		"TestWatchTTYHumanSemanticParity",
		"TestBundleParity",
	}
	for _, evidence := range requiredEvidence {
		if !strings.Contains(migrationDoc, evidence) {
			t.Errorf("migration doc missing cutover evidence marker %q", evidence)
		}
	}

	coverageByRoot := map[string][]string{
		"attach":    {"| `attach ls` |", "| `attach put` |", "| `attach get` |", "| `attach rm` |"},
		"check":     {"| `check blocked` |"},
		"comment":   {"| `comment add` |", "| `comment ls` |", "| `comment cat` |", "| `comment rm` |"},
		"container": {"| `container cat` |", "| `container set` |"},
		"relation":  {"| `relation add`/`rm` |", "| `relation ls` |"},
	}
	for _, cmd := range NewRootCmd().Commands() {
		name := cmd.Name()
		if name == "help" {
			continue
		}
		patterns := coverageByRoot[name]
		if len(patterns) == 0 {
			patterns = []string{"| `" + name + "` |"}
		}
		for _, pattern := range patterns {
			if !strings.Contains(migrationDoc, pattern) {
				t.Errorf("migration doc missing coverage pattern for root command %q: %s", name, pattern)
			}
		}
	}

	for _, check := range []struct {
		path      string
		forbidden []string
	}{
		{
			path: filepath.Join(root, "docs", "wrkq-wrkf-rpc-client-forward-spec.md"),
			forbidden: []string{
				"CLI mirror HARD-GATES this flag",
				"rpc-backed (partial)",
			},
		},
		{
			path: filepath.Join(root, "docs", "wrkq-wrkf-rpc.md"),
			forbidden: []string{
				"CLI mirror HARD-GATES this flag",
				"rpc-backed (partial)",
			},
		},
		{
			path: filepath.Join(root, "internal", "wrkqapi", "bundleview.go"),
			forbidden: []string{
				"hard-gates the flag",
			},
		},
		{
			path: filepath.Join(root, "internal", "rpccli", "attach.go"),
			forbidden: []string{
				"hard-gated as an open design decision",
			},
		},
	} {
		body := readTextFile(t, check.path)
		for _, forbidden := range check.forbidden {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s still contains stale cutover text %q", check.path, forbidden)
			}
		}
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
