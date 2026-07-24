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

interface IdempotencyLedgerEntry {
  canonicalBody: string;
  result: unknown;
}

export class FakeTransport implements Transport {
  private handlers = new Map<string, FakeHandler>();
  private idempotencyLedger = new Map<string, Map<string, IdempotencyLedgerEntry>>();
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
    const idempotency = requestIdempotency(frame);
    if (idempotency) {
      const prior = this.idempotencyLedger.get(frame.method)?.get(idempotency.key);
      if (prior) {
        if (prior.canonicalBody !== idempotency.canonicalBody) {
          return idempotencyMismatch(frame, idempotency.key);
        }
        return {
          jsonrpc: "2.0",
          id: frame.id,
          result: cloneJsonValue(prior.result),
        };
      }
    }

    const handler = this.handlers.get(frame.method);
    if (!handler) {
      return {
        jsonrpc: "2.0",
        id: frame.id,
        error: { code: -32601, message: `method not found: ${frame.method}` },
      };
    }
    const response = handler(frame);
    if (idempotency && response.error === undefined) {
      let methodLedger = this.idempotencyLedger.get(frame.method);
      if (!methodLedger) {
        methodLedger = new Map();
        this.idempotencyLedger.set(frame.method, methodLedger);
      }
      methodLedger.set(idempotency.key, {
        canonicalBody: idempotency.canonicalBody,
        result: cloneJsonValue(response.result),
      });
    }
    return response;
  }

  async close(): Promise<void> {
    this.closed = true;
  }

  kill(): void {
    this.killed = true;
  }
}

/**
 * The request-level fake recognizes only a non-empty, top-level
 * `idempotencyKey` string. That is the shared field on the typed wrkq/wrkf
 * request params whose server methods replay the whole RPC result.
 *
 * `transitionIdempotencyKey` and nested `evidence.idempotencyKey` belong to
 * sub-operations of wrkf.action.complete, so treating either as whole-request
 * idempotency would replay more than the real server does.
 */
function requestIdempotency(
  frame: JsonRpcRequest,
): { key: string; canonicalBody: string } | undefined {
  if (!isRecord(frame.params)) return undefined;
  const key = frame.params.idempotencyKey;
  if (typeof key !== "string" || key.length === 0) return undefined;

  const body = { ...frame.params };
  delete body.idempotencyKey;
  return { key, canonicalBody: canonicalJson(body) };
}

function idempotencyMismatch(frame: JsonRpcRequest, key: string): JsonRpcResponse {
  if (frame.method.startsWith("wrkf.")) {
    return {
      jsonrpc: "2.0",
      id: frame.id,
      error: {
        code: -32013,
        message: "idempotency key was reused with different params",
        data: {
          code: "WRKF_IDEMPOTENCY_MISMATCH",
          retryable: false,
        },
      },
    };
  }
  return {
    jsonrpc: "2.0",
    id: frame.id,
    error: {
      code: -32021,
      message: "idempotency key reused with a different request",
      data: {
        code: "WRKQ_CONFLICT",
        retryable: true,
        idempotencyKey: key,
      },
    },
  };
}

function canonicalJson(value: unknown): string {
  return JSON.stringify(value, (_key, current: unknown) => {
    if (!isRecord(current)) return current;
    const sorted: Record<string, unknown> = {};
    for (const key of Object.keys(current).sort()) {
      sorted[key] = current[key];
    }
    return sorted;
  }) ?? "undefined";
}

function cloneJsonValue<T>(value: T): T {
  if (value === undefined) return value;
  return JSON.parse(JSON.stringify(value)) as T;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}
