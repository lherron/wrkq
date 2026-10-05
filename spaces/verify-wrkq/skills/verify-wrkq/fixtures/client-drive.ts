// verify-wrkq feature 10: drive the published @wrkq/client against a wv scratch.
// Run from a dir where `bun add @wrkq/client@latest` installed it, with `wv env` evaluated.
import { createClient, WorkRpcError } from "@wrkq/client";

const client = await createClient({
  command: "wrkq",
  dbLocator: process.env.WRKQ_DB!,
  principalRef: "agent:clod",
  clientInfo: { name: "verify-wrkq", version: "0" },
});
// The session principalRef applies over rpc:// since T-10328 (037fe66), so no per-call principalRef here.
const req = {
  title: "WV client task",
  path: `${process.env.WRKQ_PROJECT_ROOT}/inbox/wv-client`,
  idempotencyKey: `wv-client:${process.env.WRKQ_DB}`,
};
const created: any = await client.wrkq.task.create(req);
const t: any = created.task ?? created;
console.log(JSON.stringify({ created: { id: t.id, state: t.state, etag: t.etag, by: t.createdByPrincipalRef } }));
const again: any = await client.wrkq.task.create(req as any);
console.log(JSON.stringify({ replay_same_id: (again.task ?? again).id === t.id }));
try {
  await client.wrkq.task.update({ task: t.id, expectEtag: 999, patch: { priority: 1 } });
  console.log(JSON.stringify({ stale_update: "accepted (unexpected)" }));
} catch (e) {
  const err = e as WorkRpcError;
  console.log(JSON.stringify({ stale_update_refused: (err as any).domainCode ?? (err as any).code ?? String(e) }));
}
// A replayed create returns the original response, so its etag can be stale on a rerun; read the current one.
const cur: any = await client.wrkq.task.show({ task: t.id });
const curEtag = (cur.task ?? cur).etag;
const ok: any = await client.wrkq.task.update({ task: t.id, expectEtag: curEtag, patch: { priority: 1 } });
const u: any = ok.task ?? ok;
console.log(JSON.stringify({ cas_update: { priority: u.priority, etag: u.etag, from: curEtag, by: u.updatedByPrincipalRef } }));
await (client as any).close?.();
