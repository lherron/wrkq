/**
 * types.ts — v1 hand-maintained TS types for the wrkf RPC surface.
 *
 * These mirror the frozen DTOs in docs/wrkf-rpc.md §5 and the wire shapes
 * exercised by test/smoke-wrkf-rpc.sh. No codegen yet (v1); fixture-based
 * compatibility tests guard drift.
 *
 * Where the server returns an existing internal/workflow runtime struct whose
 * exact field set is broad, we type the well-known fields and allow extra keys
 * (`[k: string]: unknown`) rather than risk pinning a field that does not exist.
 */

export const PROTOCOL_VERSION = "2026-06-01";

// ─── Lifecycle ──────────────────────────────────────────────────────────────

export interface InitializeParams {
  protocolVersion?: string;
  client?: { name: string; version: string };
}

export interface InitializeResult {
  protocolVersion: string;
  server: { name: string; version: string; pid: number };
  database: { path: string };
  capabilities: {
    cancel: boolean;
    effectClaimLease: boolean;
    runExternalBinding: boolean;
    [k: string]: unknown;
  };
  schemaHash: string;
}

// ─── Core value shapes ──────────────────────────────────────────────────────

/**
 * Workflow state. The server returns a structured `{ status, phase, outcome }`
 * object; some result envelopes (and the fake-transport fixture) carry a bare
 * state string. Both are accepted.
 */
export type State =
  | string
  | {
      status?: string;
      phase?: string;
      outcome?: string;
      [k: string]: unknown;
    };

export interface Effect {
  id: string;
  kind?: string;
  status?: string;
  role?: string;
  adapter?: string;
  leaseToken?: string;
  leasedBy?: string;
  leasedUntil?: string;
  [k: string]: unknown;
}

export interface Obligation {
  id: string;
  kind?: string;
  status?: string;
  ownerRole?: string;
  blocking?: boolean;
  reason?: string;
  [k: string]: unknown;
}

export interface Evidence {
  id: string;
  kind?: string;
  ref?: string;
  summary?: string;
  facts?: Record<string, unknown>;
  [k: string]: unknown;
}

export interface Run {
  id: string;
  status?: string;
  role?: string;
  actor?: string;
  externalRunRef?: string;
  deliveryRef?: string;
  lane?: string;
  summary?: string;
  [k: string]: unknown;
}

export interface CheckRun {
  id: string;
  verdict?: string;
  outcome?: string;
  [k: string]: unknown;
}

export interface Event {
  id: string;
  [k: string]: unknown;
}

export interface Instance {
  templateId?: string;
  phase?: string;
  status?: string;
  outcome?: string;
  revision?: number;
  contextHash?: string;
  taskDocHash?: string;
  [k: string]: unknown;
}

export interface Template {
  id: string;
  version: string;
  [k: string]: unknown;
}

export interface TemplateSummary {
  id: string;
  version: string;
  hash: string;
  kind?: string;
  description?: string;
  installedAt?: string;
  installedBy?: string;
}

export interface Blocker {
  id?: string;
  message?: string;
  [k: string]: unknown;
}

// ─── Result DTOs (§5) ───────────────────────────────────────────────────────

export interface ValidateResult {
  valid: boolean;
  [k: string]: unknown;
}

export interface WorkflowListResult {
  templates: TemplateSummary[];
}

export interface WorkflowShowResult {
  template: Template;
  hash: string;
}

export interface InstallResult {
  id: string;
  version: string;
  hash: string;
  installed: boolean;
}

export interface DiffResult {
  old: TemplateSummary;
  new: TemplateSummary;
  sameHash: boolean;
}

export interface SyncMetaResult {
  updated: number;
}

export interface SuggestResult {
  transition: string;
  required: unknown[];
  missing: string[];
  checks: string[];
  warnings: string[];
}

export interface TransitionResult {
  task: string;
  instanceId: string;
  state: State;
  revision: number;
  contextHash: string;
  eventId: string;
  effects: Effect[];
  obligations: Obligation[];
}

export interface EffectClaim {
  effects: Effect[];
  leaseToken: string;
  leaseExpiresAt?: string;
}

export interface CheckRunResult {
  runs: CheckRun[];
}

export interface NextActionResponse {
  actions: Array<{ kind: string; [k: string]: unknown }>;
  blockedTransitions?: Array<{
    id: string;
    blocksOn?: Array<{ message?: string; [k: string]: unknown }>;
    [k: string]: unknown;
  }>;
  [k: string]: unknown;
}

// ─── Param DTOs ─────────────────────────────────────────────────────────────

export interface TransitionApplyParams {
  task: string;
  transition: string;
  role?: string;
  actor?: string;
  /** CAS precondition: expected committed revision (§5.1). */
  expectRevision?: number;
  /** CAS precondition: expected committed instance context hash (§5.1, §6). */
  contextHash?: string;
  idempotencyKey?: string;
  runChecks?: boolean;
  dryRun?: boolean;
}

export interface RunStartParams {
  task: string;
  role?: string;
  actor?: string;
  idempotencyKey?: string;
  deliveryRef?: string;
  lane?: string;
  externalRunRef?: string;
}

export interface RunBindExternalParams {
  runId: string;
  externalRunRef: string;
  deliveryRef?: string;
  lane?: string;
  idempotencyKey?: string;
}

export interface RunFinishParams {
  runId: string;
  summary?: string;
  [k: string]: unknown;
}

export interface RunFailParams {
  runId: string;
  reason?: string;
  retryable?: boolean;
  [k: string]: unknown;
}

export interface EffectClaimParams {
  adapter: string;
  limit: number;
  leaseMs: number;
  task?: string;
  kind?: string;
}

export interface EffectAckParams {
  effectId: string;
  leaseToken: string;
  receipt?: unknown;
}

export interface EffectFailParams {
  effectId: string;
  leaseToken: string;
  reason: string;
  retryable?: boolean;
}

export interface EvidenceAddParams {
  task: string;
  kind: string;
  ref?: string;
  summary?: string;
  facts?: Record<string, unknown>;
}
