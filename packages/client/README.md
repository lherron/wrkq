# @wrkq/client

Unified Bun TypeScript client for the **wrkq/wrkf** stdio JSON-RPC protocol.

One package, one client object, two business namespaces:

- `client.wrkq.*` — task ownership (tasks, comments, attachments, relations,
  containers, workflow attachment/inspection)
- `client.wrkf.*` — workflow behavior (templates, instances, evidence,
  obligations, checks/hooks, transitions, runs, effects)

Lifecycle controls live under `client.rpc.*`. There are no root business
namespaces (`client.task`, `client.workflow`, … are intentionally absent).

The client speaks **only** the unified stdio JSON-RPC protocol. It never calls
`wrkqd` HTTP and never parses human/JSON CLI output.

Contract: [`docs/wrkq-wrkf-rpc.md`](../../docs/wrkq-wrkf-rpc.md) ·
Spec: [`docs/wrkq-wrkf-rpc-client-forward-spec.md`](../../docs/wrkq-wrkf-rpc-client-forward-spec.md)

## Requirements

Bun ≥ 1.3. The `wrkq` (or `wrkf`) binary must be on `PATH` or referenced by an
absolute path. Both entrypoints serve the identical protocol (proto
`2026-06-14`); the choice of binary does not change client semantics.

## Install / import

```ts
import { createClient, WorkRpcError, isWrkfError } from "@wrkq/client";
import type { WrkqTask } from "@wrkq/client/wrkq";
import type { WrkfTransitionResult } from "@wrkq/client/wrkf";
import { FakeTransport } from "@wrkq/client/testing";
```

## createClient

```ts
const client = await createClient({
  command: "wrkq",          // or "wrkf" — identical behavior
  dbPath: "/path/to/wrkq.db",
  actor: "agent:agent-loop",
  role: "coordinator",
  clientInfo: { name: "agent-loop", version: "0.1.0" },
});
// createClient runs rpc.initialize before resolving (autoInitialize: true).
```

## wrkq: create a task

```ts
const task = await client.wrkq.task.create({
  title: "Implement durable workflow primitive",
  description: "Wire agent execution to wrkq/wrkf primitives.",
  kind: "task",
  state: "open",
  idempotencyKey: "agent-loop:create:1",
});

// Atomic compare-and-set update:
await client.wrkq.task.update({
  task: task.id,
  patch: { state: "in_progress" },
  expectEtag: task.etag,
});
```

## wrkq: attach a workflow (a wrkq verb)

```ts
const attached = await client.wrkq.workflow.attach({
  task: task.id,
  workflow: "code_change@1",
  idempotencyKey: `agent-loop:attach:${task.id}:code_change@1`,
});
const inspect = await client.wrkq.workflow.inspect({ task: task.id });
```

## wrkf: evidence / transition / run / effect

```ts
await client.wrkf.workflow.install({ path: "./wrkf/templates/code-change.json" });

const run = await client.wrkf.run.start({
  task: task.id,
  role: "implementer",
  actor: "agent:agent-loop",
  externalRunRef: "agent-loop-run-123",
});

const evidence = await client.wrkf.evidence.add({
  task: task.id,
  kind: "implementation",
  ref: "/tmp/artifacts/output.json",
  summary: "Implementation complete",
  facts: { verdict: "ready" },
  role: "implementer",
  actor: "agent:agent-loop",
  runId: run.id,                       // persisted + returned on the evidence DTO
});

const transition = await client.wrkf.transition.apply({
  task: task.id,
  transition: "implementation_ready",
  role: "implementer",
  actor: "agent:agent-loop",
  expectRevision: inspect.instance.revision,   // CAS preconditions
  contextHash: inspect.instance.contextHash,
});

const claim = await client.wrkf.effect.claim({ adapter: "wake_role", limit: 5, leaseMs: 60_000 });
for (const eff of claim.effects) {
  await client.wrkf.effect.ack({ effectId: eff.id, leaseToken: claim.leaseToken });
}

await client.wrkf.run.finish({ runId: run.id, summary: "done" });
await client.close();
```

## Error handling

Every server error frame surfaces as a single `WorkRpcError`:

```ts
import { WorkRpcError, isWorkRpcError, isWrkqError, isWrkfError } from "@wrkq/client";

try {
  await client.wrkf.transition.apply({ task: task.id, transition: "x", expectRevision: 0 });
} catch (err) {
  if (isWrkfError(err)) {
    // err.domainCode e.g. "WRKF_STALE_REVISION"; err.retryable; err.rpcCode;
    // err.requestId; err.method; err.data carries CAS/lease/blocker fields.
  } else if (isWrkqError(err)) {
    // err.domainCode e.g. "WRKQ_CONFLICT"
  } else if (isWorkRpcError(err)) {
    // protocol error (parse / invalid request / method not found): no domainCode
  }
}
```

## Entrypoint equivalence

`wrkq rpc --stdio` and `wrkf rpc --stdio` start the same RPC server and expose
the same protocol version, schema hash, capabilities, method catalog, DTO
shapes, and error contract. The only difference is the diagnostic
`server.entrypoint` field on the `rpc.initialize` result. Pick whichever binary
is convenient.

## Testing

`@wrkq/client/testing` exports `FakeTransport`, an in-memory transport you can
inject via `createClient({ transport })` to unit-test without spawning a binary.

```ts
import { createClient } from "@wrkq/client";
import { FakeTransport } from "@wrkq/client/testing";

const transport = new FakeTransport().onResult("wrkq.task.create", mockTask);
const client = await createClient({ transport, autoInitialize: false });
```

## Scripts

```bash
bun test            # unit + fake-transport + real-binary integration
bun run typecheck   # bun x tsc --noEmit
bun run build       # emit dist/ (js + d.ts)
```

Integration tests require `just install` (so `wrkq`/`wrkf`/`wrkqadm` are on
`PATH`). Override discovery with `WRKQ_BIN` / `WRKF_BIN` / `WRKQADM_BIN`.
