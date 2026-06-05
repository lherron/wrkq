/**
 * client.ts — WrkfClient: typed, namespaced facade over the wrkf RPC surface.
 *
 * The client is transport-agnostic: it assigns JSON-RPC ids, serializes calls
 * into request frames, awaits responses via the injected Transport, and maps
 * any error frame to a typed WrkfRpcError. Namespaces mirror docs/wrkf-rpc.md
 * §4.1 exactly.
 *
 *   const client = WrkfClient.spawn({ command: "wrkf", dbPath, actor, role });
 *   await client.initialize();
 *   await client.workflow.install({ path });
 *   await client.transition.apply({ task, transition, expectRevision, contextHash });
 *   await client.close();
 */

import { WrkfRpcError } from "./errors.js";
import { StdioTransport, type StdioSpawnOptions } from "./stdio-transport.js";
import type { JsonRpcRequest, Transport } from "./transport.js";
import {
  PROTOCOL_VERSION,
  type CheckRunResult,
  type DiffResult,
  type EffectAckParams,
  type EffectClaim,
  type EffectClaimParams,
  type EffectFailParams,
  type Evidence,
  type EvidenceAddParams,
  type InitializeParams,
  type InitializeResult,
  type InstallResult,
  type NextActionResponse,
  type Obligation,
  type RunBindExternalParams,
  type RunFailParams,
  type RunFinishParams,
  type RunStartParams,
  type Run,
  type SuggestResult,
  type SyncMetaResult,
  type TransitionApplyParams,
  type TransitionResult,
  type ValidateResult,
  type WorkflowListResult,
  type WorkflowShowResult,
} from "./types.js";

export interface WrkfClientOptions {
  transport: Transport;
  clientInfo?: { name: string; version: string };
}

export interface WrkfClientSpawnOptions extends StdioSpawnOptions {
  clientInfo?: { name: string; version: string };
}

export class WrkfClient {
  private readonly transport: Transport;
  private readonly clientInfo: { name: string; version: string };
  private idSeq = 0;

  constructor(opts: WrkfClientOptions) {
    this.transport = opts.transport;
    this.clientInfo = opts.clientInfo ?? { name: "@wrkf/client", version: "0.1.0" };
  }

  /** Spawn the real `wrkf rpc --stdio` binary and wrap it. */
  static spawn(opts: WrkfClientSpawnOptions): WrkfClient {
    const transport = new StdioTransport(opts);
    return new WrkfClient({ transport, clientInfo: opts.clientInfo });
  }

  /** Low-level request: assign id, send frame, unwrap result or throw WrkfRpcError. */
  async call<R = unknown>(method: string, params?: unknown): Promise<R> {
    const id = `req_${++this.idSeq}`;
    const frame: JsonRpcRequest = { jsonrpc: "2.0", id, method };
    if (params !== undefined) frame.params = params;
    const resp = await this.transport.request(frame);
    if (resp.error) throw new WrkfRpcError(resp.error);
    return resp.result as R;
  }

  // ── Lifecycle (§2) ─────────────────────────────────────────────────────────

  initialize(params?: InitializeParams): Promise<InitializeResult> {
    return this.call<InitializeResult>("wrkf.initialize", {
      protocolVersion: params?.protocolVersion ?? PROTOCOL_VERSION,
      client: params?.client ?? this.clientInfo,
    });
  }

  shutdown(): Promise<unknown> {
    return this.call("wrkf.shutdown");
  }

  /** Graceful shutdown then transport close (stdin EOF == wrkf.exit). */
  async close(): Promise<void> {
    try {
      await this.shutdown();
    } catch {
      // best-effort; transport teardown still proceeds
    }
    await this.transport.close();
  }

  /** Immediate, ungraceful termination. */
  kill(): void {
    this.transport.kill();
  }

  // ── Top-level ────────────────────────────────────────────────────────────────

  next(params: { task: string; [k: string]: unknown }): Promise<NextActionResponse> {
    return this.call<NextActionResponse>("wrkf.next", params);
  }

  // ── Namespaces (§4.1) ────────────────────────────────────────────────────────

  readonly workflow = {
    validate: (p: { path: string }): Promise<ValidateResult> =>
      this.call("wrkf.workflow.validate", p),
    show: (p: { ref: string }): Promise<WorkflowShowResult> =>
      this.call("wrkf.workflow.show", p),
    list: (p?: Record<string, unknown>): Promise<WorkflowListResult> =>
      this.call("wrkf.workflow.list", p),
    diff: (p: { oldPath: string; newPath: string }): Promise<DiffResult> =>
      this.call("wrkf.workflow.diff", p),
    install: (p: { path: string }): Promise<InstallResult> =>
      this.call("wrkf.workflow.install", p),
  };

  readonly task = {
    attach: (p: { task: string; workflow: string }): Promise<Record<string, any>> =>
      this.call("wrkf.task.attach", p),
    inspect: (p: { task: string }): Promise<Record<string, any>> =>
      this.call("wrkf.task.inspect", p),
    timeline: (p: { task: string }): Promise<unknown[]> =>
      this.call("wrkf.task.timeline", p),
    refresh: (p: { task: string }): Promise<Record<string, any>> =>
      this.call("wrkf.task.refresh", p),
    syncMeta: (p: { task: string }): Promise<SyncMetaResult> =>
      this.call("wrkf.task.syncMeta", p),
  };

  readonly evidence = {
    add: (p: EvidenceAddParams): Promise<Evidence> => this.call("wrkf.evidence.add", p),
    list: (p: { task: string }): Promise<Evidence[]> => this.call("wrkf.evidence.list", p),
    show: (p: { id: string }): Promise<Evidence> => this.call("wrkf.evidence.show", p),
    suggest: (p: { task: string; transition: string }): Promise<SuggestResult> =>
      this.call("wrkf.evidence.suggest", p),
  };

  readonly obligation = {
    list: (p: { task: string }): Promise<Obligation[]> => this.call("wrkf.obligation.list", p),
    show: (p: { id: string }): Promise<Obligation> => this.call("wrkf.obligation.show", p),
    satisfy: (p: {
      task: string;
      id: string;
      evidenceId?: string;
    }): Promise<Obligation> => this.call("wrkf.obligation.satisfy", p),
    waive: (p: { task: string; id: string; reason?: string }): Promise<Obligation> =>
      this.call("wrkf.obligation.waive", p),
    cancel: (p: { task: string; id: string; reason?: string }): Promise<Obligation> =>
      this.call("wrkf.obligation.cancel", p),
  };

  readonly check = {
    preflight: (p: { task: string; transition: string }): Promise<Record<string, any>> =>
      this.call("wrkf.check.preflight", p),
    run: (p: { task: string; transition: string }): Promise<CheckRunResult> =>
      this.call("wrkf.check.run", p),
    show: (p: { id: string }): Promise<Record<string, any>> => this.call("wrkf.check.show", p),
    list: (p: { task: string; transition?: string }): Promise<unknown[]> =>
      this.call("wrkf.check.list", p),
  };

  readonly hook = {
    list: (p?: Record<string, unknown>): Promise<Record<string, any>> =>
      this.call("wrkf.hook.list", p),
    show: (p: { id: string }): Promise<Record<string, any>> => this.call("wrkf.hook.show", p),
    run: (p: {
      task: string;
      transition: string;
      hookId: string;
    }): Promise<Record<string, any>> => this.call("wrkf.hook.run", p),
  };

  readonly transition = {
    apply: (p: TransitionApplyParams): Promise<TransitionResult> =>
      this.call("wrkf.transition.apply", p),
  };

  readonly run = {
    start: (p: RunStartParams): Promise<Run> => this.call("wrkf.run.start", p),
    bindExternal: (p: RunBindExternalParams): Promise<Run> =>
      this.call("wrkf.run.bindExternal", p),
    finish: (p: RunFinishParams): Promise<Run> => this.call("wrkf.run.finish", p),
    fail: (p: RunFailParams): Promise<Run> => this.call("wrkf.run.fail", p),
    show: (p: { id: string }): Promise<Run> => this.call("wrkf.run.show", p),
    list: (p: { task: string }): Promise<Run[]> => this.call("wrkf.run.list", p),
  };

  readonly effect = {
    list: (p: { task: string; all?: boolean }): Promise<import("./types.js").Effect[]> =>
      this.call("wrkf.effect.list", p),
    show: (p: { id: string }): Promise<import("./types.js").Effect> =>
      this.call("wrkf.effect.show", p),
    claim: (p: EffectClaimParams): Promise<EffectClaim> => this.call("wrkf.effect.claim", p),
    ack: (p: EffectAckParams): Promise<import("./types.js").Effect> =>
      this.call("wrkf.effect.ack", p),
    fail: (p: EffectFailParams): Promise<import("./types.js").Effect> =>
      this.call("wrkf.effect.fail", p),
    retry: (p: { effectId: string }): Promise<import("./types.js").Effect> =>
      this.call("wrkf.effect.retry", p),
    deliver: (p: {
      effectId?: string;
      task?: string;
      adapter?: string;
    }): Promise<Record<string, any>> => this.call("wrkf.effect.deliver", p),
  };
}
