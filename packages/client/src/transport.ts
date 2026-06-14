/**
 * transport.ts — JSON-RPC 2.0 wire types + the Transport seam.
 *
 * The Transport interface is the single seam the client drives. The real
 * implementation (StdioTransport) spawns `wrkq rpc --stdio` / `wrkf rpc --stdio`
 * via Bun subprocess APIs; tests inject an in-memory FakeTransport. The client
 * is identical against both.
 *
 * Contract: docs/wrkq-wrkf-rpc.md §1 (transport), §5 (error contract).
 */

/** A JSON-RPC 2.0 request frame. `id` is assigned by the client before send. */
export interface JsonRpcRequest {
  jsonrpc: "2.0";
  id: string | number;
  method: string;
  params?: unknown;
}

/** A JSON-RPC 2.0 notification frame (no id, no response expected). */
export interface JsonRpcNotification {
  jsonrpc: "2.0";
  method: string;
  params?: unknown;
}

/**
 * JSON-RPC error object. `data.code` is the stable WRKQ_/WRKF_ contract
 * string; `code` is the numeric JSON-RPC code; `message` is diagnostic text
 * (may change). docs/wrkq-wrkf-rpc.md §5.
 */
export interface JsonRpcError {
  code: number;
  message: string;
  data?: {
    code?: string;
    retryable?: boolean;
    [k: string]: unknown;
  };
}

/** A JSON-RPC 2.0 response frame — exactly one of result/error is present. */
export interface JsonRpcResponse {
  jsonrpc: "2.0";
  id: string | number | null;
  result?: any;
  error?: JsonRpcError;
}

/**
 * The transport seam. The client holds exactly one of these and never touches
 * the subprocess directly. `request` correlates by `frame.id`. `close` is a
 * graceful drain (stdin EOF == rpc.exit); `kill` is an immediate SIGKILL.
 */
export interface Transport {
  request(frame: JsonRpcRequest): Promise<JsonRpcResponse>;
  close(): Promise<void>;
  kill(): void;
}
