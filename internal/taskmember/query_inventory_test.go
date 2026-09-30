package taskmember

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Enumerate the SQL call sites themselves rather than an implementation-owned
// list. A new store query reading tasks by container must make its membership
// decision in the SQL expression; a helper elsewhere in its function cannot
// satisfy this check.
func TestContainerScopedStoreQueriesUseMembershipPredicate(t *testing.T) {
	files, err := filepath.Glob("../store/*.go")
	if err != nil {
		t.Fatal(err)
	}
	residency := regexp.MustCompile(`(?i)(project_uuid\s*(=|in)|join\s+subtree|join\s+container_projects)`)
	reads := regexp.MustCompile(`(?i)(from|join)\s+tasks\b`)
	count := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fs := token.NewFileSet()
		file, err := parser.ParseFile(fs, path, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (sel.Sel.Name != "Query" && sel.Sel.Name != "QueryRow") {
				return true
			}
			sqlText := ""
			decision := false
			ast.Inspect(call.Args[0], func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					v, _ := strconv.Unquote(lit.Value)
					sqlText += v
				}
				if nested, ok := n.(*ast.CallExpr); ok {
					if s, ok := nested.Fun.(*ast.SelectorExpr); ok {
						if pkg, ok := s.X.(*ast.Ident); ok && pkg.Name == "taskmember" && s.Sel.Name == "Filter" {
							decision = true
						}
					}
				}
				return true
			})
			if !reads.MatchString(sqlText) || !residency.MatchString(sqlText) {
				return true
			}
			// A single record's residency/context lookup is identity-scoped, not a
			// container member query. Parent-edge traversals are scanned below.
			if (strings.Contains(sqlText, "WHERE t.uuid = ?") || strings.Contains(sqlText, "WHERE uuid = ?")) && !strings.Contains(sqlText, "subtree") {
				return true
			}
			count++
			if !decision {
				t.Errorf("%s: container-scoped tasks query bypasses taskMemberFilter", fs.Position(call.Pos()))
			}
			return true
		})
	}
	if count == 0 {
		t.Fatal("inventory found no container task queries")
	}
	t.Logf("enumerated %d container-scoped task queries", count)
}
