/**
 * json-rpc-channel.ts — NDJSON framing + request/response correlation.
 *
 * Stream-agnostic: it is handed a `write(line)` sink and fed raw stdout chunks
 * via `feed()`. It owns the pending-request map, line buffering, and protocol
 * corruption detection. It knows nothing about child processes — StdioTransport
 * wires the streams.
 *
 * Contract: docs/wrkf-rpc.md §1 — "stdout: JSON-RPC frames ONLY. A single stray
 * log/print line corrupts the protocol." Non-JSON on stdout => hard failure.
 */

import type { JsonRpcRequest, JsonRpcResponse } from "./transport";

interface Pending {
  resolve: (resp: JsonRpcResponse) => void;
  reject: (err: Error) => void;
}

export class JsonRpcChannel {
  private pending = new Map<string | number, Pending>();
  private buffer = "";
  private closed = false;

  /**
   * @param write           sink for an already-serialized frame line (includes trailing "\n")
   * @param onProtocolError invoked once when non-JSON is seen on stdout (protocol corruption)
   */
  constructor(
    private readonly write: (line: string) => void,
    private readonly onProtocolError: (err: Error) => void,
  ) {}

  /** Register a pending request and write its frame. Resolves on the matching response. */
  send(frame: JsonRpcRequest): Promise<JsonRpcResponse> {
    if (this.closed) {
      return Promise.reject(new Error("wrkf rpc channel is closed"));
    }
    if (this.pending.has(frame.id)) {
      return Promise.reject(new Error(`duplicate JSON-RPC id: ${String(frame.id)}`));
    }
    return new Promise<JsonRpcResponse>((resolve, reject) => {
      this.pending.set(frame.id, { resolve, reject });
      try {
        this.write(JSON.stringify(frame) + "\n");
      } catch (e) {
        this.pending.delete(frame.id);
        reject(e as Error);
      }
    });
  }

  /** Feed a raw stdout chunk. Parses complete NDJSON lines and dispatches them. */
  feed(chunk: string): void {
    if (this.closed) return;
    this.buffer += chunk;
    let nl: number;
    while ((nl = this.buffer.indexOf("\n")) >= 0) {
      const line = this.buffer.slice(0, nl).trim();
      this.buffer = this.buffer.slice(nl + 1);
      if (line.length === 0) continue;

      let frame: JsonRpcResponse;
      try {
        frame = JSON.parse(line) as JsonRpcResponse;
      } catch {
        // §1: a non-JSON line on stdout is protocol corruption — fatal.
        const err = new Error(
          `wrkf rpc protocol corruption: non-JSON frame on stdout: ${truncate(line, 200)}`,
        );
        this.onProtocolError(err);
        this.rejectAll(err);
        return;
      }

      const id = frame?.id;
      if (id !== undefined && id !== null && this.pending.has(id)) {
        const p = this.pending.get(id)!;
        this.pending.delete(id);
        p.resolve(frame);
      }
      // Notifications / unknown ids have no correlation target in v1 — ignore.
    }
  }

  /** Reject every outstanding request and mark the channel dead. */
  rejectAll(err: Error): void {
    this.closed = true;
    for (const p of this.pending.values()) p.reject(err);
    this.pending.clear();
  }

  get pendingCount(): number {
    return this.pending.size;
  }
}

function truncate(s: string, n: number): string {
  return s.length > n ? s.slice(0, n) + "…" : s;
}
