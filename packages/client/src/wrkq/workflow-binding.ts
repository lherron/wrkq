/**
 * wrkq/workflow-binding.ts — task-workflow binding DTOs (wrkq.workflow.*).
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7.
 */

import type { WrkfEvent } from "../wrkf/event.js";
import type { WrkfInstance } from "../wrkf/instance.js";
import type { WrkqTask } from "./task.js";

export interface WrkqWorkflowAttachParams {
  task: string;
  /** Template ref, e.g. "code_change@1". */
  workflow: string;
  supersede?: boolean;
  predecessorInstanceId?: string;
  predecessorRevision?: number;
  attachDiscontinued?: boolean;
  actor?: string;
  idempotencyKey?: string;
}

export interface WrkqWorkflowAttachResult {
  task: WrkqTask;
  instance: WrkfInstance;
  /** true = newly attached, false = already existed. */
  attached: boolean;
}

export interface WrkqWorkflowInspectParams {
  task: string;
}

export interface WrkqWorkflowInspectResult {
  instance: WrkfInstance;
  [k: string]: unknown;
}

export interface WrkqWorkflowInstancesParams {
  task: string;
}

export interface WrkqWorkflowInstancesResult {
  instances: WrkfInstance[];
  [k: string]: unknown;
}

export interface WrkqWorkflowTimelineParams {
  task: string;
}

export interface WrkqWorkflowTimelineResult {
  events: WrkfEvent[];
  [k: string]: unknown;
}

export interface WrkqWorkflowRefreshParams {
  task: string;
  idempotencyKey?: string;
}

export interface WrkqWorkflowSyncMetaParams {
  task?: string;
  actor?: string;
}

export interface WrkqWorkflowSyncMetaResult {
  synced: number;
}
