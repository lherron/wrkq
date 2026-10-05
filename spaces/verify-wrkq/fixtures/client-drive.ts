// verify-wrkq feature 10: drive the published @wrkq/client against a wv scratch.
// Run from a dir with its own package.json where `bun add @wrkq/client@latest` installed it, with `wv env` evaluated.
import { createClient, isWorkRpcError } from "@wrkq/client";
import type { WrkqTaskCreateParams } from "@wrkq/client";

const dbLocator = process.env.WRKQ_DB;
const projectRoot = process.env.WRKQ_PROJECT_ROOT;
if (!dbLocator || !projectRoot) throw new Error("run after: eval \"$(wv env <name>)\"");

const client = await createClient({
  command: "wrkq",
  dbLocator,
  principalRef: "agent:clod",
  clientInfo: { name: "verify-wrkq", version: "0" },
});
// The session principalRef applies over rpc:// since T-10328 (037fe66), so no per-call principalRef here.
const req: WrkqTaskCreateParams = {
  title: "WV client task",
  path: `${projectRoot}/inbox/wv-client`,
  idempotencyKey: `wv-client:${dbLocator}`,
};
const created = await client.wrkq.task.create(req);
console.log(JSON.stringify({ created: { id: created.id, state: created.state, etag: created.etag, by: created.createdByPrincipalRef } }));
const again = await client.wrkq.task.create(req);
console.log(JSON.stringify({ replay_same_id: again.id === created.id }));
try {
  await client.wrkq.task.update({ task: created.id, expectEtag: 999, patch: { priority: 1 } });
  console.log(JSON.stringify({ stale_update: "accepted (unexpected)" }));
} catch (e) {
  console.log(JSON.stringify({ stale_update_refused: isWorkRpcError(e) ? e.domainCode : String(e) }));
}
// A replayed create returns the original response, so its etag can be stale on a rerun; read the current one.
const current = await client.wrkq.task.show({ task: created.id });
const updated = await client.wrkq.task.update({ task: created.id, expectEtag: current.etag, patch: { priority: 1 } });
console.log(JSON.stringify({ cas_update: { priority: updated.priority, etag: updated.etag, from: current.etag, by: updated.updatedByPrincipalRef } }));
await client.close();
