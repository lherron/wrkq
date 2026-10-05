/**
 * wrkf/obligation.ts — obligations (wrkf.obligation.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.3 and §7.
 */

export interface WrkfObligation {
  id: string;
  kind?: string;
  status?: string;
  ownerRole?: string;
  blocking?: boolean;
  reason?: string;
  [k: string]: unknown;
}

export interface WrkfObligationListParams {
  task?: string;
  instanceId?: string;
}

export interface WrkfObligationShowParams {
  id: string;
}

export interface WrkfObligationSatisfyParams {
  task?: string;
  instanceId?: string;
  id: string;
  evidenceId?: string;
  idempotencyKey?: string;
}

export interface WrkfObligationWaiveParams {
  task?: string;
  instanceId?: string;
  id: string;
  reason?: string;
  idempotencyKey?: string;
}

export interface WrkfObligationCancelParams {
  task?: string;
  instanceId?: string;
  id: string;
  reason?: string;
  idempotencyKey?: string;
}

export interface WrkfObligationCreateParams {
  task: string;
  kind: string;
  ownerRole?: string;
  ownerActor?: string;
  blocking?: boolean;
  reason?: string;
}
