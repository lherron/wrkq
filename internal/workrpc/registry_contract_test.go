package workrpc_test

// registry_contract_test.go — RED gate for the unified wrkq/wrkf RPC method registry.
//
// These tests define the method-registration contract for protocol version 2026-06-30.
// They run against the unified registry implemented in internal/workrpc.
//
// Ownership invariant (§2 of docs/wrkq-wrkf-rpc.md):
//   wrkq.* owns task records and all direct task mutation.
//   wrkf.* must never expose direct task mutation or workflow attachment.

import (
	"testing"

	"github.com/lherron/wrkq/internal/workrpc"
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
		"wrkf.instance.cancel",
	"wrkf.event.query",
	"wrkf.role.list",
	"wrkf.role.bind",
	"wrkf.role.unbind",
	"wrkf.role.set",
}

// buildCurrentRegistry constructs the method set exposed by the unified registry.
func buildCurrentRegistry(t *testing.T) map[string]bool {
	t.Helper()
	srv := workrpc.NewRegistry(nil, workrpc.RegistryOptions{})
	set := make(map[string]bool)
	for _, m := range srv.RegisteredMethods() {
		set[m] = true
	}
	return set
}

// TestForbiddenMethodsAreAbsent asserts that none of the forbidden methods
// appear in the unified registry.
func TestForbiddenMethodsAreAbsent(t *testing.T) {
	registered := buildCurrentRegistry(t)
	for _, method := range forbiddenMethods {
		if registered[method] {
			t.Errorf(
				"FORBIDDEN method %q is registered; it must not appear in the "+
					"unified RPC registry (protocol 2026-06-30, §2 ownership boundary)",
				method,
			)
		}
	}
}

// TestRequiredMethodsArePresent asserts that all required methods appear in
// the unified registry.
func TestRequiredMethodsArePresent(t *testing.T) {
	registered := buildCurrentRegistry(t)
	for _, method := range requiredMethods {
		if !registered[method] {
			t.Errorf(
				"REQUIRED method %q is not registered; it must be present in the "+
					"unified RPC registry (protocol 2026-06-30, §11 P0)",
				method,
			)
		}
	}
}

// TestProtocolVersionContract asserts that the current registry reports the
// unified protocol version.
func TestProtocolVersionContract(t *testing.T) {
	const want = "2026-06-30"
	if workrpc.ProtocolVersion != want {
		t.Errorf(
			"workrpc.ProtocolVersion = %q; unified protocol requires %q (§5.1)",
			workrpc.ProtocolVersion, want,
		)
	}
}
