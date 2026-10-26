/**
 * error-mapping.test.ts — JSON-RPC error frames surface as typed WorkRpcError values.
 *
 * Unit tests over the in-memory FakeTransport; no subprocess, no I/O.
 * Contract: docs/wrkq-wrkf-rpc.md §4, §5, §6, §7.
 */

import { describe, expect, test } from "bun:test";
import { WorkRpcError, isWorkRpcError, isWrkfError, isWrkqError } from "../src/errors";
import { FakeTransport } from "../src/testing/fake-transport";
import { clientWith, rejectionOf } from "./support/client-fixtures";
import type { WrkfActionClaimPredecessor } from "../src/wrkf/action";
import type { WrkfSuspension } from "../src/wrkf/instance";

describe("error mapping", () => {
  test("domain error frame surfaces as WorkRpcError with method + requestId", async () => {
    const transport = new FakeTransport().onError("wrkf.transition.apply", {
      code: -32009,
      message: "workflow revision mismatch",
      data: {
        code: "WRKF_STALE_REVISION",
        retryable: true,
        expectedRevision: 3,
        actualRevision: 4,
      },
    });
    const client = await clientWith(transport);

    const err = await rejectionOf(client.wrkf.transition.apply({ task: "T-00001", transition: "plan_ready", expectRevision: 3 }));

    expect(err).toBeInstanceOf(WorkRpcError);
    expect(err.domainCode).toBe("WRKF_STALE_REVISION");
    expect(err.rpcCode).toBe(-32009);
    expect(err.retryable).toBe(true);
    expect(err.method).toBe("wrkf.transition.apply");
    expect(err.requestId).toBe(transport.capturedRequests.at(-1)!.id);
    expect(err.data?.expectedRevision).toBe(3);
    // Type-only: claim refusals carry typed predecessor/suspension records on data (checked by typecheck).
    void ([err.data?.predecessor, err.data?.suspension] satisfies [
      WrkfActionClaimPredecessor | undefined,
      WrkfSuspension | undefined,
    ]);

    expect(isWorkRpcError(err)).toBe(true);
    expect(isWrkfError(err)).toBe(true);
    expect(isWrkqError(err)).toBe(false);
  });

  test("WRKQ_* domain error classified by isWrkqError", async () => {
    const transport = new FakeTransport().onError("wrkq.task.update", {
      code: -32021,
      message: "stale etag",
      data: { code: "WRKQ_CONFLICT", retryable: true, currentEtag: 7 },
    });
    const client = await clientWith(transport);
    const err = await rejectionOf(client.wrkq.task.update({ task: "T-1", patch: {}, expectEtag: 2 }));
    expect(isWrkqError(err)).toBe(true);
    expect(isWrkfError(err)).toBe(false);
    expect(err.domainCode).toBe("WRKQ_CONFLICT");
  });

  test("protocol error (method-not-found) has no domainCode", async () => {
    const transport = new FakeTransport(); // no handlers → -32601
    const client = await clientWith(transport);
    const err = await rejectionOf(client.call("wrkf.bogus.method"));
    expect(isWorkRpcError(err)).toBe(true);
    expect(err.rpcCode).toBe(-32601);
    expect(err.domainCode).toBeUndefined();
    expect(isWrkqError(err)).toBe(false);
    expect(isWrkfError(err)).toBe(false);
  });
});
