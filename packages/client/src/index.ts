/**
 * @wrkq/client — unified Bun TypeScript client for the wrkq/wrkf RPC protocol.
 *
 * Root entrypoint. The concrete client factory lives here; namespace types are
 * also available via the `./wrkq` and `./wrkf` subpaths, and test doubles via
 * `./testing`.
 *
 * Contract: docs/wrkq-wrkf-rpc.md. Spec: docs/wrkq-wrkf-rpc-client-forward-spec.md §4, §7.
 */

export { createClient } from "./client.js";
export type { WorkClient, CreateClientOptions } from "./client.js";

export { WorkRpcError, isWorkRpcError, isWrkqError, isWrkfError } from "./errors.js";
export type { WorkRpcErrorData } from "./errors.js";

export { PROTOCOL_VERSION } from "./protocol.js";
export type { InitializeParams, InitializeResult } from "./protocol.js";

// Transport seam + wire types (for advanced/low-level consumers and tests).
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

// Namespace facade types + DTOs (also available via subpaths).
export type {
  WrkqAdminFacade,
  WrkqFacade,
} from "./wrkq/facade.js";
export type { WrkfFacade } from "./wrkf/facade.js";
export type * from "./wrkq/types.js";
export type * from "./wrkf/types.js";
