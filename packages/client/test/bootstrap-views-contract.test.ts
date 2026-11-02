import { expect, test } from "bun:test";
import { createClient } from "../src/client";
import { FakeTransport } from "../src/testing/fake-transport";
import type {
  WrkqPromiseListViewParams,
  WrkqWorkflowInstancesViewParams,
} from "../src/index";

test("bootstrap views forward defaults, filters and opaque cursors unchanged", async () => {
  const promises = { items: [], nextCursor: "promise-page" };
  const instances = { items: [], nextCursor: "instance-page" };
  const transport = new FakeTransport()
    .onResult("wrkq.promise.listView", promises)
    .onResult("wrkq.workflow.instancesView", instances);
  const client = await createClient({ transport, autoInitialize: false });
  const p = {
    states: ["resolved", "abandoned"], limit: 25, cursor: "opaque-p",
  } satisfies WrkqPromiseListViewParams;
  const w = {
    statuses: ["closed"], tasks: ["T-00001"], limit: 25, cursor: "opaque-w",
  } satisfies WrkqWorkflowInstancesViewParams;
  expect(await client.wrkq.promise.listView()).toEqual(promises);
  expect(await client.wrkq.promise.listView(p)).toEqual(promises);
  expect(await client.wrkq.workflow.instancesView()).toEqual(instances);
  expect(await client.wrkq.workflow.instancesView(w)).toEqual(instances);
  expect(transport.capturedRequests.map(({ method, params }) => ({ method, params }))).toEqual([
    { method: "wrkq.promise.listView", params: {} },
    { method: "wrkq.promise.listView", params: p },
    { method: "wrkq.workflow.instancesView", params: {} },
    { method: "wrkq.workflow.instancesView", params: w },
  ]);
});
