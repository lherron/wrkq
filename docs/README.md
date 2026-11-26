# wrkq documentation

## Product and protocol

| Document | Kind | Code | Grader |
| --- | --- | --- | --- |
| [Product, CLI, and daemon specification](SPEC.md) | spec | `internal/domain/**`, `internal/store/**`, `internal/wrkqapi/**`, `internal/rpccli/**`, `internal/wrkqd/**` | `internal/rpccli/*_acceptance_test.go`, `internal/workrpc/*_acceptance_test.go`, `internal/wrkqd/*_test.go` |
| [Unified wrkq and wrkf RPC contract](wrkq-wrkf-rpc.md) | spec | `internal/workrpc/**`, `internal/wrkqapi/**`, `internal/wrkfapi/**`, `pkg/client/**`, `packages/client/src/**` | `internal/workrpc/*_contract_test.go`, `internal/workrpc/*_acceptance_test.go`, `internal/workrpc/entrypoint_equivalence_test.go` |
| [Promise contract](../architecture/contracts/WRKQ_PROMISES.md) | spec | `internal/store/promise*.go`, `internal/wrkqapi/promise*.go`, `internal/webhooks/*.go` | `internal/store/promises_test.go`, `internal/store/promises_webhooks_test.go`, `internal/wrkqapi/promises_test.go`, `internal/workrpc/promise_acceptance_test.go` |
| [Campaign transition webhook](campaign-webhook.md) | spec | `internal/store/campaign*.go`, `internal/webhooks/*.go` | `internal/store/campaign_webhook_test.go`, `internal/webhooks/root_inheritance_test.go` |
| [wrkf RPC recovery and client guide](wrkf-rpc.md) | guide | | |
| [Rooms, handoffs, search, monitoring, and attribution](wrkq-concepts.md) | guide | | |

## Architecture

| Document | Kind | Code | Grader |
| --- | --- | --- | --- |
| [Durable architecture and ADRs](../architecture/README.md) | decisions | | |
| [Active invariants](../architecture/INVARIANTS.md) | guide | | |
| [Accepted risks](../architecture/RISKS.md) | guide | | |
| [ADR 0001 ― wrkf RPC recovery](../architecture/adr/0001-wrkf-rpc-recovery-contract.md) | guide | | |
| [ADR 0002 ― durable collaboration ownership](../architecture/adr/0002-wrkq-owns-durable-agent-collaboration.md) | guide | | |
| [ADR 0003 ― caller-selected task views](../architecture/adr/0003-task-view-caller-state-selection.md) | guide | | |
| [ADR 0004 ― bounded commit waits](../architecture/adr/0004-bounded-commit-wait.md) | guide | | |
| [wrkf ledger record](../architecture/contracts/wrkf-ledger.yaml) | record | | |
| [wrkf RPC record](../architecture/contracts/wrkf-rpc.yaml) | record | | |
| [wrkf verify-launch producer record](../architecture/contracts/wrkf-verify-launch-producer.yaml) | record | | |

## Operations and development

| Document | Kind | Code | Grader |
| --- | --- | --- | --- |
| [Database and daemon operations](wrkq-operations.md) | guide | | |
| [Search index operations](../internal/search/README.md) | guide | | |
| [Change validation](change-validation.md) | guide | | |
| [Rule authoring](rule-template.md) | guide | | |
| [Hook duration measurement and replay](../tools/hooks/README.md) | guide | | |
| [MCP server setup](../mcp-server/README.md) | guide | | |
| [TypeScript client guide](../packages/client/README.md) | guide | | |
| [Go client guide](../pkg/client/README.md) | guide | | |
