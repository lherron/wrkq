/**
 * wrkq-workflow-binding.test.ts — wrkq task-workflow binding facade (wrkq.workflow.*).
 *
 * Unit tests over the in-memory FakeTransport; no subprocess, no I/O.
 * Contract: docs/wrkq-wrkf-rpc.md §4, §5, §6, §7.
 */

import { describe, expect, test } from "bun:test";
import { isWorkRpcError, isWrkqError } from "../src/errors";
import { FakeTransport } from "../src/testing/fake-transport";
import { MOCK_TASK, clientWith, rejectionOf } from "./support/client-fixtures";

describe("wrkq task-workflow binding", () => {
  test("workflow.syncMeta stays in the task-owned wrkq namespace", async () => {
    const transport = new FakeTransport().onResult("wrkq.workflow.syncMeta", { synced: 3 });
    const client = await clientWith(transport);
    const result = await client.wrkq.workflow.syncMeta({ actor: "agent:cody" });
    expect(transport.capturedRequests[0]).toMatchObject({
      method: "wrkq.workflow.syncMeta",
      params: { actor: "agent:cody" },
    });
    expect(result.synced).toBe(3);
  });

  test("workflow.attach is a wrkq verb", async () => {
    const transport = new FakeTransport().onResult("wrkq.workflow.attach", {
      task: MOCK_TASK,
      instance: { id: "wfi_x", revision: 0 },
      attached: true,
    });
    const client = await clientWith(transport);
    const res = await client.wrkq.workflow.attach({
      task: "T-00001",
      workflow: "f@1",
      attachDiscontinued: true,
    });
    expect(transport.capturedRequests[0]!.method).toBe("wrkq.workflow.attach");
    expect(transport.capturedRequests[0]!.params).toMatchObject({ attachDiscontinued: true });
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

  test("workflow.instances preserves the stable list envelope and typed not-found errors", async () => {
    const instance = {
      id: "wfi_x",
      taskRef: "wrkq:T-00001",
      templateId: "f",
      templateVersion: "1",
      templateHash: "sha256:abc",
      status: "active",
      revision: 0,
      taskDocEtag: "1",
      taskDocHash: "sha256:def",
      createdAt: "2026-07-24T00:00:00Z",
      updatedAt: "2026-07-24T00:00:00Z",
      suspension: null,
    };
    const transport = new FakeTransport().onResult("wrkq.workflow.instances", {
      instances: [instance],
    });
    const client = await clientWith(transport);
    const result = await client.wrkq.workflow.instances({ task: "T-00001" });
    expect(transport.capturedRequests[0]!.method).toBe("wrkq.workflow.instances");
    expect(transport.capturedRequests[0]!.params).toEqual({ task: "T-00001" });
    expect(result.instances).toEqual([instance]);

    const missingTransport = new FakeTransport().onError("wrkq.workflow.instances", {
      code: -32004,
      message: "task not found: T-99999",
      data: { code: "WRKQ_NOT_FOUND", retryable: false, ref: "T-99999", kind: "task" },
    });
    const missingClient = await clientWith(missingTransport);
    const error = await rejectionOf(missingClient.wrkq.workflow.instances({ task: "T-99999" }));
    expect(isWorkRpcError(error)).toBe(true);
    expect(isWrkqError(error)).toBe(true);
    expect(error.domainCode).toBe("WRKQ_NOT_FOUND");
    expect(error.retryable).toBe(false);
  });
});
