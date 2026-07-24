/**
 * errors.ts — one typed client-side error for the unified RPC surface.
 *
 * Every server JSON-RPC error frame surfaces as a single `WorkRpcError`. The
 * stable contract field is `domainCode` (== `data.code`, a WRKQ_/WRKF_ string
 * per docs/wrkq-wrkf-rpc.md §5); `rpcCode` carries the numeric JSON-RPC code;
 * `data` carries the full structured payload (CAS fields, blockers, lease info).
 *
 * Standard protocol errors (parse/-32700, invalid-request/-32600,
 * method-not-found/-32601) MAY omit `data.code`. Those are transport/protocol
 * errors, NOT domain errors — they still surface as `WorkRpcError`, but
 * `domainCode` is undefined so callers can distinguish via `isWrkqError` /
 * `isWrkfError`.
 *
 * Consumers catch exactly one type; do not introduce separate wrkq/wrkf error
 * classes (spec §7.5).
 */

import type { JsonRpcError } from "./transport.js";
import type { WrkfActionClaimPredecessor, WrkfSuspension } from "./wrkf/types.js";

export interface WorkRpcErrorData {
  /** Stable machine-readable domain code, e.g. "WRKF_STALE_REVISION". */
  code?: string;
  /** Whether the operation may be retried. */
  retryable?: boolean;
  /** Present when wrkf.action.claim refuses an unacknowledged predecessor; its settled discriminator is producer-owned. */
  predecessor?: WrkfActionClaimPredecessor;
  /** Present when a write, including wrkf.action.claim, is refused while suspended. */
  suspension?: WrkfSuspension;
  [k: string]: unknown;
}

export class WorkRpcError extends Error {
  /** Numeric JSON-RPC error code (e.g. -32009). */
  readonly rpcCode: number;
  /** Full structured `data` payload from the error frame (may be undefined). */
  readonly data?: WorkRpcErrorData;
  /** Stable WRKQ_/WRKF_ code from `data.code`; undefined for protocol errors. */
  readonly domainCode?: string;
  /** Whether the operation may be retried (from `data.retryable`). */
  readonly retryable?: boolean;
  /** The JSON-RPC request id this error responded to. */
  readonly requestId?: string | number | null;
  /** The method that produced this error (e.g. "wrkf.transition.apply"). */
  readonly method?: string;

  constructor(
    err: JsonRpcError,
    ctx?: { method?: string; requestId?: string | number | null },
  ) {
    super(err?.message ?? "work rpc error");
    this.name = "WorkRpcError";
    this.rpcCode = err?.code ?? -32603;
    this.data = err?.data as WorkRpcErrorData | undefined;
    this.domainCode = typeof this.data?.code === "string" ? this.data.code : undefined;
    this.retryable = typeof this.data?.retryable === "boolean" ? this.data.retryable : undefined;
    this.method = ctx?.method;
    this.requestId = ctx?.requestId;

    // Preserve instanceof across transpilation / cross-realm.
    Object.setPrototypeOf(this, WorkRpcError.prototype);
  }
}

/** Type guard: any unified RPC error. */
export function isWorkRpcError(error: unknown): error is WorkRpcError {
  return error instanceof WorkRpcError;
}

/** Type guard: a WRKQ_* domain error. */
export function isWrkqError(
  error: unknown,
): error is WorkRpcError & { domainCode: `WRKQ_${string}` } {
  return isWorkRpcError(error) && typeof error.domainCode === "string" && error.domainCode.startsWith("WRKQ_");
}

/** Type guard: a WRKF_* domain error. */
export function isWrkfError(
  error: unknown,
): error is WorkRpcError & { domainCode: `WRKF_${string}` } {
  return isWorkRpcError(error) && typeof error.domainCode === "string" && error.domainCode.startsWith("WRKF_");
}
