/**
 * wrkf/transition.ts — transition apply (wrkf.transition.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.3 and §7.
 */

import type { WrkfEffect } from "./effect.js";
import type { WrkfState } from "./instance.js";
import type { WrkfObligation } from "./obligation.js";

export interface WrkfTransitionApplyParams {
  task?: string;
  instanceId?: string;
  transition: string;
  role?: string;
  principal_ref?: string;
  /** CAS precondition; see docs/wrkq-wrkf-rpc.md §8.3. */
  expectRevision?: number;
  idempotencyKey?: string;
  /** Persisted run ids returned by check.run; input hashes are revalidated at commit. */
  checkIds?: string[];
  dryRun?: boolean;
}

export interface WrkfTransitionResult {
  task: string;
  instanceId: string;
  state: WrkfState;
  revision: number;
  eventId: string;
  effects: WrkfEffect[];
  obligations: WrkfObligation[];
}
