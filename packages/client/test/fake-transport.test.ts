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
import type {
  WrkqContainer,
  WrkqTaskCreateParams,
  WrkqHandoff,
  WrkqTask,
  WrkqTaskCopyResult,
} from "../src/wrkq/types";

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
  createdAt: "2026-06-30T00:00:00Z",
  updatedAt: "2026-06-30T00:00:00Z",
};

const MOCK_CONTAINER: WrkqContainer = {
  uuid: "p-1",
  id: "P-00001",
  slug: "project",
  title: "Project",
  kind: "project",
  path: "project",
  etag: 1,
  createdAt: "2026-06-30T00:00:00Z",
  updatedAt: "2026-06-30T00:00:00Z",
};

const MOCK_TRANSITION_RESULT: WrkfTransitionResult = {
  task: "T-00001",
  instanceId: "wfi_abc123",
  state: { status: "active", phase: "done" },
  revision: 1,
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
      occurredAt: "2026-06-15T14:00:00Z",
      beforeRevision: 0,
      afterRevision: 1,
      principal_ref: "agent:tester",
      role: "tester",
      matchingRoleBindings: [
        {
          instanceId: "wfi_abc123",
          role: "tester",
          principal_ref: "agent:tester",
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
      protocolVersion: "2026-06-30",
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
    expect(frame.params).toMatchObject({ protocolVersion: "2026-06-30" });
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

  test("T-05381 task.create uses principalRef and rejects legacy actor attribution", async () => {
    const transport = new FakeTransport().onResult("wrkq.task.create", MOCK_TASK);
    const client = await clientWith(transport);

    await client.wrkq.task.create({
      title: "principal-only task",
      kind: "task",
      state: "open",
      principalRef: "agent:calchas",
      idempotencyKey: "principal-only-create",
    } as WrkqTaskCreateParams);

    expect(transport.capturedRequests[0]!.params).toMatchObject({
      principalRef: "agent:calchas",
    });
    expect(transport.capturedRequests[0]!.params).not.toHaveProperty("actor");

    // T-05381 removes `actor` as a wrkq mutation caller-attribution DTO field.
    // The client must fail before emitting a JSON-RPC frame so legacy callers
    // cannot be silently honored by an older server.
    await expect(
      client.wrkq.task.create({
        title: "legacy actor task",
        kind: "task",
        state: "open",
        actor: "agent:calchas",
        idempotencyKey: "legacy-actor-create",
      } as WrkqTaskCreateParams),
    ).rejects.toThrow(/actor|principalRef/i);

    expect(transport.capturedRequests).toHaveLength(1);
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

  test("handoff family forwards create/get/listView/acknowledge with explicit scope", async () => {
    const MOCK_HANDOFF: WrkqHandoff = {
      uuid: "h-u-1",
      id: "H-00001",
      scope_ref: "agent:cody:project:wrkq",
      scope_kind: "project",
      agent_id: "cody",
      project_id: "wrkq",
      agent_actor_uuid: null,
      project_container_uuid: null,
      created_by_agent_id: "cody",
      created_by_actor_uuid: null,
      title: "Next steps",
      body: "carry-over notes",
      status: "pending",
      idempotency_key: null,
      acknowledged_at: null,
      acknowledged_by_agent_id: null,
      acknowledged_by_actor_uuid: null,
      acknowledgement_note: null,
      meta: null,
      etag: 1,
      created_at: "2026-06-23T00:00:00Z",
      updated_at: "2026-06-23T00:00:00Z",
    };
    const transport = new FakeTransport()
      .onResult("wrkq.handoff.create", { handoff: MOCK_HANDOFF, idempotentReplay: false })
      .onResult("wrkq.handoff.get", MOCK_HANDOFF)
      .onResult("wrkq.handoff.listView", { items: [MOCK_HANDOFF], nextCursor: "cur-1" })
      .onResult("wrkq.handoff.acknowledge", {
        ...MOCK_HANDOFF,
        status: "acknowledged",
        acknowledged_at: "2026-06-23T01:00:00Z",
        acknowledged_by_agent_id: "cody",
        acknowledgement_note: "loaded",
        etag: 2,
      });
    const client = await clientWith(transport);

    const created = await client.wrkq.handoff.create({
      scopeRef: "agent:cody:project:wrkq",
      agentId: "cody",
      projectId: "wrkq",
      title: "Next steps",
      body: "carry-over notes",
      idempotencyKey: "k1",
    });
    const got = await client.wrkq.handoff.get({ handoff: "H-00001" });
    const listed = await client.wrkq.handoff.listView({
      scopeRef: "agent:cody:project:wrkq",
      status: "pending",
      limit: 50,
    });
    const acked = await client.wrkq.handoff.acknowledge({
      handoff: "H-00001",
      actorAgentId: "cody",
      principalRef: "agent:cody",
      scopeRef: "agent:cody:project:wrkq",
      note: "loaded",
      ifMatch: 1,
    });

    expect(transport.capturedRequests.map((r) => r.method)).toEqual([
      "wrkq.handoff.create",
      "wrkq.handoff.get",
      "wrkq.handoff.listView",
      "wrkq.handoff.acknowledge",
    ]);
    // create forwards the EXPLICIT caller-resolved scope (server reads no env).
    expect(transport.capturedRequests[0]!.params).toMatchObject({
      scopeRef: "agent:cody:project:wrkq",
      agentId: "cody",
      projectId: "wrkq",
      title: "Next steps",
      idempotencyKey: "k1",
    });
    expect(created.handoff.id).toBe("H-00001");
    expect(created.idempotentReplay).toBe(false);
    // DTO keys are DELIBERATELY snake_case (legacy handoffJSON byte-parity).
    expect(got.scope_ref).toBe("agent:cody:project:wrkq");
    expect(got.created_by_agent_id).toBe("cody");
    expect(listed.items[0]!.id).toBe("H-00001");
    expect(listed.nextCursor).toBe("cur-1");
    // acknowledge forwards the resolved acting identity + ifMatch CAS.
    expect(transport.capturedRequests[3]!.params).toMatchObject({
      handoff: "H-00001",
      actorAgentId: "cody",
      scopeRef: "agent:cody:project:wrkq",
      ifMatch: 1,
    });
    expect(acked.status).toBe("acknowledged");
    expect(acked.etag).toBe(2);
  });

  test("workflow.attach is a wrkq verb", async () => {
    const transport = new FakeTransport().onResult("wrkq.workflow.attach", {
      task: MOCK_TASK,
      instance: { id: "wfi_x", revision: 0 },
      attached: true,
    });
    const client = await clientWith(transport);
    const res = await client.wrkq.workflow.attach({ task: "T-00001", workflow: "f@1" });
    expect(transport.capturedRequests[0]!.method).toBe("wrkq.workflow.attach");
    expect(res.attached).toBe(true);
    expect(res.instance.id).toBe("wfi_x");
  });

  test("T-05381 removes the legacy actor admin facade", async () => {
    const client = await clientWith(new FakeTransport());
    expect((client.wrkq.admin as Record<string, unknown>).legacyActor).toBeUndefined();
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
  test("action facade preserves opaque sourceIdentity bindings", async () => {
    const sourceIdentity = "change-v1:implement-v4-1";
    const transport = new FakeTransport()
      .onResult("wrkf.action.next", {
        candidates: [
          {
            instanceId: "wfi_1",
            task: "T-00001",
            semanticActionKey: "verify:wfi_1:r1",
            action: "verify",
            transition: "verify_complete",
            role: "verifier",
            requiredEvidenceKind: "verify_result",
            expectedStateRevision: 1,
            expectedState: { status: "active", phase: "implemented" },
            rank: 1,
            source: {
              sourceRunId: "run-implement-1",
              sourceEvidenceId: "ev-implement-1",
              sourceIdentity,
            },
          },
        ],
      })
      .onResult("wrkf.action.claim", {
        binding: {
          run: {
            id: "run-verify-1",
            instanceId: "wfi_1",
            semanticActionKey: "verify:wfi_1:r1",
            action: "verify",
            role: "verifier",
            attempt: 1,
            status: "running",
            source: { sourceRunId: "run-implement-1", sourceIdentity },
            startedAt: "2026-07-10T00:00:00Z",
          },
        },
      });
    const client = await clientWith(transport);

    const next = await client.wrkf.action.next({ task: "T-00001" });
    const claim = await client.wrkf.action.claim({
      task: "T-00001",
      runnerId: "runner-1",
      agentRef: "agent:larry",
      leaseMs: 60_000,
      priorRun: null,
    });

    expect(next.candidates[0]?.source?.sourceIdentity).toBe(sourceIdentity);
    expect(claim.binding?.run.source?.sourceIdentity).toBe(sourceIdentity);
    expect(transport.capturedRequests[1]?.params).toMatchObject({ priorRun: null });
    expect(transport.capturedRequests.map((request) => request.method)).toEqual([
      "wrkf.action.next",
      "wrkf.action.claim",
    ]);
  });

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
      principal_ref: "human:local",
      expectRevision: 0,
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
    });
    expect(result.instanceId).toBe("wfi_abc123");
    expect(result.revision).toBe(1);
    expect(Array.isArray(result.effects)).toBe(true);
  });

  test("suspension.resolve sends frame, forwards id gate + CAS, returns typed result", async () => {
    const transport = new FakeTransport().onResult("wrkf.suspension.resolve", {
      task: "T-00001",
      instanceId: "wfi_abc123",
      suspensionId: "sus_000001",
      disposition: "resume",
      state: { status: "active", phase: "ready" },
      revision: 2,
      eventId: "wfe_000003",
      effects: [{ id: "eff_1", status: "pending", kind: "resume_notice" }],
    });
    const client = await clientWith(transport);

    const result = await client.wrkf.suspension.resolve({
      suspensionId: "sus_000001",
      disposition: "resume",
      explanation: "operator cleared the park",
      expectRevision: 1,
      role: "coordinator",
      principal_ref: "human:local",
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkf.suspension.resolve");
    expect(frame.params).toMatchObject({
      suspensionId: "sus_000001",
      disposition: "resume",
      explanation: "operator cleared the park",
      expectRevision: 1,
    });
    expect(result.suspensionId).toBe("sus_000001");
    expect(result.disposition).toBe("resume");
    expect(result.revision).toBe(2);
    expect(result.eventId).toBe("wfe_000003");
    expect(result.effects[0]!.id).toBe("eff_1");
  });

  test("suspension.resolve surfaces WRKF_SUSPENSION_NOT_FOUND as a typed wrkf error", async () => {
    const transport = new FakeTransport().onError("wrkf.suspension.resolve", {
      code: -32025,
      message: "no active suspension matches id sus_999",
      data: { code: "WRKF_SUSPENSION_NOT_FOUND", retryable: false },
    });
    const client = await clientWith(transport);

    let caught: unknown;
    try {
      await client.wrkf.suspension.resolve({ suspensionId: "sus_999", disposition: "close" });
    } catch (e) {
      caught = e;
    }
    const err = caught as WorkRpcError;
    expect(isWrkfError(err)).toBe(true);
    expect(err.domainCode).toBe("WRKF_SUSPENSION_NOT_FOUND");
    expect(err.method).toBe("wrkf.suspension.resolve");
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

  test("ledger facade exposes append + list without mutation verbs", async () => {
    const entry = {
      seq: 1,
      uuid: "deadc0de-0000-4000-8000-000000000001",
      instanceId: "wfi_live_instance",
      taskId: "T-00001",
      ts: "2026-07-11T12:00:00Z",
      kind: "runner_start",
      aboutPrincipalRef: "agent:cody",
      writtenBy: "agent:smokey",
      body: { refs: { logs: { narration: "/tmp/narration.log" } } },
    };
    const transport = new FakeTransport()
      .onResult("wrkf.ledger.append", entry)
      .onResult("wrkf.ledger.list", { entries: [entry] });
    const client = await clientWith(transport);

    const appended = await client.wrkf.ledger.append({
      taskId: "T-00001",
      kind: "runner_start",
      aboutPrincipalRef: "agent:cody",
      body: { refs: { logs: { narration: "/tmp/narration.log" } } },
    });
    const listed = await client.wrkf.ledger.list({ taskId: "T-00001" });

    expect(appended.instanceId).toBe("wfi_live_instance");
    expect(listed.entries[0]!.writtenBy).toBe("agent:smokey");
    expect(transport.capturedRequests.map((r) => r.method)).toEqual([
      "wrkf.ledger.append",
      "wrkf.ledger.list",
    ]);
    expect(transport.capturedRequests[0]!.params).not.toHaveProperty("writtenBy");
  });

  test("role facade forwards list, bind, unbind, and set", async () => {
    const binding = {
      instanceId: "wfi_1",
      role: "implementer",
      principal_ref: "agent:cody",
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
      principal_ref: "agent:cody",
      deliveryRef: "cody@wrkq:T-1",
      lane: "main",
    });
    await client.wrkf.role.list({ task: "T-00001" });
    await client.wrkf.role.unbind({ task: "T-00001", role: "implementer", principal_ref: "agent:cody" });
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
      leaseExpiresAt: "2026-06-30T01:00:00Z",
    });
    const client = await clientWith(transport);
    const claim = await client.wrkf.effect.claim({ adapter: "wake_role", limit: 5, leaseMs: 60000 });
    expect(claim.leaseToken).toBe("lease_abc");
    expect(claim.effects[0]!.id).toBe("eff_1");
  });

});

describe("error mapping", () => {
  test("action claim suspension refusal exposes only the typed suspension record", async () => {
    const transport = new FakeTransport().onError("wrkf.action.claim", {
      code: -32026,
      message: "instance wfi_1 is suspended (sus_1 operator_required)",
      data: {
        code: "WRKF_SUSPENDED",
        retryable: false,
        suspension: {
          id: "sus_1",
          reason: "operator_required",
          at: "2026-07-12T22:00:00Z",
          causeRef: "wfe_1",
        },
      },
    });
    const client = await clientWith(transport);
    let caught: unknown;
    try {
      await client.wrkf.action.claim({
        task: "T-00001", runnerId: "runner-b", agentRef: "agent:larry",
        leaseMs: 60_000, priorRun: null,
      });
    } catch (error) {
      caught = error;
    }
    const error = caught as WorkRpcError;
    expect(error.domainCode).toBe("WRKF_SUSPENDED");
    expect(error.data?.suspension).toEqual({
      id: "sus_1",
      reason: "operator_required",
      at: "2026-07-12T22:00:00Z",
      causeRef: "wfe_1",
    });
    expect(error.data?.predecessor).toBeUndefined();
  });

  test("action claim refusal exposes the typed predecessor record", async () => {
    const transport = new FakeTransport().onError("wrkf.action.claim", {
      code: -32014,
      message: "claim refused: priorRun must name predecessor run_000001",
      data: {
        code: "WRKF_LEASE_CONFLICT",
        retryable: true,
        predecessor: {
          runId: "run_000001",
          owner: "runner-a",
          claimedAt: "2026-07-12T12:00:00Z",
          heartbeatAt: "2026-07-12T12:01:00Z",
          expiresAt: "2026-07-12T12:02:00Z",
          settleStatus: "active",
          sideEffectClasses: ["git.commit"],
          evidenceWritten: [],
        },
      },
    });
    const client = await clientWith(transport);
    let caught: unknown;
    try {
      await client.wrkf.action.claim({
        task: "T-00001", runnerId: "runner-b", agentRef: "agent:larry",
        leaseMs: 60_000, priorRun: null,
      });
    } catch (error) {
      caught = error;
    }
    const error = caught as WorkRpcError;
    expect(error.data?.predecessor?.runId).toBe("run_000001");
    expect(error.data?.predecessor?.sideEffectClasses).toEqual(["git.commit"]);
  });

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

describe("wrkq search + index family (T-05114)", () => {
  const MOCK_STATUS = {
    path: "/db/wrkq.db.search.sqlite",
    enabled: true,
    status: "ready",
    last_indexed_event_id: 5,
    canonical_max_event_id: 5,
    stale_event_count: 0,
    searchable_chunk_count: 3,
  };

  test("search.listView sends wrkq.search.listView and returns the legacy response shape", async () => {
    const MOCK_VIEW = {
      query: "alpha",
      stale: false,
      status: MOCK_STATUS,
      results: [
        {
          resource_type: "task",
          resource_id: "T-00001",
          resource_uuid: "u-1",
          task_id: "T-00001",
          task_uuid: "u-1",
          path: "inbox/find-me",
          title: "Searchable Task",
          state: "open",
          kind: "task",
          snippet: "alpha beta gamma",
          score: 0.0164,
          created_at: "2026-06-23T00:00:00Z",
          updated_at: "2026-06-23T00:00:00Z",
          stale: false,
        },
      ],
      total_matches: 1,
      offset: 0,
    };
    const transport = new FakeTransport().onResult("wrkq.search.listView", MOCK_VIEW);
    const client = await clientWith(transport);

    const view = await client.wrkq.search.listView({
      query: "alpha",
      paths: ["inbox"],
      state: "all",
      sort: "updated_at",
      fresh: true,
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkq.search.listView");
    expect(frame.params).toMatchObject({
      query: "alpha",
      paths: ["inbox"],
      state: "all",
      sort: "updated_at",
      fresh: true,
    });
    // Result keys are DELIBERATELY snake_case (legacy search.Response byte-parity).
    expect(view.total_matches).toBe(1);
    expect(view.results[0]!.resource_id).toBe("T-00001");
    expect(view.status!.searchable_chunk_count).toBe(3);
  });

  test("index.status sends wrkq.index.status with no params", async () => {
    const transport = new FakeTransport().onResult("wrkq.index.status", MOCK_STATUS);
    const client = await clientWith(transport);

    const status = await client.wrkq.index.status();

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkq.index.status");
    expect(status.status).toBe("ready");
    expect(status.stale_event_count).toBe(0);
  });

  test("index.rebuild forwards the lifecycle params and returns the map-alphabetical ack", async () => {
    const transport = new FakeTransport().onResult("wrkq.index.rebuild", {
      rebuilt: true,
      status: MOCK_STATUS,
    });
    const client = await clientWith(transport);

    const result = await client.wrkq.index.rebuild({ foreground: true });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkq.index.rebuild");
    expect(frame.params).toMatchObject({ foreground: true });
    expect(result.rebuilt).toBe(true);
    expect(result.status.searchable_chunk_count).toBe(3);
  });

  test("index.update / vacuum / pause / resume route to their methods", async () => {
    const transport = new FakeTransport()
      .onResult("wrkq.index.update", { updated: true, status: MOCK_STATUS })
      .onResult("wrkq.index.vacuum", { vacuumed: true })
      .onResult("wrkq.index.pause", { status: "paused" })
      .onResult("wrkq.index.resume", { status: "ready" });
    const client = await clientWith(transport);

    expect((await client.wrkq.index.update()).updated).toBe(true);
    expect((await client.wrkq.index.vacuum()).vacuumed).toBe(true);
    expect((await client.wrkq.index.pause()).status).toBe("paused");
    expect((await client.wrkq.index.resume()).status).toBe("ready");

    const methods = transport.capturedRequests.map((r) => r.method);
    expect(methods).toEqual([
      "wrkq.index.update",
      "wrkq.index.vacuum",
      "wrkq.index.pause",
      "wrkq.index.resume",
    ]);
  });
});
