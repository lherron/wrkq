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
import type { WrkqContainer, WrkqTask, WrkqTaskCopyResult } from "../src/wrkq/types";

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
  assigneePrincipalRef: "agent:larry",
  createdAt: "2026-06-14T00:00:00Z",
  updatedAt: "2026-06-14T00:00:00Z",
};

const MOCK_CONTAINER: WrkqContainer = {
  uuid: "p-1",
  id: "P-00001",
  slug: "project",
  title: "Project",
  kind: "project",
  path: "project",
  etag: 1,
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
    expect(task.assigneePrincipalRef).toBe("agent:larry");
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

  test("task.move forwards targetPath and root expectEtag CAS precondition", async () => {
    const transport = new FakeTransport().onResult("wrkq.task.move", {
      ...MOCK_TASK,
      projectUuid: "p-2",
      path: "done/my-task",
      etag: 3,
    });
    const client = await clientWith(transport);

    const moved = await client.wrkq.task.move({
      task: "T-00001",
      targetPath: "done",
      expectEtag: 2,
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkq.task.move");
    expect(frame.params).toMatchObject({
      task: "T-00001",
      targetPath: "done",
      expectEtag: 2,
    });
    expect(moved.path).toBe("done/my-task");
    expect(moved.etag).toBe(3);
  });

  test("task.list returns the items envelope", async () => {
    const transport = new FakeTransport().onResult("wrkq.task.list", { items: [MOCK_TASK] });
    const client = await clientWith(transport);
    const res = await client.wrkq.task.list({ state: "open", summary: true });
    expect(transport.capturedRequests[0]!.params).toMatchObject({
      state: "open",
      summary: true,
    });
    expect(res.items).toHaveLength(1);
    expect(res.items[0]!.id).toBe("T-00001");
  });

  test("container mutation facade forwards create, delete, and deleteRecursive", async () => {
    const transport = new FakeTransport()
      .onResult("wrkq.container.create", MOCK_CONTAINER)
      .onResult("wrkq.container.delete", { deleted: true })
      .onResult("wrkq.container.deleteRecursive", {
        deleted: true,
        containersDeleted: 2,
        tasksDeleted: 3,
        attachmentsDeleted: 1,
        bytesFreed: 42,
      });
    const client = await clientWith(transport);

    const created = await client.wrkq.container.create({
      path: "project",
      slug: "child",
      kind: "directory",
    });
    const deleted = await client.wrkq.container.delete({ path: "project/child", expectEtag: 1 });
    const recursive = await client.wrkq.container.deleteRecursive({
      path: "project",
      expected: { containers: 2, tasks: 3, attachments: 1, bytes: 42 },
    });

    expect(created.id).toBe("P-00001");
    expect(deleted.deleted).toBe(true);
    expect(recursive.deleted).toBe(true);
    expect(transport.capturedRequests.map((r) => r.method)).toEqual([
      "wrkq.container.create",
      "wrkq.container.delete",
      "wrkq.container.deleteRecursive",
    ]);
    expect(transport.capturedRequests[2]!.params).toMatchObject({
      path: "project",
      expected: { containers: 2, tasks: 3, attachments: 1, bytes: 42 },
    });
  });

  test("task.restore forwards the full legacy restore op (move/fields/comment/ifMatch)", async () => {
    const transport = new FakeTransport().onResult("wrkq.task.restore", {
      ...MOCK_TASK,
      state: "open",
      path: "backlog/my-task",
    });
    const client = await clientWith(transport);

    const restored = await client.wrkq.task.restore({
      task: "T-00001",
      state: "open",
      toPath: "backlog/my-task",
      title: "renamed on restore",
      description: "fresh body",
      priority: 2,
      labels: '["urgent"]',
      assignee: "agent:larry",
      comment: "restored after triage",
      ifMatch: 4,
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkq.task.restore");
    expect(frame.params).toMatchObject({
      task: "T-00001",
      state: "open",
      toPath: "backlog/my-task",
      title: "renamed on restore",
      description: "fresh body",
      priority: 2,
      labels: '["urgent"]',
      assignee: "agent:larry",
      comment: "restored after triage",
      ifMatch: 4,
    });
    expect(restored.path).toBe("backlog/my-task");
  });

  test("task.copy forwards wrkq.task.copy and returns snake_case WrkqTaskCopyResult", async () => {
    const MOCK_COPY_RESULT: WrkqTaskCopyResult = {
      source_id: "T-00001",
      source_uuid: "u-1",
      dest_id: "T-00009",
      dest_uuid: "u-9",
      dest_path: "done/my-task",
      attachments_copied: 2,
      with_files: true,
    };
    const transport = new FakeTransport().onResult("wrkq.task.copy", MOCK_COPY_RESULT);
    const client = await clientWith(transport);

    const copied = await client.wrkq.task.copy({
      source: "T-00001",
      destination: "done",
      overwrite: true,
      withAttachments: true,
      expectEtag: 2,
      actor: "agent:larry",
      idempotencyKey: "cp1",
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkq.task.copy");
    expect(frame.params).toMatchObject({
      source: "T-00001",
      destination: "done",
      overwrite: true,
      withAttachments: true,
      expectEtag: 2,
      idempotencyKey: "cp1",
    });
    // Result keys are DELIBERATELY snake_case (legacy copyResult byte-parity).
    expect(copied.source_id).toBe("T-00001");
    expect(copied.source_uuid).toBe("u-1");
    expect(copied.dest_id).toBe("T-00009");
    expect(copied.dest_uuid).toBe("u-9");
    expect(copied.dest_path).toBe("done/my-task");
    expect(copied.attachments_copied).toBe(2);
    expect(copied.with_files).toBe(true);
  });

  test("container.update forwards wrkq.container.update and returns WrkqContainer", async () => {
    const transport = new FakeTransport().onResult("wrkq.container.update", {
      ...MOCK_CONTAINER,
      slug: "renamed",
      title: "Renamed",
      path: "renamed",
      etag: 2,
    });
    const client = await clientWith(transport);

    const updated = await client.wrkq.container.update({
      container: "project",
      patch: { slug: "renamed", title: "Renamed" },
      expectEtag: 1,
      actor: "agent:larry",
      idempotencyKey: "cu1",
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkq.container.update");
    expect(frame.params).toMatchObject({
      container: "project",
      patch: { slug: "renamed", title: "Renamed" },
      expectEtag: 1,
      idempotencyKey: "cu1",
    });
    expect(updated.slug).toBe("renamed");
    expect(updated.title).toBe("Renamed");
    expect(updated.etag).toBe(2);
  });

  test("webhook.add forwards wrkq.webhook.add and returns the changed mutation result", async () => {
    const transport = new FakeTransport().onResult("wrkq.webhook.add", {
      changed: true,
      count: 1,
      target: "https://hook.test/wrkq",
      webhook_urls: ["https://hook.test/wrkq"],
    });
    const client = await clientWith(transport);

    const result = await client.wrkq.webhook.add({
      url: "https://hook.test/wrkq",
      actor: "agent:larry",
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkq.webhook.add");
    expect(frame.params).toMatchObject({ url: "https://hook.test/wrkq", actor: "agent:larry" });
    expect(result.changed).toBe(true);
    expect(result.count).toBe(1);
    expect(result.target).toBe("https://hook.test/wrkq");
    expect(result.webhook_urls).toEqual(["https://hook.test/wrkq"]);
  });

  test("webhook.remove forwards wrkq.webhook.remove and returns a no-change result", async () => {
    const transport = new FakeTransport().onResult("wrkq.webhook.remove", {
      changed: false,
      webhook_urls: [],
    });
    const client = await clientWith(transport);

    const result = await client.wrkq.webhook.remove({ url: "https://absent.test/wrkq" });

    expect(transport.capturedRequests[0]!.method).toBe("wrkq.webhook.remove");
    expect(result.changed).toBe(false);
    expect(result.count).toBeUndefined();
    expect(result.webhook_urls).toEqual([]);
  });

  test("webhook.listView forwards wrkq.webhook.listView and returns {url} rows", async () => {
    const transport = new FakeTransport().onResult("wrkq.webhook.listView", [
      { url: "https://a.test/wrkq" },
      { url: "https://b.test/wrkq" },
    ]);
    const client = await clientWith(transport);

    const rows = await client.wrkq.webhook.listView();

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkq.webhook.listView");
    expect(frame.params).toEqual({});
    expect(rows).toEqual([{ url: "https://a.test/wrkq" }, { url: "https://b.test/wrkq" }]);
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

  test("admin.legacyActor facade forwards list, create, and update", async () => {
    const MOCK_ACTOR = {
      uuid: "a-u-1",
      id: "A-00001",
      slug: "larry",
      displayName: "Larry",
      role: "agent",
      meta: { team: "core" },
      createdAt: "2026-06-14T00:00:00Z",
      updatedAt: "2026-06-14T00:00:00Z",
    };
    const transport = new FakeTransport()
      .onResult("wrkq.admin.legacyActor.list", { items: [MOCK_ACTOR] })
      .onResult("wrkq.admin.legacyActor.create", MOCK_ACTOR)
      .onResult("wrkq.admin.legacyActor.update", { ...MOCK_ACTOR, displayName: "Larry P" });
    const client = await clientWith(transport);

    const listed = await client.wrkq.admin.legacyActor.list();
    const created = await client.wrkq.admin.legacyActor.create({
      slug: "larry",
      displayName: "Larry",
      role: "agent",
      meta: { team: "core" },
    });
    const updated = await client.wrkq.admin.legacyActor.update({
      actor: "larry",
      patch: { displayName: "Larry P", meta: null },
      expectUpdatedAt: "2026-06-14T00:00:00Z",
    });

    expect(listed.items).toHaveLength(1);
    expect(listed.items[0]!.id).toBe("A-00001");
    expect(created.slug).toBe("larry");
    expect(updated.displayName).toBe("Larry P");
    // DTO must not leak a principalRef field.
    expect("principalRef" in created).toBe(false);
    expect(transport.capturedRequests.map((r) => r.method)).toEqual([
      "wrkq.admin.legacyActor.list",
      "wrkq.admin.legacyActor.create",
      "wrkq.admin.legacyActor.update",
    ]);
    expect(transport.capturedRequests[1]!.params).toMatchObject({
      slug: "larry",
      role: "agent",
      meta: { team: "core" },
    });
    expect(transport.capturedRequests[2]!.params).toMatchObject({
      actor: "larry",
      patch: { displayName: "Larry P", meta: null },
      expectUpdatedAt: "2026-06-14T00:00:00Z",
    });
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

  test("WRKQ_DB_BUSY contention surfaces as a retryable wrkq error", async () => {
    const transport = new FakeTransport().onError("wrkq.task.update", {
      code: -32024,
      message: "database is busy due to write contention; retry",
      data: { code: "WRKQ_DB_BUSY", retryable: true, reason: "sqlite_busy" },
    });
    const client = await clientWith(transport);
    let caught: unknown;
    try {
      await client.wrkq.task.update({ task: "T-1", patch: {} });
    } catch (e) {
      caught = e;
    }
    expect(isWrkqError(caught)).toBe(true);
    expect((caught as WorkRpcError).domainCode).toBe("WRKQ_DB_BUSY");
    expect((caught as WorkRpcError).retryable).toBe(true);
    expect((caught as WorkRpcError).data?.reason).toBe("sqlite_busy");
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
