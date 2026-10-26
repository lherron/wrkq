//go:build wrkq_local

package workrpc_test

// registry_contract_test.go — the agent-loop readiness bar for the unified
// wrkq/wrkf RPC method registry (protocol version 2026-06-30), read the way a
// caller reads it: from the `methods` list rpc.initialize advertises.
//
// Retired and cross-owner names (wrkf.* task mutation / workflow attachment, the
// deprecated wrkf.initialize) are pinned as method-not-found in
// server_contract_test.go TestRetiredAndCrossOwnerMethodsAnswerMethodNotFound.

import (
	"testing"
)

// requiredMethods are method names that MUST be present in the unified RPC
// registry to meet the agent-loop readiness bar (§11 P0, §13, §14).
var requiredMethods = []string{
	// Lifecycle (§5.4).
	"rpc.initialize",
	// wrkq namespace — task create and workflow attach (§6.2).
	"wrkq.task.create",
	"wrkq.workflow.attach",
	"wrkq.container.taskCounts",
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

// TestInitializeAdvertisesAgentLoopRequiredMethods: rpc.initialize over the real
// `wrkq rpc --stdio` boundary advertises every method the agent-loop readiness
// bar requires in its `methods` list.
func TestInitializeAdvertisesAgentLoopRequiredMethods(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	frames := p2Run(t, migratedDB(t))
	initResult := p2ResultOrFail(t, frames[0], "rpc.initialize")
	advertised := map[string]bool{}
	methods, _ := initResult["methods"].([]any)
	for _, m := range methods {
		if name, ok := m.(string); ok {
			advertised[name] = true
		}
	}
	for _, method := range requiredMethods {
		if !advertised[method] {
			t.Errorf(
				"REQUIRED method %q is not advertised by rpc.initialize; it must be present in the "+
					"unified RPC registry (protocol 2026-06-30, §11 P0)",
				method,
			)
		}
	}
}
