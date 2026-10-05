/**
 * wrkq/listing.ts — legacy-shaped read views: ls, tree, history and monitor.
 *
 * Mirrors docs/wrkq-wrkf-rpc.md §6.2 and §7.
 */

import type { WrkqPromise } from "./promise.js";

/** Compatibility listing with caller-owned state selection. */
export interface WrkqLsListViewParams {
  path?: string;
  paths?: string[];
  subtasks?: boolean;
  sort?: "slug" | "updated_at" | "created_at" | "id";
  reverse?: boolean;
  limit?: number;
  cursor?: string;
  type?: "p" | "t";
  includeHidden?: boolean;
  includeCampaignMembers?: boolean;
  states: string | string[];
  lifecycle?: string;
  pruneEmpty?: boolean;
}
export interface WrkqLsEntry {
  type: "task" | "container";
  id: string;
  slug: string;
  title?: string;
  path: string;
  created_at: string;
  updated_at: string;
  state?: string;
  kind?: string;
  open_subtask_count: number;
  task_count?: number;
  active_task_count?: number;
  requested_by_project_id?: string;
  assigned_project_id?: string;
  acknowledged_at?: string;
  resolution?: string;
}
export interface WrkqLsListView {
  items: WrkqLsEntry[];
  next_cursor?: string;
}
export interface WrkqTreeViewParams {
  path?: string;
  maxDepth?: number;
  states: string | string[];
  lifecycle?: string;
  pruneEmpty?: boolean;
  includeCampaignMembers?: boolean;
  promiseState?: "open" | "all";
}
export interface WrkqTreeNode {
  type: "task" | "container";
  id: string;
  uuid: string;
  slug: string;
  title: string;
  state?: string;
  open_subtask_count: number;
  subtask_owner_id?: string;
  requested_by_project_id?: string;
  assigned_project_id?: string;
  acknowledged_at?: string;
  resolution?: string;
  is_archived: boolean;
  is_deleted: boolean;
  all_tasks_completed?: boolean;
  promises: WrkqPromise[];
  children?: WrkqTreeNode[];
  external_children?: WrkqTreeNode[];
  external_backlink?: boolean;
  external_project_id?: string;
  external_path?: string;
  wire_created_at?: string;
  wire_parent_task_uuid?: string;
}
export interface WrkqTreeView {
  path: string;
  project_id?: string;
  children: WrkqTreeNode[];
  promises: WrkqPromise[];
  hidden_containers_not_displayed: number;
  wire_raw_path?: string;
}

/** Task event reads include an owner's named subtasks; state conditions remain exact. */
export interface WrkqHistoryTailViewParams {
  cursor: number;
  limit?: number;
  tasks?: string[];
}

export interface WrkqHistoryEvent {
  id: number;
  timestamp: string;
  principal_ref?: string;
  scope_ref?: string;
  resource_type: string;
  resource_uuid?: string;
  resource_id?: string;
  task_id?: string;
  event_type: string;
  etag?: number;
  payload?: string;
}

export interface WrkqHistoryTailView {
  items: WrkqHistoryEvent[];
  high_water: number;
}

export interface WrkqHistoryListView {
  items: WrkqHistoryEvent[];
  next_cursor?: string;
}

export interface WrkqMonitorEvent {
  id: number;
  timestamp: string;
  resource_type: string;
  resource_uuid?: string;
  resource_id?: string;
  task_id?: string;
  event_type: string;
  payload?: string;
}
