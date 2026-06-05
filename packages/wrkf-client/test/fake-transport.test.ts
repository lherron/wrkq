/**
 * fake-transport.test.ts — TDD RED
 *
 * Tests WrkfClient using an in-memory fake transport (no child_process, no real
 * process). Imports ../src/client which is NOT YET IMPLEMENTED → `bun test` FAILS.
 *
 * Contract reference: docs/wrkf-rpc.md §3, §4.1, §5, §7
 * Frozen method names, DTO field names, and error codes are encoded verbatim.
 */

import { describe, test, expect } from "bun:test";
import { WrkfClient } from "../src/client";
import { WrkfRpcError } from "../src/errors";
import type { TransitionResult } from "../src/types";
import type { Transport, JsonRpcRequest, JsonRpcResponse } from "../src/transport";

// ─────────────────────────────────────────────────────────────────────────────
// FakeTransport — in-memory JSON-RPC transport. No child_process, no I/O.
// Implements the Transport interface so WrkfClient drives it exactly as it
// would drive the real stdio transport.
// ─────────────────────────────────────────────────────────────────────────────
class FakeTransport implements Transport {
  private handlers = new Map<
    string,
    (req: JsonRpcRequest) => JsonRpcResponse
  >();
  readonly capturedRequests: JsonRpcRequest[] = [];

  /** Register a deterministic response handler for a given JSON-RPC method. */
  onMethod(
    method: string,
    handler: (req: JsonRpcRequest) => JsonRpcResponse,
  ): void {
    this.handlers.set(method, handler);
  }

  async request(frame: JsonRpcRequest): Promise<JsonRpcResponse> {
    this.capturedRequests.push(frame);
    const handler = this.handlers.get(frame.method);
    if (!handler) {
      return {
        jsonrpc: "2.0",
        id: frame.id,
        error: {
          code: -32601,
          message: `Method not found: ${frame.method}`,
        },
      };
    }
    return handler(frame);
  }

  async close(): Promise<void> {}
  kill(): void {}
}

// ─────────────────────────────────────────────────────────────────────────────
// Fixtures
// ─────────────────────────────────────────────────────────────────────────────
const MOCK_TRANSITION_RESULT: TransitionResult = {
  task: "T-00001",
  instanceId: "wfi_abc123",
  state: "plan_ready",
  revision: 1,
  contextHash: "sha256:deadbeef",
  eventId: "evt_xyz789",
  effects: [],
  obligations: [],
};

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────
describe("WrkfClient over fake transport", () => {
  // ── transition.apply — happy path ──────────────────────────────────────────
  describe("transition.apply", () => {
    test("sends a wrkf.transition.apply JSON-RPC 2.0 frame", async () => {
      const transport = new FakeTransport();

      transport.onMethod("wrkf.transition.apply", (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        result: MOCK_TRANSITION_RESULT,
      }));

      const client = new WrkfClient({ transport });

      await client.transition.apply({
        task: "T-00001",
        transition: "plan_ready",
        role: "coordinator",
        actor: "human:local",
        expectRevision: 0,
        contextHash: "sha256:abc",
        idempotencyKey: "acp:T-00001:plan_ready:0",
        runChecks: false,
        dryRun: false,
      });

      // Must have sent exactly one frame
      expect(transport.capturedRequests).toHaveLength(1);
      const frame = transport.capturedRequests[0]!;

      // JSON-RPC 2.0 envelope
      expect(frame.jsonrpc).toBe("2.0");

      // Frozen method name from docs/wrkf-rpc.md §4.1
      expect(frame.method).toBe("wrkf.transition.apply");

      // id must be present (string or number)
      expect(
        typeof frame.id === "string" || typeof frame.id === "number",
      ).toBe(true);
    });

    test("forwards params verbatim to the JSON-RPC frame", async () => {
      const transport = new FakeTransport();

      transport.onMethod("wrkf.transition.apply", (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        result: MOCK_TRANSITION_RESULT,
      }));

      const client = new WrkfClient({ transport });

      await client.transition.apply({
        task: "T-00001",
        transition: "plan_ready",
        role: "coordinator",
        actor: "human:local",
        expectRevision: 0,
        contextHash: "sha256:abc",
        idempotencyKey: "acp:T-00001:plan_ready:0",
        runChecks: false,
        dryRun: false,
      });

      const frame = transport.capturedRequests[0]!;

      // Params must include the CAS preconditions (§5.1)
      expect(frame.params).toMatchObject({
        task: "T-00001",
        transition: "plan_ready",
        role: "coordinator",
        actor: "human:local",
        expectRevision: 0,
        contextHash: "sha256:abc",
        idempotencyKey: "acp:T-00001:plan_ready:0",
        runChecks: false,
        dryRun: false,
      });
    });

    test("returns typed TransitionResult on success", async () => {
      const transport = new FakeTransport();

      transport.onMethod("wrkf.transition.apply", (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        result: MOCK_TRANSITION_RESULT,
      }));

      const client = new WrkfClient({ transport });

      const result = await client.transition.apply({
        task: "T-00001",
        transition: "plan_ready",
        role: "coordinator",
        actor: "human:local",
        expectRevision: 0,
        contextHash: "sha256:abc",
      });

      // Frozen TransitionResult fields from docs/wrkf-rpc.md §5
      expect(result.task).toBe("T-00001");
      expect(result.instanceId).toBe("wfi_abc123");
      expect(result.state).toBe("plan_ready");
      expect(result.revision).toBe(1);
      expect(result.contextHash).toBe("sha256:deadbeef");
      expect(result.eventId).toBe("evt_xyz789");
      expect(Array.isArray(result.effects)).toBe(true);
      expect(Array.isArray(result.obligations)).toBe(true);
    });
  });

  // ── transition.apply — error path ──────────────────────────────────────────
  describe("transition.apply — WRKF_STALE_REVISION", () => {
    test("surfaces server error frame as WrkfRpcError", async () => {
      const transport = new FakeTransport();

      // Server returns a JSON-RPC error frame (docs/wrkf-rpc.md §3)
      transport.onMethod("wrkf.transition.apply", (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        error: {
          code: -32009,
          message: "workflow revision mismatch",
          data: {
            code: "WRKF_STALE_REVISION",
            instanceId: "wfi_abc123",
            expectedRevision: 3,
            actualRevision: 4,
            retryable: true,
          },
        },
      }));

      const client = new WrkfClient({ transport });

      let caught: unknown;
      try {
        await client.transition.apply({
          task: "T-00001",
          transition: "plan_ready",
          role: "coordinator",
          actor: "human:local",
          expectRevision: 3,
          contextHash: "sha256:abc",
        });
      } catch (e) {
        caught = e;
      }

      // Must throw WrkfRpcError (not a plain Error)
      expect(caught).toBeInstanceOf(WrkfRpcError);
      const rpcErr = caught as WrkfRpcError;

      // .code carries data.code from §3 — frozen string "WRKF_STALE_REVISION"
      expect(rpcErr.code).toBe("WRKF_STALE_REVISION");

      // CAS mismatch fields surfaced on .data
      expect(rpcErr.data.retryable).toBe(true);
      expect(rpcErr.data.expectedRevision).toBe(3);
      expect(rpcErr.data.actualRevision).toBe(4);
    });

    test("rejects the promise when server returns WRKF_STALE_REVISION", async () => {
      const transport = new FakeTransport();

      transport.onMethod("wrkf.transition.apply", (req) => ({
        jsonrpc: "2.0",
        id: req.id,
        error: {
          code: -32009,
          message: "workflow revision mismatch",
          data: {
            code: "WRKF_STALE_REVISION",
            instanceId: "wfi_abc123",
            expectedRevision: 0,
            actualRevision: 1,
            retryable: true,
          },
        },
      }));

      const client = new WrkfClient({ transport });

      await expect(
        client.transition.apply({
          task: "T-00001",
          transition: "plan_ready",
          role: "coordinator",
          actor: "human:local",
          expectRevision: 0,
          contextHash: "sha256:abc",
        }),
      ).rejects.toBeInstanceOf(WrkfRpcError);
    });
  });
});
