/**
 * errors.ts — typed client-side error for wrkf RPC failures.
 *
 * Every server JSON-RPC error frame surfaces as a WrkfRpcError. The stable
 * contract field is `.code` (== `data.code`, a WRKF_* string per §3); `.rpcCode`
 * carries the numeric JSON-RPC code; `.data` carries the full structured payload
 * (CAS fields, blockers, lease info, etc.).
 *
 * Per docs/wrkf-rpc.md §3: standard protocol errors (parse/-32700,
 * invalid-request/-32600, method-not-found/-32601) MAY omit `data.code`. Such
 * frames are transport/protocol-level, NOT domain errors — we still surface them
 * as WrkfRpcError but `.code` falls back to `RPC_<numeric>` and `.isDomainError`
 * is false so callers can distinguish.
 */

import type { JsonRpcError } from "./transport";

export class WrkfRpcError extends Error {
  /** Stable contract code: `data.code` (e.g. "WRKF_STALE_REVISION"), or `RPC_<n>` fallback. */
  readonly code: string;
  /** Numeric JSON-RPC error code (e.g. -32009). */
  readonly rpcCode: number;
  /** True when the server supplied a WRKF_* domain `data.code`. */
  readonly isDomainError: boolean;
  /** Whether the operation may be retried (always boolean on domain errors, §3). */
  readonly retryable: boolean;
  /** Full structured `data` payload from the error frame. */
  readonly data: Record<string, any>;

  constructor(err: JsonRpcError) {
    super(err?.message ?? "wrkf rpc error");
    this.name = "WrkfRpcError";
    this.rpcCode = err?.code ?? -32603;
    this.data = (err?.data ?? {}) as Record<string, any>;

    const domainCode = typeof this.data.code === "string" ? this.data.code : undefined;
    this.isDomainError = domainCode !== undefined;
    this.code = domainCode ?? `RPC_${this.rpcCode}`;
    this.retryable = this.data.retryable === true;

    // Preserve instanceof across transpilation / cross-realm.
    Object.setPrototypeOf(this, WrkfRpcError.prototype);
  }
}
