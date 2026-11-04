import { expect, test } from "bun:test";
import { createClient } from "../src/client";
import { FakeTransport } from "../src/testing/fake-transport";
import type { WrkqCommitWaitParams, WrkqCommitWait } from "../src/wrkq/commit";

test("commit wait forwards optional timeout and opaque tokens unchanged", async () => {
  const result: WrkqCommitWait = { token: "incarnation:sequence", changed: false };
  const transport = new FakeTransport().onResult("wrkq.commit.wait", result);
  const client = await createClient({ transport, autoInitialize: false });
  const params: WrkqCommitWaitParams = { since: "opaque-input", timeoutMs: 1234 };
  expect(await client.wrkq.commit.wait(params)).toEqual(result);
  expect(await client.wrkq.commit.wait()).toEqual(result);
  expect(transport.capturedRequests.map(({ method, params }) => ({ method, params }))).toEqual([
    { method: "wrkq.commit.wait", params }, { method: "wrkq.commit.wait", params: {} },
  ]);
});
