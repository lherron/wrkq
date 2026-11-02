import { expect, test } from "bun:test";
import { createClient } from "../src/client";
import { FakeTransport } from "../src/testing/fake-transport";
import type { WrkqExt, WrkqExtListViewParams, WrkqExtPutParams } from "../src/wrkq/ext";
import type { WrkqTaskCreateParams, WrkqTaskUpdateParams } from "../src/wrkq/task";

test("extension facade forwards fences, public subjects, filters and opaque cursor unchanged", async () => {
  const ext: WrkqExt = {
    subject: "T-12364", ns: "acp", kind: "request", id: "main", body: { rev: 1 },
    etag: 1, createdAt: "2026-11-02T12:00:00Z", updatedAt: "2026-11-02T12:00:00Z",
    updatedByPrincipalRef: "agent:faber", updatedByScopeRef: "agent:faber:project:wrkq:task:T-12364",
  };
  const item = { ...ext, subject: { type: "task" as const, id: ext.subject, state: "open" } };
  const transport = new FakeTransport()
    .onResult("wrkq.ext.put", { ext, created: true })
    .onResult("wrkq.ext.show", { ext })
    .onResult("wrkq.ext.listView", { items: [item], nextCursor: "opaque-next" })
    .onResult("wrkq.ext.delete", { deleted: true });
  const client = await createClient({ transport, autoInitialize: false });
  const key = { subject: { task: ext.subject }, ns: ext.ns, kind: ext.kind, id: ext.id };
  const put: WrkqExtPutParams = { ...key, body: ext.body, ifMatch: 0, principalRef: "agent:faber" };
  expect(await client.wrkq.ext.put(put)).toEqual({ ext, created: true });
  expect(await client.wrkq.ext.show(key)).toEqual({ ext });
  const filter: WrkqExtListViewParams = { subject: key.subject, ns: "acp", kind: "request", subjectStates: ["open"], limit: 2, cursor: "opaque-input" };
  expect(await client.wrkq.ext.listView(filter)).toEqual({ items: [item], nextCursor: "opaque-next" });
  await client.wrkq.ext.listView();
  const deletion = { ...key, ifMatch: 1 };
  expect(await client.wrkq.ext.delete(deletion)).toEqual({ deleted: true });
  expect(transport.capturedRequests.map(({ method, params }) => ({ method, params }))).toEqual([
    { method: "wrkq.ext.put", params: put }, { method: "wrkq.ext.show", params: key },
    { method: "wrkq.ext.listView", params: filter }, { method: "wrkq.ext.listView", params: {} },
    { method: "wrkq.ext.delete", params: deletion },
  ]);
});

test("workstream create and update preserve covers and the task fence", async () => {
  const covers = { container: "P-00061", tasks: ["T-12364"] };
  const transport = new FakeTransport()
    .onResult("wrkq.task.create", { id: "T-12365", kind: "workstream", covers })
    .onResult("wrkq.task.update", { id: "T-12365", kind: "workstream", covers });
  const client = await createClient({ transport, autoInitialize: false });
  const create: WrkqTaskCreateParams = { title: "hcs context", kind: "workstream", covers };
  const update: WrkqTaskUpdateParams = { task: "T-12365", patch: { covers }, expectEtag: 3 };
  expect((await client.wrkq.task.create(create)).covers).toEqual(covers);
  expect((await client.wrkq.task.update(update)).covers).toEqual(covers);
  expect(transport.capturedRequests.map(({ params }) => params)).toEqual([create, update]);
});
