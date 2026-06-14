/**
 * testing/fake-transport.ts — in-memory Transport for unit tests.
 *
 * Implements the Transport interface so the client drives it exactly as it
 * would drive the real Bun stdio transport. No subprocess, no I/O. Register
 * per-method handlers; unknown methods return JSON-RPC method-not-found.
 */

import type {
  JsonRpcRequest,
  JsonRpcResponse,
  Transport,
} from "../transport.js";

export type FakeHandler = (req: JsonRpcRequest) => JsonRpcResponse;

export class FakeTransport implements Transport {
  private handlers = new Map<string, FakeHandler>();
  readonly capturedRequests: JsonRpcRequest[] = [];
  closed = false;
  killed = false;

  /** Register a deterministic response handler for a given JSON-RPC method. */
  onMethod(method: string, handler: FakeHandler): this {
    this.handlers.set(method, handler);
    return this;
  }

  /** Convenience: respond with a result for `method`. */
  onResult(method: string, result: unknown): this {
    return this.onMethod(method, (req) => ({ jsonrpc: "2.0", id: req.id, result }));
  }

  /** Convenience: respond with an error frame for `method`. */
  onError(method: string, error: JsonRpcResponse["error"]): this {
    return this.onMethod(method, (req) => ({ jsonrpc: "2.0", id: req.id, error }));
  }

  async request(frame: JsonRpcRequest): Promise<JsonRpcResponse> {
    this.capturedRequests.push(frame);
    const handler = this.handlers.get(frame.method);
    if (!handler) {
      return {
        jsonrpc: "2.0",
        id: frame.id,
        error: { code: -32601, message: `method not found: ${frame.method}` },
      };
    }
    return handler(frame);
  }

  async close(): Promise<void> {
    this.closed = true;
  }

  kill(): void {
    this.killed = true;
  }
}
