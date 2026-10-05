/**
 * wrkq-task-facade.test.ts — wrkq task facade: create/update/claim/move/list/restore/copy/find.
 *
 * Unit tests over the in-memory FakeTransport; no subprocess, no I/O.
 * Contract: docs/wrkq-wrkf-rpc.md §4, §5, §6, §7.
 */

import { describe, expect, test } from "bun:test";
import { FakeTransport } from "../src/testing/fake-transport";
import type { WrkqTaskCopyResult, WrkqTaskCreateParams } from "../src/wrkq/task";
import { MOCK_TASK, clientWith } from "./support/client-fixtures";

describe("wrkq task facade", () => {
  test("task.create sends wrkq.task.create and returns typed WrkqTask", async () => {
    const transport = new FakeTransport().onResult("wrkq.task.create", MOCK_TASK);
    const client = await clientWith(transport);

    const task = await client.wrkq.task.create({
      title: "my task",
      kind: "task",
      state: "open",
      riskClass: "medium",
      requesterScopeRef: "mable@wrkq:primary",
      idempotencyKey: "k1",
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkq.task.create");
    expect(frame.params).toMatchObject({
      title: "my task",
      riskClass: "medium",
      requesterScopeRef: "mable@wrkq:primary",
      idempotencyKey: "k1",
    });
    expect(task.id).toBe("T-00001");
    expect(task.riskClass).toBe("medium");
    expect(task.etag).toBe(2);
    expect(task.assigneePrincipalRef).toBe("agent:larry");
    expect(task.requesterPrincipalRef).toBe("agent:mable");
    expect(task.requesterScopeRef).toBe("agent:mable:project:wrkq:role:primary");
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

  test("task claim authority methods preserve the exact fencing tuple", async () => {
    const claim = {
      task: "T-00001",
      claimedBy: "agent:cody",
      claimedScope: "agent:cody:project:wrkq:task:T-00001",
      claimedNode: "max3",
      claimedAt: "2026-07-20T00:00:00Z",
      claimGeneration: 4,
      claimToken: "secret-once",
    };
    const transport = new FakeTransport()
      .onResult("wrkq.task.claim", claim)
      .onResult("wrkq.task.claimValidate", { ...claim, claimToken: undefined })
      .onResult("wrkq.task.release", { ...claim, claimToken: undefined });
    const client = await clientWith(transport);
    const scope = "agent:cody:project:wrkq:task:T-00001";

    await client.wrkq.task.claim({
      task: "T-00001",
      principalRef: "agent:cody",
      scope,
      takeOver: true,
    });
    await client.wrkq.task.claimValidate({
      task: "T-00001",
      principalRef: "agent:cody",
      scope,
      claimGeneration: 4,
      claimToken: "secret-once",
    });
    await client.wrkq.task.release({
      task: "T-00001",
      principalRef: "agent:cody",
      scope,
      claimGeneration: 4,
      claimToken: "secret-once",
    });

    expect(transport.capturedRequests.map((frame) => frame.method)).toEqual([
      "wrkq.task.claim",
      "wrkq.task.claimValidate",
      "wrkq.task.release",
    ]);
    expect(transport.capturedRequests[1]!.params).toEqual({
      task: "T-00001",
      principalRef: "agent:cody",
      scope,
      claimGeneration: 4,
      claimToken: "secret-once",
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

  test("T-05381 removes the legacy actor admin facade", async () => {
    const client = await clientWith(new FakeTransport());
    expect((client.wrkq.admin as Record<string, unknown>).legacyActor).toBeUndefined();
  });
});

describe("wrkq exact label filter read surfaces", () => {
  test("task.findListView forwards repeatable exact-label params", async () => {
    const transport = new FakeTransport().onResult("wrkq.task.findListView", {
      items: [
        {
          type: "task",
          uuid: "u-1",
          id: "T-00001",
          slug: "my-task",
          title: "my task",
          path: "inbox/my-task",
          state: "open",
          created_at: "2026-06-30T00:00:00Z",
          updated_at: "2026-06-30T00:00:00Z",
          etag: 2,
        },
      ],
    });
    const client = await clientWith(transport);

    const view = await client.wrkq.task.findListView({
      type: "t",
      state: "all",
      labels: ["alpha", "beta"],
      sort: "id",
    });

    const frame = transport.capturedRequests[0]!;
    expect(frame.method).toBe("wrkq.task.findListView");
    expect(frame.params).toMatchObject({
      type: "t",
      state: "all",
      labels: ["alpha", "beta"],
      sort: "id",
    });
    expect(view.items[0]!.id).toBe("T-00001");
  });
});
