/**
 * wrkf/effect.ts — effects and supervisor calls that emit them (wrkf.effect.*, wrkf.supervisor.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.3 and §7.
 */

export interface WrkfEffect {
  id: string;
  instanceId?: string;
  kind?: string;
  status?: string;
  role?: string;
  payload?: Record<string, unknown>;
  adapter?: string;
  revision?: number;
  sequence?: number;
  attempts?: number;
  idempotencyKey?: string;
  semanticKey?: string;
  leaseToken?: string;
  leasedBy?: string;
  leasedUntil?: string;
  createdAt?: string;
  updatedAt?: string;
  [k: string]: unknown;
}

export interface WrkfSupervisorParams {
  task: string;
  reason?: string;
}

export interface WrkfEffectListParams {
  task?: string;
  instanceId?: string;
  all?: boolean;
}

export interface WrkfEffectShowParams {
  id: string;
}

export interface WrkfEffectClaimParams {
  adapter: string;
  limit: number;
  leaseMs: number;
  task?: string;
  instanceId?: string;
  kind?: string;
}

export interface WrkfEffectClaimResult {
  effects: WrkfEffect[];
  leaseToken: string;
  leaseExpiresAt?: string;
}

export interface WrkfEffectAckParams {
  effectId: string;
  leaseToken: string;
  receipt?: unknown;
  force?: boolean;
}

export interface WrkfEffectFailParams {
  effectId: string;
  leaseToken: string;
  reason: string;
  retryable?: boolean;
  force?: boolean;
}

export interface WrkfEffectRetryParams {
  effectId: string;
}

export interface WrkfEffectDeliverParams {
  effectId?: string;
  task?: string;
  instanceId?: string;
  adapter?: string;
}
