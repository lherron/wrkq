import { describe, expect, test } from "bun:test";
import { createClient } from "../src/client";
import { FakeTransport } from "../src/testing/fake-transport";
import type { WrkqPromise } from "../src/wrkq/types";

const PROMISE: WrkqPromise = {
  uuid: "promise-uuid",
  id: "PR-00001",
  ownerPrincipalRef: "agent:cody",
  subject: "Review the rollout",
  reviewQuestion: "What changed?",
  subjectRef: {
    type: "task",
    uuid: "task-uuid",
    id: "T-00001",
    path: "wrkq/rollout",
  },
  reviewAt: "2099-01-01T00:00:00Z",
  ready: false,
  state: "open",
  meta: {},
  etag: 2,
  createdAt: "2026-08-23T00:00:00Z",
  updatedAt: "2026-08-23T00:00:00Z",
  createdByPrincipalRef: "agent:cody",
  updatedByPrincipalRef: "agent:cody",
};

describe("wrkq.promise facade", () => {
  test("forwards the complete promise RPC family with typed results", async () => {
    const transport = new FakeTransport()
      .onResult("wrkq.promise.add", PROMISE)
      .onResult("wrkq.promise.show", PROMISE)
      .onResult("wrkq.promise.list", { items: [PROMISE] })
      .onResult("wrkq.promise.ready", { items: [PROMISE] })
      .onResult("wrkq.promise.edit", PROMISE)
      .onResult("wrkq.promise.renew", PROMISE)
      .onResult("wrkq.promise.resolve", PROMISE)
      .onResult("wrkq.promise.abandon", PROMISE)
      .onResult("wrkq.promise.attach", PROMISE)
      .onResult("wrkq.promise.detach", PROMISE)
      .onResult("wrkq.promise.delete", PROMISE);
    const client = await createClient({ transport, autoInitialize: false });

    const add = {
      ownerPrincipalRef: "agent:cody",
      subject: "Review the rollout",
      reviewIn: "7d",
      task: "T-00001",
      principalRef: "agent:cody",
    };
    const edit = { promise: PROMISE.id, reviewQuestion: "Still relevant?", ifMatch: 2 };
    const renew = { promise: PROMISE.id, reviewAt: "2099-02-01T00:00:00Z", note: "yes", ifMatch: 2 };
    const resolve = { promise: PROMISE.id, note: "done", ifMatch: 3 };
    const abandon = { promise: PROMISE.id, note: "obsolete", ifMatch: 3 };
    const attach = { promise: PROMISE.id, container: "wrkq/promises", ifMatch: 2 };
    const detach = { promise: PROMISE.id, ifMatch: 3 };
    const remove = { promise: PROMISE.id, mode: "purge" as const, ifMatch: 4 };

    expect((await client.wrkq.promise.add(add)).id).toBe(PROMISE.id);
    await client.wrkq.promise.show({ promise: PROMISE.id });
    expect((await client.wrkq.promise.list({ task: "T-00001" })).items).toHaveLength(1);
    await client.wrkq.promise.ready({
      ownerPrincipalRef: "agent:cody",
      project: "wrkq",
      includeGlobal: true,
    });
    await client.wrkq.promise.edit(edit);
    await client.wrkq.promise.renew(renew);
    await client.wrkq.promise.resolve(resolve);
    await client.wrkq.promise.abandon(abandon);
    await client.wrkq.promise.attach(attach);
    await client.wrkq.promise.detach(detach);
    await client.wrkq.promise.delete(remove);

    expect(transport.capturedRequests.map(({ method, params }) => ({ method, params }))).toEqual([
      { method: "wrkq.promise.add", params: add },
      { method: "wrkq.promise.show", params: { promise: PROMISE.id } },
      // Subject-scoped list intentionally forwards no owner; the server returns all owners.
      { method: "wrkq.promise.list", params: { task: "T-00001" } },
      {
        method: "wrkq.promise.ready",
        params: { ownerPrincipalRef: "agent:cody", project: "wrkq", includeGlobal: true },
      },
      { method: "wrkq.promise.edit", params: edit },
      { method: "wrkq.promise.renew", params: renew },
      { method: "wrkq.promise.resolve", params: resolve },
      { method: "wrkq.promise.abandon", params: abandon },
      { method: "wrkq.promise.attach", params: attach },
      { method: "wrkq.promise.detach", params: detach },
      { method: "wrkq.promise.delete", params: remove },
    ]);
  });

  test("list and ready default to empty parameter objects", async () => {
    const transport = new FakeTransport()
      .onResult("wrkq.promise.list", { items: [] })
      .onResult("wrkq.promise.ready", { items: [] });
    const client = await createClient({ transport, autoInitialize: false });
    await client.wrkq.promise.list();
    await client.wrkq.promise.ready();
    expect(transport.capturedRequests.map((request) => request.params)).toEqual([{}, {}]);
  });
});
