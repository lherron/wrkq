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
import type { WrkfActionClaimResult, WrkfActionNextResult } from "./wrkf/types.js";

const MAX_TEMPLATE_BODY_BYTES = 1 << 20;
const MAX_TEMPLATE_DIFF_BODY_BYTES = 2 << 20;

function templateBodyBytes(body: string): number {
  return new TextEncoder().encode(body).byteLength;
}

function assertTemplateBody(body: string, sourceName = "template body"): void {
  if (templateBodyBytes(body) > MAX_TEMPLATE_BODY_BYTES) {
    throw new RangeError(`${sourceName} exceeds ${MAX_TEMPLATE_BODY_BYTES}-byte template body limit`);
  }
}

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
  /** Local SQLite path or rpc:// remote wrkqd locator. Prefer this over dbPath. */
  dbLocator?: string;
  /** Legacy name for dbLocator. */
  dbPath?: string;
  /** Canonical caller authority passed to the selected CLI entrypoint. */
  principalRef?: string;
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
      create: async (p) => this.call("wrkq.task.create", rejectLegacyActorAttribution(p)),
      show: (p) => this.call("wrkq.task.show", p),
      list: (p) => this.call("wrkq.task.list", p ?? {}),
      findListView: (p) => this.call("wrkq.task.findListView", p ?? {}),
      update: (p) => this.call("wrkq.task.update", p),
      claim: (p) => this.call("wrkq.task.claim", p),
      claimValidate: (p) => this.call("wrkq.task.claimValidate", p),
      release: (p) => this.call("wrkq.task.release", p),
      move: (p) => this.call("wrkq.task.move", p),
      acknowledge: (p) => this.call("wrkq.task.acknowledge", p),
      delete: (p) => this.call("wrkq.task.delete", p),
      restore: (p) => this.call("wrkq.task.restore", p),
      copy: (p) => this.call("wrkq.task.copy", p),
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
    promise: {
      add: (p) => this.call("wrkq.promise.add", p),
      show: (p) => this.call("wrkq.promise.show", p),
      list: (p) => this.call("wrkq.promise.list", p ?? {}),
      ready: (p) => this.call("wrkq.promise.ready", p ?? {}),
      edit: (p) => this.call("wrkq.promise.edit", p),
      renew: (p) => this.call("wrkq.promise.renew", p),
      resolve: (p) => this.call("wrkq.promise.resolve", p),
      abandon: (p) => this.call("wrkq.promise.abandon", p),
      attach: (p) => this.call("wrkq.promise.attach", p),
      detach: (p) => this.call("wrkq.promise.detach", p),
      delete: (p) => this.call("wrkq.promise.delete", p),
    },
    room: {
      say: (p) => this.call("wrkq.room.say", p),
      open: (p) => this.call("wrkq.room.open", p),
      show: (p) => this.call("wrkq.room.show", p),
      list: (p) => this.call("wrkq.room.list", p ?? {}),
      logView: (p) => this.call("wrkq.room.logView", p),
      hide: (p) => this.call("wrkq.room.hide", p),
      unhide: (p) => this.call("wrkq.room.unhide", p),
      join: (p) => this.call("wrkq.room.join", p),
      leave: (p) => this.call("wrkq.room.leave", p),
      membersView: (p) => this.call("wrkq.room.membersView", p),
    },
    envelope: {
      show: (p) => this.call("wrkq.envelope.show", p),
      inboxView: (p) => this.call("wrkq.envelope.inboxView", p ?? {}),
      defer: (p) => this.call("wrkq.envelope.defer", p),
      ack: (p) => this.call("wrkq.envelope.ack", p),
      present: (p) => this.call("wrkq.envelope.present", p),
      pendingView: (p) => this.call("wrkq.envelope.pendingView", p ?? {}),
      roundEnded: (p) => this.call("wrkq.envelope.roundEnded", p),
      birthEnvelope: (p) => this.call("wrkq.envelope.birthEnvelope", p),
    },
    container: {
      create: (p) => this.call("wrkq.container.create", p),
      update: (p) => this.call("wrkq.container.update", p),
      campaignConvert: (p) => this.call("wrkq.container.campaignConvert", p),
      campaignActivate: (p) => this.call("wrkq.container.campaignActivate", p),
      campaignUpdate: (p) => this.call("wrkq.container.campaignUpdate", p),
      campaignClose: (p) => this.call("wrkq.container.campaignClose", p),
      campaignPortfolio: (p) => this.call("wrkq.container.campaignPortfolio", p ?? {}),
      timelineView: (p) => this.call("wrkq.container.timelineView", p),
      delete: (p) => this.call("wrkq.container.delete", p),
      deleteRecursive: (p) => this.call("wrkq.container.deleteRecursive", p),
      show: (p) => this.call("wrkq.container.show", p),
      list: (p) => this.call("wrkq.container.list", p ?? {}),
      taskCounts: (p) => this.call("wrkq.container.taskCounts", p ?? {}),
    },
    project: {
      listView: (p) => this.call("wrkq.project.listView", p ?? {}),
      setRoot: (p) => this.call("wrkq.project.setRoot", p),
    },
    webhook: {
      add: (p) => this.call("wrkq.webhook.add", p),
      remove: (p) => this.call("wrkq.webhook.remove", p),
      listView: (p) => this.call("wrkq.webhook.listView", p ?? {}),
    },
    workflow: {
      attach: (p) => this.call("wrkq.workflow.attach", p),
      inspect: (p) => this.call("wrkq.workflow.inspect", p),
      instances: (p) => this.call("wrkq.workflow.instances", p),
      timeline: (p) => this.call("wrkq.workflow.timeline", p),
      refresh: (p) => this.call("wrkq.workflow.refresh", p),
      syncMeta: (p) => this.call("wrkq.workflow.syncMeta", p ?? {}),
    },
    handoff: {
      create: (p) => this.call("wrkq.handoff.create", p),
      get: (p) => this.call("wrkq.handoff.get", p),
      listView: (p) => this.call("wrkq.handoff.listView", p),
      acknowledge: (p) => this.call("wrkq.handoff.acknowledge", p),
    },
    search: {
      listView: (p) => this.call("wrkq.search.listView", p),
    },
    index: {
      status: () => this.call("wrkq.index.status", {}),
      update: (p) => this.call("wrkq.index.update", p ?? {}),
      rebuild: (p) => this.call("wrkq.index.rebuild", p ?? {}),
      vacuum: (p) => this.call("wrkq.index.vacuum", p ?? {}),
      pause: (p) => this.call("wrkq.index.pause", p ?? {}),
      resume: (p) => this.call("wrkq.index.resume", p ?? {}),
    },
    admin: {},
  };

  readonly wrkf: WrkfFacade = {
    workflow: {
      validate: (p) => {
        assertTemplateBody(p.body, p.sourceName);
        return this.call("wrkf.workflow.validate", p);
      },
      show: (p) => this.call("wrkf.workflow.show", p),
      list: (p) => this.call("wrkf.workflow.list", p ?? {}),
      diff: (p) => {
        assertTemplateBody(p.oldBody, p.oldSourceName);
        assertTemplateBody(p.newBody, p.newSourceName);
        if (templateBodyBytes(p.oldBody) + templateBodyBytes(p.newBody) > MAX_TEMPLATE_DIFF_BODY_BYTES) {
          throw new RangeError(`template diff bodies exceed ${MAX_TEMPLATE_DIFF_BODY_BYTES}-byte aggregate limit`);
        }
        return this.call("wrkf.workflow.diff", p);
      },
      install: (p) => {
        assertTemplateBody(p.body, p.sourceName);
        return this.call("wrkf.workflow.install", p);
      },
      discontinue: (p) => this.call("wrkf.workflow.discontinue", p),
      reinstate: (p) => this.call("wrkf.workflow.reinstate", p),
    },
    instance: {
      show: (p) => this.call("wrkf.instance.show", p),
      next: (p) => this.call("wrkf.instance.next", p),
      cancel: (p) => this.call("wrkf.instance.cancel", p),
    },
    evidence: {
      add: (p) => this.call("wrkf.evidence.add", p),
      list: (p) => this.call("wrkf.evidence.list", p),
      show: (p) => this.call("wrkf.evidence.show", p),
      suggest: (p) => this.call("wrkf.evidence.suggest", p),
      schema: (p) => this.call("wrkf.evidence.schema", p),
    },
    ledger: {
      append: (p) => this.call("wrkf.ledger.append", p),
      list: (p) => this.call("wrkf.ledger.list", p),
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
      create: (p) => this.call("wrkf.obligation.create", p),
    },
    supervisor: {
      call: (p) => this.call("wrkf.supervisor.call", p),
      escalate: (p) => this.call("wrkf.supervisor.escalate", p),
    },
    watch: {
      snapshot: (p) => this.call("wrkf.watch.snapshot", p),
      events: (p) => this.call("wrkf.watch.events", p),
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
    suspension: {
      resolve: (p) => this.call("wrkf.suspension.resolve", p),
    },
    run: {
      start: (p) => this.call("wrkf.run.start", p),
      bindExternal: (p) => this.call("wrkf.run.bindExternal", p),
      finish: (p) => this.call("wrkf.run.finish", p),
      fail: (p) => this.call("wrkf.run.fail", p),
      show: (p) => this.call("wrkf.run.show", p),
      list: (p) => this.call("wrkf.run.list", p),
    },
    action: {
      next: (p) => this.call<WrkfActionNextResult>("wrkf.action.next", p),
      claim: (p) => this.call<WrkfActionClaimResult>("wrkf.action.claim", p),
      settle: (p) => this.call("wrkf.action.settle", p),
      start: (p) => this.call("wrkf.action.start", p),
      bindExternal: (p) => this.call("wrkf.action.bindExternal", p),
      complete: (p) => this.call("wrkf.action.complete", p),
      fail: (p) => this.call("wrkf.action.fail", p),
      heartbeat: (p) => this.call("wrkf.action.heartbeat", p),
      renewLease: (p) => this.call("wrkf.action.renewLease", p),
      show: (p) => this.call("wrkf.action.show", p),
      list: (p) => this.call("wrkf.action.list", p),
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

function rejectLegacyActorAttribution<T>(params: T): T {
  if (
    params !== null &&
    typeof params === "object" &&
    Object.prototype.hasOwnProperty.call(params, "actor")
  ) {
    throw new Error("actor is no longer accepted for wrkq caller attribution; use principalRef");
  }
  return params;
}

/**
 * Construct a unified client. By default spawns `wrkq rpc --stdio` and runs
 * `rpc.initialize` before resolving. Pass `transport` to inject a fake.
 */
export async function createClient(opts: CreateClientOptions = {}): Promise<WorkClient> {
  if (Object.prototype.hasOwnProperty.call(opts, "actor")) {
    throw new Error("actor is no longer accepted for caller authority; use principalRef");
  }
  const transport =
    opts.transport ??
    new StdioTransport({
      command: opts.command ?? "wrkq",
      args: opts.args,
      dbLocator: opts.dbLocator,
      dbPath: opts.dbPath,
      principalRef: opts.principalRef,
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
