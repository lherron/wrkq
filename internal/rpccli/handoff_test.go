package rpccli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/scope"
)

// TestHandoffCreateParams_ExplicitScopeAndOmitempty proves the mirror builds the
// wrkq.handoff.create request with ALWAYS-explicit caller-resolved scope/actor
// fields, omitting the optional ones when unset. This is the param-construction
// guard for the caller-owned-scope invariant: the server reads no env, so the
// EFFECTIVE scope/actor MUST cross the boundary as explicit params.
func TestHandoffCreateParams_ExplicitScopeAndOmitempty(t *testing.T) {
	meta := `{"k":"v"}`
	idem := "key-1"
	full := handoffCreateParams("agent:cody:project:wrkq", "cody", "wrkq", "title", "body", &meta, &idem, true, "agent:cody")
	for _, k := range []string{"scopeRef", "agentId", "projectId", "title", "body"} {
		if _, ok := full[k]; !ok {
			t.Errorf("create params missing always-explicit field %q", k)
		}
	}
	if full["scopeRef"] != "agent:cody:project:wrkq" || full["agentId"] != "cody" || full["projectId"] != "wrkq" {
		t.Errorf("create params scope mismatch: %#v", full)
	}
	if full["meta"] != meta || full["idempotencyKey"] != idem || full["dryRun"] != true || full["actorAgentId"] != "agent:cody" {
		t.Errorf("create params optional fields mismatch: %#v", full)
	}

	// Optional fields omitted when unset.
	bare := handoffCreateParams("agent:cody:project:wrkq", "cody", "wrkq", "title", "body", nil, nil, false, "")
	for _, k := range []string{"meta", "idempotencyKey", "dryRun", "actorAgentId"} {
		if _, ok := bare[k]; ok {
			t.Errorf("create params should omit unset optional field %q (params=%#v)", k, bare)
		}
	}
}

// TestHandoffAckParams_ExplicitActorAndOmitempty mirrors the above for acknowledge.
func TestHandoffAckParams_ExplicitActorAndOmitempty(t *testing.T) {
	note := "loaded"
	full := handoffAckParams("H-00001", "cody", "agent:cody", "agent:cody:project:wrkq", &note, true, 3)
	for _, k := range []string{"handoff", "actorAgentId", "principalRef", "scopeRef"} {
		if _, ok := full[k]; !ok {
			t.Errorf("ack params missing always-explicit field %q", k)
		}
	}
	if full["note"] != note || full["dryRun"] != true || full["ifMatch"] != int64(3) {
		t.Errorf("ack params optional fields mismatch: %#v", full)
	}
	bare := handoffAckParams("H-00001", "cody", "agent:cody", "agent:cody:project:wrkq", nil, false, 0)
	for _, k := range []string{"note", "dryRun", "ifMatch"} {
		if _, ok := bare[k]; ok {
			t.Errorf("ack params should omit unset optional field %q (params=%#v)", k, bare)
		}
	}
}

// TestHandoffScopeEnvPrecedence proves the mirror resolves the EFFECTIVE scope
// from agent-runtime env exactly as legacy does (ASP_SCOPE_REF wins), via the
// shared scope.Resolve the create path calls — and that the resolved canonical
// ref is what would cross the boundary as the explicit scopeRef.
func TestHandoffScopeEnvPrecedence(t *testing.T) {
	t.Setenv("ASP_SCOPE_REF", "agent:cody:project:wrkq")
	t.Setenv("ASP_HANDLE", "")
	t.Setenv("ASP_AGENT_ID", "")
	t.Setenv("ASP_PROJECT", "")

	resolved, _, err := scope.Resolve("")
	if err != nil {
		t.Fatalf("scope.Resolve from env: %v", err)
	}
	if resolved.AgentID != "cody" || resolved.ProjectID != "wrkq" {
		t.Fatalf("env scope precedence: want cody/wrkq, got %s/%s", resolved.AgentID, resolved.ProjectID)
	}
	params := handoffCreateParams(resolved.CanonicalRef, resolved.AgentID, resolved.ProjectID, "t", "b", nil, nil, false, "")
	if params["scopeRef"] != "agent:cody:project:wrkq" {
		t.Errorf("effective scopeRef must be the env-resolved canonical ref, got %v", params["scopeRef"])
	}

	// An explicit --scope override wins over env (legacy precedence).
	over, _, oerr := scope.Resolve("dave@otherproj")
	if oerr != nil {
		t.Fatalf("scope.Resolve override: %v", oerr)
	}
	if over.AgentID != "dave" || over.ProjectID != "otherproj" {
		t.Errorf("--scope override must win over env, got %s/%s", over.AgentID, over.ProjectID)
	}
}

// TestHandoffSelfScopeViolation_CallerSide proves the create gate enforces
// self-scope CLIENT-side: a runtime agent (ASP_AGENT_ID=cody) may not write a
// handoff scoped to a DIFFERENT agent (--scope dave@wrkq). EnforceSelfScope is the
// exact gate the mirror runs before any RPC call, so a violation never reaches the
// server.
func TestHandoffSelfScopeViolation_CallerSide(t *testing.T) {
	resolved, _, err := scope.Resolve("dave@wrkq")
	if err != nil {
		t.Fatalf("resolve override: %v", err)
	}
	if verr := scope.EnforceSelfScope(resolved, scope.RuntimeIdentity{AgentID: "cody"}); verr == nil {
		t.Error("expected self-scope violation when runtime agent (cody) writes a dave-scoped handoff")
	}
	// Same agent is permitted.
	self, _, _ := scope.Resolve("cody@wrkq")
	if verr := scope.EnforceSelfScope(self, scope.RuntimeIdentity{AgentID: "cody"}); verr != nil {
		t.Errorf("self-scope create must be permitted for the same agent, got %v", verr)
	}
}

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
