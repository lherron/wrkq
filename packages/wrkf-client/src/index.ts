/**
 * index.ts — public entry point for @wrkf/client.
 */

export { WrkfClient } from "./client.js";
export type { WrkfClientOptions, WrkfClientSpawnOptions } from "./client.js";
export { WrkfRpcError } from "./errors.js";
export { StdioTransport } from "./stdio-transport.js";
export type { StdioSpawnOptions } from "./stdio-transport.js";
export { JsonRpcChannel } from "./json-rpc-channel.js";
export type {
  Transport,
  JsonRpcRequest,
  JsonRpcResponse,
  JsonRpcNotification,
  JsonRpcError,
} from "./transport.js";
export * from "./types.js";
