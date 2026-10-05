/**
 * wrkf/action.ts — low-ceremony action composition over run/evidence/transition (wrkf.action.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.3 and §7.
 */

import type { WrkfEffect } from "./effect.js";
import type { WrkfEvidence } from "./evidence.js";
import type { WrkfInstance, WrkfState } from "./instance.js";
import type { WrkfObligation } from "./obligation.js";
import type { WrkfTransitionResult } from "./transition.js";

/** Workflow template ref backing an action run. */
export interface WrkfActionWorkflowRef {
  id: string;
  version: string;
  hash?: string;
}

/** Canonical semantic view of one action run. */
export interface WrkfActionRun {
  actionRunId: string;
  runId: string;
  task: string;
  instanceId: string;
  workflow: WrkfActionWorkflowRef;
  action: string;
  role: string;
  principal_ref?: string;
  lane?: string;
  deliveryRef?: string;
  externalRunRef?: string;
  status: string;
  startedAt: string;
  completedAt?: string;
  terminalResult?: string;
  leaseOwner?: string;
  leaseToken?: string;
  leaseExpiresAt?: string;
  heartbeatAt?: string;
  evidenceIds?: string[];
  evidenceKinds?: string[];
  transitionEventIds?: string[];
  [k: string]: unknown;
}

export interface WrkfActionEvidenceInput {
  kind?: string;
  ref?: string;
  summary?: string;
  facts?: Record<string, unknown>;
  data?: Record<string, unknown>;
  contentHash?: string;
  idempotencyKey?: string;
}

export interface WrkfActionNextScope {
  project?: string;
  path?: string;
  recursive?: boolean;
  templates?: string[];
}

export interface WrkfActionNextFilters {
  actions?: string[];
  roles?: string[];
  statuses?: string[];
  phases?: string[];
  includeBlocked?: boolean;
  includeActiveRuns?: boolean;
}

export interface WrkfActionNextParams {
  task?: string;
  instanceId?: string;
  scope?: WrkfActionNextScope;
  filters?: WrkfActionNextFilters;
  limit?: number;
}

export interface WrkfActionSourceBinding {
  sourceRunId: string;
  sourceEvidenceId?: string;
  /** Opaque, lane-computed source identity. Consumers must not parse it. */
  sourceIdentity?: string;
  artifactRef?: string;
}

export interface WrkfActionCandidate {
  instanceId: string;
  task: string;
  semanticActionKey: string;
  action: string;
  transition: string;
  role: string;
  requiredEvidenceKind: string;
  expectedStateRevision: number;
  expectedState: WrkfState;
  expectedTaskDocHash?: string;
  inputHash?: string;
  source?: WrkfActionSourceBinding;
  handlerContract?: string;
  workspaceMode?: "none" | "read-only" | "exclusive" | (string & {});
  workspaceRef?: string;
  sideEffectClasses?: string[];
  rank: number;
  blocked?: boolean;
  blockedReason?: string;
  [k: string]: unknown;
}

export interface WrkfActionNextResult {
  candidates: WrkfActionCandidate[];
  nextCursor?: string;
}

export interface WrkfActionClaimPrefer {
  instanceId?: string;
  semanticActionKey?: string;
  action?: string;
}

export interface WrkfRunnerCapability {
  handlerContract?: string;
  actions?: string[];
  roles?: string[];
  sideEffectClasses?: string[];
  workspaceModes?: string[];
}

export interface WrkfActionClaimParams {
  task?: string;
  instanceId?: string;
  prefer?: WrkfActionClaimPrefer;
  runnerId: string;
  agentRef: string;
  scopeRef?: string;
  capabilities?: WrkfRunnerCapability[];
  leaseMs: number;
  workspaceRoot?: string;
  idempotencyKey?: string;
  /** Latest acknowledged predecessor, or null for a first-ever claim. Suspended instances refuse before succession evaluation. */
  priorRun: string | null;
}

export interface WrkfWorkflowRunAttempt {
  id: string;
  instanceId: string;
  semanticActionKey: string;
  action: string;
  role: string;
  attempt: number;
  status: string;
  agentRef?: string;
  scopeRef?: string;
  handlerContract?: string;
  externalRunRef?: string;
  workspaceRef?: string;
  source?: WrkfActionSourceBinding;
  startedAt: string;
  completedAt?: string;
  terminalSummary?: string;
  predecessorRunId?: string;
  [k: string]: unknown;
}

export interface WrkfActionTaskBinding {
  uuid: string;
  ref: string;
  path?: string;
}

export interface WrkfActionRunAuthority {
  runnerId: string;
  ownerToken: string;
  ownerGeneration: number;
  leaseExpiresAt: string;
  claimedAt: string;
  heartbeatAt?: string;
}

export interface WrkfActionClaimEvidenceRecord {
  id: string;
  kind: string;
  ref: string;
  summary?: string;
  producedAt: string;
}

export interface WrkfActionClaimPredecessor {
  runId: string;
  owner?: string;
  claimedAt: string;
  heartbeatAt?: string;
  expiresAt?: string;
  settleStatus: string;
  /** Producer-owned terminal-status classification; consumers must not derive this from settleStatus. */
  settled: boolean;
  terminalResult?: string;
  sideEffectClasses: string[];
  externalRunRef?: string;
  workspaceRef?: string;
  evidenceWritten: WrkfActionClaimEvidenceRecord[];
}

export interface WrkfFencedRunBinding {
  run: WrkfWorkflowRunAttempt;
  task: WrkfActionTaskBinding;
  instance: WrkfInstance;
  authority: WrkfActionRunAuthority;
}

export interface WrkfActionClaimResult {
  binding?: WrkfFencedRunBinding;
}

export interface WrkfActionSettleParams {
  actionRunId?: string;
  runId?: string;
  ownerToken?: string;
  ownerGeneration?: number;
  result:
    | "completed"
    | "semantic_blocked"
    | "operational_failed"
    | "operator_required"
    | "cancelled"
    | (string & {});
  evidence?: WrkfActionEvidenceInput;
  /** Transition id to apply, or false to skip; omit for executable action transition. */
  transition?: string | false;
  terminalSummary?: string;
}

export interface WrkfActionSettleResult {
  run: WrkfWorkflowRunAttempt;
  evidence?: WrkfEvidence;
  transition?: WrkfTransitionResult;
  effects?: WrkfEffect[];
  obligations?: WrkfObligation[];
}

export interface WrkfActionStartParams {
  task?: string;
  instanceId?: string;
  /** Defaults to the producer-owned built-in wrkq-simple-task@5. */
  workflow?: string;
  action: "triage" | "implement" | "review" | "verify" | (string & {});
  role?: string;
  principal_ref?: string;
  lane?: string;
  deliveryRef?: string | Record<string, unknown>;
  externalRunRef?: string;
  idempotencyKey?: string;
  leaseOwner?: string;
  leaseMs?: number;
}

export interface WrkfActionBindExternalParams {
  actionRunId: string;
  externalRunRef: string;
  deliveryRef?: string | Record<string, unknown>;
  lane?: string;
  idempotencyKey?: string;
}

export interface WrkfActionCompleteParams {
  actionRunId: string;
  leaseToken?: string;
  evidence?: WrkfActionEvidenceInput;
  /** Transition id to apply, or false to skip; omit for default resolution. */
  transition?: string | false;
  transitionIdempotencyKey?: string;
  runSummary?: string;
}

export interface WrkfActionCompleteResult {
  run: WrkfActionRun;
  evidence?: WrkfEvidence;
  transition?: WrkfTransitionResult;
}

export interface WrkfActionFailParams {
  actionRunId: string;
  leaseToken?: string;
  summary: string;
  evidence?: WrkfActionEvidenceInput;
}

export interface WrkfActionHeartbeatParams {
  actionRunId: string;
  leaseToken: string;
  leaseMs?: number;
}

export interface WrkfActionShowParams {
  actionRunId: string;
}

export interface WrkfActionListParams {
  task?: string;
  instanceId?: string;
  includeClosedInstances?: boolean;
  status?: string;
  action?: string;
  limit?: number;
  cursor?: string;
}

export interface WrkfActionListResult {
  items: WrkfActionRun[];
}
