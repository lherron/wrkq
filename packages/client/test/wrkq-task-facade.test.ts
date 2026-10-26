/**
 * wrkq-task-facade.test.ts — wrkq task facade: create/update/claim/move/list/restore/copy/find.
 *
 * Unit tests over the in-memory FakeTransport; no subprocess, no I/O.
 * Contract: docs/wrkq-wrkf-rpc.md §4, §5, §6, §7.
 */

import { describe, expect, test } from "bun:test";
import { FakeTransport } from "../src/testing/fake-transport";
import type {
  WrkqFindListViewParams,
  WrkqTaskClaimParams,
  WrkqTaskClaimValidateParams,
  WrkqTaskCopyParams,
  WrkqTaskCopyResult,
  WrkqTaskCreateParams,
  WrkqTaskListParams,
  WrkqTaskMoveParams,
  WrkqTaskReleaseParams,
  WrkqTaskRestoreParams,
  WrkqTaskUpdateParams,
} from "../src/wrkq/task";
import { MOCK_TASK, clientWith } from "./support/client-fixtures";

describe("wrkq task facade", () => {
  test("task facade sends each task method with its params unchanged", async () => {
    // Typed as WrkqTaskCopyResult so tsc pins the snake_case result keys (legacy copyResult byte-parity).
    const MOCK_COPY_RESULT: WrkqTaskCopyResult = {
      source_id: "T-00001",
      source_uuid: "u-1",
      dest_id: "T-00009",
      dest_uuid: "u-9",
      dest_path: "done/my-task",
      attachments_copied: 2,
      with_files: true,
    };
    const transport = new FakeTransport();
    for (const method of [
      "create", "update", "claim", "claimValidate", "release", "move", "list", "restore", "findListView",
    ]) {
      transport.onResult(`wrkq.task.${method}`, MOCK_TASK);
    }
    transport.onResult("wrkq.task.copy", MOCK_COPY_RESULT);
    const client = await clientWith(transport);
    const scope = "agent:cody:project:wrkq:task:T-00001";
    const fence = {
      task: "T-00001",
      principalRef: "agent:cody",
      scope,
      claimGeneration: 4,
      claimToken: "secret-once",
    } satisfies WrkqTaskClaimValidateParams & WrkqTaskReleaseParams;

    const create = {
      title: "my task",
      kind: "task",
      state: "open",
      riskClass: "medium",
      requesterScopeRef: "mable@wrkq:primary",
      idempotencyKey: "k1",
    } satisfies WrkqTaskCreateParams;
    const update = {
      task: "T-00001",
      patch: { state: "in_progress", riskClass: "high" },
      expectEtag: 2,
      idempotencyKey: "u1",
    } satisfies WrkqTaskUpdateParams;
    const claim = { task: "T-00001", principalRef: "agent:cody", scope, takeOver: true } satisfies WrkqTaskClaimParams;
    const move = { task: "T-00001", targetPath: "done", expectEtag: 2 } satisfies WrkqTaskMoveParams;
    const list = { state: "open", summary: true } satisfies WrkqTaskListParams;
    const restore = {
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
    } satisfies WrkqTaskRestoreParams;
    const copy = {
      source: "T-00001",
      destination: "done",
      overwrite: true,
      withAttachments: true,
      expectEtag: 2,
      actor: "agent:larry",
      idempotencyKey: "cp1",
    } satisfies WrkqTaskCopyParams;
    const findListView = { type: "t", state: "all", labels: ["alpha", "beta"], sort: "id" } satisfies WrkqFindListViewParams;

    await client.wrkq.task.create(create);
    await client.wrkq.task.update(update);
    await client.wrkq.task.claim(claim);
    await client.wrkq.task.claimValidate(fence);
    await client.wrkq.task.release(fence);
    await client.wrkq.task.move(move);
    await client.wrkq.task.list(list);
    await client.wrkq.task.restore(restore);
    await client.wrkq.task.copy(copy);
    await client.wrkq.task.findListView(findListView);

    expect(transport.capturedRequests.map(({ method, params }) => ({ method, params }))).toEqual([
      { method: "wrkq.task.create", params: create },
      { method: "wrkq.task.update", params: update },
      { method: "wrkq.task.claim", params: claim },
      { method: "wrkq.task.claimValidate", params: fence },
      { method: "wrkq.task.release", params: fence },
      { method: "wrkq.task.move", params: move },
      { method: "wrkq.task.list", params: list },
      { method: "wrkq.task.restore", params: restore },
      { method: "wrkq.task.copy", params: copy },
      { method: "wrkq.task.findListView", params: findListView },
    ]);
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
});
