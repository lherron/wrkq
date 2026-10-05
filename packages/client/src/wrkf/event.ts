/**
 * wrkf/event.ts — workflow events, the event replay query and bounded watch (wrkf.event.*, wrkf.watch.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.3 and §7.
 */

import type {
  WrkfInstance,
  WrkfState,
  WrkfSuspension,
  WrkfSuspensionDisposition,
} from "./instance.js";
import type { WrkfRoleBinding } from "./role.js";
import type { WrkfRun } from "./run.js";

export type WrkfQueryableEventType =
  | "workflow.transitioned"
  | "workflow.suspended"
  | "workflow.suspension_resolved"
  | "workflow.instance_cancelled";

export interface WrkfEvent {
  id: string;
  type?: string;
  payload?: {
    from?: WrkfState;
    to?: WrkfState;
    transition?: string;
    outcome?: string;
    suspension?: WrkfSuspension;
    disposition?: WrkfSuspensionDisposition;
    beforeRevision?: number;
    afterRevision?: number;
    [k: string]: unknown;
  };
  [k: string]: unknown;
}

export interface WrkfEventQueryParams {
  eventType?: WrkfQueryableEventType;
  project?: string;
  fromPhase?: string;
  toPhase?: string;
  riskClass?: string;
  riskClasses?: string[];
  excludeRiskClass?: string;
  excludeRiskClasses?: string[];
  boundRole?: string;
  includeRoleBindings?: boolean;
  limit?: number;
  cursor?: string;
}

export interface WrkfQueriedEventTask {
  uuid: string;
  id: string;
  slug?: string;
  ref?: string;
  projectUuid?: string;
  projectId?: string;
  projectSlug?: string;
  riskClass?: string;
}

export interface WrkfQueriedEvent {
  id: string;
  eventType: WrkfQueryableEventType;
  instanceId: string;
  seq: number;
  task: WrkfQueriedEventTask;
  transition?: string;
  outcome?: string;
  from?: WrkfState;
  to?: WrkfState;
  fromPhase?: string;
  toPhase?: string;
  occurredAt: string;
  principal_ref?: string;
  role?: string;
  suspension?: WrkfSuspension;
  disposition?: WrkfSuspensionDisposition;
  beforeRevision: number;
  afterRevision: number;
  matchingRoleBindings: WrkfRoleBinding[];
  roleBindings?: WrkfRoleBinding[];
  payload?: {
    from?: WrkfState;
    to?: WrkfState;
    transition?: string;
    outcome?: string;
    suspension?: WrkfSuspension;
    disposition?: WrkfSuspensionDisposition;
    beforeRevision?: number;
    afterRevision?: number;
    [k: string]: unknown;
  };
  [k: string]: unknown;
}

export interface WrkfEventQueryResult {
  items: WrkfQueriedEvent[];
  nextCursor?: string;
  hasMore: boolean;
}

export interface WrkfWatchTarget {
  kind: "task" | "instance" | "run" | string;
  selector: string;
  instanceId?: string;
  runId?: string;
  taskRef?: string;
}

export interface WrkfWatchSnapshotParams {
  selector: string;
  until?: "terminal" | "closed" | "waiting" | "suspended" | string;
}

export interface WrkfWatchSnapshot {
  target: WrkfWatchTarget;
  until: string;
  met: boolean;
  class: string;
  exitCode: number;
  status?: string;
  phase?: string;
  outcome?: string;
  instance?: WrkfInstance;
  run?: WrkfRun;
}

export interface WrkfWatchEventsParams {
  selector: string;
  afterCursor?: string;
  limit?: number;
}

export interface WrkfWatchEventsResult {
  events: WrkfEvent[];
  nextCursor: string;
}
