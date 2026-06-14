package workrpc_test

// registry_contract_test.go — RED gate for the unified wrkq/wrkf RPC method registry.
//
// These tests define the method-registration contract for protocol version 2026-06-14.
// They run against the CURRENT registry (internal/wrkfrpc) and MUST FAIL for the
// following reasons:
//
//   FORBIDDEN methods still registered in wrkfrpc:
//     - wrkf.task.attach, wrkf.task.inspect, wrkf.task.timeline,
//       wrkf.task.refresh, wrkf.task.syncMeta, wrkf.workflow.attach,
//       wrkf.initialize
//
//   REQUIRED methods NOT yet registered in wrkfrpc:
//     - rpc.initialize, wrkq.task.create, wrkq.workflow.attach,
//       wrkf.workflow.install, wrkf.transition.apply,
//       wrkf.instance.show, wrkf.instance.next
//
// These tests turn GREEN when internal/workrpc is implemented (P1–P3 of T-04424).
// At that point the test helper below should be updated to build the new registry
// instead of delegating to wrkfrpc.RegisterAPI.
//
// Ownership invariant (§2 of docs/wrkq-wrkf-rpc.md):
//   wrkq.* owns task records and all direct task mutation.
//   wrkf.* must never expose direct task mutation or workflow attachment.

import (
	"io"
	"testing"

	"github.com/lherron/wrkq/internal/wrkfrpc"
)

// forbiddenMethods are method names that MUST NOT appear in the unified RPC
// registry. Each one violates the wrkq/wrkf ownership boundary (§2) or uses
// the deprecated wrkf.* lifecycle prefix.
var forbiddenMethods = []string{
	// Task mutation under wrkf.* — ownership boundary violation (§2.1).
	"wrkf.task.attach",
	"wrkf.task.inspect",
	"wrkf.task.timeline",
	"wrkf.task.refresh",
	"wrkf.task.syncMeta",
	// Workflow attachment belongs to wrkq.* (§2.2).
	"wrkf.workflow.attach",
	// Deprecated lifecycle prefix; unified protocol uses rpc.initialize (§5.4).
	"wrkf.initialize",
}

// requiredMethods are method names that MUST be present in the unified RPC
// registry to meet the agent-loop readiness bar (§11 P0, §13, §14).
var requiredMethods = []string{
	// Lifecycle (§5.4).
	"rpc.initialize",
	// wrkq namespace — task create and workflow attach (§6.2).
	"wrkq.task.create",
	"wrkq.workflow.attach",
	// wrkf namespace — workflow install, transition, instance, (§6.3).
	"wrkf.workflow.install",
	"wrkf.transition.apply",
	"wrkf.instance.show",
	"wrkf.instance.next",
}

// buildCurrentRegistry constructs the method set that the current wrkfrpc
// registry exposes. Replace this helper with a call to workrpc.NewRegistry()
// once the unified server is implemented (P1).
func buildCurrentRegistry(t *testing.T) map[string]bool {
	t.Helper()
	srv := wrkfrpc.NewServer(io.Discard)
	// RegisterAPI accepts a nil *wrkfapi.API safely: handlers close over the
	// pointer but are never invoked here — only the method names are inspected.
	wrkfrpc.RegisterAPI(srv, nil, wrkfrpc.RegistryOptions{})
	set := make(map[string]bool)
	for _, m := range srv.RegisteredMethods() {
		set[m] = true
	}
	return set
}

// TestForbiddenMethodsAreAbsent asserts that none of the forbidden methods
// appear in the unified registry. This test FAILS against wrkfrpc because
// wrkf.task.*, wrkf.workflow.attach, and wrkf.initialize are still registered.
func TestForbiddenMethodsAreAbsent(t *testing.T) {
	registered := buildCurrentRegistry(t)
	for _, method := range forbiddenMethods {
		if registered[method] {
			t.Errorf(
				"FORBIDDEN method %q is registered; it must not appear in the "+
					"unified RPC registry (protocol 2026-06-14, §2 ownership boundary)",
				method,
			)
		}
	}
}

// TestRequiredMethodsArePresent asserts that all required methods appear in
// the unified registry. This test FAILS against wrkfrpc because rpc.initialize,
// wrkq.task.create, wrkq.workflow.attach, wrkf.instance.show, and
// wrkf.instance.next are not yet registered.
func TestRequiredMethodsArePresent(t *testing.T) {
	registered := buildCurrentRegistry(t)
	for _, method := range requiredMethods {
		if !registered[method] {
			t.Errorf(
				"REQUIRED method %q is not registered; it must be present in the "+
					"unified RPC registry (protocol 2026-06-14, §11 P0)",
				method,
			)
		}
	}
}

// TestProtocolVersionContract asserts that the current registry reports the
// new unified protocol version. This test FAILS because wrkfrpc exports
// ProtocolVersion = "2026-06-01".
func TestProtocolVersionContract(t *testing.T) {
	const want = "2026-06-14"
	if wrkfrpc.ProtocolVersion != want {
		t.Errorf(
			"wrkfrpc.ProtocolVersion = %q; unified protocol requires %q (§5.1)",
			wrkfrpc.ProtocolVersion, want,
		)
	}
}
