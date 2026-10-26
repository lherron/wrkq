package rpccli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

// TestHandoffServerReadsNoEnv is the static guard for the caller-owned-scope
// invariant: the SERVER-side handoff implementation (internal/wrkqapi/handoffs.go)
// must NOT READ agent-runtime env — scope/actor is resolved caller-side and crosses
// as EXPLICIT params. It parses the AST (comments EXCLUDED) and fails on any actual
// env-read call (os.Getenv / scope.Resolve / scope.ReadEnv) or an ASP_* env-key
// string literal. Doc comments mentioning these tokens are NOT a violation.
func TestHandoffServerReadsNoEnv(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "../wrkqapi/handoffs.go", nil, 0) // no ParseComments
	if err != nil {
		t.Fatalf("parse server handoff impl: %v", err)
	}

	forbiddenCalls := map[string]bool{
		"os.Getenv": true, "scope.Resolve": true, "scope.ReadEnv": true,
	}
	ast.Inspect(file, func(n ast.Node) bool {
		// Forbidden call expressions (pkg.Fn).
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok {
					name := pkg.Name + "." + sel.Sel.Name
					if forbiddenCalls[name] {
						t.Errorf("server handoff impl calls %q — scope/actor must be EXPLICIT caller params, not a server env read", name)
					}
				}
			}
		}
		// Forbidden ASP_* env-key string literals (an env read by key).
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if v, err := strconv.Unquote(lit.Value); err == nil && strings.HasPrefix(v, "ASP_") {
				t.Errorf("server handoff impl references env key %q — handoff scope is a caller param, not a server env read", v)
			}
		}
		return true
	})
}
