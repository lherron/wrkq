/**
 * fake-transport.test.ts — FakeTransport request-level idempotency replay semantics.
 *
 * Unit tests over the in-memory FakeTransport; no subprocess, no I/O.
 * Contract: docs/wrkq-wrkf-rpc.md §4, §5, §6, §7.
 */

import { describe, expect, test } from "bun:test";
import { WorkRpcError, isWrkfError, isWrkqError } from "../src/errors";
import { FakeTransport } from "../src/testing/fake-transport";
import type { JsonRpcRequest, JsonRpcResponse } from "../src/transport";
import { clientWith, rejectionOf } from "./support/client-fixtures";

/** A handler answering `{ [field]: n }`, where n counts the calls that reached it. */
function countingHandler(field = "handlerCall") {
  const counter = {
    calls: 0,
    handle: (req: JsonRpcRequest): JsonRpcResponse => ({
      jsonrpc: "2.0",
      id: req.id,
      result: { [field]: ++counter.calls },
    }),
  };
  return counter;
}

describe("FakeTransport idempotency", () => {
  test("identical reuse replays the original result without redispatching", async () => {
    const handler = countingHandler();
    const transport = new FakeTransport().onMethod("wrkf.evidence.add", handler.handle);

    const first = await transport.request({
      jsonrpc: "2.0",
      id: "first",
      method: "wrkf.evidence.add",
      params: { task: "T-00001", ref: "git:abc", idempotencyKey: "idem:replay" },
    });
    const replay = await transport.request({
      jsonrpc: "2.0",
      id: "replay",
      method: "wrkf.evidence.add",
      params: { task: "T-00001", ref: "git:abc", idempotencyKey: "idem:replay" },
    });

    expect(handler.calls).toBe(1);
    expect(first.result).toEqual({ handlerCall: 1 });
    expect(replay).toEqual({ jsonrpc: "2.0", id: "replay", result: { handlerCall: 1 } });
    expect(transport.capturedRequests).toHaveLength(2);
  });

  test("canonical comparison ignores object key order recursively", async () => {
    const handler = countingHandler();
    const transport = new FakeTransport().onMethod("wrkf.evidence.add", handler.handle);

    await transport.request({
      jsonrpc: "2.0",
      id: 1,
      method: "wrkf.evidence.add",
      params: {
        task: "T-00001",
        data: { z: 3, nested: { beta: 2, alpha: 1 } },
        idempotencyKey: "idem:canonical",
      },
    });
    const replay = await transport.request({
      jsonrpc: "2.0",
      id: 2,
      method: "wrkf.evidence.add",
      params: {
        idempotencyKey: "idem:canonical",
        data: { nested: { alpha: 1, beta: 2 }, z: 3 },
        task: "T-00001",
      },
    });

    expect(handler.calls).toBe(1);
    expect(replay.result).toEqual({ handlerCall: 1 });
  });

  test("changed values surface the real namespace-specific typed mismatch errors", async () => {
    const wrkfTransport = new FakeTransport().onResult("wrkf.evidence.add", { id: "evidence-1" });
    const wrkfClient = await clientWith(wrkfTransport);
    await wrkfClient.call("wrkf.evidence.add", {
      task: "T-00001",
      ref: "git:one",
      idempotencyKey: "idem:mismatch",
    });

    const wrkfError = await rejectionOf(wrkfClient.call("wrkf.evidence.add", {
      task: "T-00001",
      ref: "git:two",
      idempotencyKey: "idem:mismatch",
    }));
    expect(wrkfError).toBeInstanceOf(WorkRpcError);
    expect(isWrkfError(wrkfError)).toBe(true);
    expect(wrkfError).toMatchObject({
      rpcCode: -32013,
      domainCode: "WRKF_IDEMPOTENCY_MISMATCH",
      retryable: false,
      method: "wrkf.evidence.add",
      data: {
        code: "WRKF_IDEMPOTENCY_MISMATCH",
        retryable: false,
      },
    });
    expect(wrkfError.data).toEqual({
      code: "WRKF_IDEMPOTENCY_MISMATCH",
      retryable: false,
    });

    const wrkqTransport = new FakeTransport().onResult("wrkq.comment.add", { id: "comment-1" });
    const wrkqClient = await clientWith(wrkqTransport);
    await wrkqClient.call("wrkq.comment.add", {
      task: "T-00001",
      body: "one",
      idempotencyKey: "idem:mismatch",
    });

    const wrkqError = await rejectionOf(wrkqClient.call("wrkq.comment.add", {
      task: "T-00001",
      body: "two",
      idempotencyKey: "idem:mismatch",
    }));
    expect(wrkqError).toBeInstanceOf(WorkRpcError);
    expect(isWrkqError(wrkqError)).toBe(true);
    expect(wrkqError).toMatchObject({
      rpcCode: -32021,
      domainCode: "WRKQ_CONFLICT",
      retryable: true,
      method: "wrkq.comment.add",
      data: {
        code: "WRKQ_CONFLICT",
        retryable: true,
        idempotencyKey: "idem:mismatch",
      },
    });
    expect(wrkqError.data).toEqual({
      code: "WRKQ_CONFLICT",
      retryable: true,
      idempotencyKey: "idem:mismatch",
    });
  });

  test("the same key is independent across methods", async () => {
    const evidenceHandler = countingHandler("evidenceCall");
    const commentHandler = countingHandler("commentCall");
    const transport = new FakeTransport()
      .onMethod("wrkf.evidence.add", evidenceHandler.handle)
      .onMethod("wrkq.comment.add", commentHandler.handle);

    const evidence = {
      method: "wrkf.evidence.add",
      params: { ref: "git:abc", idempotencyKey: "shared-key" },
    } as const;
    const comment = {
      method: "wrkq.comment.add",
      params: { body: "ready", idempotencyKey: "shared-key" },
    } as const;

    await transport.request({ jsonrpc: "2.0", id: 1, ...evidence });
    await transport.request({ jsonrpc: "2.0", id: 2, ...comment });
    await transport.request({ jsonrpc: "2.0", id: 3, ...evidence });
    await transport.request({ jsonrpc: "2.0", id: 4, ...comment });

    expect(evidenceHandler.calls).toBe(1);
    expect(commentHandler.calls).toBe(1);
  });

  test("missing and empty keys dispatch every request", async () => {
    const handler = countingHandler();
    const transport = new FakeTransport().onMethod("wrkq.comment.add", handler.handle);

    for (const params of [
      { task: "T-00001", body: "no key" },
      { task: "T-00001", body: "no key" },
      { task: "T-00001", body: "empty key", idempotencyKey: "" },
      { task: "T-00001", body: "empty key", idempotencyKey: "" },
    ]) {
      await transport.request({
        jsonrpc: "2.0",
        id: `request-${handler.calls}`,
        method: "wrkq.comment.add",
        params,
      });
    }

    expect(handler.calls).toBe(4);
  });

  test("only a non-empty top-level idempotencyKey is request-level idempotency", async () => {
    const handler = countingHandler();
    const transport = new FakeTransport().onMethod("wrkf.action.complete", handler.handle);

    const params = {
      actionRunId: "wfa_1",
      transitionIdempotencyKey: "transition-only",
      evidence: { idempotencyKey: "nested-evidence-only" },
    };
    await transport.request({
      jsonrpc: "2.0",
      id: 1,
      method: "wrkf.action.complete",
      params,
    });
    await transport.request({
      jsonrpc: "2.0",
      id: 2,
      method: "wrkf.action.complete",
      params,
    });

    expect(handler.calls).toBe(2);
  });
});
