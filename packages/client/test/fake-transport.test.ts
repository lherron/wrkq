/**
 * fake-transport.test.ts — unit tests for the unified client over an in-memory
 * transport. No subprocess, no I/O.
 *
 * Contract: docs/wrkq-wrkf-rpc.md §4, §5, §6, §7.
 * Frozen method names, DTO field names, and error codes are encoded verbatim.
 */

import { describe, expect, test } from "bun:test";
import { createClient } from "../src/client";
import { WorkRpcError, isWorkRpcError, isWrkfError, isWrkqError } from "../src/errors";
import { FakeTransport } from "../src/testing/fake-transport";
import type { WrkfEventQueryResult, WrkfTransitionResult } from "../src/wrkf/types";
import type { WrkqTask } from "../src/wrkq/types";

async function clientWith(transport: FakeTransport, autoInitialize = false) {
  return createClient({ transport, autoInitialize });
}

const MOCK_TASK: WrkqTask = {
  uuid: "u-1",
  id: "T-00001",
  slug: "my-task",
  title: "my task",
  projectUuid: "p-1",
  path: "inbox/my-task",
  state: "open",
  priority: 3,
  kind: "task",
  description: "",
  specification: "",
  labels: [],
  meta: {},
  riskClass: "medium",
  etag: 2,
  createdAt: "2026-06-14T00:00:00Z",
  updatedAt: "2026-06-14T00:00:00Z",
};

const MOCK_TRANSITION_RESULT: WrkfTransitionResult = {
  task: "T-00001",
  instanceId: "wfi_abc123",
  state: { status: "active", phase: "done" },
  revision: 1,
  contextHash: "sha256:deadbeef",
  eventId: "wfe_000002",
  effects: [],
  obligations: [],
};

const MOCK_EVENT_QUERY_RESULT: WrkfEventQueryResult = {
  items: [
    {
      id: "wfe_000002",
      eventType: "workflow.transitioned",
      instanceId: "wfi_abc123",
      seq: 2,
      task: {
        uuid: "task-u-1",
        id: "T-00001",
        slug: "my-task",
        projectUuid: "p-1",
        projectId: "P-00001",
        projectSlug: "wrkq",
        riskClass: "medium",
      },
      transition: "author_red",
      outcome: "red_recorded",
      fromPhase: "intake",
      toPhase: "red",
      transitionedAt: "2026-06-15T14:00:00Z",
      actor: "agent:tester",
      actorRole: "tester",
      matchingRoleBindings: [
        {
          instanceId: "wfi_abc123",
          role: "tester",
          actor: "agent:tester",
          bindingMode: "required",
          boundAt: "2026-06-15T13:59:00Z",
        },
      ],
    },
  ],
  nextCursor: "cursor-1",
  hasMore: true,
};

describe("createClient lifecycle", () => {
  test("autoInitialize sends rpc.initialize with the protocol version", async () => {
    const transport = new FakeTransport().onResult("rpc.initialize", {
      protocolVersion: "2026-06-14",
      protocolSchemaHash: "sha256:x",
      server: { name: "wrkq-wrkf-rpc", version: "dev", pid: 1, entrypoint: "wrkq" },
      database: { path: "/db", migrationHash: "sha256:y" },
      capabilities: {
        cancel: true,
        wrkq: true,
        wrkf: true,
        effectClaimLease: true,
        runExternalBinding: true,
      },
      methods: ["rpc.initialize"],
    });

    const client = await createClient({ transport, autoInitialize: true });
    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("rpc.initialize");
    expect(frame.jsonrpc).toBe("2.0");
    expect(frame.params).toMatchObject({ protocolVersion: "2026-06-14" });
    await client.close();
    expect(transport.closed).toBe(true);
  });

  test("close() sends rpc.shutdown then closes transport", async () => {
    const transport = new FakeTransport().onResult("rpc.shutdown", {});
    const client = await clientWith(transport);
    await client.close();
    expect(transport.capturedRequests.some((r) => r.method === "rpc.shutdown")).toBe(true);
    expect(transport.closed).toBe(true);
  });
});

describe("wrkq namespace", () => {
  test("task.create sends wrkq.task.create and returns typed WrkqTask", async () => {
    const transport = new FakeTransport().onResult("wrkq.task.create", MOCK_TASK);
    const client = await clientWith(transport);

    const task = await client.wrkq.task.create({
      title: "my task",
      kind: "task",
      state: "open",
      riskClass: "medium",
      idempotencyKey: "k1",
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkq.task.create");
    expect(frame.params).toMatchObject({
      title: "my task",
      riskClass: "medium",
      idempotencyKey: "k1",
    });
    expect(task.id).toBe("T-00001");
    expect(task.riskClass).toBe("medium");
    expect(task.etag).toBe(2);
  });

  test("task.update forwards expectEtag CAS precondition verbatim", async () => {
    const transport = new FakeTransport().onResult("wrkq.task.update", MOCK_TASK);
    const client = await clientWith(transport);

    await client.wrkq.task.update({
      task: "T-00001",
      patch: { state: "in_progress", riskClass: "high" },
      expectEtag: 2,
      idempotencyKey: "u1",
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkq.task.update");
    expect(frame.params).toMatchObject({
      task: "T-00001",
      patch: { state: "in_progress", riskClass: "high" },
      expectEtag: 2,
    });
  });

  test("task.list returns the items envelope", async () => {
    const transport = new FakeTransport().onResult("wrkq.task.list", { items: [MOCK_TASK] });
    const client = await clientWith(transport);
    const res = await client.wrkq.task.list({ state: "open" });
    expect(res.items).toHaveLength(1);
    expect(res.items[0]!.id).toBe("T-00001");
  });

  test("workflow.attach is a wrkq verb", async () => {
    const transport = new FakeTransport().onResult("wrkq.workflow.attach", {
      task: MOCK_TASK,
      instance: { id: "wfi_x", revision: 0, contextHash: "sha256:c" },
      attached: true,
    });
    const client = await clientWith(transport);
    const res = await client.wrkq.workflow.attach({ task: "T-00001", workflow: "f@1" });
    expect(transport.capturedRequests[0]!.method).toBe("wrkq.workflow.attach");
    expect(res.attached).toBe(true);
    expect(res.instance.id).toBe("wfi_x");
  });

  test("workflow.timeline returns typed events envelope", async () => {
    const transport = new FakeTransport().onResult("wrkq.workflow.timeline", {
      events: [
        {
          id: "wfe_1",
          type: "workflow.transitioned",
          payload: { transition: "finish", outcome: "done" },
        },
      ],
    });
    const client = await clientWith(transport);
    const res = await client.wrkq.workflow.timeline({ task: "T-00001" });
    expect(transport.capturedRequests[0]!.method).toBe("wrkq.workflow.timeline");
    expect(res.events[0]!.payload?.transition).toBe("finish");
  });
});

describe("wrkf namespace", () => {
  test("transition.apply sends frame, forwards CAS params, returns typed result", async () => {
    const transport = new FakeTransport().onResult(
      "wrkf.transition.apply",
      MOCK_TRANSITION_RESULT,
    );
    const client = await clientWith(transport);

    const result = await client.wrkf.transition.apply({
      task: "T-00001",
      transition: "plan_ready",
      role: "coordinator",
      actor: "human:local",
      expectRevision: 0,
      contextHash: "sha256:abc",
      idempotencyKey: "k:plan_ready:0",
      runChecks: false,
      dryRun: false,
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkf.transition.apply");
    expect(frame.params).toMatchObject({
      task: "T-00001",
      transition: "plan_ready",
      expectRevision: 0,
      contextHash: "sha256:abc",
    });
    expect(result.instanceId).toBe("wfi_abc123");
    expect(result.revision).toBe(1);
    expect(Array.isArray(result.effects)).toBe(true);
  });

  test("event.query sends replay filters and returns a typed page", async () => {
    const transport = new FakeTransport().onResult("wrkf.event.query", MOCK_EVENT_QUERY_RESULT);
    const client = await clientWith(transport);

    const result = await client.wrkf.event.query({
      eventType: "workflow.transitioned",
      fromPhase: "intake",
      toPhase: "red",
      excludeRiskClass: "low",
      boundRole: "tester",
      limit: 100,
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkf.event.query");
    expect(frame.params).toMatchObject({
      fromPhase: "intake",
      toPhase: "red",
      excludeRiskClass: "low",
      boundRole: "tester",
    });
    expect(result.items[0]!.id).toBe("wfe_000002");
    expect(result.items[0]!.matchingRoleBindings[0]!.role).toBe("tester");
    expect(result.nextCursor).toBe("cursor-1");
    expect(result.hasMore).toBe(true);
  });

  test("evidence.add forwards runId and provenance (spec §9.7)", async () => {
    const transport = new FakeTransport().onResult("wrkf.evidence.add", {
      id: "ev_1",
      kind: "implementation",
      runId: "run_000001",
      contentHash: "sha256:abc",
      build: { id: "b1", version: "2026.06.15", env: "ci" },
    });
    const client = await clientWith(transport);
    const ev = await client.wrkf.evidence.add({
      task: "T-00001",
      kind: "implementation",
      facts: { verdict: "ready" },
      runId: "run_000001",
      contentHash: "sha256:abc",
      build: { id: "b1", version: "2026.06.15", env: "ci" },
    });
    expect(transport.capturedRequests[0]!.params).toMatchObject({
      runId: "run_000001",
      contentHash: "sha256:abc",
      build: { id: "b1", version: "2026.06.15", env: "ci" },
    });
    expect(ev.runId).toBe("run_000001");
    expect(ev.contentHash).toBe("sha256:abc");
  });

  test("role facade forwards list, bind, unbind, and set", async () => {
    const binding = {
      instanceId: "wfi_1",
      role: "implementer",
      actor: "agent:cody",
      deliveryRef: "cody@wrkq:T-1",
      lane: "main",
      bindingMode: "required",
      boundAt: "2026-06-15T00:00:00Z",
    };
    const transport = new FakeTransport()
      .onResult("wrkf.role.bind", binding)
      .onResult("wrkf.role.list", [binding])
      .onResult("wrkf.role.unbind", [])
      .onResult("wrkf.role.set", [binding]);
    const client = await clientWith(transport);

    await client.wrkf.role.bind({
      task: "T-00001",
      role: "implementer",
      actor: "agent:cody",
      deliveryRef: "cody@wrkq:T-1",
      lane: "main",
    });
    await client.wrkf.role.list({ task: "T-00001" });
    await client.wrkf.role.unbind({ task: "T-00001", role: "implementer", actor: "agent:cody" });
    await client.wrkf.role.set({ task: "T-00001", roleMap: { implementer: "agent:cody" } });

    expect(transport.capturedRequests.map((r) => r.method)).toEqual([
      "wrkf.role.bind",
      "wrkf.role.list",
      "wrkf.role.unbind",
      "wrkf.role.set",
    ]);
  });

  test("effect.claim returns lease token + expiry", async () => {
    const transport = new FakeTransport().onResult("wrkf.effect.claim", {
      effects: [{ id: "eff_1", status: "leased" }],
      leaseToken: "lease_abc",
      leaseExpiresAt: "2026-06-14T01:00:00Z",
    });
    const client = await clientWith(transport);
    const claim = await client.wrkf.effect.claim({ adapter: "wake_role", limit: 5, leaseMs: 60000 });
    expect(claim.leaseToken).toBe("lease_abc");
    expect(claim.effects[0]!.id).toBe("eff_1");
  });
});

describe("error mapping", () => {
  test("domain error frame surfaces as WorkRpcError with method + requestId", async () => {
    const transport = new FakeTransport().onError("wrkf.transition.apply", {
      code: -32009,
      message: "workflow revision mismatch",
      data: {
        code: "WRKF_STALE_REVISION",
        retryable: true,
        expectedRevision: 3,
        actualRevision: 4,
      },
    });
    const client = await clientWith(transport);

    let caught: unknown;
    try {
      await client.wrkf.transition.apply({ task: "T-00001", transition: "plan_ready", expectRevision: 3 });
    } catch (e) {
      caught = e;
    }

    expect(caught).toBeInstanceOf(WorkRpcError);
    const err = caught as WorkRpcError;
    expect(err.domainCode).toBe("WRKF_STALE_REVISION");
    expect(err.rpcCode).toBe(-32009);
    expect(err.retryable).toBe(true);
    expect(err.method).toBe("wrkf.transition.apply");
    expect(err.requestId).toBe(err.requestId); // present
    expect((err.data as any).expectedRevision).toBe(3);

    expect(isWorkRpcError(err)).toBe(true);
    expect(isWrkfError(err)).toBe(true);
    expect(isWrkqError(err)).toBe(false);
  });

  test("WRKQ_* domain error classified by isWrkqError", async () => {
    const transport = new FakeTransport().onError("wrkq.task.update", {
      code: -32021,
      message: "stale etag",
      data: { code: "WRKQ_CONFLICT", retryable: true, currentEtag: 7 },
    });
    const client = await clientWith(transport);
    let caught: unknown;
    try {
      await client.wrkq.task.update({ task: "T-1", patch: {}, expectEtag: 2 });
    } catch (e) {
      caught = e;
    }
    expect(isWrkqError(caught)).toBe(true);
    expect(isWrkfError(caught)).toBe(false);
    expect((caught as WorkRpcError).domainCode).toBe("WRKQ_CONFLICT");
  });

  test("protocol error (method-not-found) has no domainCode", async () => {
    const transport = new FakeTransport(); // no handlers → -32601
    const client = await clientWith(transport);
    let caught: unknown;
    try {
      await client.call("wrkf.bogus.method");
    } catch (e) {
      caught = e;
    }
    expect(isWorkRpcError(caught)).toBe(true);
    const err = caught as WorkRpcError;
    expect(err.rpcCode).toBe(-32601);
    expect(err.domainCode).toBeUndefined();
    expect(isWrkqError(err)).toBe(false);
    expect(isWrkfError(err)).toBe(false);
  });
});
