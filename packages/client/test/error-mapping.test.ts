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

describe("error mapping", () => {
  test("action claim suspension refusal exposes only the typed suspension record", async () => {
    const transport = new FakeTransport().onError("wrkf.action.claim", {
      code: -32026,
      message: "instance wfi_1 is suspended (sus_1 operator_required)",
      data: {
        code: "WRKF_SUSPENDED",
        retryable: false,
        suspension: {
          id: "sus_1",
          reason: "operator_required",
          at: "2026-07-12T22:00:00Z",
          causeRef: "wfe_1",
        },
      },
    });
    const client = await clientWith(transport);
    const error = await rejectionOf(client.wrkf.action.claim({
      task: "T-00001", runnerId: "runner-b", agentRef: "agent:larry",
      leaseMs: 60_000, priorRun: null,
    }));
    expect(error.domainCode).toBe("WRKF_SUSPENDED");
    expect(error.data?.suspension).toEqual({
      id: "sus_1",
      reason: "operator_required",
      at: "2026-07-12T22:00:00Z",
      causeRef: "wfe_1",
    });
    expect(error.data?.predecessor).toBeUndefined();
  });

  test("action claim refusal exposes the typed predecessor record", async () => {
    const transport = new FakeTransport().onError("wrkf.action.claim", {
      code: -32014,
      message: "claim refused: priorRun must name predecessor run_000001",
      data: {
        code: "WRKF_LEASE_CONFLICT",
        retryable: true,
        predecessor: {
          runId: "run_000001",
          owner: "runner-a",
          claimedAt: "2026-07-12T12:00:00Z",
          heartbeatAt: "2026-07-12T12:01:00Z",
          expiresAt: "2026-07-12T12:02:00Z",
          settleStatus: "active",
          settled: false,
          sideEffectClasses: ["git.commit"],
          evidenceWritten: [],
        },
      },
    });
    const client = await clientWith(transport);
    const error = await rejectionOf(client.wrkf.action.claim({
      task: "T-00001", runnerId: "runner-b", agentRef: "agent:larry",
      leaseMs: 60_000, priorRun: null,
    }));
    expect(error.data?.predecessor?.runId).toBe("run_000001");
    expect(error.data?.predecessor?.settled).toBe(false);
    expect(error.data?.predecessor?.sideEffectClasses).toEqual(["git.commit"]);
  });

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
    expect(err.requestId).toBe(err.requestId); // present
    expect((err.data as any).expectedRevision).toBe(3);

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

  test("WRKQ_DB_BUSY contention surfaces as a retryable wrkq error", async () => {
    const transport = new FakeTransport().onError("wrkq.task.update", {
      code: -32024,
      message: "database is busy due to write contention; retry",
      data: { code: "WRKQ_DB_BUSY", retryable: true, reason: "sqlite_busy" },
    });
    const client = await clientWith(transport);
    const err = await rejectionOf(client.wrkq.task.update({ task: "T-1", patch: {} }));
    expect(isWrkqError(err)).toBe(true);
    expect(err.domainCode).toBe("WRKQ_DB_BUSY");
    expect(err.retryable).toBe(true);
    expect(err.data?.reason).toBe("sqlite_busy");
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
