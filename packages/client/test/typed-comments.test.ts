import { describe, expect, test } from "bun:test";
import { createClient } from "../src/client";
import { FakeTransport } from "../src/testing/fake-transport";
import type { WrkqComment, WrkqCommentAddParams, WrkqCommentListParams } from "../src/wrkq/types";

const DIGEST_COMMENT: WrkqComment = {
  uuid: "comment-u-1",
  id: "C-00001",
  container: "P-00001",
  kind: "digest",
  body: "campaign digest",
  meta: { event_log_id: 913 },
  etag: 1,
  createdAt: "2026-07-22T00:00:00Z",
};

describe("typed container comments", () => {
  test("facade forwards add/list selectors, kind, and meta without translation", async () => {
    const transport = new FakeTransport()
      .onResult("wrkq.comment.add", DIGEST_COMMENT)
      .onResult("wrkq.comment.list", { items: [DIGEST_COMMENT] });
    const client = await createClient({ transport, autoInitialize: false });
    const add: WrkqCommentAddParams = {
      container: "campaigns/release",
      kind: "digest",
      body: "campaign digest",
      meta: { event_log_id: 913 },
    };
    const list: WrkqCommentListParams = { container: "campaigns/release" };

    const created = await client.wrkq.comment.add(add);
    const listed = await client.wrkq.comment.list(list);

    expect(transport.capturedRequests.map((request) => ({ method: request.method, params: request.params }))).toEqual([
      { method: "wrkq.comment.add", params: add },
      { method: "wrkq.comment.list", params: list },
    ]);
    expect(created).toEqual(DIGEST_COMMENT);
    expect(listed.items).toEqual([DIGEST_COMMENT]);
  });
});
