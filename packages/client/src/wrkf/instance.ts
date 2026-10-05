/**
 * wrkf/instance.ts — workflow instance state, suspension, next action, cancel and suspension resolution.
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.3 and §7.
 */

import type { WrkfEffect } from "./effect.js";

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

export interface WrkfSuspension {
  id: string;
  reason: string;
  at: string;
  causeRef?: string;
}

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
  taskDocEtag?: string;
  taskDocHash?: string;
  createdAt?: string;
  updatedAt?: string;
  suspension: WrkfSuspension | null;
  [k: string]: unknown;
}

export interface WrkfTerminalizedRunSummary {
  runId: string;
  status: string;
  completedAt: string;
  terminalResult: string;
}

export interface WrkfInstanceCancelParams {
  task?: string;
  instanceId?: string;
  expectRevision?: number;
  explanation?: string;
  principal_ref?: string;
  role?: string;
}

export interface WrkfInstanceCancelResult {
  task: string;
  instanceId: string;
  state: WrkfState;
  revision: number;
  eventId: string;
  effects: WrkfEffect[];
  terminalizedRuns: WrkfTerminalizedRunSummary[];
  instance?: WrkfInstance;
}

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

/** Dispositions accepted by `wrkf.suspension.resolve`. */
export type WrkfSuspensionDisposition = "resume" | "close" | "cancel";

/**
 * `wrkf.suspension.resolve` input. The matching `suspensionId` is the ONLY gate:
 * no role checks, no evidence validation. `explanation` is recorded free text;
 * `expectRevision` is the ordinary CAS precondition (docs/wrkq-wrkf-rpc.md §8.3).
 */
export interface WrkfSuspensionResolveParams {
  suspensionId: string;
  disposition: WrkfSuspensionDisposition;
  explanation?: string;
  expectRevision?: number;
  role?: string;
  principal_ref?: string;
}

export interface WrkfSuspensionResolveResult {
  task?: string;
  instanceId: string;
  suspensionId: string;
  disposition: WrkfSuspensionDisposition;
  state: WrkfState;
  revision: number;
  eventId: string;
  effects: WrkfEffect[];
  terminalizedRuns?: WrkfTerminalizedRunSummary[];
  instance?: WrkfInstance;
}
