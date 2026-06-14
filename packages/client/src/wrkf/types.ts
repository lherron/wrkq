/**
 * wrkf/types.ts — DTOs for the wrkf namespace (templates, instances, evidence,
 * obligations, checks, hooks, transitions, runs, effects).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.3 and §7. All RPC DTO JSON fields are
 * camelCase. Field sets verified against the live `wrkf rpc --stdio` server
 * (proto 2026-06-14).
 *
 * Several wrkf result envelopes return broad workflow-runtime structs; we type
 * the well-known fields and allow extra keys (`[k: string]: unknown`) rather
 * than pin a field that may not exist for every template/state.
 */

// ── State ────────────────────────────────────────────────────────────────────

/**
 * Workflow state. The server returns a structured `{ status, phase, outcome }`
 * object; some envelopes carry a bare state string. Both are accepted.
 */
export type WrkfState =
  | string
  | {
      status?: string;
      phase?: string;
      outcome?: string;
      [k: string]: unknown;
    };

// ── Core resources ───────────────────────────────────────────────────────────

export interface WrkfInstance {
  id: string;
  taskUuid?: string;
  taskRef?: string;
  projectId?: string;
  templateId?: string;
  templateVersion?: string;
  templateHash?: string;
  status?: string;
  phase?: string;
  outcome?: string;
  revision: number;
  contextHash: string;
  taskDocEtag?: string;
  taskDocHash?: string;
  createdAt?: string;
  updatedAt?: string;
  [k: string]: unknown;
}

export interface WrkfEvent {
  id: string;
  [k: string]: unknown;
}

export interface WrkfEvidence {
  id: string;
  instanceId?: string;
  kind?: string;
  ref?: string;
  summary?: string;
  facts?: Record<string, unknown>;
  data?: unknown;
  actor?: string;
  role?: string;
  /** Persisted run linkage; see docs/wrkq-wrkf-rpc.md §9.7. */
  runId?: string;
  producedAt?: string;
  [k: string]: unknown;
}

export interface WrkfObligation {
  id: string;
  kind?: string;
  status?: string;
  ownerRole?: string;
  blocking?: boolean;
  reason?: string;
  [k: string]: unknown;
}

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

export interface WrkfRun {
  id: string;
  instanceId?: string;
  status?: string;
  role?: string;
  actor?: string;
  externalRunRef?: string;
  deliveryRef?: string;
  lane?: string;
  startedAt?: string;
  finishedAt?: string;
  terminalResult?: string;
  [k: string]: unknown;
}

export interface WrkfCheckRun {
  id: string;
  verdict?: string;
  outcome?: string;
  [k: string]: unknown;
}

export interface WrkfWorkflowTemplateSummary {
  id: string;
  version: string;
  hash: string;
  kind?: string;
  description?: string;
  installedAt?: string;
  installedBy?: string;
  [k: string]: unknown;
}

// ── Template registry ────────────────────────────────────────────────────────

export interface WrkfWorkflowValidateParams {
  path: string;
}

export interface WrkfWorkflowValidateResult {
  valid: boolean;
  [k: string]: unknown;
}

export interface WrkfWorkflowShowParams {
  ref: string;
}

export interface WrkfWorkflowShowResult {
  template: Record<string, unknown>;
  hash: string;
}

export interface WrkfWorkflowListParams {
  [k: string]: unknown;
}

export interface WrkfWorkflowListResult {
  templates: WrkfWorkflowTemplateSummary[];
}

export interface WrkfWorkflowDiffParams {
  oldPath: string;
  newPath: string;
}

export interface WrkfDiffResult {
  old: WrkfWorkflowTemplateSummary;
  new: WrkfWorkflowTemplateSummary;
  sameHash: boolean;
  [k: string]: unknown;
}

export interface WrkfWorkflowInstallParams {
  path: string;
}

export interface WrkfInstallResult {
  id: string;
  version: string;
  hash: string;
  installed: boolean;
}

// ── Instance state access ────────────────────────────────────────────────────

export interface WrkfInstanceShowParams {
  instanceId?: string;
  task?: string;
}

export interface WrkfInstanceNextParams {
  instanceId?: string;
  task?: string;
  role?: string;
}

export interface WrkfNextResult {
  instance?: Record<string, unknown>;
  actions: Array<{ kind: string; [k: string]: unknown }>;
  blockedTransitions?: unknown[];
  openObligations?: unknown[];
  pendingEffects?: unknown[];
  [k: string]: unknown;
}

// ── Evidence ─────────────────────────────────────────────────────────────────

export interface WrkfEvidenceAddParams {
  task?: string;
  instanceId?: string;
  kind: string;
  ref?: string;
  summary?: string;
  facts?: Record<string, unknown>;
  data?: unknown;
  actor?: string;
  role?: string;
  /** Persisted run linkage; see docs/wrkq-wrkf-rpc.md §9.7. */
  runId?: string;
  idempotencyKey?: string;
}

export interface WrkfEvidenceListParams {
  task?: string;
  instanceId?: string;
}

export interface WrkfEvidenceShowParams {
  id: string;
}

export interface WrkfEvidenceSuggestParams {
  task?: string;
  instanceId?: string;
  transition: string;
}

export interface WrkfSuggestResult {
  transition: string;
  required: unknown[];
  missing: string[];
  checks: string[];
  warnings: string[];
  [k: string]: unknown;
}

// ── Obligations ──────────────────────────────────────────────────────────────

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

// ── Checks and hooks ─────────────────────────────────────────────────────────

export interface WrkfCheckPreflightParams {
  task?: string;
  instanceId?: string;
  transition: string;
}

export interface WrkfCheckRunParams {
  task?: string;
  instanceId?: string;
  transition: string;
}

export interface WrkfCheckRunResult {
  runs: WrkfCheckRun[];
  [k: string]: unknown;
}

export interface WrkfCheckShowParams {
  id: string;
}

export interface WrkfCheckListParams {
  task?: string;
  instanceId?: string;
  transition?: string;
}

export interface WrkfHookListParams {
  [k: string]: unknown;
}

export interface WrkfHookShowParams {
  id: string;
}

export interface WrkfHookRunParams {
  task?: string;
  instanceId?: string;
  transition: string;
  hookId: string;
}

// ── Transitions ──────────────────────────────────────────────────────────────

export interface WrkfTransitionApplyParams {
  task?: string;
  instanceId?: string;
  transition: string;
  role?: string;
  actor?: string;
  /** CAS precondition; see docs/wrkq-wrkf-rpc.md §8.3. */
  expectRevision?: number;
  /** CAS precondition; see docs/wrkq-wrkf-rpc.md §8.3. */
  contextHash?: string;
  idempotencyKey?: string;
  runChecks?: boolean;
  dryRun?: boolean;
}

export interface WrkfTransitionResult {
  task: string;
  instanceId: string;
  state: WrkfState;
  revision: number;
  contextHash: string;
  eventId: string;
  effects: WrkfEffect[];
  obligations: WrkfObligation[];
}

// ── Runs ─────────────────────────────────────────────────────────────────────

export interface WrkfRunStartParams {
  task?: string;
  instanceId?: string;
  role?: string;
  actor?: string;
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

// ── Effects ──────────────────────────────────────────────────────────────────

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
}

export interface WrkfEffectFailParams {
  effectId: string;
  leaseToken: string;
  reason: string;
  retryable?: boolean;
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
