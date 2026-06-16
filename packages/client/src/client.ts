/**
 * client.ts — createClient: the unified WorkClient over the wrkq/wrkf RPC
 * surface.
 *
 * The client is transport-agnostic: it assigns JSON-RPC ids, serializes calls
 * into request frames, awaits responses via the injected Transport, and maps
 * any error frame to a typed WorkRpcError carrying method + requestId. The
 * public shape is exactly two business namespaces (`wrkq`, `wrkf`) plus `rpc`
 * lifecycle controls — no root business namespaces (spec §7.2).
 *
 *   const client = await createClient({ command: "wrkq", dbPath });
 *   await client.wrkq.task.create({ title: "..." });
 *   await client.wrkf.transition.apply({ task, transition, expectRevision });
 *   await client.close();
 */

import { WorkRpcError } from "./errors.js";
import {
  PROTOCOL_VERSION,
  type InitializeParams,
  type InitializeResult,
} from "./protocol.js";
import { StdioTransport } from "./stdio-transport.js";
import type { JsonRpcRequest, Transport } from "./transport.js";
import type { WrkqFacade } from "./wrkq/facade.js";
import type { WrkfFacade } from "./wrkf/facade.js";

export interface WorkClient {
  readonly rpc: {
    initialize(params?: InitializeParams): Promise<InitializeResult>;
    shutdown(): Promise<void>;
  };

  readonly wrkq: WrkqFacade;
  readonly wrkf: WrkfFacade;

  /** Low-level escape hatch for methods not yet on a typed facade. */
  call<R = unknown>(method: string, params?: unknown): Promise<R>;
  /** Graceful shutdown then transport teardown. */
  close(): Promise<void>;
  /** Immediate, ungraceful termination. */
  kill(): void;
}

export interface CreateClientOptions {
  /** Binary to spawn. Defaults to "wrkq"; "wrkf" behaves identically. */
  command?: "wrkq" | "wrkf" | string;
  /** argv after the global flags. Defaults to ["rpc", "--stdio"]. */
  args?: string[];
  dbPath?: string;
  actor?: string;
  role?: string;
  hookCatalogPath?: string;
  cwd?: string;
  env?: Record<string, string | undefined>;
  signal?: AbortSignal;
  stderrTailBytes?: number;
  closeTimeoutMs?: number;
  clientInfo?: { name: string; version: string };
  /** Run rpc.initialize before returning. Default true. */
  autoInitialize?: boolean;
  /** Inject a transport directly (for tests). Bypasses spawning. */
  transport?: Transport;
}

const DEFAULT_CLIENT_INFO = { name: "@wrkq/client", version: "0.1.0" };

class WorkClientImpl implements WorkClient {
  private readonly transport: Transport;
  private readonly clientInfo: { name: string; version: string };
  private idSeq = 0;

  constructor(transport: Transport, clientInfo?: { name: string; version: string }) {
    this.transport = transport;
    this.clientInfo = clientInfo ?? DEFAULT_CLIENT_INFO;
  }

  async call<R = unknown>(method: string, params?: unknown): Promise<R> {
    const id = `req_${++this.idSeq}`;
    const frame: JsonRpcRequest = { jsonrpc: "2.0", id, method };
    if (params !== undefined) frame.params = params;
    const resp = await this.transport.request(frame);
    if (resp.error) {
      throw new WorkRpcError(resp.error, { method, requestId: resp.id ?? id });
    }
    return resp.result as R;
  }

  readonly rpc = {
    initialize: (params?: InitializeParams): Promise<InitializeResult> =>
      this.call<InitializeResult>("rpc.initialize", {
        protocolVersion: params?.protocolVersion ?? PROTOCOL_VERSION,
        client: params?.client ?? this.clientInfo,
      }),
    shutdown: async (): Promise<void> => {
      await this.call("rpc.shutdown");
    },
  };

  readonly wrkq: WrkqFacade = {
    task: {
      create: (p) => this.call("wrkq.task.create", p),
      show: (p) => this.call("wrkq.task.show", p),
      list: (p) => this.call("wrkq.task.list", p ?? {}),
      update: (p) => this.call("wrkq.task.update", p),
      acknowledge: (p) => this.call("wrkq.task.acknowledge", p),
      delete: (p) => this.call("wrkq.task.delete", p),
      restore: (p) => this.call("wrkq.task.restore", p),
    },
    comment: {
      add: (p) => this.call("wrkq.comment.add", p),
      list: (p) => this.call("wrkq.comment.list", p),
      show: (p) => this.call("wrkq.comment.show", p),
      delete: (p) => this.call("wrkq.comment.delete", p),
    },
    attachment: {
      add: (p) => this.call("wrkq.attachment.add", p),
      list: (p) => this.call("wrkq.attachment.list", p),
      show: (p) => this.call("wrkq.attachment.show", p),
      remove: (p) => this.call("wrkq.attachment.remove", p),
    },
    relation: {
      add: (p) => this.call("wrkq.relation.add", p),
      list: (p) => this.call("wrkq.relation.list", p),
      remove: (p) => this.call("wrkq.relation.remove", p),
    },
    container: {
      create: (p) => this.call("wrkq.container.create", p),
      delete: (p) => this.call("wrkq.container.delete", p),
      deleteRecursive: (p) => this.call("wrkq.container.deleteRecursive", p),
      show: (p) => this.call("wrkq.container.show", p),
      list: (p) => this.call("wrkq.container.list", p ?? {}),
    },
    workflow: {
      attach: (p) => this.call("wrkq.workflow.attach", p),
      inspect: (p) => this.call("wrkq.workflow.inspect", p),
      timeline: (p) => this.call("wrkq.workflow.timeline", p),
      refresh: (p) => this.call("wrkq.workflow.refresh", p),
    },
    admin: {
      legacyActor: {
        list: (p) => this.call("wrkq.admin.legacyActor.list", p ?? {}),
        create: (p) => this.call("wrkq.admin.legacyActor.create", p),
        update: (p) => this.call("wrkq.admin.legacyActor.update", p),
      },
    },
  };

  readonly wrkf: WrkfFacade = {
    workflow: {
      validate: (p) => this.call("wrkf.workflow.validate", p),
      show: (p) => this.call("wrkf.workflow.show", p),
      list: (p) => this.call("wrkf.workflow.list", p ?? {}),
      diff: (p) => this.call("wrkf.workflow.diff", p),
      install: (p) => this.call("wrkf.workflow.install", p),
    },
    instance: {
      show: (p) => this.call("wrkf.instance.show", p),
      next: (p) => this.call("wrkf.instance.next", p),
    },
    evidence: {
      add: (p) => this.call("wrkf.evidence.add", p),
      list: (p) => this.call("wrkf.evidence.list", p),
      show: (p) => this.call("wrkf.evidence.show", p),
      suggest: (p) => this.call("wrkf.evidence.suggest", p),
    },
    event: {
      query: (p) => this.call("wrkf.event.query", p ?? {}),
    },
    role: {
      list: (p) => this.call("wrkf.role.list", p),
      bind: (p) => this.call("wrkf.role.bind", p),
      unbind: (p) => this.call("wrkf.role.unbind", p),
      set: (p) => this.call("wrkf.role.set", p),
    },
    obligation: {
      list: (p) => this.call("wrkf.obligation.list", p),
      show: (p) => this.call("wrkf.obligation.show", p),
      satisfy: (p) => this.call("wrkf.obligation.satisfy", p),
      waive: (p) => this.call("wrkf.obligation.waive", p),
      cancel: (p) => this.call("wrkf.obligation.cancel", p),
    },
    check: {
      preflight: (p) => this.call("wrkf.check.preflight", p),
      run: (p) => this.call("wrkf.check.run", p),
      show: (p) => this.call("wrkf.check.show", p),
      list: (p) => this.call("wrkf.check.list", p),
    },
    hook: {
      list: (p) => this.call("wrkf.hook.list", p ?? {}),
      show: (p) => this.call("wrkf.hook.show", p),
      run: (p) => this.call("wrkf.hook.run", p),
    },
    transition: {
      apply: (p) => this.call("wrkf.transition.apply", p),
    },
    run: {
      start: (p) => this.call("wrkf.run.start", p),
      bindExternal: (p) => this.call("wrkf.run.bindExternal", p),
      finish: (p) => this.call("wrkf.run.finish", p),
      fail: (p) => this.call("wrkf.run.fail", p),
      show: (p) => this.call("wrkf.run.show", p),
      list: (p) => this.call("wrkf.run.list", p),
    },
    effect: {
      list: (p) => this.call("wrkf.effect.list", p),
      show: (p) => this.call("wrkf.effect.show", p),
      claim: (p) => this.call("wrkf.effect.claim", p),
      ack: (p) => this.call("wrkf.effect.ack", p),
      fail: (p) => this.call("wrkf.effect.fail", p),
      retry: (p) => this.call("wrkf.effect.retry", p),
      deliver: (p) => this.call("wrkf.effect.deliver", p),
    },
  };

  async close(): Promise<void> {
    try {
      await this.rpc.shutdown();
    } catch {
      // best-effort; transport teardown still proceeds (stdin EOF == rpc.exit).
    }
    await this.transport.close();
  }

  kill(): void {
    this.transport.kill();
  }
}

/**
 * Construct a unified client. By default spawns `wrkq rpc --stdio` and runs
 * `rpc.initialize` before resolving. Pass `transport` to inject a fake.
 */
export async function createClient(opts: CreateClientOptions = {}): Promise<WorkClient> {
  const transport =
    opts.transport ??
    new StdioTransport({
      command: opts.command ?? "wrkq",
      args: opts.args,
      dbPath: opts.dbPath,
      actor: opts.actor,
      role: opts.role,
      hookCatalogPath: opts.hookCatalogPath,
      signal: opts.signal,
      stderrTailBytes: opts.stderrTailBytes,
      closeTimeoutMs: opts.closeTimeoutMs,
      env: opts.env,
      cwd: opts.cwd,
    });

  const client = new WorkClientImpl(transport, opts.clientInfo);

  if (opts.autoInitialize !== false) {
    await client.rpc.initialize();
  }

  return client;
}
