/**
 * wrkf/run.ts — workflow runs (wrkf.run.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.3 and §7.
 */

export interface WrkfRun {
  id: string;
  instanceId?: string;
  status?: string;
  role?: string;
  principal_ref?: string;
  externalRunRef?: string;
  deliveryRef?: string;
  lane?: string;
  startedAt?: string;
  finishedAt?: string;
  terminalResult?: string;
  [k: string]: unknown;
}

export interface WrkfRunStartParams {
  task?: string;
  instanceId?: string;
  role?: string;
  principal_ref?: string;
  idempotencyKey?: string;
  deliveryRef?: string;
  lane?: string;
  externalRunRef?: string;
}

export interface WrkfRunBindExternalParams {
  runId: string;
  externalRunRef: string;
  deliveryRef?: string;
  lane?: string;
  idempotencyKey?: string;
}

export interface WrkfRunFinishParams {
  runId: string;
  summary?: string;
  [k: string]: unknown;
}

export interface WrkfRunFailParams {
  runId: string;
  summary?: string;
  [k: string]: unknown;
}

export interface WrkfRunShowParams {
  runId: string;
}

export interface WrkfRunListParams {
  task?: string;
  instanceId?: string;
}
