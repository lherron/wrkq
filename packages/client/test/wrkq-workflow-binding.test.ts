/**
 * wrkq-workflow-binding.test.ts — wrkq task-workflow binding facade (wrkq.workflow.*).
 *
 * Unit tests over the in-memory FakeTransport; no subprocess, no I/O.
 * Contract: docs/wrkq-wrkf-rpc.md §4, §5, §6, §7.
 */

import { describe, expect, test } from "bun:test";
import { FakeTransport } from "../src/testing/fake-transport";
import type {
  WrkqWorkflowAttachParams,
  WrkqWorkflowInstancesParams,
  WrkqWorkflowSyncMetaParams,
  WrkqWorkflowTimelineParams,
} from "../src/wrkq/workflow-binding";
import { MOCK_TASK, clientWith } from "./support/client-fixtures";

describe("wrkq task-workflow binding", () => {
  test("wrkq.workflow facade sends syncMeta/attach/timeline/instances in the wrkq namespace with params unchanged", async () => {
    const transport = new FakeTransport()
      .onResult("wrkq.workflow.syncMeta", { synced: 0 })
      .onResult("wrkq.workflow.attach", { task: MOCK_TASK, instance: { id: "wfi_x", revision: 0 }, attached: true })
      .onResult("wrkq.workflow.timeline", { events: [] })
      .onResult("wrkq.workflow.instances", { instances: [] });
    const client = await clientWith(transport);

    const syncMeta = { actor: "agent:cody" } satisfies WrkqWorkflowSyncMetaParams;
    // attachDiscontinued crosses the wrkq producer verbatim (T-06357 override).
    const attach = { task: "T-00001", workflow: "f@1", attachDiscontinued: true } satisfies WrkqWorkflowAttachParams;
    const timeline = { task: "T-00001" } satisfies WrkqWorkflowTimelineParams;
    const instances = { task: "T-00001" } satisfies WrkqWorkflowInstancesParams;

    await client.wrkq.workflow.syncMeta(syncMeta);
    await client.wrkq.workflow.attach(attach);
    const events = await client.wrkq.workflow.timeline(timeline);
    const listed = await client.wrkq.workflow.instances(instances);

    // Type-only: the timeline and instances envelopes keep their stable keys.
    void [events.events, listed.instances];
    expect(transport.capturedRequests.map(({ method, params }) => ({ method, params }))).toEqual([
      { method: "wrkq.workflow.syncMeta", params: syncMeta },
      { method: "wrkq.workflow.attach", params: attach },
      { method: "wrkq.workflow.timeline", params: timeline },
      { method: "wrkq.workflow.instances", params: instances },
    ]);
  });
});
